package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/sandbox"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

type scriptedAutoResearchSandbox struct {
	workspace           string
	mutateProtectedCall int
	mutateImmutableCall int
	scoreByCall         map[int]float64
	calls               int
}

type localProcessAutoResearchSandbox struct {
	workspace string
	calls     atomic.Int32
}

func (s *localProcessAutoResearchSandbox) ExecCommandStream(ctx context.Context, _ string, command []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	s.calls.Add(1)
	if len(command) != 3 || command[0] != "bash" || command[1] != "-lc" {
		return nil, fmt.Errorf("unexpected command: %#v", command)
	}
	shellCommand := strings.Replace(command[2], "cd /workspace", "cd "+shellEscape(s.workspace), 1)
	shellCommand = strings.Replace(shellCommand, "PYTHONPATH=/workspace:", "PYTHONPATH="+shellEscape(s.workspace)+":", 1)
	process := exec.CommandContext(ctx, "bash", "-lc", shellCommand)
	stdout, stderr := strings.Builder{}, strings.Builder{}
	process.Stdout = &stdout
	process.Stderr = &stderr
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
	return &sandbox.PythonRunResponse{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, nil
}

func (s *scriptedAutoResearchSandbox) ExecCommandStream(_ context.Context, _ string, _ []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	s.calls++
	if onChunk != nil {
		onChunk("stdout", "autoresearch fixture")
	}
	raw, err := os.ReadFile(filepath.Join(s.workspace, "candidate.py"))
	if err != nil {
		return nil, err
	}
	match := regexp.MustCompile(`SCORE\s*=\s*([0-9.]+)`).FindStringSubmatch(string(raw))
	if len(match) != 2 {
		return &sandbox.PythonRunResponse{ExitCode: 1, Stderr: "missing SCORE"}, nil
	}
	score, _ := strconv.ParseFloat(match[1], 64)
	if override, ok := s.scoreByCall[s.calls]; ok {
		score = override
	}
	if s.mutateProtectedCall == s.calls {
		if err := os.WriteFile(filepath.Join(s.workspace, "evaluator.py"), []byte("tampered\n"), 0o600); err != nil {
			return nil, err
		}
	}
	if s.mutateImmutableCall == s.calls {
		if err := os.WriteFile(filepath.Join(s.workspace, "other.py"), []byte("CHANGED = True\n"), 0o600); err != nil {
			return nil, err
		}
	}
	return &sandbox.PythonRunResponse{
		ExitCode: 0,
		Stdout:   fmt.Sprintf("fixture output\n{\"metrics\":{\"macro_f1\":%.4f}}", score),
	}, nil
}

func newAutoResearchModel(t *testing.T, responses ...string) *openaiModel.ChatModel {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("unexpected model request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(string(body), "Frozen research spec") || !strings.Contains(string(body), "candidate.py") {
			t.Fatalf("model request omitted frozen AutoResearch context: %s", string(body))
		}
		index := int(calls.Add(1)) - 1
		if index >= len(responses) {
			t.Fatalf("unexpected model call %d", index+1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-autoresearch", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": responses[index]}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
		})
	}))
	t.Cleanup(server.Close)
	model, err := openaiModel.NewChatModel(context.Background(), &openaiModel.ChatModelConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func autoResearchFixture(t *testing.T, maxTrials int) (string, string) {
	return autoResearchFixtureWithValidationRuns(t, maxTrials, 1)
}

func autoResearchFixtureWithValidationRuns(t *testing.T, maxTrials, validationRuns int) (string, string) {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "candidate.py"), []byte("SCORE = 0.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "evaluator.py"), []byte("# frozen evaluator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "other.py"), []byte("UNCHANGED = True\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "fixture", Objective: "improve macro F1",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py"},
		EvalCommand: []string{"python3", "evaluator.py"}, MetricKey: "metrics.macro_f1",
		Direction: "maximize", MinDelta: 0.01, MaxTrials: maxTrials, MaxWallSeconds: 60,
		ValidationRuns: validationRuns,
	}
	raw, _ := json.Marshal(spec)
	freeze := &models.Task{
		Type: "autoresearch_spec",
		Inputs: map[string]any{
			"workspace_path": workspace, "research_spec": string(raw),
			"autoresearch_max_trials": maxTrials, "autoresearch_max_wall_seconds": 60,
		},
	}
	agent := &ResearchCodingAgent{Name: "research_coding_agent"}
	if err := agent.ExecuteTask(context.Background(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	artifacts := freeze.Metadata["artifact_values"].(map[string]string)
	return workspace, artifacts["research_spec"]
}

func autoResearchProposal(t *testing.T, score float64, hypothesis string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"status": "propose", "hypothesis": hypothesis, "reason": "bounded fixture change",
		"patches": []map[string]string{{"path": "candidate.py", "content": fmt.Sprintf("SCORE = %.1f\n", score), "reason": "change fixture score"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAutoResearchKeepsImprovementAndRollsBackRegression(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 2)
	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t,
			autoResearchProposal(t, 0.8, "a focused change improves macro F1"),
			autoResearchProposal(t, 0.4, "a second change might improve macro F1"),
		),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(artifacts["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.BaselineScore != 0.5 || ledger.BestScore != 0.8 || ledger.AcceptedTrials != 1 || ledger.CompletedTrials != 2 {
		t.Fatalf("unexpected ledger summary: %#v", ledger)
	}
	if ledger.Trials[1].Status != "kept" || ledger.Trials[2].Status != "rejected" {
		t.Fatalf("unexpected trial decisions: %#v", ledger.Trials)
	}
	var spec models.ResearchSpec
	if err := json.Unmarshal([]byte(frozenSpec), &spec); err != nil {
		t.Fatal(err)
	}
	if err := validateAutoResearchLedgerAgainstSpec(ledger, spec); err != nil {
		t.Fatalf("valid ledger rejected: %v", err)
	}
	tamperedLedger := ledger
	tamperedLedger.MetricKey = "metrics.accuracy"
	if err := validateAutoResearchLedgerAgainstSpec(tamperedLedger, spec); err == nil {
		t.Fatal("tampered ledger metric was accepted")
	}
	if ledger.ResourceUsage == nil {
		t.Fatal("ledger omitted resource usage")
	}
	tamperedUsage := *ledger.ResourceUsage
	tamperedUsage.CommandRuns++
	tamperedLedger = ledger
	tamperedLedger.ResourceUsage = &tamperedUsage
	if err := validateAutoResearchLedgerAgainstSpec(tamperedLedger, spec); err == nil {
		t.Fatal("tampered ledger resource usage was accepted")
	}
	finalSource, _ := os.ReadFile(filepath.Join(workspace, "candidate.py"))
	if string(finalSource) != "SCORE = 0.8\n" {
		t.Fatalf("regression was not rolled back: %q", finalSource)
	}

	validation := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
		"research_trial_ledger": artifacts["research_trial_ledger"], "research_best_candidate": artifacts["research_best_candidate"],
	}}
	if err := agent.ExecuteTask(context.Background(), validation, nil); err != nil {
		t.Fatal(err)
	}
	if validation.Status != models.StatusCompleted || !strings.Contains(validation.Result, `"status":"validated"`) {
		t.Fatalf("unexpected validation result: %s", validation.Result)
	}
	if fixtureSandbox.calls != 4 {
		t.Fatalf("sandbox calls=%d, want baseline + two trials + validation", fixtureSandbox.calls)
	}
}

func TestAutoResearchRunsFrozenCommandsInLocalProcessHarness(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "candidate.py"), []byte("SCORE = 0.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evaluator := "import json\nfrom candidate import SCORE\nprint(json.dumps({'metrics': {'score': SCORE}}))\n"
	if err := os.WriteFile(filepath.Join(workspace, "evaluator.py"), []byte(evaluator), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "local-process", Objective: "increase the measured score",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py"},
		GuardCommands: [][]string{{"python3", "-m", "py_compile", "candidate.py"}},
		EvalCommand:   []string{"python3", "evaluator.py"}, MetricKey: "metrics.score",
		Direction: "maximize", MinDelta: 0.1, MaxTrials: 1, MaxWallSeconds: 60,
		ValidationRuns: 3,
	}
	rawSpec, _ := json.Marshal(spec)
	freezeAgent := &ResearchCodingAgent{Name: "research_coding_agent"}
	freeze := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(rawSpec),
		"autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60,
	}}
	if err := freezeAgent.ExecuteTask(context.Background(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	frozenSpec := freeze.Metadata["artifact_values"].(map[string]string)["research_spec"]
	localSandbox := &localProcessAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: localSandbox,
		ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.9, "raise the evaluator's measured score")),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-local", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(artifacts["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.BaselineScore != 0.25 || ledger.BestScore != 0.9 || ledger.AcceptedTrials != 1 {
		t.Fatalf("real command execution produced unexpected ledger: %#v", ledger)
	}
	if ledger.ResourceUsage == nil || ledger.ResourceUsage.CommandRuns != 4 || ledger.ResourceUsage.GuardRuns != 2 || ledger.ResourceUsage.EvaluatorRuns != 2 || ledger.ResourceUsage.FailedCommands != 0 {
		t.Fatalf("unexpected search resource usage: %#v", ledger.ResourceUsage)
	}
	validation := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-local", "research_spec": frozenSpec,
		"research_trial_ledger": artifacts["research_trial_ledger"], "research_best_candidate": artifacts["research_best_candidate"],
	}}
	if err := agent.ExecuteTask(context.Background(), validation, nil); err != nil {
		t.Fatal(err)
	}
	var report models.ResearchValidationReport
	if err := json.Unmarshal([]byte(validation.Metadata["artifact_values"].(map[string]string)["research_validation_report"]), &report); err != nil {
		t.Fatal(err)
	}
	if report.RequestedRuns != 3 || report.CompletedRuns != 3 || report.PassedRuns != 3 || report.FailureRate != 0 || len(report.Runs) != 3 || len(report.ObservedScores) != 3 {
		t.Fatalf("unexpected repeated validation report: %#v", report)
	}
	if report.MeanScore != 0.9 || report.StdDev != 0 || report.ResourceUsage.CommandRuns != 6 || report.ResourceUsage.GuardRuns != 3 || report.ResourceUsage.EvaluatorRuns != 3 {
		t.Fatalf("unexpected validation statistics or resources: %#v", report)
	}
	if localSandbox.calls.Load() != 10 {
		t.Fatalf("local process calls=%d, want 2 baseline + 2 candidate + 6 validation commands", localSandbox.calls.Load())
	}
}

