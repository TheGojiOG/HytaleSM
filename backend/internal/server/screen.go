package server

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

// ScreenProcessManager handles interactions with GNU Screen sessions
type ScreenProcessManager struct {
	sshPool *ssh.ConnectionPool
	mu      sync.RWMutex
	runAs   map[string]screenRunAs
}

type screenRunAs struct {
	user    string
	useSudo bool
}

// ScreenSession represents a screen session
type ScreenSession struct {
	Name      string
	PID       int
	Status    string // "Attached", "Detached"
	StartedAt time.Time
}

// NewScreenProcessManager creates a new screen manager
func NewScreenProcessManager(pool *ssh.ConnectionPool) *ScreenProcessManager {
	return &ScreenProcessManager{
		sshPool: pool,
		runAs:   make(map[string]screenRunAs),
	}
}

// SetRunAsUser configures which user should own/manage the screen session
func (sm *ScreenProcessManager) SetRunAsUser(serverID, runAsUser string, useSudo bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if strings.TrimSpace(runAsUser) == "" {
		delete(sm.runAs, serverID)
		return
	}
	sm.runAs[serverID] = screenRunAs{user: strings.TrimSpace(runAsUser), useSudo: useSudo}
}

// Start starts a new process in a screen session with logging
func (sm *ScreenProcessManager) Start(serverID, sessionName, command, logFile string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Create screen session with tee for logging
	cmdForShell := expandTildeToHomeVarForShell(command)
	logFileForShell := expandTildeToHomeVarForShell(logFile)
	
	// Set COLUMNS=500 in the environment so applications see a wide terminal
	// The key is setting it INSIDE the bash that runs the actual command
	screenCmd := fmt.Sprintf("screen -dmS %s bash -lc \"export COLUMNS=500 LINES=100; %s 2>&1 | tee -a %s\"",
		sessionName,
		escapeForDoubleQuotes(cmdForShell),
		escapeForDoubleQuotes(logFileForShell),
	)

	wrappedCmd := sm.wrapForUser(serverID, screenCmd)
	output, err := conn.Client.RunCommand(wrappedCmd)
	if err != nil {
		return fmt.Errorf("failed to create screen session with logging: %w (output: %s)", err, output)
	}

	// Verify session was created
	time.Sleep(500 * time.Millisecond)
	exists, err := sm.IsRunning(serverID, sessionName)
	if err != nil {
		return fmt.Errorf("failed to verify session creation: %w", err)
	}

	if !exists {
		return fmt.Errorf("screen session created but not found in session list")
	}

	log.Printf("[Screen] Created session %s with logging to %s", sessionName, logFile)

	return nil
}

// IsRunning checks if a screen session exists
func (sm *ScreenProcessManager) IsRunning(serverID, sessionName string) (bool, error) {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return false, fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// List screen sessions and grep for the session name
	// Using -q for quiet mode (exit code only)
	checkCmd := fmt.Sprintf("screen -list | grep -q '%s'", sessionName)

	_, err := conn.Client.RunCommand(sm.wrapForUser(serverID, checkCmd))
	if err != nil {
		// grep returns non-zero if no match found
		errText := err.Error()
		if strings.Contains(errText, "exit status") || strings.Contains(errText, "status 1") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check session existence: %w", err)
	}

	return true, nil
}

// GetSession retrieves information about a specific screen session
func (sm *ScreenProcessManager) GetSession(serverID, sessionName string) (*ScreenSession, error) {
	sessions, err := sm.ListSessions(serverID)
	if err != nil {
		return nil, err
	}

	for _, session := range sessions {
		if session.Name == sessionName {
			return &session, nil
		}
	}

	return nil, fmt.Errorf("session %s not found", sessionName)
}

