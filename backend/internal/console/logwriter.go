package console

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogWriter handles writing console output to log files
type LogWriter struct {
	serverID       string
	logPath        string
	file           *os.File
	db             *sql.DB
	maxSizeBytes   int64
	rotationPolicy time.Duration
	mu             sync.Mutex
	currentLogID   int64
}

// LogWriterConfig contains configuration for log writer
type LogWriterConfig struct {
	ServerID       string
	LogDir         string
	MaxSizeBytes   int64         // Max size before rotation
	RotationPolicy time.Duration // Max age before rotation
	DB             *sql.DB
}

// NewLogWriter creates a new log writer
func NewLogWriter(config *LogWriterConfig) (*LogWriter, error) {
	// Ensure log directory exists
	if err := os.MkdirAll(config.LogDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(config.LogDir, fmt.Sprintf("console_%s.log", timestamp))

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	lw := &LogWriter{
		serverID:       config.ServerID,
		logPath:        logPath,
		file:           file,
		db:             config.DB,
		maxSizeBytes:   config.MaxSizeBytes,
		rotationPolicy: config.RotationPolicy,
	}

	// Record in database
	if err := lw.recordLogFile(); err != nil {
		log.Printf("[LogWriter] Failed to record log file: %v", err)
	}

	go lw.rotationChecker()

	log.Printf("[LogWriter] Created log writer for server %s: %s", config.ServerID, logPath)
	return lw, nil
}

// WriteLine writes a line to the log file
func (lw *LogWriter) WriteLine(line string) error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, err := fmt.Fprintf(lw.file, "[%s] %s\n", timestamp, line)
	if err != nil {
		return fmt.Errorf("failed to write log: %w", err)
	}

	return lw.file.Sync()
}

// Close closes the log file
func (lw *LogWriter) Close() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if lw.file != nil {
		return lw.file.Close()
	}
	return nil
}

// recordLogFile records log file metadata in database
func (lw *LogWriter) recordLogFile() error {
	result, err := lw.db.Exec(`
		INSERT INTO console_logs (server_id, log_path, is_active)
		VALUES (?, ?, 1)
	`, lw.serverID, lw.logPath)

	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err == nil {
		lw.currentLogID = id
	}

	return nil
}

// rotationChecker periodically checks if log rotation is needed
func (lw *LogWriter) rotationChecker() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		lw.checkRotation()
	}
}

// checkRotation checks if log should be rotated
func (lw *LogWriter) checkRotation() {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	stat, err := lw.file.Stat()
	if err != nil {
		log.Printf("[LogWriter] Failed to stat log file: %v", err)
		return
	}

	shouldRotate := false
	reason := ""

	// Check size
	if lw.maxSizeBytes > 0 && stat.Size() >= lw.maxSizeBytes {
		shouldRotate = true
		reason = fmt.Sprintf("size %d >= %d", stat.Size(), lw.maxSizeBytes)
	}

	// Check age
	if lw.rotationPolicy > 0 && time.Since(stat.ModTime()) >= lw.rotationPolicy {
		shouldRotate = true
		reason = fmt.Sprintf("age %v >= %v", time.Since(stat.ModTime()), lw.rotationPolicy)
	}

	if shouldRotate {
		log.Printf("[LogWriter] Rotating log file for server %s (reason: %s)", lw.serverID, reason)
		if err := lw.rotate(); err != nil {
			log.Printf("[LogWriter] Failed to rotate log: %v", err)
		}
	}
}

// rotate rotates the current log file
func (lw *LogWriter) rotate() error {
	// Close current file
	if err := lw.file.Close(); err != nil {
		return err
	}

	// Mark old log as inactive
	if lw.currentLogID > 0 {
		_, err := lw.db.Exec(`
			UPDATE console_logs 
			SET is_active = 0, rotated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, lw.currentLogID)
		if err != nil {
			log.Printf("[LogWriter] Failed to mark old log as inactive: %v", err)
		}
	}

	// Create new log file
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	lw.logPath = filepath.Join(filepath.Dir(lw.logPath), fmt.Sprintf("console_%s.log", timestamp))

	file, err := os.OpenFile(lw.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	lw.file = file

	// Record new log file
	return lw.recordLogFile()
}

// CleanupOldLogs deletes logs older than the retention period
func CleanupOldLogs(db *sql.DB, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	// Get old log files
	rows, err := db.Query(`
		SELECT id, log_path 
		FROM console_logs 
		WHERE created_at < ? AND deleted_at IS NULL
	`, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()

	deleted := 0
	for rows.Next() {
		var id int64
		var logPath string
		if err := rows.Scan(&id, &logPath); err != nil {
			log.Printf("[LogWriter] Failed to scan log row: %v", err)
			continue
		}

		// Delete physical file
		if err := os.Remove(logPath); err != nil {
			log.Printf("[LogWriter] Failed to delete log file %s: %v", logPath, err)
		} else {
			deleted++
		}

		// Mark as deleted in database
		_, err := db.Exec(`
			UPDATE console_logs 
			SET deleted_at = CURRENT_TIMESTAMP 
			WHERE id = ?
		`, id)
		if err != nil {
			log.Printf("[LogWriter] Failed to mark log as deleted: %v", err)
		}
	}

	log.Printf("[LogWriter] Cleaned up %d old log files (retention: %d days)", deleted, retentionDays)
	return nil
}
