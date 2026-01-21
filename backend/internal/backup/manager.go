package backup

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

// BackupManager orchestrates backup operations
type BackupManager struct {
	db            *sql.DB
	sshPool       *ssh.ConnectionPool
	archiveHandler *ArchiveHandler
}

// BackupRequest represents a backup creation request
type BackupRequest struct {
	ServerID    string
	Directories []string
	Exclude     []string
	WorkingDir  string
	Compression CompressionConfig
	RunAsUser   string
	UseSudo     bool
	Destination *DestinationConfig
	CreatedBy   string
}

// BackupRecord represents a backup record in the database
type BackupRecord struct {
	ID              string
	ServerID        string
	Filename        string
	SizeBytes       int64
	CreatedAt       time.Time
	DestinationType string
	DestinationPath string
	Status          string
	ErrorMessage    string
	Metadata        map[string]interface{}
	CreatedBy       string
}

// NewBackupManager creates a new backup manager
func NewBackupManager(db *sql.DB, pool *ssh.ConnectionPool) *BackupManager {
	return &BackupManager{
		db:             db,
		sshPool:        pool,
		archiveHandler: NewArchiveHandler(pool),
	}
}

// CreateBackup creates a new backup
func (bm *BackupManager) CreateBackup(req *BackupRequest) (*BackupRecord, error) {
	backupID := "backup-" + uuid.New().String()[:8]
	log.Printf("[BackupMgr] Creating backup %s for server %s", backupID, req.ServerID)

	// Create initial backup record
	record := &BackupRecord{
		ID:              backupID,
		ServerID:        req.ServerID,
		Status:          "creating",
		CreatedAt:       time.Now(),
		DestinationType: req.Destination.Type,
		DestinationPath: req.Destination.Path,
		CreatedBy:       req.CreatedBy,
	}

	if err := bm.saveBackupRecord(record); err != nil {
		return nil, fmt.Errorf("failed to save backup record: %w", err)
	}

	// Create archive on remote server
	archiveInfo, err := bm.archiveHandler.CreateArchive(req.ServerID, req.Directories, req.Exclude, req.WorkingDir, ArchiveOptions{
		Compression: req.Compression,
		RunAsUser:   req.RunAsUser,
		UseSudo:     req.UseSudo,
	})
	if err != nil {
		record.Status = "failed"
		record.ErrorMessage = err.Error()
		bm.saveBackupRecord(record)
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	// Update record with archive info
	record.Filename = archiveInfo.Filename
	record.SizeBytes = archiveInfo.SizeBytes
	record.Metadata = map[string]interface{}{
		"directories": archiveInfo.Directories,
		"exclude":     req.Exclude,
		"file_count":  archiveInfo.FileCount,
		"created_at":  archiveInfo.CreatedAt,
		"compression": archiveInfo.Compression,
	}

	// Transfer to destination
	if err := bm.transferToDestination(req.ServerID, archiveInfo, req.Destination); err != nil {
		record.Status = "failed"
		record.ErrorMessage = err.Error()
		bm.saveBackupRecord(record)
		
		// Cleanup local archive
		bm.archiveHandler.DeleteArchiveWithOptions(req.ServerID, archiveInfo.Path, ArchiveOptions{
			RunAsUser: req.RunAsUser,
			UseSudo:   req.UseSudo,
		})
		
		return nil, fmt.Errorf("failed to transfer backup: %w", err)
	}

	// Mark as completed
	record.Status = "completed"
	if err := bm.saveBackupRecord(record); err != nil {
		log.Printf("[BackupMgr] Warning: Failed to update backup status: %v", err)
	}

	// Cleanup local archive after successful transfer (optional, depends on destination type)
	if req.Destination.Type != "local" {
		if err := bm.archiveHandler.DeleteArchiveWithOptions(req.ServerID, archiveInfo.Path, ArchiveOptions{
			RunAsUser: req.RunAsUser,
			UseSudo:   req.UseSudo,
		}); err != nil {
			log.Printf("[BackupMgr] Warning: Failed to cleanup local archive: %v", err)
		}
	}

	log.Printf("[BackupMgr] Backup %s created successfully: %s (%d bytes)", 
		backupID, archiveInfo.Filename, archiveInfo.SizeBytes)

	return record, nil
}

// transferToDestination transfers the backup to the configured destination
func (bm *BackupManager) transferToDestination(serverID string, archiveInfo *ArchiveInfo, destConfig *DestinationConfig) error {
	log.Printf("[BackupMgr] Transferring backup to %s destination", destConfig.Type)

	// Create destination
	dest, err := NewDestination(destConfig)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}

	// For SFTP destination, close connection after transfer
	if sftpDest, ok := dest.(*SFTPDestination); ok {
		defer sftpDest.Close()
	}

	// Special case: If destination is local and archive is already local, just move/copy
	if localDest, ok := dest.(*LocalDestination); ok {
		// Check if source and dest are on same filesystem
		// For simplicity, we'll download and re-upload
		_ = localDest // TODO: Optimize local-to-local transfers
	}

	// Download archive from remote server
	conn := bm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Use SFTP to download the archive
	sftpClient, err := conn.Client.NewSFTPWithOptions(
		sftp.MaxPacketUnchecked(131072),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(64),
	)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	srcFile, err := sftpClient.Open(archiveInfo.Path)
	if err != nil {
		return fmt.Errorf("failed to open remote archive: %w", err)
	}
	defer srcFile.Close()

	// Upload to destination
	if err := dest.Upload(archiveInfo.Filename, srcFile, archiveInfo.SizeBytes); err != nil {
		return fmt.Errorf("failed to upload to destination: %w", err)
	}

	log.Printf("[BackupMgr] Transfer complete")
	return nil
}

