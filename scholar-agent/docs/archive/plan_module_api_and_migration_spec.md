# Plan 模块接口契约与迁移说明

## 1. 文档目标

这份文档补充 `Plan` 模块重构中的两个关键问题：

1. 新旧接口和前后端契约到底怎么定
2. 现有代码如何平滑迁移到新架构

如果不把这两部分写清楚，后面很容易出现：

- 后端模型改好了但前端接不上
- 新旧接口并存时语义混乱
- 调度器上线后旧 `/api/execute` 还在误用

---

## 2. 新旧结构对照

## 2.1 现有结构

当前后端返回的核心结构接近：

```json
{
  "plan": {
    "ID": "xxx",
    "UserIntent": "...",
    "Tasks": {
      "task-id-1": {
        "ID": "...",
        "Name": "...",
        "Description": "...",
        "AssignedTo": "...",
        "Status": "pending",
        "Dependencies": []
      }
    },
    "Status": "pending"
  }
}
```

前端依赖方式：

- 遍历 `plan.Tasks`
- 再自行推导边和展示

## 2.2 目标结构

目标后端返回：

```json
{
  "plan": {
    "id": "plan_xxx",
    "user_intent": "...",
    "intent_type": "Paper_Reproduction",
    "status": "pending",
    "nodes": [],
    "edges": [],
    "artifacts": {},
    "meta": {}
  }
}
```

前端依赖方式改成：

- 直接消费 `nodes`
- 直接消费 `edges`
- 不再自己从 `Tasks map` 猜边

---

## 3. 接口契约

## 3.1 创建计划接口

### 路由

- `POST /api/plan`

### 作用

- 根据意图创建计划图

### 请求体

```json
{
  "intent": "复现某论文并生成结果分析图"
}
```

### 成功响应

```json
{
  "message": "Plan generated successfully",
  "plan": {
    "id": "plan_001",
    "user_intent": "复现某论文并生成结果分析图",
    "intent_type": "Paper_Reproduction",
    "status": "pending",
    "nodes": [
      {
        "id": "task_1",
        "name": "Parse Paper",
        "type": "paper_parse",
        "description": "解析论文并抽取算法细节",
        "assigned_to": "librarian_agent",
        "status": "ready",
        "dependencies": [],
        "required_artifacts": [],
        "output_artifacts": ["parsed_paper"],
        "parallelizable": true,
        "priority": 0,
        "retry_limit": 0,
        "run_count": 0
      }
    ],
    "edges": [
      {
        "id": "edge_1",
        "from": "task_1",
        "to": "task_2",
        "type": "control"
      }
    ],
    "artifacts": {},
    "meta": {
      "total_nodes": 8,
      "completed_nodes": 0,
      "failed_nodes": 0,
      "blocked_nodes": 0,
      "in_progress_nodes": 0,
      "ready_nodes": 1
    }
  }
}
```

### 错误响应

```json
{
  "error": "invalid plan graph: cycle detected"
}
```

---

## 3.2 执行整个计划接口

### 路由

- `POST /api/plans/:id/execute`

### 作用

- 启动整图调度

### 请求体

可为空：

```json
{}
```

### 成功响应

```json
{
  "message": "Plan execution started",
  "plan_id": "plan_001"
}
```

### 冲突响应

如果计划已在执行：

```json
{
  "error": "plan is already running"
}
```

建议状态码：

- `409 Conflict`

### 不存在响应

```json
{
  "error": "plan not found"
}
```

建议状态码：

- `404 Not Found`

---

## 3.3 查询计划接口

### 路由

- `GET /api/plans/:id`

### 作用

- 获取当前最新图状态

### 响应

- 返回完整 `plan`

使用场景：

- 页面刷新
- SSE 断线恢复
- 调试状态核对

---

## 3.4 计划事件流接口

### 路由

- `GET /api/plans/:id/stream`

### 作用

- SSE 订阅计划执行过程

### SSE 格式

推荐固定为：

```text
event: plan_event
data: {"plan_id":"plan_001","event_type":"task_started",...}
```

心跳：

```text
event: heartbeat
data: keep-alive
```

