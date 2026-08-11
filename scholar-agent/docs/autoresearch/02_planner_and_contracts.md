# Planner 与研究契约

## 1. 意图与规划

生产 API 的规则分类器先识别 `AutoResearch`，`Planner.BuildPlan` 随后直接调用确定性 `buildAutoResearchNodes`。关键节点包括：

| Task type | Agent | 输入 Artifact | 输出 Artifact |
|---|---|---|---|
| `repo_discovery` | Coder/确定性节点 | 用户仓库 URL | `repo_url`、候选和验证报告 |
| `repo_prepare` | Coder/确定性节点 | `repo_url` | `workspace_path`、`repo_manifest` |
| `autoresearch_spec` | Research Coding | 工作区、仓库清单 | `research_spec`、`dependency_spec` |
| `prepare_runtime` | Sandbox | 工作区、依赖 | `runtime_session` |
| `install_dependencies` | Sandbox | 运行时、依赖 | `prepared_runtime`、安装报告 |
| `autoresearch_run` | Research Coding | 工作区、运行时、冻结 spec | TrialLedger、最佳候选、运行报告 |
| `autoresearch_validate` | Research Coding | spec、TrialLedger、最佳候选 | 重复验证报告、统计指标 |
| `verify_result` | Data | 全部研究证据 | `evaluation_report` |

Planner 会把用户表达的实验次数限制到 `1..8`，总时长限制到 `1..60` 分钟，重复验证次数限制到 `1..5`。默认是 3 个候选、15 分钟和 1 次验证；只有用户明确写出“最终验证 3 次”或兼容的“独立复验 3 次”表达时，Planner 才增加验证次数。`autoresearch_run` 节点超时设置为研究时长再加 30 秒；当前验证节点的 1 至 5 轮共享 600 秒节点上限，超时未启动的轮次会计入 `unfinished_runs` 和失败率。计划总预算额外保留仓库、环境、验证和报告时间。

Spec 中显式给出的正数预算只会被 Planner/运行时上限收紧，不会被系统反向增大。`max_wall_seconds` 的硬上限是 3600 秒；过短预算可能使 baseline 超时，但仍由用户决定。

`repo_prepare` 会在 manifest 中记录 `requested_revision`、`repository_commit`、`acquisition_method` 和全部获取尝试。规格提供完整 40/64 位 SHA 时，系统精确 fetch 并 detached checkout；GitHub 网络路径失败时只允许回退到同一 SHA 的 codeload 归档。未提供 SHA 时才解析远端 HEAD。Git cache 命中时不会返回旧工作区，而是从缓存仓库 commit 执行 `--local --no-hardlinks` clone，所以上一任务的未提交候选和 `.scholar/uploads` 不会进入新任务。

`prepare_runtime` 读取工作区根 `pyproject.toml` 的 `[project].requires-python`。如果最低 Python minor 高于 `SANDBOX_DEFAULT_IMAGE`，系统保留原镜像后缀并只向上提升，例如 `python:3.9-bullseye -> python:3.11-bullseye`；显式更高版本不降级。当前不会综合 monorepo 中所有子包、Poetry 约束或 `setup.py` 动态代码，无法解析时继续使用部署默认镜像。

候选生成会把 editable 文件作为 JSON 传给模型，并额外读取公开 eval/guard 命令直接引用的小型源码文件作为只读 JSON 上下文。只接受常见源码扩展名，单文件最多 48 KiB、总计最多 96 KiB；holdout、JSON/CSV 数据集、二进制和越界路径不会进入该上下文。每个候选必须给出 `diagnosis`，把最新失败用例、实际输入、当前调用路径和修改点连接起来，随后随 TrialLedger 保存。坏 JSON 或 schema 错误计作一个拒绝 trial，并在预算内继续；holdout 命令、源码、baseline 和结果始终从候选上下文移除。

## 2. `ResearchSpec`

最小规格：

```json
{
  "version": "autoresearch.spec/v1",
  "name": "intent-router-lightweight",
  "objective": "Improve macro F1 without changing the evaluator.",
  "repository_revision": "0123456789abcdef0123456789abcdef01234567",
  "editable_files": ["candidate.py"],
  "protected_files": ["evaluator.py", "benchmark.json"],
  "eval_command": ["python3", "evaluator.py"],
  "guard_commands": [["python3", "-m", "py_compile", "candidate.py"]],
  "metric_key": "metrics.macro_f1",
  "direction": "maximize",
  "min_delta": 0.001,
  "target_score": 0.9,
  "search_runs": 3,
  "search_aggregation": "worst",
  "max_trials": 3,
  "max_wall_seconds": 300,
  "validation_runs": 3,
  "dependencies": []
}
```

加载后，Go 代码会规范路径、收紧用户预算、校验命令和依赖，再补充：

- `frozen_protected`：保护文件相对路径和 SHA-256。
- `frozen_workspace_sha256`：排除 editable 与生成目录后，其余源码和关键配置的整体指纹。
- `source`：spec 的来源路径或任务输入。
- `source_sha256`：原始 spec 字节哈希。
- `created_at`：冻结时间。

`repository_revision` 可选；提供时必须与 `repo_manifest` 的请求和实际提交完全一致。`target_score` 可选且必须是有限数值，只在一个正常 Keep 的候选达到方向相关目标后终止搜索。`search_runs` 缺省为 1、硬上限为 5；`search_aggregation` 支持 `mean`、`median` 和方向相关的 `worst`。`validation_runs` 缺省为 1，硬上限为 5。任务输入可以收紧规格中更大的值，但不能把规格中显式声明的较小值增大。

最终 `research_spec` Artifact 本身再计算 SHA-256，TrialLedger 和 ValidationReport 都引用这个哈希。

## 3. 规格校验

- 可编辑文件必须是工作区内已有普通文件，不允许符号链接、`.git/` 或 `.scholar/`，最多 8 个、单文件最多 96 KiB。
- 保护文件必须是已有普通文件，最多 64 个、单文件最多 16 MiB；spec 文件本身自动加入保护集。
- 同一路径不能同时属于 editable 和 protected。
- 除 editable 外的 Python、Go、JS/TS、Rust、Java、C/C++、Shell、JSON、YAML、TOML 和主要依赖配置会进入不可变工作区指纹；运行中新增、删除或修改这些文件都会终止研究。
- 命令必须是参数数组，不接受 shell 字符串；当前允许 `python/python3/pytest/go/node/npm/pnpm/yarn/cargo/make`。
- 每个命令最多 64 个参数，拒绝 NUL、换行和超长参数；执行时每个参数单独 shell escape。
- 依赖只接受有限的包名和版本表达式，不接受 URL、pip 选项或 shell 片段。
- `metric_key` 支持 `metrics.macro_f1` 这类点路径；指标必须来自 evaluator stdout 最后一个合法 JSON 行中的有限数值。
- `target_score`、每次搜索样本和聚合值都必须有限；任一声明的搜索重放未完成时不能 Keep 候选。

## 4. 模型与确定性代码的边界

模型只能输出：

```json
{
  "status": "propose",
  "hypothesis": "one falsifiable hypothesis",
  "reason": "why this may improve the metric",
  "patches": [
    {"path": "candidate.py", "content": "complete source", "reason": "file-level reason"}
  ]
}
```

模型不能产生或修改 Plan、ResearchSpec、运行命令、指标解析、接受阈值、文件写入和回滚动作。它也可以返回 `stop` 或 `unsupported`，让循环保留当前最佳状态后正常结束。
