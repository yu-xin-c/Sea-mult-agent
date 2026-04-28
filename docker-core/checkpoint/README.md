# Checkpoint (容错闭环与状态回滚)

`checkpoint` 模块是系统环境配置的“安全网”与“后悔药”。由于 LLM 生成的安装指令具有不可预见性（有时甚至具有破坏性，如误删系统动态链接库），本模块采用物理级的容器快照机制结合认知级的上下文修正，构建了坚不可摧的容错闭环。

它与 `executor` 的 DAG 调度深度整合，确保无论 AI 如何“折腾”，整个环境构建过程都能步步为营，在遇到死胡同后能迅速回溯并绕过陷阱。

## ✨ 核心特性

### 1. 细粒度自动快照 (Automated State Checkpoints)
- **DAG 节点级锚点**：在 `executor` 完成任意一个核心目标节点，并且经过 Checker Agent 验证确认无误后，系统会自动触发 Checkpoint。
- **高兼容物理备份**：采用兼容性最强的 `Docker Commit` 策略，无缝抓取当前容器的完整文件系统与环境变量，生成唯一的 `imageID` 作为稳定的中间态快照。

### 2. 灾难级错误回滚 (Disaster Recovery & Rollback)
- **环境污染检测**：当某一节点下属的所有实现路径（Implementations）均告失败，或检测到致命错误（如 `glibc` 损坏、核心依赖树崩溃）时，触发回滚协议。
- **瞬时时空倒流**：直接销毁当前受损的容器实例，并基于上一个成功节点对应的 `imageID` 瞬间拉起全新的沙箱，将环境物理状态 100% 还原至破坏前的安全时刻。

### 3. 闭环认知修正 (Cognitive Feedback Injection)
- **经验教训强制注入**：回滚后，不仅物理环境复原，系统的 `ContextInjector` 会拦截下发给 LLM 的历史对话。
- **避免重复踩坑**：在当前任务上下文的最前端，系统会强制写入一段带有强烈系统引导的负反馈记录（例如：*"Note: Path 'pip install x.x' failed and corrupted the env. Environment rolled back. DO NOT try this path again."*），引导模型动态调整策略，尝试全新的解决方案。

## 📦 快速开始

### 集成 Checkpoint 管理器

```go
package main

import (
    "context"
    "fmt"
    "[github.com/yu-xin-c/Sea-mult-agent/checkpoint](https://github.com/yu-xin-c/Sea-mult-agent/checkpoint)"
    "[github.com/yu-xin-c/Sea-mult-agent/sandbox](https://github.com/yu-xin-c/Sea-mult-agent/sandbox)"
)

func main() {
    ctx := context.Background()

    // 1. 初始化快照管理器 (绑定底层 dockerClient 和 当前运行的容器)
    cpManager := checkpoint.NewManager(dockerClient, currentContainerID)

    // 2. 模拟 DAG 节点 A 执行成功
    fmt.Println("Node [install_base] executed successfully.")

    // 生成快照
    snapshot, err := cpManager.Commit(ctx, "env-node-A")
    if err != nil {
        panic(err)
    }
    fmt.Printf("已创建安全检查点: %s\n", snapshot.ImageID)

    // 3. 模拟 DAG 节点 B 执行失败且环境被破坏
    fmt.Println("Node [install_cuda] corrupted the environment!")

    // 4. 触发回滚协议
    rollbackResult, err := cpManager.Rollback(ctx, snapshot.ImageID)
    if err != nil {
        panic(err)
    }

    // 获取并更新 LLM 的系统提示词
    negativePrompt := cpManager.GenerateNegativeFeedback("pip install cuda-python")
    fmt.Println("\n--- 准备发送给 LLM 的修复上下文 ---")
    fmt.Println(negativePrompt)

    // 输出:
    // [System Guard]: The previous attempt "pip install cuda-python" caused a fatal system error.
    // The environment has been rolled back to a safe state.
    // DO NOT attempt this path again. Please provide an alternative installation strategy.
}
```

## 🏗 架构组件
Manager: 协调 Docker Client 的 Commit 与重置逻辑，管理当前运行容器的生命周期。

SnapshotRegistry: 快照注册表，记录 DAG 节点 ID 与 Docker imageID 的映射关系，支持链式回溯。

FeedbackInjector: 上下文修正器，负责将失败的路径转化为机器可读的强烈负面提示词（Negative Prompt），阻断 LLM 的错误循环。