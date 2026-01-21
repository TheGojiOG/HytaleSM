package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yourusername/hytale-server-manager/internal/api/middleware"
	"github.com/yourusername/hytale-server-manager/internal/auth"
	"github.com/yourusername/hytale-server-manager/internal/backup"
	"github.com/yourusername/hytale-server-manager/internal/config"
	"github.com/yourusername/hytale-server-manager/internal/permissions"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

// BackupHandler handles backup-related HTTP requests
type BackupHandler struct {
	db            *sql.DB
	config        *config.Config
	backupManager *backup.BackupManager
	retentionMgr  *backup.RetentionManager
	scheduleStore *backup.ScheduleStore
	sshPool       *ssh.ConnectionPool
}

type backupScheduleUpsertRequest struct {
	Enabled        bool     `json:"enabled"`
	Schedule       string   `json:"schedule"`
	Directories    []string `json:"directories"`
	Exclude        []string `json:"exclude"`
	RetentionCount int      `json:"retention_count"`
	Destination    struct {
		Type string `json:"type"`
		Path string `json:"path"`

		// SFTP fields
		SFTPHost     string `json:"sftp_host"`
		SFTPPort     int    `json:"sftp_port"`
		SFTPUsername string `json:"sftp_username"`
		SFTPPassword string `json:"sftp_password"`
		SFTPKeyPath  string `json:"sftp_key_path"`

		// S3 fields
		S3Bucket    string `json:"s3_bucket"`
		S3Region    string `json:"s3_region"`
		S3AccessKey string `json:"s3_access_key"`
		S3SecretKey string `json:"s3_secret_key"`
		S3Endpoint  string `json:"s3_endpoint"`
	} `json:"destination"`
	Compression struct {
		Type  string `json:"type"`
		Level int    `json:"level"`
	} `json:"compression"`
	RunAsUser string `json:"run_as_user"`
	UseSudo   bool   `json:"use_sudo"`
}

// NewBackupHandler creates a new backup handler
func NewBackupHandler(cfg *config.Config, db *sql.DB, pool *ssh.ConnectionPool) *BackupHandler {
	backupMgr := backup.NewBackupManager(db, pool)
	retentionMgr := backup.NewRetentionManager(db, backupMgr)
	scheduleStore := backup.NewScheduleStore(db)

	return &BackupHandler{
		db:            db,
		config:        cfg,
		backupManager: backupMgr,
		retentionMgr:  retentionMgr,
		scheduleStore: scheduleStore,
		sshPool:       pool,
	}
}

// RegisterRoutes registers backup routes under the servers group

func (h *BackupHandler) RegisterRoutes(serversGroup *gin.RouterGroup, rbacManager *auth.RBACManager) {
	// These routes are under /servers, so we add /:id/backups
	serversGroup.POST(":id/backups", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsCreate), h.CreateBackup)
	serversGroup.GET(":id/backups", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsList), h.ListBackups)
	serversGroup.GET(":id/backups/:backupId", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsGet), h.GetBackup)
	serversGroup.POST(":id/backups/:backupId/restore", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsRestore), h.RestoreBackup)
	serversGroup.DELETE(":id/backups/:backupId", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsDelete), h.DeleteBackup)
	serversGroup.POST(":id/backups/retention/enforce", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsRetentionEnforce), h.EnforceRetention)
	serversGroup.GET(":id/backups/schedule", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsList), h.GetBackupSchedule)
	serversGroup.PUT(":id/backups/schedule", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsCreate), h.UpsertBackupSchedule)
	serversGroup.POST(":id/backups/schedule/default", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsCreate), h.InitializeDefaultBackupSchedule)
	serversGroup.DELETE(":id/backups/schedule", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsDelete), h.DeleteBackupSchedule)
	serversGroup.GET(":id/backups/cron", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsList), h.GetBackupCron)
	serversGroup.GET(":id/backups/schedules", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsList), h.ListBackupSchedules)
	serversGroup.POST(":id/backups/schedules", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsCreate), h.CreateBackupSchedule)
	serversGroup.PUT(":id/backups/schedules/:scheduleId", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsCreate), h.UpdateBackupSchedule)
	serversGroup.DELETE(":id/backups/schedules/:scheduleId", middleware.RequireServerPermission(rbacManager, permissions.ServersBackupsDelete), h.DeleteBackupScheduleByID)
}

