package logging

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ActivityLogger provides centralized logging of all server activities
type ActivityLogger struct {
	db           *sql.DB
	logDir       string
	currentFile  *os.File
	currentDate  string
	mu           sync.Mutex
}

// Activity represents a logged activity
type Activity struct {
	Timestamp    time.Time              `json:"timestamp"`
	ServerID     string                 `json:"server_id"`
	UserID       *int64                 `json:"user_id,omitempty"`
	ActivityType string                 `json:"activity_type"`
	Description  string                 `json:"description"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Success      bool                   `json:"success"`
	ErrorMessage string                 `json:"error_message,omitempty"`
}

// Activity type constants
const (
	ActivityServerStart          = "server.start"
	ActivityServerStop           = "server.stop"
	ActivityServerRestart        = "server.restart"
	ActivityServerStatusChange   = "server.status_change"
	ActivityCommandExecute       = "command.execute"
	ActivityConfigUpdate         = "config.update"
	ActivityBackupCreate         = "backup.create"
	ActivityBackupRestore        = "backup.restore"
	ActivityConnectionEstablished = "connection.established"
	ActivityConnectionLost       = "connection.lost"
	ActivitySSHReconnect         = "ssh.reconnect"
	ActivityScreenCreate         = "screen.create"
	ActivityScreenQuit           = "screen.quit"
	ActivityPTYAttach            = "pty.attach"
	ActivityPTYDetach            = "pty.detach"
	ActivityMetricsCollected     = "metrics.collected"
	ActivityPackageInstall       = "package.install"
	ActivityPackageDetect        = "package.detect"
	ActivityError                = "error"
)

// NewActivityLogger creates a new activity logger
func NewActivityLogger(db *sql.DB, logDir string) (*ActivityLogger, error) {
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logger := &ActivityLogger{
		db:     db,
		logDir: logDir,
	}

	log.Printf("[ActivityLogger] Initialized (log directory: %s)", logDir)

	return logger, nil
}

// LogActivity logs an activity to both database and file
func (al *ActivityLogger) LogActivity(activity *Activity) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	// Set timestamp if not set
	if activity.Timestamp.IsZero() {
		activity.Timestamp = time.Now()
	}

	// Log to database
	if err := al.logToDatabase(activity); err != nil {
		log.Printf("[ActivityLogger] Error logging to database: %v", err)
		// Don't return error, continue with file logging
	}

	// Log to file
	if err := al.logToFile(activity); err != nil {
		log.Printf("[ActivityLogger] Error logging to file: %v", err)
		return err
	}

	return nil
}

// LogServerStart logs a server start activity
func (al *ActivityLogger) LogServerStart(serverID string, userID *int64, success bool, errorMsg string) error {
	metadata := make(map[string]interface{})
	if errorMsg != "" {
		metadata["error"] = errorMsg
	}

	return al.LogActivity(&Activity{
		ServerID:     serverID,
		UserID:       userID,
		ActivityType: ActivityServerStart,
		Description:  "Server start initiated",
		Metadata:     metadata,
		Success:      success,
		ErrorMessage: errorMsg,
	})
}

// LogServerStop logs a server stop activity
func (al *ActivityLogger) LogServerStop(serverID string, userID *int64, graceful bool, success bool, errorMsg string) error {
	metadata := map[string]interface{}{
		"graceful": graceful,
	}

	if errorMsg != "" {
		metadata["error"] = errorMsg
	}

	return al.LogActivity(&Activity{
		ServerID:     serverID,
		UserID:       userID,
		ActivityType: ActivityServerStop,
		Description:  fmt.Sprintf("Server stop initiated (graceful: %v)", graceful),
		Metadata:     metadata,
		Success:      success,
		ErrorMessage: errorMsg,
	})
}

// LogServerRestart logs a server restart activity
func (al *ActivityLogger) LogServerRestart(serverID string, userID *int64, graceful bool, success bool, errorMsg string) error {
	metadata := map[string]interface{}{
		"graceful": graceful,
	}

	if errorMsg != "" {
		metadata["error"] = errorMsg
	}

	return al.LogActivity(&Activity{
		ServerID:     serverID,
		UserID:       userID,
		ActivityType: ActivityServerRestart,
		Description:  fmt.Sprintf("Server restart initiated (graceful: %v)", graceful),
		Metadata:     metadata,
		Success:      success,
		ErrorMessage: errorMsg,
	})
}

// LogCommandExecute logs a console command execution
func (al *ActivityLogger) LogCommandExecute(serverID string, userID *int64, command string, success bool, output string, errorMsg string) error {
	metadata := map[string]interface{}{
		"command": command,
	}

	if output != "" {
		// Truncate output if too long
		if len(output) > 1000 {
			metadata["output"] = output[:1000] + "... (truncated)"
		} else {
			metadata["output"] = output
		}
	}

	if errorMsg != "" {
		metadata["error"] = errorMsg
	}

	return al.LogActivity(&Activity{
		ServerID:     serverID,
		UserID:       userID,
		ActivityType: ActivityCommandExecute,
		Description:  fmt.Sprintf("Command executed: %s", command),
		Metadata:     metadata,
		Success:      success,
		ErrorMessage: errorMsg,
	})
}

// LogStatusChange logs a server status change
func (al *ActivityLogger) LogStatusChange(serverID string, oldStatus, newStatus string, metadata map[string]interface{}) error {
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	metadata["old_status"] = oldStatus
	metadata["new_status"] = newStatus

	return al.LogActivity(&Activity{
		ServerID:     serverID,
		ActivityType: ActivityServerStatusChange,
		Description:  fmt.Sprintf("Status changed: %s → %s", oldStatus, newStatus),
		Metadata:     metadata,
		Success:      true,
	})
}

// LogConnectionEstablished logs an SSH connection establishment
func (al *ActivityLogger) LogConnectionEstablished(serverID string, host string, port int) error {
	return al.LogActivity(&Activity{
		ServerID:     serverID,
		ActivityType: ActivityConnectionEstablished,
		Description:  fmt.Sprintf("SSH connection established to %s:%d", host, port),
		Metadata: map[string]interface{}{
			"host": host,
			"port": port,
		},
		Success: true,
	})
}

// LogConnectionLost logs an SSH connection loss
func (al *ActivityLogger) LogConnectionLost(serverID string, reason string) error {
	return al.LogActivity(&Activity{
		ServerID:     serverID,
		ActivityType: ActivityConnectionLost,
		Description:  "SSH connection lost",
		Metadata: map[string]interface{}{
			"reason": reason,
		},
		Success: false,
	})
}

// LogError logs a general error
func (al *ActivityLogger) LogError(serverID string, errorType string, errorMsg string, metadata map[string]interface{}) error {
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	metadata["error_type"] = errorType

	return al.LogActivity(&Activity{
		ServerID:     serverID,
		ActivityType: ActivityError,
		Description:  errorType,
		Metadata:     metadata,
		Success:      false,
		ErrorMessage: errorMsg,
	})
}

// GetActivities retrieves activities from the database
func (al *ActivityLogger) GetActivities(serverID string, activityType string, since time.Time, limit int) ([]*Activity, error) {
	if al.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	query := `
		SELECT timestamp, server_id, user_id, activity_type, description, metadata, success, error_message
		FROM activity_log
		WHERE 1=1
	`
	args := make([]interface{}, 0)

	if serverID != "" {
		query += " AND server_id = ?"
		args = append(args, serverID)
	}

	if activityType != "" {
		query += " AND activity_type = ?"
		args = append(args, activityType)
	}

	if !since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, since)
	}

	query += " ORDER BY timestamp DESC"

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := al.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query activities: %w", err)
	}
	defer rows.Close()

	activities := make([]*Activity, 0)

	for rows.Next() {
		activity := &Activity{}
		var userID sql.NullInt64
		var metadataJSON sql.NullString

		err := rows.Scan(
			&activity.Timestamp,
			&activity.ServerID,
			&userID,
			&activity.ActivityType,
			&activity.Description,
			&metadataJSON,
			&activity.Success,
			&activity.ErrorMessage,
		)

		if err != nil {
			log.Printf("[ActivityLogger] Error scanning row: %v", err)
			continue
		}

		if userID.Valid {
			uid := userID.Int64
			activity.UserID = &uid
		}

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &activity.Metadata); err != nil {
				log.Printf("[ActivityLogger] Error unmarshaling metadata: %v", err)
			}
		}

		activities = append(activities, activity)
	}

	return activities, nil
}

// GetRecentActivities retrieves the most recent activities
func (al *ActivityLogger) GetRecentActivities(limit int) ([]*Activity, error) {
	return al.GetActivities("", "", time.Time{}, limit)
}

// GetServerActivities retrieves activities for a specific server
func (al *ActivityLogger) GetServerActivities(serverID string, limit int) ([]*Activity, error) {
	return al.GetActivities(serverID, "", time.Time{}, limit)
}

// logToDatabase logs an activity to the database
func (al *ActivityLogger) logToDatabase(activity *Activity) error {
	if al.db == nil {
		return nil // Database not configured
	}

	metadataJSON, err := json.Marshal(activity.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO activity_log (
			timestamp, server_id, user_id, activity_type,
			description, metadata, success, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = al.db.Exec(
		query,
		activity.Timestamp,
		activity.ServerID,
		activity.UserID,
		activity.ActivityType,
		activity.Description,
		string(metadataJSON),
		activity.Success,
		activity.ErrorMessage,
	)

	if err != nil {
		return fmt.Errorf("failed to insert activity: %w", err)
	}

	return nil
}

// logToFile logs an activity to a JSON file
func (al *ActivityLogger) logToFile(activity *Activity) error {
	// Get current date for log rotation
	currentDate := time.Now().Format("2006-01-02")

	// Check if we need to rotate the log file
	if al.currentFile == nil || al.currentDate != currentDate {
		if err := al.rotateLogFile(currentDate); err != nil {
			return fmt.Errorf("failed to rotate log file: %w", err)
		}
	}

	// Write activity as JSON line
	line, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	_, err = fmt.Fprintf(al.currentFile, "%s\n", line)
	if err != nil {
		return fmt.Errorf("failed to write to log file: %w", err)
	}

	// Sync to disk for important events
	if activity.ActivityType == ActivityServerStart ||
		activity.ActivityType == ActivityServerStop ||
		activity.ActivityType == ActivityError {
		al.currentFile.Sync()
	}

	return nil
}

// rotateLogFile rotates the log file for a new day
func (al *ActivityLogger) rotateLogFile(date string) error {
	// Close current file if open
	if al.currentFile != nil {
		al.currentFile.Close()
		al.currentFile = nil
	}

	// Create new log file
	logPath := filepath.Join(al.logDir, fmt.Sprintf("activity-%s.log", date))

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	al.currentFile = file
	al.currentDate = date

	log.Printf("[ActivityLogger] Rotated log file to: %s", logPath)

	// Compress old log files (older than 1 day)
	go al.compressOldLogs()

	return nil
}

// compressOldLogs compresses log files older than 1 day
func (al *ActivityLogger) compressOldLogs() {
	// This would typically use gzip compression
	// For now, we'll just log that we would do this
	// A real implementation would compress files older than 1 day

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	oldLogPath := filepath.Join(al.logDir, fmt.Sprintf("activity-%s.log", yesterday))

	if _, err := os.Stat(oldLogPath); err == nil {
		// File exists, would compress here
		log.Printf("[ActivityLogger] Would compress old log: %s", oldLogPath)
		// exec.Command("gzip", oldLogPath).Run()
	}
}

// Close closes the activity logger
func (al *ActivityLogger) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if al.currentFile != nil {
		return al.currentFile.Close()
	}

	return nil
}

// CleanupOldActivities removes activities older than a specified duration
func (al *ActivityLogger) CleanupOldActivities(olderThan time.Duration) error {
	if al.db == nil {
		return fmt.Errorf("database not available")
	}

	cutoff := time.Now().Add(-olderThan)

	result, err := al.db.Exec(`
		DELETE FROM activity_log
		WHERE timestamp < ?
	`, cutoff)

	if err != nil {
		return fmt.Errorf("failed to cleanup old activities: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	log.Printf("[ActivityLogger] Cleaned up %d activities older than %v", rowsAffected, olderThan)

	return nil
}

// GetActivityStats retrieves activity statistics
func (al *ActivityLogger) GetActivityStats(serverID string, since time.Time) (map[string]int, error) {
	if al.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	query := `
		SELECT activity_type, COUNT(*) as count
		FROM activity_log
		WHERE 1=1
	`
	args := make([]interface{}, 0)

	if serverID != "" {
		query += " AND server_id = ?"
		args = append(args, serverID)
	}

	if !since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, since)
	}

	query += " GROUP BY activity_type"

	rows, err := al.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query activity stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int)

	for rows.Next() {
		var activityType string
		var count int

		if err := rows.Scan(&activityType, &count); err != nil {
			log.Printf("[ActivityLogger] Error scanning stats row: %v", err)
			continue
		}

		stats[activityType] = count
	}

	return stats, nil
}
