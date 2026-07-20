package agent

import (
	"context"
	"fmt"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/sandbox"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

type researchCodingSandbox interface {
	ExecCommandStream(ctx context.Context, sandboxID string, cmd []string, onChunk func(stream string, line string)) (*sandbox.PythonRunResponse, error)
}

// ResearchCodingAgent is the repository-aware coding sub-agent. It owns bounded
// benchmark adaptation and paper-code debugging while reusing CoderAgent for
// low-level model and sandbox capabilities.
type ResearchCodingAgent struct {
	Name      string
	ChatModel *openaiModel.ChatModel
	Sandbox   researchCodingSandbox
}

func NewResearchCodingAgent(coder *CoderAgent) *ResearchCodingAgent {
	agent := &ResearchCodingAgent{Name: "research_coding_agent"}
	if coder != nil {
		agent.ChatModel = coder.ChatModel
		agent.Sandbox = coder.Sandbox
	}
	return agent
}

func (a *ResearchCodingAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	logToContext(ctx, "[%s] executing %s", a.Name, task.Type)
	switch task.Type {
	case "dataset_profile":
		return a.executeDatasetProfile(ctx, task)
	case "benchmark_adapter_generate":
		return a.executeAdapterGeneration(ctx, task)
	case "benchmark_adapter_preflight":
		return a.executeAdapterPreflight(ctx, task)
	case "benchmark_execute":
		return a.executeBenchmark(ctx, task)
	case "benchmark_validate":
		return a.executeBenchmarkValidation(ctx, task)
	case "paper_code_execute", "fix_and_rerun":
		return a.executePaperCodeTask(ctx, task, sharedContext)
	default:
		return failResearchCodingTask(task, fmt.Errorf("unsupported research coding task type %q", task.Type))
	}
}

func setResearchCodingArtifacts(task *models.Task, values map[string]string) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = values
}

func failResearchCodingTask(task *models.Task, err error) error {
	if task != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
	}
	return err
}
