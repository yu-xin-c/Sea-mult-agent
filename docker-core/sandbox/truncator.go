package sandbox

import (
	"fmt"
	"strings"
)

// Truncator 推理轨道日志瘦身器
// 采用首尾截断策略：保留前 N 行（启动信息）+ 后 M 行（Error Traceback）
type Truncator struct {
	keepFirst int // 保留前 N 行
	keepLast  int // 保留后 M 行
}

// NewTruncator 创建 Truncator
func NewTruncator(n, m int) *Truncator {
	if n <= 0 {
		n = 20
	}
	if m <= 0 {
		m = 50
	}
	return &Truncator{keepFirst: n, keepLast: m}
}

// Truncate 对原始输出执行首尾截断
func (t *Truncator) Truncate(raw string) string {
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	total := len(lines)

	// 总行数在阈值内，不需要截断
	if total <= t.keepFirst+t.keepLast {
		return raw
	}

	head := lines[:t.keepFirst]
	tail := lines[total-t.keepLast:]
	omitted := total - t.keepFirst - t.keepLast

	var sb strings.Builder
	sb.WriteString(strings.Join(head, "\n"))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("\n... [%d lines omitted] ...\n\n", omitted))
	sb.WriteString(strings.Join(tail, "\n"))
	return sb.String()
}
