package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yourusername/hytale-server-manager/internal/database"
)

func TestRBACManagerHasPermission(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "test.db")

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	_, err = db.Exec(`INSERT INTO users (username, email, password_hash) VALUES ('test', 'test@example.com', 'hash')`)
	if err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE username = 'test'").Scan(&userID); err != nil {
		t.Fatalf("failed to read user id: %v", err)
	}

	_, err = db.Exec("INSERT INTO user_roles (user_id, role_id) VALUES (?, 1)", userID)
	if err != nil {
		t.Fatalf("failed to assign role: %v", err)
	}

	manager := NewRBACManager(db.DB)
	hasPermission, err := manager.HasPermission(userID, "iam.users.list")
	if err != nil {
		t.Fatalf("failed to check permission: %v", err)
	}
	if !hasPermission {
		t.Fatalf("expected permission to be granted")
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db file to exist: %v", err)
	}
}
