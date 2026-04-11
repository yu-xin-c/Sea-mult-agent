package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/sandbox"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultSandboxImage  = "python:3.9-bullseye"
	defaultPipTimeoutSec = "60"
)

// Agent 接口定义了系统中自治工作者的标准行为
type Agent interface {
	// ExecuteTask 执行分配给该 Agent 的具体任务
	ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error
}

// logToContext 辅助函数，将日志同时输出到控制台和 Context 中的 Channel，以支持 SSE 流式返回
func logToContext(ctx context.Context, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Println(msg)
	if ch, ok := ctx.Value("logChannel").(chan string); ok {
		// 非阻塞发送
		select {
		case ch <- msg:
		default:
		}
	}
}

// CoderAgent 负责根据需求生成代码，并在沙箱中迭代测试
type CoderAgent struct {
	Name          string
	SystemPrompt  string
	Sandbox       *sandbox.SandboxClient
	CodeOnlyChain compose.Runnable[string, string]
	EinoChain     compose.Runnable[string, string] // 使用 Eino 编排的执行链
}

// NewCoderAgent 实例化一个新的 CoderAgent，并初始化真实的 Eino 执行链
func NewCoderAgent(sandbox *sandbox.SandboxClient) *CoderAgent {
	agent := &CoderAgent{
		Name: "coder_agent",
		SystemPrompt: `你是一个资深的 AI 科研助理和 Python 开发者。你的任务是复现学术论文的开源代码库并根据用户需求生成适配代码。
		请严格遵循以下规则：
		1. 你必须只输出有效的 Python 代码，不要包含 any markdown 格式（如 ` + "```" + `python）或解释，以便能够直接执行。
		2. 【极其重要】：你的代码将在纯净的 Docker 沙箱(python:3.9-bullseye)中运行，里面没有 torch, numpy, pandas, matplotlib 等第三方库。如果你需要使用 any 第三方库，**必须在 import 之前使用 subprocess 安装它们**。
		   这是正确的做法示例：
		   import subprocess
		   import sys
		   subprocess.check_call([sys.executable, "-m", "pip", "install", "torch", "numpy", "matplotlib"])
		   import torch
		   import numpy
		   import matplotlib
		   matplotlib.use('Agg') # 必须使用 Agg 后端，因为沙箱没有显示器
		   import matplotlib.pyplot as plt
		3. 【绘图规则】：如果用户要求绘图（使用 matplotlib 等），**绝对不能调用 plt.show()**。你必须使用 **plt.savefig('/workspace/output_plot.png')** 将图像保存到指定路径。
		4. 【核心复现规则 - 适配器模式】：绝对不去大面积修改原论文的核心代码（如 model.py），因为这容易破坏模型结构。
		5. 请编写一个新的独立运行脚本。在这个新脚本中：
		   - 如果涉及机器学习模型训练，必须生成完整的训练循环，并在最后打印出评估结果。
		   - 确保代码在没有外部网络依赖数据集的情况下也能运行（例如生成随机数据作为 Dummy Dataset 来测试网络跑通）。`,
		Sandbox: sandbox,
	}
	agent.SystemPrompt = `你是一名资深的 AI 科研助理和 Python 开发者。你的任务是根据用户需求生成、改写或检查代码。

请严格遵守以下规则：
1. 只输出有效的代码内容，不要附带 Markdown 代码块包裹或额外说明。
2. 如果任务只是代码生成、静态检查、改写或补全，不要主动假设必须运行代码。
3. 只有在任务明确进入沙箱执行阶段时，才依赖第三方库安装、运行环境和绘图输出。
4. 如果用户要求画图，默认将图像保存到约定路径，而不是调用交互式显示。`

	// 初始化真实的 Eino 编排链 (Real LLM -> Sandbox Execution)
	agent.SystemPrompt += "\n7. 代码必须兼容 Python 3.9，禁止使用 Python 3.10+ 语法，例如 match/case、except*、以及 X | Y 类型联合语法；请改用 typing.Optional 或 typing.Union。\n8. 生成依赖框架代码时，优先选择 Python 3.9 可用且稳定的 API，避免依赖只在更新解释器下可运行的新特性。"
	agent.initRealEinoChain()

	return agent
}

