package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestNewHostKeyCallbackTrustOnFirstUse(t *testing.T) {
	tempDir := t.TempDir()
	knownHostsPath := filepath.Join(tempDir, "known_hosts")

	callback, err := NewHostKeyCallback(knownHostsPath, true)
	if err != nil {
		t.Fatalf("failed to create callback: %v", err)
	}

	key1 := generateTestPublicKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}

	if err := callback("example.com:22", addr, key1); err != nil {
		t.Fatalf("expected first key to be accepted, got %v", err)
	}

	if _, err := os.Stat(knownHostsPath); err != nil {
		t.Fatalf("expected known_hosts file to be created: %v", err)
	}

	callback, err = NewHostKeyCallback(knownHostsPath, true)
	if err != nil {
		t.Fatalf("failed to recreate callback: %v", err)
	}

	key2 := generateTestPublicKey(t)
	if err := callback("example.com:22", addr, key2); err == nil {
		t.Fatalf("expected host key change to be rejected")
	}
}

func TestNewHostKeyCallbackRejectsUnknownWhenDisabled(t *testing.T) {
	tempDir := t.TempDir()
	knownHostsPath := filepath.Join(tempDir, "known_hosts")

	callback, err := NewHostKeyCallback(knownHostsPath, false)
	if err != nil {
		t.Fatalf("failed to create callback: %v", err)
	}

	key := generateTestPublicKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}

	if err := callback("example.com:2222", addr, key); err == nil {
		t.Fatalf("expected unknown host key to be rejected")
	}
}

func generateTestPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	pubKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create public key: %v", err)
	}

	return pubKey
}
