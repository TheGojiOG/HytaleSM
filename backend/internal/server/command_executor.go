package server

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

// CommandExecutor abstracts command execution (local or remote)
type CommandExecutor interface {
	Execute(serverID, command string) (string, error)
}

// DefaultCommandExecutor handles both SSH and local execution
type DefaultCommandExecutor struct {
	sshPool *ssh.ConnectionPool
}

func NewDefaultCommandExecutor(pool *ssh.ConnectionPool) *DefaultCommandExecutor {
	return &DefaultCommandExecutor{sshPool: pool}
}

func (e *DefaultCommandExecutor) Execute(serverID, command string) (string, error) {
	// Try SSH first
	if conn := e.sshPool.GetExistingConnection(serverID); conn != nil {
		return conn.Client.RunCommand(command)
	}

	// Fallback to local execution
	// Note: This assumes the server is running locally if no SSH connection exists
	
	// On Windows, some commands like 'ps' won't work, but for production (Linux) this is fine.
	// For dev/test on Windows, we might need special handling or just fail.
	if runtime.GOOS == "windows" {
		// Minimal simulation for Windows dev?
		// Or just try running it (e.g. if using WSL or Git Bash tools are in PATH)
		// Usually we wrap in "bash -c"
		return runLocalCommand("bash", "-c", command)
	}
	
	return runLocalCommand("bash", "-c", command)
}

func runLocalCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command failed: %s (stderr: %s)", err, stderr.String())
	}
	
	return strings.TrimSpace(stdout.String()), nil
}

// MockCommandExecutor for testing
type MockCommandExecutor struct {
	MockOutput string
	MockError  error
	Handlers   map[string]func(command string) (string, error)
}

func (m *MockCommandExecutor) Execute(serverID, command string) (string, error) {
	if m.Handlers != nil {
		for prefix, handler := range m.Handlers {
			if strings.HasPrefix(command, prefix) {
				return handler(command)
			}
		}
	}
	return m.MockOutput, m.MockError
}
