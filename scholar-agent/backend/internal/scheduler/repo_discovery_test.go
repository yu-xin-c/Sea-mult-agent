package scheduler

import (
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestBuildRepoDiscoveryQuery_PrefersStructuredInputs(t *testing.T) {
	task := &models.Task{
		Description: "任务目标: 检索并定位论文对应的高可信公开仓库",
		Inputs: map[string]any{
			"paper_title":        "Attention Is All You Need",
			"paper_search_query": "Transformer",
			"parsed_paper":       "论文标题：Some Other Paper",
		},
	}

	query := buildRepoDiscoveryQuery(task)
	if query != "Attention Is All You Need" {
		t.Fatalf("expected structured paper_title to win, got %q", query)
	}
}
