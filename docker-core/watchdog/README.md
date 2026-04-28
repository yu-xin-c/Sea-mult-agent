# Watchdog (防死循环与安全护栏)

`watchdog` 模块是整个多智能体系统的“最后一道防线”与“看门狗”。在自动化环境配置过程中，LLM 可能会陷入由于幻觉导致的无效重试（死循环），或者生成需要人工交互的阻塞指令（如等待输入 `[Y/n]` 导致进程挂起）。

本模块作为底层执行引擎的代理守卫（Proxy Guard），利用语义哈希分析与严格的超时上下文控制，强制打断危险与无效操作，并向上层 Planner 提供带有强引导性的修复建议。

## ✨ 核心特性

### 1. 语义断路器 (Semantic Circuit Breaker)
- **哈希滑动窗口检测**：针对“LLM 下发的指令”与其导致的“标准错误 (Stderr)”组合进行特征提取与哈希计算，记录在滑动窗口中。
- **强制重规划 (Replanning)**：当检测到同一语义哈希在设定的重试窗口（如连续 5 次）内频繁出现，断路器立即熔断。系统直接拦截底层的执行请求，并抛出明确的熔断异常，强制要求 LLM 抛弃当前思路，变更解决方案。

### 2. 交互阻塞防御 (Anti-Hang & Timeout Kill)
- **严格上下文控制**：所有针对沙箱的指令调用均被 `context.WithTimeout` 严格包裹。
- **智能模拟中断 (SIGINT 注入)**：对于因遗漏非交互参数（如 `apt-get install` 未加 `-y`）而导致进程挂起等待输入的情况，超时触发后，看门狗不会简单粗暴地销毁沙箱，而是向伪终端（TTY）发送 `Ctrl+C` 中断信号，释放被锁死的终端。
- **引导式补全反馈**：中断后，向 LLM 抛出特定的超时异常提示（如 *"Command timed out, likely waiting for user input. Try adding '-y' or non-interactive flags."*），引导模型修正指令并重新提交。

## 📦 快速开始

### 初始化与拦截配置

```go
package main

import (
    "context"
    "fmt"
    "time"
    "[github.com/yu-xin-c/Sea-mult-agent/watchdog](https://github.com/yu-xin-c/Sea-mult-agent/watchdog)"
    "[github.com/yu-xin-c/Sea-mult-agent/sandbox](https://github.com/yu-xin-c/Sea-mult-agent/sandbox)"
)

func main() {
    ctx := context.Background()

    // 1. 初始化看门狗配置
    cfg := watchdog.Config{
        MaxRetryWindow: 5,               // 相同语义错误最多允许 5 次重试
        CommandTimeout: 30 * time.Second, // 常规命令超时时间
    }

    // 2. 创建 Watchdog 实例，并包装现有的 sandbox 执行器
    // 假设 box 已经通过 sandbox.New() 创建
    wd := watchdog.New(cfg, box)

    // 3. 模拟 LLM 陷入死循环 (连续多次下发相同的错误指令)
    for i := 0; i < 6; i++ {
        _, err := wd.ExecuteSafe(ctx, "pip install unexisting-package")
        if err != nil {
            if watchdog.IsCircuitBroken(err) {
                fmt.Println("🚨 看门狗狂吠：断路器已触发！检测到死循环，强制要求 Replanning。")
                break
            }
        }
    }

    // 4. 模拟执行交互式阻塞命令 (忘记加 -y)
    result, err := wd.ExecuteSafe(ctx, "apt-get install htop")
    if err != nil && watchdog.IsTimeoutWithHang(err) {
        fmt.Println("⚠️ 检测到命令挂起，看门狗已发送 Ctrl+C 取消任务。")
        fmt.Printf("反馈给 LLM 的系统建议: %s\n", result.InferenceSummary)
        // 自动生成的摘要会提示 LLM 加上 -y 等非交互式参数
    }
}
```

## 🏗 架构组件
CircuitBreaker: 维护固定大小的 LRU 缓存或环形队列，负责对指令与清洗后的错误日志计算 Hash，判定是否触发熔断。

TimeoutController: 基于 context 管理命令的生命周期，负责在超时阈值到达时通过底层引擎向 TTY 注入终止控制符（如 \x03）。

ExceptionAnalyzer: 错误类型分析器，将底层的超时、被杀死的进程状态包装成结构化、带有 Guidance（引导建议）的系统级 Prompt 返回给大模型。