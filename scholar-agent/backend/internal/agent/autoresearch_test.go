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
	failCalls           map[int]bool
	emitCases           bool
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
	if s.failCalls[s.calls] {
		return &sandbox.PythonRunResponse{ExitCode: 1, Stderr: "forced evaluator failure"}, nil
	}
	payload := fmt.Sprintf("{\"metrics\":{\"macro_f1\":%.4f}}", score)
	if s.emitCases {
		payload = fmt.Sprintf("{\"metrics\":{\"macro_f1\":%.4f},\"cases\":[{\"name\":\"score_target\",\"passed\":%t}]}", score, score >= 0.8)
	}
	return &sandbox.PythonRunResponse{ExitCode: 0, Stdout: "fixture output\n" + payload}, nil
}

func newAutoResearchModel(t *testing.T, responses ...string) *openaiModel.ChatModel {
	return newAutoResearchModelWithInspector(t, nil, responses...)
}

func newAutoResearchModelWithInspector(t *testing.T, inspect func(int, string), responses ...string) *openaiModel.ChatModel {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("unexpected model request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(string(body), "Frozen research spec") || !strings.Contains(string(body), "candidate.py") ||
			!strings.Contains(string(body), "read-only JSON") || !strings.Contains(string(body), "evaluator.py") {
			t.Fatalf("model request omitted frozen AutoResearch context: %s", string(body))
		}
		if strings.Contains(string(body), "holdout.py") || strings.Contains(string(body), "HOLDOUT_SECRET") {
			t.Fatalf("model request leaked hidden holdout context: %s", string(body))
		}
		index := int(calls.Add(1)) - 1
		if inspect != nil {
			inspect(index, string(body))
		}
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
	return autoResearchFixtureWithSearchMeasurement(t, maxTrials, validationRuns, 1, "mean")
}

func autoResearchFixtureWithSearchMeasurement(t *testing.T, maxTrials, validationRuns, searchRuns int, aggregation string) (string, string) {
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
		ValidationRuns: validationRuns, SearchRuns: searchRuns, SearchAggregation: aggregation,
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
		"status": "propose", "diagnosis": "the evaluator reads SCORE from candidate.py", "hypothesis": hypothesis, "reason": "bounded fixture change",
		"patches": []map[string]string{{"path": "candidate.py", "content": fmt.Sprintf("SCORE = %.1f\n", score), "reason": "change fixture score"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func autoResearchSourceProposal(t *testing.T, source, hypothesis string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"status": "propose", "diagnosis": "the public contract exposes a bounded behavior gap", "hypothesis": hypothesis, "reason": "exercise the frozen public contract",
		"patches": []map[string]string{{"path": "candidate.py", "content": source, "reason": "replace the bounded candidate implementation"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func autoResearchStopResponse(t *testing.T, reason string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"status": "stop", "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func autoResearchHoldoutFixture(t *testing.T, holdoutMinDelta float64, validationRuns int) (string, string) {
	t.Helper()
	workspace := t.TempDir()
	files := map[string]string{
		"candidate.py": "SEARCH_SCORE = 0.5\nHOLDOUT_SCORE = 0.5\n",
		"evaluator.py": "import json\nfrom candidate import SEARCH_SCORE\nprint(json.dumps({'metrics': {'score': SEARCH_SCORE}}))\n",
		"holdout.py":   "# HOLDOUT_SECRET\nimport json\nfrom candidate import HOLDOUT_SCORE\nprint(json.dumps({'metrics': {'score': HOLDOUT_SCORE}}))\n",
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(workspace, relative), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "holdout-fixture", Objective: "improve general behavior",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py", "holdout.py"},
		GuardCommands: [][]string{{"python3", "-m", "py_compile", "candidate.py"}},
		EvalCommand:   []string{"python3", "evaluator.py"}, HoldoutCommand: []string{"python3", "holdout.py"},
		HoldoutMinDelta: &holdoutMinDelta, MetricKey: "metrics.score", Direction: "maximize", MinDelta: 0.1,
		MaxTrials: 1, MaxWallSeconds: 60, ValidationRuns: validationRuns,
	}
	rawSpec, _ := json.Marshal(spec)
	freeze := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(rawSpec), "autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60,
	}}
	if err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	return workspace, freeze.Metadata["artifact_values"].(map[string]string)["research_spec"]
}

func TestAutoResearchReadOnlySourceContextIncludesReferencedCodeOnly(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"candidate.py":        "SCORE = 0.5\n",
		"evaluator.py":        "# evaluator contract\n",
		"tests/test_guard.py": "# upstream guard\n",
		"benchmark.json":      `{"private":"example"}`,
	}
	for relative, content := range files {
		path := filepath.Join(workspace, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		EditableFiles: []string{"candidate.py"},
		EvalCommand:   []string{"python3", "evaluator.py", "benchmark.json"},
		GuardCommands: [][]string{{"python3", "-m", "pytest", "tests/test_guard.py", "candidate.py"}},
	}
	contextText := autoResearchReadOnlySourceContext(workspace, spec)
	for _, expected := range []string{"evaluator.py", "# evaluator contract", "tests/test_guard.py", "# upstream guard", `"access":"read_only"`} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("read-only context omitted %q: %s", expected, contextText)
		}
	}
	for _, forbidden := range []string{"SCORE = 0.5", "private"} {
		if strings.Contains(contextText, forbidden) {
			t.Fatalf("read-only context leaked %q: %s", forbidden, contextText)
		}
	}
}

func TestAutoResearchCandidateContextRedactsHiddenHoldout(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"candidate.py": "SEARCH_SCORE = 0.5\nHOLDOUT_SCORE = 0.5\n",
		"evaluator.py": "# public evaluator\n",
		"holdout.py":   "# HOLDOUT_SECRET\n",
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(workspace, relative), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "redaction", Objective: "improve general behavior",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py", "holdout.py"},
		EvalCommand: []string{"python3", "evaluator.py"}, HoldoutCommand: []string{"python3", "holdout.py"},
		MetricKey: "metrics.score", Direction: "maximize", MinDelta: 0.1, MaxTrials: 1, MaxWallSeconds: 60,
	}
	publicSpec := string(autoResearchCandidateSpecJSON(spec))
	readOnly := autoResearchReadOnlySourceContext(workspace, spec)
	for label, content := range map[string]string{"candidate spec": publicSpec, "read-only context": readOnly} {
		for _, secret := range []string{"holdout.py", "HOLDOUT_SECRET", "protected_files", "holdout_command"} {
			if strings.Contains(content, secret) {
				t.Fatalf("%s leaked %q: %s", label, secret, content)
			}
		}
	}
	if !strings.Contains(publicSpec, `"holdout_validation":true`) {
		t.Fatalf("candidate spec did not disclose the existence of hidden validation: %s", publicSpec)
	}
}

func TestAutoResearchHiddenEvaluationPolicyRejectsEvaluatorDiscovery(t *testing.T) {
	for _, patched := range []string{
		"import os\nfiles = list(os.walk('/workspace'))\n",
		"from pathlib import Path\nsecret = Path('.scholar/uploads').read_text()\n",
		"import sys\nargs = sys.argv\n",
	} {
		if err := validateAutoResearchHiddenEvaluationPolicy("VALUE = 1\n", patched); err == nil {
			t.Fatalf("hidden evaluator discovery was accepted: %s", patched)
		}
	}
	if err := validateAutoResearchHiddenEvaluationPolicy("VALUE = 1\n", "VALUE = 2\n"); err != nil {
		t.Fatalf("ordinary candidate was rejected: %v", err)
	}
}

func TestAutoResearchHiddenHoldoutRejectsPublicOnlyImprovement(t *testing.T) {
	workspace := t.TempDir()
	holdoutMinDelta := 0.2
	files := map[string]string{
		"candidate.py": "SEARCH_SCORE = 0.5\nHOLDOUT_SCORE = 0.5\n",
		"evaluator.py": "import json\nfrom candidate import SEARCH_SCORE\nprint(json.dumps({'metrics': {'score': SEARCH_SCORE}}))\n",
		"holdout.py":   "# HOLDOUT_SECRET\nimport json\nfrom candidate import HOLDOUT_SCORE\nprint(json.dumps({'metrics': {'score': HOLDOUT_SCORE}}))\n",
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(workspace, relative), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "holdout-rejection", Objective: "improve general behavior",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py", "holdout.py"},
		GuardCommands: [][]string{{"python3", "-m", "py_compile", "candidate.py"}},
		EvalCommand:   []string{"python3", "evaluator.py"}, HoldoutCommand: []string{"python3", "holdout.py"},
		HoldoutMinDelta: &holdoutMinDelta, MetricKey: "metrics.score", Direction: "maximize", MinDelta: 0.1, MaxTrials: 1, MaxWallSeconds: 60, ValidationRuns: 3,
	}
	rawSpec, _ := json.Marshal(spec)
	freeze := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(rawSpec), "autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60,
	}}
	if err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	frozenSpec := freeze.Metadata["artifact_values"].(map[string]string)["research_spec"]
	localSandbox := &localProcessAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: localSandbox,
		ChatModel: newAutoResearchModel(t, autoResearchSourceProposal(t, "SEARCH_SCORE = 0.9\nHOLDOUT_SCORE = 0.6\n", "improve the visible behavior")),
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
	if ledger.BestScore != 0.9 || ledger.HoldoutBaseline == nil || *ledger.HoldoutBaseline != 0.5 {
		t.Fatalf("unexpected search and holdout baselines: %#v", ledger)
	}
	if ledger.ResourceUsage == nil || ledger.ResourceUsage.CommandRuns != 5 || ledger.ResourceUsage.EvaluatorRuns != 3 {
		t.Fatalf("hidden baseline was not represented in resource usage: %#v", ledger.ResourceUsage)
	}
	validation := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-local", "research_spec": frozenSpec,
		"research_trial_ledger": artifacts["research_trial_ledger"], "research_best_candidate": artifacts["research_best_candidate"],
	}}
	if err := agent.ExecuteTask(context.Background(), validation, nil); err != nil {
		t.Fatalf("completed holdout rejection should remain reportable: %v", err)
	}
	var report models.ResearchValidationReport
	if err := json.Unmarshal([]byte(validation.Metadata["artifact_values"].(map[string]string)["research_validation_report"]), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.ValidationMode != "hidden_holdout" || report.ExpectedScore != 0.7 || report.SearchBestScore != 0.9 || report.HoldoutBaseline == nil || *report.HoldoutBaseline != 0.5 || report.PassedRuns != 0 || report.FailedRuns != 3 {
		t.Fatalf("public-only regression was not rejected with complete evidence: %#v", report)
	}
	if validation.Status != models.StatusCompleted {
		t.Fatalf("domain-level rejection blocked downstream reporting: %s", validation.Status)
	}
	for _, validationRun := range report.Runs {
		if validationRun.DeltaFromBaseline == nil || math.Abs(*validationRun.DeltaFromBaseline-0.1) > 1e-12 {
			t.Fatalf("holdout delta missing or wrong: %#v", validationRun)
		}
	}
}

