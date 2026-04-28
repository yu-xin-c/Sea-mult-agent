# Plan 模块重构开发清单

## 1. 文档目标

这份文档是 [plan_module_refactor_proposal.md](/D:/mygo/Sea-mult-agent/scholar-agent/docs/plan_module_refactor_proposal.md) 的落地版，用来回答下面几个问题：

- 具体要改哪些文件
- 每个文件准备加什么结构
- 每一步先做什么、后做什么
- 哪些地方会联动前端
- 第一版建议做到什么程度

这份文档适合直接作为后续开发任务清单使用。

---

## 2. 总体实施顺序

建议按下面顺序推进，避免一次改太散：

1. 重构模型层
2. 重构 Planner 输出
3. 增加 PlanStore
4. 增加 Scheduler
5. 增加计划级执行接口
6. 增加计划级 SSE
7. 前端适配图结构与状态流
8. 最后再接真实意图识别结果

---

## 3. 文件级改造清单

## 3.1 后端模型层

### 文件 1

- [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)

当前问题：

- 结构偏旧，只适合静态任务列表

计划改造：

- 保留 `TaskStatus`
- 扩展状态枚举
- 将当前 `Task` 调整为更偏运行态的节点模型，或保留兼容字段

建议新增或修改内容：

- 增加 `StatusReady`
- 增加 `StatusBlocked`
- 增加 `StatusSkipped`
- 增加 `StatusCanceled`
- 给任务补：
  - `Type`
  - `RequiredArtifacts`
  - `OutputArtifacts`
  - `Parallelizable`
  - `Priority`
  - `RetryLimit`
  - `RunCount`
  - `StartedAt`
  - `FinishedAt`

建议：

- 如果你希望兼容旧代码，可以先保留 `Task` 结构名不变
- 如果你想模型更清晰，可以把新图节点单独拆成 `TaskNode`

### 文件 2

- `backend/internal/models/graph.go`

当前状态：

- 不存在，建议新增

建议新增结构：

- `PlanGraph`
- `TaskEdge`
- `GraphMeta`

建议内容：

