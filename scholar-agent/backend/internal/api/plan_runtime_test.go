package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scholar-agent-backend/internal/events"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/scheduler"
	"scholar-agent-backend/internal/store"

	"github.com/gin-gonic/gin"
)

type completingAgentRunner struct{}

func (r *completingAgentRunner) ExecuteTask(_ context.Context, task *models.Task, _ map[string]interface{}) error {
	task.Status = models.StatusCompleted
	task.Result = fmt.Sprintf("completed %s with %d inputs", task.Type, len(task.Inputs))
	if task.Type == "generate_code" {
		task.Code = "print('project runtime')"
	}
	return nil
}

func TestPlanRuntimeExecutesStoredGraphAndReplaysEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	agentRunner := &completingAgentRunner{}
	executor := scheduler.NewRoutedTaskExecutor(agentRunner, agentRunner, agentRunner)
	planScheduler := scheduler.NewScheduler(planStore, executor, eventBus, 1)
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, planScheduler, eventBus)

	router := gin.New()
	runtime.register(router.Group("/api"))

	planRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/plan",
		bytes.NewBufferString(`{"intent":"运行一段 Python 代码并分析结果"}`),
	)
	planRequest.Header.Set("Content-Type", "application/json")
	planResponse := httptest.NewRecorder()
	router.ServeHTTP(planResponse, planRequest)
	if planResponse.Code != http.StatusOK {
		t.Fatalf("create plan status=%d body=%s", planResponse.Code, planResponse.Body.String())
	}

	var created struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
		Plan      models.Plan      `json:"plan"`
	}
	if err := json.Unmarshal(planResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create plan response: %v", err)
	}
	if created.PlanGraph.ID == "" || created.PlanGraph.ID != created.Plan.ID {
		t.Fatalf("plan identifiers are not compatible: graph=%q legacy=%q", created.PlanGraph.ID, created.Plan.ID)
	}
	if len(created.PlanGraph.Nodes) < 5 {
		t.Fatalf("expected executable graph, got %d nodes", len(created.PlanGraph.Nodes))
	}

	executeRequest := httptest.NewRequest(http.MethodPost, "/api/plans/"+created.PlanGraph.ID+"/execute", nil)
	executeResponse := httptest.NewRecorder()
	router.ServeHTTP(executeResponse, executeRequest)
	if executeResponse.Code != http.StatusAccepted {
		t.Fatalf("execute plan status=%d body=%s", executeResponse.Code, executeResponse.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		plan, err := planStore.GetPlan(created.PlanGraph.ID)
		if err != nil {
			t.Fatalf("get stored plan: %v", err)
		}
		if plan.Status == models.StatusCompleted {
			if plan.Meta.CompletedNodes != len(plan.Nodes) {
				t.Fatalf("completed nodes=%d total=%d", plan.Meta.CompletedNodes, len(plan.Nodes))
			}
			if len(plan.Artifacts) < len(plan.Nodes) {
				t.Fatalf("expected task artifacts, got %d for %d nodes", len(plan.Artifacts), len(plan.Nodes))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plan did not complete, last status=%s", plan.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/api/plans/"+created.PlanGraph.ID+"/events", nil)
	eventsResponse := httptest.NewRecorder()
	router.ServeHTTP(eventsResponse, eventsRequest)
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", eventsResponse.Code, eventsResponse.Body.String())
	}
	for _, eventType := range []string{"plan_started", "task_completed", "artifact_created", "plan_completed"} {
		if !strings.Contains(eventsResponse.Body.String(), `"event_type":"`+eventType+`"`) {
			t.Fatalf("missing event %s in %s", eventType, eventsResponse.Body.String())
		}
	}

	streamRequest := httptest.NewRequest(http.MethodGet, "/api/plans/"+created.PlanGraph.ID+"/stream", nil)
	streamResponse := httptest.NewRecorder()
	router.ServeHTTP(streamResponse, streamRequest)
	if streamResponse.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", streamResponse.Code, streamResponse.Body.String())
	}
	if !strings.Contains(streamResponse.Body.String(), "event: plan_event") ||
		!strings.Contains(streamResponse.Body.String(), `"event_type":"plan_completed"`) {
		t.Fatalf("terminal event was not replayed over SSE: %s", streamResponse.Body.String())
	}
}

func TestPlanRuntimeRejectsUnknownPlan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	planScheduler := scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1)
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, planScheduler, eventBus)

	router := gin.New()
	runtime.register(router.Group("/api"))

	request := httptest.NewRequest(http.MethodPost, "/api/plans/missing/execute", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBuildRuleIntentContextPreservesExplicitSmokeAndPreferredRepository(t *testing.T) {
	intent := buildRuleIntentContext(
		"复现论文 Attention Is All You Need，明确采用 smoke，不跑 WMT14 BLEU，优先 https://github.com/harvardnlp/annotated-transformer.git 做消融",
	)
	if got := intent.Entities["preferred_repo_url"]; got != "https://github.com/harvardnlp/annotated-transformer" {
		t.Fatalf("preferred_repo_url=%v", got)
	}
	if got := intent.Constraints["reproduction_mode"]; got != "smoke" {
		t.Fatalf("reproduction_mode=%v", got)
	}
	if shouldClarifyPaperReproductionMode(intent.RawIntent) {
		t.Fatal("explicit smoke request should not require full reproduction clarification")
	}
}
