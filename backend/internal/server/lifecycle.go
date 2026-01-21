package server

import (
	"database/sql"
	"fmt"
	"log"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

// LifecycleManager orchestrates server start/stop/restart operations
type LifecycleManager struct {
	sshPool        *ssh.ConnectionPool
	processManager ProcessManager
	statusTracker  *StatusDetector
	db             *sql.DB
}

// ServerConfig represents the configuration for starting a server
type ServerConfig struct {
	ServerID       string
	SessionName    string
	WorkingDir     string
	Executable     string
	JavaArgs       []string
	ServerArgs     []string
	LogFile        string
	StartupTimeout time.Duration
	StopTimeout    time.Duration
	StopCommands   []string // Commands to send for graceful shutdown
	StopWarnings   []StopWarning
	SSHConfig      *ssh.ClientConfig // SSH connection details
	RunAsUser      string
	UseSudo        bool
}

// StopWarning represents a warning message to send before shutdown
type StopWarning struct {
	Delay   time.Duration
	Message string
}

// NewLifecycleManager creates a new lifecycle manager
func NewLifecycleManager(pool *ssh.ConnectionPool, process ProcessManager, status *StatusDetector, db *sql.DB) *LifecycleManager {
	return &LifecycleManager{
		sshPool:        pool,
		processManager: process,
		statusTracker:  status,
		db:             db,
	}
}

// StartServer starts a game server
func (lm *LifecycleManager) StartServer(serverID string, config *ServerConfig) error {
	log.Printf("[Lifecycle] Starting server %s...", serverID)
	if lm.processManager != nil {
		lm.processManager.SetRunAsUser(serverID, config.RunAsUser, config.UseSudo)
	}

	// Establish SSH connection if not already connected
	if config.SSHConfig != nil {
		log.Printf("[Lifecycle] Establishing SSH connection to %s...", serverID)
		_, err := lm.sshPool.GetConnection(serverID, config.SSHConfig)
		if err != nil {
			return fmt.Errorf("failed to establish SSH connection: %w", err)
		}
		log.Printf("[Lifecycle] SSH connection established for %s", serverID)
		if err := lm.ensureRemotePrereqs(serverID, config); err != nil {
			lm.updateStatus(serverID, "error", err.Error(), 0)
			return err
		}
	}

	// Check if server is already running
	status, err := lm.statusTracker.DetectStatus(serverID, config.SessionName)
	if err != nil {
		log.Printf("[Lifecycle] Failed to check current status: %v", err)
	} else if status != nil && (status.Status == "online" || status.Status == "starting") {
		return fmt.Errorf("server is already %s", status.Status)
	}

	// Update status to starting
	if err := lm.updateStatus(serverID, "starting", "", 0); err != nil {
		log.Printf("[Lifecycle] Warning: Failed to update status: %v", err)
	}

	// Build the Java command
	javaCmd := lm.buildJavaCommand(config)

	logFile := config.LogFile
	if logFile != "" {
		logFile = expandTildeToHomeExpr(config.LogFile, config.RunAsUser)
	}

	// Create screen session with logging
	err = lm.processManager.Start(
		serverID,
		config.SessionName,
		javaCmd,
		logFile,
	)
	if err != nil {
		lm.updateStatus(serverID, "error", fmt.Sprintf("Failed to create screen session: %v", err), 0)
		return fmt.Errorf("failed to create screen session: %w", err)
	}

	// Wait for server to start
	log.Printf("[Lifecycle] Waiting for server %s to start (timeout: %v)...", serverID, config.StartupTimeout)
	startTime := time.Now()
	deadline := startTime.Add(config.StartupTimeout)

	for time.Now().Before(deadline) {
		status, err := lm.statusTracker.DetectStatus(serverID, config.SessionName)
		if err != nil {
			log.Printf("[Lifecycle] Status check error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if status != nil && status.Status == "online" {
			elapsed := time.Since(startTime)
			log.Printf("[Lifecycle] Server %s started successfully in %v", serverID, elapsed)

			// Update status with PID
			lm.updateStatus(serverID, "online", "", status.PID)
			lm.updateServerTimes(serverID, time.Now(), time.Time{})

			return nil
		}

		time.Sleep(2 * time.Second)
	}

	// Startup timeout
	lm.updateStatus(serverID, "error", "Startup timeout exceeded", 0)
	return fmt.Errorf("server startup timeout after %v", config.StartupTimeout)
}

func (lm *LifecycleManager) ensureRemotePrereqs(serverID string, config *ServerConfig) error {
	conn := lm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	run := func(cmd string) error {
		_, err := conn.Client.RunCommand(cmd)
		return err
	}

	runAsUser := func(cmd string) (string, error) {
		wrapped := fmt.Sprintf("bash -lc %s", bashDoubleQuote(cmd))
		if config.RunAsUser != "" {
			wrapped = fmt.Sprintf("sudo -n -i -u %s bash -lc %s", bashQuote(config.RunAsUser), bashDoubleQuote(cmd))
		}
		return conn.Client.RunCommand(wrapped)
	}

	if config.RunAsUser != "" {
		probeCmd := fmt.Sprintf("sudo -n -i -u %s true", bashQuote(config.RunAsUser))
		output, err := conn.Client.RunCommand(probeCmd)
		if err != nil {
			message := strings.TrimSpace(output)
			if message == "" {
				message = "sudo failed; ensure NOPASSWD sudo and no requiretty for this user"
			}
			return fmt.Errorf("failed to run as service user: %v %s", err, message)
		}
	}

	if err := run("command -v screen >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("screen is not installed on the target host")
	}

	if config.WorkingDir != "" {
		workingDir := expandTildeToHomeExpr(config.WorkingDir, config.RunAsUser)
		if output, err := runAsUser(fmt.Sprintf("test -d %s", bashDoubleQuote(workingDir))); err != nil {
			if isSudoError(output) {
				return fmt.Errorf("failed to run as service user: %v %s", err, strings.TrimSpace(output))
			}
			who, _ := runAsUser("whoami")
			home, _ := runAsUser("echo $HOME")
			return fmt.Errorf("working directory not found: %s (runAs=%s home=%s)", config.WorkingDir, strings.TrimSpace(who), strings.TrimSpace(home))
		}
	}

	if config.LogFile != "" {
		logDir := path.Dir(expandTildeToHomeExpr(config.LogFile, config.RunAsUser))
		if logDir != "." && logDir != "/" {
			if output, err := runAsUser(fmt.Sprintf("mkdir -p %s", bashDoubleQuote(logDir))); err != nil {
				if isSudoError(output) {
					return fmt.Errorf("failed to run as service user: %v %s", err, strings.TrimSpace(output))
				}
				return fmt.Errorf("failed to create log directory: %s", logDir)
			}
		}
	}

	exec := expandTildeToHomeExpr(config.Executable, config.RunAsUser)
	if exec != "" {
		if strings.HasSuffix(strings.ToLower(exec), ".jar") {
			if output, err := runAsUser(fmt.Sprintf("test -f %s", bashDoubleQuote(exec))); err != nil {
				if isSudoError(output) {
					return fmt.Errorf("failed to run as service user: %v %s", err, strings.TrimSpace(output))
				}
				return fmt.Errorf("server jar not found: %s (deploy a release to create it)", exec)
			}
		} else if strings.Contains(exec, "/") {
			if output, err := runAsUser(fmt.Sprintf("test -x %s", bashDoubleQuote(exec))); err != nil {
				if isSudoError(output) {
					return fmt.Errorf("failed to run as service user: %v %s", err, strings.TrimSpace(output))
				}
				return fmt.Errorf("executable not found or not executable: %s", exec)
			}
		} else {
			if output, err := runAsUser(fmt.Sprintf("command -v %s >/dev/null 2>&1", exec)); err != nil {
				if isSudoError(output) {
					return fmt.Errorf("failed to run as service user: %v %s", err, strings.TrimSpace(output))
				}
				return fmt.Errorf("executable not found on PATH: %s", exec)
			}
		}
	}

	return nil
}

func bashQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func bashDoubleQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func expandTildeToHomeExpr(value, runAsUser string) string {
	if value == "" {
		return value
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		homeExpr := "$HOME"
		if runAsUser != "" {
			homeExpr = fmt.Sprintf("$(getent passwd %s | cut -d: -f6)", bashQuote(runAsUser))
		}
		if value == "~" {
			return homeExpr
		}
		return homeExpr + "/" + value[2:]
	}
	return value
}

func isSudoError(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "sudo:") || strings.Contains(text, "no tty") || strings.Contains(text, "password")
}

// StopServer stops a game server
func (lm *LifecycleManager) StopServer(serverID string, config *ServerConfig, graceful bool) error {
	log.Printf("[Lifecycle] Stopping server %s (graceful: %v)...", serverID, graceful)
	log.Printf("[Lifecycle] Looking for screen session: %s", config.SessionName)
	if lm.processManager != nil {
		lm.processManager.SetRunAsUser(serverID, config.RunAsUser, config.UseSudo)
	}

	// Check if server is running
	status, err := lm.statusTracker.DetectStatus(serverID, config.SessionName)
	if err != nil {
		log.Printf("[Lifecycle] Status detection error: %v", err)
		return fmt.Errorf("failed to check server status: %w", err)
	}

	log.Printf("[Lifecycle] Current status: %+v", status)

	if status == nil || status.Status == "offline" {
		log.Printf("[Lifecycle] Server %s is already offline, skipping stop", serverID)
		return nil
	}

	// Update status to stopping
	if err := lm.updateStatus(serverID, "stopping", "", status.PID); err != nil {
		log.Printf("[Lifecycle] Warning: Failed to update status: %v", err)
	}

	if graceful {
		// Send stop warnings
		for _, warning := range config.StopWarnings {
			if warning.Delay > 0 {
				time.Sleep(warning.Delay)
			}
			if warning.Message != "" {
				log.Printf("[Lifecycle] Sending warning: %s", warning.Message)
				if err := lm.processManager.SendCommand(serverID, config.SessionName, fmt.Sprintf("say %s", warning.Message)); err != nil {
					log.Printf("[Lifecycle] Warning: Failed to send warning: %v", err)
				}
			}
		}

		// Send stop commands
		for _, cmd := range config.StopCommands {
			log.Printf("[Lifecycle] Sending stop command: %s", cmd)
			if err := lm.processManager.SendCommand(serverID, config.SessionName, cmd); err != nil {
				log.Printf("[Lifecycle] Warning: Failed to send stop command: %v", err)
			}
			time.Sleep(1 * time.Second)
		}

		// Wait for graceful shutdown
		log.Printf("[Lifecycle] Waiting for graceful shutdown (timeout: %v)...", config.StopTimeout)
		if err := lm.waitForShutdown(serverID, config.SessionName, config.StopTimeout); err == nil {
			log.Printf("[Lifecycle] Server %s stopped gracefully", serverID)
			lm.updateStatus(serverID, "offline", "", 0)
			lm.updateServerTimes(serverID, time.Time{}, time.Now())
			return nil
		}

		log.Printf("[Lifecycle] Graceful shutdown timeout, forcing stop...")
	}

	// Force stop: Send Ctrl+C
	log.Printf("[Lifecycle] Sending Ctrl+C to session %s", config.SessionName)
	if err := lm.processManager.SendCtrlC(serverID, config.SessionName); err != nil {
		log.Printf("[Lifecycle] Warning: Failed to send Ctrl+C: %v", err)
	}

	// Wait for process to stop (60 seconds)
	if err := lm.waitForShutdown(serverID, config.SessionName, 60*time.Second); err == nil {
		log.Printf("[Lifecycle] Server %s stopped after Ctrl+C", serverID)
		lm.updateStatus(serverID, "offline", "", 0)
		lm.updateServerTimes(serverID, time.Time{}, time.Now())
		return nil
	}

	// Last resort: Force kill screen session
	log.Printf("[Lifecycle] Force killing screen session %s", config.SessionName)
	if err := lm.processManager.Stop(serverID, config.SessionName); err != nil {
		log.Printf("[Lifecycle] Warning: Failed to quit session: %v", err)
	}

	// Final verification
	time.Sleep(2 * time.Second)
	status, err = lm.statusTracker.DetectStatus(serverID, config.SessionName)
	if err == nil && status != nil && status.Status != "offline" {
		return fmt.Errorf("failed to stop server completely")
	}

	log.Printf("[Lifecycle] Server %s stopped (forced)", serverID)
	lm.updateStatus(serverID, "offline", "", 0)
	lm.updateServerTimes(serverID, time.Time{}, time.Now())

	return nil
}

// RestartServer restarts a game server
func (lm *LifecycleManager) RestartServer(serverID string, config *ServerConfig, graceful bool) error {
	log.Printf("[Lifecycle] Restarting server %s...", serverID)

	// Stop the server
	if err := lm.StopServer(serverID, config, graceful); err != nil {
		return fmt.Errorf("failed to stop server: %w", err)
	}

	// Wait a moment before restart
	time.Sleep(3 * time.Second)

	// Start the server
	if err := lm.StartServer(serverID, config); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	log.Printf("[Lifecycle] Server %s restarted successfully", serverID)

	return nil
}

// GetServerPID retrieves the process ID of a running server
func (lm *LifecycleManager) GetServerPID(serverID, sessionName string) (int, error) {
	status, err := lm.statusTracker.DetectStatus(serverID, sessionName)
	if err != nil {
		return 0, err
	}

	if status == nil {
		return 0, fmt.Errorf("server status not found")
	}

	return status.PID, nil
}

// buildJavaCommand constructs the full Java command to execute
func (lm *LifecycleManager) buildJavaCommand(config *ServerConfig) string {
	workingDir := expandTildeToHomeExpr(config.WorkingDir, config.RunAsUser)

	// Check if executable is a JAR file
	if strings.HasSuffix(strings.ToLower(config.Executable), ".jar") {
		// Build Java command for JAR files
		parts := []string{"cd", workingDir, "&&", "java"}

		// Add Java arguments
		parts = append(parts, config.JavaArgs...)

		// Add -jar and executable
		parts = append(parts, "-jar", filepath.Base(config.Executable))

		// Add server arguments
		parts = append(parts, config.ServerArgs...)

		cmd := strings.Join(parts, " ")
		return cmd
	}

	// For non-JAR executables (scripts, binaries), run directly
	parts := []string{"cd", workingDir, "&&", config.Executable}
	
	// Add server arguments
	parts = append(parts, config.ServerArgs...)

	cmd := strings.Join(parts, " ")
	return cmd
}

// waitForShutdown waits for a server to shut down
func (lm *LifecycleManager) waitForShutdown(serverID, sessionName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		status, err := lm.statusTracker.DetectStatus(serverID, sessionName)
		if err != nil {
			log.Printf("[Lifecycle] Status check error during shutdown wait: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if status == nil || status.Status == "offline" {
			return nil
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("shutdown timeout exceeded")
}

// updateStatus updates the server_status table
func (lm *LifecycleManager) updateStatus(serverID, status, errorMsg string, pid int) error {
	if lm.db == nil {
		return nil
	}

	query := `
		INSERT INTO server_status (server_id, status, pid, error_message, last_checked, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET
			status = excluded.status,
			pid = excluded.pid,
			error_message = excluded.error_message,
			last_checked = excluded.last_checked,
			updated_at = excluded.updated_at
	`

	now := time.Now()
	_, err := lm.db.Exec(query, serverID, status, pid, errorMsg, now, now)
	if err != nil {
		return fmt.Errorf("failed to update server status: %w", err)
	}

	return nil
}

// updateServerTimes updates start/stop times in server_status
func (lm *LifecycleManager) updateServerTimes(serverID string, startTime, stopTime time.Time) error {
	if lm.db == nil {
		return nil
	}

	if !startTime.IsZero() {
		_, err := lm.db.Exec(`
			UPDATE server_status 
			SET last_started = ? 
			WHERE server_id = ?
		`, startTime, serverID)
		if err != nil {
			return fmt.Errorf("failed to update start time: %w", err)
		}
	}

	if !stopTime.IsZero() {
		_, err := lm.db.Exec(`
			UPDATE server_status 
			SET last_stopped = ? 
			WHERE server_id = ?
		`, stopTime, serverID)
		if err != nil {
			return fmt.Errorf("failed to update stop time: %w", err)
		}
	}

	return nil
}

// SendCommand sends a command to a running server
func (lm *LifecycleManager) SendCommand(serverID, sessionName, command string) error {
	// Verify server is running
	status, err := lm.statusTracker.DetectStatus(serverID, sessionName)
	if err != nil {
		return fmt.Errorf("failed to check server status: %w", err)
	}

	if status == nil || status.Status != "online" {
		return fmt.Errorf("server is not online (status: %s)", status.Status)
	}

	// Send command via process manager
	return lm.processManager.SendCommand(serverID, sessionName, command)
}

// NewServerConfig creates a default server configuration
func NewServerConfig(serverID string) *ServerConfig {
	return &ServerConfig{
		ServerID:       serverID,
		SessionName:    fmt.Sprintf("hytale-%s", serverID),
		WorkingDir:     fmt.Sprintf("/opt/hytale/%s", serverID),
		Executable:     "server.jar",
		JavaArgs:       []string{"-Xmx2G", "-Xms2G"},
		ServerArgs:     []string{"nogui"},
		LogFile:        fmt.Sprintf("/opt/hytale/%s/console.log", serverID),
		StartupTimeout: 60 * time.Second,
		StopTimeout:    60 * time.Second,
		StopCommands:   []string{"stop"},
		StopWarnings: []StopWarning{
			{Delay: 0, Message: "Server shutting down in 60 seconds..."},
			{Delay: 30 * time.Second, Message: "Server shutting down in 30 seconds..."},
			{Delay: 20 * time.Second, Message: "Server shutting down in 10 seconds..."},
		},
	}
}
