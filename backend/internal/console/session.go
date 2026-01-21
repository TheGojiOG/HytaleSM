package console

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yourusername/hytale-server-manager/internal/ssh"
	"github.com/yourusername/hytale-server-manager/internal/websocket"
)

// Session represents an active console session
type Session struct {
	ID              string
	ServerID        string
	ScreenSession   string
	SSHConnection   *ssh.PooledConnection
	RunAsUser       string
	UseSudo         bool
	Hub             *websocket.Hub
	Room            string
	Buffer          *RingBuffer
	db              *sql.DB
	cancel          context.CancelFunc
	mu              sync.RWMutex
	lastActivity    time.Time
	isActive        bool
	outputChan      chan string
	logWriter       *LogWriter
	lastResizeTarget string
	lastResizeTime   time.Time
}

// SessionManager manages console sessions for multiple servers
type SessionManager struct {
	sessions map[string]*Session
	hub      *websocket.Hub
	sshPool  *ssh.ConnectionPool
	db       *sql.DB
	mu       sync.RWMutex
}

// RingBuffer implements a circular buffer for console output
type RingBuffer struct {
	lines    []string
	maxLines int
	current  int
	full     bool
	mu       sync.RWMutex
}

// Match all ANSI/VT100 escape sequences including CSI, OSC, and other control sequences
var ansiEscapePattern = regexp.MustCompile(`\x1b(\[[0-9;?!]*[A-Za-z>hp]|\([B0]|[=>])`)

// NewRingBuffer creates a new ring buffer
func NewRingBuffer(maxLines int) *RingBuffer {
	return &RingBuffer{
		lines:    make([]string, maxLines),
		maxLines: maxLines,
		current:  0,
		full:     false,
	}
}

// Add adds a line to the buffer
func (rb *RingBuffer) Add(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.lines[rb.current] = line
	rb.current = (rb.current + 1) % rb.maxLines

	if rb.current == 0 {
		rb.full = true
	}
}

// GetLines returns all lines in order (oldest to newest)
func (rb *RingBuffer) GetLines() []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if !rb.full {
		return rb.lines[:rb.current]
	}

	// Rebuild in correct order
	result := make([]string, rb.maxLines)
	for i := 0; i < rb.maxLines; i++ {
		result[i] = rb.lines[(rb.current+i)%rb.maxLines]
	}
	return result
}

// GetLast returns the last N lines
func (rb *RingBuffer) GetLast(n int) []string {
	lines := rb.GetLines()
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// NewSessionManager creates a new console session manager
func NewSessionManager(hub *websocket.Hub, sshPool *ssh.ConnectionPool, db *sql.DB) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		hub:      hub,
		sshPool:  sshPool,
		db:       db,
	}
}

// StartSession starts a new console session for a server
func (sm *SessionManager) StartSession(serverID, screenSession string, sshConn *ssh.PooledConnection, runAsUser string, useSudo bool) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if session already exists
	if session, exists := sm.sessions[serverID]; exists && session.isActive {
		log.Printf("[Console] Session already exists for server %s", serverID)
		return session, nil
	}

	// Create new session
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		ID:            fmt.Sprintf("console-%s-%d", serverID, time.Now().Unix()),
		ServerID:      serverID,
		ScreenSession: screenSession,
		SSHConnection: sshConn,
		RunAsUser:     runAsUser,
		UseSudo:       useSudo,
		Hub:           sm.hub,
		Room:          fmt.Sprintf("console:%s", serverID),
		Buffer:        NewRingBuffer(1000), // Last 1000 lines
		db:            sm.db,
		cancel:        cancel,
		lastActivity:  time.Now(),
		isActive:      true,
		outputChan:    make(chan string, 100),
	}

	// Start output reader
	go session.readScreenOutput(ctx)

	// Start output broadcaster
	go session.broadcastOutput(ctx)

	sm.sessions[serverID] = session
	log.Printf("[Console] Started session %s for server %s (screen: %s)", session.ID, serverID, screenSession)

	return session, nil
}

// GetSession returns an active session for a server
func (sm *SessionManager) GetSession(serverID string) (*Session, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[serverID]
	if !exists || !session.isActive {
		return nil, fmt.Errorf("no active session for server %s", serverID)
	}

	return session, nil
}

// StopSession stops a console session
func (sm *SessionManager) StopSession(serverID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[serverID]
	if !exists {
		return fmt.Errorf("no session found for server %s", serverID)
	}

	session.cancel()
	session.isActive = false
	delete(sm.sessions, serverID)

	log.Printf("[Console] Stopped session for server %s", serverID)
	return nil
}

