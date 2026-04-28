package tests

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

// 真人真实集成测试：需要 Docker 环境以及正确的 DeepSeek API Key
// 运行命令: $env:REAL_INTEGRATION_TEST=1; go test -v ./tests -run TestRealIntegration_FullFlow
func TestRealIntegration_FullFlow(t *testing.T) {
	if os.Getenv("REAL_INTEGRATION_TEST") == "" {
		t.Skip("跳过真实集成测试。如需运行，请设置环境变量 REAL_INTEGRATION_TEST=1")
	}

	fmt.Println("\n=== [Real Integration] 开始全组件真实集成测试 (Docker + LLM) ===")

	cfg, err := config.LoadConfig("../config/config.toml")
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if cfg.LLM.APIKey == "" || cfg.LLM.APIKey == "your-api-key" {
		t.Skip("跳过：未检测到有效的 LLM API Key")
	}

	ctx := context.Background()

	sbCfg := sandbox.DefaultConfig()
	sbCfg.Runtime = "runc"
	sbCfg.AuditLogDir = t.TempDir()

	fmt.Println("[Step 1] 正在启动真实 Docker 沙箱...")
	realSandbox, err := sandbox.New(ctx, sbCfg)
	if err != nil {
		t.Fatalf("创建真实沙箱失败: %v", err)
	}
	defer realSandbox.Close()
	fmt.Printf("[Step 1] 沙箱已就绪, ContainerID: %s\n", realSandbox.Engine().ContainerID()[:12])

	fmt.Println("[Step 2] 初始化快照管理器...")
	cpManager := checkpoint.NewManager(realSandbox)

	fmt.Println("[Step 3] 初始化分布式 Agent (DeepSeek)...")
	agent := executor.NewExecutorAgent(cfg.LLM)

	fmt.Println("[Step 4] 组装 Watchdog 守护层...")
	wdCfg := watchdog.Config{
		MaxRetryWindow: cfg.Watchdog.MaxRetryWindow,
		CommandTimeout: time.Duration(cfg.Watchdog.CommandTimeout) * time.Second,
	}
	safeBox := watchdog.NewWatchdog(wdCfg, realSandbox, cpManager, agent)

	fmt.Println("[Step 5] 启动调度引擎 Engine...")
	engine := executor.NewEngine(nil, safeBox, cpManager, agent)

	graph := executor.NewDAG()
	node1 := graph.AddNode("setup_check", []string{"uname -a", "ls /"})
	node2 := graph.AddNode("trigger_diagnostic", []string{"ls /root/secret_vintces_folder"})
	graph.AddEdge(node1, node2)

	fmt.Println("\n[Execution] 引擎开始执行任务图...")
	err = engine.ExecuteGraph(ctx, graph)

	if err != nil {
		fmt.Printf("\n[Result] 引擎捕获到错误 (符合预期): %v\n", err)

		var wdErr *watchdog.WatchdogError
		if errors.As(err, &wdErr) && wdErr.Type == watchdog.ErrTypeEnvironmentCorrupted {
			fmt.Println("--------------------------------------------------")
			fmt.Printf("✅ 验证成功：Watchdog 正确拦截了故障。\n")
			fmt.Printf("故障指令: %s\n", wdErr.Command)
			fmt.Printf("AI 诊断建议: %s\n", wdErr.Hint)
			fmt.Println("--------------------------------------------------")

			fmt.Println("[Step 6] 测试回滚至最近安全节点...")
			latestSnap, ok := cpManager.Registry().GetLatest()
			if !ok {
				t.Fatal("未能在注册表中找到任何快照")
			}
			errRoll := safeBox.RollbackToSafeState(ctx, latestSnap.ImageID)
			if errRoll == nil {
				fmt.Printf("✅ 回滚执行成功 (回滚至: %s)。\n", latestSnap.NodeID)
			} else {
				t.Errorf("回滚失败: %v", errRoll)
			}
		} else {
			t.Errorf("错误类型非预期。期望 ENV_CORRUPTED，实际: %v", err)
		}
	} else {
		t.Error("预期执行应因 node2 失败，但引擎未报错。")
	}
}