// CreateBackup creates a new backup
// POST /api/v1/servers/:id/backups
func (h *BackupHandler) CreateBackup(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	var req struct {
		Directories []string `json:"directories" binding:"required"`
		Exclude     []string `json:"exclude"`
		WorkingDir  string   `json:"working_dir" binding:"required"`
		Destination struct {
			Type string `json:"type" binding:"required,oneof=local sftp s3"`
			Path string `json:"path" binding:"required"`

			// SFTP fields
			SFTPHost     string `json:"sftp_host"`
			SFTPPort     int    `json:"sftp_port"`
			SFTPUsername string `json:"sftp_username"`
			SFTPPassword string `json:"sftp_password"`
			SFTPKeyPath  string `json:"sftp_key_path"`

			// S3 fields
			S3Bucket    string `json:"s3_bucket"`
			S3Region    string `json:"s3_region"`
			S3AccessKey string `json:"s3_access_key"`
			S3SecretKey string `json:"s3_secret_key"`
			S3Endpoint  string `json:"s3_endpoint"`
		} `json:"destination" binding:"required"`
		Compression struct {
			Type  string `json:"type"`
			Level int    `json:"level"`
		} `json:"compression"`
		RunAsUser string `json:"run_as_user"`
		UseSudo   bool   `json:"use_sudo"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify server ownership and get server config
	servers, err := config.LoadServers(h.config.Storage.ConfigDir)
	if err != nil {
		log.Printf("[API] Failed to load servers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load servers"})
		return
	}

	var serverDef *config.ServerDefinition
	for _, s := range servers {
		if s.ID == serverID {
			serverDef = &s
			break
		}
	}

	if serverDef == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	// Create SSH connection if it doesn't exist
	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	log.Printf("[API] Server %s connection config: host=%s, port=%d, user=%s, authMethod=%s",
		serverID, serverDef.Connection.Host, serverDef.Connection.Port,
		serverDef.Connection.Username, serverDef.Connection.AuthMethod)

	switch serverDef.Connection.AuthMethod {
	case "key":
		sshConfig.KeyPath = serverDef.Connection.KeyPath
		log.Printf("[API] Using SSH key auth: %s", sshConfig.KeyPath)
	case "password":
		sshConfig.Password = serverDef.Connection.Password
		log.Printf("[API] Using SSH password auth")
	default:
		log.Printf("[API] Invalid auth method: '%s'", serverDef.Connection.AuthMethod)
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid SSH auth method: '%s'", serverDef.Connection.AuthMethod)})
		return
	}

	_, err = h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		log.Printf("[API] Failed to create SSH connection: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create SSH connection", "details": err.Error()})
		return
	}

	log.Printf("[API] Successfully created/retrieved SSH connection for server %s", serverID)

	// Create destination config
	destConfig := &backup.DestinationConfig{
		Type:            req.Destination.Type,
		Path:            req.Destination.Path,
		SFTPHost:        req.Destination.SFTPHost,
		SFTPPort:        req.Destination.SFTPPort,
		SFTPUsername:    req.Destination.SFTPUsername,
		SFTPPassword:    req.Destination.SFTPPassword,
		SFTPKeyPath:     req.Destination.SFTPKeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
		S3Bucket:        req.Destination.S3Bucket,
		S3Region:        req.Destination.S3Region,
		S3AccessKey:     req.Destination.S3AccessKey,
		S3SecretKey:     req.Destination.S3SecretKey,
		S3Endpoint:      req.Destination.S3Endpoint,
	}

	// Create backup request
	backupReq := &backup.BackupRequest{
		ServerID:    serverID,
		Directories: req.Directories,
		Exclude:     req.Exclude,
		WorkingDir:  req.WorkingDir,
		Compression: backup.CompressionConfig{Type: req.Compression.Type, Level: req.Compression.Level},
		RunAsUser:   req.RunAsUser,
		UseSudo:     req.UseSudo,
		Destination: destConfig,
		CreatedBy:   user.Username,
	}

	// Create backup (this may take a while)
	record, err := h.backupManager.CreateBackup(backupReq)
	if err != nil {
		log.Printf("[API] Failed to create backup: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create backup", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Backup created successfully",
		"backup":  record,
	})
}

// ListBackups lists all backups for a server
// GET /api/v1/servers/:serverId/backups
func (h *BackupHandler) ListBackups(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	// Verify server ownership
	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	backups, err := h.backupManager.ListBackups(serverID)
	if err != nil {
		log.Printf("[API] Failed to list backups: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list backups"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"backups": backups,
		"count":   len(backups),
	})
}

// GetBackup retrieves a specific backup
// GET /api/v1/servers/:serverId/backups/:backupId
func (h *BackupHandler) GetBackup(c *gin.Context) {
	serverID := c.Param("id")
	backupID := c.Param("backupId")
	user := c.MustGet("user").(*auth.Claims)

	// Verify server ownership
	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	backup, err := h.backupManager.GetBackup(backupID)
	if err != nil {
		log.Printf("[API] Failed to get backup: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Backup not found"})
		return
	}

	if backup.ServerID != serverID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Backup does not belong to this server"})
		return
	}

	c.JSON(http.StatusOK, backup)
}

// RestoreBackup restores a backup to the server
// POST /api/v1/servers/:serverId/backups/:backupId/restore
func (h *BackupHandler) RestoreBackup(c *gin.Context) {
	serverID := c.Param("id")
	backupID := c.Param("backupId")
	user := c.MustGet("user").(*auth.Claims)

	var req struct {
		Destination string `json:"destination" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify server ownership
	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	// Restore backup
	if err := h.backupManager.RestoreBackup(backupID, serverID, req.Destination); err != nil {
		log.Printf("[API] Failed to restore backup: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to restore backup", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Backup restored successfully",
		"backup_id":   backupID,
		"destination": req.Destination,
	})
}

// DeleteBackup deletes a backup
// DELETE /api/v1/servers/:serverId/backups/:backupId
func (h *BackupHandler) DeleteBackup(c *gin.Context) {
	serverID := c.Param("id")
	backupID := c.Param("backupId")
	user := c.MustGet("user").(*auth.Claims)

	// Verify server ownership
	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	// Verify backup belongs to server
	backup, err := h.backupManager.GetBackup(backupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backup not found"})
		return
	}

	if backup.ServerID != serverID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Backup does not belong to this server"})
		return
	}

	// Delete backup
	if err := h.backupManager.DeleteBackup(backupID); err != nil {
		log.Printf("[API] Failed to delete backup: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete backup"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Backup deleted successfully",
		"backup_id": backupID,
	})
}

