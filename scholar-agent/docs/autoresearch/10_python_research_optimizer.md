# Python Research Optimizer

## 1. 为什么拆成 Python 服务

ScholarAgent 保留 Go 作为控制面：Planner、Scheduler、预算、候选合法性、文件哈希、真实 evaluator、Keep/Reject、回滚和 Holdout 都不交给学习模型。Python 服务只承担更适合研究生态的部分：数据特征、候选优先级和跨任务经验。

```text
Go Experiment Harness
    -> 冻结 Model、参数、Beam 和预算
    -> Go 生成 Top-K Beam + 探索前沿
    -> Python 先用 UCB 选 Model 路线，再用 UCT 选参数路径
    -> Go 校验选择并真实执行
    -> Go 计算指标、Keep/Reject 与 Reward
    -> Python 记录 outcome
    -> Go 完成独立 Holdout
    -> Python 标记 campaign 是否可进入学习历史
```

Python 服务不可用、超时、返回未知候选或非法统计时，Go 使用 `hierarchical-contextual-ucb-uct/v2` 的确定性本地实现。Model 默认配置仍完整运行，实验不会因为学习面故障而失去基本搜索能力。

## 2. 数据特征

`POST /v1/profile` 不接收工作区路径，也不挂载实验目录。Go 先根据冻结的 `ExperimentDatasetManifest` 校验资产，再从规范化数据中抽取有界样本：语料和查询各最多 256 条，正文/查询最多 4096 字符，ID 和数组元素最多 256 字符，单类样本最多 768 KiB。请求总大小超过 2 MiB 时直接退回 manifest 画像。

通用特征来自 manifest 的 counts 与 capabilities。`retrieval.v1` 还会计算：

- 文档、查询数量和平均字符/Token 数；
- 平均相关文档数量与词汇多样性；
- 含关系边文档比例和图密度；
- 是否存在图关系和带标注查询。

输出使用 `experiment.features/v1`，包含 extractor 版本、数据指纹和稳定 context ID，并随 ExperimentSpec 冻结。Go 会重新计算数据指纹并拒绝不匹配的画像，因此 Python 不能把另一批数据的经验错误套到当前任务。

## 3. 候选选择

`POST /v1/select` 只能从 Go 提供的候选数组中返回一个 candidate ID。当前策略是 `hierarchical-contextual-ucb-uct/v2`：

1. `model_defaults` 阶段不学习排序，Go 保证每个 Model 组合的默认配置运行一次。
2. 参数阶段读取同领域、同 Adapter 且 Holdout 已验证的历史，根据数据特征计算上下文相似度。
3. 外层使用路线 Top-K Reward、Contextual prior、UCB 探索项和 virtual visit 惩罚选择 Model 参数树。
4. Go 只向 Policy 提供每条路线 Top-K Beam 父路径和一条低访问探索通道产生的候选。
5. 内层使用父路径的平均 Reward、访问数和 UCT 探索项选择具体参数候选。
6. 四个 Search Agent 依次原子预留候选；尚未完成的候选作为 virtual visit，减少重复路线和重复父路径。
7. 返回路线与节点统计、Beam/探索身份、选择分和 `propensity=1.0`，Go 再校验这些证据与冻结前沿一致。

这是真实可运行的分层 Contextual-UCB 与 UCT-style 搜索，不是 Q-learning、神经网络 Policy 或完整 MCTS。它没有模拟 rollout，所有 `Q` 都来自真实 Validation Reward。

## 4. Reward 与科学指标

Keep/Reject 继续使用原始主指标和 `min_delta`。学习 Reward 使用冻结的 `experiment.reward/v1`：

```text
baseline_scaled_delta - duration_penalty
失败时：-failure_penalty - duration_penalty
```

其中 `baseline_scaled_delta = 方向化指标差 / max(abs(baseline), 1)`。分母下限避免 baseline 接近 0 时把很小的指标变化放大成异常 Reward。

Reward 不会修改科学验收结论。这样可以避免“学习算法预测很好”被误当成 evaluator 真的提升。

## 5. Experience Store

SQLite 保存三张主要表：

| 表 | 内容 |
|---|---|
| `decisions` | context、当时全部可选动作、选中动作、Policy、propensity、预测 Reward |
| `outcomes` | 真实分数、baseline 增量、状态、耗时和实际 Reward |
| `validations` | Holdout 状态与通过轮次 |

Decision 必须先于 Outcome 写入；同一 `campaign_id + trial_number` 的重试必须内容一致，否则返回冲突。策略查询只连接 `validations.status='validated'` 的 campaign，但失败和 Reject outcome 不会删除。

PlanStore 继续负责单次 DAG 的恢复和 Artifact；Experience Store 负责跨任务查询，两者不是同一个存储。

## 6. 启动与检查

Compose 默认启动服务并将经验写入 `scholar-experience-data` 卷：

```bash
docker compose up --build -d
curl http://localhost:8090/health
curl -H "Authorization: Bearer local-optimizer-token" http://localhost:8090/v1/stats
```

本地启动：

```bash
make run-optimizer
make test-optimizer
```

Backend 使用 `RESEARCH_OPTIMIZER_URL` 和 `RESEARCH_OPTIMIZER_API_TOKEN`。URL 留空时不会调用 Python，TrialLedger 会明确记录 Go hierarchical fallback。

## 7. 后续演进

当前 Experience Store 已提供训练所需的 context/action/propensity/reward，但经验规模还不足以宣称训练出通用策略。后续顺序应是：

1. 积累多个数据集和多个领域的已验证 campaign。
2. 按数据集而不是 Trial 切分离线训练与测试。
3. 比较穷举、随机、平铺 UCB、分层 UCB/UCT 和学习排序器的预算效率。
4. 在未见数据集上验证“更少 Trial 找到同等或更好结果”。
5. 只有多阶段轨迹和样本量足够后，再研究离线 RL。
