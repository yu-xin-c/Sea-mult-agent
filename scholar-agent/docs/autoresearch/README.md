# AutoResearch 文档索引

AutoResearch 是 ScholarAgent 面向“持续提出小改动并用真实指标筛选”的受限研究循环。它不是一个可以任意改仓库、任意重写评测器的通用 Coding Agent，而是由冻结契约驱动的实验执行能力。

第一次了解项目，建议先阅读[项目介绍](00_project_introduction.md)，其中集中说明了系统定位、核心能力、架构，以及与 `karpathy/autoresearch`、ReAct、Tree of Thoughts、The AI Scientist、Agent Laboratory、PaperBench、CORE-Bench 和 SWE-agent 的借鉴关系。

![ScholarAgent AutoResearch 架构](../assets/autoresearch-architecture.png)

按模块阅读：

| 文档 | 模块 | 重点 |
|---|---|---|
| [00_project_introduction.md](00_project_introduction.md) | 项目总览 | 定位、架构、核心能力、研究来源、差异化和当前边界 |
| [01_product_workflow.md](01_product_workflow.md) | 产品与流程 | 使用场景、8 节点 DAG、项目创新点和当前状态 |
| [02_planner_and_contracts.md](02_planner_and_contracts.md) | Planner 与数据契约 | 意图路由、`ResearchSpec`、Artifact 和预算传递 |
| [03_research_coding_harness.md](03_research_coding_harness.md) | Research Coding Agent | baseline、候选生成、Keep/Reject、回滚和终止条件 |
| [04_evaluator_security_and_evidence.md](04_evaluator_security_and_evidence.md) | 沙箱、安全与证据 | 冻结评测器、路径白名单、哈希、独立复验和可信边界 |
| [05_example_and_acceptance.md](05_example_and_acceptance.md) | 示例与验收 | 意图路由示例、真实基线、测试命令和成功标准 |
| [06_research_basis_and_roadmap.md](06_research_basis_and_roadmap.md) | 研究依据与路线图 | 一手项目/论文、借鉴关系、项目差异化和后续优先级 |
| [07_repeated_validation_and_resource_evidence.md](07_repeated_validation_and_resource_evidence.md) | 重复验证与执行资源证据 | 多次独立进程复验、统计量、资源摘要、真实运行和边界 |

研究来源的逐项证据、本地落点和风险标记见 [`refs/evidence-map.md`](refs/evidence-map.md)。

主要代码：

- [`models/autoresearch.go`](../../backend/internal/models/autoresearch.go)
- [`agent/autoresearch.go`](../../backend/internal/agent/autoresearch.go)
- [`planner/planner.go`](../../backend/internal/planner/planner.go)
- [`examples/autoresearch/intent_router`](../../examples/autoresearch/intent_router/)

当前版本：`autoresearch.spec/v1`、`autoresearch.ledger/v1`、`autoresearch.validation/v1`。