// initRealEinoChain 使用字节跳动 Eino 框架和真实的 LLM 模型编排逻辑流
func (a *CoderAgent) initRealEinoChain() {
	// 1. 初始化真实的 LLM ChatModel
	// 使用用户提供的 DeepSeek API Key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is not set")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}

	modelName := os.Getenv("OPENAI_MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
	})
	if err != nil {
		log.Fatalf("初始化大模型失败: %v", err)
	}

	// 2. 构建 Eino Graph
	codeOnlyGraph := compose.NewGraph[string, string]()
	codeOnlyGraph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装代码生成提示词", a.Name)
		messages := []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: fmt.Sprintf("请完成以下任务：\n%s", input)},
		}
		return messages, nil
	}))
	codeOnlyGraph.AddChatModelNode("LLM_Generate_Code", chatModel)
	codeOnlyGraph.AddLambdaNode("Code_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Code_Extractor]: 开始提取大模型生成的代码", a.Name)
		code := msg.Content
		code = strings.TrimPrefix(code, "```python\n")
		code = strings.TrimPrefix(code, "```python")
		code = strings.TrimSuffix(code, "```")
		logToContext(ctx, "[%s] 成功提取到可执行代码", a.Name)
		if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
			codeChan <- code
		}
		return code, nil
	}))
	codeOnlyGraph.AddEdge(compose.START, "Prompt_Builder")
	codeOnlyGraph.AddEdge("Prompt_Builder", "LLM_Generate_Code")
	codeOnlyGraph.AddEdge("LLM_Generate_Code", "Code_Extractor")
	codeOnlyGraph.AddEdge("Code_Extractor", compose.END)

	codeOnlyRunnable, err := codeOnlyGraph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino CodeOnly 链失败: %v", err)
	}
	a.CodeOnlyChain = codeOnlyRunnable

	graph := compose.NewGraph[string, string]()

	// 节点 1: 提示词模板 (Prompt Template)
	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装提示词", a.Name)
		messages := []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: fmt.Sprintf("请完成以下任务：\n%s", input)},
		}
		return messages, nil
	}))

	// 节点 2: LLM 生成代码 (调用真实的 ChatModel)
	graph.AddChatModelNode("LLM_Generate_Code", chatModel)

	// 节点 3: 清洗并提取代码
	graph.AddLambdaNode("Code_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Code_Extractor]: 开始提取大模型生成的代码", a.Name)
		code := msg.Content
		// 简单清洗可能附带的 Markdown 标记
		code = strings.TrimPrefix(code, "```python\n")
		code = strings.TrimPrefix(code, "```python")
		code = strings.TrimSuffix(code, "```")
		logToContext(ctx, "[%s] 成功提取到可执行代码", a.Name)

		// 保存代码到 Context，以便外部获取
		if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
			codeChan <- code
		}

		return code, nil
	}))

	// 节点 4: 沙箱执行与自愈循环 (Self-Correction Loop)
	graph.AddLambdaNode("Sandbox_Execute", compose.InvokableLambda(func(ctx context.Context, code string) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Sandbox_Execute]: 准备将代码送入持久化 Docker 沙箱执行", a.Name)
		if a.Sandbox == nil {
			logToContext(ctx, "[Warning] 沙箱未初始化，跳过实际执行")
			return "【由于本地未安装或未启动 Docker Desktop，跳过沙箱执行环节】\n\n大模型生成的代码如下：\n\n" + code, nil
		}

		// 获取预先创建好的长生命周期容器 ID
		containerID, ok := ctx.Value("containerID").(string)
		if !ok || containerID == "" {
			logToContext(ctx, "[Warning] 无法获取长生命周期容器 ID，降级为单次执行模式")
			// 创建临时沙箱执行
			tempID, err := a.Sandbox.CreatePersistentSandbox(ctx, "temp", defaultSandboxImage, "")
			if err != nil {
				return "", fmt.Errorf("创建临时沙箱失败: %w", err)
			}
			defer a.Sandbox.CleanupSandbox(context.Background(), tempID)

			res, err := a.Sandbox.RunPythonCode(ctx, tempID, code)
			if err != nil {
				return "", fmt.Errorf("沙箱执行失败: %w", err)
			}

			// 处理图片输出
			if len(res.Images) > 0 {
				logToContext(ctx, "[%s] 检测到代码生成了 %d 张图表，正在推送至前端...", a.Name, len(res.Images))
				if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
					for _, imgBase64 := range res.Images {
						// 通过特殊的标识符发送图片给前端
						codeChan <- "IMAGE:" + imgBase64
					}
				}
			}

			return res.Stdout + "\n" + res.Stderr, nil
		}

		maxRetries := 3
		currentCode := code
		var finalOutput string

		for i := 0; i < maxRetries; i++ {
			logToContext(ctx, "[%s] 开始在持久化容器中执行代码 (第 %d/%d 次)...", a.Name, i+1, maxRetries)

			// 改进执行逻辑：直接使用 RunPythonCode 以便沙箱能够拦截并返回图表
			res, err := a.Sandbox.RunPythonCode(ctx, containerID, currentCode)

			var output string
			if res != nil {
				output = res.Stdout + "\n" + res.Stderr
				// 处理图片输出
				if len(res.Images) > 0 {
					logToContext(ctx, "[%s] 检测到代码生成了 %d 张图表，正在推送至前端...", a.Name, len(res.Images))
					if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
						for _, imgBase64 := range res.Images {
							codeChan <- "IMAGE:" + imgBase64
						}
					}
				}
			}

			if err != nil || (res != nil && res.ExitCode != 0) {
				if err == nil {
					err = fmt.Errorf("exit code: %d", res.ExitCode)
				}
				logToContext(ctx, "[%s] 代码执行失败，触发 Self-Correction 机制。错误: %v", a.Name, err)

				// 构建修正 Prompt 再次调用大模型
				correctionPrompt := fmt.Sprintf("你之前生成的代码在沙箱中执行失败了。\n错误日志如下：\n%v\n输出信息：\n%s\n\n【重要提示】如果是 ModuleNotFoundError（比如 No module named 'torch'），请务必在代码最开头加上 `import subprocess; import sys; subprocess.check_call([sys.executable, \"-m\", \"pip\", \"install\", \"torch\"])` （替换为缺失的库名）。\n请分析错误原因，并直接返回修复后的完整 Python 代码（不要包含 markdown 格式）。", err, output)

				logToContext(ctx, "[%s] 正在调用大模型进行代码自修复...", a.Name)
				msg, err := chatModel.Generate(ctx, []*schema.Message{
					{Role: schema.System, Content: a.SystemPrompt},
					{Role: schema.User, Content: correctionPrompt},
				})
				if err != nil {
					logToContext(ctx, "[%s] 错误: 自修复调用大模型失败: %v", a.Name, err)
					return "", fmt.Errorf("Self-Correction 调用大模型失败: %w", err)
				}

				currentCode = strings.TrimPrefix(msg.Content, "```python\n")
				currentCode = strings.TrimPrefix(currentCode, "```python")
				currentCode = strings.TrimSuffix(currentCode, "```")

				finalOutput = fmt.Sprintf("执行失败，已尝试修复。错误日志:\n%v\n输出:\n%s", err, output)
				continue // 尝试用新代码再次运行
			}

			// 成功执行
			logToContext(ctx, "[%s] 持久化沙箱执行成功！获取到执行结果。", a.Name)
			return output, nil
		}

		logToContext(ctx, "[%s] 达到最大重试次数，任务执行失败", a.Name)
		return finalOutput + "\n\n【达到最大重试次数，任务执行失败】", fmt.Errorf("达到最大重试次数")
	}))

	// 3. 定义边 (Edges) 将节点串联起来
	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Generate_Code")
	graph.AddEdge("LLM_Generate_Code", "Code_Extractor")
	graph.AddEdge("Code_Extractor", "Sandbox_Execute")
	graph.AddEdge("Sandbox_Execute", compose.END)

	// 4. 编译成 Runnable 链
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
	log.Printf("[%s] 成功初始化基于真实大模型的 Eino 执行链", a.Name)
}

