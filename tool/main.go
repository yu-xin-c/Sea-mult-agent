package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// LLM结构

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type LLMResponse struct {
	Content  string
	ToolCall *ToolCall
}

// 模拟 LLM（先用这个能跑）
// 后面你再换成 DeepSeek

func FakeLLM(messages []Message) LLMResponse {

	last := messages[len(messages)-1].Content

	// 简单规则模拟 LLM 决策
	if len(messages) == 1 {
		args, err := json.Marshal(map[string]string{"query": last})
		if err != nil {
			args = json.RawMessage(`{"query":""}`)
		}
		return LLMResponse{
			ToolCall: &ToolCall{
				Name:      "search_web",
				Arguments: json.RawMessage(args),
			},
		}
	}

	// 第二轮直接总结
	return LLMResponse{
		Content: "总结如下：Flink checkpoint 是一种用于容错的状态快照机制，通过周期性保存状态实现故障恢复。",
	}
}

func main() {

	// 初始化 Tool
	registry := NewRegistry()

	registry.Register(SearchTool{})
	registry.Register(DeepResearchTool{})
	registry.Register(AIAnswerTool{})

	executor := NewExecutor(registry)

	// 用户输入
	messages := []Message{
		{Role: "user", Content: "帮我解释一下Flink checkpoint机制"},
	}

	// Agent循环
	for i := 0; i < 5; i++ {

		fmt.Println("\n 第", i+1, "轮推理")

		resp := FakeLLM(messages)

		// ===== 需要调用 Tool =====
		if resp.ToolCall != nil {

			fmt.Println(" LLM决定调用工具:", resp.ToolCall.Name)

			result, err := executor.Execute(
				context.Background(),
				resp.ToolCall.Name,
				resp.ToolCall.Arguments,
			)

			if err != nil {
				log.Fatal(err)
			}

			fmt.Println("Tool返回:")
			fmt.Println(result.Content)

			// 把 Tool 结果喂回 LLM
			messages = append(messages, Message{
				Role:    "tool",
				Content: result.Content,
			})

			continue
		}

		// 最终回答
		fmt.Println("\n 最终答案：")
		fmt.Println(resp.Content)
		break
	}
}