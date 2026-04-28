package sandboxserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ExecutionResponse struct {
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
	Images   []string `json:"images,omitempty"`
}

type ExecutionStreamEvent struct {
	Type     string             `json:"type"`
	Stream   string             `json:"stream,omitempty"`
	Message  string             `json:"message,omitempty"`
	Response *ExecutionResponse `json:"response,omitempty"`
}

type Config struct {
	OpenSandboxURL string
	EnableFallback bool
}

type CreateRequest struct {
	Image     string `json:"image"`
	MountPath string `json:"mount_path"`
}

type CreateResponse struct {
	SandboxID string `json:"sandbox_id"`
}

type ExecuteRequest struct {
	Code string `json:"code"`
}

type CommandRequest struct {
	Cmd []string `json:"cmd"`
}

type sandboxService struct {
	osEngine *OpenSandboxEngine
	dkEngine *NativeDockerEngine
	config   Config
}

func StartLocal() (string, func(context.Context) error, error) {
	cfg := Config{
		OpenSandboxURL: os.Getenv("OPEN_SANDBOX_URL"),
		EnableFallback: strings.EqualFold(os.Getenv("ENABLE_OPENSANDBOX_FALLBACK"), "true"),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	server := &http.Server{
		Handler:      NewHandler(cfg),
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  10 * time.Minute,
	}

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("embedded sandbox server stopped unexpectedly: %v", serveErr)
		}
	}()

	return "http://" + listener.Addr().String(), server.Shutdown, nil
}

func NewHandler(cfg Config) http.Handler {
	svc := &sandboxService{
		config:   cfg,
		dkEngine: NewNativeDockerEngine(),
	}
	if cfg.EnableFallback && strings.TrimSpace(cfg.OpenSandboxURL) != "" {
		svc.osEngine = NewOpenSandboxEngine(cfg.OpenSandboxURL)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	api := r.Group("/api/v1")
	{
		api.POST("/sandboxes", svc.CreateSandbox)
		api.DELETE("/sandboxes/:id", svc.DeleteSandbox)
		api.POST("/sandboxes/:id/python", svc.ExecutePython)
		api.POST("/sandboxes/:id/python/stream", svc.ExecutePythonStream)
		api.POST("/sandboxes/:id/commands", svc.ExecuteCommand)
		api.POST("/sandboxes/:id/commands/stream", svc.ExecuteCommandStream)
	}

	return r
}

func (s *sandboxService) CreateSandbox(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Image == "" {
		req.Image = "python:3.9-bullseye"
	}

	// 单文件模式优先走本地 Docker，避免额外依赖外部服务。
	id, err := s.dkEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err == nil {
		c.JSON(http.StatusOK, CreateResponse{SandboxID: "dk-" + id})
		return
	}
	dockerErr := err

	if !s.config.EnableFallback || s.osEngine == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("native docker failed: %v", dockerErr),
		})
		return
	}

	id, err = s.osEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("native docker failed: %v; opensandbox failed: %v", dockerErr, err),
		})
		return
	}

	c.JSON(http.StatusOK, CreateResponse{SandboxID: "os-" + id})
}

func (s *sandboxService) DeleteSandbox(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	switch {
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		_ = s.osEngine.Delete(ctx, strings.TrimPrefix(id, "os-"))
	case strings.HasPrefix(id, "dk-"):
		_ = s.dkEngine.Delete(ctx, strings.TrimPrefix(id, "dk-"))
	}
	c.Status(http.StatusOK)
}

func (s *sandboxService) ExecutePython(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := s.executePython(c.Request.Context(), c.Param("id"), req.Code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *sandboxService) ExecuteCommand(c *gin.Context) {
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := s.executeCommand(c.Request.Context(), c.Param("id"), req.Cmd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *sandboxService) ExecutePythonStream(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.streamExecution(c, func(emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
		return s.executePythonStream(c.Request.Context(), c.Param("id"), req.Code, emit)
	})
}

func (s *sandboxService) ExecuteCommandStream(c *gin.Context) {
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.streamExecution(c, func(emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
		return s.executeCommandStream(c.Request.Context(), c.Param("id"), req.Cmd, emit)
	})
}

func (s *sandboxService) streamExecution(c *gin.Context, run func(func(ExecutionStreamEvent) error) (*ExecutionResponse, error)) {
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flushWriter, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	emit := func(event ExecutionStreamEvent) error {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(payload, '\n')); err != nil {
			return err
		}
		flushWriter.Flush()
		return nil
	}

	if _, err := run(emit); err != nil {
		writeStreamError(c.Writer, flushWriter, err)
	}
}

func (s *sandboxService) executePython(ctx context.Context, id string, code string) (*ExecutionResponse, error) {
	switch {
	case strings.HasPrefix(id, "dk-"):
		return s.dkEngine.ExecutePython(ctx, strings.TrimPrefix(id, "dk-"), code)
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			return nil, fmt.Errorf("opensandbox fallback is disabled")
		}
		return s.osEngine.ExecutePython(ctx, strings.TrimPrefix(id, "os-"), code)
	default:
		return nil, fmt.Errorf("unknown sandbox id prefix")
	}
}

func (s *sandboxService) executeCommand(ctx context.Context, id string, cmd []string) (*ExecutionResponse, error) {
	switch {
	case strings.HasPrefix(id, "dk-"):
		return s.dkEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "dk-"), cmd)
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			return nil, fmt.Errorf("opensandbox fallback is disabled")
		}
		return s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), cmd)
	default:
		return nil, fmt.Errorf("unknown sandbox id prefix")
	}
}

func (s *sandboxService) executePythonStream(ctx context.Context, id string, code string, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	switch {
	case strings.HasPrefix(id, "dk-"):
		return s.dkEngine.ExecutePythonStream(ctx, strings.TrimPrefix(id, "dk-"), code, emit)
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			return nil, fmt.Errorf("opensandbox fallback is disabled")
		}
		res, err := s.osEngine.ExecutePython(ctx, strings.TrimPrefix(id, "os-"), code)
		if err != nil {
			return nil, err
		}
		if emit != nil {
			if err := emit(ExecutionStreamEvent{Type: "final", Response: res}); err != nil {
				return nil, err
			}
		}
		return res, nil
	default:
		return nil, fmt.Errorf("unknown sandbox id prefix")
	}
}

func (s *sandboxService) executeCommandStream(ctx context.Context, id string, cmd []string, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	switch {
	case strings.HasPrefix(id, "dk-"):
		return s.dkEngine.ExecuteCommandStream(ctx, strings.TrimPrefix(id, "dk-"), cmd, emit)
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			return nil, fmt.Errorf("opensandbox fallback is disabled")
		}
		res, err := s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), cmd)
		if err != nil {
			return nil, err
		}
		if emit != nil {
			if err := emit(ExecutionStreamEvent{Type: "final", Response: res}); err != nil {
				return nil, err
			}
		}
		return res, nil
	default:
		return nil, fmt.Errorf("unknown sandbox id prefix")
	}
}

func writeStreamError(w io.Writer, flusher http.Flusher, err error) {
	payload, _ := json.Marshal(ExecutionStreamEvent{
		Type:    "chunk",
		Stream:  "stderr",
		Message: err.Error(),
	})
	_, _ = w.Write(append(payload, '\n'))
	finalPayload, _ := json.Marshal(ExecutionStreamEvent{
		Type: "final",
		Response: &ExecutionResponse{
			Stderr:   err.Error(),
			ExitCode: -1,
		},
	})
	_, _ = w.Write(append(finalPayload, '\n'))
	flusher.Flush()
}