func TestAutoResearchRepeatedValidationRejectsMetricDrift(t *testing.T) {
	workspace, frozenSpec := autoResearchFixtureWithValidationRuns(t, 1, 3)
	fixtureSandbox := &scriptedAutoResearchSandbox{
		workspace:   workspace,
		scoreByCall: map[int]float64{4: 0.79},
	}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.8, "improve the frozen metric")),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	validation := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
		"research_trial_ledger": artifacts["research_trial_ledger"], "research_best_candidate": artifacts["research_best_candidate"],
	}}
	if err := agent.ExecuteTask(context.Background(), validation, nil); err == nil {
		t.Fatal("metric drift across repeated validation runs should fail validation")
	}
	var report models.ResearchValidationReport
	validationArtifacts := validation.Metadata["artifact_values"].(map[string]string)
	if err := json.Unmarshal([]byte(validationArtifacts["research_validation_report"]), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.ScoreMatches || report.CompletedRuns != 3 || report.PassedRuns != 2 || report.FailedRuns != 1 || len(report.ObservedScores) != 3 {
		t.Fatalf("metric drift was not represented correctly: %#v", report)
	}
	if math.Abs(report.FailureRate-1.0/3.0) > 1e-12 || report.StdDev <= 0 || report.ResourceUsage.CommandRuns != 3 {
		t.Fatalf("unexpected drift statistics or resource usage: %#v", report)
	}
}

