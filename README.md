<div align="center">

# Sea-Mult-Agent

**ScholarAgent: 从论文理解到沙箱实验的多智能体科研执行系统**

[![Go](https://img.shields.io/badge/Go-1.26.1-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=111827)](https://react.dev/)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/compose/)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-research%20prototype-orange)](#project-status)

[快速开始](#quick-start) · [AutoResearch](#run-bounded-autoresearch) · [系统架构](#architecture) · [API](#api) · [实验记录](#reproduction) · [贡献指南](scholar-agent/docs/CONTRIBUTING.md)

</div>

![ScholarAgent beginner-friendly workflow](ScholarAgentOverview.png)

Sea-Mult-Agent 面向论文复现、自有数据仓库评测和受限自动实验。Intent Router 与 Planner 把用户目标转换为经过校验的 DAG，Scheduler 通过类型化 Artifact 把节点路由给 Librarian、Coder、Research Coding 和 Data 等专业 Agent；模型负责理解问题和提出候选，Go Harness 负责文件写入、真实执行、指标判定、回滚与最终验收。日志、状态和结构化证据通过 SSE 返回工作台。

总图先用五个步骤说明系统如何完成一次复现，可编辑源文件见 [ArchitectureDiagram.drawio](ArchitectureDiagram.drawio)，完整组件边界见[项目架构文档](scholar-agent/docs/project_architecture.md)。其中 Native Docker 是当前默认执行引擎；OpenSandbox 仅为可选 fallback，BERT/Qwen 意图模型也未接入默认生产请求链。

> [!NOTE]
> 本项目目前是具备持久化、恢复、审批、预算和受限沙箱能力的单机研究原型，不是已完成多租户安全认证的生产服务。Docker 沙箱仍具有较高宿主机权限，部署前请阅读[项目状态与安全说明](#project-status)。

![ScholarAgent dashboard](scholar-agent/docs/assets/scholar-agent-dashboard.png)

界面截图由当前 React 组件回放 [LightRAG 真实远端实验记录](scholar-agent/examples/autoresearch/real_repositories/results/2026-08-10_lightrag_target_stop_e2e.json)生成，展示完整 `8/8` 执行计划，不是手工拼接的静态原型。

## Why Sea-Mult-Agent

| 能力 | 当前实现 |
|---|---|
| **面向科研的任务规划** | 将论文复现、代码执行、框架对比等目标拆解为可执行 DAG |
| **专业 Agent 路由** | 根据任务类型路由到 Chat、Librarian、Coder、Research Coding 或 Data；沙箱作为确定性执行服务独立运行 |
| **真实隔离执行** | 通过独立 Go 沙箱服务调用原生 Docker，支持持久工作区与产物回传 |
| **仓库优先的论文复现** | 发现或使用指定 GitHub 仓库，准备依赖并运行受控 smoke 实验 |
| **预算受限的消融设计** | ToT 评估参数、模块、数据规模、随机种子和运行成本候选，只执行预算内的高价值组合 |
| **研究材料上传** | 在工作台附加论文、配置、笔记和小型数据文件，按用户隔离并传入复现流程 |
| **科研仓库 Coding Agent** | 对论文代码做受限调试、补丁回滚和重跑，也能为自有数据生成仓库 Benchmark 适配器 |
| **自有数据仓库评测** | Research Coding Agent 生成受限适配器，经 8 条预检、ReAct 修复和指标重算后运行用户数据 |
| **受限 AutoResearch 循环** | 冻结仓库提交、评测器与数据，只允许修改白名单文件；重复测量后 Keep/Reject、退化回滚、目标分数停止，并隐藏复验最佳候选 |
| **逐主张复现验收** | 实验前冻结分层 Rubric，实验后把论文主张、判定准则与真实 Artifact 绑定成可视化证据图 |
| **实时可观测执行** | SSE 推送计划、节点、日志和 Artifact 事件，前端同步展示执行状态 |
| **可靠执行与治理** | 任务租约、迟到结果隔离、取消/重试、持久化恢复、预算和人工审批 |
| **研究工作台** | 集成对话、PDF 阅读、DAG 看板、节点日志、代码、报告与图表视图 |

## Quick Start

### Prerequisites

- Docker Engine 20.10+ 与 Docker Compose v2
- Git
- 一个 OpenAI-compatible LLM API Key
- 本地开发时需要 Go（支持 `GOTOOLCHAIN=auto`）与 Node.js 20+

### Start with Docker Compose

```bash
git clone https://github.com/yu-xin-c/Sea-mult-agent.git
cd Sea-mult-agent/scholar-agent

cp backend.env.example backend.env
# 编辑 backend.env，至少填写 OPENAI_API_KEY

docker compose up --build -d
```

启动后访问：

| 服务 | 地址 |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health Check | http://localhost:8080/api/health |
| Sandbox API | http://localhost:8082 |

确认后端与沙箱均已就绪：

```bash
curl -s http://localhost:8080/api/health
```

期望响应中同时出现 `backend.ok=true`、`repository.ok=true` 与 `sandbox.ok=true`。`repository.ok` 会在运行镜像缺少 Git 时明确失败，避免服务表面健康但外部仓库任务不可用。查看日志或停止服务：

```bash
docker compose logs -f
docker compose down
```

### Try a Reproduction Plan

在 Web UI 中输入：

```text
请使用 https://github.com/harvardnlp/annotated-transformer 复现
Attention Is All You Need，使用 smoke 模式运行轻量注意力消融，
不要执行 WMT14 完整训练。
```

系统会生成并执行如下主链：

```text
解析论文 -> 冻结主张 Rubric -----------+
    \-> 检索仓库 -> 准备工作区 -> 解析依赖
        -> 准备运行时 -> 安装依赖 -> 执行实验 -> 对比论文声明
                                                  -> 主张证据图
```

### Benchmark a Repository with Your Data

在 Web UI 上传 `CSV`、`TSV`、`JSON` 或 `JSONL`，然后输入：

```text
用 https://github.com/OWNER/REPOSITORY 跑 benchmark，
输入列是 review，标签列是 label，最多运行 500 条样本。
```

系统会分析数据契约、克隆仓库、生成独立适配器，并在正式运行前用最多 8 条样本预检。分类和回归指标会根据逐样本预测由 Go harness 重算。论文代码运行失败时，同一 Agent 还能在有限源码上下文中生成最小补丁并重跑。组件架构、状态机、Artifact 契约和限制见 [Research Coding Agent](scholar-agent/docs/research_coding_agent.md)。

### Run Bounded AutoResearch

仓库中准备 `autoresearch.spec/v1` 配置后，在 Web UI 输入：

```text
用 https://github.com/OWNER/REPOSITORY 做 AutoResearch，
按 autoresearch.json 运行，最多 3 轮，总时长不超过 15 分钟，最终验证 3 次。
```

系统会冻结 ResearchSpec 和可选 `repository_revision`，先按 `search_runs` 重复测量 baseline，再让 Research Coding Agent 生成白名单内的小改动。只有聚合主指标达到最小提升才保留候选；退化或部分执行失败的候选会回滚。Keep 候选达到可选 `target_score` 后由 harness 确定性停止。循环结束后可启动 1 至 5 次新进程验证：未配置 holdout 时标记为 `search_evaluator_replay`，只证明公开评测可重复；配置模型不可见 holdout 时标记为 `hidden_holdout`，最终接受由隐藏指标决定。报告包含原始搜索样本、标准差、逐次验证分数、失败率以及命令资源摘要。可运行样例见 [Intent Router AutoResearch](scholar-agent/examples/autoresearch/intent_router/)和[四仓库真实实验](scholar-agent/examples/autoresearch/real_repositories/)，实现边界见 [AutoResearch 模块文档](scholar-agent/docs/autoresearch/)。

#### Research Foundations

这里的“借鉴”是方法组合与工程落地，不表示复制了来源项目的代码，也不表示 ScholarAgent 已具备原工作的全部能力。

| 项目或论文 | 借鉴到 ScholarAgent 的内容 | 当前边界 |
|---|---|---|
| [karpathy/autoresearch](https://github.com/karpathy/autoresearch) | 小改动、固定预算、真实指标以及 Keep/Reject 循环 | 核心循环已实现，并扩展为跨仓库 ResearchSpec、多文件白名单、回滚和 TrialLedger |
| [ReAct](https://arxiv.org/abs/2210.03629) | 错误观察、结构化修复动作和重跑 | 已用于依赖安装与 Benchmark 预检；不负责科研指标判定 |
| [Tree of Thoughts](https://arxiv.org/abs/2305.10601) | 展开多个消融候选，再按信息增益、成本和风险选择 | 已用于轻量消融设计；不接管 AutoResearch 每轮 Keep/Reject |
| [The AI Scientist](https://arxiv.org/abs/2408.06292) | 将想法、代码、实验、可视化和报告连接为长科研链 | 属于端到端方向启发，不宣称全自动科学发现或自动科学真值 |
| [Agent Laboratory](https://arxiv.org/abs/2501.04227) | 文献、实验和写作阶段的专业 Agent 分工与用户介入 | 专业角色已实现；完整人工审批检查点仍在路线图 |
| [PaperBench](https://arxiv.org/abs/2504.01848) | 用分层 Rubric 拆解论文复现，并逐项绑定证据 | 已实现 Claim Rubric 与 Claim-to-Evidence Graph；它属于上层复现验收，不替代主指标 |
| [CORE-Bench](https://arxiv.org/abs/2409.11363) | 真实论文仓库、真实执行环境与任务专用复现 Agent | 已实现 Research Coding Agent 和可复查 Artifact；尚未完成公开 CORE-Bench 全量对照 |
| [SWE-agent](https://arxiv.org/abs/2405.15793) | 仓库级受限阅读、代码编辑和测试反馈 | 仅作 Engineering scaffold 对照，没有移植 SWE-agent ACI 或宣称其评测结果 |
| [CORE-Bench 2026 analysis](https://arxiv.org/abs/2606.26158) | 从准确率扩展到可靠性、效率、OOD、scaffold 和人机协作 | 已覆盖重复复验和部分资源证据；OOD、多 seed 与人机协作尚未完成 |
| [MLE-bench](https://arxiv.org/abs/2410.07095) | 同时观察任务效果和计算资源投入 | 已记录命令次数与耗时，尚不包含 GPU、token 和费用账本 |
| [Microsoft R&D-Agent](https://github.com/microsoft/RD-Agent) | Research/Development 分工，以及多次实验的统计报告方式 | 已输出重复验证的均值、标准差和失败率；当前不会自动注入不同 seed |
| [Auto-Research-Recipes](https://github.com/cxcscmu/Auto-Research-Recipes) | 任务无关核心、Task Adapter、外部 evaluator 和可发布 Artifact | 已沉淀统一三文件任务包；尚无分支候选 parent lineage |
| [Arbor](https://github.com/RUC-NLPIR/Arbor) | Coordinator/Executor、想法树、隔离 worktree 和开发/heldout 分离 | 已有隔离工作区和搜索/隐藏验收边界；并行树搜索与 checkpoint 未实现 |
| [AI Scientist v2](https://github.com/SakanaAI/AI-Scientist-v2) | 渐进式 Agent tree search 与实验管理 | 当前只有线性 TrialLedger、反馈和回滚，不宣称已实现树搜索 |
| [Deep Research Agent Stochasticity](https://arxiv.org/abs/2602.23271) | 独立运行存在方差，需要重复测量和聚合 | baseline 与每个候选已支持重复 evaluator、标准差和方向相关 worst |

这些方法位于不同决策层：ToT 选择高价值消融，ReAct 处理有限故障恢复，AutoResearch 按冻结指标执行 Keep/Reject，PaperBench 风格 Rubric 与证据图负责上层论文验收。完整来源事实、代码落点和过度声明风险见 [AutoResearch 项目介绍](scholar-agent/docs/autoresearch/00_project_introduction.md)与[证据表](scholar-agent/docs/autoresearch/refs/evidence-map.md)。

真实外部仓库审计（2026-08-10）：通过完整 API/Docker 链运行 [rank-bm25、Tenacity、LightRAG 和 Microsoft GraphRAG](scholar-agent/examples/autoresearch/real_repositories/)，四份原始结果都记录实际 commit。四组搜索均使用 `3 x worst`，公开分数分别从 `5/9、6/7、4/8、6/12` 提升到满分，新的模型不可见 holdout 均从低基线提升到 `4/4`，且最终 `3/3` 重复验证通过。实验同时推动三项架构修复：重复搜索测量防止单次尖峰、`repository_revision` 防止 HEAD 漂移、`target_score` 防止满分平台浪费预算；后两项用固定 LightRAG 提交做了独立对照。早期 GraphRAG `11/11` 公开满分但隐藏 `3/4` 的失败记录仍原样保留。完整机器记录和不足复盘见[真实外部仓库实验](scholar-agent/docs/autoresearch/08_real_repository_experiments.md)。

![ScholarAgent AutoResearch architecture](scholar-agent/docs/assets/autoresearch-architecture.png)

架构图的可编辑矢量源文件见 [autoresearch-architecture.svg](scholar-agent/docs/assets/autoresearch-architecture.svg)。模型只负责提出候选；固定 Planner、ResearchSpec、Go policy gate、Docker 沙箱和最终验证共同掌握执行与接受边界。重复进程不等于多 seed，公开 evaluator 重放也不等于隐藏验证；统计规则和资源口径见 [重复验证与执行资源证据](scholar-agent/docs/autoresearch/07_repeated_validation_and_resource_evidence.md)。

![AutoResearch hidden holdout validation view](scholar-agent/docs/assets/autoresearch-validation-view.jpg)

## Interface

执行图仅突出主控制链和必要的数据依赖，重复连线会自动合并。节点编号、Agent 类型和状态共同建立阅读顺序；长链使用紧凑蛇形布局，移动端可在“对话 / 流程”视图间切换。

点击节点后可以查看任务描述、实时日志、生成代码、报告、指标和图表。论文复现末端还会提供三泳道 Claim-to-Evidence Graph，可缩放查看每条主张、独立准则、证据状态和 Artifact 哈希。

![ScholarAgent node execution panel](scholar-agent/docs/assets/scholar-agent-node-panel.png)

## Architecture

```text
Researcher / Paper / Repository / Dataset
                    |
                    v
             React Workbench
                    |
            REST / Upload / SSE
                    |
                    v
    API -> Intent Router -> Planner + DAG Validator
                               |
                 Validated PlanGraph + FilePlanStore
                               |
          Scheduler (lease / retry / cancel / budget)
                               |
                    Routed Task Executor
          +--------+----------+-------+-----------------+------+
          |        |          |       |                 |      |
         Chat  Librarian    Coder  Research Coding     Data
                    |          |     /       |       \     |
             Claim Rubric   Runtime  Paper  Benchmark  AutoResearch
                                    Debug    Adapter
                                      \        |        /
                                       Go Policy Gate
                                   |
              Sandbox Client -> docker-sandbox -> Native Docker
                                   |
           stdout / metrics / files / TrialLedger / evidence graph
                                   |
                         Artifact + Event -> SSE
```

### Core Components

| 组件 | 目录 | 职责 |
|---|---|---|
| Frontend | `scholar-agent/frontend` | React 工作台、DAG 可视化、PDF 与执行结果展示 |
| Backend | `scholar-agent/backend` | Gin API、意图识别、Planner、Scheduler、Agent 与 SSE |
| Docker Sandbox | `scholar-agent/docker-sandbox` | 容器创建、命令执行、文件与运行时生命周期管理 |
| Python AI Service | `scholar-agent/ai-services` | 可选的 Python 意图识别服务，不在默认 Compose 中启动 |
| Documentation | `scholar-agent/docs` | 启动、架构、规划、实验和用户文档 |

### Agent Roles

| Role | Responsibility |
|---|---|
| **Librarian** | 论文解析、资料检索、方法与声明提取，以及实验前冻结分层 Rubric |
| **Coder** | 仓库发现、代码准备、依赖分析和修复 |
| **Sandbox** | 运行时准备、依赖安装与隔离实验执行 |
| **Data** | 指标汇总、论文声明对比、证据图判定、报告与图表生成 |
| **Research Coding** | 论文仓库代码调试、受限补丁与重跑、自有数据 Benchmark 适配，以及冻结规格驱动的 AutoResearch 循环 |
| **Chat** | 通用问答与轻量任务入口 |

## API

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/health` | 检查后端、沙箱与 GPU runtime 状态 |
| `POST` | `/api/plan` | 根据用户意图创建并保存 DAG |
| `GET` | `/api/plans/:id` | 查询计划、节点状态和产物 |
| `POST` | `/api/plans/:id/execute` | 启动整张计划图 |
| `POST` | `/api/plans/:id/approve` | 批准需要人工确认的计划 |
| `POST` | `/api/plans/:id/cancel` | 取消计划与未完成节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` | 重试失败、阻塞或取消节点 |
| `POST` | `/api/plans/:id/tasks/:taskId/reassign` | 重分配节点并使旧执行租约失效 |
| `GET` | `/api/plans/:id/events` | 获取计划事件历史 |
| `GET` | `/api/plans/:id/stream` | 订阅计划级 SSE 事件流 |
| `POST` | `/api/execute` | 直接执行单个 Agent 任务并流式返回结果 |
| `POST` | `/api/uploads` | 上传论文、配置或小型 Benchmark 数据并返回附件 ID |
| `GET` | `/api/uploads/:id/content` | 按用户所有权读取上传内容 |
| `POST` | `/api/chat` | 通用对话接口 |
| `GET` | `/api/pdf-proxy?url=...` | 代理读取远端 PDF |

## Configuration

复制 `scholar-agent/backend.env.example` 为 `backend.env` 后配置：

| Variable | Required | Default / Purpose |
|---|---:|---|
| `OPENAI_API_KEY` | Yes | OpenAI-compatible API 密钥 |
| `OPENAI_BASE_URL` | No | 默认示例为 DashScope compatible endpoint |
| `OPENAI_MODEL_NAME` | No | 默认示例为 `qwen3-coder-plus` |
| `SANDBOX_URL` | No | 本地默认 `http://localhost:8082`；Compose 会覆盖为服务地址 |
| `SANDBOX_DEFAULT_IMAGE` | No | 默认 Python 运行时；根 `pyproject.toml` 声明更高 `requires-python` 时只向上适配 |
| `REDIS_ADDR` | No | 启用会话记忆；未设置时使用 No-op memory store |
| `REDIS_USERNAME` / `REDIS_PASSWORD` / `REDIS_DB` | No | 可选 Redis 认证与数据库配置 |
| `PLAN_STORE_PATH` | No | 单机计划和事件 JSON 存储；Compose 默认启用持久卷 |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | No | 计划尝试次数与时长预算 |
| `REQUIRE_PLAN_APPROVAL` | No | 强制计划在执行前人工审批 |
| `API_AUTH_TOKEN` / `SANDBOX_API_TOKEN` | No | 部署 API 与内部沙箱的静态 Bearer 保护 |
| `CORS_ALLOWED_ORIGINS` | No | 允许访问后端的前端 Origin 列表 |

后端镜像构建还支持 `DEBIAN_MIRROR` 与 `DEBIAN_SECURITY_MIRROR`。它们由执行 `docker compose` 的 shell 或项目 `.env` 读取，不是 `backend.env` 中的运行时变量；默认仍使用 Debian 官方源。网络受限环境可以显式覆盖：

```bash
DEBIAN_MIRROR=https://your-mirror.example/debian \
DEBIAN_SECURITY_MIRROR=https://your-mirror.example/debian-security \
docker compose up --build -d
```

GPU 透传需要宿主机安装 NVIDIA Container Toolkit，并在 Compose 环境中设置：

```bash
SANDBOX_DOCKER_GPUS=all docker compose up --build -d
```

这只启用 GPU 设备透传；`SANDBOX_DEFAULT_IMAGE` 仍需指向包含 CUDA 与所需框架的镜像。

## Development

```bash
cd scholar-agent

make install       # 安装前端依赖并整理 Go modules
make lint          # 前端 ESLint
make test          # 后端、沙箱与离线示例测试
make build         # 构建前端、后端与沙箱
make package       # 构建带嵌入式前端的单文件服务
```

分别启动本地服务时，请在三个终端中运行：

```bash
make run-sandbox
make run-backend
make run-frontend
```

Windows 用户可使用 `scholar-agent/scripts/windows/` 中的 PowerShell 脚本。更完整的环境说明见[本地启动指南](scholar-agent/docs/local_startup_guide.md)。

## Reproduction

项目包含可审计的轻量论文复现记录，用于验证 ScholarAgent 的执行链和结构行为，不替代论文完整训练结果。

从 [`examples/paper-reproduction`](scholar-agent/examples/paper-reproduction/) 开始，可以通过 Web API
重跑同一条项目原生链路，并自动验收 DAG 状态、仓库选择、关键 Artifact 和事件历史。

| Record | Execution Boundary | Result |
|---|---|---|
| [项目原生 DAG 消融](scholar-agent/docs/experiments/2026-07-17_attention_project_native_ablation.md) | `/api/plan` -> Scheduler -> Agent -> Docker -> SSE/Artifact | 8/8 节点完成，15 个产物，61 个事件 |
| [真实仓库 smoke test](scholar-agent/docs/experiments/2026-07-17_attention_repo_smoke.md) | 指定论文仓库的受控集成测试 | 验证仓库发现、准备与执行链 |
| [单节点 CPU 消融](scholar-agent/docs/experiments/2026-07-17_attention_light_ablation.md) | ScholarAgent `/api/execute` | 标准库 CPU 微基准 |
| [独立 V100 消融](scholar-agent/docs/experiments/2026-07-17_attention_gpu_ablation.md) | 项目外独立 PyTorch CUDA 脚本 | 非 ScholarAgent DAG，单独记录 |

上述 smoke 实验不下载 WMT14、不复现 BLEU，也不应外推为完整论文训练结论。

## Project Layout

```text
Sea-mult-agent/
├── README.md
├── LICENSE
├── ScholarAgentOverview.png     # 面向新用户的五步流程总图
├── ArchitectureDiagram.drawio   # 可编辑的当前系统总架构图
├── docker-core/                 # 早期/底层 Docker 执行组件
└── scholar-agent/
    ├── backend/                 # Go API 与多 Agent 编排核心
    ├── frontend/                # React + TypeScript 工作台
    ├── docker-sandbox/          # 独立 Go Docker 沙箱服务
    ├── ai-services/             # 可选 Python 服务
    ├── examples/                # 可运行示例与验收脚本
    ├── test/                    # 功能 golden test 数据、运行器与截图
    ├── docs/                    # 文档与实验记录
    ├── scripts/                 # Unix / Windows 启动脚本
    ├── backend.env.example
    ├── docker-compose.yml
    └── Makefile
```

## Documentation

- [项目架构](scholar-agent/docs/project_architecture.md)
- [本地启动指南](scholar-agent/docs/local_startup_guide.md)
- [用户手册](scholar-agent/docs/user_manual.md)
- [可运行示例](scholar-agent/examples/)
- [前后端项目结构](scholar-agent/docs/project_structure_frontend_backend.md)
- [规划与调度设计](scholar-agent/docs/plan/)
- [Agent Runtime P0/P1](scholar-agent/docs/agent_runtime_p0_p1.md)
- [受限 ToT 消融与文件上传](scholar-agent/docs/tot_ablation_and_uploads.md)
- [Research Coding Agent](scholar-agent/docs/research_coding_agent.md)
- [AutoResearch 项目介绍](scholar-agent/docs/autoresearch/00_project_introduction.md)
- [AutoResearch 模块文档](scholar-agent/docs/autoresearch/)
- [Claim-to-Evidence Graph](scholar-agent/docs/claim_evidence_graph.md)
- [Claim-to-Evidence 可运行验收](scholar-agent/test/claim-evidence/)
- [论文仓库发现](scholar-agent/docs/papers_with_code/)
- [意图识别与评测](scholar-agent/docs/intent/)
- [贡献指南](scholar-agent/docs/CONTRIBUTING.md)

<a id="project-status"></a>
## Project Status

- **Research prototype**：接口和数据结构仍可能调整，不承诺向后兼容。
- **Persistent single-node runtime**：配置 `PLAN_STORE_PATH` 后会原子持久化计划与事件，并在重启时恢复中断任务；多副本部署仍需要共享事务数据库和 leader election。
- **Authentication**：已支持静态 API token 和计划所有权检查，但用户 ID/游客会话仍不是 OIDC、RBAC 或生产级多租户认证。
- **Sandbox privilege**：已有 CPU、内存、PID、capability、镜像与挂载限制，Compose 仅本机暴露沙箱端口；但 Docker socket 仍等同于较高宿主机权限。
- **GPU runtime**：GPU 透传已在 V100 主机验证，但默认运行时镜像为 CPU/通用 Python 镜像。
- **Full reproduction**：当前重点是轻量 smoke 与结构消融，不包含大规模数据集训练。

## Contributing

Issue、文档改进、测试和小范围 PR 都欢迎提交。重大功能或架构调整请先创建 Issue 讨论，并在提交前运行：

```bash
cd scholar-agent
make lint
make test
make build
```

详细约定见[贡献指南](scholar-agent/docs/CONTRIBUTING.md)。

## License

Sea-Mult-Agent 使用 [MIT License](LICENSE)。
