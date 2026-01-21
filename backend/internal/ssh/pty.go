package ssh

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// PTYManager manages pseudo-terminal sessions for console access
type PTYManager struct {
	sessions map[string]*PTYSession
	pool     *ConnectionPool
	mu       sync.RWMutex
}

// PTYSession represents an active PTY session attached to a screen session
type PTYSession struct {
	ServerID     string
	SessionName  string
	Session      *ssh.Session
	Stdin        io.WriteCloser
	StdoutPipe   io.Reader
	StderrPipe   io.Reader
	Buffer       *ConsoleBuffer
	Subscribers  map[chan string]bool
	isAttached   bool
	stopChan     chan struct{}
	mu           sync.RWMutex
	lastActivity time.Time
}

// ConsoleBuffer maintains a circular buffer of console output
type ConsoleBuffer struct {
	Lines    []ConsoleLine
	MaxLines int
	mu       sync.RWMutex
}

// ConsoleLine represents a single line of console output
type ConsoleLine struct {
	Timestamp time.Time
	Content   string
	Type      string // "stdout", "stderr", "command"
}

const (
	// DefaultBufferSize is the default number of lines to keep in memory
	DefaultBufferSize = 10000

	// ConsoleLineTypeStdout indicates normal output
	ConsoleLineTypeStdout = "stdout"

	// ConsoleLineTypeStderr indicates error output
	ConsoleLineTypeStderr = "stderr"

	// ConsoleLineTypeCommand indicates a command sent to the console
	ConsoleLineTypeCommand = "command"
)

// NewPTYManager creates a new PTY manager
func NewPTYManager(pool *ConnectionPool) *PTYManager {
	return &PTYManager{
		sessions: make(map[string]*PTYSession),
		pool:     pool,
	}
}

// AttachToScreen attaches to an existing screen session
func (pm *PTYManager) AttachToScreen(serverID, sessionName string) (*PTYSession, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already attached
	if session, exists := pm.sessions[serverID]; exists && session.isAttached {
		log.Printf("[PTY] Already attached to server %s, returning existing session", serverID)
		return session, nil
	}

	// Get SSH connection from pool
	pooledConn := pm.pool.GetExistingConnection(serverID)
	if pooledConn == nil {
		return nil, fmt.Errorf("no SSH connection available for server %s (connection must be established first)", serverID)
	}

	// Create new SSH session
	sshSession, err := pooledConn.Client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH session: %w", err)
	}

	// Request PTY with large dimensions for proper console display
	// Use plain xterm to avoid termcap resize warnings
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	if err := sshSession.RequestPty("xterm", 100, 500, modes); err != nil {
		sshSession.Close()
		return nil, fmt.Errorf("failed to request PTY: %w", err)
	}

	// Get stdin pipe
	stdin, err := sshSession.StdinPipe()
	if err != nil {
		sshSession.Close()
		return nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	// Get stdout pipe
	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		sshSession.Close()
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Get stderr pipe
	stderr, err := sshSession.StderrPipe()
	if err != nil {
		sshSession.Close()
		return nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Attach to screen session using -r (reattach)
	// This will adapt to the PTY dimensions we specify
	attachCmd := fmt.Sprintf("screen -r %s", sessionName)
	if err := sshSession.Start(attachCmd); err != nil {
		sshSession.Close()
		return nil, fmt.Errorf("failed to attach to screen session: %w", err)
	}

	// Send window size change signal to ensure screen recognizes the terminal dimensions
	time.Sleep(100 * time.Millisecond)
	if err := sshSession.WindowChange(100, 500); err != nil {
		log.Printf("[PTY] Warning: Failed to send window change signal: %v", err)
	}

	// Create PTY session
	ptySession := &PTYSession{
		ServerID:     serverID,
		SessionName:  sessionName,
		Session:      sshSession,
		Stdin:        stdin,
		StdoutPipe:   stdout,
		StderrPipe:   stderr,
		Buffer:       NewConsoleBuffer(DefaultBufferSize),
		Subscribers:  make(map[chan string]bool),
		isAttached:   true,
		stopChan:     make(chan struct{}),
		lastActivity: time.Now(),
	}

	// Store session
	pm.sessions[serverID] = ptySession

	// Start output readers
	go ptySession.readOutput(ConsoleLineTypeStdout, stdout)
	go ptySession.readOutput(ConsoleLineTypeStderr, stderr)

	log.Printf("[PTY] Successfully attached to screen session %s for server %s", sessionName, serverID)

	return ptySession, nil
}

// SendCommand sends a command to the console
func (pm *PTYManager) SendCommand(serverID, command string) error {
	pm.mu.RLock()
	session, exists := pm.sessions[serverID]
	pm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no PTY session found for server %s", serverID)
	}

	if !session.isAttached {
		return fmt.Errorf("PTY session for server %s is not attached", serverID)
	}

	return session.SendCommand(command)
}

