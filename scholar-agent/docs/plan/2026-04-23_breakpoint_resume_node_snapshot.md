# 2026-04-23 断点重启与依赖安装修复记录

本文记录一次本地联调过程中，为支持“从中间节点重新执行（断点重启）”以及提升依赖安装稳定性所做的最小改动。

## 背景与问题

- 现象 1：执行 `install_dependencies` 时出现 `missing runtime_session input for dependency installation`
  - 触发场景：从 UI 侧“单点执行某节点”（`/api/execute`）从中间开始跑，未回放上游 artifacts（如 `runtime_session`）。
- 现象 2：`pip install` 失败
  - 场景 A：依赖 spec 中混入 Markdown 反引号、尾逗号，或把多段参数写在一个条目里（例如 `--find-links https://...`），导致 `pip` 解析参数失败。
  - 场景 B：依赖版本本身不可安装，例如 `oscar==2.2.1` 在当前索引下不存在该版本。
  - 场景 C：运行期动态导入缺失，例如 `langchain` 在部分代码路径会导入 `langchain_community`，若未安装会 `ModuleNotFoundError`。

## 变更概览

### 1) 断点重启：/api/execute 支持 plan-aware 单节点执行 + 节点快照

- 文件：[routes.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/api/routes.go)
- 目标：让“单节点执行”具备以下能力（仅进程内持久化）：
  - 从 `planStore` 回放该节点 `RequiredArtifacts`（例如 `runtime_session`）作为本次输入
  - 执行完成后把该节点产出的 artifacts 写回 `planStore`，形成“节点快照”，便于后续节点继续跑
- 协议：`POST /api/execute` 新增可选字段
  - `plan_id`：计划 ID
  - `node_id`：计划图中的节点 ID（若为空则回退使用 `task_id`）
  - `task_description`：在 plan-aware 模式下不再强制必填（节点描述来自 plan graph）

示例请求（断点执行某个 plan 节点）：

```json
{
  "plan_id": "plan-xxx",
  "node_id": "task-yyy",
  "assigned_to": "sandbox_agent"
}
```

限制：

- `planStore` 当前是内存实现（`MemoryPlanStore`），因此“节点快照”仅在后端进程不重启时有效。
- sandbox 容器复用依赖 `runtime_session` 是否已在 `plan.Artifacts` 中存在且仍有效；否则会按原逻辑重建。

### 2) sandbox_agent 路由修复：强制走确定性沙箱路径

- 文件：[coder.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)
- 变更：`AssignedTo == "sandbox_agent"` 的任务不再走 Eino 生成链路，统一走 `executeSandboxTask()`：
  - 目的：确保 `prepare_runtime/install_dependencies/execute_code` 这类节点稳定产出 `runtime_session/prepared_runtime` 等关键 artifacts，
    避免“创建临时沙箱但不写入 artifact”的路径导致下游缺参。

### 3) 依赖 spec 清洗：修复 pip 参数拼装与 token 校验

- 文件：[coder.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)
- 变更点：
  - `parseDependencySpec()` 增加统一清洗逻辑：去掉反引号/引号、去掉尾逗号、按空白拆分成 argv token（非 shell）
  - 放宽 `isValidDependencyToken()`：允许 `+` 和 URL 常见字符，避免误拒绝如 `torch==1.11.0+cu113`

### 4) 最小依赖纠错：oscar==2.2.1 -> django-oscar==2.2.1

- 文件：[coder.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)
- 规则：仅对已观测到的失败条目 `oscar==2.2.1` 做替换为 `django-oscar==2.2.1`，避免误伤其它同名包用法。

### 5) 标准库误判修正：补充 shutil

- 文件：[coder.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)
- 说明：将 `shutil` 视为标准库，避免被误判为 PyPI 依赖（`pip` 永远装不上 `shutil`）。

### 6) 最小依赖补齐：langchain -> langchain-community

- 文件：[coder.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)
- 规则：当依赖列表包含 `langchain` 且不包含 `langchain-community` 时，自动追加 `langchain-community`，
  避免运行期触发 `ModuleNotFoundError: No module named 'langchain_community'`。

## 验证方式（本地）

- 断点执行：先生成/保存 plan，然后通过 `POST /api/execute` 携带 `plan_id/node_id` 执行中间节点，检查下游节点是否能读取到 `runtime_session`。
- 依赖安装：观察 `pip install exit_code`，确保不再出现 `--find-links \`https://...\`` 这类参数解析错误；并避免 `oscar==2.2.1` 版本不可用问题。
