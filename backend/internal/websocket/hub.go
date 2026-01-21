package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message represents a WebSocket message
type Message struct {
	Type      string                 `json:"type"`
	Payload   interface{}            `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Client represents a WebSocket client connection
type Client struct {
	ID       string
	UserID   int64
	Username string
	Conn     *websocket.Conn
	Room     string
	Send     chan *Message
	Hub      *Hub
	mu       sync.Mutex
}

// Hub manages all WebSocket connections and rooms
type Hub struct {
	// Registered clients grouped by room
	rooms map[string]map[*Client]bool

	// Register requests from clients
	Register chan *Client

	// Unregister requests from clients
	Unregister chan *Client

	// Broadcast messages to room
	broadcast chan *BroadcastMessage

	// Active clients by ID for quick lookup
	clients map[string]*Client

	mu sync.RWMutex
}

// BroadcastMessage represents a message to broadcast to a room
type BroadcastMessage struct {
	Room    string
	Message *Message
	Exclude *Client // Optional: exclude this client from broadcast
}

// NewHub creates a new WebSocket hub
func NewHub() *Hub {
	return &Hub{
		rooms:      make(map[string]map[*Client]bool),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		broadcast:  make(chan *BroadcastMessage, 256),
		clients:    make(map[string]*Client),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.Register:
			h.registerClient(client)

		case client := <-h.Unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastToRoom(message)

		case <-ctx.Done():
			log.Println("[WebSocket] Hub shutting down")
			h.shutdown()
			return
		}
	}
}

// registerClient adds a client to a room
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Add to clients map
	h.clients[client.ID] = client

	// Create room if it doesn't exist
	if h.rooms[client.Room] == nil {
		h.rooms[client.Room] = make(map[*Client]bool)
	}

	// Add client to room
	h.rooms[client.Room][client] = true

	log.Printf("[WebSocket] Client %s (user=%s) joined room %s. Room size: %d",
		client.ID, client.Username, client.Room, len(h.rooms[client.Room]))

	// Notify room about new user
	h.broadcast <- &BroadcastMessage{
		Room: client.Room,
		Message: &Message{
			Type: "user_joined",
			Payload: map[string]interface{}{
				"user_id":  client.UserID,
				"username": client.Username,
				"client_id": client.ID,
			},
			Timestamp: time.Now(),
		},
		Exclude: client, // Don't send to the joining user
	}
}

// unregisterClient removes a client from a room
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Remove from clients map
	delete(h.clients, client.ID)

	// Remove from room
	if clients, ok := h.rooms[client.Room]; ok {
		if _, ok := clients[client]; ok {
			delete(clients, client)
			close(client.Send)

			// Remove room if empty
			if len(clients) == 0 {
				delete(h.rooms, client.Room)
				log.Printf("[WebSocket] Room %s is now empty and removed", client.Room)
			} else {
				log.Printf("[WebSocket] Client %s left room %s. Room size: %d",
					client.ID, client.Room, len(clients))

				// Notify room about user leaving
				go func() {
					h.broadcast <- &BroadcastMessage{
						Room: client.Room,
						Message: &Message{
							Type: "user_left",
							Payload: map[string]interface{}{
								"user_id":  client.UserID,
								"username": client.Username,
								"client_id": client.ID,
							},
							Timestamp: time.Now(),
						},
					}
				}()
			}
		}
	}
}

// broadcastToRoom sends a message to all clients in a room
func (h *Hub) broadcastToRoom(bm *BroadcastMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.rooms[bm.Room]; ok {
		for client := range clients {
			// Skip excluded client if specified
			if bm.Exclude != nil && client.ID == bm.Exclude.ID {
				continue
			}

			select {
			case client.Send <- bm.Message:
			default:
				// Client's send channel is full, drop message to avoid disconnecting
				log.Printf("[WebSocket] Client %s send channel full, dropping message", client.ID)
			}
		}
	}
}

// GetRoomClients returns all clients in a room
func (h *Hub) GetRoomClients(room string) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients := []*Client{}
	if roomClients, ok := h.rooms[room]; ok {
		for client := range roomClients {
			clients = append(clients, client)
		}
	}
	return clients
}

// GetRoomSize returns the number of clients in a room
func (h *Hub) GetRoomSize(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.rooms[room]; ok {
		return len(clients)
	}
	return 0
}

// BroadcastToRoom sends a message to all clients in a room
func (h *Hub) BroadcastToRoom(room string, message *Message) {
	h.broadcast <- &BroadcastMessage{
		Room:    room,
		Message: message,
	}
}

// shutdown closes all connections gracefully
func (h *Hub) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, client := range h.clients {
		close(client.Send)
		client.Conn.Close()
	}

	h.rooms = make(map[string]map[*Client]bool)
	h.clients = make(map[string]*Client)
}

// ReadPump pumps messages from WebSocket connection to hub
func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WebSocket] Read error: %v", err)
			}
			break
		}

		// Parse message
		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[WebSocket] Failed to parse message: %v", err)
			continue
		}

		msg.Timestamp = time.Now()
		// Message handling will be done by specific handlers (console, etc.)
		log.Printf("[WebSocket] Received message type=%s from client=%s", msg.Type, c.ID)
	}
}

// WritePump pumps messages from hub to WebSocket connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Hub closed the channel
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}

			// Marshal and write message
			data, err := json.Marshal(message)
			if err != nil {
				log.Printf("[WebSocket] Failed to marshal message: %v", err)
				continue
			}

			w.Write(data)

			// Add queued messages to current websocket message
			n := len(c.Send)
			for i := 0; i < n; i++ {
				msg := <-c.Send
				data, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				w.Write([]byte("\n"))
				w.Write(data)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// SendMessage sends a message to this specific client
func (c *Client) SendMessage(msgType string, payload interface{}) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("client send channel is closed")
		}
	}()

	msg := &Message{
		Type:      msgType,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	select {
	case c.Send <- msg:
		return nil
	default:
		return fmt.Errorf("client send channel is full")
	}
}
