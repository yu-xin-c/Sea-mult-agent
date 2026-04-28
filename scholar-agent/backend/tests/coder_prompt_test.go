package tests

import (
	"strings"
	"testing"

	"scholar-agent-backend/internal/agent"
)

func TestBuildCoderSystemPrompt_IsGenericAndFair(t *testing.T) {
	prompt := agent.BuildCoderSystemPrompt()

	required := []string{
		"your-package-name",
		"不依赖外网 API",
		"只输出纯 Python 代码",
		"JSON 格式的结果摘要",
	}
	for _, token := range required {
		if !strings.Contains(prompt, token) {
			t.Fatalf("expected system prompt to contain %q", token)
		}
	}

	forbidden := []string{
		"LangChain",
		"LlamaIndex",
		"langchain",
		"llama-index",
		"langchain-community",
		"langchain-core",
	}
	for _, token := range forbidden {
		if strings.Contains(prompt, token) {
			t.Fatalf("system prompt must stay generic, but found %q", token)
		}
	}
}
