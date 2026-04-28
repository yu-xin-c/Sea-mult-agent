package watchdog

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Simulator 提供针对 Sandbox 和 CheckpointManager 的模拟实现
type Simulator struct {
	// 记录已执行的指令历史
	History []string
	// 内存中的快照镜像
	Snapshots map[string]string
	// 当前状态标签
	CurrentState string

	// 控制开关：模拟各种故障
	FailOnKeyword string
	HangOnKeyword string
	SegmentFault  bool
}

func NewSimulator() *Simulator {
	return &Simulator{
		Snapshots:    make(map[string]string),
		CurrentState: "initial",
	}
}

// --- executor.Sandbox 接口实现 ---

func (s *Simulator) Execute(ctx context.Context, cmd string) (string, error) {
	s.History = append(s.History, cmd)
	fmt.Printf("[Simulator] Executing: %s\n", cmd)

	// 1. 模拟挂起
	if s.HangOnKeyword != "" && strings.Contains(cmd, s.HangOnKeyword) {
		fmt.Printf("[Simulator] Hanging detected for keyword '%s'...\n", s.HangOnKeyword)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			// 模拟一个很长的超时
		}
	}

	// 2. 模拟段错误 (会导致审计失败)
	if s.SegmentFault && strings.Contains(cmd, "crash") {
		return "Segmentation fault (core dumped)", nil
	}

	// 3. 模拟普通失败
	if s.FailOnKeyword != "" && strings.Contains(cmd, s.FailOnKeyword) {
		return "Error: command failed", fmt.Errorf("exit status 1")
	}

	return "Success: output of " + cmd, nil
}

func (s *Simulator) Close() error {
	fmt.Println("[Simulator] Resource cleaned up.")
	return nil
}

// --- executor.CheckpointManager 接口实现 ---

func (s *Simulator) Commit(ctx context.Context, label string) (string, error) {
	imageID := fmt.Sprintf("img-%d", time.Now().UnixNano())
	s.Snapshots[label] = imageID
	fmt.Printf("[Simulator] Committed state '%s' as %s\n", label, imageID)
	return imageID, nil
}

func (s *Simulator) Rollback(ctx context.Context, imageID string) error {
	fmt.Printf("[Simulator] Rolling back to state: %s\n", imageID)
	s.CurrentState = "restored-from-" + imageID
	return nil
}
