package watchdog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/config"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/executor"
)

// Mock子目录下的真实接口定义在 executor 包中

// MockSandbox 模拟沙箱，支持超时挂起模拟
type MockSandbox struct {
	ExecuteFunc func(cmd string) (string, error)
}

func (s *MockSandbox) Execute(ctx context.Context, cmd string) (string, error) {
	if s.ExecuteFunc != nil {
		return s.ExecuteFunc(cmd)
	}
	return "Mock success", nil
}

func (s *MockSandbox) Close() error { return nil }

// MockCheckpointManager 模拟快照管理
type MockCheckpointManager struct {
	RollbackCalled bool
	LastRollbackID string
}

func (m *MockCheckpointManager) Commit(ctx context.Context, label string) (string, error) {
	return "img-" + label, nil
}

func (m *MockCheckpointManager) Rollback(ctx context.Context, id string) error {
	m.RollbackCalled = true
	m.LastRollbackID = id
	fmt.Printf("[MockCheckpoint] Rolling back to image: %s\n", id)
	return nil
}

func TestWatchdogCircuitBreaker(t *testing.T) {
	cfg := Config{MaxRetryWindow: 3, CommandTimeout: time.Second}
	box := &MockSandbox{
		ExecuteFunc: func(cmd string) (string, error) {
			// 模拟一个总是报错的指令，其输出在语义上是重复的
			return "Error: package not found", fmt.Errorf("exit status 1")
		},
	}
	wd := NewWatchdog(cfg, box, nil, &MockAgent{})

	ctx := context.Background()
	cmd := "apt-get install some-package"

	fmt.Println("--- 场景 1: 语义死循环拦截测试 ---")
	for i := 1; i <= cfg.MaxRetryWindow+1; i++ {
		_, err := wd.Execute(ctx, cmd)
		if err != nil {
			if IsCircuitBroken(err) {
				fmt.Printf("成功拦截！在第 %d 次尝试时触发了熔断。\n", i)
				return
			}
			// 普通的审计失败或执行失败
			fmt.Printf("第 %d 次执行: %v\n", i, err)
		}
	}
	t.Error("熔断器未能按预期触发")
}

func TestWatchdogTimeout(t *testing.T) {
	cfg := Config{MaxRetryWindow: 3, CommandTimeout: 200 * time.Millisecond}
	box := &MockSandbox{
		ExecuteFunc: func(cmd string) (string, error) {
			// 模拟进程挂起
			time.Sleep(1 * time.Second)
			return "Never reached", nil
		},
	}
	wd := NewWatchdog(cfg, box, nil, &MockAgent{})

	ctx := context.Background()
	fmt.Println("\n--- 场景 2: 超时挂起拦截测试 ---")
	_, err := wd.Execute(ctx, "apt-get install without-y")
	if err != nil {
		if IsTimeoutWithHang(err) {
			wdErr := err.(*WatchdogError)
			fmt.Printf("成功检测到挂起！提示词: %s\n", wdErr.Hint)
			return
		}
	}
	t.Errorf("未能检测到超时挂起: %v", err)
}

func TestWatchdogAuditAndRollback(t *testing.T) {
	cfg := Config{MaxRetryWindow: 3, CommandTimeout: time.Second}
	box := &MockSandbox{
		ExecuteFunc: func(cmd string) (string, error) {
			// 模拟环境被破坏（审计会失败）
			return "Segmentation fault (core dumped)", nil
		},
	}
	cp := &MockCheckpointManager{}
	wd := NewWatchdog(cfg, box, cp, &MockAgent{})

	ctx := context.Background()
	fmt.Println("\n--- 场景 3: 审计异常与 Mock 回滚测试 ---")
	_, err := wd.Execute(ctx, "rm -rf /lib")
	if err != nil {
		wdErr, ok := err.(*WatchdogError)
		if ok && wdErr.Type == ErrTypeEnvironmentCorrupted {
			fmt.Printf("审计捕获到异常！错误: %s\n", wdErr.Message)
			// 尝试回滚
			errRoll := wd.RollbackToSafeState(ctx, "last-stable-node")
			if errRoll == nil && cp.RollbackCalled {
				fmt.Println("回滚逻辑已成功触发。")
				return
			}
		}
	}
	t.Errorf("未能按预期触发回滚流程: %v", err)
}

