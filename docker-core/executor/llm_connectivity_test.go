package executor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/config"
)

func TestLLMConnectivity(t *testing.T) {
	if os.Getenv("REAL_INTEGRATION_TEST") == "" && os.Getenv("REAL_LLM_TEST") == "" {
		t.Skip("跳过真实 LLM 连通性测试：请设置 REAL_INTEGRATION_TEST=1 或 REAL_LLM_TEST=1")
	}

	fmt.Println("=== 开始 LLM 连通性与指令解析测试 ===")

	// 1. 加载真实配置
	cfg, err := config.LoadConfig("../config/config.toml")
	if err != nil {
		t.Skipf("跳过测试：未检测到可用 config.toml (%v)", err)
	}

	if cfg.LLM.APIKey == "" || strings.Contains(strings.ToLower(cfg.LLM.APIKey), "your-api-key") {
		t.Skip("跳过测试：未检测到有效的 API Key")
	}

	fmt.Printf("[Config] 正在使用模型: %s\n", cfg.LLM.Model)

	// 2. 初始化真实 Agent
	agent := NewExecutorAgent(cfg.LLM)
	ctx := context.Background()

	// 3. 测试 Plan (规划能力)
	goal := "安装 Nginx 并启动服务"
	fmt.Printf("[Test] 正在测试任务规划能力，目标: %s\n", goal)
	graph, err := agent.Plan(ctx, goal)
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}

	fmt.Printf("[Test] 成功生成 DAG，节点数量: %d\n", len(graph.Nodes))
	for id, node := range graph.Nodes {
		fmt.Printf(" - 节点 [%s]: 指令 %v\n", id, node.Commands)
	}

	// 4. 测试 GenerateStrategies (策略生成能力)
	instruction := "安装 Python3 环境"
	fmt.Printf("[Test] 正在测试策略解析能力，指令: %s\n", instruction)
	strategies, err := agent.GenerateStrategies(ctx, instruction)
	if err != nil {
		t.Fatalf("GenerateStrategies 失败: %v", err)
	}

	fmt.Printf("[Test] 成功生成策略 (%d 种):\n", len(strategies))
	for i, s := range strategies {
		fmt.Printf(" - 方案 %d: %s\n", i+1, s)
	}

	fmt.Println("=== LLM 连通性测试通过 ===")
}
