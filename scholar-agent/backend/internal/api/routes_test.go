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

func TestCORSMiddlewareRejectsUnknownOrigin(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://trusted.example")
	router := gin.New()
	router.Use(CORSMiddleware())
	router.OPTIONS("/api/plan", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodOptions, "/api/plan", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected allow origin=%q", got)
	}
}

func TestAPIAuthMiddlewareRequiresConfiguredBearerToken(t *testing.T) {
	t.Setenv("API_AUTH_TOKEN", "deployment-secret")
	router := gin.New()
	router.Use(APIAuthMiddleware())
	router.GET("/api/private", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/private", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	request.Header.Set("Authorization", "Bearer deployment-secret")
	authorized := httptest.NewRecorder()
	router.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", authorized.Code, authorized.Body.String())
	}
}

func TestValidateRemotePDFURLBlocksPrivateNetworks(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/paper.pdf",
		"http://10.0.0.1/paper.pdf",
		"file:///tmp/paper.pdf",
		"http://user:pass@1.1.1.1/paper.pdf",
	} {
		if _, err := validateRemotePDFURL(t.Context(), raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
	if _, err := validateRemotePDFURL(t.Context(), "https://1.1.1.1/paper.pdf"); err != nil {
		t.Fatalf("expected public HTTPS address to be accepted: %v", err)
	}
}

func TestSafeRemoteTransportRechecksAddressAtDialTime(t *testing.T) {
	transport := safeRemoteTransport()
	if transport.DialContext == nil {
		t.Fatal("safe transport must provide a guarded dialer")
	}
	if _, err := transport.DialContext(t.Context(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("expected dial-time loopback address to be rejected")
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

func TestDirectBenchmarkTasksReuseDAGRuntime(t *testing.T) {
	if shouldAllocateDirectSandbox(&models.Task{AssignedTo: "research_coding_agent", Type: "dataset_profile"}) {
		t.Fatal("dataset profile should not allocate an unrelated direct sandbox")
	}
	if !shouldAllocateDirectSandbox(&models.Task{AssignedTo: "sandbox_agent", Type: "execute_code"}) {
		t.Fatal("ordinary sandbox task should keep direct sandbox allocation")
	}
}
