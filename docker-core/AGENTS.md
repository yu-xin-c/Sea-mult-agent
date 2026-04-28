# AGENTS.md

## 开发环境提示 (Dev environment tips)
- **架构边界严格限制**：
  - `sandbox` 模块定位：**禁止**在此处编写任何智能体（Agent）逻辑。它必须仅作为一个简单的 API 封装对象，负责封装底层的 Docker 和 gVisor，管理容器生命周期，并确保在销毁（Destroy/Close）时彻底释放所有的 Docker 资源。
  - `checkpoint` 模块定位：作为建立在 `sandbox` 对象之上的一层封装对象，仅提供状态保存（Commit）和环境回滚（Rollback）的操作接口方法。
  - **智能体分布**：`Planner`（规划者）和 `Executor`（执行者）智能体的核心逻辑由专门的 agent 模块负责调度。而 `Checker`（校验者）和 `Watchdog`（看门狗）智能体应实现在 `watchdog` 模块中，利用该模块去调用 `checkpoint` 的回滚操作以及执行断路器等防御性行为。
- 使用 `go mod tidy` 来管理依赖，确保工作区内的引用清晰。
- 若要修改或查看特定模块（如 `sandbox`），直接进入对应包目录即可，保持 Go 包结构的独立性。
- 在与底层 Docker API 交互时，务必正确传递和处理 `context.Context`，以防止协程泄漏或孤儿容器的产生。

## 测试说明 (Testing instructions)
- 在进行相关模块测试前，请确保开发环境或 CI 环境中的 Docker 守护进程正常运行，且拥有访问权限。
- 运行 `go test ./sandbox/...` 来专门测试沙箱的生命周期管理，必须确保容器启动和销毁后没有任何资源泄漏（如僵尸容器、未释放的网络端口）。
- 在项目根目录可以运行 `go test ./...` 测试全量代码。在合并（Merge）代码前，提交必须通过所有测试。
- 若要专注于某一个特定的测试用例，请添加对应的匹配模式：`go test -run "<TestName>" ./...`。
- 不断修复任何测试失败或类型报错，直到整个测试套件亮起绿灯。
- 在移动文件或更改包引用路径后，请运行 `golangci-lint run`（或项目规定的 lint 工具）确保 Go 代码风格规范和类型安全依然通过。
- 为你修改的代码（特别是 `watchdog` 中复杂的竞速、超时或回滚逻辑）添加或更新测试用例，即便审查者没有明确要求。

## PR 说明 (PR instructions)
- 标题格式：`[<模块名称>] <标题>` （例如：`[sandbox] 优化容器销毁时的资源释放逻辑` 或 `[watchdog] 引入 Checker 智能体回滚策略`）
- 在提交 commit 之前，始终在本地运行代码格式化（如 `go fmt ./...`）、Lint 检查以及 `go test ./...` 确保代码质量。