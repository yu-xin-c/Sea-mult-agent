package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
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
	running map[string]struct{}
}

func newPlanRuntime(p *planner.Planner, planStore store.PlanStore, runner *scheduler.Scheduler, eventBus *events.Bus) *planRuntime {
	return &planRuntime{
		planner:   p,
		store:     planStore,
		scheduler: runner,
		events:    eventBus,
		running:   map[string]struct{}{},
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
) {
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	executor := scheduler.NewRoutedTaskExecutor(librarian, data, coder)
	runner := scheduler.NewScheduler(planStore, executor, eventBus, 2)
	runtime := newPlanRuntime(p, planStore, runner, eventBus)
	runtime.register(apiGroup)
}

func (r *planRuntime) register(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/plan", r.createPlan)
	apiGroup.GET("/plans/:id", r.getPlan)
	apiGroup.GET("/plans/:id/events", r.getPlanEvents)
	apiGroup.POST("/plans/:id/execute", r.executePlan)
	apiGroup.GET("/plans/:id/stream", r.streamPlanEvents)
}

func (r *planRuntime) createPlan(c *gin.Context) {
	var payload RequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	intentContext := buildRuleIntentContext(payload.Intent)
	plan, err := r.planner.BuildPlan(c.Request.Context(), intentContext)
	if err != nil {
		log.Printf("Error generating plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate plan"})
		return
	}
	if err := r.store.SavePlan(plan); err != nil {
		log.Printf("Error saving plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save plan"})
		return
	}

	userID := resolveUserID(c)
	sessionID := resolveSessionID(c)
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
	plan, err := r.store.GetPlan(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plan_graph": plan})
}

func (r *planRuntime) getPlanEvents(c *gin.Context) {
	events, err := r.store.ListEvents(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (r *planRuntime) executePlan(c *gin.Context) {
	planID := c.Param("id")
	plan, err := r.store.GetPlan(planID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
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
	r.running[planID] = struct{}{}
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, planID)
			r.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
		defer cancel()
		if err := r.scheduler.ExecutePlan(ctx, planID); err != nil {
			log.Printf("plan execution failed plan_id=%s: %v", planID, err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"message": "Plan execution started",
		"plan_id": planID,
	})
}

func (r *planRuntime) streamPlanEvents(c *gin.Context) {
	planID := c.Param("id")
	if _, err := r.store.GetPlan(planID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	subscription := r.events.Subscribe(planID)
	defer r.events.Unsubscribe(planID, subscription)

	seen := map[string]struct{}{}
	history, err := r.store.ListEvents(planID)
	if err != nil {
		writePlanSSE(c, "error", gin.H{"error": err.Error()})
		return
	}
	for _, event := range history {
		seen[planEventFingerprint(event)] = struct{}{}
		if !writePlanSSE(c, planStreamEventName, event) {
			return
		}
		if isTerminalPlanEvent(event) {
			return
		}
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
			if !writePlanSSE(c, "heartbeat", "keep-alive") {
				return
			}
		case <-c.Request.Context().Done():
			return
		}
	}
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
	if intentType == "Paper_Reproduction" {
		for key, value := range collectPaperSearchFields(context, rawIntent) {
			context.Entities[key] = value
		}
		if repoURL := routeGitHubRepoURLRe.FindString(rawIntent); repoURL != "" {
			context.Entities["preferred_repo_url"] = strings.TrimSuffix(repoURL, ".git")
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
	}
	return context
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
	return event.EventType == "plan_completed" || event.EventType == "plan_failed"
}
