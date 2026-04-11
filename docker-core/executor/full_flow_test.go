package executor

import (
	"context"
	"fmt"
	"testing"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/config"
)

func TestEndToEndFullFlow(t *testing.T) {
	fmt.Println("=== 开始全流程端到端测试 (端到端双层并发调度) ===")

	// 1. 加载配置 (模拟从 config/config.toml 加载)
	cfg, err := config.LoadConfig("../config/config.toml")
	if err != nil {
		t.Logf("警告: 加载配置文件失败 (可能未创建): %v", err)
		// 仍然继续，使用默认 Mock 逻辑
		cfg = &config.Config{
			LLM: config.LLMConfig{
				Model: "mock-model",
			},
			Resource: config.ResourceConfig{
				MaxConcurrentIO: 5,
				MaxGPUTasks:     1,
			},
		}
	} else {
		fmt.Printf("[Config] 已加载模型配置: %s\n", cfg.LLM.Model)
	}

	// 2. 初始化组件
	ctx := context.Background()
	box := &MockSandbox{}
	cp := &MockCheckpoint{}
	gk := NewGatekeeper(ResourceConfig{
		MaxConcurrentIO: cfg.Resource.MaxConcurrentIO,
		MaxGPUTasks:     cfg.Resource.MaxGPUTasks,
	})

	// 3. 定义 Mock 智能体逻辑
	mockAgent := &MockAgent{
		PlanFunc: func(goal string) (*DAG, error) {
			fmt.Printf("[Agent] 正在解析目标: %s\n", goal)
			g := NewDAG()
			// 构建一个复杂的 DAG
			// base -> python_env -> [numpy, pandas] -> train
			base := g.AddNode("base_install", []string{"apt-get update", "apt-get install -y python3-pip"})
			pythonEnv := g.AddNode("python_env", []string{"pip3 install --upgrade pip"})
			numpy := g.AddNode("install_numpy", []string{"pip3 install numpy"})
			pandas := g.AddNode("install_pandas", []string{"pip3 install pandas"})
			train := g.AddNode("run_train", []string{"python3 -c 'import numpy; import pandas; print(\"Train Success\")'"})

			g.AddEdge(base, pythonEnv)
			g.AddEdge(pythonEnv, numpy)
			g.AddEdge(pythonEnv, pandas)
			g.AddEdge(numpy, train)
			g.AddEdge(pandas, train)

			return g, nil
		},
		GenerateStrategiesFunc: func(instruction string) ([]string, error) {
			fmt.Printf("[Agent] 为指令生成策略: %s\n", instruction)
			return []string{"sh -c '" + instruction + "'"}, nil
		},
	}

	engine := NewEngine(gk, box, cp, mockAgent)

	// 4. 用户输入目标
	userGoal := "安装 Python 环境并运行训练脚本"
	fmt.Printf("[User] 指令: %s\n", userGoal)

	// 5. 第一阶段: 规划 (Plan)
	graph, err := mockAgent.Plan(ctx, userGoal)
	if err != nil {
		t.Fatalf("规划失败: %v", err)
	}
	fmt.Println("[Planner] 已生成 DAG 任务图")

	// 6. 第二阶段: 执行 (Execute)
	fmt.Println("[Engine] 开始并发执行任务图...")
	err = engine.ExecuteGraph(ctx, graph)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	fmt.Println("=== 全流程端到端测试完成 ===")
}
