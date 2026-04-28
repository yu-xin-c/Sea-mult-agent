# Plan 模块重构方案（完整稿）

## 1. 文档目标

这份文档用于描述 ScholarAgent 当前 `Plan` / `Planner` / 调度执行链路的重构方案，目标是把现有“静态 mock 规划 + 单任务执行”的实现，升级为：

- 基于意图识别结果自动生成拓扑图
- 显式标记并行节点、依赖信息、输入输出信息
- 后端按拓扑自动调度执行
- 每次状态变化实时推送前端
- 前端直接可视化整个执行流程

这是一份设计方案，不是实现说明。

---

## 2. 当前问题

当前代码的核心现状：

- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)
  - 只根据 `intentType` 返回一个固定模板 DAG
  - 更像“任务清单生成器”，不是“可运行规划器”

- [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)
  - 只有基础 `Plan` / `Task` 数据结构
  - 不能完整表达输入需求、产物依赖、并行性、运行态信息

- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)
  - `/api/plan` 只负责回一个计划
  - `/api/execute` 只执行单任务
  - 没有“整图调度器”

- 前端目前虽然能画 DAG，但后端没有把“图的运行态”作为一等公民管理

因此当前架构的问题是：

1. 规划和执行是割裂的
2. DAG 只用于展示，不用于真正调度
3. 没有统一的图状态流
4. 节点间的信息依赖没有结构化表达
5. 不能自然支持并行执行和状态可视化

---

## 3. 重构目标

本次重构的目标如下：

### 3.1 规划目标

- 根据意图识别结果自动生成任务拓扑图
- 每个节点都清楚表达：
  - 是什么任务
  - 分配给哪个 Agent
  - 依赖哪些前置节点
  - 需要哪些输入信息
  - 会产出哪些结果信息
  - 是否允许并行

### 3.2 调度目标

- 后端可以从拓扑图中自动找出当前可执行节点
- 对“入度为 0 且输入满足”的节点并发执行
- 节点完成后更新后继节点状态
- 推动整张图自动向前执行

### 3.3 可视化目标

- 拓扑图生成后直接返回前端
- 前端拿到图后立即渲染
- 执行过程中每次节点状态变化都推送事件
- 前端实时更新整张图，实现可视化执行过程

### 3.4 演进目标

- 第一阶段使用内存态管理即可
- 后续可扩展为数据库持久化
- 可逐步接入 Python 意图识别微服务

---

## 4. 重构后的整体架构

建议把“规划与调度后端”拆成 5 层：

### 4.1 Intent Adapter

职责：

- 接收意图识别结果
- 统一转换成后端可消费的结构化上下文

输入来源可以有两种：

1. 当前关键词识别逻辑
2. 后续的 Python 意图识别服务

输出统一为 `IntentContext`

### 4.2 Plan Builder

职责：

- 根据 `IntentContext` 构建任务图
- 生成节点、边、依赖、并行标记、输入输出信息
- 校验 DAG 合法性

### 4.3 Graph Scheduler

职责：

- 维护运行中的拓扑图状态
- 计算当前 ready 节点
- 并发调度执行
- 节点完成后推进图状态

### 4.4 Task Runtime

职责：

- 把单个节点路由到对应 Agent 执行
- 统一收集执行结果、日志、错误、产物

### 4.5 State Store

职责：

- 存储 Plan、Task、Artifact、事件流
- 第一阶段可用内存实现
- 后续可替换成持久化实现

---

## 5. 核心理念

### 5.1 DAG 既是展示模型，也是执行模型

重构后，拓扑图不再只是前端展示用，而是后端的真正执行依据。

也就是说：

- `Plan` 不再是“静态任务列表”
- `Plan` 是“可执行任务图”

### 5.2 依赖分为两类

#### 控制依赖

表示执行顺序关系：

- A 完成后 B 才能开始

#### 信息依赖

表示输入产物关系：

- B 需要 A 产出的 `repo_path`
- C 需要 A 和 B 的结果摘要

这两类依赖都要在模型中被表达出来

### 5.3 并行不是手工硬编码，而是由依赖关系推导

只要一个节点：

