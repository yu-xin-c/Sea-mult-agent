# 后端 Agent Planner 与拓扑图优化进展

## 1. 文档目的

这份文档用于同步当前 `agent planner + PlanGraph + scheduler + sandbox` 主链路的真实状态。

它不再只描述“计划怎么改”，而是明确区分两部分：

- 已经落地并验证通过的优化
- 仍然需要继续推进的优化

当前基线以仓库现状为准，重点覆盖后端 `planner`、拓扑图执行、沙箱链路和 API 契约。

---

## 2. 当前已经落地的优化

### 2.1 `plan_graph` 已成为主执行结构

当前 `/api/plan` 默认返回：

```json
{
  "message": "Plan generated successfully",
  "plan_graph": { ... },
  "intent_context": { ... }
}
```

说明：

- 默认主返回体已经收口到 `plan_graph`
- legacy `plan` 不再默认返回
- 只有显式带 `?include_legacy_plan=true` 才会附带旧结构

这解决了“新旧两套 plan 同时返回，前后端容易串状态”的核心问题。

---

### 2.1.1 Planner 已切到 “agent 优先，模板兜底”

当前 `BuildPlan` 不再只靠硬编码模板产图。

现在的执行顺序是：

1. 先由 `planner_agent` 调用大模型生成结构化 DAG JSON
2. 后端将其物化成 `TaskNode + TaskEdge`
3. 通过现有图校验器校验合法性
4. 只有当 agent 输出不合法或解析失败时，才回退到模板 planner

这意味着：

- planner 已经开始从“模板工厂”升级成“agent planner”
- 任务步数不再固定死在代码里
- 但模板仍然保留，作为稳定兜底

---

### 2.2 新的计划级执行接口已经补齐

后端现在已支持：

- `POST /api/plan`
- `GET /api/plans/:id`
- `GET /api/plans/:id/events`
- `POST /api/plans/:id/execute`
- `GET /api/plans/:id/stream`

其中：

- `GET /api/plans/:id/events` 用于事件回放
- `POST /api/plans/:id/execute` 已补充冲突语义

当前执行冲突行为：

- plan 不存在：`404`
- plan 已在执行：`409`
- plan 已完成或已失败：`409`

这部分已经和优化目标对齐。

---

### 2.3 Code Execution 主链路已经打通真实沙箱执行

当前 `Code_Execution` 类型的图，已经可以按下面链路完整执行：

1. `Generate Code`
2. `Prepare Runtime`
3. `Execute Code`
4. `Verify And Summarize Result`

已验证通过的真实行为：

- `sandbox_agent` 不再走通用 LLM 编排链
- `Prepare Runtime` 会确定性创建并校验沙箱运行环境
- `Execute Code` 会读取上游 `generated_code` 和 `runtime_env`
- 代码会在 Docker 沙箱中真实执行
- 结果会写回 `execution_result`
- 下游 `data_agent` 会继续生成总结报告

这条链路已经实测跑通，输出过 `42`，并完成整图收尾。

---

### 2.4 `Execute Code` 的数据依赖已经修正

之前 `Execute Code` 只依赖 `runtime_env`，拿不到 `generated_code`。

现在 planner 已修正为：

- `Prepare Runtime` 依赖 `generated_code`
- `Execute Code` 同时依赖 `generated_code` 和 `runtime_env`

这让执行节点第一次具备了最基本的数据消费闭环。

---

### 2.5 沙箱服务已经按项目默认方式跑通

当前架构仍然是：

- backend agent 判断需要沙箱
- backend 通过 `SandboxClient` 调用 `SANDBOX_URL`
- `docker-sandbox` 服务再去调用本机 Docker

这意味着当前实现仍然需要单独启动沙箱服务，但它已经能稳定工作。

已补充启动脚本：

- [scripts/start-sandbox.ps1](/D:/mygo/Sea-mult-agent/scholar-agent/scripts/start-sandbox.ps1)
- [scripts/start-backend.ps1](/D:/mygo/Sea-mult-agent/scholar-agent/scripts/start-backend.ps1)

脚本已处理：

- 本地 `GOCACHE`
- `backend.env.ps1` 环境注入
- 默认 `SANDBOX_URL`

---

### 2.6 `docker-sandbox` 的并发安全补了一步

`NativeDockerEngine` 内部用于记录挂载目录的 `mountPaths` 已经加锁。

当前已处理：

- 创建容器时写锁
- 删除容器时写锁
- 执行后读取图像时读锁

这不是并发治理的终点，但至少修掉了一个明显的共享 map 并发风险。

---

## 3. 当前仍然存在的核心问题

下面这些问题依然成立，也是下一轮优化的重点。

### 3.1 Agent 对上游 artifact 的消费仍不完整

虽然 `sandbox_agent` 已经能消费 `generated_code` / `runtime_env`，但整体上仍存在问题：

