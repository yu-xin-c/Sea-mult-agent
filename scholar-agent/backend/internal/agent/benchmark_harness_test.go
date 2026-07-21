package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/sandbox"
)

type scriptedBenchmarkSandbox struct {
	workspace  string
	failFirst  bool
	mutateData bool
	calls      int
	commands   []string
}

func (s *scriptedBenchmarkSandbox) ExecCommandStream(_ context.Context, sandboxID string, cmd []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	s.calls++
	command := strings.Join(cmd, " ")
	s.commands = append(s.commands, sandboxID+" "+command)
	if onChunk != nil {
		onChunk("stdout", "benchmark fixture")
	}
	if s.failFirst && s.calls == 1 {
		return &sandbox.PythonRunResponse{ExitCode: 1, Stderr: "ImportError: fixture entrypoint mismatch"}, nil
	}

	mode := "preflight"
	if strings.Contains(command, ".scholar/benchmark/run") {
		mode = "run"
	}
	outputDirectory := filepath.Join(s.workspace, ".scholar", "benchmark", mode)
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		return nil, err
	}
	datasetPath := filepath.Join(s.workspace, ".scholar", "uploads", "01-reviews.csv")
	checksum, err := sha256File(datasetPath)
	if err != nil {
		return nil, err
	}
	metrics := []byte(`{"accuracy":0.5,"macro_f1":0.3333333333333333}`)
	runManifest, _ := json.Marshal(models.BenchmarkRunManifest{
		Status: "ok", DatasetSHA256: checksum, SampleCount: 2, Seed: 17, Adapter: "fixture",
	})
	predictions := []byte("{\"index\":0,\"prediction\":\"positive\",\"target\":\"positive\"}\n{\"index\":1,\"prediction\":\"positive\",\"target\":\"negative\"}\n")
	for name, content := range map[string][]byte{
		"metrics.json": metrics, "run_manifest.json": runManifest, "predictions.jsonl": predictions,
	} {
		if err := os.WriteFile(filepath.Join(outputDirectory, name), content, 0o600); err != nil {
			return nil, err
		}
	}
	if s.mutateData {
		if err := os.WriteFile(datasetPath, []byte("tampered\n"), 0o600); err != nil {
			return nil, err
		}
	}
	return &sandbox.PythonRunResponse{ExitCode: 0, Stdout: `{"status":"ok"}`}, nil
}

