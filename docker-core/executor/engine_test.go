package executor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/config"
)

// 原有的 Mock 已经移动到 mocks_test.go 中

func TestFullFlow(t *testing.T) {
	ctx := context.Background()

	// 1. 初始化 Mock 组件
	box := &MockSandbox{
		ExecuteFunc: func(cmd string) (string, error) {
			fmt.Printf("[Mock Sandbox] Executing: %s\n", cmd)
			time.Sleep(100 * time.Millisecond) // 模拟执行耗时
			return "Success: " + cmd, nil
		},
	}
	cp := &MockCheckpoint{}
	gk := NewGatekeeper(ResourceConfig{MaxConcurrentIO: 5, MaxGPUTasks: 1})

	// 这里不真正调用 DeepSeek，因为需要 API Key，但在代码中已经集成了
	// 我们模拟一个 Agent
	agent := NewExecutorAgent(config.LLMConfig{APIKey: "mock-key", Model: "deepseek-chat"})

	engine := NewEngine(gk, box, cp, agent)

	// 2. 构建模拟 DAG
	// base -> node1 -> node3
	//      -> node2 -> node3
	graph := NewDAG()
	base := graph.AddNode("base", []string{"apt update"})
	n1 := graph.AddNode("node1", []string{"pip install numpy", "conda install numpy"}) // 竞速
	n2 := graph.AddNode("node2", []string{"apt install git"})
	n3 := graph.AddNode("node3", []string{"python train.py"})

	graph.AddEdge(base, n1)
	graph.AddEdge(base, n2)
	graph.AddEdge(n1, n3)
	graph.AddEdge(n2, n3)

	// 3. 执行
	fmt.Println("开始全流程执行测试...")
	err := engine.ExecuteGraph(ctx, graph)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	fmt.Println("全流程执行测试完成！")
}
