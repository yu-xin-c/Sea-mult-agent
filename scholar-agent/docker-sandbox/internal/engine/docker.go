package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type NativeDockerEngine struct {
	mountPaths map[string]string // 记录容器 ID 到宿主机挂载路径的映射
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
		args = append(args, "-v", fmt.Sprintf("%s:/workspace", mountPath))
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
		e.mountPaths[containerID] = mountPath
	}
	return containerID, nil
}

func (e *NativeDockerEngine) Delete(ctx context.Context, id string) error {
	delete(e.mountPaths, id)
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
	if mountPath, ok := e.mountPaths[id]; ok {
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