// ListSessions lists all screen sessions
func (sm *ScreenProcessManager) ListSessions(serverID string) ([]ScreenSession, error) {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return nil, fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// List all screen sessions
	output, err := conn.Client.RunCommand(sm.wrapForUser(serverID, "screen -list"))
	if err != nil {
		// screen -list returns exit code 1 if no sessions exist
		if strings.Contains(output, "No Sockets found") {
			return []ScreenSession{}, nil
		}
		// Only return error if it's not the "no sockets" case
		errText := err.Error()
		if !strings.Contains(errText, "exit status 1") && !strings.Contains(errText, "status 1") {
			return nil, fmt.Errorf("failed to list screen sessions: %w", err)
		}
	}

	return sm.parseScreenList(output), nil
}

// parseScreenList parses the output of 'screen -list'
func (sm *ScreenProcessManager) parseScreenList(output string) []ScreenSession {
	sessions := make([]ScreenSession, 0)

	// Screen list format:
	// 	12345.session-name	(date) (Attached/Detached)
	// Example:
	// 	1234.hytale-server1	(01/16/2026 12:00:00 PM)	(Detached)

	// Regex to match screen sessions
	// Captures: PID, session name, status
	re := regexp.MustCompile(`^\s*(\d+)\.(\S+)\s+\([^)]+\)\s+\((\w+)\)`)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		matches := re.FindStringSubmatch(line)
		if len(matches) >= 4 {
			pid := 0
			fmt.Sscanf(matches[1], "%d", &pid)

			session := ScreenSession{
				Name:   matches[2],
				PID:    pid,
				Status: matches[3],
			}
			sessions = append(sessions, session)
		}
	}

	return sessions
}

// SendCommand sends a command to a screen session
func (sm *ScreenProcessManager) SendCommand(serverID, sessionName, command string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Verify session exists
	exists, err := sm.IsRunning(serverID, sessionName)
	if err != nil {
		return fmt.Errorf("failed to verify session: %w", err)
	}
	if !exists {
		return fmt.Errorf("screen session %s does not exist", sessionName)
	}

	// Send command using screen -X stuff
	// The command is sent to the session's stdin
	// We add \n to execute the command
	stuffCmd := fmt.Sprintf("screen -S %s -X stuff '%s\n'", sessionName, escapeCommand(command))

	output, err := conn.Client.RunCommand(sm.wrapForUser(serverID, stuffCmd))
	if err != nil {
		return fmt.Errorf("failed to send command to screen: %w (output: %s)", err, output)
	}

	log.Printf("[Screen] Sent command to session %s: %s", sessionName, command)

	return nil
}

// escapeCommand escapes special characters in commands for screen
func escapeCommand(command string) string {
	// Escape single quotes by replacing ' with '\''
	// This allows us to wrap the command in single quotes
	escaped := strings.ReplaceAll(command, "'", "'\\''")
	return escaped
}

func escapeForDoubleQuotes(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}

func expandTildeToHomeVarForShell(value string) string {
	if value == "~" {
		return "$HOME"
	}
	if strings.HasPrefix(value, "~/") {
		return "$HOME/" + value[2:]
	}
	return value
}

// AttachSession attaches to a screen session (blocking operation)
// Note: This is typically not used in automated management,
// use PTYManager.AttachToScreen for interactive console access
func (sm *ScreenProcessManager) AttachSession(serverID, sessionName string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Verify session exists
	exists, err := sm.IsRunning(serverID, sessionName)
	if err != nil {
		return fmt.Errorf("failed to verify session: %w", err)
	}
	if !exists {
		return fmt.Errorf("screen session %s does not exist", sessionName)
	}

	// This is a blocking operation that would require PTY support
	// For actual interactive use, use PTYManager instead
	return fmt.Errorf("use PTYManager.AttachToScreen for interactive console access")
}

// DetachSession detaches from a screen session
func (sm *ScreenProcessManager) DetachSession(serverID, sessionName string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Send detach command to the session
	detachCmd := fmt.Sprintf("screen -S %s -X detach", sessionName)

	output, err := conn.Client.RunCommand(sm.wrapForUser(serverID, detachCmd))
	if err != nil {
		return fmt.Errorf("failed to detach session: %w (output: %s)", err, output)
	}

	log.Printf("[Screen] Detached session %s for server %s", sessionName, serverID)

	return nil
}

