package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/TheGojiOG/HytaleSM/internal/auth"
	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/console"
	"github.com/TheGojiOG/HytaleSM/internal/permissions"
	"github.com/TheGojiOG/HytaleSM/internal/server"
	"github.com/TheGojiOG/HytaleSM/internal/ssh"
	ws "github.com/TheGojiOG/HytaleSM/internal/websocket"
)

// ConsoleHandler handles console-related HTTP and WebSocket requests
type ConsoleHandler struct {
	db             *sql.DB
	config         *config.Config
	hub            *ws.Hub
	sessionManager *console.SessionManager
	sshPool        *ssh.ConnectionPool
	rbacManager    *auth.RBACManager
	commandHistory *console.CommandHistory
}

// NewConsoleHandler creates a new console handler
func NewConsoleHandler(
	cfg *config.Config,
	db *sql.DB,
	hub *ws.Hub,
	sessionManager *console.SessionManager,
	sshPool *ssh.ConnectionPool,
	rbacManager *auth.RBACManager,
) *ConsoleHandler {
	return &ConsoleHandler{
		db:             db,
		config:         cfg,
		hub:            hub,
		sessionManager: sessionManager,
		sshPool:        sshPool,
		rbacManager:    rbacManager,
		commandHistory: console.NewCommandHistory(db),
	}
}

// HandleConsoleWebSocket handles WebSocket connections for console streaming
// WS /ws/console/:serverId
func (h *ConsoleHandler) HandleConsoleWebSocket(c *gin.Context) {
	serverID := c.Param("id")

	// Get user from context (set by JWT middleware)
	userClaims, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	claims := userClaims.(*auth.Claims)

	// Check permission to view console
	hasPermission, err := h.rbacManager.HasServerPermission(claims.UserID, serverID, permissions.ServersConsoleView)
	if err != nil || !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "No permission to view console"})
		return
	}

	// Get server definition
	servers, err := config.LoadServers(h.config.Storage.ConfigDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load servers"})
		return
	}

	var serverDef *config.ServerDefinition
	for _, s := range servers {
		if s.ID == serverID {
			serverDef = &s
			break
		}
	}

	if serverDef == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	// Get or create console session
	session, err := h.sessionManager.GetSession(serverID)
	if err != nil {
		// Create new session
		// First, ensure SSH connection exists
		sshConfig := &ssh.ClientConfig{
			Host:            serverDef.Connection.Host,
			Port:            serverDef.Connection.Port,
			Username:        serverDef.Connection.Username,
			AuthMethod:      serverDef.Connection.AuthMethod,
			KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
			TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
		}

		if serverDef.Connection.AuthMethod == "key" {
			sshConfig.KeyPath = serverDef.Connection.KeyPath
		} else {
			sshConfig.Password = serverDef.Connection.Password
		}

		sshConn, err := h.sshPool.GetConnection(serverID, sshConfig)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to server", "details": err.Error()})
			return
		}

		// Create console session
		sessionName := server.SafeSessionName(serverID)
		runAsUser := strings.TrimSpace(serverDef.Dependencies.ServiceUser)
		useSudo := serverDef.Dependencies.UseSudo
		if runAsUser == "" || runAsUser == sshConfig.Username {
			useSudo = false
		}
		session, err = h.sessionManager.StartSession(serverID, sessionName, sshConn, runAsUser, useSudo)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start console session", "details": err.Error()})
			return
		}
	}

	// Upgrade to WebSocket
	upgrader := buildUpgrader(h.config.Security.CORS.AllowedOrigins)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[Console] Failed to upgrade WebSocket: %v (origin=%s, server=%s)", err, c.Request.Header.Get("Origin"), serverID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "WebSocket upgrade failed", "details": err.Error()})
		return
	}

	// Create WebSocket client
	client := &ws.Client{
		ID:       uuid.New().String(),
		UserID:   claims.UserID,
		Username: claims.Username,
		Conn:     conn,
		Room:     session.Room,
		Send:     make(chan *ws.Message, 1024),
		Hub:      h.hub,
	}

	// Register client
	h.hub.Register <- client

	// Record session in database
	h.recordConsoleSession(client.ID, serverID, claims.UserID, c.ClientIP(), c.Request.UserAgent())

	// Send historical output to client
	go func() {
		historical := session.GetHistoricalOutput(100)
		for _, line := range historical {
			client.SendMessage("console_output", map[string]interface{}{
				"line":       line,
				"server_id":  serverID,
				"historical": true,
			})
		}

		// Send session info
		client.SendMessage("session_info", map[string]interface{}{
			"server_id":      serverID,
			"session_id":     session.ID,
			"active_viewers": session.GetActiveViewers(),
			"can_execute":    h.canExecuteCommands(claims.UserID, serverID),
		})
	}()

	// Start client message pumps
	go client.WritePump()
	go h.handleClientMessages(client, session, claims)
}

