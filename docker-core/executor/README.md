# Executor (双层并发 DAG 调度引擎)

`executor` 模块是本系统的任务调度与并发控制核心。它负责将 LLM 或规划器输出的环境配置目标解析为有向无环图（DAG），并利用 Go 强大的并发原语，在 `sandbox` 中实现高吞吐、高容错的节点调度。

该模块不仅处理任务间的拓扑依赖，还深入处理任务内的多路径竞速，同时通过严格的资源网关保障底层宿主机/沙箱的稳定性。

## ✨ 核心特性

### 1. 拓扑驱动的并发调度 (Topology-Driven Scheduling)
- **无锁入度消解**：调度器实时分析 DAG 拓扑，将所有入度为 0 的就绪节点动态推入 `ReadyQueue`。
- **Goroutine 协程池执行**：由多个并发 Worker 监听队列，实现无依赖任务的最大化并行处理，大幅缩短复杂环境的整体配置时间。

### 2. 节点内竞速机制 (Node-Level Racing)
解决 AI 生成指令不确定性与执行成功率的冲突：
- **多实现并行探测**：针对模糊指令（例如“安装 OpenCV”），支持同时下发多种包管理策略（如 `pip`、`conda`、`源码编译`）。
- **赢者通吃 (Winner-Takes-All)**：基于 Go 的 `context.WithCancel` 机制，任何一个执行路径率先返回成功信号，将立即触发 `Cancel()`。
- **孤儿进程清理**：配合底层 Sandbox，通过 SIGKILL 瞬间终止同一节点下其他仍在运行的冗余分支，释放系统资源并固定当前成功结果。

### 3. Gatekeeper 资源网关 (Resource Quotas & OS Coordination)
在依赖 Linux CFS 进行 CPU 调度的基础上，引入应用层面的软隔离：
- **GPU 显存防溢出隔离**：设置容量严格为 1 的专用 GPU 信号量 (Semaphore)。确保同一时刻只能有一个高显存消耗任务（如模型微调、CUDA 重编译）获得物理显卡访问权，彻底杜绝 VRAM OOM 导致的沙箱崩溃。
- **高并发限流**：引入加权信号量（Weighted Semaphore），为 I/O 密集型或内存密集型任务设定全局并发阈值，防止瞬时高并发 I/O 压垮宿主机磁盘。

## 📦 快速开始

### 初始化与执行 DAG

```go
package main

import (
    "context"
    "fmt"
    "[github.com/yu-xin-c/Sea-mult-agent/executor](https://github.com/yu-xin-c/Sea-mult-agent/executor)"
    "[github.com/yu-xin-c/Sea-mult-agent/sandbox](https://github.com/yu-xin-c/Sea-mult-agent/sandbox)"
)

func main() {
    ctx := context.Background()

    // 1. 初始化资源网关 (Gatekeeper)
    gk := executor.NewGatekeeper(executor.ResourceConfig{
        MaxConcurrentIO: 10,
        MaxGPUTasks:     1, // 严格限制 GPU 并发
    })

    // 2. 创建执行引擎 (绑定已初始化的 sandbox 对象)
    // 假设 box 已经通过 sandbox.New() 创建
    engine := executor.NewEngine(gk, box)

    // 3. 构建 DAG 任务图
    graph := executor.NewDAG()

    // 节点 A: 基础依赖 (无依赖)
    nodeA := graph.AddNode("install_base", []string{"apt-get install -y build-essential"})

    // 节点 B: 竞速节点 - 安装 OpenCV (依赖节点 A)
    // 提供三种可能的解决路径，进行 Racing
    nodeB := graph.AddNode("install_opencv", []string{
        "pip install opencv-python",
        "conda install -c conda-forge opencv",
    })
    graph.AddEdge(nodeA, nodeB)

    // 4. 启动拓扑调度与执行
    err := engine.ExecuteGraph(ctx, graph)
    if err != nil {
        fmt.Printf("执行失败: %v\n", err)
    } else {
        fmt.Println("环境配置 DAG 执行完毕！")
    }
}
```

## 🏗 架构组件

Graph/Node: 封装有向无环图的数据结构与依赖关系校验（环检测）。

Scheduler: 负责入度计算、拓扑排序及 ReadyQueue 的生命周期管理。

Racer: 管理单节点内的多协程并发启动与 context.Cancel 竞速逻辑。

Gatekeeper: 基于 golang.org/x/sync/semaphore 实现的全局资源调度与锁管理。