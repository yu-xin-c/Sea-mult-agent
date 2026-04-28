package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"scholar-agent-backend/internal/api"
	"scholar-agent-backend/internal/sandboxserver"
	"scholar-agent-backend/webui"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	clearProxyEnv()
	configureFileLogging()

	sandboxShutdown, err := ensureSandboxURL()
	if err != nil {
		log.Fatalf("failed to prepare embedded sandbox: %v", err)
	}
	if sandboxShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdownErr := sandboxShutdown(ctx); shutdownErr != nil {
				log.Printf("embedded sandbox shutdown failed: %v", shutdownErr)
			}
		}()
	}

	r := gin.Default()
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
			"status":  "Scholar Agent App is running",
		})
	})

	api.SetupRoutes(r)
	registerEmbeddedFrontend(r)

	listenAddr := resolveListenAddr()
	server := &http.Server{
		Addr:         listenAddr,
		Handler:      r,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  10 * time.Minute,
	}

	log.Printf("Starting single-binary app on %s", listenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("failed to start single-binary app: %v", err)
	}
}

func ensureSandboxURL() (func(context.Context) error, error) {
	if strings.TrimSpace(os.Getenv("SANDBOX_URL")) != "" {
		return nil, nil
	}

	// 单文件模式自动拉起本地沙箱，后端继续沿用已有的 HTTP 客户端调用方式。
	sandboxURL, shutdown, err := sandboxserver.StartLocal()
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("SANDBOX_URL", sandboxURL); err != nil {
		return nil, err
	}
	log.Printf("embedded sandbox is listening on %s", sandboxURL)
	return shutdown, nil
}

func registerEmbeddedFrontend(r *gin.Engine) {
	distFS, err := webui.DistFS()
	if err != nil {
		log.Printf("embedded frontend is unavailable: %v", err)
		return
	}

	fileServer := http.FileServer(http.FS(distFS))
	r.NoRoute(func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		if isAPINotFound(requestPath) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// SPA 场景下优先尝试命中真实静态文件，找不到时统一回退到 index.html。
		assetPath := normalizeAssetPath(requestPath)
		if assetExists(distFS, assetPath) {
			c.Request.URL.Path = "/" + assetPath
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/index.html"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

func isAPINotFound(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") || requestPath == "/ping"
}

func normalizeAssetPath(requestPath string) string {
	cleaned := path.Clean("/" + requestPath)
	trimmed := strings.TrimPrefix(cleaned, "/")
	if trimmed == "" || trimmed == "." {
		return "index.html"
	}
	return trimmed
}

func assetExists(distFS fs.FS, assetPath string) bool {
	info, err := fs.Stat(distFS, assetPath)
	return err == nil && !info.IsDir()
}

func resolveListenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("APP_ADDR")); addr != "" {
		return addr
	}

	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if strings.HasPrefix(port, ":") {
			return port
		}
		return ":" + port
	}

	return ":8080"
}

func configureFileLogging() {
	logDir := filepath.Join(resolveProjectRoot(), "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Printf("failed to create log dir %s: %v", logDir, err)
		return
	}

	logFile := filepath.Join(logDir, "app.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Printf("failed to open app log file %s: %v", logFile, err)
		return
	}

	writer := io.MultiWriter(os.Stdout, file)
	log.SetOutput(writer)
	gin.DefaultWriter = writer
	gin.DefaultErrorWriter = writer
	log.Printf("single-binary logs redirected to %s", logFile)
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

func clearProxyEnv() {
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy",
	} {
		_ = os.Unsetenv(key)
	}

	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		_ = os.Setenv(key, "localhost,127.0.0.1,::1")
	}
}
