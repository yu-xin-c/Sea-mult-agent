package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StreamSplitter 双路日志流分发器
// 审计轨道：全量落盘（含 ANSI 转义序列）
// 推理轨道：内存缓冲，供 Session 按 delimiter 消费
type StreamSplitter struct {
	reader    io.Reader
	auditFile *os.File

	// 推理轨道缓冲
	mu     sync.Mutex
	lines  []string
	closed bool
	notify chan struct{} // 替代 sync.Cond，可与 channel select 组合
}

// NewStreamSplitter 创建双路日志分发器
func NewStreamSplitter(reader io.Reader, auditLogDir string) (*StreamSplitter, error) {
	if err := os.MkdirAll(auditLogDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create audit log dir: %w", err)
	}

	logFile := filepath.Join(auditLogDir,
		fmt.Sprintf("audit_%d.log", time.Now().UnixNano()))
	f, err := os.Create(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit log file: %w", err)
	}

	ss := &StreamSplitter{
		reader:    reader,
		auditFile: f,
		lines:     make([]string, 0, 256),
		notify:    make(chan struct{}, 1),
	}
	return ss, nil
}

// Run 持续从 TTY 读取输出，分发到审计轨道和推理缓冲
// 应在独立 goroutine 中启动
func (ss *StreamSplitter) Run() {
	buf := make([]byte, 4096)
	var partial string // 处理不完整行

	for {
		n, err := ss.reader.Read(buf)
		if n > 0 {
			chunk := partial + string(buf[:n])
			chunk = strings.ReplaceAll(chunk, "\r", "\n")
			partial = ""

			// 按行切分
			lines := strings.Split(chunk, "\n")

			// 最后一段可能不完整，暂存
			if !strings.HasSuffix(chunk, "\n") {
				partial = lines[len(lines)-1]
				lines = lines[:len(lines)-1]
			}

			for _, line := range lines {
				// 审计轨道：原样写入（包括空行）
				ss.auditFile.WriteString(line + "\n")

				// 推理轨道：追加到内存缓冲（保留空行以维持输出完整性）
				ss.mu.Lock()
				ss.lines = append(ss.lines, line)
				ss.mu.Unlock()
				ss.signal()
			}
		}
		if err != nil {
			// 处理剩余的 partial
			if partial != "" {
				ss.auditFile.WriteString(partial + "\n")
				ss.mu.Lock()
				ss.lines = append(ss.lines, partial)
				ss.mu.Unlock()
				ss.signal()
			}

			ss.mu.Lock()
			ss.closed = true
			ss.mu.Unlock()
			ss.signal()
			break
		}
	}
}

// signal 非阻塞地通知等待方有新数据
func (ss *StreamSplitter) signal() {
	select {
	case ss.notify <- struct{}{}:
	default:
	}
}

// ReadUntilDelimiter 阻塞读取推理缓冲，直到遇到包含 delimiter 的行
// 返回 delimiter 之前的所有输出行和退出码
// delimiter 行格式: "__SANDBOX_EOF_xxx__:exitcode"
func (ss *StreamSplitter) ReadUntilDelimiter(delimiter string, timeoutCh <-chan struct{}) (string, int) {
	var collected []string
	scanFrom := 0

	for {
		ss.mu.Lock()
		// 扫描新到达的行
		for i := scanFrom; i < len(ss.lines); i++ {
			line := ss.lines[i]
			// 容错解析 delimiter：只要出现 "DELIMITER:<int>" 即认为命中。
			// 这样即使该行同时包含 echo 回显，也能正确识别退出码。
			if code, ok := parseDelimiterExitCode(line, delimiter); ok {
				ss.lines = ss.lines[i+1:]
				ss.mu.Unlock()
				return strings.Join(collected, "\n"), code
			}
			collected = append(collected, line)
		}
		scanFrom = len(ss.lines)

		if ss.closed {
			// 流已关闭，返回已收集的内容
			ss.lines = nil
			ss.mu.Unlock()
			return strings.Join(collected, "\n"), -1
		}
		ss.mu.Unlock()

		// 使用 select 同时等待新数据和超时，避免 sync.Cond 无法组合 channel 的问题
		select {
		case <-timeoutCh:
			// 超时：清除已消费的行，避免下次调用产生重复输出
			ss.mu.Lock()
			ss.lines = ss.lines[scanFrom:]
			ss.mu.Unlock()
			return strings.Join(collected, "\n"), -1
		case <-ss.notify:
			// 有新数据到达，继续扫描
		}
	}
}

func parseDelimiterExitCode(line string, delimiter string) (int, bool) {
	key := delimiter + ":"
	idx := strings.LastIndex(line, key)
	if idx == -1 {
		return 0, false
	}
	rest := strings.TrimSpace(line[idx+len(key):])
	if rest == "" {
		return 0, false
	}

	// 仅提取前缀整数，容忍后面紧跟 prompt 文本（例如 "0root@..."）。
	start := 0
	if rest[0] == '+' || rest[0] == '-' {
		start = 1
	}
	end := start
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == start {
		return 0, false
	}
	code, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return code, true
}

// Close 关闭审计日志文件
func (ss *StreamSplitter) Close() error {
	return ss.auditFile.Close()
}

// LineCount 返回当前推理缓冲中的行数（用于检测输出是否停止增长）
func (ss *StreamSplitter) LineCount() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.lines)
}
