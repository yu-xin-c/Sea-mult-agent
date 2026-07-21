package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	planRequest.Header.Set(userIDHeaderName, "runtime-test-user")
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
	executeRequest.Header.Set(userIDHeaderName, "runtime-test-user")
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
	eventsRequest.Header.Set(userIDHeaderName, "runtime-test-user")
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
	streamRequest.Header.Set(userIDHeaderName, "runtime-test-user")
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

func TestPlanRuntimeEnforcesOwnershipAndApproval(t *testing.T) {
	t.Setenv("REQUIRE_PLAN_APPROVAL", "true")
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	runtime := newPlanRuntime(
		planner.NewPlanner(),
		planStore,
		scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1),
		eventBus,
	)
	router := gin.New()
	runtime.register(router.Group("/api"))

	create := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewBufferString(`{"intent":"运行 Python 代码"}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set(userIDHeaderName, "owner-a")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.PlanGraph.Status != models.StatusAwaitingApproval || !created.PlanGraph.Approval.Required {
		t.Fatalf("expected approval gate, got status=%s approval=%#v", created.PlanGraph.Status, created.PlanGraph.Approval)
	}

	foreign := httptest.NewRequest(http.MethodGet, "/api/plans/"+created.PlanGraph.ID, nil)
	foreign.Header.Set(userIDHeaderName, "owner-b")
	foreignResponse := httptest.NewRecorder()
	router.ServeHTTP(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusForbidden {
		t.Fatalf("foreign access status=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}

	executeBeforeApproval := httptest.NewRequest(http.MethodPost, "/api/plans/"+created.PlanGraph.ID+"/execute", nil)
	executeBeforeApproval.Header.Set(userIDHeaderName, "owner-a")
	executeBeforeApprovalResponse := httptest.NewRecorder()
	router.ServeHTTP(executeBeforeApprovalResponse, executeBeforeApproval)
	if executeBeforeApprovalResponse.Code != http.StatusConflict {
		t.Fatalf("execute before approval status=%d body=%s", executeBeforeApprovalResponse.Code, executeBeforeApprovalResponse.Body.String())
	}

	approve := httptest.NewRequest(http.MethodPost, "/api/plans/"+created.PlanGraph.ID+"/approve", nil)
	approve.Header.Set(userIDHeaderName, "owner-a")
	approveResponse := httptest.NewRecorder()
	router.ServeHTTP(approveResponse, approve)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	approved, err := planStore.GetPlan(created.PlanGraph.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != models.StatusPending || approved.Approval.Status != "approved" || approved.Approval.ApprovedBy != "owner-a" {
		t.Fatalf("plan was not approved correctly: %#v", approved.Approval)
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

func TestPlanRuntimeRoutesUploadedDatasetToCustomBenchmarkHarness(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	owner := "benchmark-owner"
	metadata := uploadMetadata{
		ID: "6fb2d7dc-e40a-40cf-9a89-c8bf44f27314", Name: "reviews.csv", ContentType: "text/csv",
		OwnerID: owner, SessionID: "session", CreatedAt: time.Now().UTC(), SHA256: "fixture",
	}
	directory := uploadDirectory(owner, metadata.ID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata.StoredPath = filepath.Join(directory, "content.csv")
	content := []byte("review,label\ngood,positive\n")
	metadata.Size = int64(len(content))
	if err := os.WriteFile(metadata.StoredPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeUploadMetadata(metadata); err != nil {
		t.Fatal(err)
	}

	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	runtime := newPlanRuntime(
		planner.NewPlanner(), planStore,
		scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1), eventBus,
	)
	router := gin.New()
	runtime.register(router.Group("/api"))
	payload := fmt.Sprintf(`{"intent":"用 https://github.com/example/research-repo 跑 benchmark，输入列 review，标签列 label","attachments":[%q]}`, metadata.ID)
	request := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewBufferString(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, owner)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		PlanGraph     models.PlanGraph     `json:"plan_graph"`
		IntentContext models.IntentContext `json:"intent_context"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.IntentContext.IntentType != "Custom_Benchmark" || len(created.PlanGraph.Nodes) != 11 {
		t.Fatalf("request did not enter custom benchmark harness: intent=%s nodes=%d", created.IntentContext.IntentType, len(created.PlanGraph.Nodes))
	}
	if created.PlanGraph.Nodes[0].AssignedTo != "research_coding_agent" {
		t.Fatalf("dataset profile routed to %s", created.PlanGraph.Nodes[0].AssignedTo)
	}
	uploadedFiles, ok := created.IntentContext.Entities["uploaded_files"].([]any)
	if !ok || len(uploadedFiles) != 1 {
		t.Fatalf("unexpected public upload metadata: %#v", created.IntentContext.Entities["uploaded_files"])
	}
	if uploaded, ok := uploadedFiles[0].(map[string]any); !ok || uploaded["text_excerpt"] != nil {
		t.Fatalf("benchmark dataset excerpt leaked into plan response: %#v", uploadedFiles[0])
	}
}
