package server

import "testing"

type mockProcessManager struct {
	isRunning bool
	pid       int
}

func (m mockProcessManager) Start(serverID, sessionName, command, logFile string) error {
	return nil
}

func (m mockProcessManager) SetRunAsUser(serverID, runAsUser string, useSudo bool) {
}

func (m mockProcessManager) Stop(serverID, sessionName string) error {
	return nil
}

func (m mockProcessManager) Kill(serverID, sessionName string) error {
	return nil
}

func (m mockProcessManager) IsRunning(serverID, sessionName string) (bool, error) {
	return m.isRunning, nil
}

func (m mockProcessManager) SendCommand(serverID, sessionName, command string) error {
	return nil
}

func (m mockProcessManager) SendCtrlC(serverID, sessionName string) error {
	return nil
}

func (m mockProcessManager) GetPID(serverID, sessionName string) (int, error) {
	return m.pid, nil
}

func TestDetectStatusOffline(t *testing.T) {
	executor := &MockCommandExecutor{}
	processManager := mockProcessManager{isRunning: false}
	detector := NewStatusDetector(executor, processManager, nil)

	status, err := detector.DetectStatus("server-1", "screen-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != StatusOffline {
		t.Fatalf("expected offline status, got %s", status.Status)
	}
}
