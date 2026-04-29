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
	"regexp"
	"scholar-agent-backend/internal/agent"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/sandbox"
	"scholar-agent-backend/internal/scheduler"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type RequestPayload struct {
	Intent string `json:"intent" binding:"required"`
}

type ExecutePayload struct {
	TaskID          string         `json:"task_id"`
	TaskName        string         `json:"task_name"`
	TaskType        string         `json:"task_type"`
	TaskDescription string         `json:"task_description" binding:"required"`
	AssignedTo      string         `json:"assigned_to"`
	Inputs          map[string]any `json:"inputs"`
}

type ChatPayload struct {
	Message string `json:"message" binding:"required"`
}

var routePaperArxivIDRe = regexp.MustCompile(`\b\d{4}\.\d{4,5}\b`)

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

		RegisterPlanRoute(apiGroup, p)

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
				Type:        payload.TaskType,
				Description: payload.TaskDescription,
				AssignedTo:  payload.AssignedTo,
				Status:      models.StatusPending,
				Inputs:      payload.Inputs,
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
			// 先同步创建沙箱，再将 containerID 注入 context，避免 goroutine 内竞态
			var containerID string
			if sb != nil {
				logChannel <- "[System] 正在通过 OpenSandbox 服务分配持久化沙箱环境..."
				var sandboxErr error
				containerID, sandboxErr = sb.CreatePersistentSandbox(ctx, task.ID, "python:3.9-bullseye", workspacePath)
				if sandboxErr != nil {
					logChannel <- fmt.Sprintf("[Warning] 创建沙箱失败，将降级为临时沙箱模式: %v", sandboxErr)
				} else {
					typeStr := "OpenSandbox"
					if len(containerID) > 3 && containerID[:3] == "dk-" {
						typeStr = "原生 Docker (已启动兜底方案)"
					}
					logChannel <- fmt.Sprintf("[System] %s 沙箱创建成功 (ID: %s)", typeStr, containerID)
					ctx = context.WithValue(ctx, "containerID", containerID)
					if task.Inputs == nil {
						task.Inputs = map[string]any{}
					}
					task.Inputs["runtime_session"] = containerID
					task.Inputs["prepared_runtime"] = containerID
				}
			}
			logChannel <- formatExecuteParamsLog("[Params] 后端实际传入参数", task.Inputs)

			go func() {
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
				if err == nil {
					logChannel <- formatExecuteParamsLog("[Params] 后端产出并传递给下游的参数", buildExecuteOutputs(task))
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
								"outputs":       buildExecuteOutputs(task),
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

func RegisterPlanRoute(apiGroup *gin.RouterGroup, p *planner.Planner) {
	apiGroup.POST("/plan", func(c *gin.Context) {
		var payload RequestPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		intentType := DetectIntentType(payload.Intent)

		plan, err := p.GeneratePlan(payload.Intent, intentType)
		if err != nil {
			log.Printf("Error generating plan: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate plan"})
			return
		}

		response := gin.H{
			"message": "Plan generated successfully",
			"plan":    plan,
		}
		if clarification, ok := buildPlanClarification(intentType, payload.Intent); ok {
			response["clarification"] = clarification
		}
		c.JSON(http.StatusOK, response)
	})
}

// detectIntentType 根据用户意图文本判断任务类型
func DetectIntentType(intent string) string {
	// 论文复现类需要优先于“对比/评估”类判断。
	// 论文复现需求常包含“与论文指标对比”等词，如果先命中“对比”会被误路由到框架评测。
	reproKeywords := []string{"复现", "reproduce", "replicate", "论文", "paper", "papers with code", "arxiv", "实现算法"}
	if containsAny(intent, reproKeywords) {
		return "Paper_Reproduction"
	}

	// 框架对比评测类关键词（扩展版）
	evalKeywords := []string{
		"对比", "比较", "评估", "选型", "哪个好", "哪个更", "区别", "异同",
		"rag", "langchain", "llamaindex", "llama_index", "haystack",
		"dspy", "autogen", "crewai", "langgraph", "framework",
		"框架", "测试两", "比一比", "pk", "vs",
	}
	if containsAny(intent, evalKeywords) {
		return "Framework_Evaluation"
	}

	// 代码执行类
	codeKeywords := []string{
		"计算", "代码", "运行", "执行", "画图", "分析", "写一个", "生成代码",
		"跑一下", "plot", "matplotlib", "numpy", "python",
	}
	if containsAny(intent, codeKeywords) {
		return "Code_Execution"
	}

	return "General"
}

// containsAny 检查字符串是否包含任意一个关键词（忽略大小写）
func containsAny(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, k := range keywords {
		kLower := strings.ToLower(k)
		if strings.Contains(lower, kLower) {
			return true
		}
	}
	return false
}

func buildExecuteOutputs(task *models.Task) map[string]any {
	outputs := map[string]any{}
	if task == nil {
		return outputs
	}
	if strings.TrimSpace(task.Result) != "" {
		outputs["result"] = task.Result
	}
	if strings.TrimSpace(task.Code) != "" {
		outputs["generated_code"] = task.Code
	}
	if strings.TrimSpace(task.ImageBase64) != "" {
		outputs["image_base64"] = task.ImageBase64
	}
	return outputs
}

func formatExecuteParamsLog(title string, values map[string]any) string {
	if len(values) == 0 {
		return title + ": 无"
	}

	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return title + ": 无"
	}
	sort.Strings(keys)

	var builder strings.Builder
	builder.WriteString(title)
	builder.WriteString(":\n")
	for _, key := range keys {
		builder.WriteString("- ")
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(previewExecuteParamValue(key, values[key]))
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func previewExecuteParamValue(key string, value any) string {
	text := fmt.Sprint(value)
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "image") || strings.Contains(lowerKey, "base64") {
		return fmt.Sprintf("<base64 payload, %d chars>", len(text))
	}
	const limit = 1200
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "...[truncated]"
}

func buildPlanClarification(intentType string, intent string) (gin.H, bool) {
	if intentType != "Paper_Reproduction" || !shouldClarifyPaperReproductionMode(intent) {
		return nil, false
	}

	fullRequested := containsAny(intent, []string{"full", "complete", "bleu", "wmt", "wmt14", "完整", "全量", "真实复现", "论文指标"})
	decision := scheduler.DecidePaperReproductionMode("auto", fullRequested, os.TempDir())
	recommendedMode := "smoke"
	if decision.FullEligible && fullRequested {
		recommendedMode = "full"
	}

	return gin.H{
		"required":         true,
		"type":             "paper_reproduction_mode",
		"recommended_mode": recommendedMode,
		"question":         buildPaperReproductionModeQuestion(decision),
		"options": []gin.H{
			{
				"id":          "smoke",
				"label":       "运行最小验证",
				"description": "快速验证论文复现链路，不下载全量数据、不训练完整模型、不计算论文 BLEU。",
			},
			{
				"id":          "full",
				"label":       "开启全量复现",
				"description": "仅在你确认本机或外部计算资源足够时开启，会进入数据下载、训练和 BLEU 评测流程。",
			},
		},
		"mode_decision":  decision,
		"resource_probe": decision.Probe,
	}, true
}

func shouldClarifyPaperReproductionMode(intent string) bool {
	lower := strings.ToLower(strings.TrimSpace(intent))
	if lower == "" {
		return false
	}
	if containsAny(lower, []string{"最小实验", "最小验证", "smoke", "quick", "快速验证"}) &&
		!containsAny(lower, []string{"bleu", "wmt", "wmt14", "完整", "全量", "真实复现"}) {
		return false
	}
	return containsAny(lower, []string{
		"bleu", "wmt", "wmt14", "full", "complete",
		"完整复现", "全量复现", "完整实验", "全量实验", "真实复现", "论文指标",
	})
}

func buildPaperReproductionModeQuestion(decision scheduler.ReproductionModeDecision) string {
	probe := decision.Probe
	status := fmt.Sprintf("检测到这是论文复现场景，可能涉及全量训练/BLEU 评测。本机资源探测：CPU=%d，内存=%.1fGB，可用磁盘=%.1fGB，CUDA GPU=%d。",
		probe.CPUCount, probe.MemoryGB, probe.DiskFreeGB, probe.GPUCount)
	if decision.FullEligible {
		return status + " 当前资源满足 full reproduction 门槛。是否确认开启全量复现模式？"
	}
	return status + " 当前资源未满足默认 full reproduction 门槛，建议先运行 smoke 最小验证。你是否确认有足够的本机或外部资源并仍要开启全量复现？"
}

func collectPaperSearchFields(intentCtx models.IntentContext, rawIntent string) map[string]any {
	fields := map[string]any{}

	if rawFields, ok := intentCtx.Metadata["paper_search_fields"].(map[string]any); ok {
		for key, value := range rawFields {
			if strings.TrimSpace(fmt.Sprint(value)) != "" {
				fields[key] = value
			}
		}
	}
	for _, key := range []string{"paper_title", "paper_arxiv_id", "paper_search_query", "paper_method_name"} {
		if value, ok := intentCtx.Entities[key]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			fields[key] = value
		}
	}

	normalized := strings.ToLower(strings.TrimSpace(rawIntent))
	if _, ok := fields["paper_arxiv_id"]; !ok {
		if arxivID := routePaperArxivIDRe.FindString(rawIntent); arxivID != "" {
			fields["paper_arxiv_id"] = arxivID
		}
	}
	if _, ok := fields["paper_title"]; !ok {
		if title := extractQuotedPaperTitle(rawIntent); title != "" {
			fields["paper_title"] = title
		} else if title := extractPaperTitle(normalized); title != "" {
			fields["paper_title"] = title
		}
	}
	if _, ok := fields["paper_method_name"]; !ok {
		if method := extractPaperMethodName(normalized); method != "" {
			fields["paper_method_name"] = method
		}
	}
	if _, ok := fields["paper_search_query"]; !ok {
		arxivID := stringFieldFromMap(fields, "paper_arxiv_id")
		title := stringFieldFromMap(fields, "paper_title")
		method := stringFieldFromMap(fields, "paper_method_name")
		switch {
		case arxivID != "":
			fields["paper_search_query"] = arxivID
		case title != "":
			fields["paper_search_query"] = title
		case method != "":
			fields["paper_search_query"] = method
		}
	}
	return fields
}

func extractQuotedPaperTitle(rawIntent string) string {
	for _, pair := range [][2]string{
		{"《", "》"},
		{"\"", "\""},
		{"'", "'"},
	} {
		start := strings.Index(rawIntent, pair[0])
		end := strings.LastIndex(rawIntent, pair[1])
		if start < 0 || end <= start {
			continue
		}
		title := strings.TrimSpace(rawIntent[start+len(pair[0]) : end])
		if title != "" && len(title) <= 240 {
			return title
		}
	}
	return ""
}

func extractPaperTitle(normalized string) string {
	switch {
	case strings.Contains(normalized, "attention is all you need"):
		return "Attention Is All You Need"
	case strings.Contains(normalized, "transformer"):
		return "Transformer"
	default:
		return ""
	}
}

func extractPaperMethodName(normalized string) string {
	switch {
	case strings.Contains(normalized, "transformer"):
		return "Transformer"
	case strings.Contains(normalized, "resnet"):
		return "ResNet"
	case strings.Contains(normalized, "bert"):
		return "BERT"
	default:
		return ""
	}
}

func stringFieldFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
