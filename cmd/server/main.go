// Package main is the entry point for the Openagent-Github-Bridge server.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openagent/github-bridge/internal/agent"
	"github.com/openagent/github-bridge/internal/config"
	"github.com/openagent/github-bridge/internal/handler"
	"github.com/openagent/github-bridge/internal/queue"
	"github.com/openagent/github-bridge/internal/service"
	"github.com/openagent/github-bridge/internal/session"
)

func main() {
	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Set Gin mode based on log level
	if cfg.Log.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize components
	sessionManager, err := session.NewManager(context.Background(), cfg.Session)
	if err != nil {
		log.Fatalf("Failed to initialize session manager: %v", err)
	}
	defer func() {
		if err := sessionManager.Close(); err != nil {
			log.Printf("Failed to close session manager: %v", err)
		}
	}()
	// Use MultiRepoOpenCodeAdapter for multi-repo support
	// Routes tasks to repo-specific OpenCode instances based on config
	openCodeAgent := agent.NewMultiRepoOpenCodeAdapter(cfg)
	taskQueue := queue.NewTaskQueue(cfg.Queue.BufferSize)

	// Log configured repositories
	if openCodeAgent.IsMultiRepoMode() {
		log.Printf("Multi-repo mode enabled with %d repositories", len(cfg.Repositories))
		for repo := range cfg.Repositories {
			owner, name, _ := strings.Cut(repo, "/")
			repoCfg := cfg.GetOpenCodeConfigForRepo(owner, name)
			log.Printf("  - %s -> opencode=%s worktree-manager=%s", repo, repoCfg.Host, repoCfg.WorktreeManagerHost)
		}
	} else {
		log.Printf("Single-repo mode: opencode=%s worktree-manager=%s", cfg.OpenCode.Host, cfg.OpenCode.WorktreeManagerHost)
	}

	// Initialize bridge service (fire-and-forget pattern)
	bridgeService := service.NewBridgeService(
		sessionManager,
		openCodeAgent,
		cfg.Trigger,
		cfg.Features,
	)

	// Start queue workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < cfg.Queue.Workers; i++ {
		go taskQueue.StartWorker(ctx, bridgeService)
	}

	// Start session cleanup routine
	go sessionManager.StartCleanup(ctx, 10*time.Minute)

	// Setup HTTP router
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// Register handlers
	webhookHandler := handler.NewWebhookHandler(cfg.GitHub, taskQueue)
	healthHandler := handler.NewHealthHandler(openCodeAgent, "1.0.0")
	router.POST("/webhook", webhookHandler.HandleWebhook)
	router.GET("/health", healthHandler.HandleHealth)

	// Create HTTP server
	srv := &http.Server{
		Addr:         cfg.Server.Address(),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting server on %s", cfg.Server.Address())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Cancel context to stop workers
	cancel()

	// Shutdown HTTP server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited gracefully")
}
