package Intent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"scholar-agent-backend/internal/models"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

// IntentClassifier 基于大模型的意图识别器
type IntentClassifier struct {
	enabled   bool
	chatModel *openai.ChatModel
}

// MemoryTurn 表示一轮历史对话（记忆结构体，先模拟）
type MemoryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// PromptMemory 表示传给大模型的上下文记忆（记忆结构体，先模拟）
type PromptMemory struct {
	SessionID   string         `json:"session_id"`
	RecentTurns []MemoryTurn   `json:"recent_turns"`
	UserProfile map[string]any `json:"user_profile"`
	Preferences map[string]any `json:"preferences"`
	TopicHints  []string       `json:"topic_hints"`
}

// llmClassifyResponse 是 LLM 返回的 JSON 结构
type llmClassifyResponse struct {
	IntentType  string         `json:"intent_type"`
	Entities    map[string]any `json:"entities"`
	Constraints map[string]any `json:"constraints"`
	Confidence  float64        `json:"confidence"`
	Reasoning   string         `json:"reasoning"`
}

// llmRewriteResponse 是 Query 重写结果的 JSON 结构
type llmRewriteResponse struct {
	RewrittenQuery string `json:"rewritten_query"`
}

const systemPrompt = `你是一个专业的科研意图识别引擎。你的任务是分析用户的自然语言查询，精确识别其科研意图类型，并提取关键实体信息。

## 意图类型定义

你必须将用户查询分类到以下四种意图之一：

### 1. Framework_Evaluation（框架评估/对比）
用户希望对比、评估、选型多个技术框架或工具。通常涉及性能测试、A/B对比、基准测试。
- 典型信号：提到多个框架名称、对比/评估/选型/benchmark等词汇
- 常见框架：LangChain、LlamaIndex、Haystack、AutoGen、CrewAI、LangGraph等

### 2. Paper_Reproduction（论文复现）
用户希望复现某篇学术论文的实验结果，或基于论文实现代码。
- 典型信号：复现/reproduce/replicate、论文标题、paper、具体的模型名称
- 可能附带debug/fix需求

### 3. Code_Execution（代码执行）
用户希望生成并执行代码，包括数据计算、绘图、脚本运行等。
- 典型信号：计算/执行/运行/画图/plot/代码/python等
- 不涉及论文复现或框架对比的纯代码任务

### 4. General（通用查询）
不属于以上三类的科研咨询，包括文献综述、知识问答、概念解释、研究建议等。
- 典型信号：总结/综述/解释/建议/报告/RAG相关研究等

## 实体提取规则

请根据查询内容提取以下实体（仅提取存在的实体）：

| 实体键 | 类型 | 说明 |
|--------|------|------|
| frameworks | string[] | 涉及的框架名称列表 |
| framework_count | int | 框架数量 |
| paper_title | string | 论文标题 |
| topic | string | 研究主题（如 "RAG", "Query Rewrite"） |
| needs_plot | bool | 是否需要绘图/可视化 |
| needs_report | bool | 是否需要生成报告/总结 |
| needs_benchmark | bool | 是否需要性能基准测试 |
| needs_fix | bool | 是否需要调试/修复 |
| needs_research | bool | 是否需要文献调研 |
| output_mode | string | 输出模式："plot" 或 "report" |
| paper_task | string | 论文相关任务："summary"等 |

## 输出格式

你必须输出严格的 JSON，不要包含任何其他文本、markdown标记或解释：

{
  "intent_type": "意图类型",
  "entities": { ... },
  "constraints": { ... },
  "confidence": 0.0~1.0,
  "reasoning": "一句话解释判断依据"
}

## Few-Shot 示例

### 示例1
用户查询: "帮我对比一下 LangChain 和 LlamaIndex 在 RAG 场景下的性能表现"
输出:
{"intent_type":"Framework_Evaluation","entities":{"frameworks":["langchain","llamaindex"],"framework_count":2,"topic":"RAG","needs_benchmark":true,"needs_report":true},"constraints":{},"confidence":0.95,"reasoning":"用户明确要求对比两个框架在RAG场景下的性能"}

### 示例2
用户查询: "复现 Attention Is All You Need 这篇论文的 Transformer 模型"
输出:
{"intent_type":"Paper_Reproduction","entities":{"paper_title":"Attention Is All You Need"},"constraints":{},"confidence":0.95,"reasoning":"用户明确要求复现特定论文的模型实现"}

### 示例3
用户查询: "用 Python 画一个正弦函数的折线图"
输出:
{"intent_type":"Code_Execution","entities":{"needs_plot":true,"output_mode":"plot"},"constraints":{},"confidence":0.95,"reasoning":"用户要求编写Python代码绘制图表"}

### 示例4
用户查询: "帮我总结一下 RAG 技术的最新研究进展和主流方案"
输出:
{"intent_type":"General","entities":{"topic":"RAG","needs_report":true,"needs_research":true},"constraints":{},"confidence":0.90,"reasoning":"用户需要RAG领域的研究综述，属于通用科研咨询"}

### 示例5
用户查询: "运行一段 Python 代码计算斐波那契数列前20项"
输出:
{"intent_type":"Code_Execution","entities":{},"constraints":{},"confidence":0.95,"reasoning":"用户要求执行计算任务，属于代码执行类"}

### 示例6
用户查询: "对比 LangChain、LlamaIndex 和 Haystack 三个框架搭建 RAG 管道的难易程度"
输出:
{"intent_type":"Framework_Evaluation","entities":{"frameworks":["langchain","llamaindex","haystack"],"framework_count":3,"topic":"RAG","needs_report":true},"constraints":{},"confidence":0.95,"reasoning":"用户要求对比三个框架，属于框架评估"}

### 示例7
用户查询: "这篇 ResNet 的论文结果跑不出来，帮我排查一下代码问题"
输出:
{"intent_type":"Paper_Reproduction","entities":{"paper_title":"ResNet","needs_fix":true},"constraints":{},"confidence":0.90,"reasoning":"用户在复现论文时遇到问题需要调试"}

### 示例8
用户查询: "解释一下 Transformer 中 Multi-Head Attention 的原理"
输出:
{"intent_type":"General","entities":{"topic":"Transformer","needs_research":true},"constraints":{},"confidence":0.90,"reasoning":"用户询问技术原理，属于知识问答"}

### 示例9
用户查询: "用 matplotlib 画一个对比 LangChain 和 LlamaIndex 响应时间的柱状图"
输出:
{"intent_type":"Framework_Evaluation","entities":{"frameworks":["langchain","llamaindex"],"framework_count":2,"needs_plot":true,"needs_benchmark":true,"output_mode":"plot"},"constraints":{},"confidence":0.92,"reasoning":"虽然涉及绘图，但核心意图是对比两个框架的性能"}

### 示例10
用户查询: "帮我分析一下这段代码的时间复杂度并运行测试"
输出:
{"intent_type":"Code_Execution","entities":{"needs_report":true},"constraints":{},"confidence":0.88,"reasoning":"用户要求代码分析和运行，属于代码执行类"}

## 重要注意事项

1. 当查询同时涉及多个意图时，选择最核心的意图。例如"对比两个框架并画图"核心意图是 Framework_Evaluation。
2. confidence 应反映你对分类结果的确信程度，通常在 0.7~0.99 之间。
3. entities 中只包含从查询中实际能推断出的字段，不要凭空添加。
4. frameworks 中的名称统一使用小写形式（如 "langchain" 而不是 "LangChain"）。
5. 输出必须是合法的 JSON，不能包含注释或多余的文本。`

