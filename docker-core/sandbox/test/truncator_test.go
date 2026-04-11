package sandbox_test

import (
	"strings"
	"testing"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/sandbox"
)

func TestTruncator_NoTruncateNeeded(t *testing.T) {
	tr := sandbox.NewTruncator(5, 5)
	input := "line1\nline2\nline3"
	got := tr.Truncate(input)
	if got != input {
		t.Errorf("expected no truncation, got: %s", got)
	}
}

func TestTruncator_EmptyInput(t *testing.T) {
	tr := sandbox.NewTruncator(5, 5)
	got := tr.Truncate("")
	if got != "" {
		t.Errorf("expected empty string, got: %s", got)
	}
}

func TestTruncator_TruncatesMiddle(t *testing.T) {
	tr := sandbox.NewTruncator(2, 2)

	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	input := strings.Join(lines, "\n")

	got := tr.Truncate(input)

	if !strings.Contains(got, "16 lines omitted") {
		t.Errorf("expected omitted notice, got: %s", got)
	}

	// 应该以前两行开头
	gotLines := strings.Split(got, "\n")
	if gotLines[0] != "line" || gotLines[1] != "line" {
		t.Errorf("head lines not preserved")
	}
}

func TestTruncator_ExactBoundary(t *testing.T) {
	tr := sandbox.NewTruncator(5, 5)

	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "x"
	}
	input := strings.Join(lines, "\n")

	got := tr.Truncate(input)
	// 刚好等于阈值，不截断
	if got != input {
		t.Errorf("should not truncate at exact boundary")
	}
}
