package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

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

func TestShouldAttemptPythonRuntimeCodeRepair_SyntaxAndAPIKey(t *testing.T) {
	cases := []string{
		"SyntaxError: f-string: invalid syntax",
		"openai.AuthenticationError: Error code: 401 - invalid_api_key",
		"Incorrect API key provided: sk-placeholder",
	}
	for _, errText := range cases {
		if !shouldAttemptPythonRuntimeCodeRepair(errText) {
			t.Fatalf("expected %q to trigger runtime code repair", errText)
		}
	}
}

func TestFrameworkBenchmarkCodeConstraints(t *testing.T) {
	required := []string{
		"框架对比 / RAG Benchmark",
		"离线可跑",
		"禁止在代码中写入 sk-placeholder",
		"本地 mock/fake LLM",
		"Python 3.9 语法",
	}
	for _, want := range required {
		if !strings.Contains(prompts.FrameworkBenchmarkCodeConstraints, want) {
			t.Fatalf("frameworkBenchmarkCodeConstraints missing %q", want)
		}
	}
}

func TestCoderSystemPromptForTask_IsolatesFrameworkAndPaperModes(t *testing.T) {
	frameworkPrompt := prompts.CoderSystemPromptForTask("Framework_Evaluation", "generate_code", "Generate LangChain Benchmark Code", "Plan intent type: Framework_Evaluation")
	if !strings.Contains(frameworkPrompt, "框架对比 / RAG Benchmark") {
		t.Fatalf("expected framework benchmark prompt to include framework constraints")
	}
	if strings.Contains(frameworkPrompt, "论文复现硬性约束") {
		t.Fatalf("framework prompt must not include paper reproduction constraints")
	}

	paperPrompt := prompts.CoderSystemPromptForTask("Paper_Reproduction", "repo_prepare", "Prepare Workspace", "Plan intent type: Paper_Reproduction")
	if !strings.Contains(paperPrompt, "论文复现硬性约束") {
		t.Fatalf("expected paper reproduction prompt to include paper constraints")
	}
	if strings.Contains(paperPrompt, "框架对比 / RAG Benchmark") {
		t.Fatalf("paper prompt must not include framework benchmark constraints")
	}

	genericPrompt := prompts.CoderSystemPromptForTask("Code_Execution", "generate_code", "Generate Code", "draw a plot")
	if strings.Contains(genericPrompt, "框架对比 / RAG Benchmark") || strings.Contains(genericPrompt, "论文复现硬性约束") {
		t.Fatalf("generic code prompt should not include framework or paper-specific constraints")
	}
}

func TestDeterministicFrameworkBenchmarkCode_IsolatesBranchDependencies(t *testing.T) {
	langchainTask := &models.Task{
		Name:            "生成 LangChain 基准测试代码 / Generate LangChain Benchmark Code",
		Type:            "generate_code",
		OutputArtifacts: []string{"langchain_generated_code"},
	}
	code, ok := deterministicFrameworkBenchmarkCode(langchainTask)
	if !ok {
		t.Fatalf("expected deterministic LangChain benchmark code")
	}
	if strings.Contains(code, "pip install") || strings.Contains(code, "llama_index") || strings.Contains(code, "importlib.metadata") {
		t.Fatalf("LangChain deterministic benchmark should not include inline pip, LlamaIndex code, or package metadata lookup")
	}
	deps := filterFrameworkBenchmarkDependencies(langchainTask, []string{"langchain", "llama-index", "sentence-transformers", "numpy"})
	if len(deps) != 0 {
		t.Fatalf("offline LangChain benchmark should not install dependencies: %v", deps)
	}

	llamaTask := &models.Task{
		Name:            "生成 LlamaIndex 基准测试代码 / Generate LlamaIndex Benchmark Code",
		Type:            "generate_code",
		OutputArtifacts: []string{"llamaindex_generated_code"},
	}
	code, ok = deterministicFrameworkBenchmarkCode(llamaTask)
	if !ok {
		t.Fatalf("expected deterministic LlamaIndex benchmark code")
	}
	if strings.Contains(code, "pip install") || strings.Contains(code, "import langchain") || strings.Contains(code, "importlib.metadata") {
		t.Fatalf("LlamaIndex deterministic benchmark should not include inline pip, LangChain code, or package metadata lookup")
	}
	deps = filterFrameworkBenchmarkDependencies(llamaTask, []string{"llama-index", "langchain", "chromadb", "faiss-cpu"})
	if len(deps) != 0 {
		t.Fatalf("offline LlamaIndex benchmark should not install dependencies: %v", deps)
	}
}

func TestFilterWorkspaceLocalDependencies_RemovesRepoModules(t *testing.T) {
	workspace := t.TempDir()
	for _, file := range []string{
		"src/config.py",
		"src/learner.py",
		"src/scheduler.py",
		"src/dataset.py",
		"src/callbacks.py",
		"src/architectures/__init__.py",
		"src/utils/__init__.py",
	} {
		path := filepath.Join(workspace, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# local module\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deps := filterWorkspaceLocalDependencies(
		[]string{"torch", "wandb", "config", "learner", "scheduler", "dataset", "callbacks", "architectures", "utils", "numpy"},
		workspace,
	)
	if strings.Join(deps, ",") != "torch,wandb,numpy" {
		t.Fatalf("unexpected filtered deps: %v", deps)
	}
}

func TestNormalizeDependenciesForPython39_HandlesLegacyPaperRequirements(t *testing.T) {
	deps := normalizeDependenciesForPython39([]string{
		"python==3.6.12",
		"pytorch==1.3.1",
		"msgpack-python==1.0.2",
		"tensorflow==1.14.0",
		"numpy",
	})
	if strings.Join(deps, ",") != "torch,msgpack,numpy" {
		t.Fatalf("unexpected normalized deps: %v", deps)
	}
}

func TestDetectPythonDependencies_FiltersExpandedStdlibSet(t *testing.T) {
	code := `
from __future__ import division
import inspect
import codecs
import glob
import urllib.request
import tarfile
import torch
`
	deps := detectPythonDependencies(code)
	if strings.Join(deps, ",") != "torch" {
		t.Fatalf("unexpected deps: %v", deps)
	}
}

func TestResolveDependenciesTask_SmokeRunnerIgnoresHeavyRepoScripts(t *testing.T) {
	workspace := t.TempDir()
	for _, file := range []string{
		"scholar_repro_smoke.py",
		"src/train.py",
	} {
		path := filepath.Join(workspace, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	smokePath := filepath.Join(workspace, "scholar_repro_smoke.py")
	if err := os.WriteFile(smokePath, []byte("import json\nimport torch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "train.py"), []byte("import wandb\nfrom datasets import load_dataset\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	task := &models.Task{
		Type: "resolve_dependencies",
		Inputs: map[string]any{
			"workspace_path": workspace,
			"code_file_path": smokePath,
			"generated_code": "import json\nimport torch\n",
		},
	}
	if err := (&CoderAgent{}).resolveDependenciesTask(context.Background(), task); err != nil {
		t.Fatalf("resolveDependenciesTask returned error: %v", err)
	}
	if task.Result != `["torch"]` {
		t.Fatalf("expected only torch dependency, got %s", task.Result)
	}
}
