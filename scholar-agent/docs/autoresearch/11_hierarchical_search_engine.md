# 分层候选搜索引擎

## 1. 目标

配置型 Scientific AutoResearch 面对两个不同问题：

1. 哪个 Model 或模块组合值得继续投入实验预算。
2. 在一条 Model 路线内部，下一组参数应该从哪条路径产生。

ScholarAgent 不把所有 Model 和参数笛卡尔积平铺成独立手臂。当前离散搜索采用两层结构：外层 UCB 分配路线预算，内层使用 Top-K Beam 和一条探索通道限制前沿，再用 UCT-style 分数选择具体父路径。

## 2. 执行顺序

```text
Model 组合默认配置受限穷举
-> 等待全部默认配置完成
-> 为每条 Model 路线建立参数树
-> 外层 UCB 选择路线
-> 路线内 Top-K Beam + 1 条探索通道
-> 内层 UCT-style 选择父路径
-> 4 个 Search Agent 异步执行
-> Validation 生成真实 Score 和 Reward
-> 更新调度统计与路线 Top-K
-> 冻结全局最佳候选
-> 隐藏 Holdout 验收
```

第一阶段存在硬屏障。假设策略空间包含 `A+B`、`A+C` 和 `A+B+C`，三个默认配置必须各运行一次。快路线完成后不能提前占用参数搜索预算；只有所有默认配置完成，参数候选才有资格进入调度前沿。

## 3. 外层 UCB

每个 Model 组合对应一棵参数树。外层选择分数由该路线的真实历史计算：

```text
RouteScore = TopKMeanReward
           + ExplorationBonus
           - VirtualVisitPenalty
```

探索项使用 UCB 形式：

\[
UCB_i = \bar{R}_i + c\sqrt{\frac{\ln N}{n_i}}
\]

- `TopKMeanReward`：该路线当前最佳 K 个候选的 Reward 均值。
- `N`：所有路线已完成和已预留的访问总数。
- `n_i`：当前路线访问数。
- `VirtualVisitPenalty`：正在运行但尚未返回的候选预留。

Python Optimizer 还会把同领域、同 Adapter、Holdout 已验证的相似数据集经验作为 Contextual Bandit 先验。每条路线的跨任务先验最多折算为 `0.75` 个伪样本，低于本次 campaign 已完成的一个真实默认配置，因此历史经验不能覆盖本次 Validation。

路线默认配置分数和每个参数候选分数始终独立保存。外层统计不会覆盖根节点的真实分数。

## 4. Top-K Beam 与探索通道

每条路线按主指标保留 K 个高分父路径作为主要扩展集合，默认 `K=3`。所有已执行候选仍保留在 TrialLedger 和路线榜单中，Beam 只决定哪些父路径可以继续产生子候选。

纯 Beam 容易过早放弃早期分数一般的路径，因此每条路线额外保留一条探索通道：

```text
ActiveFrontier(route) = TopKParents + LowestVisitExplorationParent
```

当前默认值：

| 参数 | 默认值 | 范围 |
|---|---:|---:|
| `beam_width` | 3 | 1-8 |
| `exploration_slots` | 1 | 1-4 |
| `max_parallel_trials` | 4 | 1-4 |

可移植 evaluator 若会写共享状态，必须使用 `serial/v1`；只有声明 `shared-readonly/v1` 的 evaluator 才能使用多个 Search Agent。

## 5. 内层 UCT-style 搜索

在外层选中的 Model 路线内，候选根据父路径统计计算：

\[
UCT(child) = Q(parent) + c\sqrt{\frac{\ln N(route)}{N(parent)}}
\]

每次实验完成后，真实 Validation Reward 沿 `backprop_path` 更新该候选、父路径和路线根的 `visit_count` 与 `mean_reward`。这些字段仅服务调度，不修改任何候选的真实指标。

当前实现没有模型模拟、随机 rollout 或价值网络，因此准确名称是 **UCT-style 参数树搜索**，不是完整 MCTS。

## 6. 四 Agent 异步执行

