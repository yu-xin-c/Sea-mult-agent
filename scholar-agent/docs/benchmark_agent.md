# Benchmark Agent

Benchmark Agent 负责先回答“怎样才算做得好”，再允许 Research Coding Agent 适配仓库。它不生成论文算法代码，也不根据模型自报分数下结论。

## 1. 为什么独立出来

旧流程能够读取用户数据、生成仓库 Adapter，并根据逐样本预测重算分类或回归指标，但存在三个边界：

1. validation 与 test 没有独立物理划分；
2. 指标和 Reward 没有独立、版本化的冻结契约；
3. Adapter 能同时看到样本和标签，不能作为严格隐藏验收。

现在职责拆成：

```text
Benchmark Agent
  定义任务、切分数据、冻结指标、隔离标签、重算隐藏指标

Research Coding Agent
  阅读仓库、生成 Adapter、预检修复、在公开/无标签数据上执行推理

Go Harness
  校验哈希、文件边界、预测 ID、主指标、目标阈值和 Artifact
```

## 2. Custom Benchmark DAG

上传 `CSV`、`TSV`、`JSON` 或 `JSONL` 后，Planner 生成固定 13 节点 DAG：

```text
数据审计 --------------------------+
仓库发现 -> 工作区准备 ------------+-> 安全切分
                                        -> 冻结 Metric / Reward / Evaluator
                                        -> 生成仓库 Adapter
                                        -> 依赖与沙箱
	                                        -> 8 条有标签预检 + 无标签推理预检
                                        -> validation 正式运行
                                        -> 无标签 test 推理
                                        -> 后端隐藏验收
                                        -> 报告
```

前三个 Benchmark 节点和最终验收都由 `benchmark_agent` 执行；仓库 Adapter 仍由 `research_coding_agent` 负责。

## 3. 数据审计

`benchmark_dataset_audit` 输出：

- 原始文件 SHA-256、行数、格式和列类型；
- 输入列、目标列、可选 group/time 列；
- `classification / regression / generation / inference / retrieval` 任务类型；
- 映射置信度、缺失计数、类别计数和阻断问题。

用户显式指定的任务类型和列优先。无法可靠判断时产生阻断问题，后续切分不会静默猜测。

## 4. 划分与泄漏检查

默认比例为 `70% / 15% / 15%`，默认 seed 为 `17`：

| 条件 | 划分方法 |
|---|---|
| 分类 | `stratified_hash` |
| 回归 | `quantile_stratified_hash` |
| 指定 group 列 | `group_hash` |
| 指定 time 列 | `chronological` |
| 其他任务 | `deterministic_hash` |

同一归一化输入默认作为一个不可拆分单元。系统检查冲突标签、输入跨 split、group 跨 split、时间倒置，并执行最多 10,000 对的 token-Jaccard 近重复扫描。

物理产物：

```text
.scholar/benchmark/dataset/train.jsonl
.scholar/benchmark/dataset/validation.jsonl
.scholar/benchmark/dataset/preflight_features.jsonl
.scholar/benchmark/dataset/test_features.jsonl
```

`preflight_features.jsonl` 是从 validation 固定抽取的最多 8 条无标签样本，用来在有限修复循环内提前发现 Adapter 偷读目标列或不会纯推理的问题。`test_features.jsonl` 同样删除目标列，只保留稳定的 `__benchmark_id`；只有前者参与修复，正式 test 不参与修复。隐藏标签写入 Backend 的 `BENCHMARK_PRIVATE_ROOT`，不在仓库工作区和实验容器挂载目录中；代码会拒绝该目录与 `SANDBOX_WORKSPACE_ROOTS` 互相包含，Compose 则使用 Backend 独占的 `scholar-benchmark-private-data` 命名卷。公开 Artifact 只保存 opaque state ID 和文件哈希，不保存私有绝对路径。

## 5. Metric Contract

系统按照任务类型提供可重算的默认主指标：

