package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalDestinationUploadDownloadDelete(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "backups")
	ld := NewLocalDestination(baseDir)

	content := []byte("backup-data")
	if err := ld.Upload("test.tar.gz", bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("upload failed: %v", err)
	}

	if !ld.Exists("test.tar.gz") {
		t.Fatalf("expected backup file to exist")
	}

	var buf bytes.Buffer
	if err := ld.Download("test.tar.gz", &buf); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Fatalf("downloaded content mismatch")
	}

	files, err := ld.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	if err := ld.Delete("test.tar.gz"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if ld.Exists("test.tar.gz") {
		t.Fatalf("expected backup file to be removed")
	}
}

func TestNewDestinationInvalidType(t *testing.T) {
	_, err := NewDestination(&DestinationConfig{Type: "invalid", Path: os.TempDir()})
	if err == nil {
		t.Fatalf("expected error for invalid destination type")
	}
}