// SendCommand sends a command through the PTY session
func (ps *PTYSession) SendCommand(command string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if !ps.isAttached {
		return fmt.Errorf("PTY session is not attached")
	}

	// Log the command in the buffer
	ps.Buffer.AddLine(ConsoleLine{
		Timestamp: time.Now(),
		Content:   command,
		Type:      ConsoleLineTypeCommand,
	})

	// Send command with newline
	_, err := ps.Stdin.Write([]byte(command + "\n"))
	if err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	ps.lastActivity = time.Now()

	log.Printf("[PTY] Sent command to server %s: %s", ps.ServerID, command)

	return nil
}

// GetBufferedOutput retrieves the last N lines from the buffer
func (pm *PTYManager) GetBufferedOutput(serverID string, lines int) []ConsoleLine {
	pm.mu.RLock()
	session, exists := pm.sessions[serverID]
	pm.mu.RUnlock()

	if !exists {
		return []ConsoleLine{}
	}

	return session.Buffer.GetLastLines(lines)
}

// SubscribeToOutput creates a channel to receive console output in real-time
func (pm *PTYManager) SubscribeToOutput(serverID string) (chan string, error) {
	pm.mu.RLock()
	session, exists := pm.sessions[serverID]
	pm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no PTY session found for server %s", serverID)
	}

	subscriber := make(chan string, 100) // Buffered to prevent blocking

	session.mu.Lock()
	session.Subscribers[subscriber] = true
	session.mu.Unlock()

	log.Printf("[PTY] New subscriber for server %s (total: %d)", serverID, len(session.Subscribers))

	return subscriber, nil
}

// UnsubscribeFromOutput removes a subscriber
func (pm *PTYManager) UnsubscribeFromOutput(serverID string, subscriber chan string) {
	pm.mu.RLock()
	session, exists := pm.sessions[serverID]
	pm.mu.RUnlock()

	if !exists {
		return
	}

	session.mu.Lock()
	if _, ok := session.Subscribers[subscriber]; ok {
		delete(session.Subscribers, subscriber)
		close(subscriber)
		log.Printf("[PTY] Removed subscriber for server %s (remaining: %d)", serverID, len(session.Subscribers))
	}
	session.mu.Unlock()
}

// Detach detaches from the screen session without terminating it
func (pm *PTYManager) Detach(serverID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	session, exists := pm.sessions[serverID]
	if !exists {
		return fmt.Errorf("no PTY session found for server %s", serverID)
	}

	return session.Detach()
}

// Detach detaches from the screen session
func (ps *PTYSession) Detach() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if !ps.isAttached {
		return nil // Already detached
	}

	// Send Ctrl+A, D to detach from screen
	// Ctrl+A = 0x01, D = 0x44
	detachSequence := []byte{0x01, 0x44}
	_, err := ps.Stdin.Write(detachSequence)
	if err != nil {
		log.Printf("[PTY] Failed to send detach sequence: %v", err)
		// Continue with cleanup anyway
	}

	// Give screen time to process the detach
	time.Sleep(500 * time.Millisecond)

	// Close session
	if ps.Session != nil {
		ps.Session.Close()
	}

	// Close all subscribers
	for subscriber := range ps.Subscribers {
		close(subscriber)
	}
	ps.Subscribers = make(map[chan string]bool)

	ps.isAttached = false
	close(ps.stopChan)

	log.Printf("[PTY] Detached from screen session %s for server %s", ps.SessionName, ps.ServerID)

	return nil
}

