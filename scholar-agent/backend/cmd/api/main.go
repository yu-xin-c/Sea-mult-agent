package main

import (
	"log"
	"net/http"
	"scholar-agent-backend/internal/api"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}
	// 在这里添加强制开启颜色的代码
	gin.ForceConsoleColor()
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
