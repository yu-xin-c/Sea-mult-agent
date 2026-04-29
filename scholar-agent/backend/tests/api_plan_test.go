package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDetectIntentType_PaperReproductionBeatsComparisonKeyword(t *testing.T) {
	intent := "请复现论文《Attention Is All You Need》，通过 Papers with Code 找到真实开源实现，运行最小实验并和论文指标对比"
	if got := apiinternal.DetectIntentType(intent); got != "Paper_Reproduction" {
		t.Fatalf("DetectIntentType() = %q, want %q", got, "Paper_Reproduction")
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
	if len(response.Plan.Tasks) != 7 {
		t.Fatalf("expected 7 tasks, got %d", len(response.Plan.Tasks))
	}
}

func TestPlanRoute_PaperReproductionExample(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiinternal.RegisterPlanRoute(apiGroup, planner.NewPlanner())

	body := []byte(`{"intent":"请复现论文《Attention Is All You Need》，通过 Papers with Code 找到真实开源实现，运行最小实验并和论文指标对比"}`)
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
				Name       string   `json:"Name"`
				AssignedTo string   `json:"AssignedTo"`
				DependsOn  []string `json:"Dependencies"`
			} `json:"Tasks"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Message != "Plan generated successfully" {
		t.Fatalf("unexpected message: %q", response.Message)
	}
	if len(response.Plan.Tasks) != 6 {
		t.Fatalf("expected 6 paper reproduction tasks, got %d", len(response.Plan.Tasks))
	}

	var librarianCount, coderCount, sandboxCount, dataCount int
	var sawPaperParse, sawRepository, sawPaperCompare bool
	for _, task := range response.Plan.Tasks {
		switch task.AssignedTo {
		case "librarian_agent":
			librarianCount++
		case "coder_agent":
			coderCount++
		case "sandbox_agent":
			sandboxCount++
		case "data_agent":
			dataCount++
		}
		name := strings.ToLower(task.Name)
		sawPaperParse = sawPaperParse || strings.Contains(name, "parse paper") || strings.Contains(task.Name, "解析论文")
		sawRepository = sawRepository || strings.Contains(name, "repository") || strings.Contains(task.Name, "开源仓库")
		sawPaperCompare = sawPaperCompare || strings.Contains(name, "compare results with paper") || strings.Contains(task.Name, "论文进行对比")
	}

	if librarianCount != 1 || coderCount != 2 || sandboxCount != 2 || dataCount != 1 {
		t.Fatalf("unexpected task distribution: librarian=%d coder=%d sandbox=%d data=%d", librarianCount, coderCount, sandboxCount, dataCount)
	}
	if !sawPaperParse || !sawRepository || !sawPaperCompare {
		t.Fatalf("paper reproduction plan missing canonical steps: parse=%t repository=%t compare=%t", sawPaperParse, sawRepository, sawPaperCompare)
	}
}

func TestPlanRoute_PaperFullReproductionClarification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiinternal.RegisterPlanRoute(apiGroup, planner.NewPlanner())

	body := []byte(`{"intent":"请真实复现论文 Attention Is All You Need，跑 WMT14 BLEU 并和论文指标对比"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Clarification struct {
			Required        bool   `json:"required"`
			Type            string `json:"type"`
			RecommendedMode string `json:"recommended_mode"`
			Question        string `json:"question"`
			ResourceProbe   struct {
				CPUCount int `json:"cpu_count"`
				GPUCount int `json:"gpu_count"`
			} `json:"resource_probe"`
		} `json:"clarification"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !response.Clarification.Required {
		t.Fatalf("expected clarification to be required, body=%s", rec.Body.String())
	}
	if response.Clarification.Type != "paper_reproduction_mode" {
		t.Fatalf("unexpected clarification type: %q", response.Clarification.Type)
	}
	if response.Clarification.Question == "" || !strings.Contains(response.Clarification.Question, "CUDA GPU") {
		t.Fatalf("expected resource-aware question, got %q", response.Clarification.Question)
	}
	if response.Clarification.RecommendedMode == "" {
		t.Fatalf("expected recommended mode")
	}
	if response.Clarification.ResourceProbe.CPUCount <= 0 {
		t.Fatalf("expected cpu resource probe, got %+v", response.Clarification.ResourceProbe)
	}
}
