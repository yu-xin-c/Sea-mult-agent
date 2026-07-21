package sandboxserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxAuthMiddlewareProtectsExecutionRoutes(t *testing.T) {
	t.Setenv("SANDBOX_API_TOKEN", "sandbox-secret")
	handler := NewHandler(Config{})

	unauthorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(`{"image":"python:3.11"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	unknownRoute := httptest.NewRecorder()
	authorized := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/unknown", nil)
	authorized.Header.Set("Authorization", "Bearer sandbox-secret")
	handler.ServeHTTP(unknownRoute, authorized)
	if unknownRoute.Code == http.StatusUnauthorized {
		t.Fatalf("configured token was rejected: body=%s", unknownRoute.Body.String())
	}
}
