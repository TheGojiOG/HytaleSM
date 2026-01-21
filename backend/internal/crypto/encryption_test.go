package crypto

import (
	"encoding/base64"
	"os"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.Setenv("ENCRYPTION_KEY", encoded); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	defer os.Unsetenv("ENCRYPTION_KEY")

	manager, err := NewEncryptionManager()
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ciphertext, err := manager.Encrypt("secret")
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	plaintext, err := manager.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if plaintext != "secret" {
		t.Fatalf("expected plaintext to match, got %s", plaintext)
	}
}
