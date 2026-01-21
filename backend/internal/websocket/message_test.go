package websocket

import "testing"

func TestMessagePayload(t *testing.T) {
	msg := &Message{Type: "test", Payload: map[string]interface{}{"key": "value"}}
	if msg.Type != "test" {
		t.Fatalf("expected message type to be set")
	}
}
