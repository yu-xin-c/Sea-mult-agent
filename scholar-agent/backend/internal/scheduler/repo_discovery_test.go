package scheduler

import (
	"context"
	"strings"
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

func TestCuratedRepoFallbackCandidates_AttentionPaper(t *testing.T) {
	candidates := curatedRepoFallbackCandidates("Attention Is All You Need")
	if len(candidates) == 0 {
		t.Fatalf("expected curated fallback candidate")
	}
	if candidates[0].RepoURLs[0] != "https://github.com/harvardnlp/annotated-transformer" {
		t.Fatalf("unexpected curated repo: %+v", candidates[0])
	}

	report := buildRepoDiscoveryReport(
		"Attention Is All You Need",
		candidates,
		candidates[0].RepoURLs[0],
		true,
		true,
		"context deadline exceeded",
	)
	if !strings.Contains(report, "Papers API warning") || !strings.Contains(report, "Selected repo_url: https://github.com/harvardnlp/annotated-transformer") {
		t.Fatalf("fallback report missing warning or selected repo:\n%s", report)
	}
}

func TestTrustedRepoCandidate_PrefersAnnotatedTransformerForAttention(t *testing.T) {
	candidate := repoCandidate{
		RepoName:    "harvardnlp/annotated-transformer",
		Description: "An annotated implementation of the Transformer paper.",
		RepoURLs:    []string{"https://github.com/harvardnlp/annotated-transformer"},
		Source:      "github_search",
	}
	if !isTrustedRepoCandidate("Attention Is All You Need", candidate) {
		t.Fatalf("expected annotated-transformer to be trusted for Attention Is All You Need")
	}
}

func TestPreferredRepositoryInputIsValidGitHubURL(t *testing.T) {
	preferred := normalizeGitHubRepoURL("https://github.com/harvardnlp/annotated-transformer.git")
	if githubURLRe.FindString(preferred) != preferred {
		t.Fatalf("preferred repository was not accepted: %q", preferred)
	}
}

func TestRepoDiscoveryUsesPreferredRepositoryWithoutPaperSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task := &models.Task{Inputs: map[string]any{
		"preferred_repo_url": "https://github.com/example/research-repo",
	}}
	if err := executeRepoDiscovery(ctx, task); err != nil {
		t.Fatal(err)
	}
	artifacts, ok := task.Metadata["artifact_values"].(map[string]any)
	if !ok || artifacts["repo_url"] != "https://github.com/example/research-repo" {
		t.Fatalf("unexpected preferred repository artifacts: %#v", task.Metadata)
	}
	if task.Status != models.StatusCompleted || task.Result != "https://github.com/example/research-repo" {
		t.Fatalf("preferred repository was not selected: %#v", task)
	}
}
