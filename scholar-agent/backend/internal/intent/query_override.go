package intent

import (
	"context"
	"strings"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

type QueryOverride struct {
	Query    string
	Override string
}

var defaultQueryOverrides = []QueryOverride{
	{Query: "帮我", Override: ""},
	{Query: "帮忙", Override: ""},
	{Query: "麻烦你", Override: ""},
	{Query: "请你", Override: ""},
	{Query: "请问", Override: ""},
	{Query: "一下", Override: ""},
	{Query: "下", Override: ""},
	{Query: "有没有", Override: "搜索"},
	{Query: "找一下", Override: "搜索"},
	{Query: "查一下", Override: "搜索"},
	{Query: "检索一下", Override: "搜索"},
	{Query: "文献", Override: "论文"},
	{Query: "paper", Override: "papers"},
}

var queryTrimTokens = []string{"。", "？", "！", "，", ",", ".", "?", "!", "  "}

type QueryRewriter interface {
	Rewrite(ctx context.Context, query string) (string, error)
}

type LLMQueryRewriter struct {
	chatModel *openaimodel.ChatModel
}

type QueryRewriteConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

func NewLLMQueryRewriter(cfg QueryRewriteConfig) (*LLMQueryRewriter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, nil
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}

	model, err := openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
	})
	if err != nil {
		return nil, err
	}
	return &LLMQueryRewriter{chatModel: model}, nil
}

func (r *LLMQueryRewriter) Rewrite(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil
	}
	if r == nil || r.chatModel == nil {
		return query, nil
	}
	prompt := "# 角色\n你是一个科研 Query 重写助手。你的任务是将用户输入重写为更学术、规范、清晰的形式，但**绝对不能改变用户想要执行的动作**。\n\n# 动作意图保留规则（最重要）\n用户输入中隐含的动作类型必须原样保留，例如：\n- “找”、“搜索”、“检索”、“查”、“下载” → 重写后必须保留为“检索”、“查找”等动作词，不能换成“探讨”、“研究”、“分析”。\n- “总结”、“概括”、“提炼” → 保留为“总结”、“概括”。\n- “推导”、“证明”、“计算” → 保留。\n- “画”、“绘制”、“可视化” → 保留。\n- “解释”、“什么是”、“含义” → 保留为“解释”。\n\n# 其他重写规则（在不改变动作的前提下）\n1. 口语转正式：“帮我找一下” → “请检索”；“有没有……文章” → “是否存在关于……的文献”。\n2. 修正明显笔误或简称，但必须基于语义和常识，不能改变所指对象。例如 `all in need` → `Attention Is All You Need`（一篇论文名），而不是解释成“各类需求”。\n3. 优化句式，去掉冗余，保持简洁。\n4. 不要添加原文没有的新信息或新问题。\n\n# 输出要求\n只输出重写后的纯文本，不加解释、引号、JSON。\n\n# 示例\n\n**用户输入**：帮我找一下论文 Transformer all in need  \n**正确输出**：请检索关于 Transformer 的论文《Attention Is All You Need》。\n\n**用户输入**：我觉得这个催化剂效果挺好的，我们做了好几轮实验，温度从100升到200，转化率从30%升到了差不多80%。  \n**输出**：该催化剂表现出显著的催化活性。在温度从100°C升至200°C的条件下，经过多轮实验，转化率从约30%提高至约80%。\n\n**用户输入**：跑一下t检验  \n**输出**：对数据执行 t 检验。\n\n**用户输入**：这个实验部分怎么写比较好  \n**输出**：如何优化实验部分的撰写方式？\n\n# 现在请重写以下用户输入：" + query

	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	msg, err := r.chatModel.Generate(callCtx, []*schema.Message{
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		return "", err
	}
	rewritten := strings.TrimSpace(msg.Content)
	rewritten = strings.Trim(rewritten, "`\"'")
	rewritten = strings.Join(strings.Fields(rewritten), " ")
	if rewritten == "" {
		return query, nil
	}
	return rewritten, nil
}

func RewriteUserQuery(query string) string {
	normalized := strings.TrimSpace(query)
	if normalized == "" {
		return ""
	}

	for _, token := range queryTrimTokens {
		normalized = strings.ReplaceAll(normalized, token, " ")
	}
	normalized = strings.Join(strings.Fields(normalized), " ")

	for _, item := range defaultQueryOverrides {
		normalized = strings.ReplaceAll(normalized, item.Query, item.Override)
	}

	normalized = strings.Join(strings.Fields(normalized), " ")
	if normalized == "" {
		return strings.TrimSpace(query)
	}
	return normalized
}