// RestoreBackup restores a backup to the server
func (bm *BackupManager) RestoreBackup(backupID, serverID, destination string) error {
	log.Printf("[BackupMgr] Restoring backup %s to %s", backupID, destination)

	// Get backup record
	record, err := bm.GetBackup(backupID)
	if err != nil {
		return fmt.Errorf("failed to get backup record: %w", err)
	}

	if record.ServerID != serverID {
		return fmt.Errorf("backup does not belong to server %s", serverID)
	}

	if record.Status != "completed" {
		return fmt.Errorf("backup is not in completed state: %s", record.Status)
	}

	// Create destination config
	destConfig := &DestinationConfig{
		Type: record.DestinationType,
		Path: record.DestinationPath,
	}

	// Download from destination
	dest, err := NewDestination(destConfig)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}

	if sftpDest, ok := dest.(*SFTPDestination); ok {
		defer sftpDest.Close()
	}

	// Download to temporary buffer
	var buf bytes.Buffer
	if err := dest.Download(record.Filename, &buf); err != nil {
		return fmt.Errorf("failed to download backup: %w", err)
	}

	// Upload to remote server
	conn := bm.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	sftpClient, err := conn.Client.NewSFTPWithOptions(
		sftp.MaxPacketUnchecked(131072),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(64),
	)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	// Create temporary restore path
	tempPath := fmt.Sprintf("/tmp/restore_%s_%s", backupID, record.Filename)
	dstFile, err := sftpClient.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create restore file: %w", err)
	}

	if _, err := dstFile.Write(buf.Bytes()); err != nil {
		dstFile.Close()
		return fmt.Errorf("failed to write restore file: %w", err)
	}
	dstFile.Close()

	// Extract archive
	if err := bm.archiveHandler.ExtractArchive(serverID, tempPath, destination); err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	// Cleanup temp file
	if err := bm.archiveHandler.DeleteArchive(serverID, tempPath); err != nil {
		log.Printf("[BackupMgr] Warning: Failed to cleanup temp file: %v", err)
	}

	log.Printf("[BackupMgr] Backup %s restored successfully to %s", backupID, destination)
	return nil
}

