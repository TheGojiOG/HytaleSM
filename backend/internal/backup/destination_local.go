package backup

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// LocalDestination stores backups on the local filesystem
type LocalDestination struct {
	basePath string
}

// NewLocalDestination creates a new local destination
func NewLocalDestination(basePath string) *LocalDestination {
	return &LocalDestination{
		basePath: basePath,
	}
}

// Upload copies a backup file to the local destination
func (ld *LocalDestination) Upload(filename string, reader io.Reader, sizeBytes int64) error {
	// Ensure base directory exists
	if err := os.MkdirAll(ld.basePath, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	destPath := filepath.Join(ld.basePath, filename)
	log.Printf("[LocalDest] Uploading %s to %s (%d bytes)", filename, destPath, sizeBytes)

	// Create destination file
	file, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer file.Close()

	// Copy data
	written, err := io.Copy(file, reader)
	if err != nil {
		os.Remove(destPath) // Cleanup on error
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	if written != sizeBytes {
		os.Remove(destPath)
		return fmt.Errorf("size mismatch: expected %d bytes, wrote %d bytes", sizeBytes, written)
	}

	log.Printf("[LocalDest] Upload complete: %s", filename)
	return nil
}

// Download reads a backup file from the local destination
func (ld *LocalDestination) Download(filename string, writer io.Writer) error {
	srcPath := filepath.Join(ld.basePath, filename)
	log.Printf("[LocalDest] Downloading %s from %s", filename, srcPath)

	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	log.Printf("[LocalDest] Download complete: %s", filename)
	return nil
}

// Delete removes a backup file from the local destination
func (ld *LocalDestination) Delete(filename string) error {
	destPath := filepath.Join(ld.basePath, filename)
	log.Printf("[LocalDest] Deleting %s", destPath)

	if err := os.Remove(destPath); err != nil {
		return fmt.Errorf("failed to delete backup file: %w", err)
	}

	log.Printf("[LocalDest] Delete complete: %s", filename)
	return nil
}

// List returns all backup files in the local destination
func (ld *LocalDestination) List() ([]BackupFile, error) {
	// Ensure directory exists
	if err := os.MkdirAll(ld.basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to access backup directory: %w", err)
	}

	entries, err := os.ReadDir(ld.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	var files []BackupFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			log.Printf("[LocalDest] Warning: Failed to get info for %s: %v", entry.Name(), err)
			continue
		}

		files = append(files, BackupFile{
			Filename:  entry.Name(),
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().Unix(),
		})
	}

	return files, nil
}

// GetType returns the destination type
func (ld *LocalDestination) GetType() string {
	return "local"
}

// GetPath returns the base path
func (ld *LocalDestination) GetPath() string {
	return ld.basePath
}

// Exists checks if a backup file exists
func (ld *LocalDestination) Exists(filename string) bool {
	destPath := filepath.Join(ld.basePath, filename)
	_, err := os.Stat(destPath)
	return err == nil
}

// GetSize returns the size of a backup file
func (ld *LocalDestination) GetSize(filename string) (int64, error) {
	destPath := filepath.Join(ld.basePath, filename)
	info, err := os.Stat(destPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// GetModTime returns the modification time of a backup file
func (ld *LocalDestination) GetModTime(filename string) (time.Time, error) {
	destPath := filepath.Join(ld.basePath, filename)
	info, err := os.Stat(destPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
