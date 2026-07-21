package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestUploadLifecycleEnforcesOwnership(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "paper-notes.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("# Paper\nAblate the residual module."))
	_ = writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/uploads", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set(userIDHeaderName, "upload-owner")
	request.Header.Set(sessionHeaderName, "upload-session")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var uploaded uploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.ID == "" || uploaded.SHA256 == "" || uploaded.Size == 0 {
		t.Fatalf("invalid upload response: %#v", uploaded)
	}

	ownedRequest := httptest.NewRequest(http.MethodGet, uploaded.ContentURL, nil)
	ownedRequest.Header.Set(userIDHeaderName, "upload-owner")
	ownedResponse := httptest.NewRecorder()
	router.ServeHTTP(ownedResponse, ownedRequest)
	if ownedResponse.Code != http.StatusOK || !bytes.Contains(ownedResponse.Body.Bytes(), []byte("Ablate the residual")) {
		t.Fatalf("owned fetch status=%d body=%s", ownedResponse.Code, ownedResponse.Body.String())
	}

	foreignRequest := httptest.NewRequest(http.MethodGet, uploaded.ContentURL, nil)
	foreignRequest.Header.Set(userIDHeaderName, "other-owner")
	foreignResponse := httptest.NewRecorder()
	router.ServeHTTP(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign fetch status=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
}

func TestResolvePlanUploadsIncludesTextExcerpt(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	metadata := uploadMetadata{
		ID: "0f18220e-5fca-46de-bf84-4abf2b9ebafa", Name: "config.yaml", ContentType: "text/plain",
		OwnerID: "owner", SessionID: "session", CreatedAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
	}
	directory := uploadDirectory(metadata.OwnerID, metadata.ID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata.StoredPath = filepath.Join(directory, "content.yaml")
	content := []byte("batch_size: 8\nseed: 47\n")
	if err := os.WriteFile(metadata.StoredPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata.Size = int64(len(content))
	metadata.SHA256 = "test"
	if err := writeUploadMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePlanUploads("owner", []string{metadata.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0]["text_excerpt"] != string(content) {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func TestJSONLUploadIsAcceptedAsBenchmarkData(t *testing.T) {
	if !allowedUploadExtension(".jsonl") {
		t.Fatal("jsonl extension should be accepted")
	}
	if !allowedUploadContent(".jsonl", "text/plain; charset=utf-8") {
		t.Fatal("newline-delimited JSON should accept detected text content")
	}
	if got := normalizedUploadContentType(".jsonl", "text/plain; charset=utf-8"); got != "application/json" {
		t.Fatalf("normalized content type=%q", got)
	}
}
