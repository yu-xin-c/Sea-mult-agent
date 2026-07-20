package scheduler

import (
	"context"
	"testing"

	"scholar-agent-backend/internal/models"
)

type executorTestRunner struct{}

func (r *executorTestRunner) ExecuteTask(_ context.Context, _ *models.Task, _ map[string]interface{}) error {
	return nil
}

func TestBuildTaskInputs_MergesNodeInputsAndArtifacts(t *testing.T) {
	plan := &models.PlanGraph{
		Artifacts: map[string]models.Artifact{
			"parsed_paper": {
				Key:   "parsed_paper",
				Value: "论文标题：Attention Is All You Need",
			},
		},
	}
	task := &models.TaskNode{
		Inputs: map[string]any{
			"paper_title": "Attention Is All You Need",
		},
		RequiredArtifacts: []string{"parsed_paper"},
	}

	inputs := buildTaskInputs(plan, task)
	if got := inputs["paper_title"]; got != "Attention Is All You Need" {
		t.Fatalf("expected node input paper_title to be preserved, got %v", got)
	}
	if got := inputs["parsed_paper"]; got != "论文标题：Attention Is All You Need" {
		t.Fatalf("expected artifact parsed_paper to be merged, got %v", got)
	}
}

func TestRoutedExecutorUsesBenchmarkAdapterRunner(t *testing.T) {
	defaultRunner := &executorTestRunner{}
	benchmarkRunner := &executorTestRunner{}
	executor := NewRoutedTaskExecutor(defaultRunner, defaultRunner, defaultRunner, benchmarkRunner)
	runner, err := executor.resolveRunner("benchmark_adapter_agent")
	if err != nil {
		t.Fatal(err)
	}
	if runner != benchmarkRunner {
		t.Fatal("benchmark adapter task was not routed to its dedicated runner")
	}
}
