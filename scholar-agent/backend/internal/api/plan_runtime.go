package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scholar-agent-backend/internal/events"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/scheduler"
	"scholar-agent-backend/internal/store"

	"github.com/gin-gonic/gin"
)

const planStreamEventName = "plan_event"

type planRuntime struct {
	planner   *planner.Planner
	store     store.PlanStore
	scheduler *scheduler.Scheduler
	events    *events.Bus

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func newPlanRuntime(p *planner.Planner, planStore store.PlanStore, runner *scheduler.Scheduler, eventBus *events.Bus) *planRuntime {
	return &planRuntime{
		planner:   p,
		store:     planStore,
		scheduler: runner,
		events:    eventBus,
		running:   map[string]context.CancelFunc{},
	}
}

// RegisterPlanRuntimeRoutes wires the graph planner to the real scheduler and
// agents used by the production API.
func RegisterPlanRuntimeRoutes(
	apiGroup *gin.RouterGroup,
	p *planner.Planner,
	librarian scheduler.AgentRunner,
	data scheduler.AgentRunner,
	coder scheduler.AgentRunner,
	researchCoding ...scheduler.AgentRunner,
) {
	var planStore store.PlanStore = store.NewMemoryPlanStore()
	if path := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); path != "" {
		fileStore, err := store.NewFilePlanStore(path)
		if err != nil {
			log.Printf("plan store init failed, falling back to memory: %v", err)
		} else {
			planStore = fileStore
			if err := store.RecoverInterruptedPlans(planStore); err != nil {
				log.Printf("plan store recovery failed: %v", err)
			}
		}
	}
	eventBus := events.NewBus()
	executor := scheduler.NewRoutedTaskExecutor(librarian, data, coder, researchCoding...)
	runner := scheduler.NewScheduler(planStore, executor, eventBus, 2)
	runtime := newPlanRuntime(p, planStore, runner, eventBus)
	runtime.register(apiGroup)
}

func (r *planRuntime) register(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/plan", r.createPlan)
	apiGroup.GET("/plans/:id", r.getPlan)
	apiGroup.GET("/plans/:id/events", r.getPlanEvents)
	apiGroup.POST("/plans/:id/execute", r.executePlan)
	apiGroup.POST("/plans/:id/cancel", r.cancelPlan)
	apiGroup.POST("/plans/:id/approve", r.approvePlan)
	apiGroup.POST("/plans/:id/tasks/:taskId/retry", r.retryTask)
	apiGroup.POST("/plans/:id/tasks/:taskId/reassign", r.reassignTask)
	apiGroup.GET("/plans/:id/stream", r.streamPlanEvents)
}

func (r *planRuntime) createPlan(c *gin.Context) {
	var payload RequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := resolveUserID(c)
	sessionID := resolveSessionID(c)
	intentContext := buildRuleIntentContext(payload.Intent)
	attachments, err := resolvePlanUploads(userID, payload.Attachments)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(attachments) > 0 {
		if hasBenchmarkDatasetAttachment(attachments) && containsAny(payload.Intent, []string{"benchmark", "基准测试", "评测", "测评", "跑分"}) {
			intentContext.IntentType = "Custom_Benchmark"
			intentContext.Entities["needs_custom_benchmark"] = true
			removeAttachmentTextExcerpts(attachments)
		}
		intentContext.Entities["uploaded_files"] = attachments
		intentContext.Metadata["attachment_count"] = len(attachments)
	}
	plan, err := r.planner.BuildPlan(c.Request.Context(), intentContext)
	if err != nil {
		log.Printf("Error generating plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate plan"})
		return
	}
	configurePlanGovernance(plan, intentContext, userID, sessionID)
	if err := r.store.SavePlan(plan); err != nil {
		log.Printf("Error saving plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save plan"})
		return
	}

	response := gin.H{
		"message":        "Plan generated successfully",
		"plan_graph":     plan,
		"plan":           legacyPlanFromGraph(plan),
		"intent_context": intentContext,
		"session_id":     sessionID,
		"anon_user_id":   userID,
		"user_id":        userID,
	}
	if clarification, ok := buildPlanClarification(intentContext.IntentType, payload.Intent); ok {
		response["clarification"] = clarification
	}
	c.JSON(http.StatusOK, response)
}

func (r *planRuntime) getPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"plan_graph": plan})
}

