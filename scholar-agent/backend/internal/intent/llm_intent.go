package intent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

// LLMIntentInferrer LLM 意图推理器 - 使用大语言模型进行意图分类
type LLMIntentInferrer struct {
	chatModel *openai.ChatModel
}

// LLMIntentConfig LLM 意图推理器配置
type LLMIntentConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// NewLLMIntentInferrer 创建 LLM 意图推理器实例
func NewLLMIntentInferrer(cfg LLMIntentConfig) (*LLMIntentInferrer, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化意图 LLM 模型失败: %w", err)
	}

	return &LLMIntentInferrer{
		chatModel: chatModel,
	}, nil
}

// buildIntentPrompt 构建科研场景意图识别 Prompt
func buildIntentPrompt(question string, lastHistory string) string {
	// 构建意图列表
	var actionList strings.Builder
	for action, info := range ResearchIntentActions {
		actionList.WriteString(fmt.Sprintf("- %s: %s\n", action, info.Desc))
	}

	prompt := fmt.Sprintf(`你是一个科研论文智能助手的意图识别模块。请根据用户输入判断其意图类别。

当前时间: %s

上一轮对话记录:
%s

用户当前输入:
%s

可选的意图类别（请只输出下面的 action 名称）:
%s
【输出格式】
请严格按照以下格式输出，不要有任何多余文字：
toolName$keyword$keyword2

其中:
- toolName: 必须是上面列表中的某个 action 名称
- keyword: 与意图相关的关键词（如搜索的主题、写作的章节等），如无则填 none
- keyword2: 第二个关键词，如无则填 none

例如:
- 用户说"帮我搜一下transformer相关的论文" → searchPaper$transformer$none
- 用户说"润色一下这段引言" → polishText$引言$none
- 用户说"你好啊" → chat$none$none
- 用户说"对比一下PyTorch和TensorFlow" → frameworkEvaluation$PyTorch$TensorFlow`,
		time.Now().Format("2006-01-02 15:04:05"),
		lastHistory,
		question,
		actionList.String(),
	)

	return prompt
}

// Infer 调用 LLM 进行意图推理
// 支持 2 秒超时 + 1 次重试
func (li *LLMIntentInferrer) Infer(ctx context.Context, chatCtx *ChatContext) (*IntentInfo, error) {
	lastHistory := chatCtx.LastHistoryData
	if lastHistory == "" {
		lastHistory = "无记录"
	}

	prompt := buildIntentPrompt(chatCtx.Question, lastHistory)

	// 首次调用: 2秒超时
	result, err := li.callWithTimeout(ctx, prompt, 2*time.Second)
	if err != nil {
		log.Printf("[LLMIntent] 首次调用失败: %v, 触发重试", err)
		// 重试: 3秒超时（更宽松）
		result, err = li.callWithTimeout(ctx, prompt, 3*time.Second)
		if err != nil {
			log.Printf("[LLMIntent] 重试失败: %v, 降级为 none", err)
			return &IntentInfo{ToolName: "none"}, nil
		}
	}

	// 解析 LLM 输出
	intentInfo := li.parseResult(result)
	log.Printf("[LLMIntent] 推理结果: toolName=%s, keyword=%s, keyword2=%s",
		intentInfo.ToolName, intentInfo.Keyword, intentInfo.Keyword2)

	return intentInfo, nil
}

// callWithTimeout 带超时的 LLM 调用
func (li *LLMIntentInferrer) callWithTimeout(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	messages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}

	msg, err := li.chatModel.Generate(timeoutCtx, messages)
	if err != nil {
		return "", fmt.Errorf("LLM 调用失败: %w", err)
	}

	content := strings.TrimSpace(msg.Content)
	content = strings.ReplaceAll(content, "\n", "")
	return content, nil
}

// parseResult 解析 LLM 输出格式: "toolName$keyword$keyword2"
func (li *LLMIntentInferrer) parseResult(content string) *IntentInfo {
	info := &IntentInfo{
		ToolName: "none",
		Keyword:  "none",
		Keyword2: "none",
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return info
	}

	parts := strings.Split(content, "$")
	if len(parts) >= 1 {
		info.ToolName = strings.TrimSpace(parts[0])
	}
	if len(parts) >= 2 {
		info.Keyword = strings.TrimSpace(parts[1])
	}
	if len(parts) >= 3 {
		info.Keyword2 = strings.TrimSpace(parts[2])
	}

	// 校验 toolName 合法性
	if !IsValidAction(info.ToolName) {
		log.Printf("[LLMIntent] 非法 toolName=%q, 降级为 none", info.ToolName)
		info.ToolName = "none"
	}

	return info
}
