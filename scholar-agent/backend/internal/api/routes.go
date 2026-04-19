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
	"scholar-agent-backend/internal/events"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/sandbox"
	"scholar-agent-backend/internal/scheduler"
	"scholar-agent-backend/internal/store"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

func createTaskWorkspace(taskID string) (string, error) {
	safeTaskID := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(taskID))
	if safeTaskID == "" {
		safeTaskID = "task"
	}

	if volume := filepath.VolumeName(mustGetwd()); volume != "" {
		shortRoot := filepath.Join(volume+string(os.PathSeparator), "sa_tmp")
		if err := os.MkdirAll(shortRoot, 0755); err == nil {
			return os.MkdirTemp(shortRoot, safeTaskID+"_")
		}
	}

	return os.MkdirTemp("", "scholar_workspace_"+safeTaskID+"_")
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

type directRuntimeEntry struct {
	SandboxID     string
	WorkspacePath string
	UpdatedAt     time.Time
}

var directRuntimeCache = struct {
	mu    sync.Mutex
	items map[string]directRuntimeEntry
}{
	items: map[string]directRuntimeEntry{},
}

const directRuntimeTTL = 45 * time.Minute

func shouldUseSandboxRuntime(task *models.Task) bool {
	if task == nil || task.AssignedTo != "sandbox_agent" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.Type)) {
	case "prepare_runtime", "install_dependencies", "execute_code", "baseline_run":
		return true
	}
	context := strings.ToLower(strings.Join([]string{task.Name, task.Description}, " "))
	return strings.Contains(context, "runtime") || strings.Contains(context, "dependency") || strings.Contains(context, "benchmark") || strings.Contains(context, "execute")
}

func directRuntimeScopeKey(task *models.Task) string {
	if !shouldUseSandboxRuntime(task) {
		return ""
	}
	context := strings.ToLower(strings.Join([]string{task.Name, task.Description}, " "))
	switch {
	case strings.Contains(context, "langchain"):
		return "sandbox:langchain"
	case strings.Contains(context, "llamaindex"), strings.Contains(context, "llama_index"):
		return "sandbox:llamaindex"
	default:
		id := strings.TrimSpace(strings.ToLower(task.ID))
		if id == "" {
			id = "default"
		}
		return "sandbox:" + id
	}
}

func getCachedDirectRuntime(ctx context.Context, sb *sandbox.SandboxClient, key string) (directRuntimeEntry, bool) {
	if key == "" || sb == nil {
		return directRuntimeEntry{}, false
	}

	directRuntimeCache.mu.Lock()
	entry, ok := directRuntimeCache.items[key]
	if ok && time.Since(entry.UpdatedAt) > directRuntimeTTL {
		delete(directRuntimeCache.items, key)
		ok = false
	}
	directRuntimeCache.mu.Unlock()
	if !ok {
		return directRuntimeEntry{}, false
	}

	res, err := sb.ExecCommand(ctx, entry.SandboxID, []string{"python3", "--version"})
	if err != nil || res == nil || res.ExitCode != 0 {
		directRuntimeCache.mu.Lock()
		delete(directRuntimeCache.items, key)
		directRuntimeCache.mu.Unlock()
		return directRuntimeEntry{}, false
	}

	entry.UpdatedAt = time.Now()
	directRuntimeCache.mu.Lock()
	directRuntimeCache.items[key] = entry
	directRuntimeCache.mu.Unlock()
	return entry, true
}

func storeCachedDirectRuntime(key string, entry directRuntimeEntry) {
	if key == "" || strings.TrimSpace(entry.SandboxID) == "" {
		return
	}
	entry.UpdatedAt = time.Now()
	directRuntimeCache.mu.Lock()
	directRuntimeCache.items[key] = entry
	directRuntimeCache.mu.Unlock()
}

