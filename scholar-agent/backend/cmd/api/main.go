package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"scholar-agent-backend/internal/api"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	clearProxyEnv()
	configureFileLogging()

	r := gin.Default()

	// Health check endpoint
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
			"status":  "Scholar Agent Backend is running",
		})
	})

	// Setup API routes
	api.SetupRoutes(r)

	// Configure a custom HTTP server with generous timeouts for Agent execution
	server := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute, // SSE needs long write timeout
		IdleTimeout:  10 * time.Minute,
	}

	log.Println("Starting server on :8080 with 5min timeouts...")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func configureFileLogging() {
	logDir := filepath.Join(resolveProjectRoot(), "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("failed to create log dir %s: %v", logDir, err)
		return
	}

	logFile := filepath.Join(logDir, "backend.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("failed to open backend log file %s: %v", logFile, err)
		return
	}

	writer := io.MultiWriter(os.Stdout, file)
	log.SetOutput(writer)
	gin.DefaultWriter = writer
	gin.DefaultErrorWriter = writer
	log.Printf("backend logs redirected to %s", logFile)
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