func (r *planRuntime) getPlanEvents(c *gin.Context) {
	if _, ok := r.authorizedPlan(c); !ok {
		return
	}
	events, err := r.store.ListEvents(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (r *planRuntime) executePlan(c *gin.Context) {
	planID := c.Param("id")
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if plan.Status != models.StatusPending && plan.Status != models.StatusReady {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("plan cannot be started from status %s", plan.Status)})
		return
	}

	r.mu.Lock()
	if _, exists := r.running[planID]; exists {
		r.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), planTimeout(plan))
	r.running[planID] = cancel
	r.mu.Unlock()

	go func(runCtx context.Context, runCancel context.CancelFunc) {
		defer func() {
			runCancel()
			r.mu.Lock()
			delete(r.running, planID)
			r.mu.Unlock()
		}()

		if err := r.scheduler.ExecutePlan(runCtx, planID); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("plan execution failed plan_id=%s: %v", planID, err)
		}
	}(ctx, cancel)

	c.JSON(http.StatusAccepted, gin.H{
		"message": "Plan execution started",
		"plan_id": planID,
	})
}

func (r *planRuntime) cancelPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	r.mu.Lock()
	cancel := r.running[plan.ID]
	r.mu.Unlock()
	if err := r.scheduler.CancelPlan(plan.ID, "canceled by user"); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if cancel != nil {
		cancel()
	}
	c.JSON(http.StatusOK, gin.H{"message": "Plan canceled", "plan_id": plan.ID})
}

func (r *planRuntime) approvePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	now := time.Now()
	userID := resolveUserID(c)
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if !current.Approval.Required {
			return fmt.Errorf("plan does not require approval")
		}
		current.Approval.Status = "approved"
		current.Approval.ApprovedBy = userID
		current.Approval.ApprovedAt = &now
		current.Status = models.StatusPending
		current.UpdatedAt = now
		return nil
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Plan approved", "plan_id": plan.ID})
}

func (r *planRuntime) retryTask(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if err := r.scheduler.RetryTask(plan.ID, c.Param("taskId")); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Task reset for retry", "plan_id": plan.ID, "task_id": c.Param("taskId")})
}

