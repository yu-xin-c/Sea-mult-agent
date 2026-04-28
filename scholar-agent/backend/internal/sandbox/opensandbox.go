package sandbox

import (
	"bufio"
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

type executionStreamEvent struct {
	Type     string             `json:"type"`
	Stream   string             `json:"stream,omitempty"`
	Message  string             `json:"message,omitempty"`
	Response *PythonRunResponse `json:"response,omitempty"`
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

func (s *SandboxClient) RunPythonCodeStream(ctx context.Context, sandboxID string, code string, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	return s.streamExecution(ctx, sandboxID, "/python/stream", PythonRunRequest{Code: code}, onChunk)
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

func (s *SandboxClient) ExecCommandStream(ctx context.Context, sandboxID string, cmd []string, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	return s.streamExecution(ctx, sandboxID, "/commands/stream", map[string][]string{"cmd": cmd}, onChunk)
}

func (s *SandboxClient) streamExecution(ctx context.Context, sandboxID string, suffix string, payload any, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	reqBody, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+suffix, bytes.NewBuffer(reqBody))
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
		return nil, fmt.Errorf("stream execution failed (Status %d): %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)

	var finalResponse *PythonRunResponse
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var event executionStreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("decode execution stream failed: %w", err)
		}

		switch event.Type {
		case "chunk":
			if onChunk != nil {
				onChunk(event.Stream, event.Message)
			}
		case "final":
			finalResponse = event.Response
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if finalResponse == nil {
		return nil, fmt.Errorf("stream execution ended without final response")
	}
	return finalResponse, nil
}
