package checkpoint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/checkpoint"
)

func TestManager_NewManager(t *testing.T) {
	m := checkpoint.NewManager(nil)

	if m.Registry() == nil {
		t.Fatal("registry should not be nil")
	}
	if m.Injector() == nil {
		t.Fatal("injector should not be nil")
	}
}

func TestManager_RecordFailureAndGetFeedback(t *testing.T) {
	m := checkpoint.NewManager(nil)

	feedback := m.RecordFailureAndGetFeedback("node-A", "pip install torch", "ERROR: no matching distribution")

	if !strings.Contains(feedback, "pip install torch") {
		t.Error("feedback should contain the failed command")
	}
	if !strings.Contains(feedback, "[System Guard]") {
		t.Error("feedback should contain System Guard prefix")
	}

	if m.Injector().FailureCount() != 1 {
		t.Errorf("expected 1 failure recorded, got %d", m.Injector().FailureCount())
	}
}

func TestManager_GetFullFeedbackContext(t *testing.T) {
	m := checkpoint.NewManager(nil)

	// Empty context
	if m.GetFullFeedbackContext() != "" {
		t.Error("should return empty when no failures")
	}

	// Record failures and check context
	m.RecordFailureAndGetFeedback("node-A", "cmd1", "err1")
	m.RecordFailureAndGetFeedback("node-B", "cmd2", "err2")

	ctx := m.GetFullFeedbackContext()
	if !strings.Contains(ctx, "cmd1") || !strings.Contains(ctx, "cmd2") {
		t.Error("full context should contain all failed commands")
	}
	if !strings.Contains(ctx, "Rollback History") {
		t.Error("full context should contain header")
	}
}

func TestManager_RollbackToNode_NoSnapshot(t *testing.T) {
	m := checkpoint.NewManager(nil)

	_, err := m.RollbackToNode(nil, "nonexistent-node")
	if err == nil {
		t.Fatal("should error when no snapshot found")
	}
	if !strings.Contains(err.Error(), "no snapshot found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManager_RollbackToLatest_NoSnapshot(t *testing.T) {
	m := checkpoint.NewManager(nil)

	_, err := m.RollbackToLatest(nil)
	if err == nil {
		t.Fatal("should error when no snapshots available")
	}
	if !strings.Contains(err.Error(), "no snapshots available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManager_RegistryIntegration(t *testing.T) {
	m := checkpoint.NewManager(nil)

	// Simulate registering snapshots (normally done by Commit)
	m.Registry().Register(checkpoint.Snapshot{
		NodeID:    "node-A",
		ImageID:   "img-aaa",
		CreatedAt: time.Now().Unix(),
	})
	m.Registry().Register(checkpoint.Snapshot{
		NodeID:    "node-B",
		ImageID:   "img-bbb",
		CreatedAt: time.Now().Unix(),
	})
	m.Registry().Register(checkpoint.Snapshot{
		NodeID:    "node-C",
		ImageID:   "img-ccc",
		CreatedAt: time.Now().Unix(),
	})

	if m.Registry().Len() != 3 {
		t.Fatalf("expected 3 snapshots, got %d", m.Registry().Len())
	}

	// Verify RollbackToNode checks the right snapshot
	snap, ok := m.Registry().GetByNodeID("node-B")
	if !ok || snap.ImageID != "img-bbb" {
		t.Fatal("should find node-B with img-bbb")
	}

	// Simulate registry rollback
	m.Registry().RollbackTo("node-B")
	if m.Registry().Len() != 2 {
		t.Fatalf("expected 2 snapshots after rollback, got %d", m.Registry().Len())
	}

	_, ok = m.Registry().GetByNodeID("node-C")
	if ok {
		t.Error("node-C should be removed after rollback to node-B")
	}
}

func TestManager_FeedbackAndRegistryWorkflow(t *testing.T) {
	m := checkpoint.NewManager(nil)

	// Register some snapshots
	m.Registry().Register(checkpoint.Snapshot{NodeID: "step1", ImageID: "img1"})
	m.Registry().Register(checkpoint.Snapshot{NodeID: "step2", ImageID: "img2"})

	// Record a failure at step2
	feedback := m.RecordFailureAndGetFeedback("step2", "make build", "compilation error")
	if feedback == "" {
		t.Error("feedback should not be empty")
	}

	// Full context should include the failure
	fullCtx := m.GetFullFeedbackContext()
	if !strings.Contains(fullCtx, "make build") {
		t.Error("full context should contain the failed command")
	}
	if !strings.Contains(fullCtx, "compilation error") {
		t.Error("full context should contain the error")
	}

	// Rollback registry to step1
	m.Registry().RollbackTo("step1")
	if m.Registry().Len() != 1 {
		t.Errorf("expected 1 snapshot after rollback, got %d", m.Registry().Len())
	}

	// Failure history survives registry rollback
	if m.Injector().FailureCount() != 1 {
		t.Error("failure history should persist after registry rollback")
	}
}