func TestAutoResearchHiddenHoldoutAcceptsGeneralImprovement(t *testing.T) {
	workspace, frozenSpec := autoResearchHoldoutFixture(t, 0.2, 2)
	localSandbox := &localProcessAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: localSandbox,
		ChatModel: newAutoResearchModel(t, autoResearchSourceProposal(t, "SEARCH_SCORE = 0.9\nHOLDOUT_SCORE = 0.8\n", "improve both visible and adjacent behavior")),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-local", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	artifacts := run.Metadata["artifact_values"].(map[string]string)
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
	if report.Status != "validated" || report.ValidationMode != "hidden_holdout" || report.ExpectedScore != 0.7 || report.SearchBestScore != 0.9 || report.MeanScore != 0.8 || report.PassedRuns != 2 || report.FailedRuns != 0 {
		t.Fatalf("general improvement did not pass hidden holdout: %#v", report)
	}
	if strings.Contains(report.Summary, "independent evaluator") || !strings.Contains(report.Summary, "hidden holdout") {
		t.Fatalf("validation summary used an inaccurate label: %s", report.Summary)
	}
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
	if ledger.Trials[1].Diagnosis != "the evaluator reads SCORE from candidate.py" {
		t.Fatalf("candidate diagnosis was not preserved: %#v", ledger.Trials[1])
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

func TestAutoResearchStopsAfterKeptCandidateReachesTarget(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 2)
	var spec models.ResearchSpec
	if err := json.Unmarshal([]byte(frozenSpec), &spec); err != nil {
		t.Fatal(err)
	}
	target := 0.8
	spec.TargetScore = &target
	frozenWithTarget, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t,
			autoResearchProposal(t, 0.8, "reach the frozen campaign target"),
			autoResearchProposal(t, 0.9, "this proposal must not be requested"),
		),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": string(frozenWithTarget),
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(run.Metadata["artifact_values"].(map[string]string)["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.CompletedTrials != 1 || ledger.AcceptedTrials != 1 || ledger.BestScore != target || ledger.StopReason != "target_score_reached" {
		t.Fatalf("target did not stop the campaign after the first kept trial: %#v", ledger)
	}
	if ledger.TargetScore == nil || *ledger.TargetScore != target || fixtureSandbox.calls != 2 {
		t.Fatalf("target evidence or evaluator calls are inconsistent: target=%v calls=%d", ledger.TargetScore, fixtureSandbox.calls)
	}
	if err := validateAutoResearchLedgerAgainstSpec(ledger, spec); err != nil {
		t.Fatalf("valid target-stop ledger rejected: %v", err)
	}
	tampered := ledger
	tampered.StopReason = "trial_budget_exhausted"
	if err := validateAutoResearchLedgerAgainstSpec(tampered, spec); err == nil {
		t.Fatal("ledger that hid its target stop was accepted")
	}
	tampered = ledger
	tampered.StopReason = "target_score_reached"
	notReached := 0.9
	tampered.TargetScore = &notReached
	if err := validateAutoResearchLedgerAgainstSpec(tampered, spec); err == nil {
		t.Fatal("tampered target-stop ledger was accepted")
	}
}

func TestAutoResearchSearchMeasurementRejectsOneShotSpike(t *testing.T) {
	workspace, frozenSpec := autoResearchFixtureWithSearchMeasurement(t, 2, 1, 3, "worst")
	fixtureSandbox := &scriptedAutoResearchSandbox{
		workspace: workspace,
		scoreByCall: map[int]float64{
			4: 0.95,
			5: 0.4,
			6: 0.4,
		},
	}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t,
			autoResearchProposal(t, 0.8, "a one-shot spike should not be trusted"),
			autoResearchProposal(t, 0.7, "a stable gain should survive every search replay"),
		),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(run.Metadata["artifact_values"].(map[string]string)["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.SearchRuns != 3 || ledger.SearchAggregation != "worst" || ledger.BestScore != 0.7 || ledger.AcceptedTrials != 1 {
		t.Fatalf("unexpected robust search summary: %#v", ledger)
	}
	if ledger.Trials[1].Status != "rejected" || *ledger.Trials[1].Metric != 0.4 || len(ledger.Trials[1].MetricSamples) != 3 {
		t.Fatalf("one-shot spike was not rejected with raw evidence: %#v", ledger.Trials[1])
	}
	if ledger.Trials[2].Status != "kept" || *ledger.Trials[2].Metric != 0.7 || math.Abs(ledger.Trials[2].MetricStdDev) > 1e-12 {
		t.Fatalf("stable candidate was not retained: %#v", ledger.Trials[2])
	}
	if ledger.ResourceUsage == nil || ledger.ResourceUsage.EvaluatorRuns != 9 || ledger.ResourceUsage.CommandRuns != 9 {
		t.Fatalf("repeated search commands were not accounted for: %#v", ledger.ResourceUsage)
	}
	var spec models.ResearchSpec
	if err := json.Unmarshal([]byte(frozenSpec), &spec); err != nil {
		t.Fatal(err)
	}
	if err := validateAutoResearchLedgerAgainstSpec(ledger, spec); err != nil {
		t.Fatalf("robust search ledger was rejected: %v", err)
	}
	tampered := ledger
	tampered.Trials = append([]models.ResearchTrial(nil), ledger.Trials...)
	tampered.Trials[2].MetricSamples = append([]float64(nil), ledger.Trials[2].MetricSamples...)
	tampered.Trials[2].MetricSamples[0] = 0.99
	if err := validateAutoResearchLedgerAgainstSpec(tampered, spec); err == nil {
		t.Fatal("tampered repeated-search samples were accepted")
	}
}

func TestAutoResearchSearchMeasurementFailsClosedOnPartialReplay(t *testing.T) {
	workspace, frozenSpec := autoResearchFixtureWithSearchMeasurement(t, 1, 1, 3, "mean")
	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace, failCalls: map[int]bool{5: true}}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.9, "all declared search runs must finish")),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(run.Metadata["artifact_values"].(map[string]string)["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	trial := ledger.Trials[1]
	if trial.Status != "rejected" || trial.Metric != nil || len(trial.EvalResults) != 2 || len(trial.MetricSamples) != 1 || ledger.BestScore != 0.5 {
		t.Fatalf("partial repeated evaluation did not fail closed: %#v", trial)
	}
	if ledger.ResourceUsage == nil || ledger.ResourceUsage.EvaluatorRuns != 5 || ledger.ResourceUsage.FailedCommands != 1 {
		t.Fatalf("partial failure was not represented in resources: %#v", ledger.ResourceUsage)
	}
	finalSource, _ := os.ReadFile(filepath.Join(workspace, "candidate.py"))
	if string(finalSource) != "SCORE = 0.5\n" {
		t.Fatalf("partially evaluated candidate was not rolled back: %q", finalSource)
	}
}

func TestAggregateAutoResearchScoresSupportsRobustDirections(t *testing.T) {
	checks := []struct {
		name        string
		scores      []float64
		aggregation string
		direction   string
		want        float64
	}{
		{name: "mean", scores: []float64{1, 2, 3}, aggregation: "mean", direction: "maximize", want: 2},
		{name: "odd median", scores: []float64{9, 1, 3}, aggregation: "median", direction: "maximize", want: 3},
		{name: "even median", scores: []float64{4, 2}, aggregation: "median", direction: "minimize", want: 3},
		{name: "maximize worst", scores: []float64{0.9, 0.4, 0.8}, aggregation: "worst", direction: "maximize", want: 0.4},
		{name: "minimize worst", scores: []float64{10, 14, 9}, aggregation: "worst", direction: "minimize", want: 14},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			got, err := aggregateAutoResearchScores(check.scores, check.aggregation, check.direction)
			if err != nil || got != check.want {
				t.Fatalf("aggregate=%v err=%v, want %v", got, err, check.want)
			}
		})
	}
	if _, err := aggregateAutoResearchScores([]float64{1}, "unknown", "maximize"); err == nil {
		t.Fatal("unknown aggregation was accepted")
	}
	if _, err := aggregateAutoResearchScores([]float64{math.NaN()}, "mean", "maximize"); err == nil {
		t.Fatal("non-finite sample was accepted")
	}
}

