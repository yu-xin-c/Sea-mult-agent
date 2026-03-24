package engine

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

func (e *OpenSandboxEngine) GetType() string {
	return "OpenSandbox"
}

func (e *OpenSandboxEngine) Create(ctx context.Context, image string, mountPath string) (string, error) {
	reqBody := map[string]interface{}{
		"image": map[string]string{"uri": image},
		"resourceLimits": map[string]string{
			"cpu":    "1000m",
			"memory": "1Gi",
		},
		"entrypoint": []string{"/bin/sh"},
	}
	// Note: OpenSandbox might not support mountPath natively in the same way, 
	// but we'll ignore it for now or implement as needed.
	payload, _ := json.Marshal(reqBody)

	resp, err := e.httpClient.Post(e.BaseURL+"/v1/sandboxes", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenSandbox error (Status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.ID, nil
}

func (e *OpenSandboxEngine) Delete(ctx context.Context, id string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", e.BaseURL+"/v1/sandboxes/"+id, nil)
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
	resp, err := e.httpClient.Post(targetURL, "application/json", bytes.NewBuffer(payload))
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
	resp, err := e.httpClient.Post(targetURL, "application/json", bytes.NewBuffer(payload))
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