// Stop terminates a screen session
func (sm *ScreenProcessManager) Stop(serverID, sessionName string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Verify session exists before trying to quit
	exists, err := sm.IsRunning(serverID, sessionName)
	if err != nil {
		return fmt.Errorf("failed to verify session: %w", err)
	}
	if !exists {
		log.Printf("[Screen] Session %s already does not exist", sessionName)
		return nil // Not an error if it's already gone
	}

	// Send quit command to terminate the session
	quitCmd := fmt.Sprintf("screen -S %s -X quit", sessionName)

	output, err := conn.Client.RunCommand(sm.wrapForUser(serverID, quitCmd))
	if err != nil {
		return fmt.Errorf("failed to quit session: %w (output: %s)", err, output)
	}

	// Verify session is gone
	time.Sleep(500 * time.Millisecond)
	exists, _ = sm.IsRunning(serverID, sessionName)
	if exists {
		log.Printf("[Screen] Warning: Session %s still exists after quit command", sessionName)
	}

	log.Printf("[Screen] Quit session %s for server %s", sessionName, serverID)

	return nil
}

// SendCtrlC sends Ctrl+C to a screen session
func (sm *ScreenProcessManager) SendCtrlC(serverID, sessionName string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Send Ctrl+C (0x03) to the session
	// Using stuff with literal control character
	ctrlCCmd := fmt.Sprintf("screen -S %s -X stuff $'\\003'", sessionName)

	output, err := conn.Client.RunCommand(sm.wrapForUser(serverID, ctrlCCmd))
	if err != nil {
		return fmt.Errorf("failed to send Ctrl+C: %w (output: %s)", err, output)
	}

	log.Printf("[Screen] Sent Ctrl+C to session %s", sessionName)

	return nil
}

// GetPID retrieves the process ID of a screen session
func (sm *ScreenProcessManager) GetPID(serverID, sessionName string) (int, error) {
	session, err := sm.GetSession(serverID, sessionName)
	if err != nil {
		return 0, err
	}

	return session.PID, nil
}

// Kill forcefully kills a screen session
func (sm *ScreenProcessManager) Kill(serverID, sessionName string) error {
	conn := sm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Get the session PID
	pid, err := sm.GetPID(serverID, sessionName)
	if err != nil {
		// Session might not exist
		return nil
	}

	// Kill the screen process
	killCmd := fmt.Sprintf("kill -9 %d", pid)

	output, err := conn.Client.RunCommand(sm.wrapForUser(serverID, killCmd))
	if err != nil {
		return fmt.Errorf("failed to kill session: %w (output: %s)", err, output)
	}

	log.Printf("[Screen] Forcefully killed session %s (PID: %d)", sessionName, pid)

	return nil
}

// IsSessionAttached checks if a session is currently attached
func (sm *ScreenProcessManager) IsSessionAttached(serverID, sessionName string) (bool, error) {
	session, err := sm.GetSession(serverID, sessionName)
	if err != nil {
		return false, err
	}

	return session.Status == "Attached", nil
}

// WaitForSessionExit waits for a screen session to exit
func (sm *ScreenProcessManager) WaitForSessionExit(serverID, sessionName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		exists, err := sm.IsRunning(serverID, sessionName)
		if err != nil {
			return fmt.Errorf("failed to check session: %w", err)
		}

		if !exists {
			log.Printf("[Screen] Session %s has exited", sessionName)
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for session %s to exit", sessionName)
}

func (sm *ScreenProcessManager) wrapForUser(serverID, cmd string) string {
	sm.mu.RLock()
	config, ok := sm.runAs[serverID]
	sm.mu.RUnlock()
	if !ok || config.user == "" || !config.useSudo {
		return cmd
	}
	return fmt.Sprintf("sudo -n -i -u %s bash -lc %s", bashQuote(config.user), bashDoubleQuote(cmd))
}
