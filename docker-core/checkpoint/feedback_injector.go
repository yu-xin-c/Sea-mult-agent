package checkpoint

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// FailureRecord 记录一次失败路径
type FailureRecord struct {
	NodeID    string    // 失败所属的 DAG 节点
	Command   string    // 触发失败的命令
	Stderr    string    // 错误输出
	Timestamp time.Time // 发生时间
}

// FeedbackInjector 将失败路径转化为 LLM 负反馈提示词
// 阻断 LLM 的错误循环，引导模型尝试全新方案
type FeedbackInjector struct {
	mu           sync.RWMutex
	failures     []FailureRecord
	maxStderrLen int // stderr 截取最大长度（rune 计数）
}

// NewFeedbackInjector 创建负反馈生成器
func NewFeedbackInjector() *FeedbackInjector {
	return &FeedbackInjector{
		failures:     make([]FailureRecord, 0),
		maxStderrLen: 300,
	}
}

// RecordFailure 记录一次失败
func (fi *FeedbackInjector) RecordFailure(record FailureRecord) {
	fi.mu.Lock()
	defer fi.mu.Unlock()
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	fi.failures = append(fi.failures, record)
}

// GenerateNegativeFeedback 生成单条失败的负反馈提示
func (fi *FeedbackInjector) GenerateNegativeFeedback(failedCommand string) string {
	return fmt.Sprintf(
		`[System Guard]: The previous attempt "%s" caused a fatal system error. `+
			`The environment has been rolled back to a safe state. `+
			`DO NOT attempt this path again. Please provide an alternative installation strategy.`,
		failedCommand,
	)
}

// GenerateFullContext 生成包含所有历史失败路径的完整上下文
// 应插入到发给 LLM 的 system prompt 最前端
func (fi *FeedbackInjector) GenerateFullContext() string {
	fi.mu.RLock()
	defer fi.mu.RUnlock()

	if len(fi.failures) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[System Guard - Rollback History]\n")
	sb.WriteString("The following paths have been attempted and FAILED. ")
	sb.WriteString("DO NOT try any of these approaches again:\n\n")

	for i, f := range fi.failures {
		stderr := truncateString(f.Stderr, fi.maxStderrLen)
		sb.WriteString(fmt.Sprintf(
			"%d. Node [%s]: Command `%s` failed at %s\n   Error: %s\n\n",
			i+1, f.NodeID, f.Command,
			f.Timestamp.Format("15:04:05"),
			stderr,
		))
	}

	sb.WriteString("Please use a completely different approach.\n")
	return sb.String()
}

// FailureCount 返回已记录的失败次数
func (fi *FeedbackInjector) FailureCount() int {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	return len(fi.failures)
}

// Failures 返回所有失败记录的副本
func (fi *FeedbackInjector) Failures() []FailureRecord {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	cp := make([]FailureRecord, len(fi.failures))
	copy(cp, fi.failures)
	return cp
}

// Reset 清空所有失败记录
func (fi *FeedbackInjector) Reset() {
	fi.mu.Lock()
	defer fi.mu.Unlock()
	fi.failures = nil
}

// truncateString 按 rune 数截断字符串，避免截断多字节 UTF-8 字符
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
