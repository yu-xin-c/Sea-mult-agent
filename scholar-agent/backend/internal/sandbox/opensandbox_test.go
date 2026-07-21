package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSandboxClientSendsInternalBearerToken(t *testing.T) {
	t.Setenv("SANDBOX_API_TOKEN", "internal-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer internal-secret" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SandboxCreateResponse{SandboxID: "dk-test"})
	}))
	defer server.Close()

	client := NewSandboxClient(server.URL)
	id, err := client.CreatePersistentSandbox(context.Background(), "task-1", "python:3.11", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "dk-test" {
		t.Fatalf("sandbox id=%q", id)
	}
}
