package engine

import (
	"context"
)

// ExecutionResponse 统一的代码执行响应
type ExecutionResponse struct {
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
	Images   []string `json:"images,omitempty"`
}

// SandboxEngine 定义沙箱引擎的标准接口
type SandboxEngine interface {
	// Create 创建沙箱
	Create(ctx context.Context, image string, mountPath string) (string, error)
	// Delete 删除沙箱
	Delete(ctx context.Context, id string) error
	// ExecutePython 执行 Python 代码
	ExecutePython(ctx context.Context, id string, code string) (*ExecutionResponse, error)
	// ExecuteCommand 执行系统命令
	ExecuteCommand(ctx context.Context, id string, cmd []string) (*ExecutionResponse, error)
	// GetType 返回引擎类型
	GetType() string
}
