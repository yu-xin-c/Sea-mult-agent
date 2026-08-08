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

Planner 会把用户表达的实验次数限制到 `1..8`，总时长限制到 `1..60` 分钟，重复验证次数限制到 `1..5`。默认是 3 个候选、15 分钟和 1 次验证；只有用户明确写出“独立复验 3 次”一类表达时，Planner 才增加验证次数。`autoresearch_run` 节点超时设置为研究时长再加 30 秒；当前验证节点的 1 至 5 轮共享 600 秒节点上限，超时未启动的轮次会计入 `unfinished_runs` 和失败率。计划总预算额外保留仓库、环境、验证和报告时间。

Spec 中显式给出的正数预算只会被 Planner/运行时上限收紧，不会被系统反向增大。`max_wall_seconds` 的硬上限是 3600 秒；过短预算可能使 baseline 超时，但仍由用户决定。

## 2. `ResearchSpec`

最小规格：

```json
{
  "version": "autoresearch.spec/v1",
  "name": "intent-router-lightweight",
  "objective": "Improve macro F1 without changing the evaluator.",
  "editable_files": ["candidate.py"],
  "protected_files": ["evaluator.py", "benchmark.json"],
  "eval_command": ["python3", "evaluator.py"],
  "guard_commands": [["python3", "-m", "py_compile", "candidate.py"]],
  "metric_key": "metrics.macro_f1",
  "direction": "maximize",
  "min_delta": 0.001,
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

`validation_runs` 缺省为 1，硬上限为 5。任务输入可以收紧规格中更大的值，但不能把规格中显式声明的较小值增大。旧的冻结 `autoresearch.spec/v1` 没有该字段时仍按 1 次处理。

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