- 所有控制依赖都已完成
- 所有必需输入都已满足
- 且没有资源互斥约束

它就应该进入 ready 状态，可并发执行

---

## 6. 推荐的数据结构设计

建议重构现有 [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)，并新增图结构和产物结构。

## 6.1 IntentContext

```go
type IntentContext struct {
    RawIntent   string
    IntentType  string
    Entities    map[string]any
    Constraints map[string]any
    Metadata    map[string]any
}
```

说明：

- `RawIntent`：用户原始输入
- `IntentType`：意图类别，例如 `Paper_Reproduction`
- `Entities`：抽取到的实体，比如 `paper_url`、`framework_name`
- `Constraints`：约束条件，比如是否必须复现、是否要对比、是否需要图表
- `Metadata`：扩展字段

## 6.2 TaskStatus

建议从当前 4 个状态扩成 8 个状态：

```go
type TaskStatus string

const (
    StatusPending    TaskStatus = "pending"
    StatusReady      TaskStatus = "ready"
    StatusInProgress TaskStatus = "in_progress"
    StatusCompleted  TaskStatus = "completed"
    StatusFailed     TaskStatus = "failed"
    StatusBlocked    TaskStatus = "blocked"
    StatusSkipped    TaskStatus = "skipped"
    StatusCanceled   TaskStatus = "canceled"
)
```

状态含义：

- `pending`：已创建但依赖未满足
- `ready`：依赖满足，可进入调度
- `in_progress`：正在执行
- `completed`：成功完成
- `failed`：执行失败
- `blocked`：依赖失败或输入缺失，无法继续
- `skipped`：按策略跳过
- `canceled`：被人工或系统取消

## 6.3 Artifact

```go
type Artifact struct {
    Key            string
    Type           string
    ProducerTaskID string
    Value          string
    Location       string
    Metadata       map[string]any
    CreatedAt      time.Time
}
```

说明：

- 一个任务产出的结构化结果
- 例如：
  - `parsed_paper`
  - `repo_path`
  - `requirements_file`
  - `benchmark_report`

## 6.4 TaskNode

