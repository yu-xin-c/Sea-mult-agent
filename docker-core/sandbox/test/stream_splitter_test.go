package sandbox_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/sandbox"
)

func TestStreamSplitter_BasicFlow(t *testing.T) {
	// 模拟 TTY 输出流
	content := "hello world\n__SANDBOX_EOF_123__:0\n"
	reader := strings.NewReader(content)

	dir := t.TempDir()
	ss, err := sandbox.NewStreamSplitter(reader, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	go ss.Run()

	// 等待一点时间让 goroutine 读取
	time.Sleep(100 * time.Millisecond)

	timeoutCh := make(chan struct{})
	time.AfterFunc(2*time.Second, func() { close(timeoutCh) })

	output, exitCode := ss.ReadUntilDelimiter("__SANDBOX_EOF_123__", timeoutCh)

	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(output, "hello world") {
		t.Errorf("expected 'hello world' in output, got: %s", output)
	}
}

func TestStreamSplitter_NonZeroExitCode(t *testing.T) {
	content := "error occurred\n__SANDBOX_EOF_456__:1\n"
	reader := strings.NewReader(content)

	ss, err := sandbox.NewStreamSplitter(reader, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	go ss.Run()
	time.Sleep(100 * time.Millisecond)

	timeoutCh := make(chan struct{})
	time.AfterFunc(2*time.Second, func() { close(timeoutCh) })

	_, exitCode := ss.ReadUntilDelimiter("__SANDBOX_EOF_456__", timeoutCh)
	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}
}

func TestStreamSplitter_Timeout(t *testing.T) {
	// 使用一个永远不关闭的 reader 来模拟超时场景
	pr, _ := io.Pipe()

	ss, err := sandbox.NewStreamSplitter(pr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	go ss.Run()

	timeoutCh := make(chan struct{})
	// 立即超时
	close(timeoutCh)

	_, exitCode := ss.ReadUntilDelimiter("__NEVER__", timeoutCh)
	if exitCode != -1 {
		t.Errorf("expected exit code -1 on timeout, got %d", exitCode)
	}
}
