package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"scholar-agent-backend/internal/models"

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

func NewDataAgent() *DataAgent {
	agent := &DataAgent{
		Name: "data_agent",
		SystemPrompt: `你是一个资深的 AI 科研数据分析师。你的任务是根据代码沙箱执行后提取的 JSON 格式的指标结果（例如 reproduction_metrics.json），以及执行日志，生成一份专业的技术评估报告。
		请遵循以下格式生成 Markdown 报告：
		1. 实验目标与背景
		2. 执行环境与代码复现过程概述
		3. 核心指标分析 (使用表格展示)
		4. 结论与建议
		如果数据中存在对比（如原始数据 vs 用户数据），请务必在第3部分进行详细对比。`,
	}
	agent.SystemPrompt = `你是一名资深的 AI 科研数据分析师。你的任务是根据代码执行结果、实验日志、指标数据或上游分析材料，生成结构化、专业、可读的 Markdown 评估报告。

请遵循以下格式生成报告：
1. 实验目标与背景
2. 执行过程与输入材料概述
3. 核心指标分析
4. 结论与建议

如果输入中包含对比信息，请在核心指标分析中做清晰对比；如果输入中缺少关键指标，也请明确指出缺口，不要编造数据。`

	agent.initEinoChain()
	return agent
}

func (a *DataAgent) promptInput(input string) string {
	return fmt.Sprintf("请根据以下输入材料生成评估报告：\n%s", input)
}

func (a *DataAgent) initEinoChain() {
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
		log.Fatalf("初始化数据分析模型失败: %v", err)
	}

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		log.Printf("[%s] Eino 节点 [Prompt_Builder]: 正在组装报告生成提示词", a.Name)
		messages := []*schema.Message{
			{Role: schema.System, Content: a.SystemPrompt},
			{Role: schema.User, Content: fmt.Sprintf("这是沙箱执行的输出结果和提取到的指标数据，请生成评估报告：\n%s", input)},
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
