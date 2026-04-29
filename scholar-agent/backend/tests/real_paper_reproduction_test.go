package tests

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"scholar-agent-backend/internal/agent"
	"scholar-agent-backend/internal/appconfig"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/sandbox"
	"scholar-agent-backend/internal/scheduler"
	"scholar-agent-backend/internal/store"
)

func TestRealPaperReproductionFlow(t *testing.T) {
	if os.Getenv("REAL_PAPER_REPRO_TEST") == "" {
		t.Skip("跳过真实论文复现测试。如需运行，请设置 REAL_PAPER_REPRO_TEST=1")
	}
	if _, err := appconfig.LoadLLMConfig(); err != nil {
		t.Skipf("跳过真实论文复现测试：无法读取 LLM 配置: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	intent := models.IntentContext{
		RawIntent:       "请复现论文《Attention Is All You Need》，通过 Papers with Code 找到真实开源实现，准备环境运行一个最小实验，并和论文指标对比",
		RewrittenIntent: "复现 Attention Is All You Need：通过 Papers with Code 找到真实开源实现，准备环境运行最小实验，并与论文指标对比。",
		IntentType:      "Paper_Reproduction",
		Entities: map[string]any{
			"paper_title":        "Attention Is All You Need",
			"paper_search_query": "Attention Is All You Need",
			"paper_method_name":  "Transformer",
			"needs_benchmark":    true,
			"needs_report":       true,
		},
		Constraints: map[string]any{
			"source": "Papers with Code",
		},
		Confidence: 0.95,
		Source:     "test",
	}

	p := planner.NewPlanner()
	plan, err := p.BuildPlan(ctx, intent)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	t.Logf("plan_id=%s intent_type=%s nodes=%d", plan.ID, plan.IntentType, len(plan.Nodes))
	for _, node := range plan.Nodes {
		t.Logf("node type=%s agent=%s name=%s deps=%d required=%v outputs=%v", node.Type, node.AssignedTo, node.Name, len(node.Dependencies), node.RequiredArtifacts, node.OutputArtifacts)
	}

	planStore := store.NewMemoryPlanStore()
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}

	sandboxURL := os.Getenv("SANDBOX_URL")
	if strings.TrimSpace(sandboxURL) == "" {
		sandboxURL = "http://localhost:8082"
	}
	sb := sandbox.NewSandboxClient(sandboxURL)
	executor := scheduler.NewRoutedTaskExecutor(
		agent.NewLibrarianAgent(),
		agent.NewDataAgent(),
		agent.NewCoderAgent(sb),
	)
	publisher := &testEventPublisher{t: t}
	runner := scheduler.NewScheduler(planStore, executor, publisher, 1)

	if err := runner.ExecutePlan(ctx, plan.ID); err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}

	finalPlan, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan failed: %v", err)
	}
	t.Logf("final plan status=%s artifacts=%d", finalPlan.Status, len(finalPlan.Artifacts))
	for _, node := range finalPlan.Nodes {
		t.Logf("final node type=%s status=%s name=%s error=%s result=%s", node.Type, node.Status, node.Name, node.Error, truncateForLog(node.Result, 500))
	}
	for key, artifact := range finalPlan.Artifacts {
		t.Logf("artifact %s type=%s value=%s", key, artifact.Type, truncateForLog(artifact.Value, 500))
	}

	if finalPlan.Status != models.StatusCompleted {
		t.Fatalf("paper reproduction plan did not complete: status=%s", finalPlan.Status)
	}
	if _, ok := finalPlan.Artifacts["repo_url"]; !ok {
		t.Fatal("expected repo_url artifact from real repo discovery")
	}
	if _, ok := finalPlan.Artifacts["run_metrics"]; !ok {
		t.Fatal("expected run_metrics artifact from sandbox execution")
	}
	if _, ok := finalPlan.Artifacts["comparison_report"]; !ok {
		t.Fatal("expected comparison_report artifact")
	}
}

type testEventPublisher struct {
	t  *testing.T
	mu sync.Mutex
}

func (p *testEventPublisher) Publish(planID string, event models.PlanEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.EventType == "task_log" {
		if message, ok := event.Payload["message"].(string); ok {
			p.t.Logf("event=%s task=%s %s", event.EventType, shortID(event.TaskID), message)
		}
		return
	}
	p.t.Logf("event=%s task=%s status=%s payload=%v", event.EventType, shortID(event.TaskID), event.TaskStatus, event.Payload)
	_ = planID
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func truncateForLog(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
