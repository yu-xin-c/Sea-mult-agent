package api

import (
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestCollectPaperSearchFields_PrefersStructuredFields(t *testing.T) {
	intentCtx := models.IntentContext{
		Entities: map[string]any{
			"paper_title": "Attention Is All You Need",
		},
	}

	fields := collectPaperSearchFields(intentCtx, "帮我找 arXiv:1706.03762 这篇论文的实现仓库")
	if got := fields["paper_arxiv_id"]; got != "1706.03762" {
		t.Fatalf("expected arxiv id 1706.03762, got %v", got)
	}
	if got := fields["paper_search_query"]; got != "1706.03762" {
		t.Fatalf("expected search query to prefer arxiv id, got %v", got)
	}
	if got := fields["paper_title"]; got != "Attention Is All You Need" {
		t.Fatalf("expected paper title to be preserved, got %v", got)
	}
}

func TestCollectPaperSearchFields_ExtractsQuotedTitle(t *testing.T) {
	fields := collectPaperSearchFields(models.IntentContext{}, `请帮我检索《Attention Is All You Need》对应的公开仓库`)
	if got := fields["paper_title"]; got != "Attention Is All You Need" {
		t.Fatalf("expected quoted paper title, got %v", got)
	}
	if got := fields["paper_search_query"]; got != "Attention Is All You Need" {
		t.Fatalf("expected search query to use extracted title, got %v", got)
	}
}
