# 研究依据、项目差异与路线图

## 1. 一手项目与论文

| 工作 | 已公开的核心做法 | 对 ScholarAgent 的启发 |
|---|---|---|
| [karpathy/autoresearch](https://github.com/karpathy/autoresearch) | 单个可编辑训练文件、固定 5 分钟训练预算、单一验证指标，提升则保留否则丢弃 | 把研究循环缩成可比较的候选、固定预算和 Keep/Reject |
| [ReAct](https://arxiv.org/abs/2210.03629) | 交错使用推理、动作和环境观察，根据新结果更新后续行动 | 依赖安装和 Benchmark 预检采用有限的错误观察、结构化修复和重跑，不让修复动作改变科研指标 |
| [Tree of Thoughts](https://arxiv.org/abs/2305.10601) | 探索并评估多个候选推理路径，允许选择、剪枝和回溯 | 轻量消融先扩展多个实验候选，再按信息增益、可复现性、成本和风险做预算选择 |
| [The AI Scientist](https://arxiv.org/abs/2408.06292) | 从想法、代码和实验延伸到画图、论文与自动评审的开放式流程 | 说明实验循环可以接入更长科研链，但自动评审不能等同于科学真值 |
| [Agent Laboratory](https://arxiv.org/abs/2501.04227) | 将流程分为文献、实验和写作阶段，并允许用户在各阶段提供反馈和指导 | 支持专业 Agent 分工和关键阶段人工介入；其论文报告人工参与会改善整体研究质量 |
| [PaperBench](https://arxiv.org/abs/2504.01848) | 用作者参与编写的分层 rubric，把论文复现拆成 8,316 个可评分任务 | 支持 ScholarAgent 先冻结主张 Rubric，再按证据逐项验收，而不只判断代码是否运行 |
| [CORE-Bench](https://arxiv.org/abs/2409.11363) | 以真实论文仓库、依赖安装、结果运行和问题回答评测计算复现 Agent | 支持仓库专用 Research Coding Agent、真实环境执行和可复查输出 |
| [SWE-agent](https://arxiv.org/abs/2405.15793) | 通过面向 Agent 的仓库导航、代码编辑和测试接口执行软件工程任务 | 说明科研仓库 Coding Agent 也需要专门的上下文、动作边界和测试反馈；本项目没有移植其 ACI |
| [CORE-Bench 2026 分析](https://arxiv.org/abs/2606.26158) | 从单一准确率扩展到构念有效性、OOD、效率、可靠性、模型与 scaffold 和人机协作 | 支持继续补齐多 seed、成本账本、OOD 任务和人工检查点，而不是只追求一个最高分 |
| [MLE-bench](https://arxiv.org/abs/2410.07095) | 在真实机器学习工程任务上评测 Agent，并研究不同资源扩展方式 | 除指标外记录命令执行量和耗时，为后续成本归一化保留可审计基础 |
| [Microsoft R&D-Agent](https://github.com/microsoft/RD-Agent) | 将研究提议与开发实现分工，公开评测对部分结果报告多次独立 seed 的均值和标准差 | 最佳候选采用可配置重复验证并输出均值、标准差与失败率；当前不冒充已实现 seed 注入 |
| [Auto-Research-Recipes](https://github.com/cxcscmu/Auto-Research-Recipes) | 以任务无关研究循环配合 Task Adapter、外部 evaluator 和可发布 Artifact | 把每个真实仓库实验沉淀为 spec、公开 evaluator、隐藏 holdout 三文件适配包，核心 harness 不随任务改写 |
| [Arbor](https://github.com/RUC-NLPIR/Arbor) | Coordinator/Executor、想法树、隔离 worktree、开发/heldout 划分和可恢复执行 | 强化隔离工作区与搜索/隐藏验收边界；候选 lineage、并行 worktree 和 checkpoint 仍是路线图 |
| [AI Scientist v2](https://github.com/SakanaAI/AI-Scientist-v2) | 用渐进式 Agent tree search 和实验管理扩展自动科研循环 | 当前先实现有界 TrialLedger、候选反馈和回滚，不把线性循环宣传为树搜索 |
| [Stochasticity in Deep Research Agents](https://arxiv.org/abs/2602.23271) | 实证研究深度研究 Agent 独立执行间的随机性和结果方差 | 搜索 baseline 与每个候选支持重复测量、原始样本、标准差和保守聚合，避免一次尖峰被 Keep |

这些工作提供方向和风险证据。ScholarAgent 当前实现是工程组合与产品化落地，不宣称发明了自动科研、Keep/Reject 或分层 rubric。

完整的来源事实、借鉴类型、本地代码落点和过度声明风险见[项目介绍](00_project_introduction.md)与[证据表](refs/evidence-map.md)。

## 2. 本项目当前差异化

相对 `karpathy/autoresearch` 的单仓库训练脚本，本项目新增的是跨仓库系统契约：

1. `ResearchSpec` 明确固定 commit、editable、protected、命令、指标、方向、改进阈值、目标分数、重复测量和双重预算。
2. Planner 固定生成 8 节点 DAG，并复用仓库发现、上传、沙箱、SSE 和 Artifact。
3. 候选模型只提议完整文件内容；Go harness 掌握写入、执行、接受和回滚权。
4. 除 protected 文件哈希外，还冻结非 editable 源码与关键配置的工作区指纹。
5. TrialLedger 记录每轮真实命令、原始分数样本、标准差、源码哈希、指标、决策和资源摘要；搜索后再执行 1 至 5 次独立进程隐藏验收。
6. 同一项目还包含论文主张 Rubric、Claim-to-Evidence Graph 和自有数据 Benchmark Adapter，可把“优化成功”放回完整复现证据链。

这里的差异化首先是可控性、可观察性和证据完整性，不是“Agent 比现有系统更聪明”的未经验证结论。

## 3. 下一步优先级

| 优先级 | 模块 | 目标 | 验收标准 |
|---|---|---|---|
| P1（已实现） | Trial 可视化 | 指标折线、Keep/Reject、耗时和候选文件哈希摘要放在同一节点视图 | 用户无需打开原始 JSON 即可解释最佳候选来源 |
| P1（已实现） | 重复验证与执行资源证据 | 最佳候选按声明次数复验，输出逐次分数、均值、标准差、失败率与命令资源摘要 | 任一请求轮次失败、漂移或未完成都不能得到 `validated` |
| P1（已实现） | 重复搜索测量 | baseline 与每个候选按声明次数执行，并用 mean、median 或方向相关 worst 聚合 | 原始样本可审计；部分失败必须 Reject 并回滚 |
| P1（已实现） | 精确仓库版本 | 从任务包读取完整 commit SHA，精确 checkout 或下载同 SHA archive | requested 与 actual commit 不一致时禁止冻结实验 |
| P1（已实现） | 目标分数停止 | Keep 候选达到预声明目标后由 harness 停止搜索 | 账本可重算目标，不能依赖模型自报停止 |
| P1 | 人工检查点 | 在冻结 spec、扩大 GPU 预算、导出最佳补丁前支持审批 | 未批准不得改变预算或导出代码 |
| P1 | 多 seed 接受规则 | 声明 seed 集合并由 harness 受控注入，搜索阶段按统计量接受候选 | 接受规则基于预声明均值/方差或置信标准，不以单次峰值 Keep |
| P1 | 逐轮持久化 | Trial 完成后原子保存账本检查点 | 后端重启可从最近完整 Trial 恢复 |
| P2 | 隐藏评测服务 | 候选只能获得分数，不能读取测试样本或评测源码 | 候选工作区不含隐藏数据，评测服务保留独立审计日志 |
| P2 | 假设队列策略 | 在预算内按信息增益、成本和历史失败选择候选 | 与顺序候选 baseline 做固定任务集对照 |
| P2 | 候选 lineage 与恢复 | 保存 parent、假设键、失败分类和逐轮原子 checkpoint | 后端重启可恢复，分支比较不只依赖文本上下文 |
| P2 | 复现胶囊导出 | 导出 spec、镜像摘要、依赖、账本、补丁和验证报告 | 在干净主机上可一键复验相同 Artifact |
| P2 | GPU 调度与成本账本 | 显式记录 GPU 型号、显存、用时和费用 | 每个 Trial 都有可比较的资源证据 |

Trial 可视化的实现位于 [`frontend/src/features/autoresearch/`](../../frontend/src/features/autoresearch/)，当前视觉 fixture 位于 [`frontend/test/runtime-preview.html`](../../frontend/test/runtime-preview.html)，直接回放 LightRAG 真实远端结果并支持工作台、实验侧栏和隐藏验证三种视图。重复验证和资源证据的实现、统计规则与实测记录见 [`07_repeated_validation_and_resource_evidence.md`](07_repeated_validation_and_resource_evidence.md)。逐行代码 diff 需要新的受限 Artifact 契约，未混入本次 P1，以免 TrialLedger 无界增长或保存不必要的完整源码。

## 4. 不应提前宣称的结论

- 单个可见 Benchmark 提升不代表科学结论成立。
- 自动 reviewer 分数不替代作者、领域专家和独立复现者。
- 同一硬件上的固定时长比较不天然具备跨硬件公平性。
- 哈希保护能证明持久文件状态，不能单独消除数据泄漏、运行时投机和容器侧信道。
- 在公开任务集真正比较成功率、成本和 OOD 泛化前，不应声称优于通用 Agent。

因此当前最准确的定位是：一个把论文复现、仓库调试、受限自动实验和证据验收连成可执行闭环的研究原型。
