# ScholarAgent: 智能科研助理多智能体平台技术与架构报告

## 一、 项目背景与意义
在当前的科研和开发流程中，科研人员和开发者面临着文献浩如烟海、代码复现困难、数据处理繁琐等痛点。现有的单体大模型工具往往受限于上下文长度、缺乏复杂任务拆解能力，且不具备安全执行代码的环境。本项目旨在打造一个“全链路智能科研助理与文献沙箱推演系统” (ScholarAgent)，通过多智能体协作、RAG检索增强和安全沙箱执行，自动化解决从文献调研、数据分析到代码复现的复杂工作流。

本项目高度契合2026年（第19届）中国大学生计算机设计大赛“软件应用与开发”及“人工智能应用”赛道的创新要求，展现了从底层模型微调、后端高并发架构到前沿Agent范式的全栈工程能力。

## 二、 系统整体架构
系统采用前后端分离、微服务化与容器化隔离的架构设计，核心由意图识别前置网关、Go高性能调度后端、多智能体协同层和Docker沙箱执行层组成。

### 1. 核心层级划分
- **用户交互层 (Frontend)**：基于React/Vue构建，提供对话式交互界面和DAG（有向无环图）任务流可视化监控。
- **意图网关层 (Intent Gateway)**：基于微调的BERT模型，对用户输入的自然语言指令进行精准分类和实体抽取。
- **规划与调度层 (Go Backend)**：系统的心脏。Go语言负责接收请求、调用Planner进行任务拆解（生成DAG），并调度底层Agent执行。
- **多智能体层 (Multi-Agent System)**：一组专业化的Agent群（如文献Agent、代码Agent、数据分析Agent），在Planner的指挥下协同工作。
- **知识库层 (RAG)**：结合向量数据库（如Milvus/Chroma）和BM25，提供准确的文献检索能力。
- **工具与沙箱层 (Sandbox & Tools)**：基于Docker/gVisor构建的代码安全执行环境，以及供Agent调用的API工具集。

## 三、 核心技术模块设计与实现

### 1. 意图识别模块 (BERT-based Intent Recognition)
- **技术选型**：Python + PyTorch + HuggingFace (BERT-base-chinese)。
- **实现方案**：为了降低大模型API的调用频率和成本，系统在入口处部署轻量级BERT模型。通过LoRA或全参数微调，将其训练为意图分类器。
- **功能**：将用户输入（如“帮我跑一下这篇论文的代码并画出loss曲线”）识别为复合任务类型（Task Type: Code_Execution + Data_Analysis）。

### 2. Planner与多智能体协同 (Planner & Multi-Agent)
- **技术选型**：Go语言调度控制流 + 大模型(DeepSeek/Qwen等)提供智力支持。
- **实现方案**：
  - **Planner Agent**：作为“项目经理”，根据意图网关的输出，生成带有依赖关系的DAG任务流（例如：下载代码 -> 运行代码 -> 分析日志 -> 生成图表）。
  - **Worker Agents**：每个Agent被赋予特定的System Prompt和工具权限。它们通过异步消息队列或共享的State数据库进行状态同步和信息传递。

### 3. RAG 检索增强模块 (Retrieval-Augmented Generation)
- **技术选型**：LangChain/LlamaIndex思想 + 向量数据库(Milvus/Chroma)。
- **实现方案**：
  - **离线处理**：将PDF文献进行版面分析、Chunk切片，生成Embedding存入向量数据库。
  - **在线检索**：采用Hybrid Search（向量相似度 + BM25关键词匹配），并结合Query Rewrite（查询重写）技术，提高检索召回率。将检索到的高相关性文献片段作为Context注入大模型，解决科研问答中的幻觉问题。

### 4. 代码执行沙箱 (Docker Sandbox)
- **技术选型**：Go语言 (`github.com/moby/moby/client`) + Docker Engine。
- **实现方案**：这是系统的护城河。当代码Agent生成Python代码后，Go后端会动态创建一个Docker容器。
  - **资源隔离**：通过HostConfig限制容器的CPU、内存和网络访问权限，防止恶意代码执行。
  - **闭环反思**：Go程序捕获容器的标准输出(stdout)和标准错误(stderr)。如果代码报错，将stderr作为反馈发回给代码Agent，触发**Self-Reflection（自我反思）**机制，Agent自动修改代码并重新提交运行，直到成功。

### 5. Go高性能后端与状态管理
- **技术选型**：Go + Gin/Fiber + Redis + PostgreSQL/SQLite。
- **实现方案**：
  - **高并发支撑**：利用Goroutine处理多租户并发请求。
  - **实时流式通信**：通过WebSocket/SSE，将Agent的思考过程（Thought）、工具调用日志和沙箱执行状态实时推送到前端展示。
  - **DAG Checkpoint**：任务状态持久化，支持任务中断后的断点续传和失败重试。

## 四、 创新亮点与大赛竞争力
1. **Agent范式落地**：超越了传统的单轮对话Chatbot，实现了基于Planner-Worker架构的自主多智能体协同。
2. **安全可控的Computer Use**：引入Docker沙箱，赋予Agent真实的“行动力”（读写文件、运行代码），对齐目前业界最前沿的OpenHands架构。
3. **复合技术栈的完美融合**：展示了选手从深度学习算法（BERT炼丹）、大模型应用（RAG+Prompt Engineering）到后端架构工程（Go并发+容器编排）的全面能力。
4. **切中实际痛点**：以“科研助理”为切入点，业务场景清晰，极易引起高校评委的共鸣。

## 五、 后续开发路线图 (Roadmap)
- **Phase 1: 基础设施搭建** (Go后端框架、Docker沙箱通信验证)。
- **Phase 2: 核心AI接入** (大模型API接入、单体Agent功能验证)。
- **Phase 3: 多智能体与Planner** (实现DAG任务拆解和Agent间通信)。
- **Phase 4: RAG与微调** (搭建本地向量库，完成BERT意图识别模型的微调与部署)。
- **Phase 5: 前端可视化** (DAG任务流和实时日志的UI呈现)。