// 新增：集成真实配置的测试
func TestWatchdogWithRealConfig(t *testing.T) {
	fmt.Println("\n--- 场景 4: 真实配置加载与执行测试 ---")
	// 1. 加载真实配置
	fullCfg, err := config.LoadConfig("../config/config.toml")
	if err != nil {
		t.Fatalf("加载配置文件失败: %v", err)
	}

	// 2. 转换为 Watchdog 内部配置
	wdCfg := Config{
		MaxRetryWindow: fullCfg.Watchdog.MaxRetryWindow,
		CommandTimeout: time.Duration(fullCfg.Watchdog.CommandTimeout) * time.Second,
	}
	fmt.Printf("[Config] 加载成功: MaxRetryWindow=%d, Timeout=%v\n", wdCfg.MaxRetryWindow, wdCfg.CommandTimeout)

	// 3. 初始化并测试
	box := &MockSandbox{
		ExecuteFunc: func(cmd string) (string, error) {
			if cmd == "verify_llm" {
				return "Error: dependency conflict detected", nil
			}
			return "Success", nil
		},
	}
	// 使用真实的 ExecutorAgent
	realAgent := executor.NewExecutorAgent(fullCfg.LLM)
	wd := NewWatchdog(wdCfg, box, nil, realAgent)

	ctx := context.Background()
	
	fmt.Println("[Test] 正在模拟一个失败指令以触发真实的 LLM 诊断请求...")
	_, err = wd.Execute(ctx, "verify_llm")
	if err != nil {
		if wdErr, ok := err.(*WatchdogError); ok {
			fmt.Printf("[Success] 捕获到了 Watchdog 错误，类型: %s\n", wdErr.Type)
			fmt.Printf("[LLM Response] 得到的 AI 修复建议: %s\n", wdErr.Hint)
			if strings.Contains(wdErr.Hint, "AI Expert Suggestion") {
				fmt.Println("🎉 验证成功：已确认存在真实的 LLM 请求记录。")
			}
			return
		}
	}
	t.Errorf("未能触发预期的 LLM 诊断流程")
}

// TestWatchdogFullLifeCycle 模拟一个完整的生命周期场景：
// 正常执行 -> 发现挂起并处理 -> 触发电路熔断 -> 环境损坏后通过模拟器回滚
func TestWatchdogFullLifeCycle(t *testing.T) {
	fmt.Println("\n--- 场景 5: 全生命周期模拟验证 (Simulator Based) ---")
	
	sim := NewSimulator()
	cfg := Config{MaxRetryWindow: 2, CommandTimeout: 500 * time.Millisecond}
	wd := NewWatchdog(cfg, sim, sim, &MockAgent{})
	ctx := context.Background()

	// 1. 成功执行
	_, err := wd.Execute(ctx, "ls -l")
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// 2. 模拟挂起
	sim.HangOnKeyword = "apt-get"
	fmt.Println("[Step 2] 模拟一个会挂起的指令...")
	_, err = wd.Execute(ctx, "apt-get install -y vim")
	if err == nil || !IsTimeoutWithHang(err) {
		t.Errorf("Step 2 failed: expected timeout hang, got %v", err)
	}

	// 3. 模拟熔断
	sim.HangOnKeyword = ""
	sim.FailOnKeyword = "fix-me"
	fmt.Println("[Step 3] 模拟连续重复错误以触发熔断...")
	cmd := "fix-me --now"
	for i := 0; i < cfg.MaxRetryWindow+1; i++ {
		_, err = wd.Execute(ctx, cmd)
		if i == cfg.MaxRetryWindow {
			if !IsCircuitBroken(err) {
				t.Errorf("Step 3 failed: expected circuit broken, got %v", err)
			}
		}
	}

	// 4. 模拟环境损坏与回滚
	sim.SegmentFault = true
	fmt.Println("[Step 4] 模拟指令导致段错误并触发模拟回滚...")
	_, err = wd.Execute(ctx, "run_crash_bin")
	if err != nil && IsEnvironmentCorrupted(err) {
		fmt.Println("[Step 4] 审计识别到环境损坏，启动回滚...")
		errRoll := wd.RollbackToSafeState(ctx, "snap-888")
		if errRoll == nil && strings.Contains(sim.CurrentState, "restored") {
			fmt.Println("🎉 全生命周期验证成功：模拟器状态已回滚。")
		} else {
			t.Errorf("回滚验证失败: %v, state: %s", errRoll, sim.CurrentState)
		}
	} else {
		t.Errorf("Step 4 failed: expected environment corrupted, got %v", err)
	}
}

// 辅助 Mock 结构以便运行测试
type MockAgent struct{ executor.Agent }

func (m *MockAgent) Plan(ctx context.Context, goal string) (*executor.DAG, error) {
	return executor.NewDAG(), nil
}
func (m *MockAgent) GenerateStrategies(ctx context.Context, instruction string) ([]string, error) {
	return []string{"mock-advice"}, nil
}
