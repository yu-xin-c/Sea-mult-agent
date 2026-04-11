package checkpoint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/checkpoint"
)

func TestFeedbackInjector_SingleFailure(t *testing.T) {
	fi := checkpoint.NewFeedbackInjector()

	prompt := fi.GenerateNegativeFeedback("pip install cuda-python")

	if !strings.Contains(prompt, "pip install cuda-python") {
		t.Error("should contain the failed command")
	}
	if !strings.Contains(prompt, "DO NOT attempt this path again") {
		t.Error("should contain DO NOT instruction")
	}
	if !strings.Contains(prompt, "[System Guard]") {
		t.Error("should contain System Guard prefix")
	}
}

func TestFeedbackInjector_FullContext(t *testing.T) {
	fi := checkpoint.NewFeedbackInjector()

	// 空时应返回空字符串
	if fi.GenerateFullContext() != "" {
		t.Error("should return empty for no failures")
	}

	fi.RecordFailure(checkpoint.FailureRecord{
		NodeID:    "node-A",
		Command:   "pip install torch",
		Stderr:    "ERROR: no matching distribution",
		Timestamp: time.Now(),
	})
	fi.RecordFailure(checkpoint.FailureRecord{
		NodeID:    "node-B",
		Command:   "apt install libcuda",
		Stderr:    "E: Unable to locate package",
		Timestamp: time.Now(),
	})

	ctx := fi.GenerateFullContext()

	if !strings.Contains(ctx, "Rollback History") {
		t.Error("should contain header")
	}
	if !strings.Contains(ctx, "pip install torch") {
		t.Error("should contain first failure")
	}
	if !strings.Contains(ctx, "apt install libcuda") {
		t.Error("should contain second failure")
	}
	if !strings.Contains(ctx, "completely different approach") {
		t.Error("should contain instruction to change approach")
	}
}

func TestFeedbackInjector_FailureCount(t *testing.T) {
	fi := checkpoint.NewFeedbackInjector()

	if fi.FailureCount() != 0 {
		t.Error("should start at 0")
	}

	fi.RecordFailure(checkpoint.FailureRecord{NodeID: "a", Command: "cmd1"})
	fi.RecordFailure(checkpoint.FailureRecord{NodeID: "b", Command: "cmd2"})

	if fi.FailureCount() != 2 {
		t.Errorf("expected 2, got %d", fi.FailureCount())
	}
}

func TestFeedbackInjector_Reset(t *testing.T) {
	fi := checkpoint.NewFeedbackInjector()
	fi.RecordFailure(checkpoint.FailureRecord{NodeID: "a", Command: "cmd1"})
	fi.Reset()

	if fi.FailureCount() != 0 {
		t.Error("should be 0 after reset")
	}
	if fi.GenerateFullContext() != "" {
		t.Error("should return empty after reset")
	}
}

func TestFeedbackInjector_StderrTruncation(t *testing.T) {
	fi := checkpoint.NewFeedbackInjector()
	longStderr := strings.Repeat("x", 500)
	fi.RecordFailure(checkpoint.FailureRecord{
		NodeID:  "a",
		Command: "cmd",
		Stderr:  longStderr,
	})

	ctx := fi.GenerateFullContext()
	// stderr 应被截断到 300 + "..."
	if !strings.Contains(ctx, "...") {
		t.Error("long stderr should be truncated")
	}
}

func TestFeedbackInjector_Failures(t *testing.T) {
	fi := checkpoint.NewFeedbackInjector()
	fi.RecordFailure(checkpoint.FailureRecord{NodeID: "a", Command: "cmd1"})

	failures := fi.Failures()
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}

	// 修改副本不影响原始
	failures[0].NodeID = "modified"
	origFailures := fi.Failures()
	if origFailures[0].NodeID != "a" {
		t.Error("Failures() should return a copy")
	}
}