func TestAutoResearchTargetReachedSupportsMetricDirections(t *testing.T) {
	maximizeTarget := 0.8
	minimizeTarget := 10.0
	if !autoResearchTargetReached(&maximizeTarget, 0.8, "maximize") || !autoResearchTargetReached(&maximizeTarget, 0.9, "maximize") {
		t.Fatal("maximize target was not recognized")
	}
	if autoResearchTargetReached(&maximizeTarget, 0.79, "maximize") {
		t.Fatal("maximize score below the target was accepted")
	}
	if !autoResearchTargetReached(&minimizeTarget, 10, "minimize") || !autoResearchTargetReached(&minimizeTarget, 9, "minimize") {
		t.Fatal("minimize target was not recognized")
	}
	if autoResearchTargetReached(&minimizeTarget, 10.1, "minimize") || autoResearchTargetReached(nil, 1, "maximize") || autoResearchTargetReached(&maximizeTarget, 1, "unknown") {
		t.Fatal("invalid or absent target was accepted")
	}
}

func TestAutoResearchRejectsPrematureStopWhileVisibleCasesFail(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 2)
	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace, emitCases: true}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModel(t,
			autoResearchStopResponse(t, "the next edit is uncertain"),
			autoResearchProposal(t, 0.9, "address the unresolved public contract"),
		),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	var ledger models.ResearchTrialLedger
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	if err := json.Unmarshal([]byte(artifacts["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.CompletedTrials != 2 || ledger.AcceptedTrials != 1 || ledger.BestScore != 0.9 {
		t.Fatalf("premature stop prevented the remaining search budget: %#v", ledger)
	}
	if ledger.Trials[1].Decision != "reject" || !strings.Contains(ledger.Trials[1].Reason, "score_target") || ledger.Trials[2].Status != "kept" {
		t.Fatalf("premature stop was not recorded as a rejected attempt: %#v", ledger.Trials)
	}
	var spec models.ResearchSpec
	if err := json.Unmarshal([]byte(frozenSpec), &spec); err != nil {
		t.Fatal(err)
	}
	if err := validateAutoResearchLedgerAgainstSpec(ledger, spec); err != nil {
		t.Fatalf("premature-stop ledger failed final validation: %v", err)
	}
}

func TestAutoResearchContinuesAfterMalformedCandidateResponse(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 2)
	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModelWithInspector(t, func(call int, body string) {
			if call != 1 {
				return
			}
			for _, expected := range []string{"candidate model output rejected", "decode candidate response", "required_response", "without Markdown fences"} {
				if !strings.Contains(body, expected) {
					t.Fatalf("retry request omitted malformed-response feedback %q: %s", expected, body)
				}
			}
		},
			`{"status":"propose","diagnosis":"unterminated response"`,
			autoResearchProposal(t, 0.9, "recover after returning valid strict JSON"),
		),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(run.Metadata["artifact_values"].(map[string]string)["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.CompletedTrials != 2 || ledger.AcceptedTrials != 1 || ledger.BestScore != 0.9 {
		t.Fatalf("malformed response consumed the remaining campaign: %#v", ledger)
	}
	if ledger.Trials[1].Status != "rejected" || ledger.Trials[1].Decision != "reject" || !strings.Contains(ledger.Trials[1].Reason, "decode candidate response") {
		t.Fatalf("malformed model output was not audited as a rejected attempt: %#v", ledger.Trials[1])
	}
	if ledger.Trials[2].Status != "kept" || fixtureSandbox.calls != 2 {
		t.Fatalf("valid retry did not execute normally: trials=%#v sandbox_calls=%d", ledger.Trials, fixtureSandbox.calls)
	}
	validation := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
		"research_trial_ledger":   run.Metadata["artifact_values"].(map[string]string)["research_trial_ledger"],
		"research_best_candidate": run.Metadata["artifact_values"].(map[string]string)["research_best_candidate"],
	}}
	if err := agent.ExecuteTask(context.Background(), validation, nil); err != nil {
		t.Fatalf("valid candidate could not pass final ledger validation after malformed output: %v", err)
	}
	if validation.Status != models.StatusCompleted || fixtureSandbox.calls != 3 {
		t.Fatalf("unexpected final validation state: status=%s sandbox_calls=%d", validation.Status, fixtureSandbox.calls)
	}
}

