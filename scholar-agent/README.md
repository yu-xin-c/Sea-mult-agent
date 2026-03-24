# ScholarAgent: 智能科研与代码推演多智能体平台

本项目为 **2026年（第19届）中国大学生计算机设计大赛**（软件应用与开发 / 人工智能应用大类）设计的硬核底层架构。旨在解决科研人员与开发者在文献分析、框架评测、代码复现过程中的繁琐痛点。

## 🌟 核心亮点与创新点

1. **动态 DAG 任务编排 (Planner)**：不同于写死的业务逻辑，系统能根据用户的自然语言意图（如“复现论文”或“评测 RAG 框架”），动态生成多智能体协作的有向无环图（DAG），极大提升了系统的泛化能力。
2. **基于字节跳动 Eino 的 Agent 引擎**：Go 后端深度集成了字节跳动开源的 Eino 大模型编排框架，利用 `Graph` 将 Prompt 组装、大模型调用和沙箱执行串联，保证了类型安全和高并发性能。
3. **Docker 安全代码沙箱**：具备真正的“行动力（Computer Use）”。大模型生成的 Python 代码会被送入受 CPU、内存、网络限制的隔离容器中运行，并捕获真实日志返回给 Agent，形成**闭环反思（Self-Reflection）**。
4. **意图网关与 RAG 前置**：使用 Python FastAPI 搭建微服务网关，预留了 BERT 模型微调接口用于意图分类；同时结合 RAG 技术，解决了长上下文大模型在处理海量文献时的“大海捞针（Lost in the Middle）”和幻觉问题。

---

## 🛠️ 快速开始 (推荐方式)

为了方便他人快速上手，本项目提供了一个 **Makefile**，可以一键配置全链路环境。

### 前置依赖
* [Go](https://golang.org/dl/) (推荐 1.22+)
* [Node.js](https://nodejs.org/) (推荐 18+)
* [Docker Desktop](https://www.docker.com/products/docker-desktop/) (必须运行中，用于代码沙箱)

### 一键启动
在项目根目录下执行以下命令：

```bash
# 1. 安装所有服务的依赖 (Go & npm)
make install

# 2. 设置 OpenAI 环境变量 (必须)
export OPENAI_API_KEY=你的Key
export OPENAI_BASE_URL=https://api.deepseek.com/v1 # 可选，默认为 DeepSeek

# 3. 一键后台启动全链路服务 (沙箱、后端、前端)
make run-all
```
*服务启动后：*
- **前端界面**: [http://localhost:5173](http://localhost:5173)
- **后端引擎**: [http://localhost:8080](http://localhost:8080)
- **沙箱服务**: [http://localhost:8082](http://localhost:8082)

### 其他管理命令
```bash
make stop-all  # 停止所有后台服务
make clean     # 清理编译产物和日志
make help      # 查看更多详细命令
```

---

## 🏗️ 系统架构

项目采用微服务与前后端分离架构，核心分为三层：

### 1. 前端交互层 (Frontend - React + Vite + React Flow)
* 提供类似 ChatGPT 的自然语言对话界面。
* 利用 `React Flow` 将后端 Planner 生成的执行计划以**实时可视化的 DAG 图**呈现，让多智能体的协作过程透明可见。

### 2. Go 高并发调度引擎 (Backend - Go + Gin + Eino)
* **API 网关**：基于 Gin 框架，处理前端高并发请求。
* **多智能体编排 (Multi-Agent)**：
    * `Librarian Agent`：负责联网查资料或查询 RAG 向量库。
    * `Coder Agent`：**核心节点**。基于 Eino 框架调用 OpenAI 兼容接口（如 DeepSeek），根据任务需求生成 Python 代码。
    * `Sandbox Agent`：调用 Docker API 启动容器，运行代码并返回 `stdout/stderr`。
    * `Data Agent`：分析日志，生成评估报告。

### 3. Python AI 微服务 (AI Services - FastAPI)
* 负责自然语言处理的脏活累活。如 BERT 意图分类、命名实体识别（NER）以及 RAG 向量数据库（Milvus/Chroma）的离线文档注入和在线检索。

---

## 🛠️ 本地运行指南

### 前置依赖
* [Go](https://golang.org/dl/) (推荐 1.22+)
* [Node.js](https://nodejs.org/) (推荐 18+)
* [Docker Desktop](https://www.docker.com/products/docker-desktop/) (必须运行中，用于代码沙箱)

### 1. 启动 Go 后端调度器
```bash
cd backend
# 下载依赖
go mod tidy
# 运行服务 (默认监听 8080 端口)
go run cmd/api/main.go
```
*注：要使 CoderAgent 真正生成代码，请在运行前设置环境变量 `OPENAI_API_KEY` 和 `OPENAI_BASE_URL`。如果不设置，系统将自动降级使用 Mock 模式，依然可以跑通整个流程图。*

### 2. 启动 React 前端可视化界面
```bash
cd frontend
# 安装依赖
npm install
# 启动开发服务器
npm run dev
```
打开浏览器访问 `http://localhost:5173`。

---

## 🧠 关于 RAG 与代码沙箱的深度思考 (答辩要点)

**Q: 为什么不直接让大模型读几十篇 PDF，而要自己建 RAG 向量库？**
1. **规避“Lost in the Middle”**：大模型处理超长文本时，中间部分的信息召回率极低。RAG 通过先进行语义检索（Chunking + Embedding），只把最相关的段落喂给大模型，提高了答案的精准度。
2. **多跳推理（Multi-hop）对比**：科研需要横向对比多篇论文的某个局部细节（如数据集）。将论文结构化入库后，RAG 可以精准召回不同论文的同一章节进行对比，这是直接喂全文做不到的。
3. **成本与速度**：每次都把几十万字传给大模型 API，成本高昂且延迟巨大。本地向量检索只需毫秒级，极大地提升了系统的工程可用性。

**Q: 引入 Docker 沙箱的意义是什么？**
传统的 LLM 应用只是“聊天机器人”。本项目通过 Docker 沙箱，让 Agent 拥有了真实的“行动力（Computer Use）”。它可以动态安装依赖（如 `pip install langchain`）、运行实验并获取真实的耗时和准确率。**这代表了 AI 从 Copilot 向自主 Agent 演进的最前沿方向**。
