# AutoResearch 项目介绍证据覆盖检查

| Claim ID | 待表达主张 | 外部证据 | 本地证据 | 状态 |
|---|---|---|---|---|
| C-01 | 项目是受限、指标驱动的自动实验循环 | AR-01 | `autoresearch.go`、`models/autoresearch.go` | 已覆盖 |
| C-02 | 固定预算、小改动和 Keep/Reject 的直接来源 | AR-01 | `03_research_coding_harness.md` | 已覆盖 |
| C-03 | ReAct 只用于局部错误修复 | AR-06 | `coder.go`、Benchmark preflight | 已覆盖 |
| C-04 | ToT 只用于预算内轻量消融设计 | AR-07 | `ablation_tot.go`、`tot_ablation_and_uploads.md` | 已覆盖 |
| C-05 | 专业 Agent 和完整科研链属于方向启发 | AR-02、AR-03、AR-08 | `project_architecture.md`、`research_coding_agent.md` | 已覆盖 |
| C-06 | 分层 Rubric 和真实复现 Artifact 的来源 | AR-04、AR-05 | `claim_evidence_graph.md`、Planner | 已覆盖 |
| C-07 | 可靠性、OOD、效率与人机协作属于扩展评价维度 | AR-09 | `06_research_basis_and_roadmap.md` | 已覆盖，部分为路线图 |
| C-08 | 当前不支持任意仓库零配置优化 | 不需要外部证据 | `ResearchSpec` 校验和用户手册 | 已覆盖 |
| C-09 | 当前不应声称科学结论或 benchmark 优势 | AR-04、AR-05、AR-09 | `04_evaluator_security_and_evidence.md` | 已覆盖 |

审核规则：正式介绍中的来源主张必须能回到本表；涉及当前完成度时，以本地代码、测试和 Artifact 契约为准。
