package watchdog

import (
	"errors"
	"fmt"
)

// WatchdogError 定义看门狗相关的错误
type WatchdogError struct {
	Type    string
	Message string
	Command string
	Hint    string // 提供给 LLM 的修复建议
}

func (e *WatchdogError) Error() string {
	return fmt.Sprintf("[%s] %s (Command: %s)", e.Type, e.Message, e.Command)
}

// 错误类型常量
const (
	ErrTypeCircuitBroken = "CIRCUIT_BROKEN"
	ErrTypeTimeoutHang   = "TIMEOUT_HANG"
	ErrTypeEnvironmentCorrupted = "ENV_CORRUPTED"
)

// IsCircuitBroken 检查是否为断路器熔断错误
func IsCircuitBroken(err error) bool {
	var wdErr *WatchdogError
	if errors.As(err, &wdErr) {
		return wdErr.Type == ErrTypeCircuitBroken
	}
	return false
}

// IsTimeoutWithHang 检查是否为超时挂起错误
func IsTimeoutWithHang(err error) bool {
	var wdErr *WatchdogError
	if errors.As(err, &wdErr) {
		return wdErr.Type == ErrTypeTimeoutHang
	}
	return false
}

// IsEnvironmentCorrupted 检查是否为环境损坏错误
func IsEnvironmentCorrupted(err error) bool {
	var wdErr *WatchdogError
	if errors.As(err, &wdErr) {
		return wdErr.Type == ErrTypeEnvironmentCorrupted
	}
	return false
}