// handleClientMessages handles incoming messages from WebSocket client
func (h *ConsoleHandler) handleClientMessages(client *ws.Client, session *console.Session, claims *auth.Claims) {
	defer func() {
		h.hub.Unregister <- client
		client.Conn.Close()
		h.updateSessionDisconnected(client.ID)
	}()

	client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		var msg ws.Message
		err := client.Conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[Console] WebSocket error: %v", err)
			}
			break
		}

		// Handle message based on type
		switch msg.Type {
		case "execute_command":
			h.handleExecuteCommand(client, session, claims, msg)

		case "request_history":
			h.handleRequestHistory(client, session, msg)

		case "set_filter":
			h.handleSetFilter(client, msg)

		case "typing":
			// Broadcast typing indicator to other users
			h.hub.BroadcastToRoom(session.Room, &ws.Message{
				Type: "user_typing",
				Payload: map[string]interface{}{
					"user_id":  claims.UserID,
					"username": claims.Username,
					"typing":   msg.Payload,
				},
				Timestamp: time.Now(),
			})

		default:
			log.Printf("[Console] Unknown message type: %s", msg.Type)
		}
	}
}

// handleExecuteCommand handles command execution requests
func (h *ConsoleHandler) handleExecuteCommand(client *ws.Client, session *console.Session, claims *auth.Claims, msg ws.Message) {
	// Check permission
	if !h.canExecuteCommands(claims.UserID, session.ServerID) {
		client.SendMessage("error", map[string]interface{}{
			"message": "No permission to execute commands",
		})
		return
	}

	// Extract command from payload
	payload, ok := msg.Payload.(map[string]interface{})
	if !ok {
		client.SendMessage("error", map[string]interface{}{
			"message": "Invalid payload",
		})
		return
	}

	command, ok := payload["command"].(string)
	if !ok || command == "" {
		client.SendMessage("error", map[string]interface{}{
			"message": "No command provided",
		})
		return
	}

	// Execute command
	err := session.ExecuteCommand(command, claims.UserID, claims.Username)
	if err != nil {
		client.SendMessage("error", map[string]interface{}{
			"message": fmt.Sprintf("Failed to execute command: %v", err),
		})
		return
	}

	client.SendMessage("command_sent", map[string]interface{}{
		"command": command,
	})
}

// handleRequestHistory handles history requests
func (h *ConsoleHandler) handleRequestHistory(client *ws.Client, session *console.Session, msg ws.Message) {
	payload, ok := msg.Payload.(map[string]interface{})
	if !ok {
		return
	}

	lines := 100
	if l, ok := payload["lines"].(float64); ok {
		lines = int(l)
	}

	historical := session.GetHistoricalOutput(lines)
	client.SendMessage("historical_output", map[string]interface{}{
		"lines": historical,
	})
}

// handleSetFilter handles filter setting requests
func (h *ConsoleHandler) handleSetFilter(client *ws.Client, msg ws.Message) {
	// Filter logic will be implemented on client side for now
	// Server-side filtering can be added if needed
	client.SendMessage("filter_set", map[string]interface{}{
		"status": "ok",
	})
}

