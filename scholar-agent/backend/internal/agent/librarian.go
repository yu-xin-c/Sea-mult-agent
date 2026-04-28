package agent

import (
	"context"
	"fmt"
	"log"
	"scholar-agent-backend/internal/appconfig"
	"scholar-agent-backend/internal/models"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// LibrarianAgent 负责文献检索、解析与总结
type LibrarianAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
}

func NewLibrarianAgent() *LibrarianAgent {
	agent := &LibrarianAgent{
		Name: "librarian_agent",
		SystemPrompt: `你是一个专业的 AI 文献检索员与科研分析师。你的任务是根据用户提供的论文标题或相关要求，提供详细的文献分析报告，辅助科研人员阅读与理解。
		请严格遵循以下规则：
		1. 你**绝对不能**编写任何 Python 代码或 Shell 脚本。
		2. 你的输出必须是一份结构化、清晰、专业的 Markdown 格式文献分析报告。
		3. 报告应包含以下核心内容（如适用）：
		   - 论文标题与核心背景（一句话总结）
		   - 核心创新点与算法原理（通俗易懂的解释）
		   - 网络架构/模型结构简述
		   - 推荐的开源代码实现（如 GitHub 上的主流仓库）
		   - 可能遇到的复现难点提示
		4. 请直接输出内容，不要包含任何前缀如“好的，这是报告...”。`,
	}
	agent.SystemPrompt = `你是一名专业的 AI 文献检索员和科研分析师。你的任务是根据用户提供的论文标题、研究主题或分析要求，输出结构化、清晰、专业的文献分析报告，帮助科研人员快速理解主题。

请严格遵守以下规则：
1. 不要编写任何 Python 代码或 Shell 脚本。
2. 输出必须是结构化、清晰、专业的 Markdown 文献分析报告。
3. 报告应尽量包含以下内容（如适用）：
   - 论文标题与核心背景（一句话总结）
   - 核心创新点与算法原理（用通俗语言解释）
   - 网络架构或模型结构简述
   - 推荐的开源代码实现（如 GitHub 上的主流仓库）
   - 可能遇到的复现难点提示
4. 直接输出正文，不要加“好的，这是报告”之类的前缀。`

	agent.initEinoChain()
	return agent
}

func (a *LibrarianAgent) initEinoChain() {
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Fatalf("加载 LLM 配置失败: %v", err)
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: llmCfg.BaseURL,
		APIKey:  llmCfg.APIKey,
		Model:   llmCfg.Model,
	})
	if err != nil {
		log.Fatalf("初始化文献分析模型失败: %v", err)
	}

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装文献分析提示词", a.Name)
		messages := []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: fmt.Sprintf("请解析并总结以下任务相关的文献内容：\n%s", input)},
		}
		return messages, nil
	}))

	// 使用支持流式的 ChatModelNode
	graph.AddChatModelNode("LLM_Analyze_Literature", chatModel)

	graph.AddLambdaNode("Report_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Report_Extractor]: 文献分析报告生成完毕", a.Name)
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Analyze_Literature")
	graph.AddEdge("LLM_Analyze_Literature", "Report_Extractor")
	graph.AddEdge("Report_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
}

func (a *LibrarianAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	logToContext(ctx, "[%s] 开始执行任务: %s", a.Name, task.Name)

	input := task.Description
	if task != nil && len(task.Inputs) > 0 {
		input = fmt.Sprintf("%s\n\n上游输入:\n%v", task.Description, task.Inputs)
	}

	output, err := a.EinoChain.Invoke(ctx, input)
	if err != nil {
		logToContext(ctx, "[%s] 文献解析失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("文献解析失败: %v", err)
		return err
	}

	task.Result = output
	task.Status = models.StatusCompleted
	logToContext(ctx, "[%s] 任务完成: %s", a.Name, task.Name)
	return nil
}
