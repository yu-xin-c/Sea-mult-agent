package agent

import (
	"context"
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
	Name         string
	SystemPrompt string
	Sandbox      *sandbox.SandboxClient
	EinoChain    compose.Runnable[string, string] // 使用 Eino 编排的执行链
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

	// 初始化真实的 Eino 编排链 (Real LLM -> Sandbox Execution)
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
			tempID, err := a.Sandbox.CreatePersistentSandbox(ctx, "temp", "", "")
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
		tempID, _ := a.Sandbox.CreatePersistentSandbox(ctx, "mock", "", "")
		defer a.Sandbox.CleanupSandbox(context.Background(), tempID)

		// 同样写入文件执行，提高成功率
		scriptPath := filepath.Join("/tmp", "scholar_workspace_mock", "run_script.py")
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
	output, err := a.EinoChain.Invoke(ctx, task.Description)
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
