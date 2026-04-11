# 规划与调度后端专项文档

## 1. 作用范围

这份文档只覆盖 `backend/` 中的“规划与调度后端”部分，供后续修改时快速定位代码。

适用范围：

- Planner 任务规划
- Plan / Task 数据模型
- 任务状态定义
- `/api/plan` 请求入口
- 规划结果如何流向前端和执行链路

不覆盖：

- 前端渲染细节
- `ai-services/` 的 Python 意图识别实现
- `docker-sandbox/` 的底层容器执行实现

---

## 2. 当前技术栈

- 语言：Go
- Go 版本：`1.26.1`
- Web 框架：Gin

代码依据：

- [backend/go.mod](/D:/mygo/Sea-mult-agent/scholar-agent/backend/go.mod)
- [backend/cmd/api/main.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/cmd/api/main.go)

---

## 3. 代码位置总览

### 3.1 规划入口

- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)

关键位置：

- `SetupRoutes`
  - 初始化 `p := planner.NewPlanner()`
  - 注册 `POST /api/plan`
- `/api/plan` handler
  - 接收前端的 `intent`
  - 基于关键词推断 `intentType`
  - 调用 `p.GeneratePlan(payload.Intent, intentType)`
  - 返回 `plan`

### 3.2 规划器实现

- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

关键对象：

- `type Planner struct {}`
- `func NewPlanner() *Planner`
- `func (p *Planner) GeneratePlan(intent string, intentType string) (*models.Plan, error)`
- `func createMockTask(name, agent string, deps []string, context string) *models.Task`

### 3.3 数据模型

- [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)

关键对象：

- `type TaskStatus string`
- `const StatusPending / StatusInProgress / StatusCompleted / StatusFailed`
- `type Task struct`
- `type Plan struct`

---

## 4. 结构化映射

### 4.1 Planner

职责：

- 根据 `intent` 和 `intentType` 生成一个 `Plan`
- 将复杂任务拆成多个 `Task`
- 为每个 `Task` 设置：
  - 名称
  - 描述
  - 执行 Agent
  - 前置依赖 `Dependencies`
  - 初始状态

当前实现方式：

- 不是动态 LLM 规划
- 是按 `intentType` 走固定模板生成 mock DAG

代码入口：