// initMockEinoChain 保留作为兜底的 Mock 链
func (a *CoderAgent) initMockEinoChain() {
	graph := compose.NewGraph[string, string]()
	graph.AddLambdaNode("LLM_Generate_Code", compose.InvokableLambda(func(ctx context.Context, input string) (string, error) {
		return a.mockLLMGenerateCode(input), nil
	}))
	graph.AddLambdaNode("Sandbox_Execute", compose.InvokableLambda(func(ctx context.Context, code string) (string, error) {
		if a.Sandbox == nil {
			return code, nil
		}
		// 临时执行环境
		tempID, _ := a.Sandbox.CreatePersistentSandbox(ctx, "mock", defaultSandboxImage, "")
		defer a.Sandbox.CleanupSandbox(context.Background(), tempID)

		// 同样写入文件执行，提高成功率
		tempDir, _ := os.MkdirTemp("", "scholar_workspace_mock_")
		defer os.RemoveAll(tempDir)
		scriptPath := filepath.Join(tempDir, "run_script.py")
		_ = os.MkdirAll(filepath.Dir(scriptPath), 0777)
		_ = os.WriteFile(scriptPath, []byte(code), 0666)

		res, _ := a.Sandbox.RunPythonCode(ctx, tempID, code)
		return res.Stdout + res.Stderr, nil
	}))
	graph.AddEdge(compose.START, "LLM_Generate_Code")
	graph.AddEdge("LLM_Generate_Code", "Sandbox_Execute")
	graph.AddEdge("Sandbox_Execute", compose.END)
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}
	a.EinoChain = runnable
}