const rewriteSystemPrompt = `你是一个科研问题改写器。你的任务是将用户原始查询重写为更专业、清晰、可执行的表达。

## 核心要求
1. 严格保持原语义，不得新增、删除或改变任何任务目标与约束。
2. 保留关键实体（框架名、论文名、指标、数据范围、步骤顺序等）。
3. 如果原问题包含“先…再…然后…最后…”等顺序，必须在改写中保留相同顺序。
4. 只做表达优化：术语更规范、句式更清晰、歧义更少。
5. 不要添加解释、免责声明或额外背景。

## 输出格式
你必须输出严格 JSON，不要包含任何其他文本：
{
  "rewritten_query": "重写后的查询"
}

## Few-Shot 示例

### 示例1
用户查询: "先对比 langchain 和 llamaindex 在 RAG 的召回率，再给我一个总结"
输出:
{"rewritten_query":"请先对比 LangChain 与 LlamaIndex 在 RAG 场景下的召回率表现，再输出结构化总结。"}

### 示例2
用户查询: "复现 attention is all you need，然后把训练曲线画出来"
输出:
{"rewritten_query":"请复现《Attention Is All You Need》的实验流程，并绘制训练曲线。"}

### 示例3
用户查询: "帮我跑段python算一下topk准确率"
输出:
{"rewritten_query":"请运行一段 Python 代码计算 Top-K 准确率。"}

### 示例4
用户查询: "讲讲query rewrite在rag里有什么用"
输出:
{"rewritten_query":"请说明 Query Rewrite 在 RAG 流程中的作用与价值。"}`

