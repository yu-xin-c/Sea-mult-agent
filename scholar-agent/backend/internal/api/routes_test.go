package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"

	"github.com/gin-gonic/gin"
)

func TestCORSMiddlewareAllowsIdentityHeadersWithCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORSMiddleware())
	router.OPTIONS("/api/plan", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodOptions, "/api/plan", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-user-id,x-session-id")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("allow origin=%q", got)
	}
	allowHeaders := response.Header().Get("Access-Control-Allow-Headers")
	for _, header := range []string{"X-User-Id", "X-Session-Id"} {
		if !strings.Contains(allowHeaders, header) {
			t.Fatalf("missing %s in Access-Control-Allow-Headers=%q", header, allowHeaders)
		}
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials=%q", got)
	}
}

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
