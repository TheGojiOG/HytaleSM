package logging

import (
	"path/filepath"
	"testing"

	"github.com/yourusername/hytale-server-manager/internal/config"
)

func TestInitAndCloseLogger(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "app.log")

	_, err := Init(config.LoggingConfig{
		Level:      "info",
		Format:     "json",
		File:       logPath,
		MaxSize:    10,
		MaxBackups: 1,
		MaxAge:     1,
	})
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	L().Info("test_log")
	if err := Close(); err != nil {
		t.Fatalf("failed to close logger: %v", err)
	}
}
