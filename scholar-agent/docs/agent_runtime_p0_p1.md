# Agent Runtime P0/P1 功能与运维说明

本文描述 ScholarAgent 在单机部署下的运行时可靠性、安全边界和治理能力。目标是让论文复现与轻量实验具备可恢复、可取消、可审计和可约束的执行语义。

## 能力范围

| 优先级 | 能力 | 当前状态 |
|---|---|---|
| P0 | 计划与事件持久化 | `FilePlanStore` 原子写入 JSON；Compose 默认启用 |
| P0 | 崩溃恢复 | 启动时恢复中断计划，清除旧执行租约并重新排队 |
| P0 | 执行租约 | 每次尝试生成独立 `execution_id` 和递增 epoch |
| P0 | 迟到结果隔离 | 重分配、取消或恢复前的旧结果不会覆盖新状态 |
| P0 | 取消与重试 | 支持计划取消、失败节点重试和 Agent 重分配 |
| P0 | 所有权边界 | 计划绑定 `owner_id`，不同用户 ID 不能读取或操作 |
| P0 | API 与沙箱认证 | 可选静态 Bearer token；沙箱内部 API 单独鉴权 |
| P0 | Web 安全边界 | CORS allowlist、PDF SSRF 防护、响应类型与大小限制 |
| P0 | 沙箱资源限制 | CPU、内存、PID、capability、网络、镜像与挂载根限制 |
| P1 | 运行预算 | 限制总任务尝试次数和计划执行时长 |
| P1 | 人工审批 | 全量复现或配置要求时，必须审批后执行 |
| P1 | 类型化任务契约 | 版本化输入/输出 Artifact 与允许工具集合 |
| P1 | 追踪上下文 | 计划 `trace_id`，任务事件 `span_id`、`execution_id` |
| P1 | 前端治理入口 | 支持“审批并运行”、执行中取消和失败节点重试 |
| P1 | 回归评测 | 固定数据集验证意图路由、DAG 结构与治理元数据 |
| P1 | CI | GitHub Actions 执行 Go、前端与离线示例测试 |

## 运行时状态

计划由 Planner 生成 DAG，Scheduler 只调度依赖已满足的节点。一次任务尝试包含：

```text
task_id + execution_id + execution_epoch + lease_owner + lease_expires_at
```

任务完成或失败时，Scheduler 必须同时匹配 `execution_id`、租约所有者和当前 Agent。旧 worker 在任务重分配后返回的结果会产生 `task_result_discarded` 事件，不会修改节点或 Artifact。

计划终态包括：

- `completed`：全部节点成功完成。
- `failed`：存在不可重试失败或依赖阻塞。
- `canceled`：用户取消、预算耗尽或执行上下文取消。
- `awaiting_approval`：计划尚未获得人工批准。

## 持久化与恢复

设置 `PLAN_STORE_PATH` 后，后端使用单文件状态存储，同时保存计划、节点、Artifact 和事件历史。每次写入先落到权限为 `0600` 的临时文件，`fsync` 后原子替换目标文件。

Compose 默认配置：

```text
PLAN_STORE_PATH=/app/data/plans.json
volume: scholar-plan-data:/app/data
```

服务启动时会扫描 `in_progress` 计划：

1. 计划恢复为 `pending`。
2. 执行中的任务恢复为 `pending`。
3. 清除 `execution_id`、lease owner 和 lease deadline。
4. 已完成任务和 Artifact 保持不变。

`FilePlanStore` 面向可靠的单节点部署。多副本部署仍需把 `PlanStore` 替换为支持事务和分布式锁的共享数据库。

## 治理 API

所有接口位于 `/api`：

| Method | Endpoint | 行为 |
|---|---|---|
| `POST` | `/plans/:id/approve` | 批准待审批计划并记录批准人和时间 |
| `POST` | `/plans/:id/execute` | 启动 `pending` 或 `ready` 计划 |
| `POST` | `/plans/:id/cancel` | 取消计划并使未完成节点进入 `canceled` |
| `POST` | `/plans/:id/tasks/:taskId/retry` | 重置失败、阻塞或取消节点及其下游 |
| `POST` | `/plans/:id/tasks/:taskId/reassign` | 修改 Agent 并使旧执行租约失效 |
| `GET` | `/plans/:id/events` | 获取可回放事件历史 |
| `GET` | `/plans/:id/stream` | 订阅计划 SSE 事件流 |

身份请求使用 `X-User-Id` 和 `X-Session-Id`。它们适用于本地产品会话；公网部署必须由可信认证网关写入并清理外部同名请求头。`API_AUTH_TOKEN` 是单部署静态保护，不等同于 OIDC、RBAC 或完整多租户认证。

