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

const (
	llmFirstAttemptTimeout  = 4 * time.Second
	llmSecondAttemptTimeout = 8 * time.Second
	llmSingleCallHardLimit  = 12 * time.Second
)

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
	//TODO  这个 prompt 目前还需要 加上很多 约束 和 数据的 收集  这边 目前还没有 完善
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
	start := time.Now()
	lastHistory := chatCtx.LastHistoryData
	if lastHistory == "" {
		lastHistory = "无记录"
	}

	prompt := buildIntentPrompt(chatCtx.Question, lastHistory)
	log.Printf("[LLMIntent] 开始推理: question=%q", chatCtx.Question)

	result, err := li.callWithTimeout(ctx, prompt, llmFirstAttemptTimeout)
	if err != nil {
		log.Printf("[LLMIntent] 首次调用失败: %v, 触发重试", err)
		result, err = li.callWithTimeout(ctx, prompt, llmSecondAttemptTimeout)
		if err != nil {
			log.Printf("[LLMIntent] 重试失败: %v, 降级为 none, elapsed=%v", err, time.Since(start))
			return &IntentInfo{ToolName: "none"}, nil
		}
	}

	log.Printf("[LLMIntent] 原始输出: %q", result)
	intentInfo := li.parseResult(result)
	log.Printf("[LLMIntent] 推理结果: toolName=%s, keyword=%s, keyword2=%s, elapsed=%v",
		intentInfo.ToolName, intentInfo.Keyword, intentInfo.Keyword2, time.Since(start))

	return intentInfo, nil
}

// callWithTimeout 带超时的 LLM 调用
func (li *LLMIntentInferrer) callWithTimeout(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	messages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}
	type callResult struct {
		content string
		err     error
	}
	done := make(chan callResult, 1)

	go func() {
		msg, err := li.chatModel.Generate(callCtx, messages)
		if err != nil {
			done <- callResult{err: fmt.Errorf("LLM 调用失败: %w", err)}
			return
		}
		content := strings.TrimSpace(msg.Content)
		content = strings.ReplaceAll(content, "\n", "")
		done <- callResult{content: content}
	}()

	hardTimer := time.NewTimer(llmSingleCallHardLimit)
	defer hardTimer.Stop()
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("LLM 调用被上层取消: %w", ctx.Err())
	case <-callCtx.Done():
		return "", fmt.Errorf("LLM 调用超时(%v): %w", timeout, callCtx.Err())
	case <-hardTimer.C:
		return "", fmt.Errorf("LLM 调用硬超时(%v)", llmSingleCallHardLimit)
	case result := <-done:
		return result.content, result.err
	}
}

// parseResult 解析 LLM 输出格式: "toolName$keyword$keyword2"
func (li *LLMIntentInferrer) parseResult(content string) *IntentInfo {
	info := &IntentInfo{
		ToolName: "none",
		Keyword:  "none",
		Keyword2: "none",
	}

	content = strings.TrimSpace(content)
	content = strings.Trim(content, "`")
	if len(content) >= 4 && strings.EqualFold(content[:4], "json") {
		content = content[4:]
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
	if info.Keyword == "" {
		info.Keyword = "none"
	}
	if info.Keyword2 == "" {
		info.Keyword2 = "none"
	}

	// 校验 toolName 合法性
	if !IsValidAction(info.ToolName) {
		log.Printf("[LLMIntent] 非法 toolName=%q, 降级为 none", info.ToolName)
		info.ToolName = "none"
	}

	return info
}
