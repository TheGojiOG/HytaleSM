package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPathPrefersParentConfigs(t *testing.T) {
	root := t.TempDir()
	configsDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		t.Fatalf("failed to create configs dir: %v", err)
	}
	configPath := filepath.Join(configsDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  host: 0.0.0.0\n"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	backendDir := filepath.Join(root, "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatalf("failed to create backend dir: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	if err := os.Chdir(backendDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	resolved := resolveConfigPath()
	if resolved != "../configs/config.yaml" {
		t.Fatalf("expected ../configs/config.yaml, got %s", resolved)
	}
}

func TestResolveConfigPathUsesLocalConfigs(t *testing.T) {
	root := t.TempDir()
	configsDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(configsDir, 0755); err != nil {
		t.Fatalf("failed to create configs dir: %v", err)
	}
	configPath := filepath.Join(configsDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  host: 0.0.0.0\n"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(cwd)
	}()

	if err := os.Chdir(root); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	resolved := resolveConfigPath()
	if resolved != "./configs/config.yaml" {
		t.Fatalf("expected ./configs/config.yaml, got %s", resolved)
	}
}

func TestNormalizeStoragePathsDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.normalizeStoragePaths("configs/config.yaml")

	if cfg.Storage.ConfigDir == "" {
		t.Fatalf("expected ConfigDir to be set")
	}
	if cfg.Storage.DataDir == "" {
		t.Fatalf("expected DataDir to be set")
	}
	if cfg.Storage.BackupDir == "" {
		t.Fatalf("expected BackupDir to be set")
	}
	if cfg.Security.SSH.KnownHostsPath == "" {
		t.Fatalf("expected KnownHostsPath to be set")
	}
}