// GetCommandHistory returns command history for a server
// GET /api/v1/servers/:id/console/history
func (h *ConsoleHandler) GetCommandHistory(c *gin.Context) {
	serverID := c.Param("id")
	userClaims := c.MustGet("user").(*auth.Claims)

	// Check permission
	hasPermission, err := h.rbacManager.HasServerPermission(userClaims.UserID, serverID, permissions.ServersConsoleView)
	if err != nil || !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "No permission to view console"})
		return
	}

	// Get limit from query
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// Get history
	commands, err := h.commandHistory.GetRecentCommands(serverID, limit)
	if err != nil {
		log.Printf("[Console] Failed to get command history: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get command history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"commands": commands,
		"count":    len(commands),
	})
}

// SearchCommandHistory searches command history
// GET /api/v1/servers/:id/console/history/search?q=keyword
func (h *ConsoleHandler) SearchCommandHistory(c *gin.Context) {
	serverID := c.Param("id")
	query := c.Query("q")
	userClaims := c.MustGet("user").(*auth.Claims)

	// Check permission
	hasPermission, err := h.rbacManager.HasServerPermission(userClaims.UserID, serverID, permissions.ServersConsoleView)
	if err != nil || !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "No permission to view console"})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	commands, err := h.commandHistory.SearchCommands(serverID, query, limit)
	if err != nil {
		log.Printf("[Console] Failed to search command history: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search command history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"commands": commands,
		"count":    len(commands),
		"query":    query,
	})
}

// GetAutocomplete returns command autocomplete suggestions
// GET /api/v1/servers/:id/console/autocomplete?prefix=say
func (h *ConsoleHandler) GetAutocomplete(c *gin.Context) {
	serverID := c.Param("id")
	prefix := c.Query("prefix")
	userClaims := c.MustGet("user").(*auth.Claims)

	// Check permission
	hasPermission, err := h.rbacManager.HasServerPermission(userClaims.UserID, serverID, permissions.ServersConsoleView)
	if err != nil || !hasPermission {
		c.JSON(http.StatusForbidden, gin.H{"error": "No permission to view console"})
		return
	}

	suggestions, err := h.commandHistory.GetAutocomplete(serverID, prefix, 10)
	if err != nil {
		log.Printf("[Console] Failed to get autocomplete: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get autocomplete"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"suggestions": suggestions,
	})
}

// recordConsoleSession records a console session in the database
func (h *ConsoleHandler) recordConsoleSession(sessionID, serverID string, userID int64, ip, userAgent string) {
	_, err := h.db.Exec(`
		INSERT INTO console_sessions (id, server_id, user_id, ip_address, user_agent, is_active)
		VALUES (?, ?, ?, ?, ?, 1)
	`, sessionID, serverID, userID, ip, userAgent)

	if err != nil {
		log.Printf("[Console] Failed to record session: %v", err)
	}
}

// updateSessionDisconnected marks a session as disconnected
func (h *ConsoleHandler) updateSessionDisconnected(sessionID string) {
	_, err := h.db.Exec(`
		UPDATE console_sessions 
		SET is_active = 0, disconnected_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, sessionID)

	if err != nil {
		log.Printf("[Console] Failed to update session: %v", err)
	}
}

// canExecuteCommands checks if user can execute console commands
func (h *ConsoleHandler) canExecuteCommands(userID int64, serverID string) bool {
	hasPermission, err := h.rbacManager.HasServerPermission(userID, serverID, permissions.ServersConsoleExecute)
	return err == nil && hasPermission
}

func buildUpgrader(allowedOrigins []string) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			return isOriginAllowed(origin, allowedOrigins)
		},
	}
}

func isOriginAllowed(origin string, allowedOrigins []string) bool {
	if origin == "" {
		return true
	}

	for _, allowedOrigin := range allowedOrigins {
		normalized := strings.TrimSpace(allowedOrigin)
		if normalized == "" {
			continue
		}
		if normalized == "*" || normalized == "0.0.0.0/0" || normalized == origin {
			return true
		}
	}

	return false
}
