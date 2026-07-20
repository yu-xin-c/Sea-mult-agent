package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/sandbox"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

type scriptedPaperDebugSandbox struct {
	workspace  string
	alwaysFail bool
	calls      int
}

func (s *scriptedPaperDebugSandbox) ExecCommandStream(_ context.Context, sandboxID string, cmd []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	s.calls++
	if sandboxID != "dk-paper-fixture" || !strings.Contains(strings.Join(cmd, " "), "python3 'main.py'") {
		return nil, &paperDebugTestError{message: "unexpected paper debug command"}
	}
	raw, err := os.ReadFile(filepath.Join(s.workspace, "main.py"))
	if err != nil {
		return nil, err
	}
	if s.alwaysFail || strings.Contains(string(raw), "BROKEN") {
		message := "Traceback (most recent call last):\n  File \"/workspace/main.py\", line 1, in <module>\nRuntimeError: BROKEN"
		if onChunk != nil {
			onChunk("stderr", message)
		}
		return &sandbox.PythonRunResponse{ExitCode: 1, Stderr: message}, nil
	}
	if onChunk != nil {
		onChunk("stdout", `{"metric":1}`)
	}
	return &sandbox.PythonRunResponse{ExitCode: 0, Stdout: `{"metric":1}`}, nil
}

type paperDebugTestError struct {
	message string
}

func (e *paperDebugTestError) Error() string { return e.message }

func newPaperDebugModel(t *testing.T, responses ...string) *openaiModel.ChatModel {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("unexpected model request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(string(body), "Failure or mismatch evidence") || !strings.Contains(string(body), "FILE: main.py") {
			t.Fatalf("model request omitted bounded paper debug evidence: %s", string(body))
		}
		index := int(calls.Add(1)) - 1
		if index >= len(responses) {
			t.Fatalf("unexpected model call %d", index+1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-paper-debug", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": responses[index]}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
		})
	}))
	t.Cleanup(server.Close)
	model, err := openaiModel.NewChatModel(context.Background(), &openaiModel.ChatModelConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func paperDebugTaskFixture(t *testing.T, source string) (string, *models.Task) {
	t.Helper()
	workspace := t.TempDir()
	entryPath := filepath.Join(workspace, "main.py")
	if err := os.WriteFile(entryPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"code_file_candidates": []string{"main.py"}})
	return workspace, &models.Task{
		Type:        "paper_code_execute",
		AssignedTo:  "research_coding_agent",
		Description: "Run and debug the paper baseline without changing its method.",
		Inputs: map[string]any{
			"workspace_path":   workspace,
			"code_file_path":   entryPath,
			"generated_code":   source,
			"prepared_runtime": "dk-paper-fixture",
			"repo_manifest":    string(manifest),
		},
	}
}

func TestResearchCodingAgentRepairsPaperCodeAndRecordsPatchEvidence(t *testing.T) {
	original := "def evaluate():\n    return 1\n\nraise RuntimeError('BROKEN')\n"
	workspace, task := paperDebugTaskFixture(t, original)
	response, _ := json.Marshal(map[string]any{
		"status":    "patched",
		"diagnosis": "the entry raises before executing the measured baseline",
		"patches": []map[string]any{{
			"path": "main.py", "content": "def evaluate():\n    return 1\n\nprint(evaluate())\n", "reason": "remove the unconditional exception and preserve the evaluation function",
		}},
	})
	fakeSandbox := &scriptedPaperDebugSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name:      "research_coding_agent",
		ChatModel: newPaperDebugModel(t, string(response)),
		Sandbox:   fakeSandbox,
	}

	if err := agent.ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	if fakeSandbox.calls != 2 || task.Status != models.StatusCompleted || !strings.Contains(task.Result, `"metric":1`) {
		t.Fatalf("unexpected repaired execution: calls=%d status=%s result=%s", fakeSandbox.calls, task.Status, task.Result)
	}
	artifacts := task.Metadata["artifact_values"].(map[string]string)
	var report paperCodeDebugReport
	if err := json.Unmarshal([]byte(artifacts["paper_debug_report"]), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "repaired" || len(report.Runs) != 2 || len(report.Patches) != 1 || report.Patches[0].Path != "main.py" {
		t.Fatalf("unexpected paper debug report: %#v", report)
	}
	if report.Patches[0].BeforeSHA256 == report.Patches[0].AfterSHA256 || artifacts["paper_patch_manifest"] == "" {
		t.Fatalf("missing patch evidence: %#v", report.Patches)
	}
}