func (r *planRuntime) reassignTask(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	var payload struct {
		AssignedTo string `json:"assigned_to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := r.scheduler.ReassignTask(plan.ID, c.Param("taskId"), payload.AssignedTo); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Task reassigned", "plan_id": plan.ID, "task_id": c.Param("taskId"), "assigned_to": payload.AssignedTo})
}

func (r *planRuntime) streamPlanEvents(c *gin.Context) {
	planID := c.Param("id")
	if _, ok := r.authorizedPlan(c); !ok {
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	subscription := r.events.Subscribe(planID)
	defer r.events.Unsubscribe(planID, subscription)

	seen := map[string]struct{}{}
	replayUnseen := func() bool {
		history, err := r.store.ListEvents(planID)
		if err != nil {
			writePlanSSE(c, "error", gin.H{"error": err.Error()})
			return false
		}
		for _, event := range history {
			fingerprint := planEventFingerprint(event)
			if _, duplicate := seen[fingerprint]; duplicate {
				continue
			}
			seen[fingerprint] = struct{}{}
			if !writePlanSSE(c, planStreamEventName, event) || isTerminalPlanEvent(event) {
				return false
			}
		}
		return true
	}
	if !replayUnseen() {
		return
	}

	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-subscription:
			if !ok {
				return
			}
			fingerprint := planEventFingerprint(event)
			if _, duplicate := seen[fingerprint]; duplicate {
				continue
			}
			seen[fingerprint] = struct{}{}
			if !writePlanSSE(c, planStreamEventName, event) {
				return
			}
			if isTerminalPlanEvent(event) {
				return
			}
		case <-heartbeat.C:
			// The in-memory bus is intentionally non-blocking. Replaying the durable
			// history heals any live event dropped for a slow SSE subscriber.
			if !replayUnseen() {
				return
			}
			if !writePlanSSE(c, "heartbeat", "keep-alive") {
				return
			}
		case <-c.Request.Context().Done():
			return
		}
	}
}

func (r *planRuntime) authorizedPlan(c *gin.Context) (*models.PlanGraph, bool) {
	plan, err := r.store.GetPlan(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return nil, false
	}
	if plan.OwnerID != "" && plan.OwnerID != resolveUserID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "plan belongs to another user"})
		return nil, false
	}
	return plan, true
}

func configurePlanGovernance(plan *models.PlanGraph, intent models.IntentContext, userID, sessionID string) {
	plan.OwnerID = userID
	plan.SessionID = sessionID
	plan.Budget.MaxTaskAttempts = positiveEnvInt("PLAN_MAX_TASK_ATTEMPTS", plan.Budget.MaxTaskAttempts)
	plan.Budget.MaxDurationSec = positiveEnvInt("PLAN_MAX_DURATION_SECONDS", plan.Budget.MaxDurationSec)
	requiresApproval := strings.EqualFold(strings.TrimSpace(os.Getenv("REQUIRE_PLAN_APPROVAL")), "true") ||
		strings.EqualFold(strings.TrimSpace(fmt.Sprint(intent.Constraints["reproduction_mode"])), "full")
	plan.Approval.Required = requiresApproval
	if requiresApproval {
		plan.Approval.Status = "pending"
		plan.Approval.Reason = "high-risk or full reproduction plan"
		plan.Status = models.StatusAwaitingApproval
	} else {
		plan.Approval.Status = "not_required"
	}
}

func positiveEnvInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func planTimeout(plan *models.PlanGraph) time.Duration {
	if plan != nil && plan.Budget.MaxDurationSec > 0 {
		return time.Duration(plan.Budget.MaxDurationSec) * time.Second
	}
	return 35 * time.Minute
}

func buildRuleIntentContext(rawIntent string) models.IntentContext {
	intentType := DetectIntentType(rawIntent)
	context := models.IntentContext{
		RawIntent:   rawIntent,
		IntentType:  intentType,
		Entities:    map[string]any{},
		Constraints: map[string]any{},
		Metadata: map[string]any{
			"normalized_intent": strings.ToLower(strings.TrimSpace(rawIntent)),
		},
		Confidence: 0.8,
		Reasoning:  "deterministic API fallback classifier",
		Source:     "rule_fallback",
	}
	if repoURL := routeGitHubRepoURLRe.FindString(rawIntent); repoURL != "" {
		context.Entities["preferred_repo_url"] = strings.TrimSuffix(repoURL, ".git")
	}
	if intentType == "Paper_Reproduction" {
		for key, value := range collectPaperSearchFields(context, rawIntent) {
			context.Entities[key] = value
		}
		if containsAny(rawIntent, []string{"smoke", "最小实验", "最小验证", "快速验证"}) {
			context.Entities["smoke_reproduction"] = true
			context.Constraints["reproduction_mode"] = "smoke"
		}
		if containsAny(rawIntent, []string{"plot", "画图", "图表", "可视化"}) {
			context.Entities["needs_plot"] = true
		}
		if containsAny(rawIntent, []string{"debug", "fix", "修复", "排查", "不一致", "重跑"}) {
			context.Entities["needs_fix"] = true
		}
		if containsAny(rawIntent, []string{"ablation", "消融", "参数敏感性", "模块移除", "随机种子", "seed stability", "运行成本对照"}) {
			context.Entities["needs_ablation"] = true
		}
	}
	return context
}

func hasBenchmarkDatasetAttachment(attachments []map[string]any) bool {
	for _, attachment := range attachments {
		name := strings.ToLower(strings.TrimSpace(fmt.Sprint(attachment["name"])))
		for _, extension := range []string{".csv", ".tsv", ".json", ".jsonl"} {
			if strings.HasSuffix(name, extension) {
				return true
			}
		}
	}
	return false
}

func removeAttachmentTextExcerpts(attachments []map[string]any) {
	for _, attachment := range attachments {
		delete(attachment, "text_excerpt")
	}
}

func legacyPlanFromGraph(graph *models.PlanGraph) *models.Plan {
	legacy := &models.Plan{
		ID:         graph.ID,
		UserIntent: graph.UserIntent,
		Tasks:      make(map[string]*models.Task, len(graph.Nodes)),
		Status:     graph.Status,
		CreatedAt:  graph.CreatedAt,
		UpdatedAt:  graph.UpdatedAt,
	}
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		legacy.Tasks[node.ID] = &models.Task{
			ID:                node.ID,
			Name:              node.Name,
			Type:              node.Type,
			Description:       node.Description,
			AssignedTo:        node.AssignedTo,
			Status:            node.Status,
			Dependencies:      append([]string(nil), node.Dependencies...),
			RequiredArtifacts: append([]string(nil), node.RequiredArtifacts...),
			OutputArtifacts:   append([]string(nil), node.OutputArtifacts...),
			Parallelizable:    node.Parallelizable,
			Priority:          node.Priority,
			RetryLimit:        node.RetryLimit,
			Inputs:            node.Inputs,
			CreatedAt:         node.CreatedAt,
			UpdatedAt:         node.UpdatedAt,
		}
	}
	return legacy
}

func writePlanSSE(c *gin.Context, eventName string, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventName, encoded); err != nil {
		return false
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func planEventFingerprint(event models.PlanEvent) string {
	keys := make([]string, 0, len(event.Payload))
	for key := range event.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s|%s|%s|%s|%v", event.PlanID, event.EventType, event.TaskID, event.Timestamp.UTC().Format(time.RFC3339Nano), keys)
}

func isTerminalPlanEvent(event models.PlanEvent) bool {
	return event.EventType == "plan_completed" || event.EventType == "plan_failed" || event.EventType == "plan_canceled"
}
