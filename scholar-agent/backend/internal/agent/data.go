package agent

import (
	"context"
	"fmt"
	"log"
	"scholar-agent-backend/internal/appconfig"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// DataAgent is responsible for analyzing execution logs, extracting metrics, and generating reports.
type DataAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
}

type dataContextKey string

const dataSystemPromptContextKey dataContextKey = "data_system_prompt"

func NewDataAgent() *DataAgent {
	agent := &DataAgent{
		Name:         "data_agent",
		SystemPrompt: prompts.DataSystemPrompt,
	}

	agent.initEinoChain()
	return agent
}

func (a *DataAgent) promptInput(input string) string {
	return prompts.DataPromptInput(input)
}

func (a *DataAgent) initEinoChain() {
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
		log.Fatalf("初始化数据分析模型失败: %v", err)
	}

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		log.Printf("[%s] Eino 节点 [Prompt_Builder]: 正在组装报告生成提示词", a.Name)
		systemPrompt := a.SystemPrompt
		if prompt, ok := ctx.Value(dataSystemPromptContextKey).(string); ok && prompt != "" {
			systemPrompt = prompt
		}
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.DataReportUserPrompt(input)},
		}
		return messages, nil
	}))

	graph.AddChatModelNode("LLM_Generate_Report", chatModel)

	graph.AddLambdaNode("Report_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		log.Printf("[%s] Eino 节点 [Report_Extractor]: 报告生成完毕", a.Name)
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Generate_Report")
	graph.AddEdge("LLM_Generate_Report", "Report_Extractor")
	graph.AddEdge("Report_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
}

func (a *DataAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	logToContext(ctx, "[%s] 开始执行任务: %s", a.Name, task.Name)

	input := task.Description
	if task != nil && len(task.Inputs) > 0 {
		input = fmt.Sprintf("%s\n\n上游输入:\n%v", task.Description, task.Inputs)
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	ctx = context.WithValue(ctx, dataSystemPromptContextKey, prompts.DataSystemPromptForTask(intentType, task.Type, task.Name, task.Description))

	output, err := a.EinoChain.Invoke(ctx, input)
	if err != nil {
		logToContext(ctx, "[%s] 报告生成失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("报告生成失败: %v", err)
		return err
	}

	task.Result = output
	task.Status = models.StatusCompleted
	logToContext(ctx, "[%s] 任务完成: %s", a.Name, task.Name)
	return nil
}
