package sandbox

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Session 管理与容器的 TTY 长连接会话
// 通过 Docker Attach API 劫持 stdin/stdout，实现命令注入和输出捕获
type Session struct {
	engine    *Engine
	conn      io.WriteCloser // TTY stdin（hijacked connection）
	splitter  *StreamSplitter
	envMap    map[string]string // 持久化环境变量映射
	envSynced map[string]string // 已同步到容器的环境变量（避免重复注入）
	mu        sync.Mutex
	timeout   time.Duration
}

// NewSession 通过 ContainerAttach 劫持 TTY，建立持久会话
func NewSession(ctx context.Context, engine *Engine, auditLogDir string, timeout time.Duration) (*Session, error) {
	hijacked, err := engine.Client().ContainerAttach(ctx, engine.ContainerID(),
		container.AttachOptions{
			Stream: true,
			Stdin:  true,
			Stdout: true,
			Stderr: true,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to attach container: %w", err)
	}

	splitter, err := NewStreamSplitter(hijacked.Reader, auditLogDir)
	if err != nil {
		hijacked.Close()
		return nil, err
	}

	// 后台持续分流 TTY 输出
	go splitter.Run()

	// 等待 shell 就绪
	time.Sleep(500 * time.Millisecond)

	return &Session{
		engine:    engine,
		conn:      hijacked.Conn,
		splitter:  splitter,
		envMap:    make(map[string]string),
		envSynced: make(map[string]string),
		timeout:   timeout,
	}, nil
}

// Execute 向 TTY 注入命令并等待输出
// 使用唯一 delimiter 界定命令输出边界
func (s *Session) Execute(ctx context.Context, command string) (*ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 生成唯一 delimiter
	delimiter := fmt.Sprintf("__SANDBOX_EOF_%d__", time.Now().UnixNano())

	// 2. 仅注入新增或变更的环境变量（避免每次重复注入全量）
	for k, v := range s.envMap {
		if synced, ok := s.envSynced[k]; ok && synced == v {
			continue // 已同步，跳过
		}
		envCmd := fmt.Sprintf("export %s=%s\n", k, shellEscape(v))
		if _, err := io.WriteString(s.conn, envCmd); err != nil {
			return nil, fmt.Errorf("failed to inject env var %s: %w", k, err)
		}
		s.envSynced[k] = v
	}

	// 3. 构造包装命令：执行原命令 → 输出 delimiter + 退出码
	// 关键：将命令 stdin 重定向到 /dev/null，防止 apt/dpkg 等子进程吞掉后续 echo 行。
	wrappedCmd := fmt.Sprintf("(%s) </dev/null\necho \"%s:$?\"\n", command, delimiter)
	if _, err := io.WriteString(s.conn, wrappedCmd); err != nil {
		return nil, fmt.Errorf("failed to write command: %w", err)
	}

	// 4. 设置超时
	timeoutCh := make(chan struct{})
	var timeoutOnce sync.Once
	closeTimeout := func() {
		timeoutOnce.Do(func() {
			close(timeoutCh)
		})
	}

	var timer *time.Timer
	if s.timeout > 0 {
		timer = time.AfterFunc(s.timeout, func() {
			closeTimeout()
			// 超时后发送 Ctrl+C 中断命令，而不是杀死沙箱
			io.WriteString(s.conn, "\x03\n")
		})
		defer timer.Stop()
	}

	// 让执行可被上层 context 取消（例如 watchdog 的 command timeout）。
	ctxWatchDone := make(chan struct{})
	defer close(ctxWatchDone)
	go func() {
		select {
		case <-ctx.Done():
			closeTimeout()
			io.WriteString(s.conn, "\x03\n")
		case <-ctxWatchDone:
			return
		}
	}()

	// 5. nudge goroutine：检测 Docker Desktop (Windows) 缓冲问题
	// 当长时间（8秒）无新输出时，发送 \n 触发 bash 输出 prompt，
	// 从而强制 Docker 刷新其缓冲区（包括已卡住的 delimiter echo 输出）
	nudgeDone := make(chan struct{})
	defer close(nudgeDone)
	go func() {
		const nudgeInterval = 8 * time.Second
		ticker := time.NewTicker(nudgeInterval)
		defer ticker.Stop()
		prevCount := s.splitter.LineCount()
		for {
			select {
			case <-ticker.C:
				curCount := s.splitter.LineCount()
				if curCount == prevCount {
					// 8 秒内无新行 → 发送 \n 刷新 Docker 缓冲
					io.WriteString(s.conn, "\n")
				}
				prevCount = curCount
			case <-nudgeDone:
				return
			case <-timeoutCh:
				return
			}
		}
	}()

	// 6. 从 splitter 读取输出直到遇到 delimiter
	output, exitCode := s.splitter.ReadUntilDelimiter(delimiter, timeoutCh)

	// 7. 过滤掉 echo 命令本身的回显和 export 命令的回显
	output = filterEchoLine(output, delimiter)
	output = filterExportEcho(output)

	// 8. 如果是 export 命令，同步更新 envMap
	trimmed := strings.TrimSpace(command)
	if strings.HasPrefix(trimmed, "export ") {
		s.parseAndStoreExport(trimmed)
	}

	return &ExecResult{
		RawOutput:        output,
		InferenceSummary: "", // 由上层 Sandbox.Execute 通过 Truncator 填充
		ExitCode:         exitCode,
	}, nil
}

// SendSignal 向 TTY 发送信号字符（如 Ctrl+C = \x03）
func (s *Session) SendSignal(signal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.conn, signal)
	return err
}

// UpdateEnvMap 批量更新持久化环境变量
func (s *Session) UpdateEnvMap(envs map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range envs {
		s.envMap[k] = v
	}
}

// Close 关闭会话
func (s *Session) Close() error {
	s.splitter.Close()
	return s.conn.Close()
}

// parseAndStoreExport 解析 export 命令，存入 envMap
func (s *Session) parseAndStoreExport(cmd string) {
	// 支持: export KEY=VALUE, export KEY="VALUE", export KEY='VALUE'
	body := strings.TrimPrefix(cmd, "export ")
	parts := strings.SplitN(body, "=", 2)
	if len(parts) == 2 {
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// 去除外层成对引号
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		s.envMap[key] = val
	}
}

// shellEscape 使用单引号包裹值，确保所有 shell 元字符被正确转义
func shellEscape(s string) string {
	// 空字符串需要引号
	if s == "" {
		return "''"
	}
	// 始终使用单引号包裹，转义内部单引号
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// filterEchoLine 过滤掉 echo delimiter 命令的回显行
func filterEchoLine(output string, delimiter string) string {
	lines := strings.Split(output, "\n")
	filtered := make([]string, 0, len(lines))
	echoCmd := fmt.Sprintf("echo \"%s:", delimiter)
	for _, line := range lines {
		if strings.Contains(line, echoCmd) {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

// filterExportEcho 过滤掉 export 命令的 TTY 回显行
func filterExportEcho(output string) string {
	lines := strings.Split(output, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "export ") && strings.Contains(trimmed, "=") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}
