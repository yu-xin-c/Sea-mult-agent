# Docs Index

当前建议优先阅读这些最终版文档：

- [local_startup_guide.md](local_startup_guide.md)：本地启动、环境变量、联调顺序
- [project_architecture.md](project_architecture.md)：当前系统架构、运行链路、数据契约、部署与安全边界
- [project_structure_frontend_backend.md](project_structure_frontend_backend.md)：仓库结构与模块职责
- [backend_planner_models_reference.md](backend_planner_models_reference.md)：后端规划与调度模型说明
- [agent_runtime_p0_p1.md](agent_runtime_p0_p1.md)：P0/P1 可靠性、安全、治理与运维说明
- [tot_ablation_and_uploads.md](tot_ablation_and_uploads.md)：受限 ToT 消融设计、预算选择和文件上传
- [research_coding_agent.md](research_coding_agent.md)：科研 Coding 智能体组件架构、任务路由、论文调试状态机与自有数据 Benchmark 契约
- [benchmark_agent.md](benchmark_agent.md)：独立评测智能体、train/validation/test 划分、泄漏检查、Metric/Reward 契约和隐藏指标重算
- [autoresearch/00_project_introduction.md](autoresearch/00_project_introduction.md)：AutoResearch 项目定位、架构、核心能力，以及与相关项目和论文的借鉴关系
- [autoresearch/](autoresearch/)：AutoResearch Planner/ResearchSpec、Keep/Reject harness、冻结评测器、证据边界、真实示例与研究路线图
- [autoresearch/11_hierarchical_search_engine.md](autoresearch/11_hierarchical_search_engine.md)：Model 默认穷举、UCB、Beam、UCT-style 参数树、异步 Search Agent 与全局候选榜单
- [claim_evidence_graph.md](claim_evidence_graph.md)：分层论文主张、冻结 Rubric、运行证据绑定、判定边界与前端可视化
- [intent/02_dual_model_finetuning_guide.md](intent/02_dual_model_finetuning_guide.md)：CPU BERT 与 GPU Qwen 双配置、共用数据、微调流程、路由仲裁和 V100 实践
- [intent/03_bert_cpu_architecture.md](intent/03_bert_cpu_architecture.md)：BERT/BGE 编码器、自注意力、分类头、微调与 CPU 部署原理
- [intent/04_qwen_gpu_architecture.md](intent/04_qwen_gpu_architecture.md)：Qwen3-0.6B 因果解码器、GQA、RoPE、RMSNorm、LoRA 与结构化生成原理
- [intent/05_dual_model_training_record_20260722.md](intent/05_dual_model_training_record_20260722.md)：V100 双模型真实训练参数、数据哈希、指标、失败样例与评测修复
- [intent/06_interview_dual_intent_router_story.md](intent/06_interview_dual_intent_router_story.md)：可用于面试的 30 秒、2 分钟和 STAR 表达，以及技术追问与边界说明
- [user_manual.md](user_manual.md)：产品使用说明
- [../examples/](../examples/)：可运行示例、成功条件与已验证结果
- [../examples/autoresearch/intent_router/](../examples/autoresearch/intent_router/)：无需第三方依赖的 AutoResearch 意图路由验收示例
- [../test/claim-evidence/](../test/claim-evidence/)：Claim-to-Evidence 真实 harness golden test、稳定图 JSON 和截图

## Interface Preview

![ScholarAgent dashboard](assets/scholar-agent-dashboard.png)

![ScholarAgent node execution panel](assets/scholar-agent-node-panel.png)

两张截图均由当前组件回放 [`2026-08-10_lightrag_target_stop_e2e.json`](../examples/autoresearch/real_repositories/results/2026-08-10_lightrag_target_stop_e2e.json) 生成。启动前端后可分别访问：

- `http://localhost:5173/test/runtime-preview.html?view=dashboard`
- `http://localhost:5173/test/runtime-preview.html?view=run`
- `http://localhost:5173/test/runtime-preview.html?view=validation`

阶段性改造记录可参考：

- [plan/2026-04-23_breakpoint_resume_node_snapshot.md](plan/2026-04-23_breakpoint_resume_node_snapshot.md)：断点重启与依赖安装最小修复
- [plan/2026-04-23_dependency_install_react_retry.md](plan/2026-04-23_dependency_install_react_retry.md)：依赖安装从规则修复升级为模型 ReAct 重试

历史方案、重构草案和阶段性报告已归档到：

- [archive/](archive/)
