package websocket

import (
	"testing"
)

func TestHubRegisterAndUnregister(t *testing.T) {
	hub := NewHub()
	client := &Client{
		ID:       "client-1",
		UserID:   1,
		Username: "tester",
		Room:     "room-1",
		Send:     make(chan *Message, 1),
		Hub:      hub,
	}

	hub.registerClient(client)
	if hub.GetRoomSize("room-1") != 1 {
		t.Fatalf("expected room size 1")
	}

	hub.unregisterClient(client)
	if hub.GetRoomSize("room-1") != 0 {
		t.Fatalf("expected room to be empty")
	}
}

func TestHubBroadcastToRoom(t *testing.T) {
	hub := NewHub()
	client := &Client{
		ID:       "client-1",
		UserID:   1,
		Username: "tester",
		Room:     "room-1",
		Send:     make(chan *Message, 1),
		Hub:      hub,
	}

	hub.registerClient(client)

	message := &Message{Type: "ping"}
	hub.broadcastToRoom(&BroadcastMessage{Room: "room-1", Message: message})

	select {
	case received := <-client.Send:
		if received.Type != "ping" {
			t.Fatalf("expected ping message")
		}
	default:
		t.Fatalf("expected message to be delivered")
	}
}