```go
type TaskNode struct {
    ID                string
    Name              string
    Type              string
    Description       string
    AssignedTo        string
    Status            TaskStatus
    Dependencies      []string
    RequiredArtifacts []string
    OutputArtifacts   []string
    Parallelizable    bool
    Priority          int
    RetryLimit        int
    RunCount          int
    Result            string
    Code              string
    ImageBase64       string
    Error             string
    StartedAt         *time.Time
    FinishedAt        *time.Time
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

字段说明：

- `Dependencies`
  - 控制依赖
- `RequiredArtifacts`
  - 需要哪些前置节点产物
- `OutputArtifacts`
  - 当前节点会产出哪些 artifact key
- `Parallelizable`
  - 是否允许与同层节点并行
- `Priority`
  - 调度优先级
- `RetryLimit`
  - 最大重试次数
- `RunCount`
  - 当前已执行次数

## 6.5 TaskEdge

```go
type TaskEdge struct {
    From string
    To   string
    Type string
}
```

`Type` 建议值：

- `control`
- `data`

## 6.6 PlanGraph

```go
type PlanGraph struct {
    ID          string
    UserIntent  string
    IntentType  string
    Status      TaskStatus
    Nodes       []*TaskNode
    Edges       []*TaskEdge
    Artifacts   map[string]Artifact
    NodeIndex   map[string]*TaskNode
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

说明：

- `Nodes + Edges` 用于前端渲染和后端执行
- `Artifacts` 用于存储全局信息产物
- `NodeIndex` 便于后端快速检索，响应 JSON 时可忽略或单独处理

---

## 7. Planner 重构设计

建议保留 [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)，但职责要改成“图构建器”。

## 7.1 规划器职责

- 接收 `IntentContext`
- 构建 `PlanGraph`
- 生成节点与边
- 给节点补充依赖和输入输出信息
- 校验 DAG 合法性

## 7.2 建议方法拆分

```go
type Planner interface {
    BuildPlan(ctx context.Context, intent IntentContext) (*PlanGraph, error)
}
```

建议内部拆分为：

1. `BuildBaseTasks(intent IntentContext) ([]*TaskNode, error)`
2. `BuildEdges(nodes []*TaskNode, intent IntentContext) ([]*TaskEdge, error)`
3. `BindArtifacts(nodes []*TaskNode, edges []*TaskEdge, intent IntentContext) error`
4. `AnnotateParallelism(plan *PlanGraph)`
5. `ValidatePlan(plan *PlanGraph) error`

## 7.3 当前阶段的规划策略

第一阶段不一定要上完全动态的 LLM Planner，可以先采用：

- 意图类别 -> 任务模板
- 再基于实体和约束动态修饰模板

例如：

### `Paper_Reproduction`

基础任务链：

1. 解析论文
2. 查找开源仓库
3. 拉取仓库 / 准备工作目录
4. 安装依赖 / 配置环境
5. 执行 baseline
6. 收集结果
7. 对比论文结果
8. 结果不一致时修复与重跑

扩展点：

- 如果识别到多个候选仓库，可并行“仓库检查”
- 如果既要 baseline 又要 ablation，可在环境完成后并行运行多个实验节点

### `Framework_Evaluation`

基础任务链：

1. 检索框架资料
2. 准备框架 A 环境
3. 准备框架 B 环境
4. 运行 A
5. 运行 B
6. 汇总评估报告

并行点：

- 2 和 3 可并行
- 4 和 5 可并行

### `Code_Execution`

基础任务链：

1. 生成代码
2. 准备执行环境
3. 执行代码
4. 校验结果

如果校验任务独立于图像生成，也可以拆并行分支

---

## 8. DAG 合法性校验

Planner 生成图后必须做校验。

校验项至少包括：

1. 是否存在环
2. 每个依赖节点是否存在
3. 每个 `RequiredArtifacts` 是否存在生产者
4. 是否有重复节点 ID
5. 是否有悬空边
6. 是否有不可达节点

如果校验失败：

- 不返回部分成功图
- 直接返回错误

---

## 9. 调度器设计

建议新增 `scheduler` 模块，例如：

- `backend/internal/scheduler/scheduler.go`
- `backend/internal/scheduler/executor.go`
- `backend/internal/scheduler/store.go`
- `backend/internal/scheduler/events.go`

## 9.1 调度器职责

- 管理整张图的运行态
- 计算 ready 节点
- 并发执行节点
- 接收执行结果
- 更新图状态
- 推送事件给前端

## 9.2 ready 判定条件

一个节点进入 ready 的条件：

1. `Status == pending`
2. 所有 `Dependencies` 对应节点状态为 `completed`
3. 所有 `RequiredArtifacts` 已存在
4. 该节点允许执行，没有被 `blocked / canceled / skipped`

## 9.3 调度算法

建议使用“动态入度 + 状态机”方式。

流程：

1. 初始化图状态
2. 找出所有满足条件的节点，标为 `ready`
3. 当前轮取出全部 `ready` 节点
4. 对这些节点并发执行
5. 执行成功：
   - 标记 `completed`
   - 写入 artifacts
   - 更新后继节点依赖满足情况
6. 执行失败：
   - 标记 `failed`
   - 根据策略决定后继节点是否 `blocked`
7. 继续寻找下一轮 ready 节点
8. 直到全部结束

## 9.4 并发执行策略

每轮可执行节点建议使用 goroutine 并发：

- 一个 ready 节点一个 goroutine
- 用 `WaitGroup` 等待当前轮结束
- 或用 worker pool 限制最大并发数

建议引入全局并发限制：

- 避免一次性启动过多沙箱
- 避免压垮外部模型调用

例如：

- `maxConcurrentTasks = 4`

## 9.5 失败传播策略

建议第一阶段采用保守策略：

- 如果某节点失败
- 所有直接依赖它的后继节点标记为 `blocked`
- 如果后继节点还依赖其他成功节点，也不继续执行

后续可扩展更复杂策略：

- 容忍部分失败
- 自动重试
- 降级路径

---

## 10. 单任务运行时设计

调度器不应该直接写死每类任务怎么执行，建议通过统一运行时接口适配 Agent。

## 10.1 TaskExecutor 接口

```go
type TaskExecutor interface {
    ExecuteTask(ctx context.Context, plan *PlanGraph, task *TaskNode) (*TaskExecutionResult, error)
}
```

## 10.2 TaskExecutionResult

```go
type TaskExecutionResult struct {
    Status    TaskStatus
    Result    string
    Code      string
    ImageBase64 string
    Error     string
    Logs      []string
    Artifacts []Artifact
}
```

## 10.3 运行时职责

- 根据 `AssignedTo` 路由到不同 Agent
- 从 `PlanGraph.Artifacts` 中拿该节点所需输入
- 执行任务
- 收集产物
- 回传给调度器

---

## 11. 状态存储设计

建议先抽象存储接口，再做内存实现。

## 11.1 Store 接口

```go
type PlanStore interface {
    SavePlan(plan *PlanGraph) error
    GetPlan(planID string) (*PlanGraph, error)
    UpdateTask(planID string, task *TaskNode) error
    SaveArtifacts(planID string, artifacts []Artifact) error
    AppendEvent(planID string, event PlanEvent) error
}
```

## 11.2 第一阶段实现

先做内存版：

- 简单
- 便于调试
- 改动小

## 11.3 后续扩展

后续如果需要页面刷新后还能恢复状态，再接：

- SQLite
- Postgres
- Redis + DB

---

## 12. 事件流与前端同步

这一部分是这次方案的重点。

## 12.1 设计原则

- 拓扑图先整体返回前端
- 执行中不重复全量重传图
- 只推送增量事件
- 前端本地合并更新节点状态

## 12.2 事件模型

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

## 12.3 建议事件类型

- `plan_created`
- `plan_started`
- `task_ready`
- `task_started`
- `task_completed`
- `task_failed`
- `task_blocked`
- `artifact_created`
- `plan_completed`
- `plan_failed`

## 12.4 前端如何更新图

前端拿到完整图后：

1. 初始化 DAG 画布
2. 建立 SSE 连接
3. 每收到一个 `PlanEvent`
4. 更新对应节点的：
   - 状态
   - 日志
   - 结果
   - 产物标记
5. 重新渲染相关节点和边

前端不需要重新拉整图，除非断线重连

---

## 13. API 设计建议

当前接口需要从“单任务时代”升级成“整图时代”。

## 13.1 `POST /api/plan`

职责：

- 输入用户意图
- 构建完整拓扑图
- 存储 Plan
- 返回图结构

请求：

```json
{
  "intent": "复现某论文并对比实验结果"
}
```

响应：

- `plan_id`
- `nodes`
- `edges`
- `status`
- `intent_type`
- `artifacts`

## 13.2 `POST /api/plans/:id/execute`

职责：

- 启动整图执行
- 后端自动调度 ready 节点

这个接口只负责启动，不必同步等待所有任务结束

## 13.3 `GET /api/plans/:id`

职责：

- 获取当前 PlanGraph 状态
- 用于刷新页面后重载图状态

## 13.4 `GET /api/plans/:id/stream`

职责：

- SSE 推送状态更新事件

## 13.5 `POST /api/plans/:id/tasks/:taskId/execute`

职责：

- 手动执行单节点
- 用于调试和人工干预

可选保留，不是第一阶段必须

---

## 14. 前端可视化建议

前端拓扑图应展示：

- 节点名称
- 节点类型
- Agent 类型
- 当前状态
- 所需输入
- 已产出结果
- 是否并行

## 14.1 节点颜色建议

- `pending`：灰色
- `ready`：蓝色
- `in_progress`：黄色
- `completed`：绿色
- `failed`：红色
- `blocked`：深灰
- `skipped`：浅灰

## 14.2 边样式建议

- `control`：实线
- `data`：虚线

## 14.3 并行展示建议

并行节点可以采用：

- 同层布局
- 或增加 `parallel_group`
- 或在节点上显示“可并行”标签

建议布局仍由前端负责，不把坐标计算放到后端

---

## 15. 对现有代码的改造范围

## 15.1 必改文件

- [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)
  - 重构状态和节点结构

- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)
  - 改为图构建器

- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)
  - 增加整图执行与 SSE 接口

## 15.2 建议新增文件

- `backend/internal/models/artifact.go`
- `backend/internal/models/graph.go`
- `backend/internal/scheduler/scheduler.go`
- `backend/internal/scheduler/executor.go`
- `backend/internal/scheduler/store.go`
- `backend/internal/scheduler/events.go`

## 15.3 前端联动文件

- [frontend/src/App.tsx](/D:/mygo/Sea-mult-agent/scholar-agent/frontend/src/App.tsx)

需要适配：

- 新图结构
- 计划级执行入口
- 计划级 SSE 订阅
- 节点状态增量更新

---

## 16. 建议的实施阶段

## 阶段 1：模型重构

目标：

- 定义 `IntentContext`
- 重构 `TaskStatus`
- 新增 `TaskNode / TaskEdge / PlanGraph / Artifact`

产出：

- 后端具备表达完整拓扑图的能力

## 阶段 2：Planner 重构

目标：

- 把 `planner.go` 改成真正的 DAG 构建器
- 生成控制依赖和信息依赖
- 完成 DAG 合法性校验

产出：

- `/api/plan` 可以直接返回完整拓扑图

## 阶段 3：Scheduler 落地

目标：

- 支持自动找 ready 节点
- 支持并发执行
- 支持状态推进

