package backup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BackupSchedule represents a scheduled backup configuration
// JSON tags used for API responses
// Destination config includes only what the schedule needs to run
// Compression defaults to gzip level 6
// Times are in server local time
//
type BackupSchedule struct {
	ID             string             `json:"id"`
	ServerID       string             `json:"server_id"`
	Enabled        bool               `json:"enabled"`
	Schedule       string             `json:"schedule"`
	Directories    []string           `json:"directories"`
	Exclude        []string           `json:"exclude"`
	Destination    DestinationConfig  `json:"destination"`
	RetentionCount int                `json:"retention_count"`
	Compression    CompressionConfig  `json:"compression"`
	RunAsUser      string             `json:"run_as_user"`
	UseSudo        bool               `json:"use_sudo"`
	LastRun        *time.Time         `json:"last_run,omitempty"`
	NextRun        *time.Time         `json:"next_run,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

// ScheduleStore provides CRUD for backup schedules
// Multiple schedules per server
//
type ScheduleStore struct {
	db *sql.DB
}

func NewScheduleStore(db *sql.DB) *ScheduleStore {
	return &ScheduleStore{db: db}
}

func (s *ScheduleStore) GetSchedule(serverID string) (*BackupSchedule, error) {
	query := `
		SELECT id, server_id, enabled, schedule, directories, exclude, destination_type,
		       destination_path, destination_config, retention_count, compression_type,
		       compression_level, run_as_user, use_sudo, last_run, next_run, created_at, updated_at
		FROM backup_schedules
		WHERE server_id = ?
		LIMIT 1
	`

	var (
		id              string
		srvID           string
		enabled         bool
		schedule        string
		directoriesJSON string
		excludeJSON     sql.NullString
		destType        string
		destPath        string
		destConfigJSON  sql.NullString
		retentionCount  int
		compType        sql.NullString
		compLevel       sql.NullInt64
		runAsUser       sql.NullString
		useSudo         sql.NullBool
		lastRun         sql.NullTime
		nextRun         sql.NullTime
		createdAt       time.Time
		updatedAt       time.Time
	)

	if err := s.db.QueryRow(query, serverID).Scan(
		&id,
		&srvID,
		&enabled,
		&schedule,
		&directoriesJSON,
		&excludeJSON,
		&destType,
		&destPath,
		&destConfigJSON,
		&retentionCount,
		&compType,
		&compLevel,
		&runAsUser,
		&useSudo,
		&lastRun,
		&nextRun,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	var directories []string
	if err := json.Unmarshal([]byte(directoriesJSON), &directories); err != nil {
		return nil, fmt.Errorf("failed to parse directories: %w", err)
	}

	var exclude []string
	if excludeJSON.Valid {
		if err := json.Unmarshal([]byte(excludeJSON.String), &exclude); err != nil {
			return nil, fmt.Errorf("failed to parse exclude: %w", err)
		}
	}

	destConfig := DestinationConfig{Type: destType, Path: destPath}
	if destConfigJSON.Valid && destConfigJSON.String != "" {
		if err := json.Unmarshal([]byte(destConfigJSON.String), &destConfig); err != nil {
			return nil, fmt.Errorf("failed to parse destination config: %w", err)
		}
	}

	compression := normalizeCompression(CompressionConfig{Type: compType.String, Level: int(compLevel.Int64)})

	var lastRunPtr *time.Time
	if lastRun.Valid {
		lastRunPtr = &lastRun.Time
	}
	var nextRunPtr *time.Time
	if nextRun.Valid {
		nextRunPtr = &nextRun.Time
	}

	return &BackupSchedule{
		ID:             id,
		ServerID:       srvID,
		Enabled:        enabled,
		Schedule:       schedule,
		Directories:    directories,
		Exclude:        exclude,
		Destination:    destConfig,
		RetentionCount: retentionCount,
		Compression:    compression,
		RunAsUser:      runAsUser.String,
		UseSudo:        useSudo.Bool,
		LastRun:        lastRunPtr,
		NextRun:        nextRunPtr,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

func (s *ScheduleStore) GetScheduleByID(serverID, scheduleID string) (*BackupSchedule, error) {
	query := `
		SELECT id, server_id, enabled, schedule, directories, exclude, destination_type,
		       destination_path, destination_config, retention_count, compression_type,
		       compression_level, run_as_user, use_sudo, last_run, next_run, created_at, updated_at
		FROM backup_schedules
		WHERE server_id = ? AND id = ?
		LIMIT 1
	`

	var (
		id              string
		srvID           string
		enabled         bool
		schedule        string
		directoriesJSON string
		excludeJSON     sql.NullString
		destType        string
		destPath        string
		destConfigJSON  sql.NullString
		retentionCount  int
		compType        sql.NullString
		compLevel       sql.NullInt64
		runAsUser       sql.NullString
		useSudo         sql.NullBool
		lastRun         sql.NullTime
		nextRun         sql.NullTime
		createdAt       time.Time
		updatedAt       time.Time
	)

	if err := s.db.QueryRow(query, serverID, scheduleID).Scan(
		&id,
		&srvID,
		&enabled,
		&schedule,
		&directoriesJSON,
		&excludeJSON,
		&destType,
		&destPath,
		&destConfigJSON,
		&retentionCount,
		&compType,
		&compLevel,
		&runAsUser,
		&useSudo,
		&lastRun,
		&nextRun,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	var directories []string
	if err := json.Unmarshal([]byte(directoriesJSON), &directories); err != nil {
		return nil, fmt.Errorf("failed to parse directories: %w", err)
	}

	var exclude []string
	if excludeJSON.Valid {
		if err := json.Unmarshal([]byte(excludeJSON.String), &exclude); err != nil {
			return nil, fmt.Errorf("failed to parse exclude: %w", err)
		}
	}

	destConfig := DestinationConfig{Type: destType, Path: destPath}
	if destConfigJSON.Valid && destConfigJSON.String != "" {
		if err := json.Unmarshal([]byte(destConfigJSON.String), &destConfig); err != nil {
			return nil, fmt.Errorf("failed to parse destination config: %w", err)
		}
	}

	compression := normalizeCompression(CompressionConfig{Type: compType.String, Level: int(compLevel.Int64)})

	var lastRunPtr *time.Time
	if lastRun.Valid {
		lastRunPtr = &lastRun.Time
	}
	var nextRunPtr *time.Time
	if nextRun.Valid {
		nextRunPtr = &nextRun.Time
	}

	return &BackupSchedule{
		ID:             id,
		ServerID:       srvID,
		Enabled:        enabled,
		Schedule:       schedule,
		Directories:    directories,
		Exclude:        exclude,
		Destination:    destConfig,
		RetentionCount: retentionCount,
		Compression:    compression,
		RunAsUser:      runAsUser.String,
		UseSudo:        useSudo.Bool,
		LastRun:        lastRunPtr,
		NextRun:        nextRunPtr,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

func (s *ScheduleStore) ListSchedules(serverID string) ([]*BackupSchedule, error) {
	query := `
		SELECT id, server_id, enabled, schedule, directories, exclude, destination_type,
		       destination_path, destination_config, retention_count, compression_type,
		       compression_level, run_as_user, use_sudo, last_run, next_run, created_at, updated_at
		FROM backup_schedules
		WHERE server_id = ?
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(query, serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to query schedules: %w", err)
	}
	defer rows.Close()

	var schedules []*BackupSchedule
	for rows.Next() {
		var (
			id              string
			srvID           string
			enabled         bool
			schedule        string
			directoriesJSON string
			excludeJSON     sql.NullString
			destType        string
			destPath        string
			destConfigJSON  sql.NullString
			retentionCount  int
			compType        sql.NullString
			compLevel       sql.NullInt64
			runAsUser       sql.NullString
			useSudo         sql.NullBool
			lastRun         sql.NullTime
			nextRun         sql.NullTime
			createdAt       time.Time
			updatedAt       time.Time
		)

		if err := rows.Scan(
			&id,
			&srvID,
			&enabled,
			&schedule,
			&directoriesJSON,
			&excludeJSON,
			&destType,
			&destPath,
			&destConfigJSON,
			&retentionCount,
			&compType,
			&compLevel,
			&runAsUser,
			&useSudo,
			&lastRun,
			&nextRun,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan schedule row: %w", err)
		}

		var directories []string
		if err := json.Unmarshal([]byte(directoriesJSON), &directories); err != nil {
			return nil, fmt.Errorf("failed to parse directories: %w", err)
		}

		var exclude []string
		if excludeJSON.Valid {
			if err := json.Unmarshal([]byte(excludeJSON.String), &exclude); err != nil {
				return nil, fmt.Errorf("failed to parse exclude: %w", err)
			}
		}

		destConfig := DestinationConfig{Type: destType, Path: destPath}
		if destConfigJSON.Valid && destConfigJSON.String != "" {
			if err := json.Unmarshal([]byte(destConfigJSON.String), &destConfig); err != nil {
				return nil, fmt.Errorf("failed to parse destination config: %w", err)
			}
		}

		compression := normalizeCompression(CompressionConfig{Type: compType.String, Level: int(compLevel.Int64)})

		var lastRunPtr *time.Time
		if lastRun.Valid {
			lastRunPtr = &lastRun.Time
		}
		var nextRunPtr *time.Time
		if nextRun.Valid {
			nextRunPtr = &nextRun.Time
		}

		schedules = append(schedules, &BackupSchedule{
			ID:             id,
			ServerID:       srvID,
			Enabled:        enabled,
			Schedule:       schedule,
			Directories:    directories,
			Exclude:        exclude,
			Destination:    destConfig,
			RetentionCount: retentionCount,
			Compression:    compression,
			RunAsUser:      runAsUser.String,
			UseSudo:        useSudo.Bool,
			LastRun:        lastRunPtr,
			NextRun:        nextRunPtr,
			CreatedAt:      createdAt,
			UpdatedAt:      updatedAt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read schedules: %w", err)
	}

	return schedules, nil
}

func (s *ScheduleStore) UpsertSchedule(schedule *BackupSchedule) error {
	if schedule.ID == "" {
		schedule.ID = "backup-schedule-" + uuid.New().String()[:8]
	}

	if schedule.ServerID == "" {
		return fmt.Errorf("server_id is required")
	}

	directoriesJSON, err := json.Marshal(schedule.Directories)
	if err != nil {
		return fmt.Errorf("failed to marshal directories: %w", err)
	}

	excludeJSON, err := json.Marshal(schedule.Exclude)
	if err != nil {
		return fmt.Errorf("failed to marshal exclude: %w", err)
	}

	destConfigJSON, err := json.Marshal(schedule.Destination)
	if err != nil {
		return fmt.Errorf("failed to marshal destination config: %w", err)
	}

	compression := normalizeCompression(schedule.Compression)
	if schedule.Enabled {
		if schedule.Schedule == "" {
			return fmt.Errorf("schedule is required when enabled")
		}

		nextRun, err := computeNextRun(schedule.Schedule, time.Now())
		if err != nil {
			return fmt.Errorf("invalid schedule: %w", err)
		}
		schedule.NextRun = &nextRun
	}

	query := `
		INSERT INTO backup_schedules (
			id, server_id, enabled, schedule, directories, exclude, destination_type,
			destination_path, destination_config, retention_count, compression_type,
			compression_level, run_as_user, use_sudo, last_run, next_run, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			schedule = excluded.schedule,
			directories = excluded.directories,
			exclude = excluded.exclude,
			destination_type = excluded.destination_type,
			destination_path = excluded.destination_path,
			destination_config = excluded.destination_config,
			retention_count = excluded.retention_count,
			compression_type = excluded.compression_type,
			compression_level = excluded.compression_level,
			run_as_user = excluded.run_as_user,
			use_sudo = excluded.use_sudo,
			last_run = excluded.last_run,
			next_run = excluded.next_run,
			updated_at = datetime('now')
	`

	_, err = s.db.Exec(query,
		schedule.ID,
		schedule.ServerID,
		schedule.Enabled,
		schedule.Schedule,
		string(directoriesJSON),
		string(excludeJSON),
		schedule.Destination.Type,
		schedule.Destination.Path,
		string(destConfigJSON),
		schedule.RetentionCount,
		compression.Type,
		compression.Level,
		schedule.RunAsUser,
		schedule.UseSudo,
		schedule.LastRun,
		schedule.NextRun,
	)

	if err != nil {
		return fmt.Errorf("failed to upsert backup schedule: %w", err)
	}

	return nil
}

func (s *ScheduleStore) UpdateRuns(id string, lastRun, nextRun time.Time) error {
	query := `
		UPDATE backup_schedules
		SET last_run = ?, next_run = ?, updated_at = datetime('now')
		WHERE id = ?
	`

	_, err := s.db.Exec(query, lastRun, nextRun, id)
	if err != nil {
		return fmt.Errorf("failed to update schedule runs: %w", err)
	}

	return nil
}

func (s *ScheduleStore) DeleteSchedule(serverID string) error {
	_, err := s.db.Exec("DELETE FROM backup_schedules WHERE server_id = ?", serverID)
	if err != nil {
		return fmt.Errorf("failed to delete schedule: %w", err)
	}
	return nil
}

func (s *ScheduleStore) DeleteScheduleByID(serverID, scheduleID string) error {
	_, err := s.db.Exec("DELETE FROM backup_schedules WHERE server_id = ? AND id = ?", serverID, scheduleID)
	if err != nil {
		return fmt.Errorf("failed to delete schedule: %w", err)
	}
	return nil
}

func (s *ScheduleStore) ListDueSchedules(now time.Time) ([]*BackupSchedule, error) {
	query := `
		SELECT id, server_id, enabled, schedule, directories, exclude, destination_type,
		       destination_path, destination_config, retention_count, compression_type,
		       compression_level, run_as_user, use_sudo, last_run, next_run, created_at, updated_at
		FROM backup_schedules
		WHERE enabled = true
		  AND schedule != ''
		  AND (next_run IS NULL OR next_run <= ?)
	`

	rows, err := s.db.Query(query, now)
	if err != nil {
		return nil, fmt.Errorf("failed to query due schedules: %w", err)
	}
	defer rows.Close()

	var schedules []*BackupSchedule
	for rows.Next() {
		var (
			id              string
			srvID           string
			enabled         bool
			schedule        string
			directoriesJSON string
			excludeJSON     sql.NullString
			destType        string
			destPath        string
			destConfigJSON  sql.NullString
			retentionCount  int
			compType        sql.NullString
			compLevel       sql.NullInt64
			runAsUser       sql.NullString
			useSudo         sql.NullBool
			lastRun         sql.NullTime
			nextRun         sql.NullTime
			createdAt       time.Time
			updatedAt       time.Time
		)

		if err := rows.Scan(
			&id,
			&srvID,
			&enabled,
			&schedule,
			&directoriesJSON,
			&excludeJSON,
			&destType,
			&destPath,
			&destConfigJSON,
			&retentionCount,
			&compType,
			&compLevel,
			&runAsUser,
			&useSudo,
			&lastRun,
			&nextRun,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan schedule row: %w", err)
		}

		var directories []string
		if err := json.Unmarshal([]byte(directoriesJSON), &directories); err != nil {
			return nil, fmt.Errorf("failed to parse directories: %w", err)
		}

		var exclude []string
		if excludeJSON.Valid {
			if err := json.Unmarshal([]byte(excludeJSON.String), &exclude); err != nil {
				return nil, fmt.Errorf("failed to parse exclude: %w", err)
			}
		}

		destConfig := DestinationConfig{Type: destType, Path: destPath}
		if destConfigJSON.Valid && destConfigJSON.String != "" {
			if err := json.Unmarshal([]byte(destConfigJSON.String), &destConfig); err != nil {
				return nil, fmt.Errorf("failed to parse destination config: %w", err)
			}
		}

		compression := normalizeCompression(CompressionConfig{Type: compType.String, Level: int(compLevel.Int64)})

		var lastRunPtr *time.Time
		if lastRun.Valid {
			lastRunPtr = &lastRun.Time
		}
		var nextRunPtr *time.Time
		if nextRun.Valid {
			nextRunPtr = &nextRun.Time
		}

		schedules = append(schedules, &BackupSchedule{
			ID:             id,
			ServerID:       srvID,
			Enabled:        enabled,
			Schedule:       schedule,
			Directories:    directories,
			Exclude:        exclude,
			Destination:    destConfig,
			RetentionCount: retentionCount,
			Compression:    compression,
			RunAsUser:      runAsUser.String,
			UseSudo:        useSudo.Bool,
			LastRun:        lastRunPtr,
			NextRun:        nextRunPtr,
			CreatedAt:      createdAt,
			UpdatedAt:      updatedAt,
		})
		}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read schedules: %w", err)
	}

	return schedules, nil
}