func TestAutoResearchRepeatedValidationStopsAndRestoresOnIntegrityFailure(t *testing.T) {
	workspace, frozenSpec := autoResearchFixtureWithValidationRuns(t, 1, 3)
	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.8, "improve the frozen metric")),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	fixtureSandbox.mutateProtectedCall = 3
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	validation := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
		"research_trial_ledger": artifacts["research_trial_ledger"], "research_best_candidate": artifacts["research_best_candidate"],
	}}
	if err := agent.ExecuteTask(context.Background(), validation, nil); err == nil {
		t.Fatal("protected evaluator mutation should fail repeated validation")
	}
	var report models.ResearchValidationReport
	validationArtifacts := validation.Metadata["artifact_values"].(map[string]string)
	if err := json.Unmarshal([]byte(validationArtifacts["research_validation_report"]), &report); err != nil {
		t.Fatal(err)
	}
	if report.ProtectedIntact || report.CompletedRuns != 1 || report.UnfinishedRuns != 2 || report.ResourceUsage.CommandRuns != 1 {
		t.Fatalf("unexpected integrity failure report: %#v", report)
	}
	evaluator, _ := os.ReadFile(filepath.Join(workspace, "evaluator.py"))
	if string(evaluator) != "# frozen evaluator\n" {
		t.Fatalf("validation did not restore the protected evaluator: %q", evaluator)
	}
	candidate, _ := os.ReadFile(filepath.Join(workspace, "candidate.py"))
	if string(candidate) != "SCORE = 0.8\n" {
		t.Fatalf("validation did not preserve the best candidate: %q", candidate)
	}
}

