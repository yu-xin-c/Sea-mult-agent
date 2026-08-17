package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestBenchmarkAgentBuildsLeakageSafeContractAndRecomputesHiddenMetrics(t *testing.T) {
	isolationRoot := t.TempDir()
	t.Setenv("BENCHMARK_PRIVATE_ROOT", filepath.Join(isolationRoot, "backend-private"))
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", filepath.Join(isolationRoot, "sandbox-workspaces"))
	datasetPath := filepath.Join("..", "..", "..", "examples", "benchmark-agent", "classification", "reviews.csv")
	checksum, err := sha256File(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	uploads := []map[string]any{{"name": "reviews.csv", "storage_path": datasetPath, "sha256": checksum}}
	agent := NewBenchmarkAgent()

	auditTask := &models.Task{Type: "benchmark_dataset_audit", Inputs: map[string]any{
		"uploaded_files": uploads, "benchmark_input_column": "review", "benchmark_target_column": "label",
		"benchmark_task_type": "classification",
	}}
	if err := agent.ExecuteTask(context.Background(), auditTask, nil); err != nil {
		t.Fatal(err)
	}
	auditArtifacts := auditTask.Metadata["artifact_values"].(map[string]string)
	var audit models.BenchmarkDatasetAudit
	if err := json.Unmarshal([]byte(auditArtifacts["benchmark_dataset_audit"]), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.TaskType != "classification" || audit.Confidence != 1 || len(audit.BlockingIssues) != 0 {
		t.Fatalf("unexpected audit: %#v", audit)
	}

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("fixture repository"), 0o600); err != nil {
		t.Fatal(err)
	}
	splitTask := &models.Task{Type: "benchmark_split_materialize", Inputs: map[string]any{
		"workspace_path": workspace, "uploaded_files": uploads,
		"dataset_manifest": auditArtifacts["dataset_manifest"], "benchmark_dataset_audit": auditArtifacts["benchmark_dataset_audit"],
	}}
	if err := agent.ExecuteTask(context.Background(), splitTask, nil); err != nil {
		t.Fatal(err)
	}
	splitArtifacts := splitTask.Metadata["artifact_values"].(map[string]string)
	var split models.BenchmarkSplitManifest
	if err := json.Unmarshal([]byte(splitArtifacts["benchmark_split_manifest"]), &split); err != nil {
		t.Fatal(err)
	}
	if split.Method != "stratified_hash" || split.Splits["train"].RowCount == 0 || split.Splits["validation"].RowCount == 0 || split.Splits["test"].RowCount == 0 {
		t.Fatalf("unexpected split: %#v", split)
	}
	if split.PreflightFeatures == nil || split.PreflightFeatures.RowCount == 0 || split.PreflightFeatures.TargetPublic {
		t.Fatalf("unlabeled preflight split is incomplete: %#v", split.PreflightFeatures)
	}
	var leakage models.BenchmarkLeakageReport
	if err := json.Unmarshal([]byte(splitArtifacts["benchmark_leakage_report"]), &leakage); err != nil {
		t.Fatal(err)
	}
	if leakage.Status != "passed" || leakage.CrossSplitInputOverlaps != 0 {
		t.Fatalf("unexpected leakage report: %#v", leakage)
	}
	testFeaturesPath := filepath.Join(workspace, filepath.FromSlash(split.Splits["test"].RelativePath))
	testFeatures, err := os.ReadFile(testFeaturesPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(testFeatures), `"label"`) {
		t.Fatal("public test features exposed the target column")
	}
	preflightFeaturesPath := filepath.Join(workspace, filepath.FromSlash(split.PreflightFeatures.RelativePath))
	preflightFeatures, err := os.ReadFile(preflightFeaturesPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(preflightFeatures), `"label"`) {
		t.Fatal("input-only preflight features exposed the target column")
	}
	var preflightManifest models.DatasetManifest
	if err := json.Unmarshal([]byte(splitArtifacts["benchmark_input_only_preflight_manifest"]), &preflightManifest); err != nil {
		t.Fatal(err)
	}
	if preflightManifest.TargetColumn != "" || preflightManifest.SHA256 != split.PreflightFeatures.SHA256 {
		t.Fatalf("unexpected input-only preflight manifest: %#v", preflightManifest)
	}
	if err := filepath.Walk(workspace, func(path string, _ os.FileInfo, walkErr error) error {
		if walkErr == nil && strings.Contains(filepath.Base(path), "test_labels") {
			t.Fatalf("hidden labels leaked into repository workspace: %s", path)
		}
		return walkErr
	}); err != nil {
		t.Fatal(err)
	}

	freezeTask := &models.Task{Type: "benchmark_contract_freeze", Inputs: map[string]any{
		"workspace_path": workspace, "benchmark_dataset_audit": auditArtifacts["benchmark_dataset_audit"],
		"benchmark_split_manifest": splitArtifacts["benchmark_split_manifest"], "benchmark_target_score": 0.9,
	}}
	if err := agent.ExecuteTask(context.Background(), freezeTask, nil); err != nil {
		t.Fatal(err)
	}
	contractArtifacts := freezeTask.Metadata["artifact_values"].(map[string]string)
	var contract models.BenchmarkContract
	if err := json.Unmarshal([]byte(contractArtifacts["benchmark_contract"]), &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Metric.PrimaryMetric != "macro_f1" || contract.Metric.Direction != "maximize" || contract.Reward.Usage != "candidate_priority_only" {
		t.Fatalf("unexpected benchmark contract: %#v", contract)
	}

	privateDirectory, err := benchmarkPrivateStateDirectory(split.PrivateStateID, false)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := readBenchmarkHiddenLabels(filepath.Join(privateDirectory, "test_labels.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	hiddenOutput := filepath.Join(workspace, ".scholar", "benchmark", "hidden")
	if err := os.MkdirAll(hiddenOutput, 0o700); err != nil {
		t.Fatal(err)
	}
	predictionsPath := filepath.Join(hiddenOutput, "predictions.jsonl")
	if err := writePerfectHiddenPredictions(testFeaturesPath, predictionsPath, labels); err != nil {
		t.Fatal(err)
	}
	hiddenRun, _ := json.Marshal(models.BenchmarkRunManifest{
		Status: "ok", DatasetSHA256: split.Splits["test"].SHA256, SampleCount: split.Splits["test"].RowCount,
	})
	validationTask := &models.Task{Type: "benchmark_validate", Inputs: map[string]any{
		"workspace_path": workspace, "benchmark_contract": contractArtifacts["benchmark_contract"],
		"benchmark_split_manifest":          splitArtifacts["benchmark_split_manifest"],
		"benchmark_run_metrics":             `{"accuracy":0.75,"macro_f1":0.74}`,
		"benchmark_hidden_predictions_path": ".scholar/benchmark/hidden/predictions.jsonl",
		"benchmark_hidden_run_manifest":     string(hiddenRun),
	}}
	if err := agent.ExecuteTask(context.Background(), validationTask, nil); err != nil {
		t.Fatal(err)
	}
	var report models.BenchmarkValidationReport
	if err := json.Unmarshal([]byte(validationTask.Result), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "validated" || report.HiddenMetrics["macro_f1"] != 1 || report.HiddenMetrics["accuracy"] != 1 || !report.TargetReached || !report.ProtectedFilesValid {
		t.Fatalf("unexpected hidden validation report: %#v", report)
	}
}

func writePerfectHiddenPredictions(featuresPath, predictionsPath string, labels map[string]string) error {
	input, err := os.Open(featuresPath)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(predictionsPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			_ = output.Close()
			return err
		}
		id := row["__benchmark_id"].(string)
		if err := encoder.Encode(map[string]any{"id": id, "prediction": labels[id]}); err != nil {
			_ = output.Close()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func TestBuildBenchmarkMetricContractUsesTaskSpecificDirection(t *testing.T) {
	regression, err := buildBenchmarkMetricContract(&models.Task{}, "regression")
	if err != nil {
		t.Fatal(err)
	}
	if regression.PrimaryMetric != "mae" || regression.Direction != "minimize" {
		t.Fatalf("unexpected regression contract: %#v", regression)
	}
	classification, err := buildBenchmarkMetricContract(&models.Task{}, "classification")
	if err != nil {
		t.Fatal(err)
	}
	if classification.PrimaryMetric != "macro_f1" || classification.Direction != "maximize" {
		t.Fatalf("unexpected classification contract: %#v", classification)
	}
}

func TestBenchmarkPrivateRootRejectsSandboxWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", workspaceRoot)
	t.Setenv("BENCHMARK_PRIVATE_ROOT", filepath.Join(workspaceRoot, "private-benchmarks"))
	if _, err := benchmarkPrivateStateDirectory("split-isolation-test", true); err == nil || !strings.Contains(err.Error(), "isolated") {
		t.Fatalf("expected private root isolation error, got %v", err)
	}
}
