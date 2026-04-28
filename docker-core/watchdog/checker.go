package watchdog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/executor"
)

// Checker 审计智能体
type Checker struct {
	agent executor.Agent
}

func NewChecker(agent executor.Agent) *Checker {
	return &Checker{agent: agent}
}

// Audit 审计执行结果
func (c *Checker) Audit(ctx context.Context, cmd string, output string, err error) (bool, string) {
	lowerOut := strings.ToLower(output)
	if containsCrashSignal(lowerOut) {
		hint := "Execution output indicates a crash or corrupted runtime state."
		if err != nil {
			hint = fmt.Sprintf("Execution failed (crash detected): %v", err)
		}
		return false, hint
	}

	// 优先信任 exit code：命令以 0 退出表示成功，不做关键词检测
	// 理由：apt-get 等命令的正常输出中可能包含 "error"/"failed" 子串（如进度日志），但实际已成功
	if err == nil {
		return true, ""
	}

	// 命令失败（exit code != 0），进行深度审计
	hint := fmt.Sprintf("Execution failed: %v", err)

	// 引导：如果持有 Agent，则调用 LLM 进行深度诊断
	if c.agent != nil {
		fmt.Println("[Checker] 正在调用 LLM 进行审计诊断...")

		var advice []string
		var lastErr error

		// 截断过长的输出以节省 Token 并避免超出上下文窗口
		truncatedOutput := output
		if len(truncatedOutput) > 2000 {
			truncatedOutput = truncatedOutput[:1000] + "\n... [TRUNCATED] ...\n" + truncatedOutput[len(truncatedOutput)-1000:]
		}

		for i := 0; i < 3; i++ { // 最多重试 3 次
			advice, lastErr = c.agent.GenerateStrategies(ctx, fmt.Sprintf("Command '%s' failed. Error: %v, Output: %s. What is the root cause and how to fix it?", cmd, err, truncatedOutput))
			if lastErr == nil && len(advice) > 0 {
				break
			}
			fmt.Printf("[Checker] LLM 调用尝试 %d 失败: %v，正在重试...\n", i+1, lastErr)
			time.Sleep(1 * time.Second)
		}

		if lastErr != nil {
			fmt.Printf("[Checker] LLM 最终调用失败: %v\n", lastErr)
		} else if len(advice) > 0 {
			fmt.Printf("[Checker] LLM 调用成功，最终建议: %s\n", advice[0])
			hint = fmt.Sprintf("AI Expert Suggestion: %s", advice[0])
		}
	} else {
		// 如果没有 Agent，使用简单的规则命中
		if strings.Contains(lowerOut, "permission denied") {
			hint = "Permission denied. Try using 'sudo'."
		} else if strings.Contains(lowerOut, "command not found") {
			hint = "Command not found."
		}
	}
	return false, hint
}

func containsCrashSignal(lowerOut string) bool {
	crashSignals := []string{
		"segmentation fault",
		"core dumped",
		"bus error",
		"illegal instruction",
		"fatal python error",
	}
	for _, signal := range crashSignals {
		if strings.Contains(lowerOut, signal) {
			return true
		}
	}
	return false
}
