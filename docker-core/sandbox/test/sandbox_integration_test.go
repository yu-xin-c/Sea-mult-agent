package sandbox_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/yu-xin-c/Sea-mult-agent/docker-core/sandbox"
)

// 集成测试：需要 Docker 运行环境
// 运行: SANDBOX_INTEGRATION_TEST=1 go test -v -run TestIntegration ./sandbox/test/...

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("SANDBOX_INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test. Set SANDBOX_INTEGRATION_TEST=1 to run.")
	}
}

func TestIntegration_SandboxLifecycle(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	cfg := sandbox.DefaultConfig()
	cfg.Runtime = "runc"
	cfg.AuditLogDir = t.TempDir()
	cfg.ExecTimeout = 30 * time.Second

	// 1. 创建沙箱
	box, err := sandbox.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}
	defer box.Close()

	// 2. 验证容器已启动
	containerID := box.Engine().ContainerID()
	if containerID == "" {
		t.Fatal("container ID should not be empty")
	}
	t.Logf("Container created: %s", containerID[:12])

	// 3. 执行简单命令
	result, err := box.Execute(ctx, "echo hello_sandbox")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	t.Logf("Output: %s", result.InferenceSummary)

	// 4. 验证状态持久化 (export → 跨命令保留)
	_, err = box.Execute(ctx, "export TEST_VAR=sandbox_works")
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	result2, err := box.Execute(ctx, "echo $TEST_VAR")
	if err != nil {
		t.Fatalf("echo var failed: %v", err)
	}
	t.Logf("Var output: %s", result2.RawOutput)
}

func TestIntegration_EngineRestartFrom(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	cfg := sandbox.DefaultConfig()
	cfg.Runtime = "runc"
	cfg.AuditLogDir = t.TempDir()
	cfg.ExecTimeout = 30 * time.Second

	box, err := sandbox.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}
	defer box.Close()

	oldID := box.Engine().ContainerID()

	// 使用 docker commit 创建快照
	cli := box.Engine().Client()
	resp, err := cli.ContainerCommit(ctx, oldID, container.CommitOptions{
		Reference: "test-checkpoint",
		Comment:   "integration test snapshot",
	})
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	t.Logf("Committed image: %s", resp.ID[:12])

	// RestartFrom
	newID, err := box.Engine().RestartFrom(ctx, resp.ID)
	if err != nil {
		t.Fatalf("restart from failed: %v", err)
	}
	if newID == oldID {
		t.Error("new container should have different ID")
	}
	t.Logf("Restarted: %s -> %s", oldID[:12], newID[:12])
}
