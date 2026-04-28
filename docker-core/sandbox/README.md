# Sandbox (持久化交互式沙箱)

`sandbox` 模块提供了一个对底层 Docker API 深度封装的执行器对象。专为支持类似 Cursor / Claude Code 的渐进式、有状态 AI Agent 交互场景而设计。

它摒弃了传统的“单次运行后销毁”模式，通过 TTY 劫持技术维护长连接会话，确保上下文（环境变量、文件系统状态）在多轮 LLM 指令执行中得以持久保留。

## ✨ 核心特性

### 1. 增量执行与状态保持 (Stateful TTY Session)
- **TTY 伪终端劫持**：通过 Go Docker SDK 劫持容器 Stdin/Stdout，LLM 生成的 bash 指令可以直接注入持续运行的容器内部。
- **环境上下文持久化**：跨指令执行时，`export PATH` 等环境变量及文件读写操作天然保留。
- **全局 EnvMap 注入**：内置环境变量映射表，每次命令执行前自动对齐必要上下文，防止复杂的 Shell 切换导致路径或变量丢失。

### 2. 双路日志流 (Dual-track Logging)
解决“用户完整核查”与“模型上下文窗口限制”的冲突：
- **审计轨道 (Audit Track)**：全量保留包含 ANSI 转义序列的原始终端输出，实时落盘，供前端 UI 完美复现执行现场。
- **推理轨道 (Inference Track)**：专为 LLM 优化的精简流。内置 `Log-Truncator`，采用首尾截断策略（保留前 N 行启动状态与后 M 行 Error Traceback），自动剥离冗长的编译过程，仅向模型返回核心执行结果或报错矛盾点。

## 📦 快速开始

### 初始化 Sandbox 对象

```go
package main

import (
    "context"
    "fmt"
    "[github.com/yu-xin-c/Sea-mult-agent/sandbox](https://github.com/yu-xin-c/Sea-mult-agent/sandbox)"
)

func main() {
    ctx := context.Background()

    // 1. 初始化沙箱配置
    cfg := sandbox.Config{
        Image:       "ubuntu:22.04",
        WorkingDir:  "/workspace",
        AuditLogDir: "/var/logs/audit",
    }

    // 2. 创建并启动沙箱对象
    box, err := sandbox.New(ctx, cfg)
    if err != nil {
        panic(err)
    }
    defer box.Close()

    // 3. 执行第一条指令 (状态被保留)
    result1, _ := box.Execute("export MY_VAR=hello_agent")
    fmt.Printf("推理摘要1: %s\n", result1.InferenceSummary)

    // 4. 执行第二条指令 (验证状态持久化)
    result2, _ := box.Execute("echo $MY_VAR")
    // 推理轨道输出: hello_agent
    fmt.Printf("推理摘要2: %s\n", result2.InferenceSummary)

    // 审计轨道日志已自动写入 cfg.AuditLogDir
}
```

## 🏗 架构设计
模块核心由以下几个核心组件构成：

Engine: 负责与 Docker Daemon 进行底层生命周期交互。

Session: 管理单次长连接的 TTY 数据管道。

StreamSplitter: 实现双路日志流的 I/O 多路复用分发。

Truncator: 执行启发式的日志瘦身与报错提取。