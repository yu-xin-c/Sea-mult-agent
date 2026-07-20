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
	"regexp"
	"strings"
	"sync"
	"time"
)

var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

type NativeDockerEngine struct {
	mu         sync.RWMutex
	mountPaths map[string]string // 记录容器 ID 到宿主机挂载路径的映射
}

type streamChunk struct {
	stream string
	line   string
}

type DockerHealth struct {
	Available           bool   `json:"available"`
	Command             string `json:"command"`
	ServerVersion       string `json:"server_version,omitempty"`
	GPURequest          string `json:"gpu_request,omitempty"`
	GPURuntimeAvailable bool   `json:"gpu_runtime_available"`
	Error               string `json:"error,omitempty"`
}

func NewNativeDockerEngine() *NativeDockerEngine {
	return &NativeDockerEngine{
		mountPaths: make(map[string]string),
	}
}

func (e *NativeDockerEngine) Health(ctx context.Context) DockerHealth {
	command := dockerCommand()
	gpuRequest := dockerGPURequest()
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	output, err := exec.CommandContext(checkCtx, command, "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		return DockerHealth{
			Available:  false,
			Command:    command,
			GPURequest: gpuRequest,
			Error:      fmt.Sprintf("Docker CLI or daemon is unavailable: %v, output: %s", err, strings.TrimSpace(string(output))),
		}
	}

	health := DockerHealth{
		Available:     true,
		Command:       command,
		ServerVersion: strings.TrimSpace(string(output)),
		GPURequest:    gpuRequest,
	}
	if gpuRequest == "" {
		return health
	}

	runtimes, runtimeErr := exec.CommandContext(checkCtx, command, "info", "--format", "{{json .Runtimes}}").CombinedOutput()
	health.GPURuntimeAvailable = runtimeErr == nil && strings.Contains(string(runtimes), `"nvidia"`)
	if !health.GPURuntimeAvailable {
		health.Available = false
		health.Error = fmt.Sprintf("GPU sandbox requested (%s), but the NVIDIA Docker runtime is unavailable", gpuRequest)
	}
	return health
}

func dockerGPURequest() string {
	request := strings.TrimSpace(os.Getenv("SANDBOX_DOCKER_GPUS"))
	if strings.EqualFold(request, "none") {
		return ""
	}
	return request
}

func dockerCommand() string {
	if path, err := exec.LookPath("docker"); err == nil {
		return path
	}
	for _, path := range []string{
		"/usr/local/bin/docker",
		"/opt/homebrew/bin/docker",
		"/Applications/Docker.app/Contents/Resources/bin/docker",
		"/Volumes/Docker/Docker.app/Contents/Resources/bin/docker",
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return "docker"
}

func (e *NativeDockerEngine) GetType() string {
	return "Docker"
}

func (e *NativeDockerEngine) Create(ctx context.Context, image string, mountPath string) (string, error) {
	args, err := secureDockerRunArgs(image)
	if err != nil {
		return "", err
	}
	if gpuRequest := dockerGPURequest(); gpuRequest != "" {
		args = append(args, "--gpus", gpuRequest)
	}
	if mountPath != "" {
		normalizedMountPath, err := normalizeDockerMountPath(mountPath)
		if err != nil {
			return "", err
		}
		args = append(args, "-v", fmt.Sprintf("%s:/workspace", normalizedMountPath))
	}
	args = append(args, image, "sleep", "infinity")
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, dockerCommand(), args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Docker run failed: %v, output: %s", err, string(output))
	}
	containerID := extractContainerIDFromDockerRunOutput(string(output))
	if containerID == "" {
		return "", fmt.Errorf("Docker run returned empty container id, output: %s", string(output))
	}
	if mountPath != "" {
		e.mu.Lock()
		e.mountPaths[containerID] = mountPath
		e.mu.Unlock()
	}
	return containerID, nil
}

func extractContainerIDFromDockerRunOutput(raw string) string {
	// `docker run -d` may print image pull progress before the final container id
	// when the image is not present locally. We scan from the last line upward and
	// validate the candidate against the expected hex container-id pattern.
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if containerIDPattern.MatchString(line) {
			return line
		}
	}
	return ""
}

func normalizeDockerMountPath(mountPath string) (string, error) {
	if strings.TrimSpace(mountPath) == "" {
		return "", nil
	}
	return normalizeAndAuthorizeMountPath(mountPath)
}

func (e *NativeDockerEngine) Delete(ctx context.Context, id string) error {
	e.mu.Lock()
	delete(e.mountPaths, id)
	e.mu.Unlock()
	return exec.CommandContext(ctx, dockerCommand(), "rm", "-f", id).Run()
}

func (e *NativeDockerEngine) ExecutePython(ctx context.Context, id string, code string) (*ExecutionResponse, error) {
	cmd := exec.CommandContext(ctx, dockerCommand(), "exec", id, "python3", "-c", code)
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
	cmd := exec.CommandContext(ctx, dockerCommand(), "exec", id, "python3", "-c", code)
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
	dockerCmd := exec.CommandContext(ctx, dockerCommand(), args...)
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
	dockerCmd := exec.CommandContext(ctx, dockerCommand(), args...)
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
