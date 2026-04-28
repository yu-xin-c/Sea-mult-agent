# 本地启动说明

当前项目的后端主链路已经默认依赖 `docker-sandbox` 服务。

也就是说，如果你要跑通真实的 `Code_Execution` 沙箱执行链路，需要启动三部分：

- `docker-sandbox`
- `backend`
- `frontend`

---

## 1. 准备环境变量

先在项目根目录复制统一环境模板：

- Windows `cmd`
```bat
copy backend.env.example backend.env
```

- PowerShell
```powershell
Copy-Item .\backend.env.example .\backend.env
```

- Linux / macOS
```sh
cp ./backend.env.example ./backend.env
```

然后编辑：

- [backend.env](/D:/mygo/Sea-mult-agent/scholar-agent/backend.env)

最少需要确认这些变量：

- `OPENAI_API_KEY`
- `OPENAI_BASE_URL`
- `OPENAI_MODEL_NAME`
- `SANDBOX_URL`

示例：

```dotenv
OPENAI_API_KEY=your-api-key
OPENAI_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
OPENAI_MODEL_NAME=qwen3-coder-plus
SANDBOX_URL=http://localhost:8082
OPEN_SANDBOX_URL=http://localhost:8081
```

模板文件：

- [backend.env.example](/D:/mygo/Sea-mult-agent/scholar-agent/backend.env.example)

兼容说明：

- `scripts/windows/*.ps1` 现在会优先读取 `backend.env`
- 若仓库里仍保留 [backend.env.ps1](/D:/mygo/Sea-mult-agent/scholar-agent/backend.env.ps1)，PowerShell 启动脚本也会继续兼容它
- Linux / macOS / `cmd` 统一只读取 `backend.env`

---

## 2. 启动沙箱服务

在项目根目录执行：

- Windows `cmd`
```bat
scripts\windows\start-sandbox.cmd
```

- PowerShell
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\windows\start-sandbox.ps1
```

- Linux / macOS
```sh
./scripts/unix/start-sandbox.sh
```

脚本位置：

- [scripts/windows/start-sandbox.ps1](/D:/mygo/Sea-mult-agent/scholar-agent/scripts/windows/start-sandbox.ps1)

这个脚本会：

- 使用项目内本地 `GOCACHE`
- 设置 `OPEN_SANDBOX_URL`
- 进入 `docker-sandbox/`
- 执行 `go run main.go`

启动后可检查：

- [http://localhost:8082/api/v1/sandboxes](http://localhost:8082/api/v1/sandboxes)

---

## 3. 启动后端

在项目根目录执行：

- Windows `cmd`
```bat
scripts\windows\start-backend.cmd
```

- PowerShell
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\windows\start-backend.ps1
```

- Linux / macOS
```sh
./scripts/unix/start-backend.sh
```

脚本位置：

- [scripts/windows/start-backend.ps1](/D:/mygo/Sea-mult-agent/scholar-agent/scripts/windows/start-backend.ps1)

这个脚本会：

- 读取 `backend.env`
- 使用项目内本地 `GOCACHE`
- 进入 `backend/`
- 执行 `go run cmd/api/main.go`

健康检查：

- [http://localhost:8080/ping](http://localhost:8080/ping)

---

## 4. 启动前端

在项目根目录执行：

- Windows `cmd`
```bat
scripts\windows\start-frontend.cmd
```

- PowerShell
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\windows\start-frontend.ps1
```

- Linux / macOS
```sh
./scripts/unix/start-frontend.sh
```

脚本位置：

- [scripts/windows/start-frontend.ps1](/D:/mygo/Sea-mult-agent/scholar-agent/scripts/windows/start-frontend.ps1)

默认访问地址：

- [http://localhost:5173](http://localhost:5173)

---

## 5. 端到端验证顺序

推荐按下面顺序联调：

1. 启动 `docker-sandbox`
2. 启动 `backend`
3. 启动 `frontend`
4. 调用 `POST /api/plan`
5. 调用 `POST /api/plans/:id/execute`
6. 用 `GET /api/plans/:id/stream` 观察事件流
7. 用 `GET /api/plans/:id/events` 回放历史事件

---

## 6. 当前已验证通过的能力

当前已经验证通过：

- `plan_graph` 生成
- 计划级执行调度
- 计划级 SSE 事件流
- Docker 沙箱创建与销毁
- `Code_Execution` 主链路中的真实代码执行

如果只启动 `backend` 而不启动 `docker-sandbox`，则：

- 普通非沙箱链路仍可能工作
- 但真实代码执行类节点会失败或无法完成

---

## 7. 常用检查点

如果链路跑不通，优先检查下面几个点：

1. Docker Desktop 是否已启动
2. `SANDBOX_URL` 是否指向 `http://localhost:8082`
3. `http://localhost:8082/api/v1/sandboxes` 是否可访问
4. `http://localhost:8080/ping` 是否返回正常
5. `backend.env` 中模型配置是否有效
