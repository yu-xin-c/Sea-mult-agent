🔍 Go Agent Search Tools
基于 Go 实现的 Agent 工具调用框架，封装五类搜索能力，完全遵循 LLM Function Calling 规范（兼容 OpenAI / Anthropic tool_use 格式）。

🌟 核心设计理念

统一接口，开箱即用：所有搜索引擎实现同一个 Tool 接口，Agent 无需关心底层差异，通过 Registry 按名称路由即可。
JSON Schema 自描述：每个工具通过 Definition() 返回标准 JSON Schema，可直接作为 LLM 请求的 tools 参数传入，无需额外配置。
内外分层，职责清晰：Tavily、DuckDuckGo 等底层引擎对 LLM 不可见，LLM 只能调用语义层工具（search_web、deep_research、ai_answer），避免决策混乱。
Context 全链路透传：所有 Execute 调用均携带 context.Context，支持超时控制与请求取消，生产级可用。


🛠️ 核心接口
Tool 接口
gotype Tool interface {
    Definition() ToolDefinition                                          // 返回 JSON Schema，供 LLM 决策
    Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error)
}
ToolDefinition — 工具描述
gotype ToolDefinition struct {
    Name        string      `json:"name"`
    Description string      `json:"description"` // LLM 靠这句话决定要不要调用
    Parameters  interface{} `json:"parameters"`  // JSON Schema，LLM 靠这个填参
}
ToolResponse — 统一返回
gotype ToolResponse struct {
    Content string `json:"content"` // 直接追加到 LLM 上下文的 tool 消息
}

📦 工具列表
1. search_web — 通用网络搜索

适用场景：查询互联网资料、技术文档、概念解释等需要外部信息的任务
底层实现：Tavily（主） → DuckDuckGo（兜底）聚合
是否需要 Key：Tavily 需要

入参 Schema：
json{
  "query": "搜索关键词或问题"
}

2. deep_research — 深度多轮研究

适用场景：复杂问题、多角度分析、需要深入调研的任务
底层实现：自动拆分为「原理」「实现」「示例」三次 Tavily 查询，融合返回
是否需要 Key：Tavily 需要

与 search_web 的区别：内部多轮检索，结果更全面，但耗时更长。
入参 Schema：
json{
  "query": "研究主题"
}

3. ai_answer — AI 直接答案

适用场景：不需要原始资料，只要结论；快速问答场景
底层实现：Perplexity API（sonar 模型），返回带引用的综合答案
是否需要 Key：是

入参 Schema：
json{
  "query": "问题或关键词"
}

🏗️ 架构总览
用户输入
  └─► LLM 推理（拿到 ToolDefinition JSON Schema）
        ├─ 决定调用工具
        │    └─► Executor.Execute(name, input)
        │              └─► Registry.Get(name)
        │                      ├─ search_web       → Tavily + DuckDuckGo 聚合
        │                      ├─ deep_research    → 多轮 Tavily 融合
        │                      └─ ai_answer        → Perplexity 直接答案
        │
        └─ 直接输出最终答案（无工具调用）
底层引擎（对 LLM 不可见）
引擎说明tavilySearch(query)高质量结构化结果，带相关度分数duckduckgoSearch(query)免费、隐私友好，无需 Key

🚀 快速开始
注册工具
goregistry := NewRegistry()
registry.Register(SearchTool{})
registry.Register(DeepResearchTool{})
registry.Register(AIAnswerTool{})

executor := NewExecutor(registry)
获取工具描述（传给 LLM）
go// 直接放进 LLM 请求的 tools 字段
fmt.Println(registry.DefinitionsJSON())
执行工具（Agent 循环内）
goresult, err := executor.Execute(
    ctx,
    resp.ToolCall.Name,       // LLM 返回的工具名
    resp.ToolCall.Arguments,  // LLM 返回的参数 (json.RawMessage)
)
Agent 循环示例
gofor i := 0; i < 5; i++ {
    resp := LLM(messages)

    if resp.ToolCall != nil {
        result, _ := executor.Execute(ctx, resp.ToolCall.Name, resp.ToolCall.Arguments)
        messages = append(messages, Message{Role: "tool", Content: result.Content})
        continue
    }

    // 无工具调用 → 最终答案
    fmt.Println(resp.Content)
    break
}
消息角色约定：
Role含义user用户输入assistantLLM 回复tool工具执行结果

🔧 扩展新工具
实现 Tool 接口，Register 即生效，LLM 自动感知：
gotype MyTool struct{}

func (m MyTool) Definition() ToolDefinition {
    return ToolDefinition{
        Name:        "my_tool",
        Description: "工具用途说明，LLM 靠这句话决定要不要调用",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "query": map[string]interface{}{"type": "string"},
            },
            "required": []string{"query"},
        },
    }
}

func (m MyTool) Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error) {
    // 实现逻辑
    return ToolResponse{Content: "结果"}, nil
}

registry.Register(MyTool{})

🧠 工具选择建议（答辩要点）
Q：三个工具分别适合什么场景？

search_web：通用首选。查概念、查文档、找资料，速度快，双引擎兜底保证可用性。
deep_research：深度任务。写报告、多角度研究，内部自动拆分查询，结果更全面。
ai_answer：直接结论。只要答案不要来源，Perplexity 实时检索 + LLM 综合，省去 Agent 二次推理。

Q：为什么底层引擎不直接暴露给 LLM？
LLM 的决策能力有限，让它直接选择 tavily 还是 duckduckgo 会引入不必要的歧义。语义层工具（search_web 等）对 LLM 描述的是"做什么"，底层引擎负责"怎么做"，分层设计让 LLM 专注推理，工程侧保留调度控制权。

📋 依赖
仅使用 Go 标准库，无外部依赖。生产环境接入真实 API 时，只需在 Execute 内将 mock 函数替换为 HTTP 请求，接口签名不变。
