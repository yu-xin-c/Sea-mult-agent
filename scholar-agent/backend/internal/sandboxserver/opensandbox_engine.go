package sandboxserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type OpenSandboxEngine struct {
	BaseURL    string
	httpClient *http.Client
}

func NewOpenSandboxEngine(url string) *OpenSandboxEngine {
	return &OpenSandboxEngine{
		BaseURL: url,
		httpClient: &http.Client{
			Timeout: 600 * time.Second,
		},
	}
}

func (e *OpenSandboxEngine) Create(ctx context.Context, image string, mountPath string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"image": map[string]string{"uri": image},
		"resourceLimits": map[string]string{
			"cpu":    "1000m",
			"memory": "1Gi",
		},
		"entrypoint": []string{"/bin/sh"},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/v1/sandboxes", bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("opensandbox error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	_ = mountPath
	return result.ID, nil
}

func (e *OpenSandboxEngine) Delete(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, e.BaseURL+"/v1/sandboxes/"+id, nil)
	if err != nil {
		return err
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (e *OpenSandboxEngine) ExecutePython(ctx context.Context, id string, code string) (*ExecutionResponse, error) {
	payload, _ := json.Marshal(map[string]string{"code": code})
	targetURL := fmt.Sprintf("%s/v1/sandboxes/%s/proxy/49152/python", e.BaseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res ExecutionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (e *OpenSandboxEngine) ExecuteCommand(ctx context.Context, id string, cmd []string) (*ExecutionResponse, error) {
	payload, _ := json.Marshal(map[string][]string{"cmd": cmd})
	targetURL := fmt.Sprintf("%s/v1/sandboxes/%s/proxy/49152/commands", e.BaseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res ExecutionResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}
