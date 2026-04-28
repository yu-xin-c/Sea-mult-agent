package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apiinternal "scholar-agent-backend/internal/api"
	"scholar-agent-backend/internal/planner"

	"github.com/gin-gonic/gin"
)

func TestDetectIntentType_FrameworkEvaluationExample(t *testing.T) {
	intent := "比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子"
	if got := apiinternal.DetectIntentType(intent); got != "Framework_Evaluation" {
		t.Fatalf("DetectIntentType() = %q, want %q", got, "Framework_Evaluation")
	}
}

func TestDetectIntentType_DoesNotMisclassifyGenericQuestion(t *testing.T) {
	intent := "介绍一下多智能体系统的基本概念"
	if got := apiinternal.DetectIntentType(intent); got != "General" {
		t.Fatalf("DetectIntentType() = %q, want %q", got, "General")
	}
}

func TestPlanRoute_FrameworkEvaluationExample(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiinternal.RegisterPlanRoute(apiGroup, planner.NewPlanner())

	body := []byte(`{"intent":"比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子，并给出客观对比"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Message string `json:"message"`
		Plan    struct {
			Tasks map[string]struct {
				AssignedTo   string   `json:"AssignedTo"`
				Description  string   `json:"Description"`
				Dependencies []string `json:"Dependencies"`
			} `json:"Tasks"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Message != "Plan generated successfully" {
		t.Fatalf("unexpected message: %q", response.Message)
	}
	if len(response.Plan.Tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(response.Plan.Tasks))
	}
}