| 任务 | 主指标 | 方向 | 次指标 |
|---|---|---|---|
| 分类 | `macro_f1` | maximize | `accuracy` |
| 回归 | `mae` | minimize | `rmse` |
| 生成 | `exact_match` | maximize | `token_f1` |
| 检索 | `ndcg_at_k` | maximize | 由领域 Adapter 扩展 |
| 推理性能 | `p95_ms` | minimize | `throughput` |

`benchmark.metric-contract/v1` 同时冻结 `min_delta`、可选 `target_score`、聚合方式、验证次数和 evaluator 版本。显式指标必须属于该任务的允许集合。

## 6. Reward Contract

Reward 与科学验收分开：

```text
maximize: delta = candidate - baseline
minimize: delta = baseline - candidate

reward = delta / max(abs(baseline), 1)
         - duration_penalty_per_second * duration

failed reward = -failure_penalty - duration penalty
```

`benchmark.reward-contract/v1` 的 `usage` 固定为 `candidate_priority_only`：

- 主指标和 `min_delta` 决定 Keep/Reject；
- 隐藏主指标和目标阈值决定最终是否通过；
- Reward 只用于学习下一次优先实验哪个候选。

因此不能用“运行更便宜但主指标退化”的高性价比候选替代科学最佳候选。

## 7. 隐藏验收

Research Coding Agent 在 `test_features` 上只能输出：

```json
{"id":"frozen-example-id","prediction":"positive"}
```

Benchmark Agent 随后执行：

1. 重验 split、公开 evaluator、隐藏标签和隐藏 evaluator 的 SHA-256；
2. 检查 hidden run manifest 与冻结 test hash/行数一致；
3. 要求每个预测 ID 恰好出现一次，并覆盖全部隐藏 ID；
4. 在 Backend 内重新计算指标；
5. 使用冻结方向和 `target_score` 产生 `validated` 或 `not_validated`。

Adapter 写入的隐藏 metrics 不参与最终判定。

## 8. Artifact

| Artifact | 内容 |
|---|---|
| `benchmark_dataset_audit` | 任务、列映射、缺失和阻断问题 |
| `benchmark_split_manifest` | 方法、seed、比例、各 split 行数/分布/哈希 |
| `benchmark_leakage_report` | 重复、冲突、跨 split 和近重复检查 |
| `benchmark_input_only_preflight_manifest` | 最多 8 条 validation 派生的无标签预检样本及哈希 |
| `benchmark_metric_contract` | 主指标、方向、阈值和 evaluator 版本 |
| `benchmark_reward_contract` | 质量归一化、时间惩罚、失败惩罚和用途 |
| `benchmark_contract` | 数据、split、metric、reward 和 evaluator 的总冻结契约 |
| `benchmark_hidden_predictions_path` | 无标签 test 的逐样本预测 |
| `benchmark_validation_report` | 后端重算的隐藏指标和完整性检查 |

## 9. 当前边界

- 分类、回归和简单文本生成具有内置后端指标重算。
- 检索/RAG 的可信评测优先使用 `retrieval.v1` Domain Adapter；复杂论文任务需要 Portable Adapter 提供领域 evaluator。
- 推理性能指标目前仍由受限 Adapter 报告有限数值，尚未由独立计时 sidecar 复测。
- 近重复检查是有上限的 token-Jaccard 扫描，不替代图像、音频或领域语义去重。
- 当前私有标签隔离建立在 Backend 与 Docker 工作区不共享 `BENCHMARK_PRIVATE_ROOT` 的部署边界上；生产部署仍应使用独立对象存储与最小权限凭证。

## 10. 验收示例

示例位于 [`examples/benchmark-agent/classification`](../examples/benchmark-agent/classification/)：

```bash
cd scholar-agent/backend
go test ./internal/agent -run TestBenchmarkAgentBuildsLeakageSafeContractAndRecomputesHiddenMetrics -v
```

该测试真实生成三份公开 split、私有标签、Metric/Reward Contract 和 evaluator，并用隐藏预测完成后端指标重算。
