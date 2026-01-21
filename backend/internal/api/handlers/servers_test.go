package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yourusername/hytale-server-manager/internal/auth"
	"github.com/yourusername/hytale-server-manager/internal/config"
	"github.com/yourusername/hytale-server-manager/internal/database"
	"github.com/yourusername/hytale-server-manager/internal/logging"
	"github.com/yourusername/hytale-server-manager/internal/server"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
	ws "github.com/yourusername/hytale-server-manager/internal/websocket"
	"modernc.org/sqlite"
)

// Prevent unused import error if I don't use it immediately
var _ = sqlite.Driver{}

func setupTestServerHandler(t *testing.T) (*ServerHandler, *MockProcessManager, *server.MockCommandExecutor, *config.ServerManager) {
	// 1. Config
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	os.MkdirAll(logDir, 0755)

	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "debug",
			File:  filepath.Join(logDir, "activity.log"),
		},
		Storage: config.StorageConfig{
			ConfigDir: tmpDir,
		},
	}

	// 2. Server Manager
	sm, err := config.NewServerManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create server manager: %v", err)
	}
	
	// Add test server
	initialServer := config.ServerDefinition{
		ID:   "test-server",
		Name: "Test Server",
		Connection: config.ConnectionConfig{
			Host:       "localhost",
			Port:       22,
			Username:   "test",
			AuthMethod: "password",
			Password:   "test",
		},
		Server: config.GameServerConfig{
			Executable:       "java",
			WorkingDirectory: tmpDir,
			ProcessManager:   "screen",
		},
	}
	if err := sm.Add(initialServer); err != nil {
		t.Fatalf("Failed to add test server: %v", err)
	}

	// 3. RBAC (Dummy DB for now)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open mock db: %v", err)
	}
	dbWrapper := &database.DB{DB: db}
	rbac := auth.NewRBACManager(db)

	// 4. Mock Process Manager
	mockPM := NewMockProcessManager()

	// 5. SSH Pool (Dummy)
	sshPool := ssh.NewConnectionPool(db)

	// 6. Support Dependencies
	mockExecutor := &server.MockCommandExecutor{}
	status := server.NewStatusDetector(mockExecutor, mockPM, db)
	
	// Create activity logs table for the logger
	_, _ = db.Exec(`CREATE TABLE activity_log (
		id INTEGER PRIMARY KEY,
		timestamp DATETIME,
		server_id TEXT,
		user_id INTEGER,
		activity_type TEXT,
		description TEXT,
		metadata TEXT,
		success BOOLEAN,
		error_message TEXT
	)`)
	
	// Activity Logger needs valid DB and existing dir
	activityLogger, err := logging.NewActivityLogger(db, logDir)
	if err != nil {
		t.Fatalf("Failed to create activity logger: %v", err)
	}

	// 7. Lifecycle Manager
	lifecycle := server.NewLifecycleManager(sshPool, mockPM, status, db)
	hub := ws.NewHub()

	handler := NewServerHandler(
		cfg,
		dbWrapper,
		sm,
		rbac,
		sshPool,
		lifecycle,
		status,
		mockPM,
		activityLogger,
		hub,
	)

	return handler, mockPM, mockExecutor, sm
}

func TestServerHandler_ListServers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, _, _ := setupTestServerHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	handler.ListServers(c)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var servers []config.ServerDefinition
	err := json.Unmarshal(w.Body.Bytes(), &servers)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(servers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(servers))
	}
	if servers[0].ID != "test-server" {
		t.Errorf("Expected server ID 'test-server', got '%s'", servers[0].ID)
	}
}

func TestServerHandler_StartServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, mockPM, mockExec, _ := setupTestServerHandler(t)

	// Configure mock executor for success
	mockExec.Handlers = map[string]func(string) (string, error){
		"ps -o pid": func(c string) (string, error) {
			// ps list output. screenPID is 12345 (from mockPM).
			// We need a java process child of 12345.
			// Format: PID PPID COMM
			return "20000 12345 java\n12345 1 SCREEN", nil
		},
		"ps -o etimes": func(c string) (string, error) {
			// Uptime in seconds
			return "60", nil
		},
		"netstat": func(c string) (string, error) {
			return "", nil // Port listening (no error = listening? No, grep returns 0 exit code)
			// Wait, if execution succeeds and output is empty?
			// The command is `netstat ... | grep ...`.
			// If grep finds match, it outputs match line.
			// So we should return "some line" to simulate match?
			// "tcp 0 0 0.0.0.0:25565 ..."
			return "tcp 0 0 0.0.0.0:25565 LISTEN", nil
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

    // Setup params and claims
    c.Params = gin.Params{{Key: "id", Value: "test-server"}}
    c.Set("user", &auth.Claims{UserID: 1, Username: "admin"})

	handler.StartServer(c)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d. Body: %s", w.Code, w.Body.String())
	}

    // Give goroutine a moment (StartServer spins off a goroutine)
    handler.WaitForCompletion()

    // Verify process started
    running, _ := mockPM.IsRunning("test-server", "")
    if !running {
        t.Error("Expected server 'test-server' to be running")
    }

    // Close logger to release file lock for cleanup
    handler.activityLogger.Close()
}