func benchmarkHarnessFixture(t *testing.T) (string, models.DatasetManifest, models.BenchmarkAdapterSpec, string) {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("fixture repository"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploadDirectory := filepath.Join(workspace, ".scholar", "uploads")
	if err := os.MkdirAll(uploadDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	datasetPath := filepath.Join(uploadDirectory, "01-reviews.csv")
	if err := os.WriteFile(datasetPath, []byte("review,label\none,positive\ntwo,negative\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checksum, err := sha256File(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := models.DatasetManifest{
		Version: "benchmark.dataset/v1", Name: "reviews.csv", Format: "csv", SHA256: checksum, RowCount: 2,
		InputColumn: "review", TargetColumn: "label", SuggestedTask: "classification",
	}
	code := benchmarkTestAdapterCode("before repair")
	codeHash := sha256.Sum256([]byte(code))
	spec := models.BenchmarkAdapterSpec{
		Version: "benchmark.adapter/v1", Status: "generated", Strategy: "native_eval", EntryPoint: "evaluate.py:predict",
		DatasetSHA256: checksum, InputColumn: "review", TargetColumn: "label", Metrics: []string{"accuracy", "macro_f1"},
		AdapterCodeSHA256: hex.EncodeToString(codeHash[:]),
	}
	return workspace, manifest, spec, code
}

func TestBenchmarkHarnessRepairsFailedPreflightThenExecutesAndValidates(t *testing.T) {
	workspace, manifest, spec, originalCode := benchmarkHarnessFixture(t)
	manifestJSON, _ := json.Marshal(manifest)
	specJSON, _ := json.Marshal(spec)
	repairedCode := benchmarkTestAdapterCode("after repair")
	repairPayload, _ := json.Marshal(map[string]any{
		"adapter_code": repairedCode,
		"reason":       "use the repository's documented import path",
	})
	fakeSandbox := &scriptedBenchmarkSandbox{workspace: workspace, failFirst: true}
	agent := &ResearchCodingAgent{
		Name:      "research_coding_agent",
		ChatModel: newSequentialBenchmarkModel(t, string(repairPayload)),
		Sandbox:   fakeSandbox,
	}
	preflight := &models.Task{
		Type: "benchmark_adapter_preflight",
		Inputs: map[string]any{
			"workspace_path": workspace, "dataset_manifest": string(manifestJSON),
			"benchmark_adapter_spec": string(specJSON), "benchmark_generated_code": originalCode,
			"prepared_runtime": "dk-fixture", "benchmark_max_preflight_attempts": 3,
		},
	}
	if err := agent.ExecuteTask(context.Background(), preflight, nil); err != nil {
		t.Fatal(err)
	}
	if fakeSandbox.calls != 2 || preflight.Code != repairedCode {
		t.Fatalf("preflight calls=%d repaired=%t", fakeSandbox.calls, preflight.Code == repairedCode)
	}
	preflightArtifacts := preflight.Metadata["artifact_values"].(map[string]string)
	var validatedSpec models.BenchmarkAdapterSpec
	if err := json.Unmarshal([]byte(preflightArtifacts["validated_benchmark_adapter_spec"]), &validatedSpec); err != nil {
		t.Fatal(err)
	}
	if validatedSpec.Status != "preflight_passed" || validatedSpec.RepairAttempts != 1 {
		t.Fatalf("unexpected validated spec: %#v", validatedSpec)
	}

	runTask := &models.Task{
		Type: "benchmark_execute",
		Inputs: map[string]any{
			"workspace_path": workspace, "dataset_manifest": string(manifestJSON),
			"validated_benchmark_adapter_spec":   preflightArtifacts["validated_benchmark_adapter_spec"],
			"validated_benchmark_generated_code": repairedCode, "prepared_runtime": "dk-fixture",
			"benchmark_max_samples": 2,
		},
	}
	if err := agent.ExecuteTask(context.Background(), runTask, nil); err != nil {
		t.Fatal(err)
	}
	runArtifacts := runTask.Metadata["artifact_values"].(map[string]string)
	if runArtifacts["benchmark_run_metrics"] == "" || !strings.HasSuffix(runArtifacts["benchmark_predictions_path"], "run/predictions.jsonl") {
		t.Fatalf("missing formal run artifacts: %#v", runArtifacts)
	}

	validationTask := &models.Task{
		Type: "benchmark_validate",
		Inputs: map[string]any{
			"dataset_manifest":       string(manifestJSON),
			"benchmark_run_metrics":  runArtifacts["benchmark_run_metrics"],
			"benchmark_run_manifest": runArtifacts["benchmark_run_manifest"],
		},
	}
	if err := agent.ExecuteTask(context.Background(), validationTask, nil); err != nil {
		t.Fatal(err)
	}
	if validationTask.Status != models.StatusCompleted || !strings.Contains(validationTask.Result, `"status":"validated"`) {
		t.Fatalf("unexpected validation result: %s", validationTask.Result)
	}
	if len(fakeSandbox.commands) != 3 || !strings.Contains(fakeSandbox.commands[0], "--limit 8") || !strings.Contains(fakeSandbox.commands[2], "--limit 2") {
		t.Fatalf("unexpected harness commands: %#v", fakeSandbox.commands)
	}
}

func TestValidateBenchmarkOutputRejectsMetricPredictionMismatch(t *testing.T) {
	workspace, manifest, _, _ := benchmarkHarnessFixture(t)
	outputDirectory := filepath.Join(workspace, ".scholar", "benchmark", "run")
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runManifest, _ := json.Marshal(models.BenchmarkRunManifest{Status: "ok", DatasetSHA256: manifest.SHA256, SampleCount: 1})
	files := map[string][]byte{
		"metrics.json":      []byte(`{"accuracy":1}`),
		"run_manifest.json": runManifest,
		"predictions.jsonl": []byte("{\"prediction\":\"wrong\",\"target\":\"right\"}\n"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(outputDirectory, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := validateBenchmarkOutputDirectory(workspace, ".scholar/benchmark/run", manifest, 1, "run"); err == nil || !strings.Contains(err.Error(), "does not match predictions") {
		t.Fatalf("expected metric mismatch, got %v", err)
	}
}

func TestBenchmarkHarnessRejectsDatasetMutation(t *testing.T) {
	workspace, manifest, spec, code := benchmarkHarnessFixture(t)
	manifestJSON, _ := json.Marshal(manifest)
	specJSON, _ := json.Marshal(spec)
	agent := &ResearchCodingAgent{
		Name:    "research_coding_agent",
		Sandbox: &scriptedBenchmarkSandbox{workspace: workspace, mutateData: true},
	}
	task := &models.Task{
		Type: "benchmark_adapter_preflight",
		Inputs: map[string]any{
			"workspace_path": workspace, "dataset_manifest": string(manifestJSON),
			"benchmark_adapter_spec": string(specJSON), "benchmark_generated_code": code,
			"prepared_runtime": "dk-fixture", "benchmark_max_preflight_attempts": 1,
		},
	}
	err := agent.ExecuteTask(context.Background(), task, nil)
	if err == nil || !strings.Contains(err.Error(), "preflight failed") || !strings.Contains(task.Result, "dataset changed") {
		t.Fatalf("expected dataset mutation rejection, err=%v result=%s", err, task.Result)
	}
}
