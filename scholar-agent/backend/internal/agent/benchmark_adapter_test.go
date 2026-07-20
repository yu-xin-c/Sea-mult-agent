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

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

func benchmarkTestAdapterCode(marker string) string {
	return `import argparse
import hashlib

parser = argparse.ArgumentParser()
parser.add_argument("--dataset")
parser.add_argument("--output-dir")
parser.add_argument("--limit", type=int)
parser.add_argument("--repo-root")
parser.add_argument("--seed", type=int, default=17)
args = parser.parse_args()
dataset_sha256 = hashlib.sha256(open(args.dataset, "rb").read()).hexdigest()
outputs = ("metrics.json", "predictions.jsonl", "run_manifest.json")
print(dataset_sha256, outputs, args.output_dir, args.repo_root, args.limit)
` + "# " + marker
}

func newSequentialBenchmarkModel(t *testing.T, responses ...string) *openaiModel.ChatModel {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("unexpected model request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(string(body), "Dataset manifest") && !strings.Contains(string(body), "Repair the benchmark adapter") {
			t.Fatalf("model request omitted benchmark context: %s", string(body))
		}
		index := int(calls.Add(1)) - 1
		if index >= len(responses) {
			t.Fatalf("unexpected model call %d", index+1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-benchmark", "object": "chat.completion", "created": 1, "model": "test-model",
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

func TestBenchmarkAdapterGenerationUsesBoundedPlanAndScopedFiles(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("Run python evaluate.py with a local model."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "evaluate.py"), []byte("def predict(text):\n    return text.upper()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := models.DatasetManifest{
		Version: "benchmark.dataset/v1", Name: "reviews.csv", Format: "csv", SHA256: strings.Repeat("a", 64),
		RowCount: 3, InputColumn: "review", TargetColumn: "label", SuggestedTask: "classification",
	}
	manifestJSON, _ := json.Marshal(manifest)
	planJSON := `{"status":"ready","candidates":[{"kind":"native_eval","entrypoint":"evaluate.py:predict","confidence":0.92,"evidence":"README and evaluate.py expose predict"}],"selected_index":0,"reason":"native API is documented"}`
	generationPayload, _ := json.Marshal(map[string]any{
		"status": "ready", "strategy": "native_eval", "entrypoint": "evaluate.py:predict", "confidence": 0.9,
		"metrics": []string{"accuracy"}, "dependencies": []string{}, "reason": "uses repository prediction function",
		"adapter_code": benchmarkTestAdapterCode("generated"),
	})
	agent := &ResearchCodingAgent{
		Name:      "research_coding_agent",
		ChatModel: newSequentialBenchmarkModel(t, planJSON, string(generationPayload)),
	}
	task := &models.Task{
		Type:        "benchmark_adapter_generate",
		Description: "Run a lightweight benchmark",
		Inputs: map[string]any{
			"workspace_path":   workspace,
			"dataset_manifest": string(manifestJSON),
		},
	}

	if err := agent.ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	artifacts, ok := task.Metadata["artifact_values"].(map[string]string)
	if !ok {
		t.Fatalf("missing adapter artifacts: %#v", task.Metadata)
	}
	if artifacts["benchmark_adapter_plan"] == "" || artifacts["benchmark_adapter_spec"] == "" {
		t.Fatalf("missing structured adapter artifacts: %#v", artifacts)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	adapterPath := filepath.Join(canonicalWorkspace, ".scholar", "benchmark", "adapter.py")
	storedCode, err := os.ReadFile(adapterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(storedCode) != benchmarkTestAdapterCode("generated") || artifacts["benchmark_code_file_path"] != adapterPath {
		t.Fatalf("adapter file did not match generated artifact")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".scholar", "benchmark", "benchmark.json")); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBenchmarkAdapterCodeRejectsNetworkAndInstallCommands(t *testing.T) {
	for _, addition := range []string{
		"\nimport requests\nrequests.get('https://example.com')",
		"\nimport subprocess\nsubprocess.run(['python', 'eval.py'])",
		"\n# pip install torch",
	} {
		if err := validateBenchmarkAdapterCode(benchmarkTestAdapterCode("unsafe") + addition); err == nil {
			t.Fatalf("expected policy rejection for %q", addition)
		}
	}
}

func TestWriteBenchmarkAdapterRejectsScholarSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, ".scholar")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeBenchmarkAdapterFiles(workspace, benchmarkTestAdapterCode("safe"), []byte(`{}`)); err == nil {
		t.Fatal("expected .scholar symlink to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "benchmark", "adapter.py")); !os.IsNotExist(err) {
		t.Fatalf("adapter escaped workspace through symlink: %v", err)
	}
}
