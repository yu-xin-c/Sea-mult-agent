# Intent Router AutoResearch Example

这个例子用于验证 AutoResearch harness 本身，不依赖 GPU、网络或第三方 Python 包。

## 文件角色

| 文件 | 角色 | AutoResearch 是否可改 |
|---|---|---|
| `candidate.py` | 六分类意图路由候选 | 是 |
| `evaluator.py` | 指标计算与输出契约 | 否，运行前冻结 SHA-256 |
| `benchmark.json` | 26 条固定评测样本 | 否，运行前冻结 SHA-256 |
| `autoresearch.json` | 文件范围、命令、指标和预算 | 否，加载后纳入保护集 |

## 直接跑基线

在 `scholar-agent/` 目录执行：

```bash
python3 examples/autoresearch/intent_router/evaluator.py
# 或
make example-autoresearch
```

最后一行必须是 JSON，并至少包含：

```json
{"metrics":{"accuracy":0.0,"macro_f1":0.0,"p95_latency_ms":0.0},"status":"ok"}
```

数值以真实执行输出为准。Harness 从最后一个合法 JSON 行读取 `metrics.macro_f1`，不会从自然语言日志猜指标。

## 通过 ScholarAgent 运行

仓库 URL 换成当前 fork 的公开地址：

```text
用 https://github.com/OWNER/Sea-mult-agent 做 AutoResearch，
使用 examples/autoresearch/intent_router/autoresearch.json，
最多 3 次实验，总时长 5 分钟。
```

成功标准：

1. Planner 生成固定的 8 节点 AutoResearch DAG。
2. baseline 先于任何候选运行。
3. 指标至少提升 `0.001` 才 Keep，否则 Reject 并恢复上一个最佳文件。
4. `research_trial_ledger` 保存每次假设、补丁哈希、命令结果、指标和决策。
5. 最终按 `validation_runs=3` 启动三组独立 guard/evaluator 进程；每一组都与账本最佳指标一致后才得到成功的 `research_validation_report`。
6. 搜索与验证 Artifact 分别汇总命令次数、成功/失败数、命令累计耗时和墙钟耗时。

这不是 BERT 或 Qwen 的完整训练实验。它是一个低成本的 harness 验收样例；同一 `autoresearch.spec/v1` 可以把可编辑文件和评测命令换成真实训练脚本，再在 GPU 沙箱中运行。

## 已记录的三次实测

[`results/2026-08-07_repeated_validation.json`](results/2026-08-07_repeated_validation.json) 保存了三次真实 Python 进程输出：`macro_f1` 均为 `0.672875816993464`，总体标准差和失败率为 `0`，数据与候选 SHA-256 一致。该记录只证明当前固定样例可重复，不代表多随机种子模型训练。