### 连接行为

- 服务端保持连接
- 每 5 秒发一次心跳
- 计划结束后连接可以不断，也可以由前端主动关闭

推荐：

- 服务端不断开
- 前端收到 `plan_completed/plan_failed` 后主动关闭

---

## 3.5 手动执行单节点接口

### 路由

- `POST /api/plans/:id/tasks/:taskId/execute`

### 第一版建议

- 不实现

理由：

- 会和整图自动调度冲突
- 调度锁和节点状态锁会复杂很多

后续如需实现，必须增加：

- 计划锁
- 单节点状态校验
- 人工干预权限控制

---

## 4. 前端契约

## 4.1 前端必须假设后端返回的主结构

前端以后应以 `plan.nodes + plan.edges` 为主，不再依赖：

- `Tasks map`
- 人工排序逻辑
- 自行推导依赖

## 4.2 前端节点渲染字段

前端渲染节点时，至少依赖这些字段：

- `id`
- `name`
- `type`
- `assigned_to`
- `status`
- `parallelizable`
- `required_artifacts`
- `output_artifacts`
- `error`

## 4.3 前端本地状态模型

建议前端维护：

```ts
type PlanGraphState = {
  planId: string
  status: string
  nodes: Record<string, TaskNode>
  edges: TaskEdge[]
  artifacts: Record<string, Artifact>
  events: PlanEvent[]
}
```

核心原则：

- 收到 `/api/plan` 时全量初始化
- 收到 SSE 时按 `task_id` 增量更新

## 4.4 前端执行流程

1. 用户提交意图
2. 前端调用 `/api/plan`
3. 前端画出拓扑图
4. 用户点击“执行计划”
5. 前端调用 `/api/plans/:id/execute`
6. 前端打开 `/api/plans/:id/stream`
7. 前端实时刷新节点状态

---

## 5. SSE 事件到前端 UI 的映射

## 5.1 状态更新

### `task_ready`

- 节点颜色变蓝
- 显示“可执行”

### `task_started`

- 节点颜色变黄
- 显示“执行中”

### `task_completed`

- 节点颜色变绿
- 可展开结果摘要

### `task_failed`

- 节点颜色变红
- 显示错误信息

### `task_blocked`

- 节点颜色变深灰
- 显示“被上游阻断”

## 5.2 Artifact 更新

### `artifact_created`

前端应：

- 更新 `artifacts` 状态
- 给节点加一个“已输出”标记
- 如果是图表产物，可直接展示预览入口

---

## 6. 迁移策略

建议采用“双轨迁移”，不要一次性推翻旧逻辑。

## 6.1 第 1 阶段：新增模型，不删旧结构

后端先新增：

- `PlanGraph`
- `TaskEdge`
- `Artifact`
- `IntentContext`
- `PlanEvent`

但旧 `Task` / `Plan` 先不删

目的：

- 保持旧编译链可用
- 减少一次性改动

## 6.2 第 2 阶段：`/api/plan` 改成输出新结构

这一步建议：

- 后端内部完全使用新结构
- 前端开始改用新结构

如果想更稳，可以临时兼容：

```json
{
  "plan": {
    "nodes": [],
    "edges": [],
    "legacy_tasks": {}
  }
}
```

但不建议长期保留双格式

推荐策略：

- 只在一个短周期内兼容
- 前端迁完就删

## 6.3 第 3 阶段：保留旧 `/api/execute`，新增整图执行接口

建议短期内：

- 旧 `/api/execute` 继续存在
- 新增 `/api/plans/:id/execute`

好处：

- 前端可逐步迁移
- 后端能单独联调整图调度

## 6.4 第 4 阶段：前端切换到计划级执行

前端完成下面几点后，即可切换：

1. 能渲染 `nodes + edges`
2. 能调用 `/api/plans/:id/execute`
3. 能消费 `/api/plans/:id/stream`
4. 能用事件增量更新节点状态

## 6.5 第 5 阶段：删除旧单任务链路

当计划级执行稳定后，可考虑废弃：

- 旧式 `Plan.Tasks` 消费逻辑
- 旧的“点节点单独执行”主流程

