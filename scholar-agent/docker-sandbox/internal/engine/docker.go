package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type NativeDockerEngine struct {
	mu         sync.RWMutex
	mountPaths map[string]string // 记录容器 ID 到宿主机挂载路径的映射
}

type streamChunk struct {
	stream string
	line   string
}

func NewNativeDockerEngine() *NativeDockerEngine {
	return &NativeDockerEngine{
		mountPaths: make(map[string]string),
	}
}

func (e *NativeDockerEngine) GetType() string {
	return "Docker"
}

func (e *NativeDockerEngine) Create(ctx context.Context, image string, mountPath string) (string, error) {
	args := []string{"run", "-d", "--rm"}
	if mountPath != "" {
		normalizedMountPath, err := normalizeDockerMountPath(mountPath)
		if err != nil {
			return "", err
		}
		args = append(args, "-v", fmt.Sprintf("%s:/workspace", normalizedMountPath))
	}
	args = append(args, image, "sleep", "infinity")
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Docker run failed: %v, output: %s", err, string(output))
	}
	containerID := strings.TrimSpace(string(output))
	if mountPath != "" {
		e.mu.Lock()
		e.mountPaths[containerID] = mountPath
		e.mu.Unlock()
	}
	return containerID, nil
}

func normalizeDockerMountPath(mountPath string) (string, error) {
	if strings.TrimSpace(mountPath) == "" {
		return "", nil
	}

	absPath, err := filepath.Abs(mountPath)
	if err != nil {
		return "", fmt.Errorf("resolve mount path failed: %w", err)
	}

	if runtime.GOOS == "windows" {
		absPath = filepath.Clean(absPath)
		absPath = strings.ReplaceAll(absPath, "\\", "/")
	}

	return absPath, nil
}

func (e *NativeDockerEngine) Delete(ctx context.Context, id string) error {
	e.mu.Lock()
	delete(e.mountPaths, id)
	e.mu.Unlock()
	return exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
}

func (e *NativeDockerEngine) ExecutePython(ctx context.Context, id string, code string) (*ExecutionResponse, error) {
	cmd := exec.CommandContext(ctx, "docker", "exec", id, "python3", "-c", code)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	response := &ExecutionResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}

	// 尝试从挂载目录读取生成的图表 (Matplotlib 默认保存路径)
	e.mu.RLock()
	mountPath, ok := e.mountPaths[id]
	e.mu.RUnlock()
	if ok {
		plotPath := filepath.Join(mountPath, "output_plot.png")
		if _, err := os.Stat(plotPath); err == nil {
			imgData, readErr := os.ReadFile(plotPath)
			if readErr == nil {
				response.Images = []string{base64.StdEncoding.EncodeToString(imgData)}
				// 读取后删除，避免下一次执行干扰
				os.Remove(plotPath)
			}
		}
	}

	return response, nil
}

func (e *NativeDockerEngine) ExecutePythonStream(ctx context.Context, id string, code string, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	cmd := exec.CommandContext(ctx, "docker", "exec", id, "python3", "-c", code)
	response, err := e.runStreamingCommand(ctx, cmd, emit)
	if err != nil {
		return nil, err
	}

	e.mu.RLock()
	mountPath, ok := e.mountPaths[id]
	e.mu.RUnlock()
	if ok {
		plotPath := filepath.Join(mountPath, "output_plot.png")
		if _, statErr := os.Stat(plotPath); statErr == nil {
			imgData, readErr := os.ReadFile(plotPath)
			if readErr == nil {
				response.Images = []string{base64.StdEncoding.EncodeToString(imgData)}
				os.Remove(plotPath)
			}
		}
	}

	return response, emit(ExecutionStreamEvent{
		Type:     "final",
		Response: response,
	})
}

func (e *NativeDockerEngine) ExecuteCommand(ctx context.Context, id string, cmdArr []string) (*ExecutionResponse, error) {
	args := append([]string{"exec", id}, cmdArr...)
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	dockerCmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	dockerCmd.Stdout = &stdout
	dockerCmd.Stderr = &stderr
	err := dockerCmd.Run()

	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return &ExecutionResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

func (e *NativeDockerEngine) ExecuteCommandStream(ctx context.Context, id string, cmdArr []string, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	args := append([]string{"exec", id}, cmdArr...)
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	dockerCmd := exec.CommandContext(ctx, "docker", args...)
	response, err := e.runStreamingCommand(ctx, dockerCmd, emit)
	if err != nil {
		return nil, err
	}
	return response, emit(ExecutionStreamEvent{
		Type:     "final",
		Response: response,
	})
}

func (e *NativeDockerEngine) runStreamingCommand(ctx context.Context, cmd *exec.Cmd, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	chunks := make(chan streamChunk, 64)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readPipeLines(stdoutPipe, "stdout", &stdoutBuf, chunks)
	}()
	go func() {
		defer wg.Done()
		readPipeLines(stderrPipe, "stderr", &stderrBuf, chunks)
	}()
	go func() {
		wg.Wait()
		close(chunks)
	}()

	for chunk := range chunks {
		if emitErr := emit(ExecutionStreamEvent{
			Type:    "chunk",
			Stream:  chunk.stream,
			Message: chunk.line,
		}); emitErr != nil {
			return nil, emitErr
		}
	}

	waitErr := cmd.Wait()
	exitCode := 0
	if waitErr != nil {
		if exitError, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return &ExecutionResponse{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
	}, nil
}

func readPipeLines(reader io.Reader, stream string, sink *bytes.Buffer, out chan<- streamChunk) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		sink.WriteString(line)
		sink.WriteByte('\n')
		out <- streamChunk{stream: stream, line: line}
	}
}