- `DataAgent` 和 `LibrarianAgent` 仍主要依赖 `task.Description`
- 上游 artifact 没有统一标准格式注入 prompt
- `sharedContext` 还不是稳定契约

也就是说，当前只有代码执行链路完成了“真实消费输入”，其他链路还没有彻底补齐。

---

### 3.2 Artifact 仍然缺少强类型校验

当前调度层对于 artifact 的判断仍偏弱，主要问题包括：

- 更偏向“key 是否存在”
- 缺少严格的类型校验
- 缺少统一的合法性校验
- 生产者与消费者之间还没有稳定的 schema

例如：

- `workspace_path`
- `runtime_env`
- `run_metrics`
- `parsed_paper`

这些都还应该进一步结构化。

---

### 3.3 `parallelizable` 仍未完全成为运行时约束

planner 中已经有 `Parallelizable`，scheduler 也已经开始按该字段筛选 ready 节点，但仍有进一步优化空间：

- 更细粒度的资源感知调度仍需增强
- 节点优先级策略还比较基础
- 更复杂的批量/阶段调度还没有补齐

这部分已经从“字段占位”进入“开始生效”，但还没有彻底做完。

---

### 3.4 失败传播仍需补齐 data edge 语义

当前失败传播逻辑仍需要继续增强：

- 不应只看 `control` 下游
- 应同时覆盖 `data` 下游
- 被阻断的节点应能明确体现“是谁阻断了它”

否则图模型虽然看上去有数据边，但失败语义仍是不完整的。

---

### 3.5 Artifact 生产唯一性校验仍需加强

当前 planner 的校验还应该继续收紧：

- 同一个 artifact key 不应由多个节点生产
- 构图阶段应直接报错
- 不应允许后写覆盖前写

否则图上的数据来源会变得不确定。

---

### 3.6 Scheduler / Planner / API 仍缺少测试兜底

目前 `go test ./...` 可以通过，但仓库里针对以下行为的测试仍然不够：

- DAG 构图校验
- artifact 依赖满足判断
- control/data edge 失败传播
- `parallelizable` 调度
- 事件顺序
- 计划级接口冲突语义

当前大部分结论仍然来自集成验证和静态审查，不是来自系统化测试。

---

## 4. 建议的下一轮优化顺序

建议按下面顺序推进，性价比最高。

### 4.1 第一优先级

先补运行时语义：

1. 让 `DataAgent` / `LibrarianAgent` 统一消费 `task.Inputs`
2. 给 artifact 增加类型与有效性校验
3. 让 scheduler 真正尊重 `parallelizable`
4. 让失败传播同时覆盖 `control + data`

---

### 4.2 第二优先级

再补 planner 和契约：

1. 禁止重复 artifact producer
2. 统一 artifact 输出结构
3. 为不同节点声明 `ExpectedArtifactTypes`
4. 收紧图校验逻辑

---

### 4.3 第三优先级

最后补回归保障：

1. planner 单测
2. scheduler 单测
3. API 单测
4. 至少一条 plan execution 集成测试

---

## 5. 当前启动与验证方式

当前本地联调建议按下面顺序执行。

### 5.1 准备环境变量

先复制模板：

```powershell
Copy-Item .\backend.env.ps1.example .\backend.env.ps1
```

至少填写：

- `OPENAI_API_KEY`
- `OPENAI_BASE_URL`
- `OPENAI_MODEL_NAME`
- `SANDBOX_URL`

默认本地沙箱地址：

```powershell
$env:SANDBOX_URL = "http://localhost:8082"
```

---

### 5.2 启动沙箱服务

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-sandbox.ps1
```

健康检查：

- [http://localhost:8082/api/v1/sandboxes](http://localhost:8082/api/v1/sandboxes)

---

### 5.3 启动后端

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-backend.ps1
```

健康检查：

- [http://localhost:8080/ping](http://localhost:8080/ping)

---

### 5.4 执行主链路

依次调用：

1. `POST /api/plan`
2. `POST /api/plans/:id/execute`
3. `GET /api/plans/:id/stream`
4. 可选 `GET /api/plans/:id/events`

当前建议优先验证 `Code_Execution` 场景，因为它已经具备真实沙箱执行能力。

---

## 6. 结论

当前系统已经从“展示型 DAG”迈进到“部分可执行 DAG”。

已经真正落地的关键点有三件：

1. `plan_graph` 已经成为主执行结构
2. 计划级 API 已经具备更清晰的执行与事件语义
3. `Code_Execution` 链路已经能真实使用 Docker 沙箱执行代码

但如果要把整个后端拓扑图体系做扎实，下一步仍然必须继续完成：

1. 全 Agent 的 artifact 输入闭环
2. scheduler 的串并行和失败传播语义补齐
3. artifact 强校验
4. 系统化测试
