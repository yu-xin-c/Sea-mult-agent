# 🌊 Sea-Mult-Agent

**Sea-Mult-Agent** 是一个面向学术研究场景的多智能体协作系统（Scholar-Agent），旨在通过多个专业化 AI Agent 的协同工作，帮助研究者完成从文献检索、数据分析到代码执行的完整科研工作流。

## ✨ 核心特性

- **多智能体协作** — 内置 Librarian（文献助手）、Coder（代码助手）、Data（数据助手）等多个专业化 Agent，各司其职、协同完成复杂任务
- **意图识别与路由** — 基于规则 + LLM 的双层意图分类引擎，精准识别用户意图并自动路由到对应 Agent
- **任务规划与调度** — Planner 将复杂任务分解为 DAG 执行图，Scheduler 按依赖关系并行调度执行
- **沙箱代码执行** — 基于 Docker / OpenSandbox 的隔离执行环境，安全运行用户代码
- **实时交互界面** — React + TypeScript 前端，支持对话、PDF 阅读、执行图可视化等多面板协作
- **SSE 实时推送** — 后端事件总线 + SSE 流式推送，Agent 执行过程实时可见

## 🏗️ 系统架构

```
┌─────────────────────────────────────────────────────────┐
│                    Frontend (React)                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────┐ │
│  │ ChatPanel│  │ PdfPanel │  │GraphPanel│  │ExecSide │ │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬────┘ │
│       └──────────────┴──────────────┴─────────────┘      │
│                         SSE / REST                       │
├─────────────────────────────────────────────────────────┤
│                   Backend (Go)                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────┐ │
│  │  Intent   │  │ Planner  │  │Scheduler │  │  Agent   │ │
│  │Classifier │  │          │  │          │  │ Router   │ │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬────┘ │
│       │              │              │              │      │
│  ┌────┴─────┐  ┌─────┴────┐  ┌─────┴────┐  ┌────┴────┐ │
│  │   LLM /  │  │Plan Store│  │ Executor  │  │Librarian│ │
│  │  Redis   │  │          │  │           │  │  Coder  │ │
│  └──────────┘  └──────────┘  └───────────┘  │  Data   │ │
│                                              └─────────┘ │
├─────────────────────────────────────────────────────────┤
│              AI Services (Python)                        │
│  ┌──────────────────────────┐                           │
│  │  Intent Recognition API  │                           │
│  └──────────────────────────┘                           │
├─────────────────────────────────────────────────────────┤
│              Sandbox (Docker / OpenSandbox)              │
└─────────────────────────────────────────────────────────┘
```

## 📁 项目结构

```
Sea-mult-agent/
├── README.md
└── scholar-agent/
    ├── backend/                  # Go 后端服务
    │   ├── cmd/
    │   │   ├── api/main.go       # API 服务器入口
    │   │   └── app/main.go       # 应用入口
    │   ├── internal/
    │   │   ├── agent/            # 多 Agent 实现（Librarian, Coder, Data, Chat）
    │   │   ├── api/              # HTTP 路由与中间件
    │   │   ├── events/           # 事件总线
    │   │   ├── Intent/           # 意图分类器（规则 + LLM + Redis）
    │   │   ├── models/           # 数据模型（Task, Graph, Intent, Event, Artifact）
    │   │   ├── planner/          # 任务规划器
    │   │   ├── sandbox/          # 沙箱接口
    │   │   ├── sandboxserver/    # 沙箱服务器（Docker / OpenSandbox）
    │   │   ├── scheduler/        # DAG 调度器与执行器
    │   │   ├── store/            # 计划存储
    │   │   └── tools/            # Agent 工具（ask_user_question 等）
    │   └── webui/                # 前端静态资源嵌入
    ├── frontend/                 # React + TypeScript 前端
    │   └── src/
    │       ├── app/              # 应用主体（ScholarApp, Context, Hooks）
    │       ├── features/         # 功能模块
    │       │   ├── chat/         # 对话面板
    │       │   ├── execution/    # 执行侧边栏
    │       │   ├── pdf-viewer/   # PDF 阅读器
    │       │   ├── plan-graph/   # DAG 执行图可视化
    │       │   └── shared/       # 共享组件
    │       ├── services/         # API 服务层
    │       ├── contracts/        # 类型契约
    │       └── hooks/            # 通用 Hooks
    ├── ai-services/              # Python AI 微服务
    │   └── intent_recognition/   # 意图识别服务
    ├── docker-sandbox/           # 独立沙箱服务
    ├── docs/                     # 项目文档
    │   ├── local_startup_guide.md
    │   ├── user_manual.md
    │   ├── CONTRIBUTING.md
    │   ├── intent/               # 意图识别文档与基准测试
    │   ├── plan/                 # 规划模块设计文档
    │   ├── papers_with_code/     # 论文与代码发现文档
    │   └── archive/              # 归档设计文档
    ├── scripts/                  # 启动脚本（Unix / Windows）
    ├── docker-compose.yml
    ├── Makefile
    └── backend.env.example
```

