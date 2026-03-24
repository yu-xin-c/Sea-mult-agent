package agent

import (
	"context"
	"log"
	"os"
	"scholar-agent-backend/internal/models"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ChatAgent 负责简单的问答交互，并能识别是否需要执行代码
type ChatAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
	Coder        *CoderAgent // 注入 CoderAgent 以便在需要时执行代码
}

func NewChatAgent(coder *CoderAgent) *ChatAgent {
	agent := &ChatAgent{
		Name: "chat_agent",
		Coder: coder,
		SystemPrompt: `你是一个专业的 AI 科研助理。你的任务是回答用户关于科研、论文、代码或技术选型的问题。
		
		【重要规则】：
		1. 如果用户要求你“计算”、“运行代码”、“执行”、“画图”或任何需要 Python 环境的任务，请在回答的开头加上特殊的标记 [CODE_EXECUTION_REQUIRED]，然后给出你的分析。
		2. 如果用户只是普通的咨询，则直接回答。
		3. 你提供的代码应该是 Python 格式。`,
	}

	agent.initEinoChain()
	return agent
}

func (a *ChatAgent) initEinoChain() {
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
		log.Fatalf("初始化聊天模型失败: %v", err)
	}

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		messages := []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: input},
		}
		return messages, nil
	}))

	graph.AddChatModelNode("LLM_Chat", chatModel)

	graph.AddLambdaNode("Response_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Chat")
	graph.AddEdge("LLM_Chat", "Response_Extractor")
	graph.AddEdge("Response_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
}

func (a *ChatAgent) Answer(ctx context.Context, question string) (string, error) {
	response, err := a.EinoChain.Invoke(ctx, question)
	if err != nil {
		return "", err
	}

	// 如果识别到需要执行代码，尝试自动执行并追加结果
	if strings.Contains(response, "[CODE_EXECUTION_REQUIRED]") {
		log.Printf("[%s] 检测到代码执行需求，正在调用 CoderAgent 自动执行...", a.Name)
		
		// 创建一个临时任务给 CoderAgent
		tempTask := &models.Task{
			ID:          "chat-exec-" + question[:min(len(question), 10)],
			Name:        "Auto-Execute from Chat",
			Description: question,
			Status:      models.StatusPending,
		}

		err := a.Coder.ExecuteTask(ctx, tempTask, make(map[string]interface{}))
		if err == nil {
			response = strings.Replace(response, "[CODE_EXECUTION_REQUIRED]", "🤖 **已自动在沙箱中运行**：\n\n```python\n"+tempTask.Code+"\n```\n\n**执行结果**：\n```text\n"+tempTask.Result+"\n```\n\n---", 1)
		} else {
			response = strings.Replace(response, "[CODE_EXECUTION_REQUIRED]", "❌ **沙箱执行失败**: "+err.Error()+"\n\n", 1)
		}
	}

	return response, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *ChatAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	output, err := a.Answer(ctx, task.Description)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return err
	}
	task.Result = output
	task.Status = models.StatusCompleted
	return nil
}