// ExecuteTask 使用 Eino Chain 执行任务
func (a *CoderAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task != nil && task.Type == "resolve_dependencies" {
		return a.resolveDependenciesTask(ctx, task)
	}
	if task != nil && task.AssignedTo == "sandbox_agent" && shouldUseDeterministicSandboxPath(task) {
		return a.executeSandboxTask(ctx, task)
	}
	log.Printf("[%s] 开始执行任务: %s (使用 Eino 驱动)", a.Name, task.Name)

	codeChan := make(chan string, 10)
	ctx = context.WithValue(ctx, "codeChannel", codeChan)

	// 在后台收集生成的代码和图片
	go func() {
		for msg := range codeChan {
			if strings.HasPrefix(msg, "IMAGE:") {
				task.ImageBase64 = strings.TrimPrefix(msg, "IMAGE:")
			} else {
				task.Code = msg
			}
		}
	}()

	// 通过 Eino 链运行任务描述
	selectedChain := a.CodeOnlyChain
	usesSandbox := task.AssignedTo == "sandbox_agent"
	if usesSandbox || selectedChain == nil {
		selectedChain = a.EinoChain
	}
	if usesSandbox {
		logToContext(ctx, "[%s] 当前节点为 sandbox_agent，执行时才会尝试创建或复用沙箱容器", a.Name)
	} else {
		logToContext(ctx, "[%s] 当前节点为 %s，仅执行代码生成/检查链路，不触发沙箱创建", a.Name, task.AssignedTo)
	}
	output, err := selectedChain.Invoke(ctx, task.Description)
	close(codeChan) // 关闭通道让 goroutine 退出

	if err != nil {
		log.Printf("[%s] Eino 执行流失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("执行失败: %v", err)
		return err
	}

	log.Printf("[%s] Eino 执行流完成. 最终输出:\n%s", a.Name, output)
	task.Result = output
	task.Status = models.StatusCompleted
	return nil
}

// mockLLMGenerateCode 模拟大模型根据提示词生成 Python 代码
func (a *CoderAgent) executeSandboxTask(ctx context.Context, task *models.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if a.Sandbox == nil {
		task.Status = models.StatusFailed
		task.Error = "sandbox client is not configured"
		return fmt.Errorf("%s", task.Error)
	}

	switch inferSandboxTaskKind(task) {
	case "prepare_runtime":
		return a.prepareRuntime(ctx, task)
	case "install_dependencies":
		return a.installDependencies(ctx, task)
	case "execute_code":
		return a.executeCodeInSandbox(ctx, task)
	default:
		return a.executeCodeInSandbox(ctx, task)
	}
}

func (a *CoderAgent) prepareRuntime(ctx context.Context, task *models.Task) error {
	logToContext(ctx, "[%s] Preparing runtime for %s", a.Name, task.Name)

	if existing := chooseNonEmpty(extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"), extractTaskInputLike(task, "prepared_runtime")); strings.HasPrefix(existing, "dk-") || strings.HasPrefix(existing, "os-") {
		task.Result = existing
		logToContext(ctx, "[%s] Reusing existing runtime: %s", a.Name, existing)
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = map[string]string{
			"runtime_session": existing,
		}
		task.Status = models.StatusCompleted
		return nil
	}

	sandboxID, err := a.Sandbox.CreatePersistentSandbox(ctx, task.ID, defaultSandboxImage, "")
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("failed to create runtime sandbox: %v", err)
		return err
	}
	task.Result = sandboxID
	logToContext(ctx, "[%s] Runtime ready: %s", a.Name, sandboxID)
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
		"runtime_session": sandboxID,
	})
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) installDependencies(ctx context.Context, task *models.Task) error {
	runtimeSession := chooseNonEmpty(extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"))
	if strings.TrimSpace(runtimeSession) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing runtime_session input for dependency installation"
		return fmt.Errorf("%s", task.Error)
	}

	rawDependencySpec := extractTaskInputLike(task, "dependency_spec")
	dependencies, parseErr := parseDependencySpec(rawDependencySpec)
	if parseErr != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("invalid dependency_spec: %v", parseErr)
		task.Result = strings.TrimSpace(rawDependencySpec)
		return fmt.Errorf("%s", task.Error)
	}
	if len(dependencies) == 0 {
		task.Result = "no external dependencies detected"
		task.Status = models.StatusCompleted
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
			"prepared_runtime":          runtimeSession,
			"dependency_install_report": "no external dependencies detected",
		})
		return nil
	}

	dependencies = normalizeDependenciesForPython39(dependencies)
	logToContext(ctx, "[%s] Installing dependencies in sandbox %s for Python 3.9: %s", a.Name, runtimeSession, strings.Join(dependencies, ", "))
	cmd := append([]string{"python3", "-m", "pip", "install", "--default-timeout", defaultPipTimeoutSec}, dependencies...)
	res, err := a.Sandbox.ExecCommandStream(ctx, runtimeSession, cmd, func(stream string, line string) {
		logToContext(ctx, "[%s] pip %s: %s", a.Name, stream, line)
	})
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("dependency installation failed: %v", err)
		return err
	}
	if res == nil {
		task.Status = models.StatusFailed
		task.Error = "dependency installation returned nil response"
		return fmt.Errorf("%s", task.Error)
	}
	logToContext(ctx, "[%s] pip install exit_code=%d", a.Name, res.ExitCode)
	if res.ExitCode != 0 {
		task.Status = models.StatusFailed
		task.Result = strings.TrimSpace(res.Stdout)
		task.Error = chooseNonEmpty(strings.TrimSpace(res.Stderr), fmt.Sprintf("pip install exited with code %d", res.ExitCode))
		return fmt.Errorf("%s", task.Error)
	}

	report := strings.TrimSpace(res.Stdout)
	if report == "" {
		report = "dependencies installed successfully"
	}
	task.Result = report
	task.Status = models.StatusCompleted
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
		"prepared_runtime":          runtimeSession,
		"dependency_install_report": report,
	})
	return nil
}