- [planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

要修改规划逻辑时优先看：

1. `GeneratePlan`
2. `createMockTask`

### 4.2 Models

职责：

- 统一定义 Plan 与 Task 的结构
- 定义任务状态枚举
- 作为规划结果、执行输入、执行状态流转的公共数据结构

代码入口：

- [task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)

要修改数据结构时优先看：

1. `TaskStatus`
2. `Task`
3. `Plan`

---

## 5. 真实调用链

### 5.1 规划请求链路

1. 前端请求 `POST /api/plan`
2. 进入 [routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go) 的 `/api/plan` handler
3. handler 根据关键词推断 `intentType`
4. 调用 [planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go) 里的 `GeneratePlan`
5. `GeneratePlan` 构造 [task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go) 中定义的 `Plan` 和 `Task`
6. 规划结果作为 JSON 返回前端

### 5.2 执行衔接链路

当前 `Plan` 本身只用于前端展示和节点选择，不是由后端统一调度执行器自动消费。

实际执行时：

1. 前端从 DAG 中拿到某个 `Task`
2. 前端单独请求 `POST /api/execute`
3. 后端重新构造一个 `models.Task`
4. 再按 `AssignedTo` 分发给不同 Agent

这意味着：

- `Plan` 是“规划产物”
- `/api/execute` 是“单任务执行入口”
- 目前还没有一个完整的“按 DAG 自动调度执行全部任务”的后端调度器

这个结论后面改代码时很重要，避免误以为 `Planner` 已经接管完整调度闭环

---

## 6. 当前 Planner 的分支逻辑

文件：

- [planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

### `Framework_Evaluation`

生成任务序列：

1. 检索文档与最佳实践
2. 为框架 A 生成环境与集成代码
3. 为框架 B 生成环境与集成代码
4. 在沙箱中执行 A/B 测试
5. 分析指标并生成报告

### `Paper_Reproduction`

生成任务序列：

1. 解析论文并提取算法细节
2. 查找/克隆开源仓库
3. 配置沙箱环境和依赖
4. 执行 baseline 代码
5. 对比论文结果
6. 如果结果不一致则调试/修正代码

### `Code_Execution`

生成任务序列：

1. 生成并运行代码
2. 校验结果

### 默认分支

生成任务序列：

1. 通用请求处理

---

## 7. Models 字段说明

文件：

- [task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)

### `TaskStatus`

当前状态枚举：

- `pending`
- `in_progress`
- `completed`
- `failed`

### `Task`

字段说明：

- `ID`
  - 任务唯一标识

- `Name`
  - 任务名称

- `Description`
  - 任务描述

- `AssignedTo`
  - 分配给哪个 Agent
  - 当前代码中常见值：
    - `librarian_agent`
    - `coder_agent`
    - `sandbox_agent`
    - `data_agent`
    - `general_agent`

- `Status`
  - 任务状态

- `Dependencies`
  - 前置任务 ID 列表
  - 用来表达 DAG 边关系

- `Result`
  - 执行结果文本

- `Code`
  - 生成的代码

- `ImageBase64`
  - 生成图片的 Base64 内容

- `Error`
  - 错误信息

- `CreatedAt`
  - 创建时间

- `UpdatedAt`
  - 更新时间

### `Plan`

字段说明：

- `ID`
  - 计划唯一标识

- `UserIntent`
  - 用户原始意图文本

- `Tasks`
  - `map[string]*Task`
  - 以任务 ID 为 key 的任务集合

- `Status`
  - 计划状态

- `CreatedAt`
  - 创建时间

- `UpdatedAt`
  - 更新时间

---

## 8. 修改指引

### 8.1 如果要改“意图到任务流”的映射

改这里：

- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)
- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

通常涉及：

- 关键词如何映射到 `intentType`
- 某个 `intentType` 应该生成什么 DAG
- 每个任务的 `AssignedTo`
- 依赖关系 `Dependencies`

### 8.2 如果要改 Task / Plan 的字段

改这里：

- [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)

通常还要联动检查：

- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)
- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)
- [frontend/src/App.tsx](/D:/mygo/Sea-mult-agent/scholar-agent/frontend/src/App.tsx)

因为前端直接依赖 `Task` / `Plan` 返回结构来渲染 DAG

### 8.3 如果要增加新的任务状态

先改这里：

- [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go)

再联动检查：

- [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)
- [backend/internal/agent/*.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/agent)
- [frontend/src/App.tsx](/D:/mygo/Sea-mult-agent/scholar-agent/frontend/src/App.tsx)

因为执行状态展示和节点颜色可能依赖状态值

### 8.4 如果要把 mock 规划升级成真实规划器

优先改这里：

- [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go)

建议保持这几个边界稳定：

- 输入仍然是 `intent`、`intentType`
- 输出仍然是 `*models.Plan`
- `Task.Dependencies` 继续承担 DAG 边关系

这样能最大程度减少对前端和 API 的破坏

---

## 9. 后续修改时的默认定位规则

以后凡是涉及“规划与调度后端”，默认按下面顺序定位：

1. 先看 [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go) 确认请求入口和调用位置
2. 再看 [backend/internal/planner/planner.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/planner/planner.go) 确认 DAG 生成逻辑
3. 最后看 [backend/internal/models/task.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/models/task.go) 确认字段和状态定义

如果改动影响返回结构，再补查：

- [frontend/src/App.tsx](/D:/mygo/Sea-mult-agent/scholar-agent/frontend/src/App.tsx)

---

## 10. 给后续修改留的结论

当前这部分代码的本质是：

- `routes.go` 负责接收规划请求
- `planner.go` 负责按模板生成 DAG
- `task.go` 负责定义 Plan / Task / Status

也就是说，后面如果你说“改规划逻辑”“改任务结构”“加状态”“加依赖字段”“加新意图类型”，我都应该先从这份文档列出的 3 个文件开始找，而不是到整个仓库里盲找。
