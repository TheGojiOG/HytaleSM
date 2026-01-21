package handlers

import (
	"github.com/yourusername/hytale-server-manager/internal/server"
)

// MockProcessManager implements server.ProcessManager for testing
type MockProcessManager struct {
	processes map[string]bool // map[serverID]running
}

func NewMockProcessManager() *MockProcessManager {
	return &MockProcessManager{
		processes: make(map[string]bool),
	}
}

func (m *MockProcessManager) Start(serverID, sessionName, command, logFile string) error {
	m.processes[serverID] = true
	return nil
}

func (m *MockProcessManager) Stop(serverID, sessionName string) error {
	m.processes[serverID] = false
	return nil
}

func (m *MockProcessManager) Kill(serverID, sessionName string) error {
	m.processes[serverID] = false
	return nil
}

func (m *MockProcessManager) IsRunning(serverID, sessionName string) (bool, error) {
	return m.processes[serverID], nil
}

func (m *MockProcessManager) SendCommand(serverID, sessionName, command string) error {
	// No-op for mock
	return nil
}

func (m *MockProcessManager) SendCtrlC(serverID, sessionName string) error {
	// No-op for mock
	return nil
}

func (m *MockProcessManager) GetPID(serverID, sessionName string) (int, error) {
	if running := m.processes[serverID]; running {
		return 12345, nil
	}
	return 0, nil
}

// Ensure interface compliance
var _ server.ProcessManager = &MockProcessManager{}
