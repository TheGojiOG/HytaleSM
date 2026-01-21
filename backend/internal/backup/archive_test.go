package backup

import "testing"

func TestBuildTarCommandRelativePaths(t *testing.T) {
	handler := &ArchiveHandler{}
	cmd := handler.buildTarCommand(
		[]string{"/srv/data", "world"},
		[]string{"*.log"},
		"/tmp/archive.tar.gz",
		"/srv",
		CompressionConfig{Type: "gzip", Level: 6},
	)
	if cmd == "" {
		t.Fatalf("expected tar command to be generated")
	}
}