// DeleteBackup deletes a backup
func (bm *BackupManager) DeleteBackup(backupID string) error {
	log.Printf("[BackupMgr] Deleting backup %s", backupID)

	// Get backup record
	record, err := bm.GetBackup(backupID)
	if err != nil {
		return fmt.Errorf("failed to get backup record: %w", err)
	}

	// Create destination
	destConfig := &DestinationConfig{
		Type: record.DestinationType,
		Path: record.DestinationPath,
	}

	dest, err := NewDestination(destConfig)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}

	if sftpDest, ok := dest.(*SFTPDestination); ok {
		defer sftpDest.Close()
	}

	// Delete from destination
	if err := dest.Delete(record.Filename); err != nil {
		log.Printf("[BackupMgr] Warning: Failed to delete from destination: %v", err)
	}

	// Update database record
	record.Status = "deleted"
	if err := bm.saveBackupRecord(record); err != nil {
		return fmt.Errorf("failed to update backup record: %w", err)
	}

	log.Printf("[BackupMgr] Backup %s deleted successfully", backupID)
	return nil
}

// ListBackups returns all backups for a server
func (bm *BackupManager) ListBackups(serverID string) ([]*BackupRecord, error) {
	query := `
		SELECT id, server_id, filename, size_bytes, created_at, 
		       destination_type, destination_path, status, error_message, 
		       metadata, created_by
		FROM backups
		WHERE server_id = ? AND status != 'deleted'
		ORDER BY created_at DESC
	`

	rows, err := bm.db.Query(query, serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to query backups: %w", err)
	}
	defer rows.Close()

	var backups []*BackupRecord
	for rows.Next() {
		record := &BackupRecord{}
		var metadataJSON sql.NullString
		var errorMsg sql.NullString
		var createdBy sql.NullString

		err := rows.Scan(
			&record.ID,
			&record.ServerID,
			&record.Filename,
			&record.SizeBytes,
			&record.CreatedAt,
			&record.DestinationType,
			&record.DestinationPath,
			&record.Status,
			&errorMsg,
			&metadataJSON,
			&createdBy,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan backup record: %w", err)
		}

		if errorMsg.Valid {
			record.ErrorMessage = errorMsg.String
		}

		if createdBy.Valid {
			record.CreatedBy = createdBy.String
		}

		if metadataJSON.Valid {
			if err := json.Unmarshal([]byte(metadataJSON.String), &record.Metadata); err != nil {
				log.Printf("[BackupMgr] Warning: Failed to parse metadata: %v", err)
			}
		}

		backups = append(backups, record)
	}

	return backups, nil
}

// GetBackup retrieves a specific backup
func (bm *BackupManager) GetBackup(backupID string) (*BackupRecord, error) {
	query := `
		SELECT id, server_id, filename, size_bytes, created_at, 
		       destination_type, destination_path, status, error_message, 
		       metadata, created_by
		FROM backups
		WHERE id = ?
	`

	record := &BackupRecord{}
	var metadataJSON sql.NullString
	var errorMsg sql.NullString
	var createdBy sql.NullString

	err := bm.db.QueryRow(query, backupID).Scan(
		&record.ID,
		&record.ServerID,
		&record.Filename,
		&record.SizeBytes,
		&record.CreatedAt,
		&record.DestinationType,
		&record.DestinationPath,
		&record.Status,
		&errorMsg,
		&metadataJSON,
		&createdBy,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("backup not found: %s", backupID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query backup: %w", err)
	}

	if errorMsg.Valid {
		record.ErrorMessage = errorMsg.String
	}

	if createdBy.Valid {
		record.CreatedBy = createdBy.String
	}

	if metadataJSON.Valid {
		if err := json.Unmarshal([]byte(metadataJSON.String), &record.Metadata); err != nil {
			log.Printf("[BackupMgr] Warning: Failed to parse metadata: %v", err)
		}
	}

	return record, nil
}

// saveBackupRecord saves or updates a backup record
func (bm *BackupManager) saveBackupRecord(record *BackupRecord) error {
	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO backups 
		(id, server_id, filename, size_bytes, created_at, destination_type, 
		 destination_path, status, error_message, metadata, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = bm.db.Exec(query,
		record.ID,
		record.ServerID,
		record.Filename,
		record.SizeBytes,
		record.CreatedAt,
		record.DestinationType,
		record.DestinationPath,
		record.Status,
		record.ErrorMessage,
		string(metadataJSON),
		record.CreatedBy,
	)

	if err != nil {
		return fmt.Errorf("failed to save backup record: %w", err)
	}

	return nil
}
