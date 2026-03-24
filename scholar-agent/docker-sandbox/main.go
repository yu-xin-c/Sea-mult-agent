package main

import (
	"docker-sandbox/internal/engine"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

type Config struct {
	OpenSandboxURL string
	ListenAddr     string
}

type SandboxService struct {
	osEngine *engine.OpenSandboxEngine
	dkEngine *engine.NativeDockerEngine
}

func NewSandboxService(osURL string) *SandboxService {
	return &SandboxService{
		osEngine: engine.NewOpenSandboxEngine(osURL),
		dkEngine: engine.NewNativeDockerEngine(),
	}
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
		req.Image = "opensandbox/code-interpreter:latest"
	}

	// 1. 优先尝试 Native Docker (本地开发最可靠，支持挂载)
	id, err := s.dkEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err == nil {
		log.Printf("[Success] Native Docker sandbox created: %s", id)
		c.JSON(http.StatusOK, CreateResponse{SandboxID: "dk-" + id})
		return
	}

	log.Printf("[Warning] Native Docker failed, falling back to OpenSandbox: %v", err)

	// 2. 兜底 OpenSandbox
	id, err = s.osEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "All engines failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, CreateResponse{SandboxID: "os-" + id})
}

func (s *SandboxService) DeleteSandbox(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	if strings.HasPrefix(id, "os-") {
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
		res, err = s.osEngine.ExecutePython(ctx, strings.TrimPrefix(id, "os-"), req.Code)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecutePython(ctx, strings.TrimPrefix(id, "dk-"), req.Code)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
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
		res, err = s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), req.Cmd)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "dk-"), req.Cmd)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func main() {
	osURL := os.Getenv("OPEN_SANDBOX_URL")
	if osURL == "" {
		osURL = "http://localhost:8081"
	}
	listenAddr := ":8082"

	svc := NewSandboxService(osURL)

	r := gin.Default()
	api := r.Group("/api/v1")
	{
		api.POST("/sandboxes", svc.CreateSandbox)
		api.DELETE("/sandboxes/:id", svc.DeleteSandbox)
		api.POST("/sandboxes/:id/python", svc.ExecutePython)
		api.POST("/sandboxes/:id/commands", svc.ExecuteCommand)
	}

	log.Printf("Docker Sandbox Service starting on %s, decoupled architecture", listenAddr)
	r.Run(listenAddr)
}
