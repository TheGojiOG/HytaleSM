package backup

import (
	"fmt"
	"path"
	"strings"

	"github.com/yourusername/hytale-server-manager/internal/config"
)

// BuildDefaultSchedule returns the default nightly backup schedule for a server.
func BuildDefaultSchedule(server *config.ServerDefinition) (*BackupSchedule, error) {
	if server == nil {
		return nil, fmt.Errorf("server definition is required")
	}

	installDir := strings.TrimSpace(server.Dependencies.InstallDir)
	if installDir == "" {
		installDir = strings.TrimSpace(server.Runtime.BackupDir)
	}
	if installDir == "" {
		return nil, fmt.Errorf("install_dir is required to build default backup schedule")
	}

	destinationPath := path.Join(installDir, "Backups", "Automated")
	directories := []string{
		path.Join(installDir, "Server"),
		path.Join(installDir, "config.json"),
		path.Join(installDir, "permissions.json"),
		path.Join(installDir, "universe"),
		path.Join(installDir, "whitelist.json"),
		path.Join(installDir, "bans.json"),
	}

	return &BackupSchedule{
		ServerID:       server.ID,
		Enabled:        true,
		Schedule:       "0 0 * * *",
		Directories:    directories,
		Exclude:        []string{},
		Destination:    DestinationConfig{Type: "local", Path: destinationPath},
		RetentionCount: 7,
		Compression:    CompressionConfig{Type: "gzip", Level: 6},
		RunAsUser:      server.Dependencies.ServiceUser,
		UseSudo:        server.Dependencies.UseSudo,
	}, nil
}
