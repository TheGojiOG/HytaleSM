package database

import (
	"path/filepath"
	"testing"
)

func TestNewDBAndMigrate(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "test.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count); err != nil {
		t.Fatalf("failed to query migrations: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected migrations to be applied")
	}
}