func (a *CoderAgent) executeCodeInSandbox(ctx context.Context, task *models.Task) error {
	code := extractTaskInputLike(task, "generated_code")
	if strings.TrimSpace(code) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing generated_code input for sandbox execution"
		return fmt.Errorf("%s", task.Error)
	}

	runtimeSession := chooseNonEmpty(extractTaskInputLike(task, "prepared_runtime"), extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"))
	sandboxID := runtimeSession
	ownsEphemeralSandbox := false
	if strings.TrimSpace(sandboxID) == "" || (!strings.HasPrefix(sandboxID, "dk-") && !strings.HasPrefix(sandboxID, "os-")) {
		if strings.TrimSpace(runtimeSession) != "" {
			task.Status = models.StatusFailed
			task.Error = "invalid prepared_runtime artifact: expected sandbox id"
			task.Result = strings.TrimSpace(runtimeSession)
			return fmt.Errorf("%s", task.Error)
		}
		image := sandboxID
		if strings.TrimSpace(image) == "" {
			image = defaultSandboxImage
		}
		logToContext(ctx, "[%s] Running %s in sandbox image %s", a.Name, task.Name, image)
		var err error
		sandboxID, err = a.Sandbox.CreatePersistentSandbox(ctx, task.ID, image, "")
		if err != nil {
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("failed to create execution sandbox: %v", err)
			return err
		}
		ownsEphemeralSandbox = true
	}
	if ownsEphemeralSandbox {
		defer a.Sandbox.CleanupSandbox(context.Background(), sandboxID)
	}

	if err := a.validatePythonSyntaxInSandbox(ctx, sandboxID, code); err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("python syntax validation failed: %v", err)
		return err
	}

	res, err := a.Sandbox.RunPythonCodeStream(ctx, sandboxID, code, func(stream string, line string) {
		logToContext(ctx, "[%s] python %s: %s", a.Name, stream, line)
	})
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("sandbox execution failed: %v", err)
		return err
	}
	if res == nil {
		task.Status = models.StatusFailed
		task.Error = "sandbox execution returned nil response"
		return fmt.Errorf("%s", task.Error)
	}
	if res.ExitCode != 0 {
		task.Status = models.StatusFailed
		task.Result = strings.TrimSpace(res.Stdout)
		task.Error = strings.TrimSpace(res.Stderr)
		if task.Error == "" {
			task.Error = fmt.Sprintf("sandbox exited with code %d", res.ExitCode)
		}
		logToContext(ctx, "[%s] sandbox run exit_code=%d", a.Name, res.ExitCode)
		return fmt.Errorf("%s", task.Error)
	}

	logToContext(ctx, "[%s] sandbox run exit_code=%d", a.Name, res.ExitCode)
	if len(res.Images) > 0 {
		logToContext(ctx, "[%s] sandbox produced %d image artifact(s)", a.Name, len(res.Images))
	}
	task.Code = code
	task.Result = strings.TrimSpace(res.Stdout)
	if task.Result == "" {
		task.Result = strings.TrimSpace(res.Stderr)
	}
	if len(res.Images) > 0 {
		task.ImageBase64 = res.Images[0]
	}
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) resolveDependenciesTask(ctx context.Context, task *models.Task) error {
	code := extractTaskInputLike(task, "generated_code")
	if strings.TrimSpace(code) == "" {
		code = task.Code
	}
	if strings.TrimSpace(code) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing generated_code input for dependency resolution"
		return fmt.Errorf("%s", task.Error)
	}

	dependencies := normalizeDependenciesForPython39(detectPythonDependencies(code))
	payload, err := json.Marshal(dependencies)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("failed to marshal dependency spec: %v", err)
		return err
	}

	task.Result = string(payload)
	task.Status = models.StatusCompleted
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"dependency_spec": string(payload),
	}
	logToContext(ctx, "[%s] 识别到依赖: %s", a.Name, chooseNonEmpty(strings.Join(dependencies, ", "), "none"))
	return nil
}

