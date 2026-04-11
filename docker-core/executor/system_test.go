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

func TestSystemWithWatchdogAndRealLLM(t *testing.T) {
	if os.Getenv("REAL_INTEGRATION_TEST") == "" {
		t.Skip("跳过真实集成测试。设置 REAL_INTEGRATION_TEST=1 以运行。")
	}

	fmt.Println("=== 开始系统级集成与真实 LLM 验证测试 ===")

	// 1. 加载真实配置
	cfg, err := config.LoadConfig("../config/config.toml")
	if err != nil {
		t.Fatalf("加载配置文件失败: %v", err)
	}

	if cfg.LLM.APIKey == "" || cfg.LLM.APIKey == "your-api-key" {
		t.Skip("跳过测试：未检测到有效的 API Key")
	}

	ctx := context.Background()

	// 2. 初始化真实沙箱
	sbCfg := sandbox.DefaultConfig()
	sbCfg.Runtime = "runc"
	sbCfg.AuditLogDir = t.TempDir()
	
	realSandbox, err := sandbox.New(ctx, sbCfg)
	if err != nil {
		t.Fatalf("创建真实沙箱失败: %v", err)
	}
	defer realSandbox.Close()

	// 3. 初始化真实 Checkpoint 管理器
	cpManager := checkpoint.NewManager(realSandbox)

	// 4. 初始化真实 Agent
	realAgent := executor.NewExecutorAgent(cfg.LLM)

	// 5. 使用 Watchdog 包装沙箱
	wdCfg := watchdog.Config{
		MaxRetryWindow: cfg.Watchdog.MaxRetryWindow,
		CommandTimeout: time.Duration(cfg.Watchdog.CommandTimeout) * time.Second,
	}
	safeBox := watchdog.NewWatchdog(wdCfg, realSandbox, cpManager, realAgent)

	// 6. 初始化引擎，并将 Watchdog 作为 Sandbox 传入
	engine := executor.NewEngine(nil, safeBox, cpManager, realAgent)

	// 7. 构造一个包含故障命令的任务图 (ls 一个不存在的目录来模拟故障)
	graph := executor.NewDAG()
	node1 := graph.AddNode("setup", []string{"ls /"})
	node2 := graph.AddNode("broken_task", []string{"ls /non_existent_folder_system_test"})
	graph.AddEdge(node1, node2)

	fmt.Println("[System] 启动引擎执行包含预设故障的任务图...")
	
	// 8. 执行并观察 Watchdog 是否拦截并调用 LLM
	err = engine.ExecuteGraph(ctx, graph)
	
	if err != nil {
		fmt.Printf("[System] 引擎捕获到预期失败: %v\n", err)
		// 检查错误是否包含来自 LLM 的修复建议
		var wdErr *watchdog.WatchdogError
		if errors.As(err, &wdErr) && wdErr.Type == watchdog.ErrTypeEnvironmentCorrupted {
			fmt.Println("🎉 验证成功：Watchdog 拦截了故障，并通过真实 LLM 获得了诊断建议。")
			fmt.Printf("AI 建议: %s\n", wdErr.Hint)
			return
		}
	} else {
		t.Error("预期执行失败但成功了")
	}

	t.Errorf("未能确认 LLM 的真实诊断流程")
}
