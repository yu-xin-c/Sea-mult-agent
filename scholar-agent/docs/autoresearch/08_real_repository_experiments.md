# 真实外部仓库实验与架构复盘

## 1. 结论

2026-08-10，ScholarAgent 通过部署后的产品 API 和 Docker 沙箱，对 4 个提交可追踪的真实 GitHub 仓库完成受限 AutoResearch。四组实验都包含公开搜索 evaluator、模型不可见 holdout、3 次搜索重放和 3 次最终隐藏验证。结果文件保存在 [`examples/autoresearch/real_repositories/results`](../../examples/autoresearch/real_repositories/results/)，不是手工补写的演示 JSON。前四份记录保存了实际 checkout SHA；随后任务包改为显式请求这些 SHA，并用两次 LightRAG 对照验证了请求版本与实际版本一致。

这批实验支持一个朴素结论：当前系统已经能在白名单文件和固定预算内完成“读真实仓库、提议代码、真实执行、Keep/Reject、回滚、隐藏验收、产出证据”的闭环。它仍然只是模块级实验，不能外推为整个上游项目的质量 Benchmark。

## 2. 四组实验

所有搜索分数都采用 `3 x worst`：最大化指标取三次观测中的最小值。最终 holdout 另起 3 个新进程执行。

| 仓库与模块 | 记录提交，当前任务包已锁定 | 公开搜索 | 隐藏 holdout | 搜索轮次 | 结论 |
|---|---|---:|---:|---:|---|
| rank-bm25 `rank_bm25.py` | `47aa3ddf8dc1ebeb7ef4e65f2b4536af44594099` | `5/9 -> 9/9` | `1/4 -> 4/4`，3/3 通过 | 1 Keep | 通过 |
| Tenacity `tenacity/wait.py` | `26f719dc73d3c5612b9c1b8d18a7883837790ad8` | `6/7 -> 7/7` | `0/4 -> 4/4`，3/3 通过 | 1 Keep | 通过 |
| LightRAG `lightrag/rerank.py` | `24ee484864357865b20770e478b177ae68391796` | `4/8 -> 8/8` | `1/4 -> 4/4`，3/3 通过 | 1 Keep | 通过 |
| GraphRAG `graphrag_common/hasher/hasher.py` | `14a00ad88fc33cf2b52f4f113f25807556f8e25e` | `6/12 -> 12/12` | `1/4 -> 4/4`，3/3 通过 | 8 次，4 Keep | 通过 |

对应 Plan ID：

- rank-bm25：`db4445b8-3507-4568-a6c6-a5f80855dd5b`
- Tenacity：`f0ab4b40-9d90-4f02-941c-455d1aee3de7`
- LightRAG 最终对照：`181cd848-0f28-496c-b63e-5041b68879a2`
- GraphRAG V2：`1a3ba23b-d5a7-4cf9-bbbe-18c748f08439`

rank-bm25 还真实触发了环境修复：`numpy==2.1.3` 在 Python 3.9 安装失败，ReAct 修复流程切换到 Python 3.10 后完成安装和实验。这证明实验没有绕过产品的环境准备节点。

## 3. 产品执行链

```text
上传 spec + evaluator + holdout
  -> POST /api/plan
  -> Repository Prepare：精确 commit + 独立工作区
  -> Runtime Detect：Python / Docker / 资源探测
  -> Dependency Install：有限 ReAct 修复
  -> Spec Freeze：范围、命令、指标、重复次数、目标和预算
  -> Search：Coding Agent 提议，Harness 执行和决定 Keep/Reject
  -> Hidden Validation：模型不可见 holdout 重复执行
  -> TrialLedger + ValidationReport + Data Agent 报告
```

候选模型不能直接决定写入、接受或回滚，也看不到 holdout 命令、源码、基线和逐轮结果。Go harness 是 evaluator 和状态转移的所有者。

## 4. 实验推动的三项架构修复

### 4.1 单次分数不可靠

问题：原循环只执行一次公开 evaluator。随机数据顺序、外部服务和数值波动可能把一次尖峰误当成改进。

修复：`ResearchSpec` 增加 `search_runs` 和 `search_aggregation`。baseline 与每个候选都完整执行声明次数，账本保存原始样本、总体标准差和每次命令。任一重复执行失败时整轮候选 Reject 并回滚。`worst` 按指标方向取最保守值。

