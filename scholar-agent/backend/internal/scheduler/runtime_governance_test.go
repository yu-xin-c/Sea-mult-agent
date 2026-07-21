package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/store"
)

type taskExecutorFunc func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error)

func (fn taskExecutorFunc) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	return fn(ctx, plan, task)
}

func TestSchedulerRetriesFailedAttemptWithinLimit(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("retry-plan", 1)
	plan.Nodes[0].RetryLimit = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		if attempts.Add(1) == 1 {
			return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "transient"}, nil
		}
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "recovered"}, nil
	}), &NoopEventPublisher{}, 1)

	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCompleted || finalPlan.Nodes[0].RunCount != 2 || finalPlan.Nodes[0].Result != "recovered" {
		t.Fatalf("automatic retry did not recover plan: %#v", finalPlan.Nodes[0])
	}
}

func TestSchedulerEnforcesTaskTimeout(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("timeout-plan", 1)
	plan.Nodes[0].TimeoutSeconds = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, taskExecutorFunc(func(ctx context.Context, _ *models.PlanGraph, _ *models.TaskNode) (*models.TaskExecutionResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), &NoopEventPublisher{}, 1)

	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusFailed {
		t.Fatalf("timeout did not fail plan: status=%s task=%#v", finalPlan.Status, finalPlan.Nodes[0])
	}
	if !strings.Contains(finalPlan.Nodes[0].Error, "timed out") {
		t.Fatalf("timeout failure should record a useful error: %q", finalPlan.Nodes[0].Error)
	}
}

func TestSchedulerCancelsPlanWhenAttemptBudgetIsExhausted(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("budget-plan", 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Budget.MaxTaskAttempts = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)

	if err := runner.ExecutePlan(context.Background(), plan.ID); err == nil {
		t.Fatal("expected execution to report exhausted budget")
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCanceled || finalPlan.Usage.TaskAttempts != 1 {
		t.Fatalf("budget did not cancel plan: status=%s usage=%#v", finalPlan.Status, finalPlan.Usage)
	}
}

func TestCancelPlanRejectsTerminalPlan(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("terminal-plan", 1)
	plan.Status = models.StatusCompleted
	plan.Nodes[0].Status = models.StatusCompleted
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := NewScheduler(planStore, nil, nil, 1).CancelPlan(plan.ID, "too late"); err == nil {
		t.Fatal("expected terminal plan cancellation to be rejected")
	}
}

func TestCanceledPlanCannotBeResurrectedByLateTaskResult(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("cancel-race-plan", 1)
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		close(started)
		<-release
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "late result"}, nil
	}), &NoopEventPublisher{}, 1)
	done := make(chan error, 1)
	go func() { done <- runner.ExecutePlan(context.Background(), plan.ID) }()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not start")
	}
	if err := runner.CancelPlan(plan.ID, "test cancellation"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not observe cancellation")
	}
	close(release)
	time.Sleep(100 * time.Millisecond)
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCanceled || finalPlan.Nodes[0].Status != models.StatusCanceled || finalPlan.Nodes[0].Result != "" {
		t.Fatalf("late task result resurrected canceled state: %#v", finalPlan.Nodes[0])
	}
}

func governanceTestPlan(id string, nodeCount int) *models.PlanGraph {
	now := time.Now()
	plan := &models.PlanGraph{
		ID:        id,
		TraceID:   "trace-" + id,
		Status:    models.StatusPending,
		Budget:    models.RunBudget{MaxTaskAttempts: 10, MaxDurationSec: 30},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	for i := 0; i < nodeCount; i++ {
		plan.Nodes = append(plan.Nodes, &models.TaskNode{
			ID:             fmt.Sprintf("%s-task-%d", id, i+1),
			Name:           "Test task",
			Type:           "execute_code",
			AssignedTo:     "sandbox_agent",
			Status:         models.StatusPending,
			TimeoutSeconds: 5,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	return plan
}
