package server

import "testing"

func TestMockCommandExecutor(t *testing.T) {
	executor := &MockCommandExecutor{MockOutput: "ok"}
	out, err := executor.Execute("server-1", "echo ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("expected output ok, got %s", out)
	}
}