产出：

- 后端可执行整张图，而不是只执行单任务

## 阶段 4：状态流

目标：

- 增加 PlanEvent
- 增加 SSE 输出
- 前端实时更新拓扑图

产出：

- 执行流程可视化

## 阶段 5：意图识别接入

目标：

- 将 Python 网关结果统一接到 `IntentContext`

产出：

- 完整闭环：意图识别 -> 拓扑规划 -> 自动执行 -> 可视化反馈

---

## 17. 风险与注意事项

### 17.1 接口变更风险

当前前端直接依赖旧的 `Plan.Tasks` 结构，重构后前端需要适配 `Nodes / Edges`

### 17.2 状态一致性风险

如果调度器并发更新任务状态，没有统一 Store 或锁机制，容易状态错乱

### 17.3 资源竞争风险

多个并行节点如果共享工作目录、容器、artifact key，容易互相污染

### 17.4 错误传播风险

如果没有清晰的失败传播策略，图会出现“未完成也未失败”的悬空状态

### 17.5 复杂度控制

第一阶段不要一口气做“智能重规划、自愈式 DAG、数据库恢复”

建议先做：

- 稳定图结构
- 稳定调度器
- 稳定事件流

---

## 18. 验收标准

完成本轮重构后，至少满足以下标准：

1. 给定一个意图，后端能返回完整拓扑图
2. 图中每个节点都能看出依赖、输入、输出、状态、并行属性
3. 后端能自动找出当前 ready 节点并并发执行
4. 节点完成后能推进后继节点
5. 每次状态变化都能推送前端
6. 前端能实时更新拓扑图
7. 整个执行流程对用户可视化

---

## 19. 当前建议的最小可行版本

如果要控制复杂度，建议先落一个 MVP：

- 保留现有几类 `intentType`
- 先不接真实 Python 网关
- 先用内存 Store
- 先支持 `Nodes + Edges + SSE`
- 先做整图自动执行
- 先做基础失败阻断

这一版能最快验证核心架构是否正确

---

## 20. 结论

本次 Plan 模块重构，本质上不是只改一个 `planner.go`，而是把当前的“计划生成”升级成“可执行拓扑引擎”。

重构后的目标模式应该是：

1. 意图识别产生结构化输入
2. Planner 生成完整拓扑图
3. 拓扑图直接返回前端展示
4. Scheduler 按拓扑自动并发执行
5. 状态变化实时推送前端
6. 前端持续更新拓扑图，实现流程可视化

这套方案兼顾了：

- 结构清晰
- 可扩展
- 易于前端展示
- 易于后续接入真实意图识别和持久化

后续如果进入实施阶段，建议先基于这份文档继续细化一版“文件级改造清单”和“接口字段草案”。
