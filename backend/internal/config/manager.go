package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ServerManager handles thread-safe access to server configurations
type ServerManager struct {
	configDir string
	mutex     sync.RWMutex
	servers   []ServerDefinition
}

// NewServerManager creates a new server manager
func NewServerManager(configDir string) (*ServerManager, error) {
	sm := &ServerManager{
		configDir: configDir,
		servers:   []ServerDefinition{},
	}
	
	if err := sm.Load(); err != nil {
		return nil, err
	}
	
	return sm, nil
}

// Load reads the configuration from disk
func (sm *ServerManager) Load() error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	servers, err := LoadServers(sm.configDir)
	if err != nil {
		return err
	}
	sm.servers = servers
	return nil
}

// Save writes the current configuration to disk
func (sm *ServerManager) Save() error {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	serversPath := fmt.Sprintf("%s/servers.yaml", sm.configDir)
	
	data := struct {
		Servers []ServerDefinition `yaml:"servers"`
	}{
		Servers: sm.servers,
	}

	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal servers config: %w", err)
	}

	// Log what we're about to save
	fmt.Printf("[ServerManager.Save] Writing %d servers to %s\n", len(sm.servers), serversPath)
	for _, srv := range sm.servers {
		fmt.Printf("  - Server: %s, Dependencies: {InstallDir: %s, ServiceUser: %s, UseSudo: %v}\n",
			srv.ID, srv.Dependencies.InstallDir, srv.Dependencies.ServiceUser, srv.Dependencies.UseSudo)
	}

	if err := os.WriteFile(serversPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write servers config: %w", err)
	}

	fmt.Printf("[ServerManager.Save] Successfully wrote servers config\n")
	return nil
}

// GetAll returns a copy of all server definitions
func (sm *ServerManager) GetAll() []ServerDefinition {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	result := make([]ServerDefinition, len(sm.servers))
	copy(result, sm.servers)
	return result
}

// GetByID returns a server definition by ID
func (sm *ServerManager) GetByID(id string) (ServerDefinition, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	for _, s := range sm.servers {
		if s.ID == id {
			return s, true
		}
	}
	return ServerDefinition{}, false
}

// Add adds a new server definition
func (sm *ServerManager) Add(server ServerDefinition) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	// Check for duplicates
	for _, s := range sm.servers {
		if s.ID == server.ID {
			return fmt.Errorf("server with ID %s already exists", server.ID)
		}
	}

	// Auto-generate ID if empty
	if server.ID == "" {
		server.ID = fmt.Sprintf("server-%d", time.Now().Unix())
	}

	// Validate server definition
	if err := ValidateServerDefinition(&server); err != nil {
		return fmt.Errorf("invalid server definition: %w", err)
	}

	sm.servers = append(sm.servers, server)
	return nil // Call Save() explicitly after adding
}

// Update updates an existing server definition
func (sm *ServerManager) Update(server ServerDefinition) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	// Validate server definition
	if err := ValidateServerDefinition(&server); err != nil {
		return fmt.Errorf("invalid server definition: %w", err)
	}

	for i, s := range sm.servers {
		if s.ID == server.ID {
			sm.servers[i] = server
			return nil // Call Save() explicitly after updating
		}
	}

	return fmt.Errorf("server with ID %s not found", server.ID)
}

// Delete removes a server definition
func (sm *ServerManager) Delete(id string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	for i, s := range sm.servers {
		if s.ID == id {
			// Remove element
			sm.servers = append(sm.servers[:i], sm.servers[i+1:]...)
			return nil // Call Save() explicitly after deleting
		}
	}

	return fmt.Errorf("server with ID %s not found", id)
}

// UnmarshalJSON is a helper to verify JSON correctness
func (sm *ServerManager) UnmarshalJSON(data []byte) error {
    var raw []ServerDefinition
    if err := json.Unmarshal(data, &raw); err != nil {
        return err
    }
    sm.mutex.Lock()
    defer sm.mutex.Unlock()
    sm.servers = raw
    return nil
}
