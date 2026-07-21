package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/store"
)

// blockingSnapshotExecutor exposes the existing scheduler's executor boundary:
// it keeps the TaskNode snapshot that was routed before a prospective handoff,
// then returns that old executor's result after the assignment has changed.
type blockingSnapshotExecutor struct {
	started chan string
	release chan struct{}
}

func (e *blockingSnapshotExecutor) ExecuteTask(
	ctx context.Context,
	plan *models.PlanGraph,
	task *models.TaskNode,
) (*models.TaskExecutionResult, error) {
	_ = plan
	if task.AssignedTo == "coder_agent" {
		select {
		case e.started <- task.AssignedTo:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &models.TaskExecutionResult{
		Status: models.StatusCompleted,
		Result: fmt.Sprintf("completed by pre-handoff executor %s", task.AssignedTo),
	}, nil
}

func TestHandoffDiscardsLatePreReassignmentResult(t *testing.T) {
	now := time.Now()
	planStore := store.NewMemoryPlanStore()
	plan := &models.PlanGraph{
		ID:         "relaystate-p0-plan",
		UserIntent: "prospective mid-task handoff",
		IntentType: "Code_Execution",
		Status:     models.StatusPending,
		Nodes: []*models.TaskNode{
			{
				ID:              "task-1",
				Name:            "Run code",
				Type:            "execute_code",
				AssignedTo:      "coder_agent",
				Status:          models.StatusPending,
				OutputArtifacts: []string{"run_metrics"},
				CreatedAt:       now,
				UpdatedAt:       now,
			},
		},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}

	executor := &blockingSnapshotExecutor{
		started: make(chan string, 1),
		release: make(chan struct{}),
	}
	runner := NewScheduler(planStore, executor, &NoopEventPublisher{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.ExecutePlan(ctx, plan.ID) }()

	select {
	case originalExecutor := <-executor.started:
		if originalExecutor != "coder_agent" {
			t.Fatalf("unexpected original executor: %s", originalExecutor)
		}
	case <-ctx.Done():
		t.Fatal("original executor did not start")
	}

	if err := runner.ReassignTask(plan.ID, "task-1", "sandbox_agent"); err != nil {
		t.Fatal(err)
	}

	close(executor.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("scheduler did not terminate")
	}

	finalPlan, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	finalTask := getNodeByID(finalPlan, "task-1")
	if finalTask == nil {
		t.Fatal("final task missing")
	}
	if finalTask.AssignedTo != "sandbox_agent" {
		t.Fatalf("handoff assignment was lost: %s", finalTask.AssignedTo)
	}
	if finalTask.Status != models.StatusCompleted {
		t.Fatalf("late pre-handoff result was not accepted: %s", finalTask.Status)
	}
	if finalTask.Result != "completed by pre-handoff executor sandbox_agent" {
		t.Fatalf("unexpected completion result: %q", finalTask.Result)
	}
	events, err := planStore.ListEvents(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundDiscarded := false
	for _, event := range events {
		if event.EventType == "task_result_discarded" {
			foundDiscarded = true
			break
		}
	}
	if !foundDiscarded {
		t.Fatal("expected stale pre-handoff result to be discarded")
	}
}
