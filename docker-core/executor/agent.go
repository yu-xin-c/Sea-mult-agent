package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/config"
)

// ExecutorAgent 执行器智能体，负责解析指令和生成执行策略
type ExecutorAgent struct {
	client *openai.Client
	model  string
}

func NewExecutorAgent(cfg config.LLMConfig) *ExecutorAgent {
	oc := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		oc.BaseURL = cfg.BaseURL
	}

	// 增加 HTTP 客户端超时设置，提升网络抗干扰能力
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}
	oc.HTTPClient = httpClient

	return &ExecutorAgent{
		client: openai.NewClientWithConfig(oc),
		model:  cfg.Model,
	}
}

// GenerateStrategies 针对模糊指令生成多种可能的执行策略（竞速模式）
func (a *ExecutorAgent) GenerateStrategies(ctx context.Context, instruction string) ([]string, error) {
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "你是一个资深的 Linux 运维专家。请针对用户的模糊指令，提供 2-3 种不同的实现方案。输出格式：每行一个方案，只输出 shell 命令，不要包含任何 Markdown 格式。",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: instruction,
			},
		},
	})

	if err != nil {
		return nil, err
	}

	// 记录真实 API 调用统计
	fmt.Printf("[LLM Stats] Model: %s | Prompt: %d, Completion: %d, Total: %d\n",
		a.model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)

	content := resp.Choices[0].Message.Content
	// 解析响应内容
	var strategies []string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			// 清理可能包含的 Markdown 标记
			line = strings.Trim(line, "`")
			strategies = append(strategies, line)
		}
	}

	if len(strategies) == 0 {
		return []string{"sh -c " + instruction}, nil // 兜底
	}

	return strategies, nil
}

type planNode struct {
	ID           string   `json:"id"`
	Instruction  string   `json:"instruction"`
	Dependencies []string `json:"dependencies"`
}

type planResponse struct {
	Nodes []planNode `json:"nodes"`
}

// Plan 将高层目标解析为 DAG
func (a *ExecutorAgent) Plan(ctx context.Context, goal string) (*DAG, error) {
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: `你是一个资深的全栈与环境配置专家。请将用户的目标解析为逻辑严密的任务 DAG。
特别注意：
1. 完全没有相互依赖关系的任务，必须将它们并行化（即在 dependencies 中仅依赖公共的上游节点，而不是互相依赖）。
2. 如果需要底层引擎发散出多种方案并进行竞跑测试，请故意将该任务的 "instruction" 置为了空字符串 ""，并将它的 "id" 作为指令描述（如： "安装 redis-server 数据库，需要给出多种实现方案"）。
3. 使用 DEBIAN_FRONTEND=noninteractive 参数以确保自动化安装环境时不被中断。

输出格式要求为纯 JSON (不要包含 markdown 代码块标记):
{
  "nodes": [
    {"id": "setup_base", "instruction": "apt-get update -y", "dependencies": []},
    {"id": "install_python", "instruction": "apt-get install -y python3", "dependencies": ["setup_base"]},
    {"id": "使用最高效的方式安装基础性能工具 htop", "instruction": "", "dependencies": ["setup_base"]}
  ]
}`,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: goal,
			},
		},
	})

	if err != nil {
		return nil, err
	}

	// 记录真实 API 调用统计
	fmt.Printf("[LLM Stats] Model: %s | Prompt: %d, Completion: %d, Total: %d\n",
		a.model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)

	content := resp.Choices[0].Message.Content
	// 清理 Markdown 代码块包裹
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var pr planResponse
	if err := json.Unmarshal([]byte(content), &pr); err != nil {
		return nil, fmt.Errorf("failed to parse plan JSON: %w, content: %s", err, content)
	}

	graph := NewDAG()
	nodeMap := make(map[string]*Node)

	// 首先创建所有节点
	for _, pn := range pr.Nodes {
		var cmds []string
		if pn.Instruction != "" {
			cmds = []string{pn.Instruction}
		}
		node := graph.AddNode(pn.ID, cmds)
		nodeMap[pn.ID] = node
	}

	// 然后建立依赖关系
	for _, pn := range pr.Nodes {
		toNode := nodeMap[pn.ID]
		for _, depID := range pn.Dependencies {
			if fromNode, ok := nodeMap[depID]; ok {
				graph.AddEdge(fromNode, toNode)
			}
		}
	}

	return graph, nil
}
