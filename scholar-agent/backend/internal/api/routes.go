package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"scholar-agent-backend/internal/agent"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/sandbox"
	"time"

	"github.com/gin-gonic/gin"
)

type RequestPayload struct {
	Intent string `json:"intent" binding:"required"`
}

type ExecutePayload struct {
	TaskID          string `json:"task_id"`
	TaskName        string `json:"task_name"`
	TaskDescription string `json:"task_description" binding:"required"`
	AssignedTo      string `json:"assigned_to"`
}

type ChatPayload struct {
	Message string `json:"message" binding:"required"`
}

// CORSMiddleware allows frontend to communicate with backend
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func SetupRoutes(r *gin.Engine) {
	// Apply CORS
	r.Use(CORSMiddleware())

	p := planner.NewPlanner()

	// Initialize Agents
	sandboxURL := os.Getenv("SANDBOX_URL")
	if sandboxURL == "" {
		sandboxURL = "http://localhost:8082"
	}
	sb := sandbox.NewSandboxClient(sandboxURL)
	coderAgent := agent.NewCoderAgent(sb)
	librarianAgent := agent.NewLibrarianAgent()
	dataAgent := agent.NewDataAgent()
	chatAgent := agent.NewChatAgent(coderAgent)

	apiGroup := r.Group("/api")
	{
		// Preflight handlers for the group
		apiGroup.OPTIONS("/*path", func(c *gin.Context) {
			c.Status(204)
		})

		apiGroup.GET("/hello", func(c *gin.Context) {
			c.String(200, "hello api group")
		})

		apiGroup.POST("/chat", func(c *gin.Context) {
			var payload ChatPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			response, err := chatAgent.Answer(c.Request.Context(), payload.Message)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"response": response,
			})
		})

		apiGroup.GET("/pdf-proxy", func(c *gin.Context) {
			pdfURL := c.Query("url")
			if pdfURL == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "url parameter is required"})
				return
			}

			log.Printf("[PDF Proxy] Fetching: %s", pdfURL)

			client := &http.Client{
				Timeout: 30 * time.Second,
			}

			req, err := http.NewRequest("GET", pdfURL, nil)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create request: %v", err)})
				return
			}

			// Add User-Agent to mimic a browser
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

			resp, err := client.Do(req)
			if err != nil {
				log.Printf("[PDF Proxy] Error fetching PDF: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch PDF: %v", err)})
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("[PDF Proxy] Unexpected status code: %d", resp.StatusCode)
				c.JSON(resp.StatusCode, gin.H{"error": fmt.Sprintf("Upstream returned status %d", resp.StatusCode)})
				return
			}

			// Set content type and other headers
			c.Header("Content-Type", "application/pdf")
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

			// Stream the PDF back to the frontend
			_, err = io.Copy(c.Writer, resp.Body)
			if err != nil {
				log.Printf("[PDF Proxy] Error streaming PDF: %v", err)
			}
		})

		apiGroup.POST("/plan", func(c *gin.Context) {
			var payload RequestPayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			intentType := "General"
			if contains(payload.Intent, []string{"对比", "评估", "选型", "RAG"}) {
				intentType = "Framework_Evaluation"
			} else if contains(payload.Intent, []string{"复现"}) {
				intentType = "Paper_Reproduction"
			} else if contains(payload.Intent, []string{"计算", "代码", "运行", "执行", "画图", "分析"}) {
				intentType = "Code_Execution"
			}

			plan, err := p.GeneratePlan(payload.Intent, intentType)
			if err != nil {
				log.Printf("Error generating plan: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate plan"})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"message": "Plan generated successfully",
				"plan":    plan,
			})
		})

		apiGroup.POST("/execute", func(c *gin.Context) {
			var payload ExecutePayload
			if err := c.ShouldBindJSON(&payload); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}

			// Disable proxy buffering for SSE
			c.Header("X-Accel-Buffering", "no")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")

			// Create a channel for logs
			logChannel := make(chan string, 100)
			done := make(chan error, 1)

			// Create a context with the log channel
			ctx := context.WithValue(c.Request.Context(), "logChannel", logChannel)

			// Create a mock task to pass to the agent
			task := &models.Task{
				ID:          payload.TaskID,
				Name:        payload.TaskName,
				Description: payload.TaskDescription,
				AssignedTo:  payload.AssignedTo,
				Status:      models.StatusPending,
			}

			if task.ID == "" {
				task.ID = "exec-1"
			}
			if task.Name == "" {
				task.Name = "Direct Execution Task"
			}

			// Add taskID to context
			ctx = context.WithValue(ctx, "taskID", task.ID)

			// Create a workspace directory for the task
			workspacePath := filepath.Join("/tmp", "scholar_workspace_"+task.ID)
			_ = os.MkdirAll(workspacePath, 0777)

			// Initialize persistent sandbox for this task
			var containerID string
			go func() {
				if sb != nil {
					logChannel <- "[System] 正在通过 OpenSandbox 服务分配持久化沙箱环境..."
					var err error
					containerID, err = sb.CreatePersistentSandbox(ctx, task.ID, "python:3.9-bullseye", workspacePath)
					if err != nil {
						logChannel <- fmt.Sprintf("[Error] 创建沙箱失败: %v", err)
					} else {
						typeStr := "OpenSandbox"
						if len(containerID) > 3 && containerID[:3] == "dk-" {
							typeStr = "原生 Docker (已启动兜底方案)"
						} else if len(containerID) > 3 && containerID[:3] == "os-" {
							typeStr = "OpenSandbox"
						}
						logChannel <- fmt.Sprintf("[System] %s 沙箱创建成功 (ID: %s)", typeStr, containerID)
						ctx = context.WithValue(ctx, "containerID", containerID)
					}
				}

				var err error
				switch task.AssignedTo {
				case "librarian_agent":
					err = librarianAgent.ExecuteTask(ctx, task, nil)
				case "data_agent":
					err = dataAgent.ExecuteTask(ctx, task, nil)
				case "coder_agent", "sandbox_agent":
					err = coderAgent.ExecuteTask(ctx, task, nil)
				default:
					err = coderAgent.ExecuteTask(ctx, task, nil)
				}

				// Check if an image was generated in the workspace
				if containerID != "" {
					plotPath := filepath.Join("/tmp/scholar_workspace_"+task.ID, "output_plot.png")
					if _, err := os.Stat(plotPath); err == nil {
						logChannel <- "[System] 检测到生成的图表，正在处理图像数据..."
						imgData, readErr := os.ReadFile(plotPath)
						if readErr == nil {
							task.ImageBase64 = base64.StdEncoding.EncodeToString(imgData)
							logChannel <- "[System] 图表处理完成"
						}
					}
				}

				if sb != nil && containerID != "" {
					logChannel <- "[System] 任务执行完毕，正在清理沙箱环境..."
					_ = sb.CleanupSandbox(context.Background(), containerID)
					logChannel <- "[System] 沙箱环境清理完成"
				}
				done <- err
			}()

			// Use Gin's Stream for robust SSE
			c.Stream(func(w io.Writer) bool {
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()

				for {
					select {
					case logMsg := <-logChannel:
						c.SSEvent("log", logMsg)
						return true
					case <-ticker.C:
						c.SSEvent("heartbeat", "keep-alive")
						return true
					case err := <-done:
						for len(logChannel) > 0 {
							c.SSEvent("log", <-logChannel)
						}
						if err != nil {
							c.SSEvent("error", err.Error())
						} else {
							if len(task.Result) > 50000 {
								c.SSEvent("log", "[Warning] Result is very large, truncating...")
								task.Result = task.Result[:50000] + "\n...[Truncated]..."
							}
							c.SSEvent("result", gin.H{
								"result":        task.Result,
								"code":          task.Code,
								"image_base_64": task.ImageBase64,
							})
						}
						return false // Close stream
					case <-c.Request.Context().Done():
						return false
					}
				}
			})
		})
	}
}

func contains(s string, keywords []string) bool {
	for _, k := range keywords {
		// Simple substring match for demo
		if len(s) >= len(k) {
			for i := 0; i <= len(s)-len(k); i++ {
				if s[i:i+len(k)] == k {
					return true
				}
			}
		}
	}
	return false
}