// readScreenOutput reads output from screen session
func (s *Session) readScreenOutput(ctx context.Context) {
	defer close(s.outputChan)

	// Instead of hardcopy, attach with wide PTY and capture output
	buildCmd := func(target string) string {
		// Use timeout to attach briefly, capture screen state, then auto-disconnect
		return fmt.Sprintf("timeout 0.1 screen -r %s || true", target)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastLines []string

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			target := s.resolveScreenSessionID()
			if target == "" {
				target = s.ScreenSession
			}
			
			// Attach with a 500-column PTY to force screen to display wide
			output, err := s.runCommandWithPTY(buildCmd(target), 500, 100)
			if err != nil {
				if strings.Contains(output, "No screen session found") {
					if resolved := s.resolveScreenSessionID(); resolved != "" && resolved != s.ScreenSession {
						output, err = s.runCommandWithPTY(buildCmd(resolved), 500, 100)
					}
				}
			}
			if err != nil && !strings.Contains(err.Error(), "exit status 124") && !strings.Contains(err.Error(), "exit status 1") {
				trimmedOutput := strings.TrimSpace(output)
				if trimmedOutput != "" {
					log.Printf("[Console] Failed to read screen output: %v (output: %s)", err, trimmedOutput)
				} else {
					log.Printf("[Console] Failed to read screen output: %v", err)
				}
				continue
			}

			trimmed := strings.TrimRight(output, "\r\n")
			if trimmed == "" {
				continue
			}

			normalized := strings.ReplaceAll(trimmed, "\r", "")
			lines := strings.Split(normalized, "\n")
			if len(lines) == 0 {
				continue
			}

			// Skip update if output hasn't changed at all
			if len(lines) == len(lastLines) && linesEqual(lines, lastLines) {
				continue
			}

			startIndex := 0
			if len(lastLines) > 0 {
				if len(lines) >= len(lastLines) && linesEqual(lines[:len(lastLines)], lastLines) {
					startIndex = len(lastLines)
				} else {
					lastLine := lastLines[len(lastLines)-1]
					idx := lastIndex(lines, lastLine)
					if idx >= 0 && idx+1 < len(lines) {
						startIndex = idx + 1
					}
				}
			}

			if startIndex < len(lines) {
				for _, line := range lines[startIndex:] {
					clean := sanitizeConsoleLine(line)
					if clean == "" {
						continue
					}
					select {
					case s.outputChan <- clean:
					case <-ctx.Done():
						return
					}
				}
			}

			lastLines = lines
		}
	}
}

func (s *Session) ensureResize(target string) {
	s.mu.Lock()
	shouldResize := false
	if s.lastResizeTarget != target || time.Since(s.lastResizeTime) > 30*time.Second {
		shouldResize = true
		s.lastResizeTarget = target
		s.lastResizeTime = time.Now()
	}
	s.mu.Unlock()

	if shouldResize {
		if err := s.resizeScreen(target); err != nil {
			log.Printf("[Console] Failed to resize screen for %s (target=%s): %v", s.ServerID, target, err)
		}
	}
}

func (s *Session) resizeScreen(target string) error {
	// Attach with a wide PTY, send window change, then detach
	// This actually forces screen to recognize the new width
	resizeScript := fmt.Sprintf(`
		export TERM=xterm
		screen -r %s <<EOF

EOF
	`, target)
	if _, err := s.runCommandWithPTY(resizeScript, 500, 100); err != nil {
		return err
	}
	return nil
}

func (s *Session) resolveScreenSessionID() string {
	output, err := s.runCommand("screen -list")
	if err != nil {
		trimmed := strings.TrimSpace(output)
		if trimmed != "" {
			log.Printf("[Console] Failed to list screen sessions: %v (output: %s)", err, trimmed)
		} else {
			log.Printf("[Console] Failed to list screen sessions: %v", err)
		}
		return ""
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[0]
		if strings.HasSuffix(id, "."+s.ScreenSession) {
			return id
		}
	}

	return ""
}

func linesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lastIndex(lines []string, target string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == target {
			return i
		}
	}
	return -1
}