// DetachAll detaches from all active sessions
func (pm *PTYManager) DetachAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for serverID, session := range pm.sessions {
		if err := session.Detach(); err != nil {
			log.Printf("[PTY] Error detaching from server %s: %v", serverID, err)
		}
	}

	pm.sessions = make(map[string]*PTYSession)
}

// GetSession retrieves a PTY session if it exists
func (pm *PTYManager) GetSession(serverID string) (*PTYSession, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	session, exists := pm.sessions[serverID]
	return session, exists
}

// IsAttached checks if a server has an attached PTY session
func (pm *PTYManager) IsAttached(serverID string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	session, exists := pm.sessions[serverID]
	if !exists {
		return false
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	return session.isAttached
}

// readOutput reads from an output pipe and broadcasts to subscribers
func (ps *PTYSession) readOutput(outputType string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 64KB initial, 1MB max

	for {
		select {
		case <-ps.stopChan:
			return
		default:
			if scanner.Scan() {
				line := scanner.Text()
				timestamp := time.Now()

				// Add to buffer
				consoleLine := ConsoleLine{
					Timestamp: timestamp,
					Content:   line,
					Type:      outputType,
				}
				ps.Buffer.AddLine(consoleLine)

				// Broadcast to subscribers
				ps.mu.RLock()
				for subscriber := range ps.Subscribers {
					select {
					case subscriber <- line:
					default:
						// Subscriber channel full, skip
						log.Printf("[PTY] Subscriber channel full for server %s, dropping line", ps.ServerID)
					}
				}
				ps.mu.RUnlock()

				ps.mu.Lock()
				ps.lastActivity = timestamp
				ps.mu.Unlock()
			} else {
				// Check for errors
				if err := scanner.Err(); err != nil {
					log.Printf("[PTY] Error reading %s from server %s: %v", outputType, ps.ServerID, err)
				}
				// Exit loop on EOF or error
				return
			}
		}
	}
}

// GetLastActivity returns the timestamp of the last activity
func (ps *PTYSession) GetLastActivity() time.Time {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.lastActivity
}

// IsActive checks if the session is still active
func (ps *PTYSession) IsActive() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.isAttached
}

// NewConsoleBuffer creates a new console buffer
func NewConsoleBuffer(maxLines int) *ConsoleBuffer {
	return &ConsoleBuffer{
		Lines:    make([]ConsoleLine, 0, maxLines),
		MaxLines: maxLines,
	}
}

// AddLine adds a line to the buffer (circular buffer)
func (cb *ConsoleBuffer) AddLine(line ConsoleLine) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// If buffer is full, remove oldest line
	if len(cb.Lines) >= cb.MaxLines {
		cb.Lines = cb.Lines[1:]
	}

	cb.Lines = append(cb.Lines, line)
}

// GetLastLines retrieves the last N lines from the buffer
func (cb *ConsoleBuffer) GetLastLines(n int) []ConsoleLine {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if n <= 0 {
		return []ConsoleLine{}
	}

	totalLines := len(cb.Lines)
	if n > totalLines {
		n = totalLines
	}

	// Return a copy of the slice
	result := make([]ConsoleLine, n)
	copy(result, cb.Lines[totalLines-n:])

	return result
}

// GetLinesSince retrieves all lines since a given timestamp
func (cb *ConsoleBuffer) GetLinesSince(since time.Time) []ConsoleLine {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	result := make([]ConsoleLine, 0)

	for _, line := range cb.Lines {
		if line.Timestamp.After(since) {
			result = append(result, line)
		}
	}

	return result
}

// GetAllLines retrieves all lines in the buffer
func (cb *ConsoleBuffer) GetAllLines() []ConsoleLine {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	result := make([]ConsoleLine, len(cb.Lines))
	copy(result, cb.Lines)

	return result
}

// Clear removes all lines from the buffer
func (cb *ConsoleBuffer) Clear() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.Lines = make([]ConsoleLine, 0, cb.MaxLines)
}

// GetLineCount returns the current number of lines in the buffer
func (cb *ConsoleBuffer) GetLineCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return len(cb.Lines)
}