依据：[Stochasticity in Deep Research Agents](https://arxiv.org/abs/2602.23271)指出深度研究 Agent 的独立运行存在明显方差，因此可靠性判断需要重复执行与聚合，而不是单次结果。

### 4.2 仓库 HEAD 会漂移

问题：首次四组实验都记录了实际 commit，但仓库准备仍跟随远端默认分支，用户无法预先要求复现同一版本。

修复：任务包增加 `repository_revision`。Repository Prepare 对完整 SHA 执行精确 fetch 和 detached checkout，失败时回退到同一 SHA 的 GitHub codeload archive；缓存只有在 commit 完全相同时才能复用。`repo_manifest` 同时记录 `requested_revision`、`repository_commit` 和 `acquisition_method`，规格冻结会再次检查两者一致。

实测：LightRAG 请求和实际提交均为 `24ee484864357865b20770e478b177ae68391796`，本地不可变缓存复用后仍保持相同 SHA。

### 4.3 满分平台浪费试验预算

问题：固定版本的 LightRAG 在第 1 个候选已经达到公开 `1.0`，旧循环仍依赖模型自己决定停止，最终执行了 4 个候选，其中 3 个是满分平台上的零增益 Reject。

修复：规格增加可选 `target_score`。只有候选先满足正常改进规则并被 Keep，且聚合分数达到方向相关目标时，harness 才写入 `stop_reason=target_score_reached` 并结束搜索。账本校验器会重算目标、最佳分数和最后一次决策，不能只改字符串伪造停止原因。

真实 A/B：

| LightRAG 条件 | 最佳分数 | 候选轮次 | Keep | 停止原因 | 隐藏验证 |
|---|---:|---:|---:|---|---:|
| 精确 commit，无确定性目标停止 | `1.0` | 4 | 1 | 模型主动 Stop | `3/3` |
| 同 commit，同 evaluator，`target_score=1.0` | `1.0` | 1 | 1 | `target_score_reached` | `3/3` |

后者搜索账本为 9 次命令、7 次 evaluator，墙钟 32.872 秒；最终隐藏验收另有 6 次命令，三轮分数均为 `1.0`。

## 5. 旧 GraphRAG 失败没有被删掉

早期 V6 的公开 evaluator 为 `11/11`，隐藏 holdout 只有 `3/4`。失败用例把混合键和递归 mapping 组合起来，候选在 canonical serialization 中触发 `RecursionError`。系统正确输出 `status=failed`，说明“计划节点全部完成”不等于“候选通过验收”。

V2 没有复用这道已花费的 holdout。它把已知递归组合缺口移入公开 evaluator，再设计新的未见 holdout，最终公开 `12/12`、新隐藏 `4/4`。这样做保留了评测诚实性。

| 旧轮次 | 暴露的问题 | 系统更新 |
|---|---|---|
| V1 | 有公开失败时模型提前 Stop | 可见 case 未清零时拒绝 Stop |
| V2 | 下一轮看不到被拒候选和精确错误 | 回传被拒源码与命令证据 |
| V3 | 公开单项覆盖不能代表组合泛化 | 保留隐藏组合测试，最终失败不伪装成功 |
| V4 | 一次坏 JSON 终止整个任务 | 协议错误成为有界 Reject，预算继续 |
| V5 | 账本的决策枚举与运行器不一致 | 统一 `keep/reject` 语义并补校验回归 |
| V6 | 公开满分，隐藏仍为 `3/4` | 保存可信负结果；V2 使用新公开契约和新 holdout |

## 6. 从优秀项目中吸收了什么

这轮没有复制外部项目源码，而是把可验证的设计原则落到现有 harness：

| 来源 | 设计 | 当前落地 | 尚未落地 |
|---|---|---|---|
| [Auto-Research-Recipes](https://github.com/cxcscmu/Auto-Research-Recipes) | 任务无关核心、Task Adapter、外部 evaluator、Artifact/lineage | 三文件任务包；Go harness 持有 evaluator；完整 plan artifact | 分支候选的显式父子 lineage |
| [Arbor](https://github.com/RUC-NLPIR/Arbor) | Coordinator/Executor、想法树、隔离工作树、开发集与 heldout 分离 | 单计划隔离工作区；公开搜索与隐藏验收分离 | 并行 worktree、树搜索和断点恢复 |
| [AI Scientist v2](https://github.com/SakanaAI/AI-Scientist-v2) | 渐进式 Agent tree search 和实验管理 | 有界 trial ledger、候选反馈和回滚 | 多分支渐进搜索与研究写作闭环 |
| [RE-Bench](https://arxiv.org/abs/2411.15114) | 在固定时间预算下比较机器学习研究 Agent | `max_wall_seconds` 和命令资源证据 | 标准 RE-Bench 任务集对照 |

借鉴边界很重要：ScholarAgent 当前仍是线性候选循环，不应写成已经拥有 Arbor 或 AI Scientist v2 的树搜索。

## 7. 当前仍不合理的地方

1. **依赖安装重复且慢**：rank-bm25 的 Python 切换有效，但每个隔离容器重新下载依赖会吞掉大量墙钟。下一步应做按镜像、锁文件和包哈希寻址的只读 wheelhouse。
2. **大仓库获取延迟高**：LightRAG 的浅克隆曾两次超时后回退 archive。现在有精确 SHA 和缓存，但首次获取仍可能较慢，应记录下载字节、命中率和阶段耗时。
3. **线性搜索缺少候选 lineage**：GraphRAG 用满 8 次，失败知识只能进入文本反馈。应为每个候选记录 parent、hypothesis key 和失败类别，再做预算内分支选择。
4. **隐藏评测不是零信任服务**：当前靠模型上下文隐藏、受保护文件哈希和策略门禁。高价值 Benchmark 应把 holdout 放到独立 evaluator 服务。
5. **没有模型成本账本**：已有命令次数和耗时，仍缺 token、费用、GPU 秒和依赖缓存命中率。

## 8. 证据边界

- 这些任务验证专用 Research Coding Agent harness，不证明 rank-bm25、Tenacity、LightRAG 或 GraphRAG 的完整产品质量。
- evaluator 与 holdout 是本项目为边界行为编写的冻结契约，不是上游官方 Benchmark。
- `hidden_holdout` 表示候选模型不可见，不表示密码学隔离或第三方独立测评。
- 四个任务全通过不能支持“优于通用 Agent”的结论；还需要固定任务集、通用 Agent baseline、成本和 OOD 对照。
