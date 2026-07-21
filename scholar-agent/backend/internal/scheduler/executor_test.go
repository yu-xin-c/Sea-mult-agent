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

func TestRoutedExecutorUsesResearchCodingRunner(t *testing.T) {
	defaultRunner := &executorTestRunner{}
	researchCodingRunner := &executorTestRunner{}
	executor := NewRoutedTaskExecutor(defaultRunner, defaultRunner, defaultRunner, researchCodingRunner)
	runner, err := executor.resolveRunner("research_coding_agent")
	if err != nil {
		t.Fatal(err)
	}
	if runner != researchCodingRunner {
		t.Fatal("research coding task was not routed to its dedicated runner")
	}
}

func TestClaimArtifactsUseJSONType(t *testing.T) {
	for _, key := range []string{"claim_rubric", "claim_evidence_graph"} {
		if got := inferArtifactType(key, &models.Task{}); got != "json" {
			t.Fatalf("inferArtifactType(%q)=%q, want json", key, got)
		}
	}
	if got := inferArtifactType("claim_verification_report", &models.Task{}); got != "report" {
		t.Fatalf("claim verification report type=%q, want report", got)
	}
}
