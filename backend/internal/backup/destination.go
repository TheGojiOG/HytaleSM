package backup

import (
	"fmt"
	"io"
)

// Destination represents a backup storage destination
type Destination interface {
	// Upload uploads a file from the source reader to the destination
	Upload(filename string, reader io.Reader, sizeBytes int64) error

	// Download downloads a file from the destination to the writer
	Download(filename string, writer io.Writer) error

	// Delete removes a file from the destination
	Delete(filename string) error

	// List returns all backup files at the destination
	List() ([]BackupFile, error)

	// GetType returns the destination type identifier
	GetType() string
}

// BackupFile represents a file in a backup destination
type BackupFile struct {
	Filename  string
	SizeBytes int64
	CreatedAt int64 // Unix timestamp
}

// DestinationConfig contains configuration for a backup destination
type DestinationConfig struct {
	Type string // "local", "sftp", "s3"
	Path string // Base path for backups

	// SFTP specific
	SFTPHost        string
	SFTPPort        int
	SFTPUsername    string
	SFTPPassword    string
	SFTPKeyPath     string
	KnownHostsPath  string
	TrustOnFirstUse bool

	// S3 specific
	S3Bucket    string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	S3Endpoint  string // Optional, for S3-compatible storage
}

// NewDestination creates a new backup destination based on config
func NewDestination(config *DestinationConfig) (Destination, error) {
	switch config.Type {
	case "local":
		return NewLocalDestination(config.Path), nil
	case "sftp":
		return NewSFTPDestination(config)
	case "s3":
		return NewS3Destination(config)
	default:
		return nil, fmt.Errorf("unsupported destination type: %s", config.Type)
	}
}
