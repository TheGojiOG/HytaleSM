package server

// ProcessManager defines the interface for managing game server processes
type ProcessManager interface {
	// Start starts a new process
	Start(serverID, sessionName, command, logFile string) error

	// SetRunAsUser configures which user should own/manage the screen session
	SetRunAsUser(serverID, runAsUser string, useSudo bool)
	
	// Stop gracefully stops the process
	Stop(serverID, sessionName string) error
	
	// Kill forcefully kills the process
	Kill(serverID, sessionName string) error
	
	// IsRunning checks if the process is running
	IsRunning(serverID, sessionName string) (bool, error)
	
	// SendCommand sends a command to the process input
	SendCommand(serverID, sessionName, command string) error
	
	// SendCtrlC sends a Ctrl+C signal to the process
	SendCtrlC(serverID, sessionName string) error
	
	// GetPID returns the process ID
	GetPID(serverID, sessionName string) (int, error)
}