func extractTaskInput(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func extractTaskInputLike(task *models.Task, key string) string {
	if value := extractTaskInput(task, key); strings.TrimSpace(value) != "" {
		return value
	}
	if task == nil || task.Inputs == nil {
		return ""
	}
	for inputKey, value := range task.Inputs {
		if strings.HasSuffix(strings.TrimSpace(inputKey), "_"+key) || strings.EqualFold(strings.TrimSpace(inputKey), key) {
			if stringValue := extractTaskInput(task, inputKey); strings.TrimSpace(stringValue) != "" {
				return stringValue
			}
		}
		if value == nil {
			continue
		}
	}
	return ""
}

func shouldUseDeterministicSandboxPath(task *models.Task) bool {
	if task == nil {
		return false
	}
	if strings.TrimSpace(task.Type) != "" {
		return true
	}
	if strings.TrimSpace(extractTaskInputLike(task, "generated_code")) != "" {
		return true
	}
	if strings.TrimSpace(extractTaskInputLike(task, "dependency_spec")) != "" {
		return true
	}
	if strings.TrimSpace(extractTaskInputLike(task, "runtime_session")) != "" || strings.TrimSpace(extractTaskInputLike(task, "prepared_runtime")) != "" || strings.TrimSpace(extractTaskInputLike(task, "runtime_env")) != "" {
		return true
	}
	return false
}

func inferSandboxTaskKind(task *models.Task) string {
	if task == nil {
		return "execute_code"
	}

	switch strings.ToLower(strings.TrimSpace(task.Type)) {
	case "prepare_runtime", "env_setup", "prepare_environment", "setup_runtime", "test_environment":
		return "prepare_runtime"
	case "install_dependencies", "dependency_install":
		return "install_dependencies"
	case "execute_code", "baseline_run", "run_benchmark":
		return "execute_code"
	}

	context := strings.ToLower(strings.Join([]string{task.Name, task.Description}, " "))
	switch {
	case strings.Contains(context, "prepare_runtime"), strings.Contains(context, "env_setup"), strings.Contains(context, "prepare runtime"), strings.Contains(context, "prepare environment"), strings.Contains(context, "setup runtime"), strings.Contains(context, "test environment"):
		return "prepare_runtime"
	case strings.Contains(context, "install_depend"), strings.Contains(context, "pip install"):
		return "install_dependencies"
	case strings.Contains(context, "execute_code"), strings.Contains(context, "baseline_run"), strings.Contains(context, "run benchmark"), strings.Contains(context, "benchmark"), strings.Contains(context, "execute"):
		return "execute_code"
	default:
		if strings.TrimSpace(extractTaskInputLike(task, "dependency_spec")) != "" && strings.TrimSpace(extractTaskInputLike(task, "runtime_session")) != "" && strings.TrimSpace(extractTaskInputLike(task, "generated_code")) == "" {
			return "install_dependencies"
		}
		if strings.TrimSpace(extractTaskInputLike(task, "generated_code")) == "" {
			return "prepare_runtime"
		}
		return "execute_code"
	}
}

func parseDependencySpec(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var deps []string
	if err := json.Unmarshal([]byte(raw), &deps); err == nil {
		return uniqueDependencies(deps), nil
	}

	if looksLikeRichTextDependencySpec(raw) {
		return nil, fmt.Errorf("expected structured dependency list but received report-like content")
	}

	parts := strings.Split(raw, ",")
	cleaned := uniqueDependencies(parts)
	if len(cleaned) == 0 {
		return nil, nil
	}
	for _, item := range cleaned {
		if !isValidDependencyToken(item) {
			return nil, fmt.Errorf("invalid dependency token %q", item)
		}
	}
	return cleaned, nil
}

func detectPythonDependencies(code string) []string {
	standard := map[string]struct{}{
		"abc": {}, "argparse": {}, "asyncio": {}, "base64": {}, "collections": {}, "contextlib": {}, "copy": {}, "csv": {}, "dataclasses": {}, "datetime": {}, "functools": {},
		"hashlib": {}, "io": {}, "itertools": {}, "json": {}, "logging": {}, "math": {}, "os": {}, "pathlib": {}, "random": {},
		"re": {}, "statistics": {}, "string": {}, "subprocess": {}, "sys": {}, "tempfile": {}, "time": {}, "typing": {}, "uuid": {}, "warnings": {},
	}

	packageMap := map[string]string{
		"cv2":                 "opencv-python",
		"sklearn":             "scikit-learn",
		"PIL":                 "pillow",
		"bs4":                 "beautifulsoup4",
		"yaml":                "pyyaml",
		"llama_index":         "llama-index",
		"langchain_openai":    "langchain-openai",
		"langchain_community": "langchain-community",
		"faiss":               "faiss-cpu",
	}

	deps := make([]string, 0, 8)
	lines := strings.Split(code, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import ") {
			modules := strings.Split(strings.TrimPrefix(trimmed, "import "), ",")
			for _, module := range modules {
				name := strings.Fields(strings.TrimSpace(module))
				if len(name) == 0 {
					continue
				}
				root := strings.Split(name[0], ".")[0]
				if _, ok := standard[root]; ok {
					continue
				}
				if mapped, ok := packageMap[root]; ok {
					deps = append(deps, mapped)
				} else {
					deps = append(deps, root)
				}
			}
		}
		if strings.HasPrefix(trimmed, "from ") {
			parts := strings.Fields(trimmed)
			if len(parts) < 2 {
				continue
			}
			root := strings.Split(parts[1], ".")[0]
			if _, ok := standard[root]; ok {
				continue
			}
			if mapped, ok := packageMap[root]; ok {
				deps = append(deps, mapped)
			} else {
				deps = append(deps, root)
			}
		}
	}

	return filterStandardLibraryDependencies(uniqueDependencies(deps))
}

