package watchdog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/executor"
)

// Config 看门狗配置
type Config struct {
	MaxRetryWindow int
	CommandTimeout time.Duration
}

// Watchdog 看门狗，包装执行器并提供安全保障
type Watchdog struct {
	cfg     Config
	box     executor.Sandbox
	cp      executor.CheckpointManager
	cb      *CircuitBreaker
	checker *Checker
}

func NewWatchdog(cfg Config, box executor.Sandbox, cp executor.CheckpointManager, agent executor.Agent) *Watchdog {
	return &Watchdog{
		cfg:     cfg,
		box:     box,
		cp:      cp,
		cb:      NewCircuitBreaker(cfg.MaxRetryWindow),
		checker: NewChecker(agent),
	}
}

// Execute 带保护的执行方法，完全对齐 executor.Sandbox 接口
func (w *Watchdog) Execute(ctx context.Context, cmd string) (string, error) {
	// 1. 设置执行超时
	execCtx, cancel := context.WithTimeout(ctx, w.cfg.CommandTimeout)
	defer cancel()

	// 2. 执行命令
	output, err := w.box.Execute(execCtx, cmd)

	// 节点竞速模式下，胜出分支会取消其余分支；这类 context canceled 不是故障，不应进入审计。
	if execCtx.Err() == context.Canceled {
		return output, context.Canceled
	}

	// 3. 处理执行结果 (优先检查超时)
	if execCtx.Err() == context.DeadlineExceeded {
		return "", &WatchdogError{
			Type:    ErrTypeTimeoutHang,
			Message: "Command timed out, possibly waiting for input.",
			Command: cmd,
			Hint:    "Try adding '-y' or non-interactive flags to avoid hanging.",
		}
	}

	// 4. 语义断路器检测（仅在失败信号下参与，避免对重复成功命令误熟断）
	if shouldRecordForCircuit(err, output) && w.cb.RecordAndCheck(cmd, output) {
		return "", &WatchdogError{
			Type:    ErrTypeCircuitBroken,
			Message: "Circuit breaker triggered: detected an infinite semantic loop.",
			Command: cmd,
			Hint:    "The same command and error are repeating. Please change your strategy completely.",
		}
	}

	// 5. Checker 审计
	success, hint := w.checker.Audit(ctx, cmd, output, err)
	if !success {
		return output, &WatchdogError{
			Type:    ErrTypeEnvironmentCorrupted,
			Message: "Audit failed: environment state might be corrupted.",
			Command: cmd,
			Hint:    hint,
		}
	}

	return output, nil
}

func shouldRecordForCircuit(err error, output string) bool {
	if err != nil {
		return true
	}
	lower := strings.ToLower(output)
	failureSignals := []string{
		"error:",
		" failed:",
		"failed to",
		"traceback",
		"exception:",
		"segmentation fault",
		"command not found",
		"no such file or directory",
	}
	for _, s := range failureSignals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// Close 代理到底层 Sandbox 的关闭逻辑
func (w *Watchdog) Close() error {
	if w.box != nil {
		return w.box.Close()
	}
	return nil
}

// RollbackToSafeState 强制回滚到指定的安全镜像快照
func (w *Watchdog) RollbackToSafeState(ctx context.Context, imageID string) error {
	if w.cp == nil {
		return fmt.Errorf("checkpoint manager not initialized")
	}
	return w.cp.Rollback(ctx, imageID)
}
