package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

func newMockOpenAIChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("expected /chat/completions path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": content,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func newTestChatModel(t *testing.T, content string) *openaiModel.ChatModel {
	t.Helper()
	server := newMockOpenAIChatServer(t, content)
	t.Cleanup(server.Close)

	model, err := openaiModel.NewChatModel(context.Background(), &openaiModel.ChatModelConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewChatModel returned error: %v", err)
	}
	return model
}

// TestPlanDependencyRecovery_RemoveStdlib 验证：通过真实 ChatModel 请求本地 mock 服务，
// 当 pip 报错中出现标准库（如 shutil）时，ReAct 计划会返回 remove_package。
func TestPlanDependencyRecovery_RemoveStdlib(t *testing.T) {
	agent := &CoderAgent{
		ChatModel: newTestChatModel(t, `{"action":"remove_package","reason":"shutil 是标准库，不能通过 pip 安装","remove_package":"shutil"}`),
	}

	deps := []string{"numpy", "shutil"}
	plan, err := agent.planDependencyRecovery(context.Background(), deps, "ERROR: No matching distribution found for shutil")
	if err != nil {
		t.Fatalf("planDependencyRecovery returned error: %v", err)
	}
	if plan.Action != "remove_package" || plan.RemovePackage != "shutil" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

// TestPlanDependencyRecovery_UpgradePython 验证：通过真实 ChatModel 请求本地 mock 服务，
// 当 pip 日志提示 Requires-Python 不满足时，ReAct 计划会选择 upgrade_python。
func TestPlanDependencyRecovery_UpgradePython(t *testing.T) {
	agent := &CoderAgent{
		ChatModel: newTestChatModel(t, `{"action":"upgrade_python","reason":"依赖要求 Python>=3.11","target_image":"python:3.11-bullseye"}`),
	}

	deps := []string{"llama-index"}
	plan, err := agent.planDependencyRecovery(context.Background(), deps, "Ignored the following versions that require a different python version; Requires-Python >=3.11")
	if err != nil {
		t.Fatalf("planDependencyRecovery returned error: %v", err)
	}
	if plan.Action != "upgrade_python" || plan.TargetImage != "python:3.11-bullseye" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestMapPythonImportToPackage_LlamaIndexPlugin(t *testing.T) {
	got := mapPythonImportToPackage("llama_index.llms.openai")
	if got != "llama-index-llms-openai" {
		t.Fatalf("expected llama-index-llms-openai, got %q", got)
	}
}

func TestShouldAttemptPythonRuntimeCodeRepair_ImportError(t *testing.T) {
	errText := "ImportError: cannot import name 'OpenAI' from 'llama_index.core.llms'"
	if !shouldAttemptPythonRuntimeCodeRepair(errText) {
		t.Fatalf("expected import compatibility error to trigger runtime code repair")
	}
}
