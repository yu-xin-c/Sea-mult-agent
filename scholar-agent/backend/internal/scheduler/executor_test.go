package scheduler

import (
	"testing"

	"scholar-agent-backend/internal/models"
)

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
