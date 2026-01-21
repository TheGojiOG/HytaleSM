package config

import (
	"os"
	"testing"
)

func TestServerManager_CRUD(t *testing.T) {
	// 1. Setup temporary directory
	tempDir, err := os.MkdirTemp("", "hytale-config-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// 2. Initialize Manager
	manager, err := NewServerManager(tempDir)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// 3. Test: Add Server
	newServer := ServerDefinition{
		ID:   "test-server-1",
		Name: "Test Server",
		Connection: ConnectionConfig{
			Host:       "localhost",
			Port:       22,
			Username:   "root",
			AuthMethod: "password",
			Password:   "secret",
		},
		Server: GameServerConfig{
			Executable:       "java",
			WorkingDirectory: "/home/hytale",
			ProcessManager:   "screen",
		},
	}

	if err := manager.Add(newServer); err != nil {
		t.Errorf("Failed to add server: %v", err)
	}
	if err := manager.Save(); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	// 4. Test: Get Server
	retrieved, found := manager.GetByID("test-server-1")
	if !found {
		t.Error("Server not found after adding")
	}
	if retrieved.Name != "Test Server" {
		t.Errorf("Expected name 'Test Server', got '%s'", retrieved.Name)
	}

	// Verify persistence (save is called implicitly by Add)
	// Create new manager to read from disk
	manager2, err := NewServerManager(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := manager2.GetByID("test-server-1"); !found {
		t.Error("Server not persisted to disk")
	}

	// 5. Test: Update Server
	newServer.Name = "Updated Name"
	if err := manager.Update(newServer); err != nil {
		t.Errorf("Failed to update server: %v", err)
	}

	updated, _ := manager.GetByID("test-server-1")
	if updated.Name != "Updated Name" {
		t.Error("Update did not persist in memory")
	}

	// 6. Test: Delete Server
	if err := manager.Delete("test-server-1"); err != nil {
		t.Errorf("Failed to delete server: %v", err)
	}

	if _, found := manager.GetByID("test-server-1"); found {
		t.Error("Server still exists after deletion")
	}
}

func TestServerManager_Concurrency(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hytale-conc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	manager, _ := NewServerManager(tempDir)

	// Run concurrent reads and writes
	for i := 0; i < 10; i++ {
		go func(id int) {
			manager.GetAll()
		}(i)
		go func(id int) {
			manager.Add(ServerDefinition{ID: "server-"})
		}(i)
	}
}
