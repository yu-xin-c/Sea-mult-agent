# 基于 Docker 与 gVisor 的 LLM TEE环境架构设计

## 1. 理论基础与沙箱化策略

在执行不受信任的 AI 生成代码时，传统的命名空间隔离（Standard Docker）不足以防御内核级攻击。本方案采用 **gVisor** 作为运行时容器引擎。

- **gVisor (runsc) 隔离层**：通过在用户态实现 Sentry 内核，拦截并重新处理沙箱内应用的所有系统调用。即使智能体尝试通过执行漏洞（Exploits）逃逸，也仅能触及 gVisor 的用户态内核，而无法直接操作宿主机的 Linux 内核。
- **nvproxy 透传机制**：针对需要 GPU 的论文复现任务，利用 nvproxy 过滤 NVIDIA 驱动的 ioctl 调用，在保障安全的前提下实现接近原生的 CUDA 计算性能。

------

## 2. 持久化交互式沙箱（Stateful Sandbox）

为了支持类似 Cursor 或 Claude Code 的渐进式配置，系统摒弃了“单次运行”模式，改用 **TTY 劫持技术** 实现长连接会话。

### 2.1 增量执行机制

后端利用 Go Docker SDK 劫持容器的伪终端（Pseudo-TTY）：

1. **输入流 (Stdin)**：将 LLM 产生的指令实时写入容器的标准输入。
2. **状态保持**：由于 bash 进程在容器内持续运行，跨指令的环境变量（如 `export PATH`）和文件系统变更得以天然保留。
3. **环境变量持久化**：后端维护一个全局 `EnvMap`，在每次执行命令前，自动注入必要的上下文变量，防止 Shell 切换导致的路径丢失。

### 2.2 双路日志流 (Dual-track Logging)

系统对终端输出进行多路转发，解决“用户审计”与“模型推理”的需求冲突：

- **审计轨道 (Audit Track)**：原始输出（含 ANSI 转义序列）实时存入日志文件，供用户通过 UI 进行完整复现核查。
- **推理轨道 (Inference Track)**：输出经过 **Log-Truncator** 过滤。
  - **策略**：保留前 $N$ 行启动信息与后 $M$ 行退出信息（通常包含 Error Traceback）。
  - **提炼**：利用轻量级模型对冗长编译日志进行摘要，仅反馈核心矛盾点。

------

## 3. 双层并发 DAG 调度引擎

系统将环境配置目标拆解为有向无环图（DAG），利用 Go 的并发原语实现高吞吐执行。

### 3.1 拓扑驱动与节点执行

1. **节点间并发**：调度器通过入度检测，将所有入度为 0 的节点推入 `ReadyQueue`，由多个并发 Goroutine 协同处理。
2. **节点内竞速 (Racing)**：
   - 对于模糊指令（如“安装 OpenCV”），系统允许 LLM 提供多种实现（`pip`, `conda`, `source`）。
   - Go 后端并发启动这些实现。第一个成功返回的路径将触发 `context.Cancel()`，通过 `SIGKILL` 瞬间终止其他仍在运行的分支，并固定当前成功结果。

### 3.2 资源配额与 OS 调度协同

虽然 CPU 调度交由 Linux CFS 处理，但后端引入 **Gatekeeper 资源网关**：

- **显存隔离**：设置专用 GPU 信号量（容量为 1），确保同一时刻只有一个高负载 CUDA 任务（如编译或微调）占用物理显卡，防止 VRAM 溢出导致的沙箱崩溃。
- **并发限额**：利用加权信号量（Weighted Semaphore）限制总体的 I/O 和内存密集型任务数量。

------

## 4. 容错闭环：Checkpoint 与回滚逻辑

本方案选用兼容性最强的 **Docker Commit** 策略来管理状态快照。

### 4.1 自动检查点触发

在 DAG 的每个核心节点（Vertex）执行成功且通过 **Checker Agent** 的验证后：

```go
// 伪代码：生成中间快照
imageID, _ := dockerClient.ContainerCommit(ctx, containerID, types.ContainerCommitOptions{
    Reference: fmt.Sprintf("env-node-%s", nodeID),
})
```

### 4.2 废弃路径回溯流程

1. **故障判定**：当 LLM 在某一节点尝试了所有实现（Implementations）均告失败，或检测到环境已破坏（如破坏了 GLIBC）。

2. **触发回滚**：

   - 销毁当前受损容器。
   - 基于上一个成功的 `imageID` 重新启动容器实例。

3. **逻辑注入**：在下发给 LLM 的上下文（Context）中强制插入“负反馈”记录：

   > "Note: Path 'pip install x.x' failed and corrupted the env. Environment rolled back. DO NOT try this path again."

------

## 5. 防死循环与安全护栏

- **断路器 (Circuit Breaker)**：后端对 LLM 发出的命令及其对应的 Error 结尾进行哈希运算。若检测到在特定窗口内（如 5 次）出现重复的语义哈希值，则强制拦截请求，要求 LLM 必须变更解决方案（Replanning）。
- **超时强杀**：所有工具调用均受控于 `context.WithTimeout`。对于由于需要交互输入（如 `[Y/n]`）而挂起的任务，超时后自动发送 `Ctrl+C` 并返回超时异常，引导 LLM 补全 `-y` 等非交互参数。

------

## 6. 后端接口方案

系统暴露两类接口以供集成：

1. **HTTP API**：用于任务提交、DAG 图状态查询、手动触发回滚以及下载审计日志。
2. **WebSocket**：用于实时流式传输终端 I/O，支持用户或前端实时观测配置进度，并允许在必要时进行人工干预