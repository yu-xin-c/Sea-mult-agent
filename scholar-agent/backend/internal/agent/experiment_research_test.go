package agent

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/sandbox"
)

type scriptedExperimentSandbox struct {
	workspace string
	calls     int
	failAfter int
}

type localExperimentSandbox struct {
	workspace string
	python    string
}

func (s *localExperimentSandbox) ExecCommandStream(ctx context.Context, _ string, command []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty experiment command")
	}
	localCommand := append([]string(nil), command...)
	localCommand[0] = s.python
	for index := 1; index < len(localCommand); index++ {
		if strings.HasPrefix(localCommand[index], "/workspace/") {
			localCommand[index] = filepath.Join(s.workspace, filepath.FromSlash(strings.TrimPrefix(localCommand[index], "/workspace/")))
		}
	}
	process := exec.CommandContext(ctx, localCommand[0], localCommand[1:]...)
	stdout, stderr := strings.Builder{}, strings.Builder{}
	process.Stdout, process.Stderr = &stdout, &stderr
	err := process.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}
	if onChunk != nil && strings.TrimSpace(stdout.String()) != "" {
		onChunk("stdout", strings.TrimSpace(stdout.String()))
	}
	return &sandbox.PythonRunResponse{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

func (s *scriptedExperimentSandbox) ExecCommandStream(_ context.Context, _ string, command []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	s.calls++
	if s.failAfter > 0 && s.calls >= s.failAfter {
		return &sandbox.PythonRunResponse{ExitCode: 1, Stderr: "fixture evaluator failure"}, nil
	}
	configPath, queryPath := "", ""
	for index, item := range command {
		if item == "--config" && index+1 < len(command) {
			configPath = strings.Replace(command[index+1], "/workspace/", s.workspace+string(filepath.Separator), 1)
		}
		if item == "--queries" && index+1 < len(command) {
			queryPath = strings.Replace(command[index+1], "/workspace/", s.workspace+string(filepath.Separator), 1)
		}
	}
	var candidate models.ExperimentCandidate
	raw, err := os.ReadFile(configPath)
	if err != nil || json.Unmarshal(raw, &candidate) != nil {
		return nil, fmt.Errorf("read candidate config: %w", err)
	}
	score := map[string]float64{"bm25": 0.40, "tfidf": 0.55, "hybrid_rrf": 0.80, "graph_hybrid": 0.70}[candidate.Strategy]
	if strings.Contains(queryPath, "holdout_queries") {
		score = map[string]float64{"bm25": 0.45, "tfidf": 0.60, "hybrid_rrf": 0.82, "graph_hybrid": 0.72}[candidate.Strategy]
	}
	corpusPath := filepath.Join(s.workspace, filepath.FromSlash(retrievalCorpusPath))
	corpusHash, _ := sha256File(corpusPath)
	queryHash, _ := sha256File(queryPath)
	evaluation := models.ExperimentEvaluation{
		Version: models.ExperimentEvaluationVersion, CandidateID: candidate.ID,
		Strategy: candidate.Strategy, Parameters: candidate.Parameters,
		Metrics:   map[string]float64{"ndcg_at_k": score, "mrr": score, "recall_at_k": score},
		CaseCount: 2, AssetHashes: map[string]string{"corpus": corpusHash, "queries": queryHash},
		Evidence: []models.ExperimentCaseEvidence{{CaseID: "q-fixture", Expected: []string{"d1"}, Observed: []string{"d1"}, Metrics: map[string]float64{"ndcg": score}}},
	}
	payload, _ := json.Marshal(evaluation)
	if onChunk != nil {
		onChunk("stdout", string(payload))
	}
	return &sandbox.PythonRunResponse{ExitCode: 0, Stdout: string(payload)}, nil
}

func TestDatasetExperimentRunsStrategySearchAndHoldoutValidation(t *testing.T) {
	corpus := filepath.Join(t.TempDir(), "corpus.csv")
	queries := filepath.Join(t.TempDir(), "queries.csv")
	writeExperimentCSV(t, corpus, []string{"doc_id", "text", "links"}, [][]string{
		{"d1", "reset an industrial pump alarm", "d2"},
		{"d2", "inspect pump pressure and valve state", "d1"},
		{"d3", "rotate database credentials", ""},
	})
	writeExperimentCSV(t, queries, []string{"query_id", "query", "relevant_doc_ids", "split"}, [][]string{
		{"q1", "pump alarm reset", "d1", "search"},
		{"q2", "pump pressure", "d2", "search"},
		{"q3", "credential rotation", "d3", "holdout"},
		{"q4", "valve inspection", "d2", "holdout"},
	})
	uploads := []map[string]any{
		{"name": "corpus.csv", "storage_path": corpus},
		{"name": "queries.csv", "storage_path": queries},
	}
	prepare := &models.Task{
		Type: "experiment_dataset_prepare", Description: "对上传数据做 RAG 策略自动研究",
		Inputs: map[string]any{"uploaded_files": uploads, "research_domain": "retrieval", "experiment_adapter": "retrieval.v1"},
	}
	agent := &ResearchCodingAgent{Name: "research_coding_agent"}
	if err := agent.ExecuteTask(t.Context(), prepare, nil); err != nil {
		t.Fatal(err)
	}
	prepareArtifacts := prepare.Metadata["artifact_values"].(map[string]string)
	workspace := prepareArtifacts["workspace_path"]
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	var manifest models.ExperimentDatasetManifest
	if err := json.Unmarshal([]byte(prepareArtifacts["experiment_dataset_manifest"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Domain != "retrieval" || manifest.Counts["search_cases"] != 2 || manifest.Counts["holdout_cases"] != 2 || !manifest.Capabilities["graph_links"] {
		t.Fatalf("unexpected dataset manifest: %#v", manifest)
	}

	freeze := &models.Task{
		Type: "experiment_spec",
		Inputs: map[string]any{
			"workspace_path": workspace, "experiment_dataset_manifest": prepareArtifacts["experiment_dataset_manifest"],
			"experiment_max_trials": 10, "experiment_max_wall_seconds": 60,
			"experiment_validation_runs": 3, "experiment_target_score": 0.8,
		},
	}
	if err := agent.ExecuteTask(t.Context(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	freezeArtifacts := freeze.Metadata["artifact_values"].(map[string]string)
	var spec models.ExperimentResearchSpec
	if err := json.Unmarshal([]byte(freezeArtifacts["experiment_spec"]), &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Strategies) != 4 || spec.Strategies[3].Name != "graph_hybrid" {
		t.Fatalf("retrieval strategy space is incomplete: %#v", spec.Strategies)
	}

	sandboxRunner := &scriptedExperimentSandbox{workspace: workspace}
	agent.Sandbox = sandboxRunner
	run := &models.Task{
		Type: "experiment_run",
		Inputs: map[string]any{
			"workspace_path": workspace, "prepared_runtime": "dk-fixture", "experiment_spec": freezeArtifacts["experiment_spec"],
		},
	}
	if err := agent.ExecuteTask(t.Context(), run, nil); err != nil {
		t.Fatal(err)
	}
	runArtifacts := run.Metadata["artifact_values"].(map[string]string)
	var ledger models.ExperimentTrialLedger
	if err := json.Unmarshal([]byte(runArtifacts["experiment_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.StopReason != "target_score_reached" || ledger.CompletedTrials != 2 || ledger.BestCandidate.Strategy != "hybrid_rrf" || ledger.BestScore != 0.8 {
		t.Fatalf("unexpected experiment search result: %#v", ledger)
	}
	if len(ledger.StrategySpace) != 4 || ledger.StrategySpace[3].Name != "graph_hybrid" || len(ledger.StrategySpace[3].Parameters) == 0 {
		t.Fatalf("trial ledger must retain the frozen strategy tree: %#v", ledger.StrategySpace)
	}
	if ledger.Trials[2].Candidate.ParentID != "" || ledger.Trials[2].Candidate.Depth != 0 {
		t.Fatalf("method branch should remain a root-level ablation: %#v", ledger.Trials[2].Candidate)
	}

	validate := &models.Task{
		Type: "experiment_validate",
		Inputs: map[string]any{
			"workspace_path": workspace, "prepared_runtime": "dk-fixture", "experiment_spec": freezeArtifacts["experiment_spec"],
			"experiment_trial_ledger": runArtifacts["experiment_trial_ledger"], "experiment_best_candidate": runArtifacts["experiment_best_candidate"],
		},
	}
	if err := agent.ExecuteTask(t.Context(), validate, nil); err != nil {
		t.Fatal(err)
	}
	var report models.ExperimentValidationReport
	if err := json.Unmarshal([]byte(validate.Result), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "validated" || report.PassedRuns != 3 || len(report.Runs) != 3 || report.Runs[0].CandidateScore != 0.82 {
		t.Fatalf("unexpected holdout validation: %#v", report)
	}

	sandboxRunner.failAfter = sandboxRunner.calls + 1
	failedValidation := &models.Task{Type: "experiment_validate", Inputs: validate.Inputs}
	err := agent.ExecuteTask(t.Context(), failedValidation, nil)
	if err == nil || failedValidation.Status != models.StatusFailed {
		t.Fatalf("evaluator infrastructure failure must fail validation task: status=%s err=%v", failedValidation.Status, err)
	}
	if json.Unmarshal([]byte(failedValidation.Result), &report) != nil || report.Status != "failed" {
		t.Fatalf("failed validation must retain a structured report: %s", failedValidation.Result)
	}
}

func TestRetrievalExperimentRejectsUnlabeledQueries(t *testing.T) {
	corpus := filepath.Join(t.TempDir(), "corpus.csv")
	queries := filepath.Join(t.TempDir(), "queries.csv")
	writeExperimentCSV(t, corpus, []string{"id", "text"}, [][]string{{"d1", "alpha"}, {"d2", "beta"}})
	writeExperimentCSV(t, queries, []string{"query_id", "query"}, [][]string{{"q1", "alpha"}, {"q2", "beta"}})
	task := &models.Task{
		Type: "experiment_dataset_prepare", Description: "RAG 自动实验",
		Inputs: map[string]any{"research_domain": "retrieval", "uploaded_files": []map[string]any{
			{"name": "corpus.csv", "storage_path": corpus}, {"name": "queries.csv", "storage_path": queries},
		}},
	}
	err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(t.Context(), task, nil)
	if err == nil || !strings.Contains(err.Error(), "relevant document IDs") {
		t.Fatalf("expected an actionable missing-label error, got %v", err)
	}
}

func TestExperimentSpecRejectsSearchAsHoldout(t *testing.T) {
	command := []string{"python", "evaluate.py", "--config", "{config_path}"}
	spec := models.ExperimentResearchSpec{
		Version: models.ExperimentSpecVersion, Domain: "generic", Adapter: portableExperimentAdapterName,
		CandidateKind: "strategy_config", MetricKey: "score", Direction: "maximize",
		SearchCommand: command, HoldoutCommand: append([]string(nil), command...),
		Strategies: []models.ExperimentStrategy{{Name: "baseline"}},
		MaxTrials:  4, MaxWallSeconds: 60, ValidationRuns: 2,
		FrozenFiles: []models.ResearchFileHash{{Path: "evaluator.py", SHA256: strings.Repeat("0", 64)}},
	}
	err := validateExperimentSpec(t.TempDir(), spec)
	if err == nil || !strings.Contains(err.Error(), "different frozen evaluation data") {
		t.Fatalf("expected search/holdout isolation error, got %v", err)
	}
}

func TestPortableExperimentAdapterFreezesGenericContract(t *testing.T) {
	root := t.TempDir()
	target := 125.5
	sourceSpec := models.ExperimentResearchSpec{
		Version: models.ExperimentSpecVersion, Name: "latency paper", Domain: "systems",
		CandidateKind: "strategy_config", Objective: "minimize p95 latency",
		SearchCommand:  []string{"python3", "{asset:evaluator.py}", "--data", "{asset:search.jsonl}", "--config", "{config_path}"},
		HoldoutCommand: []string{"python3", "{asset:evaluator.py}", "--data", "{asset:holdout.jsonl}", "--config", "{config_path}"},
		Strategies: []models.ExperimentStrategy{{Name: "indexed", Parameters: []models.ExperimentParameter{{
			Name: "workers", Values: []any{float64(1), float64(2)}, Default: float64(1),
		}}}},
		MetricKey: "p95_ms", Direction: "minimize", MinDelta: 0.1, TargetScore: &target,
		Dependencies: []string{"example-runtime==1.0"},
	}
	specPayload, err := json.Marshal(sourceSpec)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string][]byte{
		"experiment.json": specPayload,
		"evaluator.py":    []byte("print('fixture')\n"),
		"search.jsonl":    []byte("{\"id\":\"search\"}\n"),
		"holdout.jsonl":   []byte("{\"id\":\"holdout\"}\n"),
	}
	files := make([]benchmarkUploadedFile, 0, len(paths))
	for name, payload := range paths {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, benchmarkUploadedFile{Name: name, StoragePath: path, Size: int64(len(payload))})
	}
	adapter := portableExperimentAdapter{}
	workspace, manifest, err := adapter.Prepare(t.Context(), nil, files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if manifest.Domain != "systems" || manifest.Adapter != portableExperimentAdapterName || len(manifest.FrozenFiles) != 4 {
		t.Fatalf("unexpected portable manifest: %#v", manifest)
	}
	spec, err := adapter.BuildSpec(nil, workspace, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if spec.MaxTrials != 12 || spec.MaxWallSeconds != 900 || spec.ValidationRuns != 3 {
		t.Fatalf("portable defaults were not applied: %#v", spec)
	}
	if spec.HoldoutTargetScore == nil || *spec.HoldoutTargetScore != target || spec.Adapter != portableExperimentAdapterName {
		t.Fatalf("portable target or identity was not frozen: %#v", spec)
	}
	if !strings.Contains(strings.Join(spec.SearchCommand, " "), "/workspace/.scholar/experiment/uploads/search.jsonl") ||
		!strings.Contains(strings.Join(spec.HoldoutCommand, " "), "/workspace/.scholar/experiment/uploads/holdout.jsonl") {
		t.Fatalf("portable asset placeholders were not rewritten: search=%v holdout=%v", spec.SearchCommand, spec.HoldoutCommand)
	}
	if err := validateExperimentSpec(workspace, spec); err != nil {
		t.Fatal(err)
	}
}

func TestRetrievalExperimentRealRunnerEndToEnd(t *testing.T) {
	python := strings.TrimSpace(os.Getenv("SCHOLAR_EXPERIMENT_PYTHON"))
	if python == "" {
		t.Skip("set SCHOLAR_EXPERIMENT_PYTHON to a Python environment with the retrieval dependencies")
	}
	root, err := filepath.Abs("../../../examples/scientific-autoresearch/retrieval")
	if err != nil {
		t.Fatal(err)
	}
	prepare := &models.Task{
		Type: "experiment_dataset_prepare", Description: "retrieval strategy AutoResearch",
		Inputs: map[string]any{
			"research_domain": "retrieval", "experiment_adapter": "retrieval.v1",
			"uploaded_files": []map[string]any{
				{"name": "corpus.jsonl", "storage_path": filepath.Join(root, "corpus.jsonl")},
				{"name": "queries.jsonl", "storage_path": filepath.Join(root, "queries.jsonl")},
			},
		},
	}
	agent := &ResearchCodingAgent{Name: "research_coding_agent"}
	if err := agent.ExecuteTask(t.Context(), prepare, nil); err != nil {
		t.Fatal(err)
	}
	prepareArtifacts := prepare.Metadata["artifact_values"].(map[string]string)
	workspace := prepareArtifacts["workspace_path"]
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	freeze := &models.Task{
		Type: "experiment_spec",
		Inputs: map[string]any{
			"workspace_path": workspace, "experiment_dataset_manifest": prepareArtifacts["experiment_dataset_manifest"],
			"experiment_max_trials": 6, "experiment_max_wall_seconds": 180,
			"experiment_validation_runs": 2, "experiment_target_score": 0.6, "experiment_cutoff": 1,
		},
	}
	if err := agent.ExecuteTask(t.Context(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	freezeArtifacts := freeze.Metadata["artifact_values"].(map[string]string)
	agent.Sandbox = &localExperimentSandbox{workspace: workspace, python: python}
	run := &models.Task{Type: "experiment_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-local-real", "experiment_spec": freezeArtifacts["experiment_spec"],
	}}
	if err := agent.ExecuteTask(t.Context(), run, nil); err != nil {
		t.Fatal(err)
	}
	runArtifacts := run.Metadata["artifact_values"].(map[string]string)
	var ledger models.ExperimentTrialLedger
	if err := json.Unmarshal([]byte(runArtifacts["experiment_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.BestCandidate.Strategy != "graph_hybrid" || ledger.BaselineScore != 0.4 || ledger.BestScore != 0.6 || ledger.StopReason != "target_score_reached" {
		t.Fatalf("unexpected real search result: baseline=%v best=%v strategy=%s stop=%s", ledger.BaselineScore, ledger.BestScore, ledger.BestCandidate.Strategy, ledger.StopReason)
	}
	validate := &models.Task{Type: "experiment_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-local-real", "experiment_spec": freezeArtifacts["experiment_spec"],
		"experiment_trial_ledger": runArtifacts["experiment_trial_ledger"], "experiment_best_candidate": runArtifacts["experiment_best_candidate"],
	}}
	if err := agent.ExecuteTask(t.Context(), validate, nil); err != nil {
		t.Fatal(err)
	}
	var report models.ExperimentValidationReport
	if err := json.Unmarshal([]byte(validate.Result), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "validated" || report.PassedRuns != 2 || len(report.Runs) != 2 || mathAbs(report.Runs[0].CandidateScore-2.0/3.0) > 1e-9 {
		t.Fatalf("unexpected real holdout result: %#v", report)
	}
	t.Logf("real experiment: baseline=%.4f best=%.4f strategy=%s holdout=%.4f passed=%d/%d", ledger.BaselineScore, ledger.BestScore, ledger.BestCandidate.Strategy, report.Runs[0].CandidateScore, report.PassedRuns, report.RequestedRuns)
}

func mathAbs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func writeExperimentCSV(t *testing.T, path string, header []string, rows [][]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
