package agent

import (
	"context"
	"fmt"

	"scholar-agent-backend/internal/models"
)

// BenchmarkAgent owns dataset audit, split isolation, metric/reward contracts,
// evaluator generation, and hidden-label validation. Repository integration
// remains the responsibility of ResearchCodingAgent.
type BenchmarkAgent struct {
	Name string
}

func NewBenchmarkAgent() *BenchmarkAgent {
	return &BenchmarkAgent{Name: "benchmark_agent"}
}

func (a *BenchmarkAgent) ExecuteTask(ctx context.Context, task *models.Task, _ map[string]interface{}) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	logToContext(ctx, "[%s] executing %s", a.Name, task.Type)
	switch task.Type {
	case "benchmark_dataset_audit", "dataset_profile":
		return a.executeDatasetAudit(ctx, task)
	case "benchmark_split_materialize":
		return a.executeSplitMaterialization(ctx, task)
	case "benchmark_contract_freeze":
		return a.executeContractFreeze(ctx, task)
	case "benchmark_validate":
		return a.executeHiddenValidation(ctx, task)
	default:
		return failBenchmarkTask(task, fmt.Errorf("unsupported benchmark task type %q", task.Type))
	}
}

func setBenchmarkArtifacts(task *models.Task, values map[string]string) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = values
}

func failBenchmarkTask(task *models.Task, err error) error {
	if task != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
	}
	return err
}