## 🤖 Agent 体系

| Agent | 职责 | 说明 |
|-------|------|------|
| **Librarian** | 文献检索与管理 | 搜索学术论文、管理文献库、提取论文关键信息 |
| **Coder** | 代码生成与执行 | 编写和运行代码、数据分析脚本、可视化图表 |
| **Data** | 数据处理与分析 | 数据清洗、统计分析、结果解读 |
| **Chat** | 通用对话 | 日常问答、任务协调、用户引导 |

## 🧠 意图识别

系统采用**规则优先 + LLM 兜底**的双层意图分类架构：

1. **规则层** — 基于关键词匹配的快速分类，覆盖常见意图模式
2. **LLM 层** — 调用大语言模型进行语义理解，处理规则无法覆盖的复杂意图
3. **Redis 缓存** — 缓存历史分类结果，加速重复意图的识别

支持的意图类型包括：文献搜索、代码执行、数据分析、PDF 阅读、通用问答等。

## 📋 任务规划与调度

- **Planner** — 将用户请求分解为多个子任务，构建 DAG（有向无环图）执行计划
- **Scheduler** — 按照依赖关系拓扑排序，并行执行无依赖的子任务
- **Executor** — 管理每个子任务的生命周期，支持重试与错误处理
- **Plan Store** — 内存存储执行计划，支持断点续执行

## 🚀 快速开始

### 前置要求

- **Go** 1.22+
- **Node.js** 18+ & npm
- **Python** 3.10+
- **Docker** & Docker Compose（用于沙箱和容器化部署）
- **Redis**（意图识别缓存，可选）

### 环境配置

```bash
# 1. 克隆仓库
git clone https://github.com/yu-xin-c/Sea-mult-agent.git
cd Sea-mult-agent/scholar-agent

# 2. 配置后端环境变量
cp backend.env.example backend.env
# 编辑 backend.env，填入必要的 API Key 等配置
```

### 本地开发启动

#### 方式一：使用 Makefile

```bash
# 启动所有服务
make dev

# 或分别启动
make backend    # 启动 Go 后端
make frontend   # 启动前端开发服务器
make sandbox    # 启动沙箱服务
make ai         # 启动意图识别服务
```

#### 方式二：使用脚本

```bash
# Unix
./scripts/unix/start-backend.sh
./scripts/unix/start-frontend.sh
./scripts/unix/start-sandbox.sh

# Windows
scripts\windows\start-backend.ps1
scripts\windows\start-frontend.ps1
scripts\windows\start-sandbox.ps1
```

#### 方式三：Docker Compose

```bash
docker-compose up --build
```

### 访问服务

| 服务 | 地址 |
|------|------|
| 前端界面 | http://localhost:5173 |
| 后端 API | http://localhost:8080 |
| 意图识别服务 | http://localhost:8000 |

## 🛠️ 技术栈

| 层级 | 技术 |
|------|------|
| **前端** | React 19, TypeScript, Vite, CSS |
| **后端** | Go 1.22, Gin, SSE |
| **AI 服务** | Python, FastAPI |
| **沙箱** | Docker, OpenSandbox |
| **缓存** | Redis |
| **部署** | Docker Compose, Makefile |

## 📖 文档

- [本地启动指南](scholar-agent/docs/local_startup_guide.md) — 详细的本地开发环境搭建
- [用户手册](scholar-agent/docs/user_manual.md) — 功能使用说明
- [项目结构说明](scholar-agent/docs/project_structure_frontend_backend.md) — 前后端架构详解
- [贡献指南](scholar-agent/docs/CONTRIBUTING.md) — 如何参与项目开发
- [意图识别文档](scholar-agent/docs/intent/) — 意图分类设计与基准测试
- [规划模块文档](scholar-agent/docs/plan/) — 任务规划与调度设计

## 🤝 贡献

欢迎贡献！请阅读 [贡献指南](scholar-agent/docs/CONTRIBUTING.md) 了解详情。

## 📄 许可证

本项目采用 MIT 许可证，详见 [LICENSE](LICENSE) 文件。