```go
type PlanGraph struct {
    ID         string
    UserIntent string
    IntentType string
    Status     TaskStatus
    Nodes      []*TaskNode
    Edges      []*TaskEdge
    Artifacts  map[string]Artifact
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

### 文件 3

- `backend/internal/models/artifact.go`

当前状态：

- 不存在，建议新增

建议新增结构：

- `Artifact`

用途：

- 记录节点产物
- 为后继节点提供输入

### 文件 4

- `backend/internal/models/intent.go`

当前状态：

- 不存在，建议新增

建议新增结构：

- `IntentContext`

用途：

- 统一承接意图识别结果
- 隔离 Planner 和具体识别来源

---

## 3.2 Planner 层

### 文件 5

- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

当前问题：

- 只有 `GeneratePlan(intent, intentType)`
- 生成的是 mock Task map

改造目标：

- 变成真正的图构建器

建议调整：

- 保留 `NewPlanner()`
- 用新方法替换或并存：
  - `BuildPlan(ctx context.Context, intent models.IntentContext) (*models.PlanGraph, error)`

建议拆分的内部函数：

- `buildBaseTasks`
- `buildEdges`
- `bindArtifacts`
- `annotateParallelism`
- `validatePlan`

具体要做的事情：

1. 先把当前模板任务迁移成节点数组
2. 再把依赖关系从 `Dependencies` 转成 `Edges`
3. 再给每个节点补 `RequiredArtifacts / OutputArtifacts`
4. 最后加 DAG 校验

第一版可以保留当前这几个意图类型：

- `Framework_Evaluation`
- `Paper_Reproduction`
- `Code_Execution`
- `General`

### 文件 6

- `backend/internal/planner/templates.go`

当前状态：

- 不存在，建议新增

用途：

- 把不同意图类型的任务模板拆出去
- 避免 `planner.go` 膨胀

建议内容：

- `buildPaperReproductionTemplate`
- `buildFrameworkEvaluationTemplate`
- `buildCodeExecutionTemplate`

这样后续改某类任务流时，不用在一个文件里改所有逻辑

### 文件 7

- `backend/internal/planner/validate.go`

当前状态：

- 不存在，建议新增

用途：

- 专门负责 DAG 校验

建议校验项：

- 是否有环
- 边的起终点是否存在
- artifact 依赖是否有来源
- 节点 ID 是否重复

---

## 3.3 Store 层

### 文件 8

- `backend/internal/store/plan_store.go`

当前状态：

- 不存在，建议新增

建议定义接口：

```go
type PlanStore interface {
    SavePlan(plan *models.PlanGraph) error
    GetPlan(planID string) (*models.PlanGraph, error)
    UpdateTask(planID string, taskID string, update func(*models.TaskNode) error) error
    SaveArtifacts(planID string, artifacts []models.Artifact) error
    AppendEvent(planID string, event models.PlanEvent) error
}
```

### 文件 9

- `backend/internal/store/memory_plan_store.go`

当前状态：

- 不存在，建议新增

用途：

- 第一版内存存储实现

实现建议：

- 用 `map[string]*models.PlanGraph`
- 配 `sync.RWMutex`

注意事项：

- 节点状态更新必须加锁
- SSE 读取和调度器更新会并发访问

---

## 3.4 调度层

### 文件 10

- `backend/internal/scheduler/scheduler.go`

当前状态：

- 不存在，建议新增

职责：

- 执行整张图
- 找 ready 节点
- 推进依赖关系

建议接口：

```go
type Scheduler struct {
    store         store.PlanStore
    executor      TaskExecutor
    eventBus      EventBus
    maxConcurrent int
}
```

建议方法：

- `ExecutePlan(ctx context.Context, planID string) error`
- `findReadyNodes(plan *models.PlanGraph) []*models.TaskNode`
- `updatePlanStatus(plan *models.PlanGraph)`
- `blockDependents(plan *models.PlanGraph, taskID string)`

### 文件 11

- `backend/internal/scheduler/executor.go`

职责：

- 执行单节点
- 根据 `AssignedTo` 分发 Agent

建议接口：

```go
type TaskExecutor interface {
    ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error)
}
```

建议实现：

- `DefaultTaskExecutor`

内部逻辑：

- `librarian_agent` -> LibrarianAgent
- `coder_agent` -> CoderAgent
- `sandbox_agent` -> CoderAgent 或专门 SandboxExecutor
- `data_agent` -> DataAgent

### 文件 12

- `backend/internal/scheduler/graph.go`

用途：

- 放图遍历辅助函数

建议函数：

- `GetNodeByID`
- `GetOutgoingNodes`
- `GetIncomingNodes`
- `AllDependenciesCompleted`
- `ArtifactsSatisfied`

### 文件 13

- `backend/internal/scheduler/policy.go`

用途：

- 放失败传播、重试策略

第一版规则建议简单一些：

- 节点失败 -> 直接依赖它的后继节点标记 `blocked`
- 不做自动重试

---

## 3.5 事件流层

### 文件 14

- `backend/internal/models/event.go`

建议新增结构：

- `PlanEvent`

建议内容：

```go
type PlanEvent struct {
    PlanID     string
    EventType  string
    TaskID     string
    TaskStatus string
    Payload    map[string]any
    Timestamp  time.Time
}
```

### 文件 15

- `backend/internal/events/bus.go`

当前状态：

- 不存在，建议新增

用途：

- 统一广播 Plan 运行事件

建议能力：

- `Publish(planID, event)`
- `Subscribe(planID)`
- `Unsubscribe(planID, subscriberID)`

如果先不做完整事件总线，也可以在 `routes.go` 里先用 channel 做最小版

---

## 3.6 API 层

### 文件 16

- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)

当前问题：

- 规划与执行接口边界不清晰

建议改造：

- 保留 `POST /api/plan`
- 新增 `POST /api/plans/:id/execute`
- 新增 `GET /api/plans/:id`
- 新增 `GET /api/plans/:id/stream`

### `POST /api/plan`

改造目标：

- 不再返回旧式 `Tasks map`
- 返回 `PlanGraph`

内部变化：

1. 把请求里的 `intent` 转成 `IntentContext`
2. 调用 `Planner.BuildPlan`
3. 把结果存到 `PlanStore`
4. 返回完整图给前端

### `POST /api/plans/:id/execute`

改造目标：

- 后端启动整图执行

内部变化：

1. 从 `PlanStore` 取 Plan
2. 启动 `Scheduler.ExecutePlan`
3. 立即返回启动成功

### `GET /api/plans/:id`

用途：

- 刷新页面后拉取整图状态

### `GET /api/plans/:id/stream`

用途：

- 前端监听执行状态变化

建议协议：

- 使用 SSE
- 每条消息传一个 `PlanEvent`

---

## 3.7 Agent 适配层

### 文件 17

- [backend/internal/agent/coder.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)
- [backend/internal/agent/librarian.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/agent/librarian.go)
- [backend/internal/agent/data.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/agent/data.go)

当前问题：

- Agent 接口是按单任务执行设计的
- 还没有很好地消费 `RequiredArtifacts`

建议改造方向：

- 保留现有 `ExecuteTask`
- 在调度器调用前先把 artifact 注入 `task.Description` 或 `sharedContext`

第一版可以先不大改 Agent，只做调度层适配

也就是说：

- 先让 Scheduler 负责把前序产物拼进任务上下文
- Agent 仍然按现有接口执行

这样改动范围更可控

---

## 3.8 前端适配层

### 文件 18

- [frontend/src/App.tsx](/D:/mygo/Sea-mult-agent/scholar-agent/frontend/src/App.tsx)

当前问题：

- 依赖旧的 `Plan.Tasks`
- 执行是单节点模式

建议改造：

1. 接收新的 `PlanGraph`
2. 根据 `Nodes + Edges` 渲染 DAG
3. 增加“执行整个计划”按钮
4. 建立计划级 SSE 连接
5. 收到事件后增量更新节点状态

建议前端状态结构：

- `planGraph`
- `planEvents`
- `selectedTask`
- `taskStates`

---

## 4. 推荐的结构体增量方案

为了避免一次性重命名过多，我建议采用“增量兼容”方式：

### 第一版

- 保留 `Task`
- 新增 `TaskEdge`
- 新增 `PlanGraph`
- 新增 `Artifact`
- 新增 `IntentContext`
- 前端逐步改为以 `PlanGraph` 为主

### 第二版

等跑通后，再决定是否：

- 把旧 `Plan` 废弃
- 把旧 `Task` 完全升级成 `TaskNode`

这样更稳

---

## 5. 实施优先级

## P0：必须先做

- 模型结构
- Planner 输出图结构
- 内存 Store
- Scheduler 基础执行
- `POST /api/plans/:id/execute`
- `GET /api/plans/:id/stream`

## P1：紧接着做

- 前端整图执行按钮
- 前端订阅计划级事件
- 前端状态颜色和节点增量刷新

## P2：后续优化

- Python 意图识别接入
- 持久化 Store
- 自动重试策略
- 更精细的 artifact 结构
- 更丰富的失败传播与恢复

---

## 6. 第一版最小交付范围

为了尽快落地，我建议第一版只做到：

1. `/api/plan` 返回完整拓扑图
2. `/api/plans/:id/execute` 启动整图执行
3. Scheduler 支持 ready 节点并发执行
4. 节点状态变化通过 SSE 推前端
5. 前端实时更新拓扑图

先不要在第一版做：

- 数据库持久化
- 自动恢复执行
- 复杂失败恢复
- 动态重规划
- 太细的多种 edge 语义优化

---

## 7. 接口字段草案

## 7.1 `POST /api/plan` 响应草案

```json
{
  "message": "Plan generated successfully",
  "plan": {
    "id": "plan-123",
    "user_intent": "复现某论文",
    "intent_type": "Paper_Reproduction",
    "status": "pending",
    "nodes": [],
    "edges": [],
    "artifacts": {},
    "created_at": "",
    "updated_at": ""
  }
}
```

## 7.2 `POST /api/plans/:id/execute` 响应草案

```json
{
  "message": "Plan execution started",
  "plan_id": "plan-123"
}
```

## 7.3 `GET /api/plans/:id/stream` 事件草案

```json
{
  "plan_id": "plan-123",
  "event_type": "task_started",
  "task_id": "task-2",
  "task_status": "in_progress",
  "payload": {
    "assigned_to": "coder_agent"
  },
  "timestamp": "2026-04-07T22:00:00Z"
}
```

---

## 8. 推荐的开发顺序细化

### 第一步

先改模型：

- `task.go`
- `graph.go`
- `artifact.go`
- `intent.go`
- `event.go`

### 第二步

改 Planner：

- `planner.go`
- `templates.go`
- `validate.go`

先做到“能稳定生成图”

### 第三步

加内存 Store：

- `plan_store.go`
- `memory_plan_store.go`

### 第四步

加 Scheduler：

- `scheduler.go`
- `executor.go`
- `graph.go`

先做到“能执行整图”

### 第五步

改 API：

- `routes.go`

先把：

- 创建计划
- 获取计划
- 启动执行
- SSE 订阅

都接起来

### 第六步

改前端：

- 适配图结构
- 适配计划级执行
- 适配事件流

---

## 9. 风险控制建议

### 9.1 兼容旧前端

如果你想减少前端改动，可以在第一阶段后端同时返回：

- 新结构 `nodes / edges`
- 旧结构 `tasks`

等前端迁完再删旧结构

### 9.2 避免大爆炸式重构

不要一次性把：

- Planner
- Scheduler
- Agent
- Frontend

全部推翻

推荐策略：

- 先新增模块
- 旧逻辑并存
- 功能跑通后再删旧接口

### 9.3 并发安全

Store 必须保证：

- 更新节点状态线程安全
- 追加事件线程安全
- 读取图状态线程安全

---

## 10. 审核重点

你在审核这份实施清单时，建议重点看这几个决策是否认可：

1. 是否接受 `PlanGraph = Nodes + Edges + Artifacts` 的主结构
2. 是否接受第一版先用内存 Store
3. 是否接受通过 SSE 推增量事件，而不是每次重传整图
4. 是否接受 Scheduler 作为独立模块
5. 是否接受第一版先保守处理失败传播

---

## 11. 结论

这份清单的核心思路是：

- 先把图模型搭起来
- 再把调度器搭起来
- 再把接口和前端接起来

也就是说，后续开发时，优先顺序不是“先改页面”，而是：

1. 模型
2. 规划
3. 调度
4. 接口
5. 前端

这是当前最稳、返工最少的一条落地路径。