func uniqueDependencies(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.Trim(item, `"'`))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizeDependenciesForPython39(items []string) []string {
	normalized := make([]string, 0, len(items)+1)
	seen := map[string]struct{}{}
	appendOnce := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	for _, item := range filterStandardLibraryDependencies(items) {
		name := strings.TrimSpace(item)
		switch strings.ToLower(name) {
		case "llama-index":
			appendOnce("llama-index<0.12")
			appendOnce("pydantic<2.10")
		default:
			appendOnce(name)
		}
	}
	return normalized
}

func filterStandardLibraryDependencies(items []string) []string {
	standard := map[string]struct{}{
		"abc": {}, "argparse": {}, "asyncio": {}, "base64": {}, "collections": {}, "contextlib": {}, "copy": {}, "csv": {}, "dataclasses": {}, "datetime": {}, "functools": {},
		"hashlib": {}, "io": {}, "itertools": {}, "json": {}, "logging": {}, "math": {}, "os": {}, "pathlib": {}, "random": {},
		"re": {}, "statistics": {}, "string": {}, "subprocess": {}, "sys": {}, "tempfile": {}, "time": {}, "typing": {}, "uuid": {}, "warnings": {},
	}

	filtered := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.Trim(item, `"'`))
		root := strings.Split(strings.ToLower(name), ".")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.Split(root, "[")[0]
		if _, ok := standard[root]; ok {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (a *CoderAgent) validatePythonSyntaxInSandbox(ctx context.Context, sandboxID string, code string) error {
	encoded, err := json.Marshal(code)
	if err != nil {
		return fmt.Errorf("marshal python source failed: %w", err)
	}

	validator := fmt.Sprintf(
		"import ast\nsource = %s\nast.parse(source)\nprint('syntax_ok')\n",
		string(encoded),
	)
	res, err := a.Sandbox.RunPythonCodeStream(ctx, sandboxID, validator, func(stream string, line string) {
		logToContext(ctx, "[%s] syntax %s: %s", a.Name, stream, line)
	})
	if err != nil {
		return fmt.Errorf("syntax validation sandbox error: %w", err)
	}
	if res == nil {
		return fmt.Errorf("syntax validation returned nil response")
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s", chooseNonEmpty(strings.TrimSpace(res.Stderr), strings.TrimSpace(res.Stdout), fmt.Sprintf("syntax validation exited with code %d", res.ExitCode)))
	}
	return nil
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildArtifactValueMap(task *models.Task, base map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if task == nil {
		return out
	}
	for _, outputKey := range task.OutputArtifacts {
		lowerKey := strings.ToLower(strings.TrimSpace(outputKey))
		switch {
		case strings.Contains(lowerKey, "runtime_session"):
			out[outputKey] = chooseNonEmpty(base["runtime_session"], base["prepared_runtime"])
		case strings.Contains(lowerKey, "prepared_runtime"):
			out[outputKey] = chooseNonEmpty(base["prepared_runtime"], base["runtime_session"])
		case strings.Contains(lowerKey, "dependency_install_report"):
			out[outputKey] = base["dependency_install_report"]
		case strings.Contains(lowerKey, "dependency_spec"):
			out[outputKey] = base["dependency_spec"]
		}
	}
	return out
}

func looksLikeRichTextDependencySpec(raw string) bool {
	normalized := strings.ToLower(raw)
	markers := []string{
		"\n#",
		"\n##",
		"```",
		"|",
		"任务目标",
		"结论",
		"建议",
		"report",
		"summary",
		"分析",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func isValidDependencyToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._-<>!=[]", r):
		default:
			return false
		}
	}
	return true
}

func (a *CoderAgent) mockLLMGenerateCode(prompt string) string {
	return `
import sys
import math

def main():
    print("环境初始化中 (Eino 驱动)...")
    print("正在运行为该任务生成的代码...")
    result = math.sqrt(144)
    print(f"计算结果: {result}")
    
if __name__ == "__main__":
    main()
`
}
