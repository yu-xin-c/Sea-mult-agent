# Docs Index

当前建议优先阅读这些最终版文档：

- [local_startup_guide.md](local_startup_guide.md)：本地启动、环境变量、联调顺序
- [project_architecture.md](project_architecture.md)：当前系统架构、运行链路、数据契约、部署与安全边界
- [project_structure_frontend_backend.md](project_structure_frontend_backend.md)：仓库结构与模块职责
- [backend_planner_models_reference.md](backend_planner_models_reference.md)：后端规划与调度模型说明
- [agent_runtime_p0_p1.md](agent_runtime_p0_p1.md)：P0/P1 可靠性、安全、治理与运维说明
- [tot_ablation_and_uploads.md](tot_ablation_and_uploads.md)：受限 ToT 消融设计、预算选择和文件上传
- [research_coding_agent.md](research_coding_agent.md)：科研 Coding 智能体组件架构、任务路由、论文调试状态机与自有数据 Benchmark 契约
- [claim_evidence_graph.md](claim_evidence_graph.md)：分层论文主张、冻结 Rubric、运行证据绑定、判定边界与前端可视化
- [user_manual.md](user_manual.md)：产品使用说明
- [../examples/](../examples/)：可运行示例、成功条件与已验证结果
- [../test/claim-evidence/](../test/claim-evidence/)：Claim-to-Evidence 真实 harness golden test、稳定图 JSON 和截图

## Interface Preview

![ScholarAgent dashboard](assets/scholar-agent-dashboard.png)

![ScholarAgent node execution panel](assets/scholar-agent-node-panel.png)

阶段性改造记录可参考：

- [plan/2026-04-23_breakpoint_resume_node_snapshot.md](plan/2026-04-23_breakpoint_resume_node_snapshot.md)：断点重启与依赖安装最小修复
- [plan/2026-04-23_dependency_install_react_retry.md](plan/2026-04-23_dependency_install_react_retry.md)：依赖安装从规则修复升级为模型 ReAct 重试

历史方案、重构草案和阶段性报告已归档到：

- [archive/](archive/)
