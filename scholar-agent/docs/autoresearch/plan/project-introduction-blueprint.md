# AutoResearch 项目介绍写作蓝图

## Section 1

- Role: 项目定位与问题背景
- Main claim: ScholarAgent AutoResearch 是面向科研仓库的受限自动实验子系统，目标是让改动可比较、可回滚、可审计和可复验。
- Evidence IDs: AR-01、AR-04、AR-05
- Contrast: 区别于一次性 Coding Agent 和无边界的开放式自动科研。
- Forbidden content: “全自动科学家”“可适配任意仓库”“指标提升等于论文成立”。

## Section 2

- Role: 系统工作流
- Main claim: 固定 DAG 将仓库、规格、运行时、搜索、验证和报告拆开，模型只负责提出候选。
- Evidence IDs: AR-01，辅以本地 Planner、ResearchSpec 和 harness 代码。
- Contrast: 决策权位于确定性 Go harness，而不是模型自由工具调用。
- Forbidden content: 未实现的隐藏评测、逐轮事务恢复和自动推送。

## Section 3

- Role: 直接借鉴的方法
- Main claim: Keep/Reject 来自 autoresearch 式闭环；ReAct 用于局部修复；ToT 用于预算内消融设计。
- Evidence IDs: AR-01、AR-06、AR-07
- Contrast: 三种方法处在不同层，不混成一个“万能推理 Agent”。
- Forbidden content: 把 ToT 写成每轮代码修复策略，把 ReAct 写成无上限自我修复。

## Section 4

- Role: 科研系统与专业 Agent 架构来源
- Main claim: AI Scientist 和 Agent Laboratory 说明科研链可以跨越实验、报告和协作；SWE-agent 说明工具接口和仓库上下文需要专门设计。
- Evidence IDs: AR-02、AR-03、AR-08
- Contrast: ScholarAgent 选择更窄、治理优先的产品边界。
- Forbidden content: 声称复制完整系统或达到原论文效果。

## Section 5

- Role: 可信复现与评价来源
- Main claim: PaperBench、CORE-Bench 和后续 CORE-Bench 分析推动项目从“能运行”扩展到分层证据、专用 scaffold、可靠性和成本。
- Evidence IDs: AR-04、AR-05、AR-09
- Contrast: 已实现 Artifact 与账本，OOD、多 seed 和完整成本评价仍在路线图。
- Forbidden content: 未经公开 benchmark 对照就声称优于通用 Agent。

## Section 6

- Role: 当前完成度与边界
- Main claim: P0/P1 已完成有限循环和可视化，但仍要求显式 ResearchSpec 与可运行 baseline。
- Evidence IDs: 本地代码、测试与文档。
- Contrast: 区分已实现、部分实现和未实现。
- Forbidden content: 使用视觉 fixture 的数值作为真实科研结论。