// NewIntentClassifier 创建新的意图识别器
func NewIntentClassifier() *IntentClassifier {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		log.Printf("[IntentClassifier] OPENAI_API_KEY not set, classifier disabled")
		return &IntentClassifier{enabled: false}
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
		log.Printf("[IntentClassifier] init failed: %v, classifier disabled", err)
		return &IntentClassifier{enabled: false}
	}

	log.Printf("[IntentClassifier] initialized successfully with model=%s", modelName)
	return &IntentClassifier{
		enabled:   true,
		chatModel: chatModel,
	}
}

// Enabled 返回分类器是否可用
func (c *IntentClassifier) Enabled() bool {
	return c != nil && c.enabled && c.chatModel != nil
}

// Classify 使用大模型对用户查询进行意图识别和实体抽取
func (c *IntentClassifier) Classify(ctx context.Context, rawQuery string) (models.IntentContext, error) {
	if !c.Enabled() {
		return models.IntentContext{}, fmt.Errorf("intent classifier is disabled")
	}

	memory := c.mockPromptMemory(rawQuery)
	rewrittenQuery, err := c.rewriteQuery(ctx, rawQuery, memory)
	if err != nil {
		log.Printf("[IntentClassifier] query rewrite failed, fallback to raw query: %v", err)
		rewrittenQuery = rawQuery
	}

	userPrompt := buildClassifyUserPrompt(rawQuery, rewrittenQuery, memory)

	msg, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return models.IntentContext{}, fmt.Errorf("LLM intent classification failed: %w", err)
	}

	result, err := parseLLMResponse(msg.Content)
	if err != nil {
		return models.IntentContext{}, fmt.Errorf("failed to parse LLM response: %w (raw: %s)", err, truncate(msg.Content, 200))
	}

	// 校验 intent_type 合法性
	if !isValidIntentType(result.IntentType) {
		return models.IntentContext{}, fmt.Errorf("LLM returned invalid intent_type: %q", result.IntentType)
	}

	intentCtx := models.IntentContext{
		RawIntent:       rawQuery,
		RewrittenIntent: rewrittenQuery,
		IntentType:      result.IntentType,
		Entities:        normalizeEntities(result.Entities),
		Constraints:     result.Constraints,
		Confidence:      result.Confidence,
		Reasoning:       result.Reasoning,
		Source:          "llm",
		Metadata: map[string]any{
			"normalized_intent": strings.ToLower(rawQuery),
			"rewritten_intent":  rewrittenQuery,
		},
	}

	if intentCtx.Entities == nil {
		intentCtx.Entities = map[string]any{}
	}
	if intentCtx.Constraints == nil {
		intentCtx.Constraints = map[string]any{}
	}

	log.Printf("[IntentClassifier] query=%q intent_type=%s confidence=%.2f reasoning=%q",
		truncate(rawQuery, 80), result.IntentType, result.Confidence, result.Reasoning)

	return intentCtx, nil
}