但建议保留单节点调试能力作为内部接口，而不是主用户流程

---

## 7. 路由层具体迁移步骤

## 第一步

修改 [backend/internal/api/routes.go](/D:/mygo/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go) 中 `/api/plan`

目标：

- 改为返回 `PlanGraph`
- 创建后写入 `PlanStore`

## 第二步

新增：

- `POST /api/plans/:id/execute`
- `GET /api/plans/:id`
- `GET /api/plans/:id/stream`

## 第三步

旧 `/api/execute` 暂时保留，但标记为：

- legacy

文档上明确：

- 新主流程不再使用它

---

## 8. 调度器上线时的约束

为避免第一版太乱，建议明确这些约束：

1. 一个 `plan` 只能有一个调度实例
2. 同一个 `plan` 不允许重复启动执行
3. 不支持运行中修改 DAG
4. 不支持运行中插入节点
5. 不支持手动重试失败节点

这些能力都可以后续再加，但第一版先不要做

---

## 9. 兼容性建议

## 9.1 字段命名

如果前后端都在你手里，建议统一改成 JSON snake_case：

- `user_intent`
- `intent_type`
- `required_artifacts`
- `output_artifacts`

这样前端处理更稳定

## 9.2 前端兼容旧状态值

如果前端先于后端上线新图结构，建议节点渲染逻辑兼容：

- `pending`
- `ready`
- `in_progress`
- `completed`
- `failed`
- `blocked`

即便后端还没推全，也不会崩

## 9.3 旧 DAG 布局逻辑

前端原本有按 `Dependencies` 推导位置的逻辑。

迁移后建议：

- 继续保留布局函数
- 只是输入从 `Tasks map` 改为 `nodes + edges`

这样视觉部分改动最小

---

## 10. 联调检查表

当前后端与前端联调时，按下面顺序检查：

1. `POST /api/plan` 是否返回 `nodes + edges`
2. 前端是否能正确画出全部节点和边
3. `POST /api/plans/:id/execute` 是否能启动调度
4. `GET /api/plans/:id/stream` 是否持续有事件
5. 事件是否能正确映射到节点颜色
6. 任务完成后后继节点是否自动进入 ready
7. 图结束后 plan 状态是否正确

---

## 11. 推荐的开发验收顺序

建议按以下顺序验收：

### 验收 1：只看图生成

- 不执行
- 只验证 `/api/plan`
- 确认图结构正确

### 验收 2：后端静默执行

- 不接前端 SSE
- 后端先跑通调度器
- 确认状态流转正确

### 验收 3：前端接状态流

- 前端接入 SSE
- 确认节点状态动态变化

### 验收 4：跑完整条链路

- 输入意图
- 生成拓扑图
- 启动整图执行
- 实时更新
- 结束收敛

---

## 12. 文档使用建议

后面如果开始真正改代码，建议默认使用下面这套文档组合：

- 看总体目标：
  - [plan_module_refactor_proposal.md](/D:/mygo/Sea-mult-agent/scholar-agent/docs/plan_module_refactor_proposal.md)

- 看文件落点和实施顺序：
  - [plan_module_implementation_checklist.md](/D:/mygo/Sea-mult-agent/scholar-agent/docs/plan_module_implementation_checklist.md)

- 看结构体、调度、状态机、伪代码：
  - [plan_module_execution_spec.md](/D:/mygo/Sea-mult-agent/scholar-agent/docs/plan_module_execution_spec.md)

- 看接口契约和迁移方案：
  - [plan_module_api_and_migration_spec.md](/D:/mygo/Sea-mult-agent/scholar-agent/docs/plan_module_api_and_migration_spec.md)

---

## 13. 结论

这份文档把“怎么和现有项目平滑对接”补齐了。

到这里为止，关于这次 `Plan` 模块重构，文档已经覆盖了：

- 为什么改
- 改成什么样
- 具体怎么拆文件
- 数据结构怎么定
- 调度器怎么跑
- SSE 怎么发
- 前端怎么接
- 老代码怎么迁

也就是说，现在已经接近“按文档直接重构”的粒度了。
