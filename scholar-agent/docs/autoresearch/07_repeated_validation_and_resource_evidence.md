# 重复验证与执行资源证据

## 1. 为什么补这一层

AutoResearch 搜索得到的最高分可能来自偶然波动、间歇性 evaluator 故障或未完成的运行。只重跑一次可以发现明显错误，但不足以描述稳定性。另一方面，若只展示分数而不记录执行次数和耗时，也无法判断提升付出了多少运行成本。

本模块把搜索和最终验证升级为三项可审计能力：

1. baseline 和每个候选执行 1 至 5 次公开 evaluator，保存原始样本、总体标准差并按 `mean`、`median` 或 `worst` 聚合。
2. 对最佳候选启动 1 至 5 次新的 guard/evaluator 进程序列，并明确区分公开 evaluator 重放与隐藏 holdout。
3. 在搜索账本和验证报告中汇总命令次数、成功/失败数、命令累计耗时和节点墙钟耗时。

默认仍执行 1 次，避免旧任务无提示地增加成本。仓库规格可以声明 `validation_runs`，用户也可以在请求中写“最终验证 3 次”；运行时只会收紧显式规格，不会把更小预算反向增大。生产 DAG 中所有验证轮次共享当前验证节点的 600 秒上限，未完成轮次会使验证失败。

```json
{
  "version": "autoresearch.spec/v1",
  "search_runs": 3,
  "search_aggregation": "worst",
  "validation_runs": 3
}
```

## 2. 借鉴依据

- [MLE-bench](https://arxiv.org/abs/2410.07095)除任务成功率外还研究 Agent 的资源扩展，因此本项目把命令执行量和耗时放入正式 Artifact，而不是只留在临时日志。
- [Microsoft R&D-Agent](https://github.com/microsoft/RD-Agent)公开结果以多次独立 seed 的均值和标准差报告部分 MLE-bench 表现，说明自动研究结果需要分布证据，而不应只展示单次峰值。
- [CORE-Bench 2026 分析](https://arxiv.org/abs/2606.26158)把可靠性和效率列为复现 Agent 的独立评价维度，本模块先落地可在现有 harness 中确定性验证的部分。
- [Stochasticity in Deep Research Agents](https://arxiv.org/abs/2602.23271)显示同类研究 Agent 的独立执行可能有明显方差，支持把重复测量前移到搜索接受规则，而不只在最终结果上补一次复验。

这里是工程借鉴，不是能力等价。每轮不调用候选模型并启动新的命令进程，但仍复用同一个 `prepared_runtime`，不是新建容器。系统也不会自动改写随机种子，因此它能发现进程级漂移和间歇失败，却不能称为多 seed 或跨环境复现。若没有配置 `holdout_command`，它也不能称为独立测试集验证。

## 3. 验证规则

`autoresearch_validate` 在不调用候选模型的情况下执行：

1. 校验 ResearchSpec、TrialLedger 和最佳候选之间的 SHA-256 引用。
2. 校验 protected 文件、非 editable 工作区指纹和最佳 editable 文件哈希。
3. 每轮先恢复最佳候选与冻结快照，再运行全部 guard；随后按 `validation_mode` 重放搜索 evaluator 或运行模型不可见 holdout。
4. 每次命令后重新检查 protected 与非 editable 文件；每轮结束后再检查最佳候选文件。
5. 公开重放使用 `max(1e-9, abs(expected_score) * 1e-6)` 容差比较账本最佳分；隐藏模式使用 baseline、方向和 `holdout_min_delta` 判定。
6. 只有请求的所有验证都完成、命令成功、文件完整且分数匹配，报告才是 `validated`。

普通 evaluator 失败或分数漂移不会立即丢弃后续证据，系统会在预算和上下文允许时继续剩余轮次。文件完整性失败则立即恢复快照并停止，避免在已受污染的工作区继续运行。

报告给出：

```text
mean_score      = 所有成功产生数值的运行均值
stddev          = 总体标准差
failure_rate    = (失败运行 + 未完成运行) / 请求运行数
score_matches   = 每一次请求运行都完成且通过
```

`observed_score` 保留为兼容字段；多次验证时其值等于 `mean_score`。原有 `guard_results` 与 `eval_result` 仍保存第一次运行，完整逐轮证据位于 `runs[]`。

## 4. 资源账本

`research_trial_ledger.resource_usage` 汇总 baseline 和候选搜索；`research_validation_report.resource_usage` 单独汇总最终复验：

| 字段 | 含义 |
|---|---|
| `command_runs` | 实际启动的冻结命令总数 |
| `guard_runs` | guard 命令次数 |
| `evaluator_runs` | evaluator 命令次数 |
| `successful_commands` | exit code 为 0 且无 harness 错误的命令数 |
| `failed_commands` | 其余已启动命令数 |
| `command_duration_ms` | 各命令运行耗时之和 |
| `wall_duration_ms` | 节点从开始到完成的墙钟耗时 |

Ledger 校验器会依据逐轮命令证据重算资源摘要；若摘要被单独篡改，最终验证会拒绝该账本。前端 Trial 面板会显示这组摘要，旧的 `autoresearch.ledger/v1` 没有该可选字段时仍可读取。

这还不是 GPU 成本账本。当前没有记录 GPU 型号、显存峰值、功耗、云费用或模型 token 成本，因此文档和界面只称其为“执行资源证据”。

## 5. 真实轻量运行

2026-08-07 在项目自带意图路由 evaluator 上连续启动 3 个 Python 进程，得到：

| 指标 | 结果 |
|---|---:|
| 完成 / 请求 | 3 / 3 |
| `macro_f1` 均值 | 0.672875816993464 |
| 总体标准差 | 0 |
| 失败率 | 0 |
| 数据 SHA-256 一致 | 是 |
| 候选 SHA-256 一致 | 是 |

机器可读记录见 [`2026-08-07_repeated_validation.json`](../../examples/autoresearch/intent_router/results/2026-08-07_repeated_validation.json)。这是 CPU、固定数据、固定候选的 harness 验收，不是模型训练结果，也不是多 seed 科学实验。

真实进程集成测试还覆盖 `0.25 -> 0.9` 候选保留，并执行 3 轮 `py_compile + evaluator`；另一个负向测试让第二轮验证从 `0.8` 漂移到 `0.79`，最终报告为 `passed=2/3`、`failure_rate=1/3`。

```bash
cd scholar-agent/backend
go test ./internal/agent \
  -run 'TestAutoResearchRunsFrozenCommandsInLocalProcessHarness|TestAutoResearchRepeatedValidationRejectsMetricDrift' \
  -count=1 -v
```

## 6. 下一层升级

重复进程验证完成后，仍应继续补齐：

1. 在 ResearchSpec 中声明 seed 集合，并把 seed 作为 evaluator 的受控参数而不是候选代码输入。
2. 在重复 evaluator 基础上继续支持按预声明 seed 集、均值方差或置信区间接受候选；当前已经不按单次峰值 Keep，但还没有 seed 注入。
3. 记录 GPU 型号、显存、GPU 秒和费用，形成可跨 Trial 比较的成本账本。
4. 将逐轮账本原子持久化，支持后端重启后从最近完整 Trial 恢复。
5. 在冻结 evaluator 前审计 objective 维度的单项与 pairwise 覆盖，避免“每项都通过、组合仍失败”；最终 holdout 不参与补题或候选选择。
