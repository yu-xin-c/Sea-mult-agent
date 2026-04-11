# ScholarAgent 项目结构梳理

## 1. 项目整体分层

这个仓库是一个多模块项目，主要由 4 个部分组成：

1. `frontend`
   - React + TypeScript + Vite 前端应用
   - 负责聊天界面、DAG 可视化、任务执行面板、PDF 查看与交互

2. `backend`
   - Go 后端服务
   - 负责 HTTP API、任务规划、Agent 调度、与沙箱服务通信

3. `ai-services`
   - Python FastAPI 微服务
   - 当前包含意图识别服务，负责自然语言任务的分类入口

4. `docker-sandbox`
   - Go 沙箱服务
   - 负责创建/销毁容器、执行 Python 代码或命令，给后端提供隔离执行环境

---

## 2. 哪些是前端部分

前端代码集中在 `frontend/` 目录。

### 前端核心目录

- `frontend/src/App.tsx`
  - 前端主页面
  - 负责聊天面板、任务触发、DAG 节点展示、执行日志展示、报告/代码/图片切换
  - 会直接请求后端接口：
    - `POST http://localhost:8080/api/chat`
    - `POST http://localhost:8080/api/plan`
    - `POST http://localhost:8080/api/execute`

- `frontend/src/main.tsx`
  - React 应用入口

- `frontend/src/hooks/useAITranslationPlugin.tsx`
  - PDF 相关 AI 翻译/划词交互插件

- `frontend/src/App.css`
  - 页面主样式

- `frontend/src/index.css`
  - 全局样式

- `frontend/public/`
  - 静态资源目录

- `frontend/src/assets/`
  - 前端图片资源

### 前端配置文件

- `frontend/package.json`
  - 前端依赖和脚本
  - 技术栈包括 React、Vite、TypeScript、Axios、React Markdown、PDF Viewer、React Flow

- `frontend/vite.config.ts`
  - Vite 构建配置

- `frontend/tsconfig*.json`
  - TypeScript 配置

- `frontend/eslint.config.js`
  - ESLint 配置

- `frontend/Dockerfile`
  - 前端容器构建文件

### 前端职责总结

前端主要负责：

- 接收用户输入
- 展示聊天内容
- 调用后端生成任务计划
- 用 DAG 图展示任务节点
- 触发节点执行
- 实时展示 SSE 日志和结果
- 展示 PDF、分析报告、代码和生成图片

---

## 3. 哪些是后端部分

如果按“业务后端”来划分，后端主要包括 `backend/`，以及为后端提供能力支撑的 `ai-services/` 和 `docker-sandbox/`。

### 3.1 核心业务后端：`backend/`

这是主后端服务，监听 `:8080`。

#### 入口

- `backend/cmd/api/main.go`
  - Gin 服务入口
  - 注册健康检查和 API 路由

#### API 层

- `backend/internal/api/routes.go`
  - 定义后端对外 HTTP 接口
  - 核心接口包括：
    - `/api/chat`
    - `/api/plan`
    - `/api/execute`
    - `/api/pdf-proxy`

#### 调度与规划层

- `backend/internal/planner/planner.go`
  - 根据用户意图生成任务计划
  - 计划结果会被前端渲染为 DAG

#### Agent 层

- `backend/internal/agent/chat.go`
  - 聊天问答 Agent

- `backend/internal/agent/coder.go`
  - 代码生成与执行 Agent
  - 会调用大模型生成代码，再调用沙箱执行

- `backend/internal/agent/librarian.go`
  - 文献/资料相关 Agent

- `backend/internal/agent/data.go`
  - 数据分析/结果报告相关 Agent

#### 模型层

- `backend/internal/models/task.go`
  - 任务结构定义

#### 沙箱客户端

- `backend/internal/sandbox/opensandbox.go`
  - 后端访问沙箱服务的客户端封装

#### 配置与构建

- `backend/go.mod`
- `backend/go.sum`
- `backend/Dockerfile`

### 3.2 AI 微服务后端：`ai-services/`

这个目录下是 Python 微服务，目前已实现意图识别服务。

- `ai-services/intent_recognition/main.py`
  - FastAPI 服务
  - 提供 `/predict` 和 `/health`
  - 当前是基于关键词的 mock 逻辑，未来可以接 BERT/分类模型

- `ai-services/intent_recognition/requirements.txt`
  - Python 依赖

这一部分更适合归类为“辅助后端服务”或“AI 能力服务”。

### 3.3 执行沙箱后端：`docker-sandbox/`

这是一个独立 Go 服务，监听 `:8082`，专门为 `backend` 提供隔离执行能力。

#### 入口

- `docker-sandbox/main.go`
  - 暴露沙箱 API：
    - 创建沙箱
    - 删除沙箱
    - 执行 Python
    - 执行命令

#### 执行引擎

- `docker-sandbox/internal/engine/docker.go`
  - 原生 Docker 执行引擎

- `docker-sandbox/internal/engine/opensandbox.go`
  - OpenSandbox 执行引擎

- `docker-sandbox/internal/engine/interface.go`
  - 引擎接口定义

#### 配置与构建

- `docker-sandbox/go.mod`
- `docker-sandbox/go.sum`
- `docker-sandbox/Dockerfile`

这一部分也属于后端，但它不是业务 API 层，而是“基础设施型后端服务”。

---

## 4. 前后端边界怎么理解

### 前端

只负责界面、交互、状态展示，不直接做任务规划和代码执行。

### 后端

负责真正的业务处理和执行链路，包括：

- 接收前端请求
- 分析用户意图
- 生成任务计划
- 调度不同 Agent
- 调用大模型
- 调用沙箱执行代码
- 将日志和结果通过 SSE 回传前端

---

## 5. 模块调用链

典型调用链如下：

1. 用户在 `frontend` 输入请求
2. `frontend` 调用 `backend` 的 `/api/chat` 或 `/api/plan`
3. `backend` 生成任务计划或直接回答
4. 如果执行任务，`backend` 调用 Agent
5. `coder agent` 等 Agent 再调用 `docker-sandbox`
6. `docker-sandbox` 在隔离环境里执行代码
7. 执行结果返回 `backend`
8. `backend` 通过 SSE 把日志、结果、代码、图片推给 `frontend`

如果后续启用更完整的 NLP 能力，`backend` 也可以进一步调用 `ai-services`

---

## 6. 一句话分类

### 前端部分

- `frontend/`

### 后端部分

- 主业务后端：`backend/`
- AI 微服务后端：`ai-services/`
- 沙箱执行后端：`docker-sandbox/`

---

## 7. 后续查看建议

后面如果要快速定位代码，可以直接按下面方式查：

- 看界面和交互：先看 `frontend/src/App.tsx`
- 看 API 和请求入口：先看 `backend/internal/api/routes.go`
- 看任务规划：先看 `backend/internal/planner/planner.go`
- 看代码生成执行：先看 `backend/internal/agent/coder.go`
- 看沙箱执行：先看 `docker-sandbox/main.go`
- 看 AI 意图识别：先看 `ai-services/intent_recognition/main.py`

如果是专门查看“规划与调度后端”，优先看：

- `docs/backend_planner_models_reference.md`

---

## 8. 当前结论

这个项目不是单纯的“前后端两层结构”，而是：

- 一个 React 前端
- 一个 Go 主后端
- 一个 Python AI 微服务
- 一个 Go 沙箱执行服务

如果后面需要继续细分，我可以基于这份文档继续补：

- 接口清单版
- 调用链时序版
- 目录树版
- Agent 职责拆解版
