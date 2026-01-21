package models

import "time"

// Backup represents a server backup
type Backup struct {
	ID           string    `json:"id"`
	ServerID     string    `json:"server_id"`
	Filename     string    `json:"filename"`
	Size         int64     `json:"size"` // bytes
	Destination  string    `json:"destination"`
	Status       string    `json:"status"` // "pending", "in_progress", "completed", "failed"
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// CreateBackupRequest represents a backup creation request
type CreateBackupRequest struct {
	Destinations []string `json:"destinations,omitempty"` // If empty, use all configured
}

// RestoreBackupRequest represents a backup restore request
type RestoreBackupRequest struct {
	BackupID string `json:"backup_id" binding:"required"`
	TargetPath string `json:"target_path,omitempty"` // If empty, restore to original location
}
