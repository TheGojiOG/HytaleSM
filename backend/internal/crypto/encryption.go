package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

const (
	// DefaultKeyID is the default encryption key version
	DefaultKeyID = "v1"
)

// EncryptionManager handles AES-256 encryption/decryption
type EncryptionManager struct {
	key   []byte
	keyID string
}

// NewEncryptionManager creates a new encryption manager
func NewEncryptionManager() (*EncryptionManager, error) {
	// Try to get key from environment
	keyStr := os.Getenv("ENCRYPTION_KEY")
	
	var key []byte
	if keyStr == "" {
		// Generate new key
		generatedKey, err := generateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate encryption key: %w", err)
		}
		key = generatedKey

		fmt.Printf("\nWARNING: No ENCRYPTION_KEY found in environment. Generated a new key in memory.\n")
		fmt.Printf("Set ENCRYPTION_KEY in your .env to keep data decryptable across restarts.\n\n")

		// In production, you might want to fail here instead of auto-generating
	} else {
		// Decode key from base64
		decoded, err := base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ENCRYPTION_KEY format (must be base64): %w", err)
		}
		
		// Ensure key is 32 bytes for AES-256
		if len(decoded) != 32 {
			// Derive key using SHA-256 if not exactly 32 bytes
			hash := sha256.Sum256(decoded)
			key = hash[:]
		} else {
			key = decoded
		}
	}
	
	return &EncryptionManager{
		key:   key,
		keyID: DefaultKeyID,
	}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM
func (em *EncryptionManager) Encrypt(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(em.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// GCM (Galois/Counter Mode) provides authenticated encryption
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Create a nonce (number used once)
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and authenticate
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM
func (em *EncryptionManager) Decrypt(ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(em.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and ciphertext
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt and verify
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// GetKeyID returns the current encryption key ID/version
func (em *EncryptionManager) GetKeyID() string {
	return em.keyID
}

// generateKey generates a random 32-byte key for AES-256
func generateKey() ([]byte, error) {
	key := make([]byte, 32) // 256 bits
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// EncryptSSHKey encrypts an SSH private key for storage
func (em *EncryptionManager) EncryptSSHKey(keyContent string) ([]byte, error) {
	return em.Encrypt(keyContent)
}

// DecryptSSHKey decrypts an SSH private key from storage
func (em *EncryptionManager) DecryptSSHKey(encrypted []byte) (string, error) {
	return em.Decrypt(encrypted)
}

// EncryptPassword encrypts a password for storage
func (em *EncryptionManager) EncryptPassword(password string) ([]byte, error) {
	return em.Encrypt(password)
}

// DecryptPassword decrypts a password from storage
func (em *EncryptionManager) DecryptPassword(encrypted []byte) (string, error) {
	return em.Decrypt(encrypted)
}
