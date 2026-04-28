package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Tool基础结构

type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type ToolResponse struct {
	Content string `json:"content"`
}

type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error)
}

// Regist

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Definition().Name] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Definitions() []ToolDefinition {
	var defs []ToolDefinition
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

func (r *Registry) DefinitionsJSON() string {
    bs, _ := json.MarshalIndent(r.Definitions(), "", "  ")
    return string(bs)
}
// Executor

type Executor struct {
	registry *Registry
}

func NewExecutor(r *Registry) *Executor {
	return &Executor{registry: r}
}

func (e *Executor) Execute(ctx context.Context, name string, input json.RawMessage) (ToolResponse, error) {

	tool, ok := e.registry.Get(name)
	if !ok {
		return ToolResponse{}, fmt.Errorf("tool not found")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	log.Println("🔧 调用工具:", name)

	return tool.Execute(ctx, input)
}
//

//  SearchTool

type SearchTool struct{}

type SearchInput struct {
	Query string `json:"query"`
}

func (s SearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "search_web",
		Description: "用于查询互联网资料、技术文档、概念解释等。当你需要获取外部信息时使用",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索问题或关键词",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (s SearchTool) Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error) {
	var req SearchInput
	json.Unmarshal(input, &req)

	if req.Query == "" {
		return ToolResponse{}, fmt.Errorf("query empty")
	}

	// 优先 Tavily → fallback Duck
	res1 := tavilyMock(req.Query)
	res2 := duckMock(req.Query)

	final := fmt.Sprintf("【综合搜索结果】\n%s\n%s", res1, res2)

	return ToolResponse{
		Content: final,
	}, nil
}

//  DeepResearchTool

type DeepResearchTool struct{}

func (d DeepResearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "deep_research",
		Description: "用于复杂问题、深入分析、多角度研究。比普通搜索更深入",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type": "string",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (d DeepResearchTool) Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error) {

	var req SearchInput
	json.Unmarshal(input, &req)

	// 模拟多轮搜索 + 融合
	res1 := tavilyMock(req.Query + " 原理")
	res2 := tavilyMock(req.Query + " 实现")
	res3 := duckMock(req.Query + " 示例")

	final := fmt.Sprintf("【深度研究】\n%s\n%s\n%s", res1, res2, res3)

	return ToolResponse{
		Content: final,
	}, nil
}

//  AIAnswerTool

type AIAnswerTool struct{}

func (a AIAnswerTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "ai_answer",
		Description: "直接返回AI生成的答案，适合不需要原始资料，只要结论的场景",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type": "string",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (a AIAnswerTool) Execute(ctx context.Context, input json.RawMessage) (ToolResponse, error) {

	var req SearchInput
	json.Unmarshal(input, &req)

	// 👉 这里可以接 Perplexity API
	mock := fmt.Sprintf("【AI总结】关于 %s 的直接结论：这是一个容错机制...", req.Query)

	return ToolResponse{
		Content: mock,
	}, nil
}

// 内部“搜索引擎”（不暴露给LLM）

func tavilyMock(query string) string {
	return fmt.Sprintf("Tavily结果：%s 的高质量资料...", query)
}

func duckMock(query string) string {
	return fmt.Sprintf("Duck结果：%s 的基础信息...", query)
}