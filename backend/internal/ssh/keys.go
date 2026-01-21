package ssh

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	crypto "github.com/yourusername/hytale-server-manager/internal/crypto"
)

const encryptedKeyHeader = "ENC1\n"

// ReadPrivateKeyBytes reads a private key file and decrypts it if it uses ENC1 encoding.
func ReadPrivateKeyBytes(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}

	if !bytes.HasPrefix(data, []byte(encryptedKeyHeader)) {
		return data, nil
	}

	payload := strings.TrimSpace(string(data[len(encryptedKeyHeader):]))
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted key: %w", err)
	}

	manager, err := crypto.NewEncryptionManager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize encryption manager: %w", err)
	}

	plaintext, err := manager.Decrypt(decoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key: %w", err)
	}

	return []byte(plaintext), nil
}