## 审批与预算

以下任一条件会要求审批：

- `REQUIRE_PLAN_APPROVAL=true`
- 论文复现模式为 `full`

审批前计划处于 `awaiting_approval`，Scheduler 拒绝执行。前端工具栏会显示“审批并运行”；计划失败后会切换为“重试失败节点”，重置失败节点及其受阻塞下游并重新执行。

预算由计划字段和环境变量共同决定：

```text
PLAN_MAX_TASK_ATTEMPTS=100
PLAN_MAX_DURATION_SECONDS=2100
```

任务尝试计数包含自动重试。达到尝试上限且仍有未完成工作，或超过最长执行时间时，计划被取消并记录原因。

## 沙箱边界

容器创建默认加入：

```text
--cpus 2
--memory 4g
--pids-limit 256
--security-opt no-new-privileges:true
--cap-drop ALL
--network bridge
```

可通过以下变量调整：

| Variable | Default | Purpose |
|---|---|---|
| `SANDBOX_API_TOKEN` | Compose 本地值 | 后端与沙箱间 Bearer token |
| `SANDBOX_CPU_LIMIT` | `2` | 单容器 CPU 上限 |
| `SANDBOX_MEMORY_LIMIT` | `4g` | 单容器内存上限 |
| `SANDBOX_PIDS_LIMIT` | `256` | 单容器进程数上限 |
| `SANDBOX_NETWORK_MODE` | `bridge` | 可设为 `none` 禁止联网 |
| `SANDBOX_WORKSPACE_ROOTS` | 系统临时目录 | 允许挂载的宿主目录根集合 |
| `SANDBOX_IMAGE_ALLOWLIST` | 空 | 逗号分隔的镜像前缀 allowlist |
| `SANDBOX_CONTAINER_USER` | 空 | 可指定非 root UID/GID |
| `SANDBOX_READ_ONLY_ROOT` | `false` | 只读根文件系统并提供受限 `/tmp` |
| `SANDBOX_DOCKER_GPUS` | 空 | 例如 `all`，请求 NVIDIA runtime |

Compose 仅将沙箱端口绑定到 `127.0.0.1:8082`。注意：沙箱服务仍挂载 Docker socket，因此控制该服务通常等价于控制宿主 Docker daemon。面向不可信用户时，应进一步使用独立 worker 主机、rootless runtime、gVisor/Kata 或云端短生命周期执行环境。

## Web 与日志安全

- `CORS_ALLOWED_ORIGINS` 明确列出允许的前端 Origin，未知预检请求返回 `403`。
- PDF 代理只允许 HTTP(S)，拒绝用户信息、回环、私网、链路本地和组播地址，并在实际建连时重新校验解析结果以防 DNS rebinding。
- PDF 重定向逐跳重新校验，最多 5 次；默认最大响应 32 MB，且要求 `application/pdf`。
- `LOG_MODEL_OUTPUT=false` 时仅记录模型输出字符数，不把完整输出写入进程日志。
- `API_AUTH_TOKEN` 设置后，除 `/api/health` 外的 API 要求 Bearer token。

## 事件与任务契约

每个计划包含 `trace_id`。任务事件包含：

- `span_id`：默认与 task ID 对应。
- `execution_id`：区分同一任务的不同尝试。
- `trace_id`：关联同一计划的全部事件。

每个节点包含 `contract.version=v1`，并声明：

- `input_artifacts`
- `output_artifacts`
- `allowed_tools`

这套契约目前由 Planner 生成并随状态持久化，是后续工具授权、schema 校验和 Agent 兼容性检查的基础。

## 验证

```bash
cd scholar-agent

make eval
make test
make lint
make build
docker compose config
```

关键回归覆盖：文件存储重开、崩溃恢复、旧租约结果丢弃、审批和所有权、CORS、SSRF、沙箱 token、镜像 allowlist、挂载根和资源参数。

## 尚未覆盖

- OIDC/OAuth、组织/角色权限、审计日志签名。
- PostgreSQL/Redis 事务状态机与多副本 scheduler leader election。
- 跨机器 Artifact 对象存储和大结果分片。
- 沙箱内核级隔离证明、网络出口 allowlist 和密钥代理。
- OpenTelemetry exporter、成本 token 计量和分布式 trace backend。
- 大规模论文训练结果的可重复性保证。

因此当前推荐定位是：具备 P0/P1 运行时基础的单机研究 Agent 系统，而不是可直接承载不可信公网租户的生产平台。
