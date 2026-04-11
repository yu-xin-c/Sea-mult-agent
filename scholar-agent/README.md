# ScholarAgent

ScholarAgent 是一个面向科研推演与代码执行场景的多智能体平台。当前仓库包含：

- `frontend/`：React + Vite 前端界面
- `backend/`：Go 后端调度与多智能体执行链路
- `docker-sandbox/`：Go 编写的 Docker 安全执行沙箱
- `ai-services/`：预留的 Python AI 微服务能力
- `docs/`：架构、模块设计与本地调试文档

当前本地主链路默认依赖三个服务：

1. `docker-sandbox`
2. `backend`
3. `frontend`

`ai-services/intent_recognition` 目前是预留模块，不是跑通主链路的必需服务。

## 技术栈

- Frontend: React 19, TypeScript, Vite, ESLint
- Backend: Go 1.26, Gin, Eino, Docker SDK
- Sandbox: Go 1.26, Gin, Docker SDK
- AI Services: Python, FastAPI

## 目录结构

```text
.
├─ frontend/          # 前端应用
├─ backend/           # 调度后端
├─ docker-sandbox/    # 代码执行沙箱
├─ ai-services/       # 预留 AI 微服务
├─ docs/              # 设计文档与补充说明
├─ scripts/           # 按平台拆分的本地启动与 Docker 封装脚本
├─ docker-compose.yml # 容器化联调入口
└─ Makefile           # 项目命令入口
```

## 环境依赖

开始前请先准备：

- Go `1.26+`
- Node.js `20+`
- npm `10+`
- Docker Desktop / Docker Engine
- 可用的 OpenAI 兼容模型服务

如果只跑预留的 Python 微服务，还需要：

- Python `3.10+`

## 环境变量

项目根目录统一使用 `backend.env` 作为本地运行配置文件。

1. 复制模板

```powershell
Copy-Item .\backend.env.example .\backend.env
```

或：

```sh
cp ./backend.env.example ./backend.env
```

2. 至少确认以下配置：

```dotenv
OPENAI_API_KEY=your-api-key
OPENAI_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
OPENAI_MODEL_NAME=qwen3-coder-plus
SANDBOX_URL=http://localhost:8082
OPEN_SANDBOX_URL=http://localhost:8081
ENABLE_OPENSANDBOX_FALLBACK=false
```

说明：

- `scripts/windows/start-backend.*` 会读取 `backend.env`
- `scripts/windows/start-sandbox.*` 会读取 `backend.env`
- `scripts/unix/start-backend.sh` 会读取 `backend.env`
- `scripts/unix/start-sandbox.sh` 会读取 `backend.env`
- `docker compose` 也会复用同一个 `backend.env`
- `backend.env.ps1` 仅作为 PowerShell 兼容方案保留，不推荐作为主配置入口

## 快速开始

### 方式一：本地脚本启动，推荐日常开发使用

先安装依赖：

```powershell
cd .\frontend
npm install

cd ..\backend
go mod tidy

cd ..\docker-sandbox
go mod tidy
```

然后在项目根目录分别启动三个服务。

PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\windows\start-sandbox.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\windows\start-backend.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\windows\start-frontend.ps1
```

Windows `cmd`:

```bat
scripts\windows\start-sandbox.cmd
scripts\windows\start-backend.cmd
scripts\windows\start-frontend.cmd
```

Linux / macOS:

```sh
./scripts/unix/start-sandbox.sh
./scripts/unix/start-backend.sh
./scripts/unix/start-frontend.sh
```

默认访问地址：

- Frontend: [http://localhost:5173](http://localhost:5173)
- Backend: [http://localhost:8080/ping](http://localhost:8080/ping)
- Sandbox: [http://localhost:8082/api/v1/sandboxes](http://localhost:8082/api/v1/sandboxes)

### 方式二：Docker Compose 联调

复制好 `backend.env` 后，在项目根目录执行：

```sh
./scripts/unix/docker-up.sh
```

停止：

```sh
./scripts/unix/docker-down.sh
```

适合联调和演示，不适合频繁修改源码后的快速热更新。

### 方式三：使用 Makefile，适合 Unix 风格环境

```sh
make install
make lint
make build
```

如果需要，也可以直接通过 `make run-backend`、`make run-sandbox`、`make run-frontend` 启动对应服务。

## 常用脚本与命令

`scripts/` 目录脚本：

- Windows:
`scripts/windows/start-backend.ps1` / `scripts/windows/start-backend.cmd`
`scripts/windows/start-sandbox.ps1` / `scripts/windows/start-sandbox.cmd`
`scripts/windows/start-frontend.ps1` / `scripts/windows/start-frontend.cmd`
`scripts/windows/docker-up.ps1` / `scripts/windows/docker-up.cmd`
`scripts/windows/docker-down.ps1` / `scripts/windows/docker-down.cmd`
- Linux / macOS:
`scripts/unix/start-backend.sh`
`scripts/unix/start-sandbox.sh`
`scripts/unix/start-frontend.sh`
`scripts/unix/docker-up.sh`
`scripts/unix/docker-down.sh`

这些脚本已经统一处理了：

- 根目录定位
- `backend.env` 加载
- 项目内 `.gocache` 使用
- 本地代理变量清空

`make` 常用目标：

- `make install`：安装前端依赖并整理 Go 模块
- `make tidy`：仅执行 Go 模块整理
- `make lint`：执行前端 ESLint
- `make build`：构建 frontend、backend、docker-sandbox
- `make clean`：清理仓库内构建产物
- `make docker-up`：通过 `scripts/unix/` 启动 compose
- `make docker-down`：通过 `scripts/unix/` 停止 compose

## 依赖说明

### Frontend

依赖文件：

- `frontend/package.json`
- `frontend/package-lock.json`

安装：

```sh
cd frontend && npm install
```

### Backend

依赖文件：

- `backend/go.mod`
- `backend/go.sum`

安装：

```sh
cd backend && go mod tidy
```

### Docker Sandbox

依赖文件：

- `docker-sandbox/go.mod`
- `docker-sandbox/go.sum`

安装：

```sh
cd docker-sandbox && go mod tidy
```

### AI Services

依赖文件：

- `ai-services/intent_recognition/requirements.txt`

安装：

```sh
cd ai-services/intent_recognition && pip install -r requirements.txt
```

说明：该模块当前不是主链路必需依赖，按需安装即可。

## 联调建议

建议按这个顺序启动和验证：

1. 启动 `docker-sandbox`
2. 启动 `backend`
3. 启动 `frontend`
4. 调用 `POST /api/plan`
5. 调用 `POST /api/plans/:id/execute`
6. 通过 `GET /api/plans/:id/stream` 观察 SSE 事件

补充说明见：

- [docs/README.md](./docs/README.md)
- [docs/local_startup_guide.md](./docs/local_startup_guide.md)

## 提交 PR 前检查

建议至少执行：

```sh
cd frontend && npm run lint
cd frontend && npm run build
cd backend && go test ./...
cd docker-sandbox && go test ./...
```

如果使用 Docker 联调，再补一次：

```sh
./scripts/unix/docker-up.sh
```

## 不应提交的内容

以下内容默认不应进入 PR：

- 本地环境文件，如 `backend.env`
- 本地缓存，如 `.gocache/`
- 运行日志，如 `logs/`
- 二进制产物，如 `backend/api.exe`
- 编辑过程中生成的临时备份文件，如 `*.go.<随机数字>`

这些规则已经写入根目录 `.gitignore`。
