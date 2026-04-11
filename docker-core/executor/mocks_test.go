package executor

import (
	"context"
	"fmt"
)

// MockSandbox 模拟沙箱
type MockSandbox struct {
	ExecuteFunc func(cmd string) (string, error)
}

func (s *MockSandbox) Execute(ctx context.Context, cmd string) (string, error) {
	if s.ExecuteFunc != nil {
		return s.ExecuteFunc(cmd)
	}
	fmt.Printf("[MockSandbox] Executing: %s\n", cmd)
	return fmt.Sprintf("Success output for: %s", cmd), nil
}

func (s *MockSandbox) Close() error {
	return nil
}

// MockCheckpoint 模拟检查点管理器
type MockCheckpoint struct{}

func (c *MockCheckpoint) Commit(ctx context.Context, label string) (string, error) {
	fmt.Printf("[MockCheckpoint] Committing: %s\n", label)
	return "img-" + label, nil
}

func (c *MockCheckpoint) Rollback(ctx context.Context, id string) error {
	fmt.Printf("[MockCheckpoint] Rolling back to: %s\n", id)
	return nil
}

// MockAgent 模拟智能体
type MockAgent struct {
	PlanFunc               func(goal string) (*DAG, error)
	GenerateStrategiesFunc func(instruction string) ([]string, error)
}

func (m *MockAgent) Plan(ctx context.Context, goal string) (*DAG, error) {
	if m.PlanFunc != nil {
		return m.PlanFunc(goal)
	}
	// 默认返回一个简单的图
	g := NewDAG()
	g.AddNode("base", []string{"apt update"})
	return g, nil
}

func (m *MockAgent) GenerateStrategies(ctx context.Context, instruction string) ([]string, error) {
	if m.GenerateStrategiesFunc != nil {
		return m.GenerateStrategiesFunc(instruction)
	}
	return []string{"echo mock-strategy"}, nil
}