// rewriteQuery 将用户查询重写为更专业的表达（语义保持不变）
func (c *IntentClassifier) rewriteQuery(ctx context.Context, rawQuery string, memory *PromptMemory) (string, error) {

	// 把用户 和 系统的对话历史生成一个字符串
	userPrompt := buildRewriteUserPrompt(rawQuery, memory)

	msg, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: rewriteSystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return "", fmt.Errorf("LLM query rewrite failed: %w", err)
	}

	result, err := parseRewriteResponse(msg.Content)
	if err != nil {
		return "", fmt.Errorf("failed to parse rewrite response: %w", err)
	}

	rewritten := strings.TrimSpace(result.RewrittenQuery)
	if rewritten == "" {
		return "", fmt.Errorf("rewritten_query is empty")
	}
	return rewritten, nil
}

// parseLLMResponse 解析 LLM 返回的 JSON
func parseLLMResponse(raw string) (*llmClassifyResponse, error) {
	cleaned := strings.TrimSpace(raw)
	// 去除可能的 markdown 包裹
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	// 尝试从文本中提取 JSON 对象
	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > idx {
			cleaned = cleaned[idx : end+1]
		}
	}

	var result llmClassifyResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	if result.IntentType == "" {
		return nil, fmt.Errorf("intent_type is empty in LLM response")
	}

	return &result, nil
}

// parseRewriteResponse 解析 Query 重写 JSON
func parseRewriteResponse(raw string) (*llmRewriteResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end > idx {
			cleaned = cleaned[idx : end+1]
		}
	}

	var result llmRewriteResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return &result, nil
}

// isValidIntentType 检查意图类型是否合法
func isValidIntentType(intentType string) bool {
	switch intentType {
	case "Framework_Evaluation", "Paper_Reproduction", "Code_Execution", "General":
		return true
	default:
		return false
	}
}

// normalizeEntities 规范化实体字段
func normalizeEntities(entities map[string]any) map[string]any {
	if entities == nil {
		return map[string]any{}
	}

	// 规范化 frameworks：确保是 []string 类型
	if raw, ok := entities["frameworks"]; ok {
		switch v := raw.(type) {
		case []any:
			frameworks := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					frameworks = append(frameworks, strings.ToLower(strings.TrimSpace(s)))
				}
			}
			entities["frameworks"] = frameworks
			if _, hasCount := entities["framework_count"]; !hasCount {
				entities["framework_count"] = len(frameworks)
			}
		case []string:
			for i, s := range v {
				v[i] = strings.ToLower(strings.TrimSpace(s))
			}
			entities["frameworks"] = v
		}
	}

	return entities
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (c *IntentClassifier) mockPromptMemory(rawQuery string) *PromptMemory {
	return &PromptMemory{
		SessionID: "mock-session-id",
		RecentTurns: []MemoryTurn{
			{Role: "user", Content: "我最近在做 RAG 相关实验"},
			{Role: "assistant", Content: "可以先确定框架，再定义评测指标"},
			{Role: "user", Content: rawQuery},
		},
		UserProfile: map[string]any{
			"domain": "nlp",
			"level":  "researcher",
		},
		Preferences: map[string]any{
			"language": "zh-CN",
			"style":    "structured",
		},
		TopicHints: []string{"RAG", "benchmark", "query rewrite"},
	}
}

func formatMemoryForPrompt(memory *PromptMemory) string {
	if memory == nil {
		return "{}"
	}
	b, err := json.Marshal(memory)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func buildClassifyUserPrompt(rawQuery, rewrittenQuery string, memory *PromptMemory) string {
	return fmt.Sprintf(
		"用户查询: %q\n\n专业化改写: %q\n\n上下文记忆: %s\n\n请优先依据用户原始查询进行意图识别，并参考改写查询和上下文记忆提升术语一致性，按指定JSON格式输出。",
		rawQuery,
		rewrittenQuery,
		formatMemoryForPrompt(memory),
	)
}

func buildRewriteUserPrompt(rawQuery string, memory *PromptMemory) string {
	return fmt.Sprintf(
		"用户查询: %q\n\n上下文记忆: %s\n\n请按要求输出改写结果。",
		rawQuery,
		formatMemoryForPrompt(memory),
	)
}