// broadcastOutput broadcasts console output to all connected clients
func (s *Session) broadcastOutput(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case line, ok := <-s.outputChan:
			if !ok {
				return
			}

			// Add to buffer
			s.Buffer.Add(line)

			// Update activity
			s.mu.Lock()
			s.lastActivity = time.Now()
			s.mu.Unlock()

			// Broadcast to all clients in room
			s.Hub.BroadcastToRoom(s.Room, &websocket.Message{
				Type: "console_output",
				Payload: map[string]interface{}{
					"line":      line,
					"server_id": s.ServerID,
				},
				Timestamp: time.Now(),
			})

			// Write to log file if enabled
			if s.logWriter != nil {
				s.logWriter.WriteLine(line)
			}
		}
	}
}

// ExecuteCommand sends a command to the screen session
func (s *Session) ExecuteCommand(command string, userID int64, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isActive {
		return fmt.Errorf("session is not active")
	}

	command = strings.TrimSpace(command)
	clean, err := sanitizeConsoleCommand(command)
	if err != nil {
		return err
	}

	// Escape command for screen
	escapedCmd := strings.ReplaceAll(clean, `"`, `\"`)
	
	// Send command to screen session using 'stuff' command
	screenCmd := fmt.Sprintf(`screen -S %s -X stuff "%s\n"`, s.ScreenSession, escapedCmd)
	
	_, err = s.runCommand(screenCmd)
	if err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	// Save to command history
	go s.saveCommandHistory(userID, username, clean, true, nil)

	// Broadcast command to other users in room
	s.Hub.BroadcastToRoom(s.Room, &websocket.Message{
		Type: "command_executed",
		Payload: map[string]interface{}{
			"command":  clean,
			"user_id":  userID,
			"username": username,
		},
		Timestamp: time.Now(),
	})

	log.Printf("[Console] Command executed on %s by %s: %s", s.ServerID, username, clean)
	return nil
}

func (s *Session) runCommand(cmd string) (string, error) {
	if s.RunAsUser == "" || !s.UseSudo {
		return s.SSHConnection.Client.RunCommand(cmd)
	}
	wrapped := fmt.Sprintf("sudo -n -i -u %s bash -lc %s", bashQuote(s.RunAsUser), bashDoubleQuote(cmd))
	return s.SSHConnection.Client.RunCommand(wrapped)
}

func (s *Session) runCommandWithPTY(cmd string, cols, rows int) (string, error) {
	if s.RunAsUser == "" || !s.UseSudo {
		return s.SSHConnection.Client.RunCommandWithPTY(cmd, cols, rows)
	}
	wrapped := fmt.Sprintf("sudo -n -i -u %s bash -lc %s", bashQuote(s.RunAsUser), bashDoubleQuote(cmd))
	return s.SSHConnection.Client.RunCommandWithPTY(wrapped, cols, rows)
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

func sanitizeConsoleLine(line string) string {
	if line == "" {
		return ""
	}
	stripped := ansiEscapePattern.ReplaceAllString(line, "")
	return strings.Map(func(r rune) rune {
		// Keep tabs, remove other control characters
		if r == '\t' {
			return r
		}
		if r == '\n' || r == '\r' {
			return -1
		}
		if r < 32 {
			return -1
		}
		return r
	}, stripped)
}

func sanitizeConsoleCommand(command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is empty")
	}
	if len(command) > 512 {
		return "", fmt.Errorf("command is too long")
	}
	if strings.ContainsAny(command, "\n\r;|&`$()<>") {
		return "", fmt.Errorf("command contains invalid characters")
	}
	if ansiEscapePattern.MatchString(command) {
		return "", fmt.Errorf("command contains escape sequences")
	}
	return command, nil
}

// saveCommandHistory saves command to database
func (s *Session) saveCommandHistory(userID int64, username, command string, success bool, output *string) {
	var outputPreview sql.NullString
	if output != nil {
		preview := *output
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		outputPreview = sql.NullString{String: preview, Valid: true}
	}

	_, err := s.db.Exec(`
		INSERT INTO console_commands (server_id, user_id, command, success, output_preview)
		VALUES (?, ?, ?, ?, ?)
	`, s.ServerID, userID, command, success, outputPreview)

	if err != nil {
		log.Printf("[Console] Failed to save command history: %v", err)
	}
}

// GetHistoricalOutput returns buffered output for new clients
func (s *Session) GetHistoricalOutput(lines int) []string {
	if lines <= 0 {
		lines = 100
	}
	return s.Buffer.GetLast(lines)
}

// IsActive returns whether the session is active
func (s *Session) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isActive
}

// GetActiveViewers returns the number of active viewers
func (s *Session) GetActiveViewers() int {
	return s.Hub.GetRoomSize(s.Room)
}
