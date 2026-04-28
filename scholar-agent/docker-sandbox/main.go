package main

import (
	"docker-sandbox/internal/engine"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

type Config struct {
	OpenSandboxURL string
	EnableFallback bool
	ListenAddr     string
}

type SandboxService struct {
	osEngine *engine.OpenSandboxEngine
	dkEngine *engine.NativeDockerEngine
	config   Config
}

func NewSandboxService(cfg Config) *SandboxService {
	var osEngine *engine.OpenSandboxEngine
	if cfg.EnableFallback && cfg.OpenSandboxURL != "" {
		osEngine = engine.NewOpenSandboxEngine(cfg.OpenSandboxURL)
	}

	return &SandboxService{
		osEngine: osEngine,
		dkEngine: engine.NewNativeDockerEngine(),
		config:   cfg,
	}
}

func configureFileLogging() {
	logDir := filepath.Join(resolveProjectRoot(), "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("failed to create log dir %s: %v", logDir, err)
		return
	}

	logFile := filepath.Join(logDir, "docker-sandbox.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("failed to open sandbox log file %s: %v", logFile, err)
		return
	}

	writer := io.MultiWriter(os.Stdout, file)
	log.SetOutput(writer)
	gin.DefaultWriter = writer
	gin.DefaultErrorWriter = writer
	log.Printf("sandbox logs redirected to %s", logFile)
}

func resolveProjectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}

	candidates := []string{wd, filepath.Dir(wd), filepath.Dir(filepath.Dir(wd))}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if isProjectRoot(candidate) {
			return candidate
		}
	}
	return wd
}

func isProjectRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "backend"))
	if err != nil || !info.IsDir() {
		return false
	}
	info, err = os.Stat(filepath.Join(dir, "docker-sandbox"))
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

// --- API 定义 ---

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

// --- 处理器 ---

func (s *SandboxService) CreateSandbox(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Image == "" {
		req.Image = "python:3.9-bullseye"
	}

	// 1. 优先尝试 Native Docker (本地开发最可靠，支持挂载)
	id, err := s.dkEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err == nil {
		log.Printf("[Success] Native Docker sandbox created: %s", id)
		c.JSON(http.StatusOK, CreateResponse{SandboxID: "dk-" + id})
		return
	}
	dockerErr := err

	log.Printf("[Warning] Native Docker failed: %v", dockerErr)

	if !s.config.EnableFallback || s.osEngine == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Native Docker failed: %v", dockerErr),
		})
		return
	}

	log.Printf("[Warning] Falling back to OpenSandbox: %s", s.config.OpenSandboxURL)

	// 2. 显式开启时才兜底 OpenSandbox
	id, err = s.osEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Native Docker failed: %v; OpenSandbox failed: %v", dockerErr, err),
		})
		return
	}

	c.JSON(http.StatusOK, CreateResponse{SandboxID: "os-" + id})
}

func (s *SandboxService) DeleteSandbox(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		s.osEngine.Delete(ctx, strings.TrimPrefix(id, "os-"))
	} else if strings.HasPrefix(id, "dk-") {
		s.dkEngine.Delete(ctx, strings.TrimPrefix(id, "dk-"))
	}
	c.Status(http.StatusOK)
}

func (s *SandboxService) ExecutePython(c *gin.Context) {
	id := c.Param("id")
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var res *engine.ExecutionResponse
	var err error
	ctx := c.Request.Context()

	if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		res, err = s.osEngine.ExecutePython(ctx, strings.TrimPrefix(id, "os-"), req.Code)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecutePython(ctx, strings.TrimPrefix(id, "dk-"), req.Code)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sandbox id prefix"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *SandboxService) ExecutePythonStream(c *gin.Context) {
	id := c.Param("id")
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flushWriter, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	emit := func(event engine.ExecutionStreamEvent) error {
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

	ctx := c.Request.Context()
	switch {
	case strings.HasPrefix(id, "dk-"):
		if _, err := s.dkEngine.ExecutePythonStream(ctx, strings.TrimPrefix(id, "dk-"), req.Code, emit); err != nil {
			writeStreamError(c.Writer, flushWriter, err)
		}
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("opensandbox fallback is disabled"))
			return
		}
		res, err := s.osEngine.ExecutePython(ctx, strings.TrimPrefix(id, "os-"), req.Code)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: res})
	default:
		writeStreamError(c.Writer, flushWriter, fmt.Errorf("unknown sandbox id prefix"))
	}
}

func (s *SandboxService) ExecuteCommand(c *gin.Context) {
	id := c.Param("id")
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var res *engine.ExecutionResponse
	var err error
	ctx := c.Request.Context()

	if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		res, err = s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), req.Cmd)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "dk-"), req.Cmd)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sandbox id prefix"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *SandboxService) ExecuteCommandStream(c *gin.Context) {
	id := c.Param("id")
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flushWriter, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	emit := func(event engine.ExecutionStreamEvent) error {
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

	ctx := c.Request.Context()
	switch {
	case strings.HasPrefix(id, "dk-"):
		if _, err := s.dkEngine.ExecuteCommandStream(ctx, strings.TrimPrefix(id, "dk-"), req.Cmd, emit); err != nil {
			writeStreamError(c.Writer, flushWriter, err)
		}
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("opensandbox fallback is disabled"))
			return
		}
		res, err := s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), req.Cmd)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: res})
	default:
		writeStreamError(c.Writer, flushWriter, fmt.Errorf("unknown sandbox id prefix"))
	}
}

func writeStreamError(w io.Writer, flusher http.Flusher, err error) {
	payload, _ := json.Marshal(engine.ExecutionStreamEvent{
		Type:    "chunk",
		Stream:  "stderr",
		Message: err.Error(),
	})
	_, _ = w.Write(append(payload, '\n'))
	finalPayload, _ := json.Marshal(engine.ExecutionStreamEvent{
		Type: "final",
		Response: &engine.ExecutionResponse{
			Stderr:   err.Error(),
			ExitCode: -1,
		},
	})
	_, _ = w.Write(append(finalPayload, '\n'))
	flusher.Flush()
}

func main() {
	configureFileLogging()

	osURL := os.Getenv("OPEN_SANDBOX_URL")
	enableFallback := strings.EqualFold(os.Getenv("ENABLE_OPENSANDBOX_FALLBACK"), "true")
	listenAddr := ":8082"

	svc := NewSandboxService(Config{
		OpenSandboxURL: osURL,
		EnableFallback: enableFallback,
		ListenAddr:     listenAddr,
	})

	r := gin.Default()
	api := r.Group("/api/v1")
	{
		api.POST("/sandboxes", svc.CreateSandbox)
		api.DELETE("/sandboxes/:id", svc.DeleteSandbox)
		api.POST("/sandboxes/:id/python", svc.ExecutePython)
		api.POST("/sandboxes/:id/python/stream", svc.ExecutePythonStream)
		api.POST("/sandboxes/:id/commands", svc.ExecuteCommand)
		api.POST("/sandboxes/:id/commands/stream", svc.ExecuteCommandStream)
	}

	log.Printf("Docker Sandbox Service starting on %s, fallback=%t, opensandbox=%s", listenAddr, enableFallback, osURL)
	r.Run(listenAddr)
}
