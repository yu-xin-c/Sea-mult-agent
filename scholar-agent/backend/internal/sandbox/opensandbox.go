package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SandboxClient 是后端与外部 docker-sandbox 服务通信的客户端
type SandboxClient struct {
	BaseURL string
	Client  *http.Client
}

// SandboxCreateRequest 创建沙箱请求 (简化版)
type SandboxCreateRequest struct {
	Image     string `json:"image"`
	MountPath string `json:"mount_path"`
}

// SandboxCreateResponse 创建沙箱响应
type SandboxCreateResponse struct {
	SandboxID string `json:"sandbox_id"`
}

// PythonRunRequest Python 代码执行请求
type PythonRunRequest struct {
	Code string `json:"code"`
}

// PythonRunResponse Python 代码执行响应 (包含 stdout, stderr, exit_code 和 images)
type PythonRunResponse struct {
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
	Images   []string `json:"images,omitempty"`
}

func NewSandboxClient(baseURL string) *SandboxClient {
	if baseURL == "" {
		baseURL = "http://localhost:8082" // 默认指向我们的新 docker-sandbox 服务
	}
	return &SandboxClient{
		BaseURL: baseURL,
		Client: &http.Client{
			Timeout: 600 * time.Second,
		},
	}
}

// CreatePersistentSandbox 创建持久化沙箱
func (s *SandboxClient) CreatePersistentSandbox(ctx context.Context, taskID string, image string, mountPath string) (string, error) {
	reqBody, _ := json.Marshal(SandboxCreateRequest{
		Image:     image,
		MountPath: mountPath,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("沙箱服务返回错误 (Status %d): %s", resp.StatusCode, string(body))
	}

	var res SandboxCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	return res.SandboxID, nil
}

// CleanupSandbox 清理沙箱
func (s *SandboxClient) CleanupSandbox(ctx context.Context, sandboxID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", s.BaseURL+"/api/v1/sandboxes/"+sandboxID, nil)
	if err != nil {
		return err
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// RunPythonCode 运行 Python 代码
func (s *SandboxClient) RunPythonCode(ctx context.Context, sandboxID string, code string) (*PythonRunResponse, error) {
	reqBody, _ := json.Marshal(PythonRunRequest{Code: code})
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+"/python", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("沙箱执行失败 (Status %d): %s", resp.StatusCode, string(body))
	}

	var res PythonRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}

// ExecCommand 在沙箱中执行命令
func (s *SandboxClient) ExecCommand(ctx context.Context, sandboxID string, cmd []string) (*PythonRunResponse, error) {
	reqBody, _ := json.Marshal(map[string][]string{"cmd": cmd})
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+"/commands", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("执行命令失败 (Status %d): %s", resp.StatusCode, string(body))
	}

	var res PythonRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}