Go Harness 是候选队列和 Ledger 的唯一写入者：

1. Coordinator 原子选择候选并从队列移除。
2. 为候选分配稳定 ID，例如 `search-agent-01`。
3. 立即登记 virtual visit，下一次选择会降低相同路线和父路径优先级。
4. evaluator 在独立候选配置文件上并行运行。
5. 哪个结果先完成，哪个先进入中央事件循环。
6. Coordinator 单线程更新最佳候选、队列、Reward、UCB/UCT 统计和 TrialLedger。

账本同时记录 `dispatch_order` 和 `completion_order`，因此可以复核异步调度，而不把完成时序伪装成固定批次。

## 7. Reward、排行榜和 Holdout

Reward 默认采用：

```text
directional_metric_delta / max(abs(baseline), 1)
- duration_penalty
```

失败候选使用固定失败惩罚。Reward 只控制搜索优先级，Keep/Reject 和最终接受仍由冻结主指标决定。

每条路线生成独立 `route_summary`：

- 默认配置真实分数。
- 路线实验数。
- 路线最佳分数。
- Top-K Reward 均值。
- Top-K 候选的参数、Score、Reward 和耗时。

前端先展示各路线 Top-K，再标出全局最佳。搜索结束后只冻结一个全局最佳 `Model + parameters`，隐藏 Holdout 不参与 UCB、UCT、Beam 或候选排序。

## 8. 其他参数类型

算法按搜索对象路由，而不是让 UCT 处理所有参数：

| 搜索对象 | 算法 | 当前接入状态 |
|---|---|---|
| 全部合法 Model 组合的默认配置 | 受限穷举 | 已实现 |
| 哪个 Model 值得继续调参 | UCB / Contextual Bandit | 已实现 |
| 离散、条件化参数路径 | Top-K Beam + UCT-style | 已实现 |
| 连续参数 | 贝叶斯优化 | 需要连续域提议器，当前离散协议不启用 |
| epoch、数据量、训练步数 | Hyperband | 需要 fidelity 与 checkpoint 契约，当前协议不启用 |
| 最终可信结果 | 隐藏 Holdout | 已实现 |

贝叶斯优化不能通过把连续区间随意离散化来冒充，Hyperband 也不能在 evaluator 不支持分档预算和 checkpoint 时启用。后续协议需要分别声明连续上下界、尺度、代理模型版本，以及 fidelity 参数、预算档位、恢复命令和 checkpoint 哈希。

## 9. 审计字段

`ExperimentPolicyDecision` 保存：

- `phase`、`route`、`frontier_kind` 和 `beam_rank`。
- 路线访问数、平均 Reward、Top-K Reward、最佳 Reward 和 UCB 探索项。
- 节点访问数、节点平均 Reward 和 UCT 探索项。
- virtual visits、最终选择分和 Policy 版本。

`ExperimentTrial` 另外保存 Search Agent、派发顺序、完成顺序和 `backprop_path`。`ExperimentTrialLedger` 保存 Beam 参数、Agent 槽位、峰值并发和各路线 Top-K。前端可以直接解释每次选择，不依赖模型私有思维过程。

## 10. 实现与测试

- Go 调度与账本：[`experiment_research.go`](../../backend/internal/agent/experiment_research.go)
- Go UCB/UCT fallback 与 Beam 前沿：[`research_optimizer_client.go`](../../backend/internal/agent/research_optimizer_client.go)
- Python Contextual UCB/UCT：[`policy.py`](../../research-optimizer/research_optimizer/policy.py)
- 前端全局榜单与候选详情：[`ExperimentResearchView.tsx`](../../frontend/src/features/experiment/ExperimentResearchView.tsx)
- Go 集成测试：[`experiment_research_test.go`](../../backend/internal/agent/experiment_research_test.go)
- Python Policy 测试：[`test_optimizer.py`](../../research-optimizer/tests/test_optimizer.py)
- 前端稳定回放账本：[`scientific-autoresearch-ledger.json`](../../frontend/test/fixtures/scientific-autoresearch-ledger.json)