// EnforceRetention manually enforces retention policy for a server
// POST /api/v1/servers/:serverId/backups/retention/enforce
func (h *BackupHandler) EnforceRetention(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	var req struct {
		RetentionCount int `json:"retention_count" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify server ownership
	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	// Get stats before enforcement
	statsBefore, _ := h.retentionMgr.GetRetentionStats(serverID, req.RetentionCount)

	// Enforce retention
	if err := h.retentionMgr.EnforceRetention(serverID, req.RetentionCount); err != nil {
		log.Printf("[API] Failed to enforce retention: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enforce retention"})
		return
	}

	// Get stats after enforcement
	statsAfter, _ := h.retentionMgr.GetRetentionStats(serverID, req.RetentionCount)

	c.JSON(http.StatusOK, gin.H{
		"message":      "Retention policy enforced successfully",
		"stats_before": statsBefore,
		"stats_after":  statsAfter,
	})
}

// GetBackupSchedule returns the backup schedule for a server
// GET /api/v1/servers/:id/backups/schedule
func (h *BackupHandler) GetBackupSchedule(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	// Verify server ownership
	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	schedule, err := h.scheduleStore.GetSchedule(serverID)
	if err == nil {
		c.JSON(http.StatusOK, schedule)
		return
	}

	if err != sql.ErrNoRows {
		log.Printf("[API] Failed to get backup schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load backup schedule"})
		return
	}

	c.Status(http.StatusNoContent)
}

// ListBackupSchedules returns all schedules for a server
// GET /api/v1/servers/:id/backups/schedules
func (h *BackupHandler) ListBackupSchedules(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	schedules, err := h.scheduleStore.ListSchedules(serverID)
	if err != nil {
		log.Printf("[API] Failed to list schedules: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load schedules"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"schedules": schedules})
}

// CreateBackupSchedule creates a new schedule for a server
// POST /api/v1/servers/:id/backups/schedules
func (h *BackupHandler) CreateBackupSchedule(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	var req backupScheduleUpsertRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	if req.Enabled {
		if req.Schedule == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "schedule is required when enabled"})
			return
		}
		if len(req.Directories) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "directories are required when enabled"})
			return
		}
		if req.Destination.Type == "" || req.Destination.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "destination type and path are required"})
			return
		}
	}

	schedule := h.buildScheduleFromRequest(serverID, req)

	if err := h.scheduleStore.UpsertSchedule(schedule); err != nil {
		log.Printf("[API] Failed to create schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save schedule"})
		return
	}

	if serverDef, err := h.GetServerDefinitionFromConfig(serverID); err == nil {
		if err := backup.InstallCronJob(h.config, h.sshPool, serverDef, schedule); err != nil {
			log.Printf("[API] Warning: Failed to install cron job: %v", err)
		}
	}

	c.JSON(http.StatusCreated, schedule)
}

// UpdateBackupSchedule updates an existing schedule
// PUT /api/v1/servers/:id/backups/schedules/:scheduleId
func (h *BackupHandler) UpdateBackupSchedule(c *gin.Context) {
	serverID := c.Param("id")
	scheduleID := c.Param("scheduleId")
	user := c.MustGet("user").(*auth.Claims)

	var req backupScheduleUpsertRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	schedule := h.buildScheduleFromRequest(serverID, req)
	schedule.ID = scheduleID

	if err := h.scheduleStore.UpsertSchedule(schedule); err != nil {
		log.Printf("[API] Failed to update schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save schedule"})
		return
	}

	if serverDef, err := h.GetServerDefinitionFromConfig(serverID); err == nil {
		if err := backup.InstallCronJob(h.config, h.sshPool, serverDef, schedule); err != nil {
			log.Printf("[API] Warning: Failed to install cron job: %v", err)
		}
	}

	c.JSON(http.StatusOK, schedule)
}

// DeleteBackupScheduleByID deletes a schedule by ID and removes its cron job
// DELETE /api/v1/servers/:id/backups/schedules/:scheduleId
func (h *BackupHandler) DeleteBackupScheduleByID(c *gin.Context) {
	serverID := c.Param("id")
	scheduleID := c.Param("scheduleId")
	user := c.MustGet("user").(*auth.Claims)

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	schedule, _ := h.scheduleStore.GetScheduleByID(serverID, scheduleID)
	serverDef, err := h.GetServerDefinitionFromConfig(serverID)
	if err == nil {
		if err := backup.RemoveCronJob(h.config, h.sshPool, serverDef, schedule); err != nil {
			log.Printf("[API] Warning: Failed to remove cron job: %v", err)
		}
	}

	if err := h.scheduleStore.DeleteScheduleByID(serverID, scheduleID); err != nil {
		log.Printf("[API] Failed to delete schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete schedule"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Schedule deleted"})
}

// UpsertBackupSchedule creates or updates the backup schedule for a server
// PUT /api/v1/servers/:id/backups/schedule
func (h *BackupHandler) UpsertBackupSchedule(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	var req backupScheduleUpsertRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	if req.Enabled {
		if req.Schedule == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "schedule is required when enabled"})
			return
		}
		if len(req.Directories) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "directories are required when enabled"})
			return
		}
		if req.Destination.Type == "" || req.Destination.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "destination type and path are required"})
			return
		}
	}

	schedule := h.buildScheduleFromRequest(serverID, req)

	if err := h.scheduleStore.UpsertSchedule(schedule); err != nil {
		log.Printf("[API] Failed to upsert backup schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save backup schedule"})
		return
	}

	if serverDef, err := h.GetServerDefinitionFromConfig(serverID); err == nil {
		if err := backup.InstallCronJob(h.config, h.sshPool, serverDef, schedule); err != nil {
			log.Printf("[API] Warning: Failed to install cron job: %v", err)
		}
	}

	// Update YAML server backups for visibility
	if err := h.updateServerBackupConfig(serverID, req); err != nil {
		log.Printf("[API] Warning: Failed to update server backup config: %v", err)
	}

	updated, err := h.scheduleStore.GetSchedule(serverID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "Backup schedule saved"})
		return
	}

	c.JSON(http.StatusOK, updated)
}

// InitializeDefaultBackupSchedule creates the default nightly backup schedule for a server
// POST /api/v1/servers/:id/backups/schedule/default
func (h *BackupHandler) InitializeDefaultBackupSchedule(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	serverDef, err := h.GetServerDefinitionFromConfig(serverID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	existing, err := h.scheduleStore.ListSchedules(serverID)
	if err == nil && len(existing) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Schedule already exists"})
		return
	}

	defaultSchedule, err := backup.BuildDefaultSchedule(serverDef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.scheduleStore.UpsertSchedule(defaultSchedule); err != nil {
		log.Printf("[API] Failed to save default schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save default schedule"})
		return
	}

	if err := backup.InstallCronJob(h.config, h.sshPool, serverDef, defaultSchedule); err != nil {
		log.Printf("[API] Warning: Failed to install cron job: %v", err)
	}

	_ = h.updateServerBackupConfig(serverID, backupScheduleUpsertRequest{
		Enabled:        defaultSchedule.Enabled,
		Schedule:       defaultSchedule.Schedule,
		Directories:    defaultSchedule.Directories,
		Exclude:        defaultSchedule.Exclude,
		RetentionCount: defaultSchedule.RetentionCount,
		RunAsUser:      defaultSchedule.RunAsUser,
		UseSudo:        defaultSchedule.UseSudo,
		Destination: struct {
			Type string `json:"type"`
			Path string `json:"path"`
			SFTPHost string `json:"sftp_host"`
			SFTPPort int `json:"sftp_port"`
			SFTPUsername string `json:"sftp_username"`
			SFTPPassword string `json:"sftp_password"`
			SFTPKeyPath string `json:"sftp_key_path"`
			S3Bucket string `json:"s3_bucket"`
			S3Region string `json:"s3_region"`
			S3AccessKey string `json:"s3_access_key"`
			S3SecretKey string `json:"s3_secret_key"`
			S3Endpoint string `json:"s3_endpoint"`
		}{
			Type: defaultSchedule.Destination.Type,
			Path: defaultSchedule.Destination.Path,
		},
		Compression: struct {
			Type  string `json:"type"`
			Level int    `json:"level"`
		}{
			Type:  defaultSchedule.Compression.Type,
			Level: defaultSchedule.Compression.Level,
		},
	})

	c.JSON(http.StatusOK, defaultSchedule)
}

// DeleteBackupSchedule deletes a backup schedule and removes its cron job
// DELETE /api/v1/servers/:id/backups/schedule
func (h *BackupHandler) DeleteBackupSchedule(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	schedule, _ := h.scheduleStore.GetSchedule(serverID)
	serverDef, err := h.GetServerDefinitionFromConfig(serverID)
	if err == nil {
		if err := backup.RemoveCronJob(h.config, h.sshPool, serverDef, schedule); err != nil {
			log.Printf("[API] Warning: Failed to remove cron job: %v", err)
		}
	}

	if err := h.scheduleStore.DeleteSchedule(serverID); err != nil {
		log.Printf("[API] Failed to delete schedule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete schedule"})
		return
	}

	_ = h.updateServerBackupConfig(serverID, backupScheduleUpsertRequest{})

	c.JSON(http.StatusOK, gin.H{"message": "Schedule deleted"})
}

// GetBackupCron returns the current crontab for the service user
// GET /api/v1/servers/:id/backups/cron
func (h *BackupHandler) GetBackupCron(c *gin.Context) {
	serverID := c.Param("id")
	user := c.MustGet("user").(*auth.Claims)

	if !h.verifyServerOwnership(c, serverID, fmt.Sprintf("%d", user.UserID)) {
		return
	}

	serverDef, err := h.GetServerDefinitionFromConfig(serverID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	runAsUser := strings.TrimSpace(serverDef.Dependencies.ServiceUser)
	useSudo := serverDef.Dependencies.UseSudo || runAsUser != ""

	output, err := backup.ReadCronTab(h.config, h.sshPool, serverDef, runAsUser, useSudo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read cron", "details": err.Error()})
		return
	}

	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}

	c.JSON(http.StatusOK, gin.H{
		"user":  runAsUser,
		"lines": lines,
		"raw":   output,
	})
}

func (h *BackupHandler) buildScheduleFromRequest(serverID string, req backupScheduleUpsertRequest) *backup.BackupSchedule {
	destConfig := backup.DestinationConfig{
		Type:         req.Destination.Type,
		Path:         req.Destination.Path,
		SFTPHost:     req.Destination.SFTPHost,
		SFTPPort:     req.Destination.SFTPPort,
		SFTPUsername: req.Destination.SFTPUsername,
		SFTPPassword: req.Destination.SFTPPassword,
		SFTPKeyPath:  req.Destination.SFTPKeyPath,
		S3Bucket:     req.Destination.S3Bucket,
		S3Region:     req.Destination.S3Region,
		S3AccessKey:  req.Destination.S3AccessKey,
		S3SecretKey:  req.Destination.S3SecretKey,
		S3Endpoint:   req.Destination.S3Endpoint,
	}

	return &backup.BackupSchedule{
		ServerID:       serverID,
		Enabled:        req.Enabled,
		Schedule:       req.Schedule,
		Directories:    req.Directories,
		Exclude:        req.Exclude,
		RetentionCount: req.RetentionCount,
		Destination:    destConfig,
		Compression:    backup.CompressionConfig{Type: req.Compression.Type, Level: req.Compression.Level},
		RunAsUser:      req.RunAsUser,
		UseSudo:        req.UseSudo || req.RunAsUser != "",
	}
}

func (h *BackupHandler) updateServerBackupConfig(serverID string, req backupScheduleUpsertRequest) error {
	servers, err := config.LoadServers(h.config.Storage.ConfigDir)
	if err != nil {
		return err
	}

	updated := false
	for i, server := range servers {
		if server.ID != serverID {
			continue
		}

		servers[i].Backups.Enabled = req.Enabled
		servers[i].Backups.Schedule = req.Schedule
		servers[i].Backups.Directories = req.Directories
		servers[i].Backups.Retention.Count = req.RetentionCount

		if req.Destination.Type != "" {
			servers[i].Backups.Destinations = []config.BackupDestination{
				{
					Type:     req.Destination.Type,
					Path:     req.Destination.Path,
					Endpoint: req.Destination.S3Endpoint,
					Bucket:   req.Destination.S3Bucket,
					Region:   req.Destination.S3Region,
				},
			}
		}

		updated = true
		break
	}

	if !updated {
		return fmt.Errorf("server not found: %s", serverID)
	}

	return config.SaveServers(h.config.Storage.ConfigDir, servers)
}

func (h *BackupHandler) GetServerDefinitionFromConfig(serverID string) (*config.ServerDefinition, error) {
	servers, err := config.LoadServers(h.config.Storage.ConfigDir)
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

// verifyServerOwnership checks if a server exists (servers are in YAML, not database)
func (h *BackupHandler) verifyServerOwnership(c *gin.Context, serverID, userID string) bool {
	// Load servers from YAML config
	servers, err := config.LoadServers(h.config.Storage.ConfigDir)
	if err != nil {
		log.Printf("[API] Failed to load servers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load servers"})
		return false
	}

	// Check if server exists
	for _, server := range servers {
		if server.ID == serverID {
			// Server exists - in this implementation, we trust RBAC middleware for permissions
			return true
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
	return false
}

// GetServerDefinition retrieves a server definition (helper method)
func (h *BackupHandler) GetServerDefinition(serverID string) (*config.ServerDefinition, error) {
	var defJSON string
	err := h.db.QueryRow("SELECT definition FROM servers WHERE id = ?", serverID).Scan(&defJSON)
	if err != nil {
		return nil, err
	}

	var def config.ServerDefinition
	if err := json.Unmarshal([]byte(defJSON), &def); err != nil {
		return nil, err
	}

	return &def, nil
}
