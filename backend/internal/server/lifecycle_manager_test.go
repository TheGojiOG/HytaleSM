package server

import "testing"

type noopProcessManager struct{}

func (noopProcessManager) Start(serverID, sessionName, command, logFile string) error {
	return nil
}

func (noopProcessManager) Stop(serverID, sessionName string) error {
	return nil
}

func (noopProcessManager) Kill(serverID, sessionName string) error {
	return nil
}

func (noopProcessManager) IsRunning(serverID, sessionName string) (bool, error) {
	return false, nil
}

func (noopProcessManager) SendCommand(serverID, sessionName, command string) error {
	return nil
}

func (noopProcessManager) SendCtrlC(serverID, sessionName string) error {
	return nil
}

func (noopProcessManager) GetPID(serverID, sessionName string) (int, error) {
	return 0, nil
}

func TestBuildJavaCommand(t *testing.T) {
	manager := NewLifecycleManager(nil, noopProcessManager{}, nil, nil)
	cmd := manager.buildJavaCommand(&ServerConfig{
		WorkingDir: "/srv",
		Executable: "java",
		JavaArgs:   []string{"-Xmx1G"},
		ServerArgs: []string{"nogui"},
	})

	if cmd == "" {
		t.Fatalf("expected command to be built")
	}
}
