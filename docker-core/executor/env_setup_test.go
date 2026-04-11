package executor_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/checkpoint"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/config"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/executor"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/sandbox"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/watchdog"
)

// 全真实环境搭建测试：安装 Python3 并配置 PyTorch (轻量化验证)
// 命令行运行: $env:REAL_INTEGRATION_TEST=1; go test -v -timeout 60m ./executor/env_setup_test.go
func TestRealIntegration_AI_EnvSetup(t *testing.T) {
	if os.Getenv("REAL_INTEGRATION_TEST") == "" {
		t.Skip("跳过真实环境实验。设置 REAL_INTEGRATION_TEST=1 以运行。")
	}

	fmt.Println("\n🚀 === [Full Real Flow] 开始 AI 驱动的 PyTorch 环境安装验证 ===")

	// 1. 加载配置与初始化模块
	cfg, err := config.LoadConfig("../config/config.toml")
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	sbCfg := sandbox.DefaultConfig()
	sbCfg.Runtime = "runc"
	sbCfg.AuditLogDir = t.TempDir()
	
	realSandbox, err := sandbox.New(ctx, sbCfg)
	if err != nil {
		t.Fatalf("创建沙箱失败: %v", err)
	}
	defer realSandbox.Close()

	cpManager := checkpoint.NewManager(realSandbox)
	agent := executor.NewExecutorAgent(cfg.LLM)
	
	wdCfg := watchdog.Config{
		MaxRetryWindow: cfg.Watchdog.MaxRetryWindow,
		CommandTimeout: 1800 * time.Second, // 增加到 30 分钟，确保巨大的 CUDA 依赖可以下载完成
	}
	safeBox := watchdog.NewWatchdog(wdCfg, realSandbox, cpManager, agent)
	engine := executor.NewEngine(nil, safeBox, cpManager, agent)
	engine.NodeTimeout = 45 * time.Minute // 扩大单节点执行的总超时时间

	// 2. 模拟高层目标：配置全栈开发环境，测试并行分支与多方案竞跑触发机制
	goal := `在 Ubuntu 环境中配置完整的全栈与监控环境，具体要求：
1. 基础依赖：必须最先更新 apt 源。
2. 并行分支A：非交互模式安装 python3 和 python3-pip。
3. 并行分支B：使用最高效、稳定的方式安装 redis-server 数据库（请留空 instruction 测试多方案并发竞跑逻辑）。
4. 汇总验证：在所有分支执行完成后，执行 "python3 --version && redis-server --version" 进行安全冒烟测试。`
	fmt.Printf("[Goal] %s\n", goal)

	// 3. AI 规划流程
	fmt.Println("[Agent] 正在调用 AI 进行任务分解与规划 (Plan)...")
	graph, err := agent.Plan(ctx, goal)
	if err != nil {
		t.Fatalf("AI 规划失败: %v", err)
	}

	fmt.Println("[Plan] AI 生成的任务节点:")
	for _, node := range graph.Nodes {
		cmdStr := "<空指令，等待底层多方案并发竞跑>"
		if len(node.Commands) > 0 {
			cmdStr = node.Commands[0]
		}
		fmt.Printf(" - 节点 [%s]: %s (依赖: %v)\n", node.ID, cmdStr, getDepIDs(node))
	}

	// 4. 引擎执行流程 (带有重试逻辑，应对网络波动)
	fmt.Println("\n[Execution] 引擎开始执行 AI 规划的任务流...")
	start := time.Now()
	
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err = engine.ExecuteGraph(ctx, graph)
		if err == nil {
			break
		}
		fmt.Printf("\n❌ [Retry] 任务执行失败 (尝试 %d/%d): %v\n", i+1, maxRetries, err)
		
		// 如果是 Watchdog 错误，打印详细 Hint
		var wdErr *watchdog.WatchdogError
		if errors.As(err, &wdErr) {
			fmt.Printf("   - 故障指令: %s\n", wdErr.Command)
			fmt.Printf("   - 诊断类型: %s\n", wdErr.Type)
			fmt.Printf("   - AI 建议: %s\n", wdErr.Hint)
		}

		if i < maxRetries-1 {
			fmt.Println("[Retry] 等待 10 秒后重试...")
			time.Sleep(10 * time.Second)
		}
	}

	if err != nil {
		t.Fatalf("在 %d 次尝试后仍然执行失败: %v", maxRetries, err)
	}
	fmt.Printf("[Success] 任务流执行完成，耗时: %v\n", time.Since(start))

	// 5. 验证快照仓库
	snaps := cpManager.Registry().All()
	fmt.Printf("\n[Checkpoint] 共捕获并注册了 %d 个环境快照镜像:\n", len(snaps))
	for _, s := range snaps {
		fmt.Printf(" - 节点 [%s] -> 镜像 [%s]\n", s.NodeID, s.ImageID[:12])
	}

	if len(snaps) == 0 {
		t.Error("预期应生成快照镜像，但注册表为空")
	}

	fmt.Println("\n🎉 === 全真实环境搭建实验验证成功！所有模块连接正常且逻辑正确。 ===")
}

func getDepIDs(n *executor.Node) []string {
	var ids []string
	for _, d := range n.Dependencies {
		ids = append(ids, d.ID)
	}
	return ids
}