func TestAutoResearchReturnsRejectedCandidateSourceToNextAttempt(t *testing.T) {
	workspace, frozenSpec := autoResearchFixture(t, 2)
	fixtureSandbox := &scriptedAutoResearchSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name: "research_coding_agent", Sandbox: fixtureSandbox,
		ChatModel: newAutoResearchModelWithInspector(t, func(call int, body string) {
			if call == 1 {
				for _, expected := range []string{"Previous rejected candidate", "BROKEN = True", "missing SCORE"} {
					if !strings.Contains(body, expected) {
						t.Fatalf("second candidate request omitted rejected-source feedback %q: %s", expected, body)
					}
				}
			}
		},
			autoResearchSourceProposal(t, "BROKEN = True\n", "try an incomplete implementation"),
			autoResearchProposal(t, 0.9, "repair the exact rejected candidate"),
		),
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "dk-fixture", "research_spec": frozenSpec,
	}}
	if err := agent.ExecuteTask(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(run.Metadata["artifact_values"].(map[string]string)["research_trial_ledger"]), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Trials[1].Status != "rejected" || ledger.Trials[2].Status != "kept" || ledger.BestScore != 0.9 {
		t.Fatalf("rejected-source repair did not recover the campaign: %#v", ledger.Trials)
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
	if err := agent.ExecuteTask(context.Background(), validation, nil); err != nil {
		t.Fatalf("completed metric rejection should remain reportable: %v", err)
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
	if frozen.SearchRuns != 1 || frozen.SearchAggregation != "mean" {
		t.Fatalf("default search measurement=%dx%s, want 1xmean", frozen.SearchRuns, frozen.SearchAggregation)
	}
}

func TestAutoResearchSpecBoundsAndValidatesSearchMeasurement(t *testing.T) {
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"candidate.py": "VALUE = 1\n",
		"evaluator.py": "print('{\"score\": 1}')\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := models.ResearchSpec{
		Version: models.AutoResearchSpecVersion, Name: "search-measurement", Objective: "freeze repeated search measurement",
		EditableFiles: []string{"candidate.py"}, ProtectedFiles: []string{"evaluator.py"},
		EvalCommand: []string{"python3", "evaluator.py"}, MetricKey: "score", Direction: "minimize",
		MaxTrials: 1, MaxWallSeconds: 60, SearchRuns: 99, SearchAggregation: "WORST",
	}
	raw, _ := json.Marshal(base)
	task := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(raw),
		"autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60,
	}}
	if err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	var frozen models.ResearchSpec
	if err := json.Unmarshal([]byte(task.Metadata["artifact_values"].(map[string]string)["research_spec"]), &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.SearchRuns != autoResearchMaxSearchRuns || frozen.SearchAggregation != "worst" {
		t.Fatalf("bounded search measurement=%dx%s", frozen.SearchRuns, frozen.SearchAggregation)
	}

	base.SearchAggregation = "peak"
	raw, _ = json.Marshal(base)
	bad := &models.Task{Type: "autoresearch_spec", Inputs: map[string]any{
		"workspace_path": workspace, "research_spec": string(raw),
		"autoresearch_max_trials": 1, "autoresearch_max_wall_seconds": 60,
	}}
	if err := (&ResearchCodingAgent{Name: "research_coding_agent"}).ExecuteTask(context.Background(), bad, nil); err == nil {
		t.Fatal("unsupported search aggregation was accepted")
	}
}

func TestValidateAutoResearchRepositoryRevisionMatchesPreparedCommit(t *testing.T) {
	revision := "47aa3ddf8dc1ebeb7ef4e65f2b4536af44594099"
	manifest := fmt.Sprintf(`{"requested_revision":%q,"repository_commit":%q}`, revision, revision)
	task := &models.Task{Inputs: map[string]any{"repo_manifest": manifest}}
	if err := validateAutoResearchRepositoryRevision(task, revision); err != nil {
		t.Fatal(err)
	}

	mismatch := &models.Task{Inputs: map[string]any{"repo_manifest": fmt.Sprintf(`{"requested_revision":%q,"repository_commit":%q}`, revision, "14a00ad88fc33cf2b52f4f113f25807556f8e25e")}}
	if err := validateAutoResearchRepositoryRevision(mismatch, revision); err == nil {
		t.Fatal("mismatched prepared commit was accepted")
	}
	if err := validateAutoResearchRepositoryRevision(&models.Task{}, revision); err == nil {
		t.Fatal("missing repo_manifest evidence was accepted")
	}
}