func TestAutoResearchAbortsAndRestoresProtectedEvaluatorMutation(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 1)
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.8, "improve score")),
		Sandbox: &scriptedAutoResearchSandbox{workspace: workspace, mutateProtectedCall: 2},
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	err := agent.ExecuteTask(context.Background(), run, nil)
	if err == nil || !strings.Contains(err.Error(), "protected AutoResearch evaluator or data changed") {
		t.Fatalf("expected protected-file integrity failure, got %v", err)
	}
	evaluator, _ := os.ReadFile(filepath.Join(workspace, "evaluator.py"))
	if string(evaluator) != "# frozen evaluator\n" {
		t.Fatalf("protected evaluator was not restored: %q", evaluator)
	}
	candidate, _ := os.ReadFile(filepath.Join(workspace, "candidate.py"))
	if string(candidate) != "SCORE = 0.5\n" {
		t.Fatalf("candidate was not rolled back after compromise: %q", candidate)
	}
}

func TestAutoResearchAbortsAndRestoresNonEditableSourceMutation(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 1)
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.8, "improve score")),
		Sandbox: &scriptedAutoResearchSandbox{workspace: workspace, mutateImmutableCall: 2},
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	err := agent.ExecuteTask(context.Background(), run, nil)
	if err == nil || !strings.Contains(err.Error(), "non-editable repository source changed") {
		t.Fatalf("expected immutable-source integrity failure, got %v", err)
	}
	other, _ := os.ReadFile(filepath.Join(workspace, "other.py"))
	if string(other) != "UNCHANGED = True\n" {
		t.Fatalf("non-editable source was not restored: %q", other)
	}
	candidate, _ := os.ReadFile(filepath.Join(workspace, "candidate.py"))
	if string(candidate) != "SCORE = 0.5\n" {
		t.Fatalf("candidate was not restored after source mutation: %q", candidate)
	}
}