func TestResearchCodingAgentDebugsResultGapAndReruns(t *testing.T) {
	original := "def evaluate():\n    return 1\n\nraise RuntimeError('BROKEN')\n"
	workspace, task := paperDebugTaskFixture(t, original)
	task.Type = "fix_and_rerun"
	task.Inputs["comparison_report"] = `{"status":"mismatch","evidence":"entry stops before evaluation"}`
	response, _ := json.Marshal(map[string]any{
		"status":    "patched",
		"diagnosis": "the comparison evidence and source both show an unconditional pre-evaluation exception",
		"patches": []map[string]any{{
			"path": "main.py", "content": "def evaluate():\n    return 1\n\nprint(evaluate())\n", "reason": "remove only the unconditional exception",
		}},
	})
	fakeSandbox := &scriptedPaperDebugSandbox{workspace: workspace}
	agent := &ResearchCodingAgent{
		Name:      "research_coding_agent",
		ChatModel: newPaperDebugModel(t, string(response)),
		Sandbox:   fakeSandbox,
	}

	if err := agent.ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	if fakeSandbox.calls != 1 || task.Status != models.StatusCompleted {
		t.Fatalf("unexpected gap-debug execution: calls=%d status=%s", fakeSandbox.calls, task.Status)
	}
	artifacts := task.Metadata["artifact_values"].(map[string]string)
	if !strings.Contains(artifacts["rerun_metrics"], `"metric":1`) || artifacts["gap_debug_report"] == "" || artifacts["gap_patch_manifest"] == "" {
		t.Fatalf("missing result-gap debug artifacts: %#v", artifacts)
	}
}

func TestResearchCodingAgentRestoresPaperCodeWhenRepairBudgetIsExhausted(t *testing.T) {
	original := "raise RuntimeError('BROKEN')\n"
	workspace, task := paperDebugTaskFixture(t, original)
	first, _ := json.Marshal(map[string]any{
		"status": "patched", "diagnosis": "first repair",
		"patches": []map[string]any{{"path": "main.py", "content": "raise RuntimeError('still broken one')\n", "reason": "first bounded repair"}},
	})
	second, _ := json.Marshal(map[string]any{
		"status": "patched", "diagnosis": "second repair",
		"patches": []map[string]any{{"path": "main.py", "content": "raise RuntimeError('still broken two')\n", "reason": "second bounded repair"}},
	})
	fakeSandbox := &scriptedPaperDebugSandbox{workspace: workspace, alwaysFail: true}
	agent := &ResearchCodingAgent{
		Name:      "research_coding_agent",
		ChatModel: newPaperDebugModel(t, string(first), string(second)),
		Sandbox:   fakeSandbox,
	}

	err := agent.ExecuteTask(context.Background(), task, nil)
	if err == nil || fakeSandbox.calls != 3 || task.Status != models.StatusFailed {
		t.Fatalf("expected bounded failure, err=%v calls=%d status=%s", err, fakeSandbox.calls, task.Status)
	}
	restored, readErr := os.ReadFile(filepath.Join(workspace, "main.py"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(restored) != original {
		t.Fatalf("paper source was not restored: %q", restored)
	}
	var report paperCodeDebugReport
	if err := json.Unmarshal([]byte(task.Result), &report); err != nil {
		t.Fatal(err)
	}
	if !report.RestoredOriginals || len(report.Patches) != 2 || report.Status != "failed" {
		t.Fatalf("unexpected failed repair report: %#v", report)
	}
}

func TestPaperCodePatchPolicyRejectsNewSideEffectsAndFakeResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		patched string
	}{
		{name: "package install", patched: "import subprocess\nsubprocess.run(['pip', 'install', 'torch'])\n"},
		{name: "network call", patched: "import requests\nrequests.get('https://example.com')\n"},
		{name: "fake model", patched: "class FakeModel:\n    pass\n"},
		{name: "validation bypass", patched: "requests.get(url, verify=False)\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validatePaperCodePatchPolicy("print(run())\n", test.patched); err == nil {
				t.Fatalf("expected policy rejection for %s", test.name)
			}
		})
	}
	if err := validatePaperCodePatchPolicy("result = evaluate(model)\n", "result = evaluate(model, batch_size=8)\n"); err != nil {
		t.Fatalf("minimal local compatibility patch was rejected: %v", err)
	}
}
