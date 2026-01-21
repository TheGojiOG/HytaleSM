package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/yourusername/hytale-server-manager/internal/config"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

// ScheduleRunner executes scheduled backups
// It polls the database for due schedules
//
type ScheduleRunner struct {
	cfg          *config.Config
	sshPool      *ssh.ConnectionPool
	backupMgr    *BackupManager
	retentionMgr *RetentionManager
	store        *ScheduleStore
	interval     time.Duration
}

func NewScheduleRunner(cfg *config.Config, dbConn *sql.DB, pool *ssh.ConnectionPool) *ScheduleRunner {
	backupMgr := NewBackupManager(dbConn, pool)
	retentionMgr := NewRetentionManager(dbConn, backupMgr)

	return &ScheduleRunner{
		cfg:          cfg,
		sshPool:      pool,
		backupMgr:    backupMgr,
		retentionMgr: retentionMgr,
		store:        NewScheduleStore(dbConn),
		interval:     30 * time.Second,
	}
}

func (sr *ScheduleRunner) Start(ctx context.Context) {
	ticker := time.NewTicker(sr.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[BackupSchedule] Stopping schedule runner")
				return
			case <-ticker.C:
				sr.runDueSchedules()
			}
		}
	}()
}

func (sr *ScheduleRunner) runDueSchedules() {
	now := time.Now()
	schedules, err := sr.store.ListDueSchedules(now)
	if err != nil {
		log.Printf("[BackupSchedule] Failed to list due schedules: %v", err)
		return
	}

	if len(schedules) == 0 {
		return
	}

	for _, schedule := range schedules {
		nextRun, err := computeNextRun(schedule.Schedule, now)
		if err != nil {
			log.Printf("[BackupSchedule] Invalid schedule for server %s: %v", schedule.ServerID, err)
			continue
		}

		if err := sr.store.UpdateRuns(schedule.ID, now, nextRun); err != nil {
			log.Printf("[BackupSchedule] Failed to update run times: %v", err)
		}

		go sr.executeSchedule(schedule)
	}
}

func (sr *ScheduleRunner) executeSchedule(schedule *BackupSchedule) {
	serverDef, err := sr.getServerDefinition(schedule.ServerID)
	if err != nil {
		log.Printf("[BackupSchedule] Failed to load server %s: %v", schedule.ServerID, err)
		return
	}

	if err := sr.ensureSSHConnection(schedule.ServerID, serverDef); err != nil {
		log.Printf("[BackupSchedule] Failed SSH connection for server %s: %v", schedule.ServerID, err)
		return
	}

	directories := schedule.Directories
	if len(directories) == 0 {
		directories = serverDef.Backups.Directories
	}

	if len(directories) == 0 {
		log.Printf("[BackupSchedule] No directories configured for server %s", schedule.ServerID)
		return
	}

	destination := schedule.Destination
	if destination.Type == "" && len(serverDef.Backups.Destinations) > 0 {
		firstDest := serverDef.Backups.Destinations[0]
		destination.Type = firstDest.Type
		destination.Path = firstDest.Path
		destination.S3Endpoint = firstDest.Endpoint
		destination.S3Bucket = firstDest.Bucket
		destination.S3Region = firstDest.Region
	}

	if destination.Type == "" || destination.Path == "" {
		log.Printf("[BackupSchedule] No destination configured for server %s", schedule.ServerID)
		return
	}

	destination.KnownHostsPath = sr.cfg.Security.SSH.KnownHostsPath
	destination.TrustOnFirstUse = sr.cfg.Security.SSH.TrustOnFirstUse

	backupReq := &BackupRequest{
		ServerID:     schedule.ServerID,
		Directories:  directories,
		Exclude:      schedule.Exclude,
		WorkingDir:   serverDef.Server.WorkingDirectory,
		Compression:  schedule.Compression,
		RunAsUser:    schedule.RunAsUser,
		UseSudo:      schedule.UseSudo,
		Destination:  &destination,
		CreatedBy:    "scheduler",
	}

	if _, err := sr.backupMgr.CreateBackup(backupReq); err != nil {
		log.Printf("[BackupSchedule] Backup failed for server %s: %v", schedule.ServerID, err)
		return
	}

	if schedule.RetentionCount > 0 {
		if err := sr.retentionMgr.EnforceRetention(schedule.ServerID, schedule.RetentionCount); err != nil {
			log.Printf("[BackupSchedule] Retention enforcement failed for %s: %v", schedule.ServerID, err)
		}
	}
}

func (sr *ScheduleRunner) getServerDefinition(serverID string) (*config.ServerDefinition, error) {
	servers, err := config.LoadServers(sr.cfg.Storage.ConfigDir)
	if err != nil {
		return nil, err
	}

	for _, server := range servers {
		if server.ID == serverID {
			return &server, nil
		}
	}

	return nil, fmt.Errorf("server not found: %s", serverID)
}

func (sr *ScheduleRunner) ensureSSHConnection(serverID string, serverDef *config.ServerDefinition) error {
	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		KnownHostsPath:  sr.cfg.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: sr.cfg.Security.SSH.TrustOnFirstUse,
	}

	switch serverDef.Connection.AuthMethod {
	case "key":
		sshConfig.KeyPath = serverDef.Connection.KeyPath
	case "password":
		sshConfig.Password = serverDef.Connection.Password
	default:
		return fmt.Errorf("invalid SSH auth method: %s", serverDef.Connection.AuthMethod)
	}

	_, err := sr.sshPool.GetConnection(serverID, sshConfig)
	return err
}

func computeNextRun(schedule string, from time.Time) (time.Time, error) {
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	parsed, err := parser.Parse(schedule)
	if err != nil {
		return time.Time{}, err
	}

	return parsed.Next(from), nil
}
