package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TheGojiOG/HytaleSM/internal/api"
	"github.com/TheGojiOG/HytaleSM/internal/backup"
	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/console"
	"github.com/TheGojiOG/HytaleSM/internal/database"
	"github.com/TheGojiOG/HytaleSM/internal/logging"
	"github.com/TheGojiOG/HytaleSM/internal/metrics"
	"github.com/TheGojiOG/HytaleSM/internal/server"
	"github.com/TheGojiOG/HytaleSM/internal/ssh"
	"github.com/TheGojiOG/HytaleSM/internal/websocket"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize server manager
	serverManager, err := config.NewServerManager(cfg.Storage.ConfigDir)
	if err != nil {
		log.Fatalf("Failed to initialize server manager: %v", err)
	}

	if err := buildAgentBinaries(cfg); err != nil {
		log.Printf("Agent build failed: %v", err)
	}

	// Set up logging
	if err := setupLogging(cfg); err != nil {
		log.Fatalf("Failed to set up logging: %v", err)
	}
	defer logging.Close()

	// Check if running migrations
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrations(cfg)
		return
	}

	// Initialize database
	db, err := database.NewDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Run migrations automatically
	log.Println("Running database migrations...")
	if err := db.Migrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	log.Println("Migrations completed successfully")


	// Initialize activity logger
	logDir := filepath.Join(cfg.Storage.DataDir, "logs", "activity")
	activityLogger, err := logging.NewActivityLogger(db.DB, logDir)
	if err != nil {
		log.Fatalf("Failed to initialize activity logger: %v", err)
	}
	defer activityLogger.Close()

	// Initialize SSH connection pool
	log.Println("Initializing SSH connection pool...")
	sshPool := ssh.NewConnectionPool(db.DB)
	defer sshPool.Stop()

	// Initialize process manager (using screen impl)
	processManager := server.NewScreenProcessManager(sshPool)

	// Initialize status detector
	executor := server.NewDefaultCommandExecutor(sshPool)
	statusDetector := server.NewStatusDetector(executor, processManager, db.DB)

	// Initialize lifecycle manager
	lifecycleManager := server.NewLifecycleManager(sshPool, processManager, statusDetector, db.DB)

	// Initialize WebSocket hub
	log.Println("Initializing WebSocket hub...")
	hub := websocket.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// Initialize console session manager
	log.Println("Initializing console session manager...")
	sessionManager := console.NewSessionManager(hub, sshPool, db.DB)

	// Start metrics collector
	metricsCollector := metrics.NewCollector(cfg, serverManager, db)
	metricsCollector.Start()
	defer metricsCollector.Stop()

	// Start backup schedule runner
	backupScheduler := backup.NewScheduleRunner(cfg, db.DB, sshPool)
	backupScheduler.Start(ctx)

	log.Println("All server components initialized successfully")

	// Set up HTTP server
	router, shutdownOps := api.SetupRouter(cfg, serverManager, db, sshPool, lifecycleManager, statusDetector, processManager, activityLogger, hub, sessionManager)

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting server on %s", server.Addr)

		if cfg.Server.TLS.Enabled {
			if err := server.ListenAndServeTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Failed to start HTTPS server: %v", err)
			}
		} else {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Failed to start HTTP server: %v", err)
			}
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop SSH pool
	log.Println("Closing SSH connections...")
	sshPool.Stop()

	// Close activity logger
	activityLogger.Close()

	// Shutdown HTTP server
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Wait for background operations
	shutdownOps()

	log.Println("Server exited")
}

func setupLogging(cfg *config.Config) error {
	if cfg != nil && strings.TrimSpace(cfg.Logging.File) == "" {
		dataDir := cfg.Storage.DataDir
		if dataDir == "" {
			dataDir = "./data"
		}
		cfg.Logging.File = filepath.Join(dataDir, "logs", "server.log")
	}
	if cfg != nil && strings.TrimSpace(cfg.Logging.File) != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Logging.File), 0755); err != nil {
			return err
		}
	}
	_, err := logging.Init(cfg.Logging)
	return err
}

func runMigrations(cfg *config.Config) {
	log.Println("Running database migrations...")

	db, err := database.NewDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	log.Println("Migrations completed successfully")
}


func buildAgentBinaries(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	dataDir := cfg.Storage.DataDir
	if dataDir == "" {
		return fmt.Errorf("data dir not configured")
	}

	modDir, err := findGoModDir()
	if err != nil {
		return err
	}

	binDir := filepath.Join(dataDir, "agent-binaries")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}

	archs := []string{"amd64", "arm64"}
	for _, arch := range archs {
		binPath := filepath.Join(binDir, fmt.Sprintf("hytale-agent-linux-%s", arch))
		if err := buildAgentBinary(modDir, arch, binPath); err != nil {
			return err
		}
	}

	return nil
}

func buildAgentBinary(modDir, arch, outputPath string) error {
	cmd := exec.Command("go", "build", "-o", outputPath, "./agent")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %s: %w: %s", arch, err, out.String())
	}
	return nil
}

func findGoModDir() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", err
	}
	current := start
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("go.mod not found from %s", start)
}
