package ssh

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

// ConnectionPool manages SSH connections to multiple servers
type ConnectionPool struct {
	connections map[string]*PooledConnection
	mu          sync.RWMutex
	db          *sql.DB
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

// PooledConnection wraps an SSH client with pool metadata
type PooledConnection struct {
	Client            *Client
	ServerID          string
	HealthStatus      string
	ReconnectAttempts int
	LastHealthCheck   time.Time
	mu                sync.Mutex
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(db *sql.DB) *ConnectionPool {
	pool := &ConnectionPool{
		connections: make(map[string]*PooledConnection),
		db:          db,
		stopChan:    make(chan struct{}),
	}

	// Start health check routine
	pool.wg.Add(1)
	go pool.healthCheckLoop()

	return pool
}

// GetConnection gets or creates a connection for a server
func (p *ConnectionPool) GetConnection(serverID string, config *ClientConfig) (*PooledConnection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if connection exists
	if conn, exists := p.connections[serverID]; exists {
		// Check if connection is still alive
		if conn.Client.IsConnected() {
			conn.updateActivity()
			return conn, nil
		}

		// Connection is dead, remove it
		log.Printf("[Pool] Connection to %s is dead, removing", serverID)
		delete(p.connections, serverID)
	}

	// Create new connection
	conn, err := p.createConnection(serverID, config)
	if err != nil {
		return nil, err
	}

	p.connections[serverID] = conn
	p.recordConnection(serverID, true)

	return conn, nil
}

// createConnection creates a new pooled connection
func (p *ConnectionPool) createConnection(serverID string, config *ClientConfig) (*PooledConnection, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	conn := &PooledConnection{
		Client:          client,
		ServerID:        serverID,
		HealthStatus:    "healthy",
		LastHealthCheck: time.Now(),
	}

	log.Printf("[Pool] Created new connection to %s", serverID)
	return conn, nil
}

// RemoveConnection removes a connection from the pool
func (p *ConnectionPool) RemoveConnection(serverID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if conn, exists := p.connections[serverID]; exists {
		conn.Client.Close()
		delete(p.connections, serverID)
		p.recordConnection(serverID, false)
		log.Printf("[Pool] Removed connection to %s", serverID)
	}

	return nil
}

// GetExistingConnection retrieves an existing connection without creating a new one
func (p *ConnectionPool) GetExistingConnection(serverID string) *PooledConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.connections[serverID]
}

// CloseAll closes all connections
func (p *ConnectionPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for serverID, conn := range p.connections {
		conn.Client.Close()
		p.recordConnection(serverID, false)
		log.Printf("[Pool] Closed connection to %s", serverID)
	}

	p.connections = make(map[string]*PooledConnection)
}

// GetConnectionCount returns the number of active connections
func (p *ConnectionPool) GetConnectionCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.connections)
}

// healthCheckLoop periodically checks connection health
func (p *ConnectionPool) healthCheckLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.performHealthChecks()
		case <-p.stopChan:
			return
		}
	}
}

// performHealthChecks checks all connections
func (p *ConnectionPool) performHealthChecks() {
	p.mu.RLock()
	serverIDs := make([]string, 0, len(p.connections))
	for serverID := range p.connections {
		serverIDs = append(serverIDs, serverID)
	}
	p.mu.RUnlock()

	for _, serverID := range serverIDs {
		p.mu.RLock()
		conn, exists := p.connections[serverID]
		p.mu.RUnlock()

		if !exists {
			continue
		}

		conn.performHealthCheck(p)
	}
}

// performHealthCheck checks the health of a single connection
func (pc *PooledConnection) performHealthCheck(pool *ConnectionPool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if !pc.Client.IsConnected() {
		log.Printf("[Pool] Health check failed for %s, attempting reconnect", pc.ServerID)
		
		pc.HealthStatus = "failed"
		pc.ReconnectAttempts++

		// Try to reconnect if not exceeded max attempts
		if pc.ReconnectAttempts <= 3 {
			if err := pc.Client.Connect(); err != nil {
				log.Printf("[Pool] Reconnect attempt %d failed for %s: %v", 
					pc.ReconnectAttempts, pc.ServerID, err)
				
				// If max attempts reached, remove from pool
				if pc.ReconnectAttempts >= 3 {
					log.Printf("[Pool] Max reconnect attempts reached for %s, removing from pool", pc.ServerID)
					pool.RemoveConnection(pc.ServerID)
				}
			} else {
				log.Printf("[Pool] Reconnected to %s successfully", pc.ServerID)
				pc.HealthStatus = "healthy"
				pc.ReconnectAttempts = 0
			}
		}
	} else {
		// Connection is healthy
		if pc.HealthStatus != "healthy" {
			log.Printf("[Pool] Connection to %s recovered", pc.ServerID)
			pc.HealthStatus = "healthy"
			pc.ReconnectAttempts = 0
		}
	}

	pc.LastHealthCheck = time.Now()
	pool.updateConnectionHealth(pc.ServerID, pc.HealthStatus, pc.ReconnectAttempts)
}

// updateActivity updates the last activity time
func (pc *PooledConnection) updateActivity() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	// Activity is tracked in the Client itself
}

// GetHealthStatus returns the current health status
func (pc *PooledConnection) GetHealthStatus() string {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.HealthStatus
}

// recordConnection records connection status in database
func (p *ConnectionPool) recordConnection(serverID string, connected bool) {
	if connected {
		_, err := p.db.Exec(`
			INSERT INTO ssh_connections (server_id, connected_at, last_activity, health_status, is_active)
			VALUES (?, datetime('now'), datetime('now'), 'healthy', 1)
		`, serverID)
		if err != nil {
			log.Printf("[Pool] Failed to record connection for %s: %v", serverID, err)
		}
	} else {
		_, err := p.db.Exec(`
			UPDATE ssh_connections 
			SET is_active = 0 
			WHERE server_id = ? AND is_active = 1
		`, serverID)
		if err != nil {
			log.Printf("[Pool] Failed to update connection status for %s: %v", serverID, err)
		}
	}
}

// updateConnectionHealth updates connection health in database
func (p *ConnectionPool) updateConnectionHealth(serverID, healthStatus string, reconnectAttempts int) {
	_, err := p.db.Exec(`
		UPDATE ssh_connections 
		SET health_status = ?, 
		    reconnect_attempts = ?,
		    last_activity = datetime('now')
		WHERE server_id = ? AND is_active = 1
	`, healthStatus, reconnectAttempts, serverID)
	
	if err != nil {
		log.Printf("[Pool] Failed to update health for %s: %v", serverID, err)
	}
}

// Stop stops the health check loop
func (p *ConnectionPool) Stop() {
	select {
	case <-p.stopChan:
		// Already closed
		return
	default:
		close(p.stopChan)
	}
	p.wg.Wait()
	p.CloseAll()
}

// GetStats returns pool statistics
func (p *ConnectionPool) GetStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	healthy := 0
	degraded := 0
	failed := 0

	for _, conn := range p.connections {
		status := conn.GetHealthStatus()
		switch status {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		case "failed":
			failed++
		}
	}

	return map[string]interface{}{
		"total_connections": len(p.connections),
		"healthy":           healthy,
		"degraded":          degraded,
		"failed":            failed,
	}
}