func cleanupPlanSandboxes(ctx context.Context, sb *sandbox.SandboxClient, plan *models.PlanGraph) {
	if sb == nil || plan == nil {
		return
	}

	seen := map[string]struct{}{}
	for _, artifact := range plan.Artifacts {
		value := strings.TrimSpace(artifact.Value)
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, "dk-") && !strings.HasPrefix(value, "os-") {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		if err := sb.CleanupSandbox(ctx, value); err != nil {
			log.Printf("[PlanCleanup] failed to cleanup sandbox %s for plan %s: %v", value, plan.ID, err)
			continue
		}
		log.Printf("[PlanCleanup] cleaned sandbox %s for plan %s", value, plan.ID)
	}
}

type RequestPayload struct {
	Intent string `json:"intent" binding:"required"`
}

type ExecutePayload struct {
	TaskID          string `json:"task_id"`
	TaskName        string `json:"task_name"`
	TaskType        string `json:"task_type"`
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
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()

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
	graphExecutor := scheduler.NewRoutedTaskExecutor(librarianAgent, dataAgent, coderAgent)
	graphScheduler := scheduler.NewScheduler(planStore, graphExecutor, eventBus, 4)
	graphScheduler.SetOnTerminal(func(ctx context.Context, plan *models.PlanGraph) {
		cleanupPlanSandboxes(ctx, sb, plan)
	})

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

			intentCtx := inferIntentContextV2(payload.Intent)
			logPlanRequest(payload.Intent, intentCtx)
			intentType := intentCtx.IntentType

			if false {
				intentCtx = models.IntentContext{
					RawIntent:   payload.Intent,
					IntentType:  intentType,
					Entities:    map[string]any{},
					Constraints: map[string]any{},
					Metadata:    map[string]any{},
				}
			}

			planGraph, err := p.BuildPlan(c.Request.Context(), intentCtx)
			if err != nil {
				log.Printf("Error generating graph plan: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate graph plan"})
				return
			}
			if err := planStore.SavePlan(planGraph); err != nil {
				log.Printf("Error saving graph plan: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save graph plan"})
				return
			}
			eventBus.Publish(planGraph.ID, models.PlanEvent{
				PlanID:    planGraph.ID,
				EventType: "plan_created",
				Timestamp: time.Now(),
			})
			logPlanGraphGenerated(planGraph)

			response := gin.H{
				"message":        "Plan generated successfully",
				"plan_graph":     planGraph,
				"intent_context": intentCtx,
			}

			if c.Query("include_legacy_plan") == "true" {
				plan, err := p.GeneratePlan(payload.Intent, intentCtx.IntentType)
				if err != nil {
					log.Printf("Error generating legacy plan: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate legacy plan"})
					return
				}
				logLegacyPlanFallback(plan, planGraph)
				response["plan"] = plan
			}

			c.JSON(http.StatusOK, response)
		})

		apiGroup.GET("/plans/:id", func(c *gin.Context) {
			planID := c.Param("id")
			planGraph, err := planStore.GetPlan(planID)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"plan":      planGraph,
				"plan_graph": planGraph,
			})
		})

		apiGroup.GET("/plans/:id/events", func(c *gin.Context) {
			planID := c.Param("id")
			events, err := planStore.ListEvents(planID)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"events": events,
			})
		})

		apiGroup.POST("/plans/:id/execute", func(c *gin.Context) {
			planID := c.Param("id")

			planGraph, err := planStore.GetPlan(planID)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
				return
			}
			if planGraph.Status == models.StatusInProgress {
				c.JSON(http.StatusConflict, gin.H{"error": "plan is already running"})
				return
			}
			if planGraph.Status == models.StatusCompleted || planGraph.Status == models.StatusFailed {
				c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("plan is already in terminal state: %s", planGraph.Status)})
				return
			}

			go func() {
				if err := graphScheduler.ExecutePlan(context.Background(), planID); err != nil {
					log.Printf("Plan execution failed for %s: %v", planID, err)
					eventBus.Publish(planID, models.PlanEvent{
						PlanID:     planID,
						EventType:  "plan_failed",
						TaskStatus: string(models.StatusFailed),
						Payload: map[string]any{
							"error": err.Error(),
						},
						Timestamp: time.Now(),
					})
				}
			}()

			c.JSON(http.StatusOK, gin.H{
				"message": "Plan execution started",
				"plan_id": planID,
			})
		})

		apiGroup.GET("/plans/:id/stream", func(c *gin.Context) {
			planID := c.Param("id")

			if _, err := planStore.GetPlan(planID); err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
				return
			}

			eventCh := eventBus.Subscribe(planID)
			defer eventBus.Unsubscribe(planID, eventCh)

			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")

			c.Stream(func(w io.Writer) bool {
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()

				select {
				case event, ok := <-eventCh:
					if !ok {
						return false
					}
					c.SSEvent("plan_event", event)
					return true
				case <-ticker.C:
					c.SSEvent("heartbeat", "keep-alive")
					return true
				case <-c.Request.Context().Done():
					return false
				}
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
				Type:        payload.TaskType,
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

			runtimeKey := directRuntimeScopeKey(task)

			// Initialize persistent sandbox for this task
			var containerID string
			var workspacePath string
			var keepRuntime bool
			go func() {
				if sb != nil && shouldUseSandboxRuntime(task) {
					if cached, ok := getCachedDirectRuntime(ctx, sb, runtimeKey); ok {
						containerID = cached.SandboxID
						workspacePath = cached.WorkspacePath
						keepRuntime = true
						logChannel <- fmt.Sprintf("[System] Reusing existing sandbox (ID: %s)", containerID)
						ctx = context.WithValue(ctx, "containerID", containerID)
						storeCachedDirectRuntime(runtimeKey, directRuntimeEntry{
							SandboxID:     containerID,
							WorkspacePath: workspacePath,
						})
						keepRuntime = true
						storeCachedDirectRuntime(runtimeKey, directRuntimeEntry{
							SandboxID:     containerID,
							WorkspacePath: workspacePath,
						})
						keepRuntime = true
					}
				}
				if sb != nil && shouldUseSandboxRuntime(task) && containerID == "" {
					var mkErr error
					workspacePath, mkErr = createTaskWorkspace(task.ID)
					if mkErr != nil {
						done <- fmt.Errorf("failed to create workspace: %v", mkErr)
						return
					}
					logChannel <- "[System] 正在为当前任务分配持久化沙箱环境..."
					var err error
					containerID, err = sb.CreatePersistentSandbox(ctx, task.ID, "python:3.9-bullseye", workspacePath)
					if err != nil {
						logChannel <- fmt.Sprintf("[Error] 创建沙箱失败: %v", err)
						if task.AssignedTo == "sandbox_agent" {
							done <- err
							return
						}
					} else {
						typeStr := "Sandbox"
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
					plotPath := filepath.Join(workspacePath, "output_plot.png")
					if _, err := os.Stat(plotPath); err == nil {
						logChannel <- "[System] 检测到生成的图表，正在处理图像数据..."
						imgData, readErr := os.ReadFile(plotPath)
						if readErr == nil {
							task.ImageBase64 = base64.StdEncoding.EncodeToString(imgData)
							logChannel <- "[System] 图表处理完成"
						}
					}
				}

				if sb != nil && containerID != "" && !keepRuntime {
					logChannel <- "[System] 任务执行完毕，正在清理沙箱环境..."
					_ = sb.CleanupSandbox(context.Background(), containerID)
					logChannel <- "[System] 沙箱环境清理完成"
				}
				if workspacePath != "" && !keepRuntime {
					_ = os.RemoveAll(workspacePath)
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
								"image_base64":  task.ImageBase64,
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

func inferIntentContext(rawIntent string) models.IntentContext {
	normalized := strings.ToLower(rawIntent)
	intentType := "General"

	frameworks := extractFrameworks(normalized)
	switch {
	case containsAny(normalized, []string{"对比", "比较", "评估", "选型", "benchmark", "ab test", "a/b", "rag"}) || len(frameworks) >= 2:
		intentType = "Framework_Evaluation"
	case containsAny(normalized, []string{"复现", "reproduce", "paper", "论文", "attention is all you need", "transformer"}):
		intentType = "Paper_Reproduction"
	case containsAny(normalized, []string{"代码", "python", "执行", "运行", "画图", "plot", "matplotlib", "分析", "计算"}):
		intentType = "Code_Execution"
	}

	entities := map[string]any{}
	if len(frameworks) > 0 {
		entities["frameworks"] = frameworks
	}
	if containsAny(normalized, []string{"rag"}) {
		entities["topic"] = "RAG"
	}
	if containsAny(normalized, []string{"plot", "画图", "图表", "曲线", "柱状图", "折线图"}) {
		entities["needs_plot"] = true
	}
	if containsAny(normalized, []string{"report", "报告", "总结", "分析"}) {
		entities["needs_report"] = true
	}
	if containsAny(normalized, []string{"benchmark", "性能", "评测", "吞吐", "延迟"}) {
		entities["needs_benchmark"] = true
	}

	return models.IntentContext{
		RawIntent:   rawIntent,
		IntentType:  intentType,
		Entities:    entities,
		Constraints: map[string]any{},
		Metadata: map[string]any{
			"normalized_intent": normalized,
		},
	}
}

func extractFrameworks(normalized string) []string {
	candidates := []string{
		"langchain",
		"llamaindex",
		"haystack",
		"autogen",
		"crewai",
		"langgraph",
	}

	found := make([]string, 0, 2)
	for _, candidate := range candidates {
		if strings.Contains(normalized, candidate) {
			found = append(found, candidate)
		}
	}
	return found
}

func containsAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func contains(_ string, _ []string) bool {
	// Legacy compatibility shim: old planner intent routing is intentionally disabled.
	return false
}

func inferIntentContextV2(rawIntent string) models.IntentContext {
	normalized := strings.ToLower(rawIntent)
	intentType := "General"

	compareKeywords := []string{
		"\u5bf9\u6bd4",
		"\u6bd4\u8f83",
		"\u8bc4\u4f30",
		"\u9009\u578b",
		"benchmark",
		"ab test",
		"a/b",
	}
	reproduceVerbKeywords := []string{
		"\u590d\u73b0",
		"reproduce",
		"replicate",
		"rerun",
	}
	paperKeywords := []string{
		"paper",
		"\u8bba\u6587",
		"attention is all you need",
		"transformer",
	}
	codeKeywords := []string{
		"\u4ee3\u7801",
		"python",
		"\u6267\u884c",
		"\u8fd0\u884c",
		"\u8ba1\u7b97",
		"run code",
	}
	plotKeywords := []string{
		"\u753b\u56fe",
		"\u56fe\u8868",
		"\u66f2\u7ebf",
		"\u6298\u7ebf\u56fe",
		"\u67f1\u72b6\u56fe",
		"plot",
		"matplotlib",
	}
	reportKeywords := []string{
		"\u62a5\u544a",
		"\u603b\u7ed3",
		"\u8d21\u732e",
		"\u5c40\u9650",
		"\u7efc\u8ff0",
		"\u5206\u6790",
		"summary",
		"report",
		"contribution",
		"contributions",
		"limitation",
		"limitations",
		"drawback",
		"drawbacks",
	}
	researchKeywords := []string{
		"rag",
		"query rewrite",
		"rewrite",
		"\u6539\u5199",
		"\u91cd\u5199",
	}

	frameworks := extractFrameworks(normalized)
	paperTitle := extractPaperTitle(normalized)
	outputMode := detectOutputMode(normalized)
	needsReport := containsAny(normalized, reportKeywords)
	needsPlot := containsAny(normalized, plotKeywords)
	needsBenchmark := containsAny(normalized, []string{"benchmark", "\u6027\u80fd", "\u8bc4\u6d4b", "\u541e\u5410", "\u5ef6\u8fdf"})
	needsFix := containsAny(normalized, []string{"debug", "fix", "\u4fee\u590d", "\u6392\u67e5", "\u91cd\u8dd1", "\u4e0d\u4e00\u81f4"})
	isPaperSummary := needsReport && (containsAny(normalized, paperKeywords) || paperTitle != "")

	switch {
	case containsAny(normalized, compareKeywords) || len(frameworks) >= 2 || needsBenchmark:
		intentType = "Framework_Evaluation"
	case containsAny(normalized, reproduceVerbKeywords) || (needsFix && (containsAny(normalized, paperKeywords) || paperTitle != "")):
		intentType = "Paper_Reproduction"
	case containsAny(normalized, codeKeywords) || containsAny(normalized, plotKeywords):
		intentType = "Code_Execution"
	case isPaperSummary || containsAny(normalized, researchKeywords):
		intentType = "General"
	}

	entities := map[string]any{}
	if len(frameworks) > 0 {
		entities["frameworks"] = frameworks
		entities["framework_count"] = len(frameworks)
	}
	if strings.Contains(normalized, "rag") {
		entities["topic"] = "RAG"
	}
	if needsPlot {
		entities["needs_plot"] = true
	}
	if needsReport {
		entities["needs_report"] = true
	}
	if needsBenchmark {
		entities["needs_benchmark"] = true
	}
	if needsFix {
		entities["needs_fix"] = true
	}
	if paperTitle != "" {
		entities["paper_title"] = paperTitle
	}
	if outputMode != "" {
		entities["output_mode"] = outputMode
	}
	if containsAny(normalized, []string{"query rewrite", "rewrite", "\u91cd\u5199", "\u6539\u5199"}) {
		entities["topic"] = chooseTopic(entities, "Query Rewrite")
	}
	if intentType == "General" && (needsReport || strings.Contains(normalized, "rag")) {
		entities["needs_research"] = true
	}
	if isPaperSummary {
		entities["paper_task"] = "summary"
	}

	return models.IntentContext{
		RawIntent:   rawIntent,
		IntentType:  intentType,
		Entities:    entities,
		Constraints: map[string]any{},
		Metadata: map[string]any{
			"normalized_intent": normalized,
		},
	}
}

func chooseTopic(entities map[string]any, fallback string) string {
	if topic, ok := entities["topic"].(string); ok && topic != "" {
		return topic
	}
	return fallback
}

func logPlanRequest(rawIntent string, intentCtx models.IntentContext) {
	log.Printf(
		"[Planner] request intent=%q intent_type=%s entities=%s",
		rawIntent,
		intentCtx.IntentType,
		formatEntitySummary(intentCtx.Entities),
	)
}

func logPlanGraphGenerated(planGraph *models.PlanGraph) {
	log.Printf(
		"[Planner] plan_graph id=%s intent_type=%s nodes=%d edges=%d status=%s",
		planGraph.ID,
		planGraph.IntentType,
		len(planGraph.Nodes),
		len(planGraph.Edges),
		planGraph.Status,
	)
}

func logLegacyPlanFallback(plan *models.Plan, planGraph *models.PlanGraph) {
	taskCount := 0
	legacyPlanID := ""
	if plan != nil {
		legacyPlanID = plan.ID
		if plan.Tasks != nil {
			taskCount = len(plan.Tasks)
		}
	}

	log.Printf(
		"[Planner] legacy_plan_fallback enabled=true graph_id=%s legacy_plan_id=%s graph_nodes=%d legacy_tasks=%d",
		planGraph.ID,
		legacyPlanID,
		len(planGraph.Nodes),
		taskCount,
	)
}

func formatEntitySummary(entities map[string]any) string {
	if len(entities) == 0 {
		return "{}"
	}

	parts := make([]string, 0, len(entities))
	for key, value := range entities {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

func detectOutputMode(normalized string) string {
	switch {
	case containsAny(normalized, []string{"plot", "matplotlib", "\u753b\u56fe", "\u56fe\u8868", "\u53ef\u89c6\u5316"}):
		return "plot"
	case containsAny(normalized, []string{"report", "summary", "\u62a5\u544a", "\u603b\u7ed3", "\u5206\u6790"}):
		return "report"
	default:
		return ""
	}
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
