package logging

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/TheGojiOG/HytaleSM/internal/database"
)

func TestActivityLoggerLogActivity(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "test.db")
	logDir := filepath.Join(root, "logs")

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}

	logger, err := NewActivityLogger(db.DB, logDir)
	if err != nil {
		t.Fatalf("failed to create activity logger: %v", err)
	}
	defer logger.Close()

	if err := logger.LogActivity(&Activity{
		ServerID:     "server-1",
		UserID:       nil,
		ActivityType: ActivityServerStart,
		Description:  "started",
		Success:      true,
	}); err != nil {
		t.Fatalf("failed to log activity: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM activity_log").Scan(&count); err != nil {
		t.Fatalf("failed to query activity log: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected activity_log to contain rows")
	}

	if err := logger.CleanupOldActivities(24 * time.Hour); err != nil {
		t.Fatalf("failed to cleanup activities: %v", err)
	}
}