func TestAutoResearchRestoreRejectsSymlinkedParent(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "src")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "candidate.py")
	if err := os.WriteFile(path, []byte("SAFE = True\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshots, err := snapshotAutoResearchFiles(workspace, []string{"src/candidate.py"}, autoResearchMaxFileBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, directory); err != nil {
		t.Fatal(err)
	}
	if err := restoreAutoResearchSnapshots(snapshots); err == nil || !strings.Contains(err.Error(), "restore parent") {
		t.Fatalf("expected symlinked restore parent rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "candidate.py")); !os.IsNotExist(err) {
		t.Fatalf("restore escaped workspace through a symlink: %v", err)
	}
}

func TestParseAutoResearchMetricUsesFinalNestedJSON(t *testing.T) {
	metric, err := parseAutoResearchMetric("log line\n{\"metrics\":{\"macro_f1\":0.75}}", "metrics.macro_f1")
	if err != nil || metric != 0.75 {
		t.Fatalf("metric=%v err=%v", metric, err)
	}
	if _, err := parseAutoResearchMetric(`{"metrics":{"macro_f1":"fake"}}`, "metrics.macro_f1"); err == nil {
		t.Fatal("string metric should be rejected")
	}
}

func TestAutoResearchSpecRejectsEditableEvaluatorEntrypoint(t *testing.T) {
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"candidate.py": "print('{\"score\": 1}')\n",
		"evaluator.py": "# protected but unused\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "unsafe", Objective: "reject reward hacking",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py"},
		EvalCommand: []string{"python3", "candidate.py"}, MetricKey: "score", Direction: "maximize",
		MaxTrials: 1, MaxWallSeconds: 60,
	}
	raw, _ := json.Marshal(spec)
	task := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(raw),
		"autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60,
	}}
	err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), task, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot execute editable file") {
		t.Fatalf("expected editable evaluator rejection, got %v", err)
	}
}

func TestAutoResearchSpecDoesNotIncreaseExplicitWallBudget(t *testing.T) {
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"candidate.py": "VALUE = 1\n",
		"evaluator.py": "print('{\"score\": 1}')\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "short-budget", Objective: "respect the user's wall budget",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py"},
		EvalCommand: []string{"python3", "evaluator.py"}, MetricKey: "score", Direction: "maximize",
		MaxTrials: 1, MaxWallSeconds: 5, ValidationRuns: 2,
	}
	raw, _ := json.Marshal(spec)
	task := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(raw),
		"autoresearch_max_trials": 3, "autoresearch_max_wall_seconds": 60, "autoresearch_validation_runs": 4,
	}}
	if err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	var frozen models.ResearchSpec
	if err := json.Unmarshal([]byte(task.Metadata["artifact_values"].(map[string]string)["research_spec"]), &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.MaxWallSeconds != 5 {
		t.Fatalf("explicit wall budget was increased to %d seconds", frozen.MaxWallSeconds)
	}
	if frozen.ValidationRuns != 2 {
		t.Fatalf("explicit validation run budget was increased to %d", frozen.ValidationRuns)
	}
}

func TestAutoResearchSpecUsesBoundedValidationRunsWhenUnset(t *testing.T) {
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"candidate.py": "VALUE = 1\n",
		"evaluator.py": "print('{\"score\": 1}')\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "validation-budget", Objective: "freeze repeated validation",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py"},
		EvalCommand: []string{"python3", "evaluator.py"}, MetricKey: "score", Direction: "maximize",
		MaxTrials: 1, MaxWallSeconds: 60,
	}
	raw, _ := json.Marshal(spec)
	task := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(raw),
		"autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60, "autoresearch_validation_runs": 9,
	}}
	if err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	var frozen models.ResearchSpec
	if err := json.Unmarshal([]byte(task.Metadata["artifact_values"].(map[string]string)["research_spec"]), &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.ValidationRuns != autoResearchMaxValidationRuns {
		t.Fatalf("validation run budget=%d, want runtime maximum %d", frozen.ValidationRuns, autoResearchMaxValidationRuns)
	}
}
