package backup

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
)

// RetentionManager handles backup retention policies
type RetentionManager struct {
	db            *sql.DB
	backupManager *BackupManager
}

// RetentionPolicy defines how many backups to keep
type RetentionPolicy struct {
	Count     int    // Number of backups to keep (0 = keep all)
	ServerID  string // Server ID for the policy
}

// NewRetentionManager creates a new retention manager
func NewRetentionManager(db *sql.DB, backupMgr *BackupManager) *RetentionManager {
	return &RetentionManager{
		db:            db,
		backupManager: backupMgr,
	}
}

// EnforceRetention enforces retention policy for a server
func (rm *RetentionManager) EnforceRetention(serverID string, retentionCount int) error {
	if retentionCount <= 0 {
		log.Printf("[Retention] No retention policy for server %s (keep all)", serverID)
		return nil
	}

	log.Printf("[Retention] Enforcing retention policy for server %s (keep %d)", serverID, retentionCount)

	// Get all completed backups for the server
	backups, err := rm.backupManager.ListBackups(serverID)
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}

	// Filter only completed backups
	var completedBackups []*BackupRecord
	for _, backup := range backups {
		if backup.Status == "completed" {
			completedBackups = append(completedBackups, backup)
		}
	}

	// If we have fewer backups than the retention count, nothing to do
	if len(completedBackups) <= retentionCount {
		log.Printf("[Retention] Current backup count (%d) is within retention policy (%d)", 
			len(completedBackups), retentionCount)
		return nil
	}

	// Sort backups by creation time (newest first)
	sort.Slice(completedBackups, func(i, j int) bool {
		return completedBackups[i].CreatedAt.After(completedBackups[j].CreatedAt)
	})

	// Delete old backups beyond retention count
	deleted := 0
	for i := retentionCount; i < len(completedBackups); i++ {
		backup := completedBackups[i]
		log.Printf("[Retention] Deleting old backup: %s (created: %s)", 
			backup.ID, backup.CreatedAt.Format("2006-01-02 15:04:05"))

		if err := rm.backupManager.DeleteBackup(backup.ID); err != nil {
			log.Printf("[Retention] Error deleting backup %s: %v", backup.ID, err)
			continue
		}

		deleted++
	}

	log.Printf("[Retention] Retention enforcement complete: deleted %d backups", deleted)
	return nil
}

// EnforceAllRetentions enforces retention policies for all servers
func (rm *RetentionManager) EnforceAllRetentions() error {
	log.Printf("[Retention] Enforcing retention policies for all servers")

	// Get all servers with backup schedules (use max retention per server)
	query := `
		SELECT server_id, MAX(retention_count)
		FROM backup_schedules
		WHERE enabled = true AND retention_count > 0
		GROUP BY server_id
	`

	rows, err := rm.db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query backup schedules: %w", err)
	}
	defer rows.Close()

	enforced := 0
	for rows.Next() {
		var serverID string
		var retentionCount int

		if err := rows.Scan(&serverID, &retentionCount); err != nil {
			log.Printf("[Retention] Error scanning row: %v", err)
			continue
		}

		if err := rm.EnforceRetention(serverID, retentionCount); err != nil {
			log.Printf("[Retention] Error enforcing retention for server %s: %v", serverID, err)
			continue
		}

		enforced++
	}

	log.Printf("[Retention] Enforced retention policies for %d servers", enforced)
	return nil
}

// GetRetentionStats returns retention statistics for a server
func (rm *RetentionManager) GetRetentionStats(serverID string, retentionCount int) (map[string]interface{}, error) {
	backups, err := rm.backupManager.ListBackups(serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to list backups: %w", err)
	}

	// Filter completed backups
	var completedBackups []*BackupRecord
	var totalSize int64
	for _, backup := range backups {
		if backup.Status == "completed" {
			completedBackups = append(completedBackups, backup)
			totalSize += backup.SizeBytes
		}
	}

	stats := map[string]interface{}{
		"total_backups":     len(completedBackups),
		"retention_limit":   retentionCount,
		"backups_to_delete": 0,
		"total_size_bytes":  totalSize,
		"will_delete_size":  int64(0),
	}

	if retentionCount > 0 && len(completedBackups) > retentionCount {
		toDelete := len(completedBackups) - retentionCount
		stats["backups_to_delete"] = toDelete

		// Sort to find oldest backups
		sort.Slice(completedBackups, func(i, j int) bool {
			return completedBackups[i].CreatedAt.After(completedBackups[j].CreatedAt)
		})

		var deleteSize int64
		for i := retentionCount; i < len(completedBackups); i++ {
			deleteSize += completedBackups[i].SizeBytes
		}
		stats["will_delete_size"] = deleteSize
	}

	return stats, nil
}
