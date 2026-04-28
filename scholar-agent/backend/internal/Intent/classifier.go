package Intent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"scholar-agent-backend/internal/models"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/sync/errgroup"
)

// IntentClassifier 基于大模型的意图识别器
type IntentClassifier struct {
	enabled     bool
	chatModel   *openai.ChatModel
	memoryStore MemoryStore
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

// llmPaperSearchResponse 是论文仓库检索字段抽取结果。
// 这些字段用于后续 Papers with Code / GitHub 检索，避免再从长文本中二次猜测。
type llmPaperSearchResponse struct {
	PaperTitle  string  `json:"paper_title"`
	ArxivID     string  `json:"paper_arxiv_id"`
	SearchQuery string  `json:"paper_search_query"`
	MethodName  string  `json:"paper_method_name"`
	Confidence  float64 `json:"confidence"`
	Reasoning   string  `json:"reasoning"`
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

const paperSearchSystemPrompt = `你是一个论文仓库检索字段提取器。你的任务是从用户查询中提取最适合用于 Papers with Code / GitHub 仓库搜索的结构化字段。

## 目标
输出尽量稳定、可检索的字段，供后续真实联网查询使用。

## 字段定义
- paper_title: 论文标题。只有在用户明确提到某篇论文时才填写，尽量保留原始标题大小写。
- paper_arxiv_id: arXiv ID，例如 1706.03762。仅在用户明确给出时填写。
- paper_search_query: 最适合直接用于检索论文或仓库的查询词。优先级通常是 arXiv ID > 论文标题 > 方法名。
- paper_method_name: 方法名、模型名或别名，例如 Transformer、ResNet、LoRA。
- confidence: 0~1 之间的置信度。
- reasoning: 一句话说明提取依据。

## 约束
1. 不能编造论文标题、arXiv ID 或方法名。
2. 如果不是论文相关请求，相关字段保持空字符串。
3. paper_search_query 必须简洁，不能把整段任务描述原样复制进去。
4. 若已识别到 paper_arxiv_id，paper_search_query 优先直接使用该 ID。
5. 若已识别到 paper_title，paper_search_query 优先使用 paper_title。

## 输出格式
你必须输出严格 JSON，不要包含任何其他文本：
{
  "paper_title": "",
  "paper_arxiv_id": "",
  "paper_search_query": "",
  "paper_method_name": "",
  "confidence": 0.0,
  "reasoning": ""
}

## Few-Shot 示例
用户查询: "复现 Attention Is All You Need 这篇论文"
输出:
{"paper_title":"Attention Is All You Need","paper_arxiv_id":"","paper_search_query":"Attention Is All You Need","paper_method_name":"Transformer","confidence":0.96,"reasoning":"用户明确提到论文标题，且对应方法名是 Transformer"}

用户查询: "帮我找一下 arXiv:1706.03762 的实现仓库"
输出:
{"paper_title":"","paper_arxiv_id":"1706.03762","paper_search_query":"1706.03762","paper_method_name":"","confidence":0.98,"reasoning":"用户明确给出了 arXiv ID，适合作为首选检索词"}

用户查询: "解释一下 Transformer 的多头注意力"
输出:
{"paper_title":"","paper_arxiv_id":"","paper_search_query":"Transformer","paper_method_name":"Transformer","confidence":0.72,"reasoning":"用户只提到了方法名，没有明确指定论文标题"}`

// NewIntentClassifier 创建新的意图识别器
func NewIntentClassifier() *IntentClassifier {
	apiKey := os.Getenv("OPENAI_API_KEY")
	memoryStore, memoryErr := NewRedisMemoryStoreFromEnv()
	if memoryErr != nil {
		log.Printf("[IntentClassifier] redis memory store init failed: %v", memoryErr)
		memoryStore = &NoopMemoryStore{}
	}
	if strings.TrimSpace(apiKey) == "" {
		log.Printf("[IntentClassifier] OPENAI_API_KEY not set, classifier disabled")
		return &IntentClassifier{enabled: false, memoryStore: memoryStore}
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
		return &IntentClassifier{enabled: false, memoryStore: memoryStore}
	}

	log.Printf("[IntentClassifier] initialized successfully with model=%s", modelName)
	return &IntentClassifier{
		enabled:     true,
		chatModel:   chatModel,
		memoryStore: memoryStore,
	}
}

// Enabled 返回分类器是否可用
func (c *IntentClassifier) Enabled() bool {
	return c != nil && c.enabled && c.chatModel != nil
}

// Classify 使用大模型做意图识别，并并行完成 query 重写与短期记忆注入。
func (c *IntentClassifier) Classify(ctx context.Context, userID, sessionID, rawQuery string) (models.IntentContext, error) {
	if !c.Enabled() {
		return models.IntentContext{}, fmt.Errorf("intent classifier is disabled")
	}

	memory, err := c.loadPromptMemory(ctx, userID, sessionID)
	if err != nil {
		log.Printf("[IntentClassifier] load prompt memory failed, fallback to empty memory: %v", err)
		memory = &PromptMemory{
			SessionID:   sessionID,
			RecentTurns: nil,
			UserProfile: map[string]any{},
			Preferences: map[string]any{},
			TopicHints:  nil,
		}
	}

	intentCtx, err := c.classifyRewriteAndExtractParallel(ctx, rawQuery, memory)
	if err != nil {
		return models.IntentContext{}, err
	}
	if intentCtx.Metadata == nil {
		intentCtx.Metadata = map[string]any{}
	}
	intentCtx.Metadata["normalized_intent"] = strings.ToLower(rawQuery)
	intentCtx.Metadata["session_id"] = sessionID
	intentCtx.Metadata["user_id"] = userID
	if strings.TrimSpace(intentCtx.RewrittenIntent) != "" {
		intentCtx.Metadata["rewritten_intent"] = intentCtx.RewrittenIntent
	}

	return intentCtx, nil
}

// Rewrite 将用户查询重写为更专业的表达（语义保持不变）。
func (c *IntentClassifier) Rewrite(ctx context.Context, rawQuery string, memory *PromptMemory) (string, error) {
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

// ExtractPaperSearchFields 只负责提取论文仓库检索所需的结构化字段。
func (c *IntentClassifier) ExtractPaperSearchFields(ctx context.Context, rawQuery string, memory *PromptMemory) (map[string]any, error) {
	userPrompt := buildPaperSearchUserPrompt(rawQuery, memory)

	msg, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: paperSearchSystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM paper search extraction failed: %w", err)
	}

	result, err := parsePaperSearchResponse(msg.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse paper search response: %w", err)
	}

	fields := map[string]any{}
	if title := strings.TrimSpace(result.PaperTitle); title != "" {
		fields["paper_title"] = title
	}
	if arxivID := strings.TrimSpace(result.ArxivID); arxivID != "" {
		fields["paper_arxiv_id"] = arxivID
	}
	if query := strings.TrimSpace(result.SearchQuery); query != "" {
		fields["paper_search_query"] = query
	}
	if method := strings.TrimSpace(result.MethodName); method != "" {
		fields["paper_method_name"] = method
	}
	if result.Confidence > 0 {
		fields["paper_search_confidence"] = clampConfidence(result.Confidence)
	}
	if reasoning := strings.TrimSpace(result.Reasoning); reasoning != "" {
		fields["paper_search_reasoning"] = reasoning
	}
	return fields, nil
}

// ClassifyOnly 只做分类和实体抽取，便于和 Rewrite 并行执行。
func (c *IntentClassifier) ClassifyOnly(ctx context.Context, rawQuery string, memory *PromptMemory) (models.IntentContext, error) {
	userPrompt := buildClassifyUserPrompt(rawQuery, "", memory)

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
	if !isValidIntentType(result.IntentType) {
		return models.IntentContext{}, fmt.Errorf("LLM returned invalid intent_type: %q", result.IntentType)
	}

	intentCtx := models.IntentContext{
		RawIntent:   rawQuery,
		IntentType:  result.IntentType,
		Entities:    normalizeEntities(result.Entities),
		Constraints: result.Constraints,
		Confidence:  clampConfidence(result.Confidence),
		Reasoning:   result.Reasoning,
		Source:      "llm",
	}
	if intentCtx.Entities == nil {
		intentCtx.Entities = map[string]any{}
	}
	if intentCtx.Constraints == nil {
		intentCtx.Constraints = map[string]any{}
	}
	return intentCtx, nil
}

func (c *IntentClassifier) classifyRewriteAndExtractParallel(ctx context.Context, rawQuery string, memory *PromptMemory) (models.IntentContext, error) {
	var (
		intentCtx      models.IntentContext
		rewrittenQuery string
		rewriteErr     error
		paperFields    map[string]any
		paperFieldErr  error
	)

	g, groupCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		intentCtx, err = c.ClassifyOnly(groupCtx, rawQuery, memory)
		return err
	})
	g.Go(func() error {
		var err error
		rewrittenQuery, err = c.Rewrite(groupCtx, rawQuery, memory)
		if err != nil {
			rewriteErr = err
			return nil
		}
		return nil
	})
	g.Go(func() error {
		var err error
		paperFields, err = c.ExtractPaperSearchFields(groupCtx, rawQuery, memory)
		if err != nil {
			paperFieldErr = err
			return nil
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return models.IntentContext{}, err
	}

	if strings.TrimSpace(rewrittenQuery) == "" {
		rewrittenQuery = rawQuery
	}
	intentCtx.RewrittenIntent = rewrittenQuery
	if intentCtx.Metadata == nil {
		intentCtx.Metadata = map[string]any{}
	}
	if rewriteErr != nil {
		intentCtx.Metadata["rewrite_error"] = rewriteErr.Error()
		log.Printf("[IntentClassifier] query rewrite failed, fallback to raw query: %v", rewriteErr)
	}
	if paperFieldErr != nil {
		intentCtx.Metadata["paper_search_error"] = paperFieldErr.Error()
		log.Printf("[IntentClassifier] paper search field extraction failed: %v", paperFieldErr)
	}
	if len(paperFields) > 0 {
		mergePaperSearchFields(intentCtx.Entities, paperFields)
		intentCtx.Metadata["paper_search_fields"] = cloneAnyMap(paperFields)
	}

	log.Printf("[IntentClassifier] intent_type=%s confidence=%.2f source=%s",
		intentCtx.IntentType, intentCtx.Confidence, intentCtx.Source)

	return intentCtx, nil
}

func (c *IntentClassifier) loadPromptMemory(ctx context.Context, userID, sessionID string) (*PromptMemory, error) {
	if c == nil || c.memoryStore == nil || !c.memoryStore.Enabled() || strings.TrimSpace(sessionID) == "" {
		return &PromptMemory{
			SessionID:   sessionID,
			RecentTurns: nil,
			UserProfile: map[string]any{},
			Preferences: map[string]any{},
			TopicHints:  nil,
		}, nil
	}

	ttl := sessionTTLFromEnv()
	if err := c.memoryStore.EnsureSession(ctx, userID, sessionID, ttl); err != nil {
		return nil, err
	}

	turns, err := c.memoryStore.LoadRecentTurns(ctx, sessionID, turnsFetchFromEnv())
	if err != nil {
		return nil, err
	}

	recent := make([]MemoryTurn, 0, len(turns))
	for _, turn := range turns {
		if strings.TrimSpace(turn.Content) == "" {
			continue
		}
		recent = append(recent, turn.toMemoryTurn())
	}

	return &PromptMemory{
		SessionID:   sessionID,
		RecentTurns: recent,
		UserProfile: map[string]any{
			"user_id": userID,
		},
		Preferences: map[string]any{},
		TopicHints:  nil,
	}, nil
}

func (c *IntentClassifier) persistTurnsAsync(ctx context.Context, userID, sessionID, rawQuery string, intentCtx models.IntentContext) {
	if c == nil || c.memoryStore == nil || !c.memoryStore.Enabled() || strings.TrimSpace(sessionID) == "" {
		return
	}
	go func() {
		writeCtx, cancel := context.WithTimeout(ctx, llmTimeoutFromEnv())
		defer cancel()

		ttl := sessionTTLFromEnv()
		maxTurns := turnsMaxFromEnv()
		if err := c.memoryStore.EnsureSession(writeCtx, userID, sessionID, ttl); err != nil {
			log.Printf("[IntentClassifier] ensure redis session failed: %v", err)
			return
		}
		if err := c.memoryStore.AppendTurn(writeCtx, sessionID, StoredTurn{
			Role:    "user",
			Content: rawQuery,
		}, maxTurns, ttl); err != nil {
			log.Printf("[IntentClassifier] append user turn failed: %v", err)
			return
		}
		if strings.TrimSpace(intentCtx.RewrittenIntent) != "" {
			if err := c.memoryStore.AppendTurn(writeCtx, sessionID, StoredTurn{
				Role:       "assistant",
				Content:    intentCtx.RewrittenIntent,
				IntentType: intentCtx.IntentType,
				Entities:   intentCtx.Entities,
			}, maxTurns, ttl); err != nil {
				log.Printf("[IntentClassifier] append assistant turn failed: %v", err)
			}
		}
	}()
}

// RecordTurn 用于在 chat 链路写入真实消息记录，供短期记忆复用。
func (c *IntentClassifier) RecordTurn(ctx context.Context, userID, sessionID string, turn StoredTurn) {
	if c == nil || c.memoryStore == nil || !c.memoryStore.Enabled() || strings.TrimSpace(sessionID) == "" {
		return
	}

	writeCtx, cancel := context.WithTimeout(ctx, llmTimeoutFromEnv())
	defer cancel()

	ttl := sessionTTLFromEnv()
	if err := c.memoryStore.EnsureSession(writeCtx, userID, sessionID, ttl); err != nil {
		log.Printf("[IntentClassifier] ensure redis session failed: %v", err)
		return
	}
	if err := c.memoryStore.AppendTurn(writeCtx, sessionID, turn, turnsMaxFromEnv(), ttl); err != nil {
		log.Printf("[IntentClassifier] append turn failed: %v", err)
	}
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

func parsePaperSearchResponse(raw string) (*llmPaperSearchResponse, error) {
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

	var result llmPaperSearchResponse
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

func mergePaperSearchFields(dst map[string]any, fields map[string]any) {
	if dst == nil || len(fields) == 0 {
		return
	}
	for _, key := range []string{"paper_title", "paper_arxiv_id", "paper_search_query", "paper_method_name"} {
		value, ok := fields[key]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if existing, exists := dst[key]; exists && strings.TrimSpace(fmt.Sprint(existing)) != "" {
			continue
		}
		dst[key] = value
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
	if strings.TrimSpace(rewrittenQuery) == "" {
		return fmt.Sprintf(
			"用户查询: %q\n\n上下文记忆: %s\n\n请优先依据用户原始查询进行意图识别，并参考上下文记忆提升术语一致性，按指定JSON格式输出。",
			rawQuery,
			formatMemoryForPrompt(memory),
		)
	}
	return fmt.Sprintf(
		"用户查询: %q\n\n专业化改写: %q\n\n上下文记忆: %s\n\n请优先依据用户原始查询进行意图识别，并参考改写查询和上下文记忆提升术语一致性，按指定JSON格式输出。",
		rawQuery,
		rewrittenQuery,
		formatMemoryForPrompt(memory),
	)
}

func clampConfidence(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func sessionTTLFromEnv() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("INTENT_SESSION_TTL")); raw != "" {
		if ttl, err := time.ParseDuration(raw); err == nil && ttl > 0 {
			return ttl
		}
	}
	return 7 * 24 * time.Hour
}

func turnsFetchFromEnv() int {
	return envIntWithDefault("INTENT_TURNS_FETCH", 10)
}

func turnsMaxFromEnv() int {
	return envIntWithDefault("INTENT_TURNS_MAX", 30)
}

func llmTimeoutFromEnv() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("INTENT_LLM_TIMEOUT")); raw != "" {
		if timeout, err := time.ParseDuration(raw); err == nil && timeout > 0 {
			return timeout
		}
	}
	return 5 * time.Second
}

func envIntWithDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func buildRewriteUserPrompt(rawQuery string, memory *PromptMemory) string {
	return fmt.Sprintf(
		"用户查询: %q\n\n上下文记忆: %s\n\n请按要求输出改写结果。",
		rawQuery,
		formatMemoryForPrompt(memory),
	)
}

func buildPaperSearchUserPrompt(rawQuery string, memory *PromptMemory) string {
	return fmt.Sprintf(
		"用户查询: %q\n\n上下文记忆: %s\n\n请提取适合论文仓库检索的结构化字段。",
		rawQuery,
		formatMemoryForPrompt(memory),
	)
}
