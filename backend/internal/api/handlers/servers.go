package handlers

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/sftp"
	"github.com/yourusername/hytale-server-manager/internal/agentcert"
	"github.com/yourusername/hytale-server-manager/internal/auth"
	"github.com/yourusername/hytale-server-manager/internal/config"
	crypto "github.com/yourusername/hytale-server-manager/internal/crypto"
	"github.com/yourusername/hytale-server-manager/internal/database"
	"github.com/yourusername/hytale-server-manager/internal/logging"
	"github.com/yourusername/hytale-server-manager/internal/models"
	"github.com/yourusername/hytale-server-manager/internal/releases"
	"github.com/yourusername/hytale-server-manager/internal/server"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
	ws "github.com/yourusername/hytale-server-manager/internal/websocket"
)

// ServerHandler handles server management requests
type ServerHandler struct {
	config           *config.Config
	db               *database.DB
	serverManager    *config.ServerManager
	rbacManager      *auth.RBACManager
	sshPool          *ssh.ConnectionPool
	lifecycleManager *server.LifecycleManager
	statusDetector   *server.StatusDetector
	processManager   server.ProcessManager
	activityLogger   *logging.ActivityLogger
	hub              *ws.Hub
	pendingOps       sync.WaitGroup
	cpuMu            sync.Mutex
	cpuSamples       map[string]cpuSample
	streamMu         sync.Mutex
	streamBuffers    map[string]*taskStreamBuffer
	tasksMu          sync.Mutex
	tasks            map[string]*serverTaskState
}

type cpuSample struct {
	timestamp time.Time
	idle      float64
	total     float64
}

// NewServerHandler creates a new server handler
func NewServerHandler(
	cfg *config.Config,
	db *database.DB,
	serverManager *config.ServerManager,
	rbacManager *auth.RBACManager,
	pool *ssh.ConnectionPool,
	lifecycle *server.LifecycleManager,
	status *server.StatusDetector,
	process server.ProcessManager,
	logger *logging.ActivityLogger,
	hub *ws.Hub,
) *ServerHandler {
	return &ServerHandler{
		config:           cfg,
		db:               db,
		serverManager:    serverManager,
		rbacManager:      rbacManager,
		sshPool:          pool,
		lifecycleManager: lifecycle,
		statusDetector:   status,
		processManager:   process,
		activityLogger:   logger,
		hub:              hub,
		cpuSamples:       make(map[string]cpuSample),
		streamBuffers:    make(map[string]*taskStreamBuffer),
		tasks:            make(map[string]*serverTaskState),
	}
}

// WaitForCompletion waits for all pending background operations to finish
func (h *ServerHandler) WaitForCompletion() {
	h.pendingOps.Wait()
}

// ListServers returns all servers with their connection status
func (h *ServerHandler) ListServers(c *gin.Context) {
	servers := h.serverManager.GetAll()
	
	// Build response with connection status for each server
	response := make([]models.ServerListItem, 0, len(servers))
	for _, serverDef := range servers {
		sessionName := server.SafeSessionName(serverDef.ID)
		statusInfo, _ := h.statusDetector.DetectStatus(serverDef.ID, sessionName)
		if statusInfo == nil {
			statusInfo = &server.ServerStatusInfo{Status: server.StatusOffline}
		}
		
		connectionStatus := h.determineConnectionStatus(serverDef.ID, serverDef, statusInfo)
		
		response = append(response, models.ServerListItem{
			ID:               serverDef.ID,
			Name:             serverDef.Name,
			Description:      serverDef.Description,
			ConnectionStatus: connectionStatus,
			Host:             serverDef.Connection.Host,
			Port:             serverDef.Connection.Port,
		})
	}
	
	c.JSON(http.StatusOK, response)
}

// GetServer returns a specific server
func (h *ServerHandler) GetServer(c *gin.Context) {
	serverID := c.Param("id")
	server, found := h.serverManager.GetByID(serverID)

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	c.JSON(http.StatusOK, server)
}

// CreateServer creates a new server definition
func (h *ServerHandler) CreateServer(c *gin.Context) {
	var newServer config.ServerDefinition
	if err := c.ShouldBindJSON(&newServer); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Auto-generate ID if not provided
	if newServer.ID == "" {
		newServer.ID = fmt.Sprintf("server-%d", time.Now().Unix())
	}

	if err := h.persistSSHKey(newServer.ID, &newServer.Connection); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store SSH key", "details": err.Error()})
		return
	}

	if err := h.serverManager.Add(newServer); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	if err := h.serverManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save servers"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Server created successfully", "id": newServer.ID, "server": newServer})
}

// UpdateServer updates a server definition
func (h *ServerHandler) UpdateServer(c *gin.Context) {
	serverID := c.Param("id")
	var updatedServer config.ServerDefinition
	if err := c.ShouldBindJSON(&updatedServer); err != nil {
		log.Printf("[UpdateServer] Failed to bind JSON for server %s: %v", serverID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updatedServer.ID = serverID

	log.Printf("[UpdateServer] Updating server %s with dependencies: install_dir=%s, service_user=%s, use_sudo=%v",
		serverID, updatedServer.Dependencies.InstallDir, updatedServer.Dependencies.ServiceUser, updatedServer.Dependencies.UseSudo)
	log.Printf("[UpdateServer] Runtime config: java_xms=%s, java_xmx=%s, java_metaspace=%s, enable_backup=%v, backup_dir=%s, backup_frequency=%s, assets_path=%s, extra_java_args=%s, extra_server_args=%s",
		updatedServer.Runtime.JavaXms, updatedServer.Runtime.JavaXmx, updatedServer.Runtime.JavaMetaspace,
		updatedServer.Runtime.EnableBackup, updatedServer.Runtime.BackupDir, updatedServer.Runtime.BackupFrequency,
		updatedServer.Runtime.AssetsPath, updatedServer.Runtime.ExtraJavaArgs, updatedServer.Runtime.ExtraServerArgs)

	if err := h.persistSSHKey(updatedServer.ID, &updatedServer.Connection); err != nil {
		log.Printf("[UpdateServer] Failed to persist SSH key for server %s: %v", serverID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store SSH key", "details": err.Error()})
		return
	}

	if err := h.serverManager.Update(updatedServer); err != nil {
		log.Printf("[UpdateServer] Failed to update server %s: %v", serverID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if err := h.serverManager.Save(); err != nil {
		log.Printf("[UpdateServer] Failed to save servers config: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save servers"})
		return
	}

	log.Printf("[UpdateServer] Successfully updated and saved server %s", serverID)
	c.JSON(http.StatusOK, gin.H{"message": "Server updated successfully"})
}

// DeleteServer deletes a server definition
func (h *ServerHandler) DeleteServer(c *gin.Context) {
	serverID := c.Param("id")

	if err := h.serverManager.Delete(serverID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if err := h.serverManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save servers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Server deleted successfully"})
}

// TestConnection validates SSH access and returns basic system info
func (h *ServerHandler) TestConnection(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}
	if h.processManager != nil {
		h.processManager.SetRunAsUser(serverID, strings.TrimSpace(serverDef.Dependencies.ServiceUser), serverDef.Dependencies.UseSudo)
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	run := func(cmd string) string {
		output, err := conn.Client.RunCommand(cmd)
		if err != nil {
			return ""
		}
		return output
	}

	user := run("whoami")
	hostname := run("hostname")
	osInfo := run("uname -a")
	uptime := run("uptime -p")
	if uptime == "" {
		uptime = run("uptime")
	}

	metrics := h.collectMetrics(run)
	if nodeMetrics, err := h.collectNodeExporterMetrics(serverID, serverDef); err == nil && len(nodeMetrics) > 0 {
		metrics = nodeMetrics
	} else if err != nil {
		log.Printf("[API] Node exporter metrics unavailable for %s: %v", serverID, err)
	}
	if err := h.recordMetrics(serverID, metrics, "online"); err != nil {
		log.Printf("[API] Failed to record metrics for %s: %v", serverID, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"user":     user,
		"hostname": hostname,
		"os":       osInfo,
		"uptime":   uptime,
		"host":     serverDef.Connection.Host,
		"port":     serverDef.Connection.Port,
		"metrics":  metrics,
	})
}

// GetMetrics returns recent metrics history for a server
func (h *ServerHandler) GetMetrics(c *gin.Context) {
	serverID := c.Param("id")
	limitParam := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit <= 0 || limit > 500 {
		limit = 50
	}

	rows, err := h.db.Query(`
		SELECT timestamp, cpu_usage, memory_used, memory_total, disk_used, disk_total, network_rx, network_tx, status
		FROM server_metrics
		WHERE server_id = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, serverID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load metrics"})
		return
	}
	defer rows.Close()

	metrics := make([]map[string]interface{}, 0)
	for rows.Next() {
		var timestamp string
		var cpuUsage, memoryUsed, memoryTotal, diskUsed, diskTotal, networkRx, networkTx interface{}
		var status string
		if err := rows.Scan(&timestamp, &cpuUsage, &memoryUsed, &memoryTotal, &diskUsed, &diskTotal, &networkRx, &networkTx, &status); err != nil {
			continue
		}
		metrics = append(metrics, map[string]interface{}{
			"timestamp":    timestamp,
			"cpu_usage":    cpuUsage,
			"memory_used":  memoryUsed,
			"memory_total": memoryTotal,
			"disk_used":    diskUsed,
			"disk_total":   diskTotal,
			"network_rx":   networkRx,
			"network_tx":   networkTx,
			"status":       status,
		})
	}

	c.JSON(http.StatusOK, gin.H{"metrics": metrics})
}

// GetLatestMetrics returns the latest metrics per server
func (h *ServerHandler) GetLatestMetrics(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"metrics": map[string]interface{}{}})
		return
	}

	rows, err := h.db.Query(`
		SELECT sm.server_id, sm.timestamp, sm.cpu_usage, sm.memory_used, sm.memory_total, sm.disk_used, sm.disk_total, sm.network_rx, sm.network_tx, sm.status
		FROM server_metrics sm
		INNER JOIN (
			SELECT server_id, MAX(timestamp) AS max_ts
			FROM server_metrics
			GROUP BY server_id
		) latest ON sm.server_id = latest.server_id AND sm.timestamp = latest.max_ts
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load latest metrics"})
		return
	}
	defer rows.Close()

	metrics := make(map[string]map[string]interface{})
	for rows.Next() {
		var serverID string
		var timestamp string
		var cpuUsage, memoryUsed, memoryTotal, diskUsed, diskTotal, networkRx, networkTx interface{}
		var status string
		if err := rows.Scan(&serverID, &timestamp, &cpuUsage, &memoryUsed, &memoryTotal, &diskUsed, &diskTotal, &networkRx, &networkTx, &status); err != nil {
			continue
		}
		metrics[serverID] = map[string]interface{}{
			"timestamp":    timestamp,
			"cpu_usage":    cpuUsage,
			"memory_used":  memoryUsed,
			"memory_total": memoryTotal,
			"disk_used":    diskUsed,
			"disk_total":   diskTotal,
			"network_rx":   networkRx,
			"network_tx":   networkTx,
			"status":       status,
		}
	}
	c.JSON(http.StatusOK, gin.H{"metrics": metrics})
}

// GetLiveMetrics collects live node_exporter metrics for all servers
func (h *ServerHandler) GetLiveMetrics(c *gin.Context) {
	servers := h.serverManager.GetAll()
	metrics := make(map[string]map[string]interface{})
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, serverDef := range servers {
		if serverDef.ID == "" {
			continue
		}

		wg.Add(1)
		go func(def config.ServerDefinition) {
			defer wg.Done()

			data, err := h.collectNodeExporterMetrics(def.ID, def)
			if err != nil || len(data) == 0 {
				return
			}

			timestamp := time.Now().UTC().Format(time.RFC3339)
			data["timestamp"] = timestamp

			_ = h.recordMetrics(def.ID, data, "online")

			mu.Lock()
			metrics[def.ID] = data
			mu.Unlock()
		}(serverDef)
	}

	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"metrics": metrics})
}

// GetServerActivity returns recent activity log entries for a server
func (h *ServerHandler) GetServerActivity(c *gin.Context) {
	serverID := c.Param("id")
	limitParam := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit <= 0 || limit > 500 {
		limit = 50
	}
	activityType := strings.TrimSpace(c.Query("type"))

	activities, err := h.activityLogger.GetActivities(serverID, activityType, time.Time{}, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load activity log"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"activities": activities})
}

// GetServerTasks returns recent tasks for a server
func (h *ServerHandler) GetServerTasks(c *gin.Context) {
	serverID := c.Param("id")
	if _, found := h.serverManager.GetByID(serverID); !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}
	if serverDef, ok := h.serverManager.GetByID(serverID); ok {
		if h.processManager != nil {
			h.processManager.SetRunAsUser(serverID, strings.TrimSpace(serverDef.Dependencies.ServiceUser), serverDef.Dependencies.UseSudo)
		}
	}

	items := h.listTasks(serverID)
	response := make([]map[string]interface{}, 0, len(items))
	for _, record := range items {
		entry := map[string]interface{}{
			"id":         record.ID,
			"task":       record.Task,
			"status":     record.Status,
			"started_at": record.StartedAt,
			"last_line":  record.LastLine,
		}
		if record.FinishedAt != nil {
			entry["finished_at"] = *record.FinishedAt
		}
		if record.Error != "" {
			entry["error"] = record.Error
		}
		response = append(response, entry)
	}

	c.JSON(http.StatusOK, gin.H{"tasks": response})
}

// GetNodeExporterStatus checks node_exporter installation and service status
func (h *ServerHandler) GetNodeExporterStatus(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	status, err := h.checkNodeExporterStatus(conn.Client)
	if err != nil {
		h.activityLogger.LogActivity(&logging.Activity{
			ServerID:     serverID,
			ActivityType: logging.ActivityPackageDetect,
			Description:  "Node exporter status check failed",
			Metadata: map[string]interface{}{
				"package": "node_exporter",
				"error":   err.Error(),
			},
			Success:      false,
			ErrorMessage: err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check node_exporter status", "details": err.Error()})
		return
	}

	metadata := map[string]interface{}{"package": "node_exporter"}
	for key, value := range status {
		metadata[key] = value
	}
	_ = h.activityLogger.LogActivity(&logging.Activity{
		ServerID:     serverID,
		ActivityType: logging.ActivityPackageDetect,
		Description:  "Node exporter status checked",
		Metadata:     metadata,
		Success:      true,
	})

	status["url"] = resolveNodeExporterURL(serverDef)
	c.JSON(http.StatusOK, status)
}

// InstallNodeExporter installs node_exporter and streams output to the task stream
func (h *ServerHandler) InstallNodeExporter(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Node exporter install started"})

	go func() {
		task := h.startTask(serverID, "node-exporter-install")
		outputLog := &strings.Builder{}
		var outputMu sync.Mutex
		emit := func(line string) {
			outputMu.Lock()
			appendOutput(outputLog, line, 4000)
			outputMu.Unlock()
			h.appendTaskStreamLine(serverID, task.ID, task.Task, line)
		}

		emit("Starting node_exporter install...")

		installScript := NodeExporterInstallScript
		writer := newLineSinkWriter(emit)
		err = conn.Client.StreamCommand(bashDollarQuotedCommand(installScript), writer, writer)
		writer.FlushRemaining()

		status, statusErr := h.checkNodeExporterStatus(conn.Client)
		if statusErr != nil {
			emit("Status check failed: " + statusErr.Error())
		}

		if status != nil {
			emit(fmt.Sprintf("Status: installed=%v running=%v enabled=%v", status["installed"], status["running"], status["enabled"]))
			if status["version"] != "" {
				emit(fmt.Sprintf("Version: %v", status["version"]))
			}
			emit(fmt.Sprintf("Metrics URL: %s", resolveNodeExporterURL(serverDef)))
		}

		if err != nil {
			emit("Install failed: " + err.Error())
			h.finishTask(serverID, task.ID, err)
			_ = h.activityLogger.LogActivity(&logging.Activity{
				ServerID:     serverID,
				ActivityType: logging.ActivityPackageInstall,
				Description:  "Node exporter install failed",
				Metadata: map[string]interface{}{
					"package": "node_exporter",
					"output":  truncateOutput(outputLog.String(), 2000),
					"error":   err.Error(),
				},
				Success:      false,
				ErrorMessage: err.Error(),
			})
			return
		}

		if status == nil {
			status = map[string]interface{}{}
		}
		emit("Node exporter install complete.")
		h.finishTask(serverID, task.ID, nil)
		_ = h.activityLogger.LogActivity(&logging.Activity{
			ServerID:     serverID,
			ActivityType: logging.ActivityPackageInstall,
			Description:  "Node exporter installed",
			Metadata: map[string]interface{}{
				"package":   "node_exporter",
				"installed": status["installed"],
				"running":   status["running"],
				"output":    truncateOutput(outputLog.String(), 2000),
			},
			Success: true,
		})
	}()
}

func (h *ServerHandler) collectMetrics(run func(string) string) map[string]interface{} {
	metrics := map[string]interface{}{}

	cpu := run("top -bn1 | awk '/Cpu\\(s\\)/{print 100-$8}'")
	if cpuValue, err := parseFloat(cpu); err == nil {
		metrics["cpu_usage"] = cpuValue
	}

	memory := run("free -b | awk '/Mem:/{print $3\" \"$2}'")
	memUsed, memTotal, err := parseTwoInt64(memory)
	if err == nil {
		metrics["memory_used"] = memUsed
		metrics["memory_total"] = memTotal
	}

	disk := run("df -B1 / | awk 'NR==2{print $3\" \"$2}'")
	diskUsed, diskTotal, err := parseTwoInt64(disk)
	if err == nil {
		metrics["disk_used"] = diskUsed
		metrics["disk_total"] = diskTotal
	}

	net := run("cat /proc/net/dev | awk 'NR>2{rx+=$2; tx+=$10} END{print rx\" \"tx}'")
	rx, tx, err := parseTwoInt64(net)
	if err == nil {
		metrics["network_rx"] = rx
		metrics["network_tx"] = tx
	}

	return metrics
}

func (h *ServerHandler) collectNodeExporterMetrics(serverID string, serverDef config.ServerDefinition) (map[string]interface{}, error) {
	url := resolveNodeExporterURL(serverDef)
	if url == "" {
		return nil, fmt.Errorf("node exporter URL not resolved")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("node exporter returned %s", resp.Status)
	}

	parsed, err := parseNodeExporterMetrics(resp.Body)
	if err != nil {
		return nil, err
	}

	metrics := map[string]interface{}{}
	if parsed.hasMemoryTotal && parsed.hasMemoryAvail {
		used := parsed.memoryTotal - parsed.memoryAvailable
		if used < 0 {
			used = 0
		}
		metrics["memory_total"] = int64(parsed.memoryTotal)
		metrics["memory_used"] = int64(used)
	}

	if parsed.hasDiskSize && parsed.hasDiskAvail {
		used := parsed.diskSize - parsed.diskAvailable
		if used < 0 {
			used = 0
		}
		metrics["disk_total"] = int64(parsed.diskSize)
		metrics["disk_used"] = int64(used)
	}

	if parsed.networkRx > 0 || parsed.networkTx > 0 {
		metrics["network_rx"] = int64(parsed.networkRx)
		metrics["network_tx"] = int64(parsed.networkTx)
	}

	if parsed.load1 >= 0 {
		metrics["load1"] = parsed.load1
	}

	if parsed.cpuTotal > 0 {
		if usage, ok := h.calculateCPUUsage(serverID, parsed.cpuIdle, parsed.cpuTotal); ok {
			metrics["cpu_usage"] = usage
		}
	}

	return metrics, nil
}

func (h *ServerHandler) calculateCPUUsage(serverID string, idle float64, total float64) (float64, bool) {
	h.cpuMu.Lock()
	defer h.cpuMu.Unlock()

	now := time.Now()
	prev, ok := h.cpuSamples[serverID]
	h.cpuSamples[serverID] = cpuSample{timestamp: now, idle: idle, total: total}
	if !ok {
		return 0, false
	}

	if total <= prev.total {
		return 0, false
	}

	deltaIdle := idle - prev.idle
	deltaTotal := total - prev.total
	if deltaTotal <= 0 {
		return 0, false
	}

	usage := (1 - (deltaIdle / deltaTotal)) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}

	return usage, true
}

type nodeExporterMetrics struct {
	memoryTotal     float64
	memoryAvailable float64
	hasMemoryTotal  bool
	hasMemoryAvail  bool
	diskSize        float64
	diskAvailable   float64
	hasDiskSize     bool
	hasDiskAvail    bool
	networkRx       float64
	networkTx       float64
	load1           float64
	cpuIdle         float64
	cpuTotal        float64
}

func parseNodeExporterMetrics(reader io.Reader) (*nodeExporterMetrics, error) {
	metrics := &nodeExporterMetrics{load1: -1}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		name, labels, value, ok := parsePrometheusLine(line)
		if !ok {
			continue
		}

		switch name {
		case "node_memory_MemTotal_bytes":
			metrics.memoryTotal = value
			metrics.hasMemoryTotal = true
		case "node_memory_MemAvailable_bytes":
			metrics.memoryAvailable = value
			metrics.hasMemoryAvail = true
		case "node_filesystem_size_bytes":
			if isRootFilesystem(labels) {
				metrics.diskSize = value
				metrics.hasDiskSize = true
			}
		case "node_filesystem_avail_bytes":
			if isRootFilesystem(labels) {
				metrics.diskAvailable = value
				metrics.hasDiskAvail = true
			}
		case "node_load1":
			metrics.load1 = value
		case "node_network_receive_bytes_total":
			if labels["device"] != "lo" {
				metrics.networkRx += value
			}
		case "node_network_transmit_bytes_total":
			if labels["device"] != "lo" {
				metrics.networkTx += value
			}
		case "node_cpu_seconds_total":
			metrics.cpuTotal += value
			if labels["mode"] == "idle" {
				metrics.cpuIdle += value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return metrics, nil
}

func parsePrometheusLine(line string) (string, map[string]string, float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", nil, 0, false
	}

	metricPart := fields[0]
	valueStr := fields[len(fields)-1]
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return "", nil, 0, false
	}

	name := metricPart
	labels := map[string]string{}
	if brace := strings.Index(metricPart, "{"); brace != -1 {
		name = metricPart[:brace]
		end := strings.LastIndex(metricPart, "}")
		if end > brace {
			labelStr := metricPart[brace+1 : end]
			labels = parsePrometheusLabels(labelStr)
		}
	}

	return name, labels, value, true
}

func parsePrometheusLabels(raw string) map[string]string {
	labels := map[string]string{}
	var key strings.Builder
	var value strings.Builder
	readingKey := true
	inQuotes := false
	escape := false

	flush := func() {
		if key.Len() == 0 {
			return
		}
		labels[key.String()] = value.String()
		key.Reset()
		value.Reset()
		readingKey = true
	}

	for _, r := range raw {
		if escape {
			if readingKey {
				key.WriteRune(r)
			} else {
				value.WriteRune(r)
			}
			escape = false
			continue
		}

		if r == '\\' {
			escape = true
			continue
		}

		if readingKey {
			if r == '=' {
				readingKey = false
				continue
			}
			if r == ',' {
				continue
			}
			key.WriteRune(r)
			continue
		}

		if r == '"' {
			inQuotes = !inQuotes
			continue
		}

		if r == ',' && !inQuotes {
			flush()
			continue
		}

		value.WriteRune(r)
	}

	flush()
	return labels
}

func isRootFilesystem(labels map[string]string) bool {
	if labels["mountpoint"] != "/" {
		return false
	}

	fsType := labels["fstype"]
	switch fsType {
	case "tmpfs", "overlay", "squashfs", "proc", "sysfs", "devtmpfs", "cgroup2", "cgroup", "nsfs", "rpc_pipefs", "autofs", "tracefs":
		return false
	default:
		return true
	}
}

func resolveNodeExporterURL(serverDef config.ServerDefinition) string {
	if serverDef.Monitoring.NodeExporterURL != "" {
		return normalizeNodeExporterURL(serverDef.Monitoring.NodeExporterURL)
	}

	if serverDef.Connection.Host == "" {
		return ""
	}

	port := serverDef.Monitoring.NodeExporterPort
	if port == 0 {
		port = 9100
	}

	return fmt.Sprintf("http://%s:%d/metrics", serverDef.Connection.Host, port)
}

func normalizeNodeExporterURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return ""
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	if !strings.HasSuffix(url, "/metrics") {
		if strings.HasSuffix(url, "/") {
			url += "metrics"
		} else {
			url += "/metrics"
		}
	}
	return url
}

func (h *ServerHandler) recordMetrics(serverID string, metrics map[string]interface{}, status string) error {
	if h.db == nil {
		return nil
	}

	_, err := h.db.Exec(
		"INSERT INTO server_metrics ("+
			"server_id, cpu_usage, memory_used, memory_total, disk_used, disk_total, network_rx, network_tx, status"+
			") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		serverID,
		metrics["cpu_usage"],
		metrics["memory_used"],
		metrics["memory_total"],
		metrics["disk_used"],
		metrics["disk_total"],
		metrics["network_rx"],
		metrics["network_tx"],
		status,
	)

	return err
}

func parseFloat(value string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(value), 64)
}

func parseTwoInt64(value string) (int64, int64, error) {
	parts := strings.Fields(value)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid metrics")
	}

	first, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	second, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}

	return first, second, nil
}

func (h *ServerHandler) checkNodeExporterStatus(client *ssh.Client) (map[string]interface{}, error) {
	run := func(cmd string) (string, error) {
		return client.RunCommand(bashDollarQuotedCommand(cmd))
	}

	installedOut, err := run(NodeExporterCheckInstalledScript)
	if err != nil {
		return nil, err
	}
	installed := strings.TrimSpace(installedOut) == "yes"

	version := ""
	if installed {
		verOut, _ := run(NodeExporterCheckVersionScript)
		version = strings.TrimSpace(verOut)
	}

	runningOut, _ := run(NodeExporterCheckRunningScript)
	running := strings.TrimSpace(runningOut) == "yes"

	enabledOut, _ := run(NodeExporterCheckEnabledScript)
	enabled := strings.TrimSpace(enabledOut) == "yes"

	return map[string]interface{}{
		"installed": installed,
		"running":   running,
		"enabled":   enabled,
		"version":   version,
	}, nil
}

func truncateOutput(output string, limit int) string {
	value := strings.TrimSpace(output)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "... (truncated)"
}

func bashDollarQuotedCommand(script string) string {
	return "bash -lc $'" + escapeForBashDollarQuote(script) + "'"
}

func escapeForBashDollarQuote(script string) string {
	escaped := strings.ReplaceAll(script, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	return escaped
}

func resolveManagerHost(c *gin.Context, cfg *config.Config) string {
	host := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	if host == "" {
		host = fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	return host
}

func resolveManagerURL(c *gin.Context, cfg *config.Config) string {
	scheme := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		scheme = "http"
		if c.Request.TLS != nil || cfg.Server.TLS.Enabled {
			scheme = "https"
		}
	}
	return fmt.Sprintf("%s://%s", scheme, resolveManagerHost(c, cfg))
}

func normalizeArch(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func fetchRemoteMachineID(conn *ssh.PooledConnection) string {
	output, err := conn.Client.RunCommand("bash -lc 'cat /etc/machine-id 2>/dev/null || cat /var/lib/dbus/machine-id 2>/dev/null'")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func uploadFileSFTP(client *sftp.Client, localPath, remotePath string, mode os.FileMode) error {
	local, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer local.Close()

	remote, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	defer remote.Close()

	if _, err := io.Copy(remote, local); err != nil {
		return err
	}
	_ = remote.Chmod(mode)
	return nil
}

func uploadBytesSFTP(client *sftp.Client, remotePath string, data []byte, mode os.FileMode) error {
	remote, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	defer remote.Close()

	if _, err := remote.Write(data); err != nil {
		return err
	}
	_ = remote.Chmod(mode)
	return nil
}

func ensureAgentBinary(arch string, dataDir string, emit func(string)) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("data dir not configured")
	}
	binDir := filepath.Join(dataDir, "agent-binaries")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	binPath := filepath.Join(binDir, fmt.Sprintf("hytale-agent-linux-%s", arch))
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	}

	if emit != nil {
		emit("Agent binary missing; attempting local build...")
	}

	startDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	modDir, err := findGoModDir(startDir)
	if err != nil {
		return "", err
	}

	cmd := exec.Command("go", "build", "-o", binPath, "./agent")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build agent: %w: %s", err, out.String())
	}

	return binPath, nil
}

func findGoModDir(start string) (string, error) {
	current := start
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("go.mod not found from %s", start)
}

type DependenciesInstallRequest struct {
	SkipUpdate    *bool    `json:"skip_update"`
	UseSudo       *bool    `json:"use_sudo"`
	CreateUser    *bool    `json:"create_user"`
	ServiceUser   *string  `json:"service_user"`
	ServiceGroups []string `json:"service_groups"`
	InstallDir    *string  `json:"install_dir"`
	SaveConfig    bool     `json:"save_config"`
}

type DependenciesCheckResponse struct {
	JavaOK   bool   `json:"java_ok"`
	JavaLine string `json:"java_line"`
	UserOK   bool   `json:"user_ok"`
	UserHome string `json:"user_home"`
	DirOK    bool   `json:"dir_ok"`
	DirPath  string `json:"dir_path"`
}

type AgentInstallRequest struct {
	UseSudo *bool `json:"use_sudo"`
}

type ProcessKillRequest struct {
	PID     int   `json:"pid"`
	UseSudo *bool `json:"use_sudo"`
}

type ReleaseDeployRequest struct {
	PackageName       string  `json:"package_name"`
	InstallDir        *string `json:"install_dir"`
	ServiceUser       *string `json:"service_user"`
	UseSudo           *bool   `json:"use_sudo"`
	JavaXms           *string `json:"java_xms"`
	JavaXmx           *string `json:"java_xmx"`
	JavaMetaspace     *string `json:"java_metaspace"`
	EnableStringDedup *bool   `json:"enable_string_dedup"`
	EnableAOT         *bool   `json:"enable_aot"`
	EnableBackup      *bool   `json:"enable_backup"`
	BackupDir         *string `json:"backup_dir"`
	BackupFrequency   *int    `json:"backup_frequency"`
	AssetsPath        *string `json:"assets_path"`
	ExtraJavaArgs     *string `json:"extra_java_args"`
	ExtraServerArgs   *string `json:"extra_server_args"`
}

type TransferBenchmarkRequest struct {
	SizeMB      *int  `json:"size_mb"`
	BlockMB     *int  `json:"block_mb"`
	RemoveAfter *bool `json:"remove_after"`
}

type taskStreamLine struct {
	Line      string
	Task      string
	TaskID    string
	Timestamp time.Time
}

type taskStatus string

const (
	taskStatusRunning  taskStatus = "running"
	taskStatusComplete taskStatus = "complete"
	taskStatusFailed   taskStatus = "failed"
)

// AgentState represents the state information returned by the agent
type AgentState struct {
	HostUUID      string        `json:"host_uuid"`
	Timestamp     int64         `json:"timestamp"`
	Services      map[string]string `json:"services"`
	Ports         map[int]bool  `json:"ports"`
	JavaProcesses []JavaProcess `json:"java"`
}

// JavaProcess represents a Java process detected by the agent
type JavaProcess struct {
	PID         int    `json:"pid"`
	User        string `json:"user"`
	CommandLine string `json:"cmdline"`
	ListenPorts []int  `json:"listen_ports"`
}

// HealthCheck represents comprehensive server health information
type HealthCheck struct {
	ConnectionStatus models.ServerConnectionStatus `json:"connection_status"`
	SSHStatus        SSHHealthStatus               `json:"ssh"`
	AgentStatus      AgentHealthStatus             `json:"agent"`
	ProcessStatus    ProcessHealthStatus           `json:"process"`
	ScreenStatus     ScreenHealthStatus            `json:"screen"`
}

// SSHHealthStatus represents SSH connectivity status
type SSHHealthStatus struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
}

// AgentHealthStatus represents agent connectivity and state
type AgentHealthStatus struct {
	Available     bool              `json:"available"`
	Connected     bool              `json:"connected"`
	Error         string            `json:"error,omitempty"`
	JavaProcesses []JavaProcess     `json:"java_processes,omitempty"`
	ListeningPorts map[int]bool     `json:"listening_ports,omitempty"`
	Services      map[string]string `json:"services,omitempty"`
}

// ProcessHealthStatus represents Hytale server process status
type ProcessHealthStatus struct {
	Running       bool   `json:"running"`
	PID           int    `json:"pid,omitempty"`
	Port          string `json:"port,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	DetectionMethod string `json:"detection_method,omitempty"`
}

// ScreenHealthStatus represents screen session status
type ScreenHealthStatus struct {
	SessionExists bool   `json:"session_exists"`
	SessionName   string `json:"session_name"`
	Streaming     bool   `json:"streaming"`
}

type taskRecord struct {
	ID         string     `json:"id"`
	Task       string     `json:"task"`
	Status     taskStatus `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	LastLine   string     `json:"last_line,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type serverTaskState struct {
	order []string
	tasks map[string]*taskRecord
}

type taskStreamBuffer struct {
	mu    sync.RWMutex
	max   int
	lines []taskStreamLine
}

func newTaskStreamBuffer(max int) *taskStreamBuffer {
	return &taskStreamBuffer{max: max, lines: make([]taskStreamLine, 0, max)}
}

func (b *taskStreamBuffer) Add(line taskStreamLine) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
}

func (b *taskStreamBuffer) GetLines() []taskStreamLine {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]taskStreamLine, len(b.lines))
	copy(result, b.lines)
	return result
}

func (h *ServerHandler) InstallDependencies(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req DependenciesInstallRequest
	_ = c.ShouldBindJSON(&req)

	merged := config.DependenciesConfig{
		SkipUpdate:  false,
		UseSudo:     true,
		CreateUser:  true,
		ServiceUser: "hytale",
		InstallDir:  "~/hytale-server",
	}

	if serverDef.Dependencies.Configured {
		merged = serverDef.Dependencies
		if merged.ServiceUser == "" {
			merged.ServiceUser = "hytale"
		}
		if merged.InstallDir == "" {
			merged.InstallDir = "~/hytale-server"
		}
	}

	if req.SkipUpdate != nil {
		merged.SkipUpdate = *req.SkipUpdate
	}
	if req.UseSudo != nil {
		merged.UseSudo = *req.UseSudo
	}
	if req.CreateUser != nil {
		merged.CreateUser = *req.CreateUser
	}
	if req.ServiceUser != nil && strings.TrimSpace(*req.ServiceUser) != "" {
		merged.ServiceUser = strings.TrimSpace(*req.ServiceUser)
	}
	if req.InstallDir != nil && strings.TrimSpace(*req.InstallDir) != "" {
		merged.InstallDir = strings.TrimSpace(*req.InstallDir)
	}
	if req.ServiceGroups != nil {
		merged.ServiceGroups = req.ServiceGroups
	}

	if req.SaveConfig {
		serverDef.Dependencies = merged
		serverDef.Dependencies.Configured = true
		_ = h.serverManager.Update(serverDef)
		_ = h.serverManager.Save()
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Dependency install started"})

	go func() {
		task := h.startTask(serverID, "dependencies-install")
		outputLog := &strings.Builder{}
		var outputMu sync.Mutex
		emit := func(line string) {
			outputMu.Lock()
			appendOutput(outputLog, line, 4000)
			outputMu.Unlock()
			h.appendTaskStreamLine(serverID, task.ID, task.Task, line)
		}

		emit("Starting dependency install...")
		keepAlive := time.NewTicker(15 * time.Second)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-keepAlive.C:
					emit("Still running...")
				case <-done:
					return
				}
			}
		}()

		script := ServerDependenciesInstallScript
		script = strings.ReplaceAll(script, "{{SKIP_UPDATE}}", boolToScript(merged.SkipUpdate))
		script = strings.ReplaceAll(script, "{{USE_SUDO}}", boolToScript(merged.UseSudo))
		script = strings.ReplaceAll(script, "{{CREATE_USER}}", boolToScript(merged.CreateUser))
		script = strings.ReplaceAll(script, "{{SERVICE_USER}}", escapeForScript(merged.ServiceUser))
		script = strings.ReplaceAll(script, "{{SERVICE_GROUPS}}", escapeForScript(strings.Join(merged.ServiceGroups, ",")))
		script = strings.ReplaceAll(script, "{{INSTALL_DIR}}", escapeForScriptPath(merged.InstallDir))

		writer := newLineSinkWriter(emit)
		err = conn.Client.StreamCommand(bashDollarQuotedCommand(script), writer, writer)
		close(done)
		keepAlive.Stop()
		writer.FlushRemaining()

		if err != nil {
			emit("Install failed: " + err.Error())
			emit("Hint: apt-get failures usually provide details above. Expand the output to see the root cause.")
			h.finishTask(serverID, task.ID, err)
			_ = h.activityLogger.LogActivity(&logging.Activity{
				ServerID:     serverID,
				ActivityType: logging.ActivityPackageInstall,
				Description:  "Server dependencies install failed",
				Metadata: map[string]interface{}{
					"output": truncateOutput(outputLog.String(), 2000),
					"error":  err.Error(),
				},
				Success:      false,
				ErrorMessage: err.Error(),
			})
			return
		}

		emit("Dependencies install complete.")
		h.finishTask(serverID, task.ID, nil)
		_ = h.activityLogger.LogActivity(&logging.Activity{
			ServerID:     serverID,
			ActivityType: logging.ActivityPackageInstall,
			Description:  "Server dependencies installed",
			Metadata: map[string]interface{}{
				"output": truncateOutput(outputLog.String(), 2000),
			},
			Success: true,
		})
	}()
}

func (h *ServerHandler) InstallAgent(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req AgentInstallRequest
	_ = c.ShouldBindJSON(&req)

	useSudo := true
	if req.UseSudo != nil {
		useSudo = *req.UseSudo
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	managerHost := resolveManagerHost(c, h.config)
	agentUser := "hytale-agent"

	c.JSON(http.StatusAccepted, gin.H{"message": "Agent install started"})

	go func() {
		task := h.startTask(serverID, "agent-install")
		outputLog := &strings.Builder{}
		var outputMu sync.Mutex
		emit := func(line string) {
			outputMu.Lock()
			appendOutput(outputLog, line, 4000)
			outputMu.Unlock()
			h.appendTaskStreamLine(serverID, task.ID, task.Task, line)
		}

		emit("Starting agent install...")
		keepAlive := time.NewTicker(15 * time.Second)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-keepAlive.C:
					emit("Still running...")
				case <-done:
					return
				}
			}
		}()

		emit("Preparing agent artifacts...")

		rawArch, err := conn.Client.RunCommand("uname -m")
		if err != nil {
			emit("Install failed: unable to detect architecture")
			h.finishTask(serverID, task.ID, err)
			return
		}
		arch := normalizeArch(strings.TrimSpace(rawArch))
		if arch == "" {
			emit("Install failed: unsupported architecture")
			h.finishTask(serverID, task.ID, fmt.Errorf("unsupported arch: %s", strings.TrimSpace(rawArch)))
			return
		}

		localBin, err := ensureAgentBinary(arch, h.config.Storage.DataDir, emit)
		if err != nil {
			emit("Install failed: agent binary unavailable")
			h.finishTask(serverID, task.ID, err)
			return
		}

		hostUUID := strings.TrimSpace(fetchRemoteMachineID(conn))
		if hostUUID == "" {
			emit("Install failed: unable to read host UUID")
			h.finishTask(serverID, task.ID, fmt.Errorf("host UUID not found"))
			return
		}

		caDir := filepath.Join(h.config.Storage.DataDir, "agent-ca")
		ca, err := agentcert.LoadOrCreateCA(caDir)
		if err != nil {
			emit("Install failed: unable to load CA")
			h.finishTask(serverID, task.ID, err)
			return
		}

		httpsCertPEM, httpsKeyPEM, serial, notAfter, fingerprint, err := agentcert.IssueServerCert(ca, serverDef.Connection.Host, serverID, hostUUID, 365*24*time.Hour)
		if err != nil {
			emit("Install failed: unable to issue HTTPS cert")
			h.finishTask(serverID, task.ID, err)
			return
		}

		clientCert, err := agentcert.GetClientCert(h.db.DB, "server-manager")
		if err != nil {
			emit("Install failed: unable to load manager client cert")
			h.finishTask(serverID, task.ID, err)
			return
		}
		if clientCert == nil || time.Until(clientCert.ExpiresAt) < (30*24*time.Hour) {
			clientPEM, clientKeyPEM, clientSerial, clientNotAfter, clientFingerprint, err := agentcert.IssueClientCert(ca, "server-manager", 365*24*time.Hour)
			if err != nil {
				emit("Install failed: unable to issue manager client cert")
				h.finishTask(serverID, task.ID, err)
				return
			}

			tx, err := h.db.DB.Begin()
			if err != nil {
				emit("Install failed: unable to store manager client cert")
				h.finishTask(serverID, task.ID, err)
				return
			}
			if err := agentcert.InsertClientCert(tx, "server-manager", clientSerial, clientFingerprint, clientPEM, clientKeyPEM, clientNotAfter); err != nil {
				_ = tx.Rollback()
				emit("Install failed: unable to store manager client cert")
				h.finishTask(serverID, task.ID, err)
				return
			}
			if err := tx.Commit(); err != nil {
				emit("Install failed: unable to store manager client cert")
				h.finishTask(serverID, task.ID, err)
				return
			}
			clientCert = &agentcert.ClientCert{
				Name:        "server-manager",
				CertPEM:     clientPEM,
				KeyPEM:      clientKeyPEM,
				Serial:      clientSerial,
				Fingerprint: clientFingerprint,
				ExpiresAt:   clientNotAfter,
			}
			_ = os.WriteFile(filepath.Join(caDir, "manager-client.crt"), clientPEM, 0644)
			_ = os.WriteFile(filepath.Join(caDir, "manager-client.key"), clientKeyPEM, 0600)
		}

		tx, err := h.db.DB.Begin()
		if err != nil {
			emit("Install failed: unable to store HTTPS cert")
			h.finishTask(serverID, task.ID, err)
			return
		}
		if err := agentcert.InsertHTTPSCertificate(tx, serverID, hostUUID, serial, fingerprint, httpsCertPEM, httpsKeyPEM, notAfter); err != nil {
			_ = tx.Rollback()
			emit("Install failed: unable to store HTTPS cert")
			h.finishTask(serverID, task.ID, err)
			return
		}
		if err := tx.Commit(); err != nil {
			emit("Install failed: unable to store HTTPS cert")
			h.finishTask(serverID, task.ID, err)
			return
		}

		sftpClient, err := conn.Client.NewSFTPWithOptions(
			sftp.MaxPacketUnchecked(131072),
			sftp.UseConcurrentWrites(true),
			sftp.MaxConcurrentRequestsPerFile(64),
		)
		if err != nil {
			emit("Install failed: unable to open SFTP")
			h.finishTask(serverID, task.ID, err)
			return
		}
		defer sftpClient.Close()

		remoteBin := "/tmp/hytale-agent"
		remoteHTTPSDir := "/tmp/hytale-agent-https"
		_ = sftpClient.MkdirAll(remoteHTTPSDir)

		if err := uploadFileSFTP(sftpClient, localBin, remoteBin, 0755); err != nil {
			emit("Install failed: unable to upload agent binary")
			h.finishTask(serverID, task.ID, err)
			return
		}
		if err := uploadBytesSFTP(sftpClient, path.Join(remoteHTTPSDir, "server.crt"), httpsCertPEM, 0644); err != nil {
			emit("Install failed: unable to upload HTTPS cert")
			h.finishTask(serverID, task.ID, err)
			return
		}
		if err := uploadBytesSFTP(sftpClient, path.Join(remoteHTTPSDir, "server.key"), httpsKeyPEM, 0600); err != nil {
			emit("Install failed: unable to upload HTTPS key")
			h.finishTask(serverID, task.ID, err)
			return
		}
		if err := uploadBytesSFTP(sftpClient, path.Join(remoteHTTPSDir, "ca.crt"), ca.CertPEM, 0644); err != nil {
			emit("Install failed: unable to upload HTTPS CA")
			h.finishTask(serverID, task.ID, err)
			return
		}

		script := ServerAgentInstallScript
		script = strings.ReplaceAll(script, "{{USE_SUDO}}", boolToScript(useSudo))
		script = strings.ReplaceAll(script, "{{AGENT_USER}}", escapeForScript(agentUser))
		script = strings.ReplaceAll(script, "{{AGENT_SERVER_ADDR}}", escapeForScript(managerHost))
		script = strings.ReplaceAll(script, "{{AGENT_STAGED_BIN}}", escapeForScript(remoteBin))
		script = strings.ReplaceAll(script, "{{AGENT_HTTPS_CERTS_DIR}}", escapeForScript(remoteHTTPSDir))

		writer := newLineSinkWriter(emit)
		err = conn.Client.StreamCommand(bashDollarQuotedCommand(script), writer, writer)
		close(done)
		keepAlive.Stop()
		writer.FlushRemaining()

		if err != nil {
			emit("Install failed: " + err.Error())
			h.finishTask(serverID, task.ID, err)
			_ = h.activityLogger.LogActivity(&logging.Activity{
				ServerID:     serverID,
				ActivityType: logging.ActivityPackageInstall,
				Description:  "Agent install failed",
				Metadata: map[string]interface{}{
					"output": truncateOutput(outputLog.String(), 2000),
					"error":  err.Error(),
				},
				Success:      false,
				ErrorMessage: err.Error(),
			})
			return
		}

		emit("Agent install complete.")
		h.finishTask(serverID, task.ID, nil)
		_ = h.activityLogger.LogActivity(&logging.Activity{
			ServerID:     serverID,
			ActivityType: logging.ActivityPackageInstall,
			Description:  "Agent installed",
			Metadata: map[string]interface{}{
				"output": truncateOutput(outputLog.String(), 2000),
			},
			Success: true,
		})
	}()
}

func (h *ServerHandler) CheckDependencies(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	merged := config.DependenciesConfig{
		SkipUpdate:  false,
		UseSudo:     true,
		CreateUser:  true,
		ServiceUser: "hytale",
		InstallDir:  "~/hytale-server",
	}
	if serverDef.Dependencies.Configured {
		merged = serverDef.Dependencies
		if merged.ServiceUser == "" {
			merged.ServiceUser = "hytale"
		}
		if merged.InstallDir == "" {
			merged.InstallDir = "~/hytale-server"
		}
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	script := ServerDependenciesCheckScript
	script = strings.ReplaceAll(script, "{{SERVICE_USER}}", escapeForScript(merged.ServiceUser))
	script = strings.ReplaceAll(script, "{{INSTALL_DIR}}", escapeForScriptPath(merged.InstallDir))

	output, err := conn.Client.RunCommand(bashDollarQuotedCommand(script))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Dependency check failed", "details": err.Error(), "output": output})
		return
	}

	parsed := parseDependencyCheckOutput(output)
	parsed.DirPath = strings.TrimSpace(parsed.DirPath)

	c.JSON(http.StatusOK, parsed)
}

func (h *ServerHandler) GetAgentState(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	if strings.TrimSpace(serverDef.Connection.Host) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Server host is required"})
		return
	}

	clientCert, err := agentcert.GetClientCert(h.db.DB, "server-manager")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load manager client cert", "details": err.Error()})
		return
	}
	if clientCert == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Manager client cert not found. Install agent first."})
		return
	}

	cert, err := tls.X509KeyPair(clientCert.CertPEM, clientCert.KeyPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid manager client cert", "details": err.Error()})
		return
	}

	caPath := filepath.Join(h.config.Storage.DataDir, "agent-ca", "ca.crt")
	caData, err := os.ReadFile(caPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read agent CA", "details": err.Error()})
		return
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid agent CA"})
		return
	}

	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS12,
				RootCAs:      pool,
				Certificates: []tls.Certificate{cert},
			},
		},
	}

	url := fmt.Sprintf("https://%s:9443/state", strings.TrimSpace(serverDef.Connection.Host))
	resp, err := client.Get(url)
	if err != nil {
		diag := h.diagnoseAgentConnection(serverDef)
		payload := gin.H{"error": "Failed to fetch agent state", "details": err.Error()}
		if diag != nil {
			payload["agent_status"] = diag.Status
			payload["listening"] = diag.Listening
			payload["journal"] = diag.Journal
			payload["process"] = diag.Process
		}
		c.JSON(http.StatusBadGateway, payload)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to read agent response", "details": err.Error()})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Agent returned error", "status": resp.StatusCode, "body": string(body)})
		return
	}

	if len(body) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Empty agent response"})
		return
	}

	c.Data(http.StatusOK, "application/json", body)
}

func (h *ServerHandler) KillProcess(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req ProcessKillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	if req.PID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pid must be > 0"})
		return
	}

	useSudo := true
	if req.UseSudo != nil {
		useSudo = *req.UseSudo
	}
	sudo := ""
	if useSudo {
		sudo = "sudo -n "
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	cmd := fmt.Sprintf("%skill -TERM %d >/dev/null 2>&1; sleep 1; %skill -0 %d >/dev/null 2>&1 || exit 0; %skill -KILL %d", sudo, req.PID, sudo, req.PID, sudo, req.PID)
	output, err := conn.Client.RunCommand(bashDollarQuotedCommand(cmd))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to kill process", "details": err.Error(), "output": output})
		return
	}

	_ = h.activityLogger.LogActivity(&logging.Activity{
		ServerID:     serverID,
		ActivityType: logging.ActivityCommandExecute,
		Description:  "Killed process by PID",
		Metadata: map[string]interface{}{
			"pid": req.PID,
		},
		Success: true,
	})

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type agentConnDiag struct {
	Status    string
	Listening string
	Journal   string
	Process   string
}

func (h *ServerHandler) diagnoseAgentConnection(serverDef config.ServerDefinition) *agentConnDiag {
	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		return nil
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		return nil
	}

	conn, err := h.sshPool.GetConnection(serverDef.ID, sshConfig)
	if err != nil {
		return nil
	}

	statusOut, _ := conn.Client.RunCommand("systemctl is-active hytale-agent || true")
	listenCmd := "if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then sudo ss -lntp | grep ':9443' || true; else ss -lntp | grep ':9443' || true; fi"
	listenFallback := "if command -v netstat >/dev/null 2>&1; then netstat -lntp 2>/dev/null | grep ':9443' || true; fi"
	listenOut, _ := conn.Client.RunCommand("bash -lc '" + listenCmd + "'")
	if strings.TrimSpace(listenOut) == "" {
		fallbackOut, _ := conn.Client.RunCommand("bash -lc '" + listenFallback + "'")
		listenOut = fallbackOut
	}
	psOut, _ := conn.Client.RunCommand("ps -eo pid,cmd | grep -E 'hytale-agent' | grep -v grep || true")
	journalOut, _ := conn.Client.RunCommand("journalctl -u hytale-agent --no-pager -n 80 || true")

	return &agentConnDiag{
		Status:    strings.TrimSpace(statusOut),
		Listening: strings.TrimSpace(listenOut),
		Journal:   truncateOutput(strings.TrimSpace(journalOut), 1200),
		Process:   truncateOutput(strings.TrimSpace(psOut), 600),
	}
}

func (h *ServerHandler) DeployRelease(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req ReleaseDeployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.PackageName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "package_name is required"})
		return
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH key path is required"})
		return
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH password is required"})
		return
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect via SSH", "details": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Release deployment started"})

	go func() {
		task := h.startTask(serverID, "release-deploy")
		outputLog := &strings.Builder{}
		var outputMu sync.Mutex
		emit := func(line string) {
			outputMu.Lock()
			appendOutput(outputLog, line, 4000)
			outputMu.Unlock()
			h.appendTaskStreamLine(serverID, task.ID, task.Task, line)
		}

		emit("Starting release deployment...")

		manager := releases.NewManager(h.config, h.db)
		releasesList, listErr := manager.ListAllReleases()
		if listErr != nil {
			emit("Failed to load releases: " + listErr.Error())
			h.finishTask(serverID, task.ID, listErr)
			return
		}

		var selected *releases.Release
		for _, release := range releasesList {
			base := strings.TrimSuffix(filepath.Base(release.FilePath), filepath.Ext(release.FilePath))
			if base == req.PackageName && !release.Removed {
				selected = release
				break
			}
		}
		if selected == nil {
			emit("Release not found: " + req.PackageName)
			h.finishTask(serverID, task.ID, fmt.Errorf("release not found"))
			return
		}

		if _, err := os.Stat(selected.FilePath); err != nil {
			emit("Release file missing: " + selected.FilePath)
			h.finishTask(serverID, task.ID, err)
			return
		}

		installDir := "~/hytale-server"
		serviceUser := "hytale"
		useSudo := true
		if serverDef.Dependencies.Configured {
			if serverDef.Dependencies.InstallDir != "" {
				installDir = serverDef.Dependencies.InstallDir
			}
			if serverDef.Dependencies.ServiceUser != "" {
				serviceUser = serverDef.Dependencies.ServiceUser
			}
			useSudo = serverDef.Dependencies.UseSudo
		}
		if req.InstallDir != nil && strings.TrimSpace(*req.InstallDir) != "" {
			installDir = strings.TrimSpace(*req.InstallDir)
		}
		if req.ServiceUser != nil && strings.TrimSpace(*req.ServiceUser) != "" {
			serviceUser = strings.TrimSpace(*req.ServiceUser)
		}
		if req.UseSudo != nil {
			useSudo = *req.UseSudo
		}

		userHome, err := resolveUserHome(conn.Client, serviceUser)
		if err != nil {
			emit("Failed to resolve user home: " + err.Error())
			h.finishTask(serverID, task.ID, err)
			return
		}
		installDir = resolveTilde(installDir, userHome)
		installDirUnix := toUnixPath(installDir)

		remoteZip := fmt.Sprintf("/tmp/%s.zip", req.PackageName)
		skipUpload := false
		expectedHash := strings.TrimSpace(selected.SHA256)
		if expectedHash != "" {
			remoteHash, hashErr := remoteSHA256(conn.Client, remoteZip)
			if hashErr != nil {
				emit("Remote hash check skipped: " + hashErr.Error())
			} else if remoteHash != "" && strings.EqualFold(remoteHash, expectedHash) {
				skipUpload = true
				emit("Existing package verified by SHA256. Skipping upload.")
			} else if remoteHash != "" {
				emit("Existing package hash mismatch. Re-uploading.")
			}
		} else {
			emit("No SHA256 available for package; uploading fresh copy.")
		}
		if !skipUpload {
			if err := uploadFile(conn.Client, selected.FilePath, remoteZip, emit); err != nil {
				emit("Upload failed: " + err.Error())
				h.finishTask(serverID, task.ID, err)
				return
			}
		}

		javaXms := "10G"
		javaXmx := "10G"
		javaMetaspace := "2560M"
		enableStringDedup := true
		enableAOT := true
		enableBackup := true
		backupDir := path.Join(installDirUnix, "Backups")
		backupFrequency := 30
		assetsPath := path.Join(installDirUnix, "Assets.zip")
		extraJavaArgs := ""
		extraServerArgs := ""

		if req.JavaXms != nil {
			javaXms = strings.TrimSpace(*req.JavaXms)
		}
		if req.JavaXmx != nil {
			javaXmx = strings.TrimSpace(*req.JavaXmx)
		}
		if req.JavaMetaspace != nil {
			javaMetaspace = strings.TrimSpace(*req.JavaMetaspace)
		}
		if req.EnableStringDedup != nil {
			enableStringDedup = *req.EnableStringDedup
		}
		if req.EnableAOT != nil {
			enableAOT = *req.EnableAOT
		}
		if req.EnableBackup != nil {
			enableBackup = *req.EnableBackup
		}
		if req.BackupDir != nil && strings.TrimSpace(*req.BackupDir) != "" {
			backupDir = strings.TrimSpace(*req.BackupDir)
		}
		if req.BackupFrequency != nil {
			backupFrequency = *req.BackupFrequency
		}
		if req.AssetsPath != nil && strings.TrimSpace(*req.AssetsPath) != "" {
			assetsPath = strings.TrimSpace(*req.AssetsPath)
		}
		if req.ExtraJavaArgs != nil {
			extraJavaArgs = strings.TrimSpace(*req.ExtraJavaArgs)
		}
		if req.ExtraServerArgs != nil {
			extraServerArgs = strings.TrimSpace(*req.ExtraServerArgs)
		}

		backupDir = toUnixPath(backupDir)
		assetsPath = toUnixPath(assetsPath)

		script := ServerReleaseDeployScript
		script = strings.ReplaceAll(script, "{{SERVICE_USER}}", escapeForScript(serviceUser))
		script = strings.ReplaceAll(script, "{{INSTALL_DIR}}", escapeForScriptPath(installDirUnix))
		script = strings.ReplaceAll(script, "{{PACKAGE_PATH}}", escapeForScript(remoteZip))
		script = strings.ReplaceAll(script, "{{PACKAGE_SHA256}}", escapeForScript(strings.TrimSpace(selected.SHA256)))
		script = strings.ReplaceAll(script, "{{USE_SUDO}}", boolToScript(useSudo))
		script = strings.ReplaceAll(script, "{{ENABLE_STRING_DEDUP}}", boolToScript(enableStringDedup))
		script = strings.ReplaceAll(script, "{{ENABLE_AOT}}", boolToScript(enableAOT))
		script = strings.ReplaceAll(script, "{{ENABLE_BACKUP}}", boolToScript(enableBackup))
		script = strings.ReplaceAll(script, "{{BACKUP_DIR}}", escapeForScriptPath(backupDir))
		script = strings.ReplaceAll(script, "{{BACKUP_FREQUENCY}}", fmt.Sprintf("%d", backupFrequency))
		script = strings.ReplaceAll(script, "{{ASSETS_PATH}}", escapeForScriptPath(assetsPath))
		script = strings.ReplaceAll(script, "{{JAVA_XMS}}", escapeForScript(javaXms))
		script = strings.ReplaceAll(script, "{{JAVA_XMX}}", escapeForScript(javaXmx))
		script = strings.ReplaceAll(script, "{{JAVA_METASPACE}}", escapeForScript(javaMetaspace))
		script = strings.ReplaceAll(script, "{{EXTRA_JAVA_ARGS}}", escapeForScript(extraJavaArgs))
		script = strings.ReplaceAll(script, "{{EXTRA_SERVER_ARGS}}", escapeForScript(extraServerArgs))
		script = strings.ReplaceAll(script, "{{SERVER_DIR}}", escapeForScriptPath(path.Join(installDirUnix, "Server")))

		emit("Extracting and configuring release...")
		writer := newLineSinkWriter(emit)
		err = conn.Client.StreamCommand(bashDollarQuotedCommand(script), writer, writer)
		writer.FlushRemaining()
		if err != nil {
			emit("Deploy failed: " + err.Error())
			h.finishTask(serverID, task.ID, err)
			return
		}

		emit("Release deployment complete.")
		h.finishTask(serverID, task.ID, nil)
	}()
}

func (h *ServerHandler) HandleServerTasksWebSocket(c *gin.Context) {
	serverID := c.Param("id")
	if _, found := h.serverManager.GetByID(serverID); !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	userClaims, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	claims := userClaims.(*auth.Claims)

	upgrader := buildUpgrader(h.config.Security.CORS.AllowedOrigins)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[Tasks] Failed to upgrade WebSocket: %v", err)
		return
	}

	room := fmt.Sprintf("server-tasks:%s", serverID)
	client := &ws.Client{
		ID:       fmt.Sprintf("tasks-%s-%d", serverID, time.Now().UnixNano()),
		UserID:   claims.UserID,
		Username: claims.Username,
		Conn:     conn,
		Room:     room,
		Send:     make(chan *ws.Message, 256),
		Hub:      h.hub,
	}

	h.hub.Register <- client

	go func() {
		for _, entry := range h.getTaskStreamBuffer(serverID).GetLines() {
			client.SendMessage("task_output", map[string]interface{}{
				"line":       entry.Line,
				"server_id":  serverID,
				"task_id":    entry.TaskID,
				"task":       entry.Task,
				"timestamp":  entry.Timestamp,
				"historical": true,
			})
		}
	}()

	go func() {
		for _, record := range h.listTasks(serverID) {
			h.broadcastTaskStatus(serverID, record, true)
		}
	}()

	go client.WritePump()
	go client.ReadPump()
}

func (h *ServerHandler) StartTransferBenchmark(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req TransferBenchmarkRequest
	_ = c.ShouldBindJSON(&req)

	params := normalizeBenchmarkRequest(req)
	go func() {
		task := h.startTask(serverID, "transfer-benchmark")
		err := h.runTransferBenchmark(serverID, serverDef, params, func(line string) {
			h.appendTaskStreamLine(serverID, task.ID, task.Task, line)
		})
		if err != nil {
			h.finishTask(serverID, task.ID, err)
			return
		}
		h.finishTask(serverID, task.ID, nil)
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Benchmark started"})
}

type benchmarkParams struct {
	sizeMB      int
	blockMB     int
	removeAfter bool
}

func normalizeBenchmarkRequest(req TransferBenchmarkRequest) benchmarkParams {
	sizeMB := 64
	blockMB := 4
	removeAfter := true
	if req.SizeMB != nil {
		sizeMB = *req.SizeMB
	}
	if req.BlockMB != nil {
		blockMB = *req.BlockMB
	}
	if req.RemoveAfter != nil {
		removeAfter = *req.RemoveAfter
	}
	if sizeMB < 1 {
		sizeMB = 1
	}
	if sizeMB > 2048 {
		sizeMB = 2048
	}
	if blockMB < 1 {
		blockMB = 1
	}
	if blockMB > 64 {
		blockMB = 64
	}
	return benchmarkParams{sizeMB: sizeMB, blockMB: blockMB, removeAfter: removeAfter}
}

func (h *ServerHandler) runTransferBenchmark(serverID string, serverDef config.ServerDefinition, params benchmarkParams, emit func(string)) error {
	if emit == nil {
		emit = func(string) {}
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		return fmt.Errorf("SSH key path is required")
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		return fmt.Errorf("SSH password is required")
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to connect via SSH: %w", err)
	}

	emit("Starting transfer benchmark...")
	emit(fmt.Sprintf("Target size: %d MB, block size: %d MB", params.sizeMB, params.blockMB))

	sftpClient, err := conn.Client.NewSFTPWithOptions(
		sftp.MaxPacketUnchecked(131072),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(64),
	)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	remotePath := fmt.Sprintf("/tmp/hsm-transfer-benchmark-%d.bin", time.Now().UnixNano())
	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()
	_ = remoteFile.Chmod(0644)

	totalBytes := int64(params.sizeMB) * 1024 * 1024
	blockBytes := int64(params.blockMB) * 1024 * 1024
	buffer := make([]byte, blockBytes)

	start := time.Now()
	var totalWritten int64

	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		var written int64
		for written < totalBytes {
			remaining := totalBytes - written
			writeSize := blockBytes
			if remaining < writeSize {
				writeSize = remaining
			}
			if _, err := remoteFile.Write(buffer[:writeSize]); err != nil {
				errCh <- err
				return
			}
			written += writeSize
			atomic.StoreInt64(&totalWritten, written)
		}
	}()

	progressTicker := time.NewTicker(2 * time.Second)
	defer progressTicker.Stop()

	for {
		select {
		case err := <-errCh:
			return fmt.Errorf("write failed: %w", err)
		case <-doneCh:
			current := atomic.LoadInt64(&totalWritten)
			elapsed := time.Since(start).Seconds()
			mbps := 0.0
			if elapsed > 0 {
				mbps = (float64(current) / (1024 * 1024)) / elapsed
			}
			emit(fmt.Sprintf("Benchmark complete: %d bytes in %.2fs (avg %.2f MB/s)", current, elapsed, mbps))
			goto cleanup
		case <-progressTicker.C:
			current := atomic.LoadInt64(&totalWritten)
			elapsed := time.Since(start).Seconds()
			percent := 0.0
			if totalBytes > 0 {
				percent = float64(current) / float64(totalBytes) * 100
			}
			mbps := 0.0
			if elapsed > 0 {
				mbps = (float64(current) / (1024 * 1024)) / elapsed
			}
			emit(fmt.Sprintf("Progress: %.1f%% (%d / %d bytes) %.2f MB/s | %.0fs elapsed", percent, current, totalBytes, mbps, elapsed))
		}
	}

cleanup:
	if params.removeAfter {
		if err := sftpClient.Remove(remotePath); err != nil {
			emit("Cleanup failed: " + err.Error())
		} else {
			emit("Cleanup complete: removed " + remotePath)
		}
	} else {
		emit("Benchmark file retained: " + remotePath)
	}

	return nil
}

func (h *ServerHandler) getTaskStreamBuffer(serverID string) *taskStreamBuffer {
	h.streamMu.Lock()
	defer h.streamMu.Unlock()
	if buf, ok := h.streamBuffers[serverID]; ok {
		return buf
	}
	buf := newTaskStreamBuffer(1000)
	h.streamBuffers[serverID] = buf
	return buf
}

func (h *ServerHandler) getServerTaskState(serverID string) *serverTaskState {
	if state, ok := h.tasks[serverID]; ok {
		return state
	}
	state := &serverTaskState{order: make([]string, 0, 10), tasks: make(map[string]*taskRecord)}
	h.tasks[serverID] = state
	return state
}

func (h *ServerHandler) startTask(serverID string, task string) *taskRecord {
	h.tasksMu.Lock()
	state := h.getServerTaskState(serverID)
	id := fmt.Sprintf("task-%s-%d", serverID, time.Now().UnixNano())
	record := &taskRecord{
		ID:        id,
		Task:      task,
		Status:    taskStatusRunning,
		StartedAt: time.Now(),
	}
	state.tasks[id] = record
	state.order = append(state.order, id)
	if len(state.order) > 50 {
		oldest := state.order[0]
		state.order = state.order[1:]
		delete(state.tasks, oldest)
	}
	h.tasksMu.Unlock()

	h.broadcastTaskStatus(serverID, record, false)
	return record
}

func (h *ServerHandler) updateTaskLine(serverID string, taskID string, line string) {
	h.tasksMu.Lock()
	state, ok := h.tasks[serverID]
	if !ok {
		h.tasksMu.Unlock()
		return
	}
	if record, ok := state.tasks[taskID]; ok {
		record.LastLine = line
	}
	h.tasksMu.Unlock()
}

func (h *ServerHandler) finishTask(serverID string, taskID string, err error) {
	now := time.Now()
	h.tasksMu.Lock()
	state, ok := h.tasks[serverID]
	if !ok {
		h.tasksMu.Unlock()
		return
	}
	record, ok := state.tasks[taskID]
	if !ok {
		h.tasksMu.Unlock()
		return
	}
	record.FinishedAt = &now
	if err != nil {
		record.Status = taskStatusFailed
		record.Error = err.Error()
	} else {
		record.Status = taskStatusComplete
	}
	h.tasksMu.Unlock()

	h.broadcastTaskStatus(serverID, record, false)
}

func (h *ServerHandler) broadcastTaskStatus(serverID string, record *taskRecord, historical bool) {
	payload := map[string]interface{}{
		"task_id":    record.ID,
		"task":       record.Task,
		"status":     record.Status,
		"server_id":  serverID,
		"started_at": record.StartedAt,
		"last_line":  record.LastLine,
		"historical": historical,
	}
	if record.FinishedAt != nil {
		payload["finished_at"] = *record.FinishedAt
	}
	if record.Error != "" {
		payload["error"] = record.Error
	}

	h.hub.BroadcastToRoom(fmt.Sprintf("server-tasks:%s", serverID), &ws.Message{
		Type:      "task_status",
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

func (h *ServerHandler) listTasks(serverID string) []*taskRecord {
	h.tasksMu.Lock()
	defer h.tasksMu.Unlock()
	state, ok := h.tasks[serverID]
	if !ok {
		return []*taskRecord{}
	}
	items := make([]*taskRecord, 0, len(state.order))
	for _, id := range state.order {
		if record, ok := state.tasks[id]; ok {
			clone := *record
			items = append(items, &clone)
		}
	}
	return items
}

func (h *ServerHandler) appendTaskStreamLine(serverID string, taskID string, task string, line string) {
	entry := taskStreamLine{Line: line, Task: task, TaskID: taskID, Timestamp: time.Now()}
	h.getTaskStreamBuffer(serverID).Add(entry)
	h.updateTaskLine(serverID, taskID, line)
	h.hub.BroadcastToRoom(fmt.Sprintf("server-tasks:%s", serverID), &ws.Message{
		Type: "task_output",
		Payload: map[string]interface{}{
			"line":      entry.Line,
			"server_id": serverID,
			"task_id":   entry.TaskID,
			"task":      entry.Task,
			"timestamp": entry.Timestamp,
		},
		Timestamp: entry.Timestamp,
	})
}

func resolveUserHome(client *ssh.Client, user string) (string, error) {
	cmd := fmt.Sprintf("getent passwd %s | cut -d: -f6", user)
	output, err := client.RunCommand(bashDollarQuotedCommand(cmd))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func resolveTilde(path string, home string) string {
	if strings.HasPrefix(path, "~") {
		return strings.Replace(path, "~", home, 1)
	}
	return path
}

func (h *ServerHandler) detectListeningJavaProcess(serverID string, serverDef config.ServerDefinition) (int, string, error) {
	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		Password:        serverDef.Connection.Password,
		KeyPath:         serverDef.Connection.KeyPath,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if sshConfig.AuthMethod == "key" && sshConfig.KeyPath == "" {
		return 0, "", fmt.Errorf("SSH key path is required")
	}
	if sshConfig.AuthMethod == "password" && sshConfig.Password == "" {
		return 0, "", fmt.Errorf("SSH password is required")
	}

	conn, err := h.sshPool.GetConnection(serverID, sshConfig)
	if err != nil {
		return 0, "", err
	}

	installDir := strings.TrimSpace(serverDef.Dependencies.InstallDir)
	serviceUser := strings.TrimSpace(serverDef.Dependencies.ServiceUser)
	if installDir != "" && strings.HasPrefix(installDir, "~") {
		if serviceUser == "" {
			serviceUser = serverDef.Connection.Username
		}
		if home, err := resolveUserHome(conn.Client, serviceUser); err == nil && home != "" {
			installDir = resolveTilde(installDir, home)
		}
	}

	needle := []string{"HytaleServer.jar"}
	if installDir != "" {
		needle = append(needle, installDir)
	}

	output, err := conn.Client.RunCommand("ss -H -lpun; ss -H -lptn")
	if err != nil {
		output, err = conn.Client.RunCommand("netstat -lpun 2>/dev/null; netstat -lptn 2>/dev/null")
		if err != nil {
			return 0, "", err
		}
	}

	pidRegex := regexp.MustCompile(`pid=(\d+)`)
	portRegex := regexp.MustCompile(`:(\d+)`)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if !strings.Contains(strings.ToLower(line), "java") {
			continue
		}
		pidMatch := pidRegex.FindStringSubmatch(line)
		if len(pidMatch) < 2 {
			continue
		}
		pidValue, err := strconv.Atoi(pidMatch[1])
		if err != nil || pidValue <= 0 {
			continue
		}
		args, _ := conn.Client.RunCommand(fmt.Sprintf("ps -p %d -o args=", pidValue))
		if args == "" || !containsAny(args, needle) {
			continue
		}
		portMatch := portRegex.FindAllStringSubmatch(line, -1)
		port := ""
		if len(portMatch) > 0 {
			port = portMatch[len(portMatch)-1][1]
		}
		return pidValue, port, nil
	}

	return 0, "", nil
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func remoteSHA256(client *ssh.Client, path string) (string, error) {
	cmd := fmt.Sprintf(
		"if [ ! -f '%s' ]; then\n"+
			"  exit 2\n"+
			"fi\n"+
			"if command -v sha256sum >/dev/null 2>&1; then\n"+
			"  sha256sum '%s' | awk '{print $1}'\n"+
			"elif command -v shasum >/dev/null 2>&1; then\n"+
			"  shasum -a 256 '%s' | awk '{print $1}'\n"+
			"elif command -v openssl >/dev/null 2>&1; then\n"+
			"  openssl dgst -sha256 '%s' | awk '{print $2}'\n"+
			"else\n"+
			"  exit 127\n"+
			"fi",
		path, path, path, path,
	)
	output, err := client.RunCommand(bashDollarQuotedCommand(strings.TrimSpace(cmd)))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func uploadFile(client *ssh.Client, localPath string, remotePath string, emit func(string)) error {
	sftpClient, err := client.NewSFTPWithOptions(
		sftp.MaxPacketUnchecked(131072),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(64),
	)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	_ = sftpClient.MkdirAll(filepath.Dir(remotePath))

	localFile, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer localFile.Close()

	stat, err := localFile.Stat()
	if err != nil {
		return err
	}
	fileSize := stat.Size()
	start := time.Now()

	emit("Uploading package...")
	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return err
	}
	defer remoteFile.Close()
	_ = remoteFile.Chmod(0644)

	buffer := make([]byte, 8*1024*1024)
	var totalWritten int64
	lastReport := time.Now()
	lastKeepAlive := time.Now()
	for {
		n, readErr := localFile.Read(buffer)
		if n > 0 {
			if _, err := remoteFile.Write(buffer[:n]); err != nil {
				return err
			}
			totalWritten += int64(n)
			if time.Since(lastReport) > 2*time.Second {
				percent := float64(totalWritten) / float64(fileSize) * 100
				elapsed := time.Since(start).Seconds()
				mbps := 0.0
				if elapsed > 0 {
					mbps = (float64(totalWritten) / (1024 * 1024)) / elapsed
				}
				emit(fmt.Sprintf("Upload progress: %.1f%% (%d / %d bytes) %.2f MB/s", percent, totalWritten, fileSize, mbps))
				lastReport = time.Now()
			}
		}
		if time.Since(lastKeepAlive) > 10*time.Second {
			elapsed := time.Since(start).Seconds()
			mbps := 0.0
			if elapsed > 0 {
				mbps = (float64(totalWritten) / (1024 * 1024)) / elapsed
			}
			emit(fmt.Sprintf("Upload still running... avg %.2f MB/s", mbps))
			lastKeepAlive = time.Now()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	emit(fmt.Sprintf("Upload complete: %d bytes", totalWritten))
	return nil
}

func parseDependencyCheckOutput(output string) DependenciesCheckResponse {
	resp := DependenciesCheckResponse{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := parts[0]
		val := ""
		if len(parts) > 1 {
			val = parts[1]
		}
		switch key {
		case "JAVA_OK":
			resp.JavaOK = val == "1"
		case "JAVA_LINE":
			resp.JavaLine = val
		case "USER_OK":
			resp.UserOK = val == "1"
		case "USER_HOME":
			resp.UserHome = val
		case "DIR_OK":
			resp.DirOK = val == "1"
		case "DIR_PATH":
			resp.DirPath = val
		}
	}
	return resp
}

func boolToScript(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func escapeForScript(value string) string {
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func escapeForScriptPath(value string) string {
	return escapeForScript(toUnixPath(value))
}

func toUnixPath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

type lineSinkWriter struct {
	mu      sync.Mutex
	pending string
	onLine  func(string)
}

func newLineSinkWriter(onLine func(string)) *lineSinkWriter {
	return &lineSinkWriter{onLine: onLine}
}

func (sw *lineSinkWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	chunk := sw.pending + string(p)
	lines := strings.Split(chunk, "\n")
	for i := 0; i < len(lines)-1; i++ {
		if sw.onLine != nil {
			sw.onLine(lines[i])
		}
	}
	sw.pending = lines[len(lines)-1]
	return len(p), nil
}

func (sw *lineSinkWriter) FlushRemaining() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.pending != "" {
		if sw.onLine != nil {
			sw.onLine(sw.pending)
		}
		sw.pending = ""
	}
}

func appendOutput(builder *strings.Builder, line string, limit int) {
	if builder.Len() >= limit {
		return
	}

	remaining := limit - builder.Len()
	if len(line)+1 > remaining {
		builder.WriteString(line[:remaining])
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n")
	}
	builder.WriteString(line)
}

func getUserIDFromContext(c *gin.Context) *int64 {
	userClaims, ok := c.Get("user")
	if !ok {
		return nil
	}
	claims, ok := userClaims.(*auth.Claims)
	if !ok || claims == nil {
		return nil
	}
	userID := claims.UserID
	return &userID
}

// StartServer starts a game server
func (h *ServerHandler) StartServer(c *gin.Context) {
	serverID := c.Param("id")
	userID := getUserIDFromContext(c)

	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req models.ServerStartRequest
	if c.Request != nil && c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	serverConfig := h.createServerConfig(&serverDef)
	if hasStartOverrides(&req) {
		customConfig, err := h.createStartServerConfig(&serverDef, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		serverConfig = customConfig
	}

	h.pendingOps.Add(1)
	go func() {
		defer h.pendingOps.Done()
		err := h.lifecycleManager.StartServer(serverID, serverConfig)
		if err != nil {
			log.Printf("[API] Failed to start server %s: %v", serverID, err)
			h.activityLogger.LogServerStart(serverID, userID, false, err.Error())
		} else {
			log.Printf("[API] Server %s started successfully", serverID)
			h.activityLogger.LogServerStart(serverID, userID, true, "")
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Server start initiated", "server_id": serverID, "status": "starting"})
}

// StopServer stops a game server
func (h *ServerHandler) StopServer(c *gin.Context) {
	serverID := c.Param("id")
	userID := getUserIDFromContext(c)
	graceful := c.DefaultQuery("graceful", "true") == "true"

	log.Printf("[StopServer] Request received for server %s (user: %s, graceful: %v)", serverID, userID, graceful)

	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		log.Printf("[StopServer] Server %s not found", serverID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	serverConfig := h.createServerConfig(&serverDef)

	log.Printf("[StopServer] Initiating stop for server %s in background", serverID)
	h.pendingOps.Add(1)
	go func() {
		defer h.pendingOps.Done()
		err := h.lifecycleManager.StopServer(serverID, serverConfig, graceful)
		if err != nil {
			log.Printf("[API] Failed to stop server %s: %v", serverID, err)
			h.activityLogger.LogServerStop(serverID, userID, graceful, false, err.Error())
		} else {
			log.Printf("[API] Server %s stopped successfully", serverID)
			h.activityLogger.LogServerStop(serverID, userID, graceful, true, "")
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Server stop initiated", "server_id": serverID, "status": "stopping", "graceful": graceful})
}

// RestartServer restarts a game server
func (h *ServerHandler) RestartServer(c *gin.Context) {
	serverID := c.Param("id")
	userID := getUserIDFromContext(c)
	graceful := c.DefaultQuery("graceful", "true") == "true"

	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	var req models.ServerStartRequest
	if c.Request != nil && c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	serverConfig := h.createServerConfig(&serverDef)
	if hasStartOverrides(&req) {
		customConfig, err := h.createStartServerConfig(&serverDef, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		serverConfig = customConfig
	}

	h.pendingOps.Add(1)
	go func() {
		defer h.pendingOps.Done()
		err := h.lifecycleManager.RestartServer(serverID, serverConfig, graceful)
		if err != nil {
			log.Printf("[API] Failed to restart server %s: %v", serverID, err)
			h.activityLogger.LogServerRestart(serverID, userID, graceful, false, err.Error())
		} else {
			log.Printf("[API] Server %s restarted successfully", serverID)
			h.activityLogger.LogServerRestart(serverID, userID, graceful, true, "")
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Server restart initiated", "server_id": serverID, "status": "restarting", "graceful": graceful})
}

// GetServerStatus returns the current status of a server
func (h *ServerHandler) GetServerStatus(c *gin.Context) {
	serverID := c.Param("id")
	serverDef, found := h.serverManager.GetByID(serverID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	sessionName := server.SafeSessionName(serverID)

	// Comprehensive health check
	health := h.performHealthCheck(serverID, serverDef, sessionName)

	// Determine overall status based on health check
	var overallStatus string
	var errorMsg string
	
	if health.ProcessStatus.Running {
		overallStatus = server.StatusOnline
	} else {
		overallStatus = server.StatusOffline
		if !health.SSHStatus.Connected {
			errorMsg = "SSH connection failed"
		} else {
			errorMsg = "Server process not running"
		}
	}

	status := models.ServerStatus{
		ServerID:         serverID,
		Name:             serverDef.Name,
		Status:           overallStatus,
		ConnectionStatus: health.ConnectionStatus,
		PlayerCount:      0,
		MaxPlayers:       20,
		Uptime:           health.ProcessStatus.UptimeSeconds,
		LastChecked:      time.Now(),
		ErrorMessage:     errorMsg,
		HealthCheck:      &health,
	}

	c.JSON(http.StatusOK, status)
}

// ExecuteCommand executes a console command on a server
func (h *ServerHandler) ExecuteCommand(c *gin.Context) {
	serverID := c.Param("id")
	userID := getUserIDFromContext(c)

	var req models.CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, found := h.serverManager.GetByID(serverID); !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	sessionName := server.SafeSessionName(serverID)

	err := h.processManager.SendCommand(serverID, sessionName, req.Command)

	if err != nil {
		log.Printf("[API] Failed to execute command on %s: %v", serverID, err)
		h.activityLogger.LogCommandExecute(serverID, userID, req.Command, false, "", err.Error())
		c.JSON(http.StatusInternalServerError, models.CommandResponse{Success: false, Error: err.Error()})
		return
	}

	h.activityLogger.LogCommandExecute(serverID, userID, req.Command, true, "", "")
	c.JSON(http.StatusOK, models.CommandResponse{Success: true, Output: "Command sent successfully"})
}

// createServerConfig creates a server configuration from server definition
func (h *ServerHandler) createServerConfig(def *config.ServerDefinition) *server.ServerConfig {
	// SECURITY: Always generate session name from ID to prevent command injection
	// User provided ScreenSessionName is ignored
	sessionName := server.SafeSessionName(def.ID)

	javaArgs := []string{}
	if def.Server.JavaArgs != "" {
		javaArgs = splitArgs(def.Server.JavaArgs)
	}

	sshConfig := &ssh.ClientConfig{
		Host:            def.Connection.Host,
		Port:            def.Connection.Port,
		Username:        def.Connection.Username,
		AuthMethod:      def.Connection.AuthMethod,
		Password:        def.Connection.Password,
		KeyPath:         def.Connection.KeyPath,
		Timeout:         10 * time.Second,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	// If no host is specified, assume local execution (nil SSHConfig)
	if def.Connection.Host == "" || def.Connection.Host == "localhost" || def.Connection.Host == "127.0.0.1" {
		sshConfig = nil
	}

	return &server.ServerConfig{
		ServerID:       def.ID,
		SessionName:    sessionName,
		WorkingDir:     def.Server.WorkingDirectory,
		Executable:     def.Server.Executable,
		JavaArgs:       javaArgs,
		ServerArgs:     []string{"nogui"},
		LogFile:        fmt.Sprintf("%s/console.log", def.Server.WorkingDirectory),
		StartupTimeout: 60 * time.Second,
		StopTimeout:    60 * time.Second,
		StopCommands:   []string{"stop"},
		StopWarnings: []server.StopWarning{
			{Delay: 0, Message: "Server shutting down in 60 seconds..."},
			{Delay: 30 * time.Second, Message: "Server shutting down in 30 seconds..."},
			{Delay: 20 * time.Second, Message: "Server shutting down in 10 seconds..."},
		},
		SSHConfig:  sshConfig,
		RunAsUser:  def.Dependencies.ServiceUser,
		UseSudo:    def.Dependencies.UseSudo,
	}
}

func hasStartOverrides(req *models.ServerStartRequest) bool {
	return req.InstallDir != nil || req.ServiceUser != nil || req.UseSudo != nil ||
		req.JavaXms != nil || req.JavaXmx != nil || req.JavaMetaspace != nil ||
		req.EnableStringDedup != nil || req.EnableAot != nil || req.EnableBackup != nil ||
		req.BackupDir != nil || req.BackupFrequency != nil || req.AssetsPath != nil ||
		req.ExtraJavaArgs != nil || req.ExtraServerArgs != nil
}

func (h *ServerHandler) createStartServerConfig(def *config.ServerDefinition, req *models.ServerStartRequest) (*server.ServerConfig, error) {
	installDir := "~/hytale-server"
	serviceUser := "hytale"
	useSudo := true
	if def.Dependencies.Configured {
		if def.Dependencies.InstallDir != "" {
			installDir = def.Dependencies.InstallDir
		}
		if def.Dependencies.ServiceUser != "" {
			serviceUser = def.Dependencies.ServiceUser
		}
		useSudo = def.Dependencies.UseSudo
	}
	if req.InstallDir != nil && strings.TrimSpace(*req.InstallDir) != "" {
		installDir = strings.TrimSpace(*req.InstallDir)
	}
	if req.ServiceUser != nil && strings.TrimSpace(*req.ServiceUser) != "" {
		serviceUser = strings.TrimSpace(*req.ServiceUser)
	}
	if req.UseSudo != nil {
		useSudo = *req.UseSudo
	}
	if !isSafeUsername(serviceUser) {
		return nil, fmt.Errorf("service_user contains invalid characters")
	}
	if serviceUser != "" {
		useSudo = true
	}

	javaXms := "10G"
	javaXmx := "10G"
	javaMetaspace := "2560M"
	enableStringDedup := true
	enableAot := true
	enableBackup := true
	backupDir := path.Join(toUnixPath(installDir), "Backups")
	backupFrequency := "30m"
	assetsPath := path.Join(toUnixPath(installDir), "Assets.zip")
	extraJavaArgs := ""
	extraServerArgs := ""

	if req.JavaXms != nil {
		javaXms = strings.TrimSpace(*req.JavaXms)
	}
	if req.JavaXmx != nil {
		javaXmx = strings.TrimSpace(*req.JavaXmx)
	}
	if req.JavaMetaspace != nil {
		javaMetaspace = strings.TrimSpace(*req.JavaMetaspace)
	}
	if req.EnableStringDedup != nil {
		enableStringDedup = *req.EnableStringDedup
	}
	if req.EnableAot != nil {
		enableAot = *req.EnableAot
	}
	if req.EnableBackup != nil {
		enableBackup = *req.EnableBackup
	}
	if req.BackupDir != nil && strings.TrimSpace(*req.BackupDir) != "" {
		backupDir = toUnixPath(strings.TrimSpace(*req.BackupDir))
	}
	if req.BackupFrequency != nil {
		backupFrequency = *req.BackupFrequency
	}
	if req.AssetsPath != nil && strings.TrimSpace(*req.AssetsPath) != "" {
		assetsPath = toUnixPath(strings.TrimSpace(*req.AssetsPath))
	}
	if req.ExtraJavaArgs != nil {
		extraJavaArgs = strings.TrimSpace(*req.ExtraJavaArgs)
	}
	if req.ExtraServerArgs != nil {
		extraServerArgs = strings.TrimSpace(*req.ExtraServerArgs)
	}

	if err := validateArgsString(extraJavaArgs); err != nil {
		return nil, fmt.Errorf("extra_java_args %v", err)
	}
	if err := validateArgsString(extraServerArgs); err != nil {
		return nil, fmt.Errorf("extra_server_args %v", err)
	}

	serverDir := path.Join(toUnixPath(installDir), "Server")
	executable := path.Join(serverDir, "HytaleServer.jar")

	javaArgs := []string{}
	if javaXms != "" {
		javaArgs = append(javaArgs, "-Xms"+javaXms)
	}
	if javaXmx != "" {
		javaArgs = append(javaArgs, "-Xmx"+javaXmx)
	}
	if javaMetaspace != "" {
		javaArgs = append(javaArgs, "-XX:MaxMetaspaceSize="+javaMetaspace)
	}
	if enableStringDedup {
		javaArgs = append(javaArgs, "-XX:+UseStringDeduplication")
	}
	if enableAot {
		javaArgs = append(javaArgs, "-XX:AOTCache=HytaleServer.aot")
	}
	if extraJavaArgs != "" {
		javaArgs = append(javaArgs, splitArgs(extraJavaArgs)...)
	}

	serverArgs := []string{"--assets", assetsPath}
	if enableBackup {
		serverArgs = append(serverArgs, "--backup", "--backup-dir", backupDir, "--backup-frequency", fmt.Sprintf("%d", backupFrequency))
	}
	if extraServerArgs != "" {
		serverArgs = append(serverArgs, splitArgs(extraServerArgs)...)
	}

	sshConfig := &ssh.ClientConfig{
		Host:            def.Connection.Host,
		Port:            def.Connection.Port,
		Username:        def.Connection.Username,
		AuthMethod:      def.Connection.AuthMethod,
		Password:        def.Connection.Password,
		KeyPath:         def.Connection.KeyPath,
		Timeout:         10 * time.Second,
		KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
	}

	if def.Connection.Host == "" || def.Connection.Host == "localhost" || def.Connection.Host == "127.0.0.1" {
		sshConfig = nil
	}

	return &server.ServerConfig{
		ServerID:       def.ID,
		SessionName:    server.SafeSessionName(def.ID),
		WorkingDir:     serverDir,
		Executable:     executable,
		JavaArgs:       javaArgs,
		ServerArgs:     serverArgs,
		LogFile:        path.Join(serverDir, "Logs", "console.log"),
		StartupTimeout: 90 * time.Second,
		StopTimeout:    60 * time.Second,
		StopCommands:   []string{"stop"},
		StopWarnings: []server.StopWarning{
			{Delay: 0, Message: "Server shutting down in 60 seconds..."},
			{Delay: 30 * time.Second, Message: "Server shutting down in 30 seconds..."},
			{Delay: 20 * time.Second, Message: "Server shutting down in 10 seconds..."},
		},
		SSHConfig: sshConfig,
		RunAsUser: serviceUser,
		UseSudo:   useSudo,
	}, nil
}

func validateArgsString(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "\n\r;|&`$()<>") {
		return fmt.Errorf("contains invalid characters")
	}
	return nil
}

func isSafeUsername(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (h *ServerHandler) persistSSHKey(serverID string, conn *config.ConnectionConfig) error {
	if conn == nil {
		return nil
	}
	if conn.AuthMethod != "key" || conn.KeyContent == "" {
		return nil
	}

	keysDir := filepath.Join(h.config.Storage.DataDir, "ssh_keys")
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return err
	}

	keyPath := filepath.Join(keysDir, fmt.Sprintf("%s.pem", serverID))

	manager, err := crypto.NewEncryptionManager()
	if err != nil {
		return err
	}

	encrypted, err := manager.EncryptSSHKey(conn.KeyContent)
	if err != nil {
		return err
	}

	encoded := base64.StdEncoding.EncodeToString(encrypted)
	payload := []byte("ENC1\n" + encoded)

	if err := os.WriteFile(keyPath, payload, 0600); err != nil {
		return err
	}

	conn.KeyPath = keyPath
	conn.KeyContent = ""
	return nil
}

// determineConnectionStatus determines the overall connection status of a server
func (h *ServerHandler) determineConnectionStatus(serverID string, serverDef config.ServerDefinition, statusInfo *server.ServerStatusInfo) models.ServerConnectionStatus {
	// Check if we have an active SSH connection
	conn := h.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		// No active SSH connection
		return models.StatusDisconnected
	}
	
	// SSH connected - check if Hytale process is running
	// First check the status detector result
	if statusInfo != nil && statusInfo.Status == server.StatusOnline {
		// Hytale is running with console streaming
		return models.StatusRunning
	}
	
	// Try to get agent state for more accurate process detection
	agentState := h.fetchAgentState(serverID, serverDef)
	if agentState != nil && len(agentState.JavaProcesses) > 0 {
		// Check if any Java process is running HytaleServer.jar
		for _, proc := range agentState.JavaProcesses {
			if strings.Contains(proc.CommandLine, "HytaleServer.jar") {
				log.Printf("[Status] Server %s: Found Java process via agent (PID %d)", serverID, proc.PID)
				return models.StatusRunning
			}
		}
	}
	
	// Fallback: check for any Java process with HytaleServer.jar via SSH
	output, err := conn.Client.RunCommand("pgrep -f 'HytaleServer.jar'")
	if err == nil && strings.TrimSpace(output) != "" {
		// Found a running Java process (Hytale server)
		log.Printf("[Status] Server %s: Found Java process via pgrep", serverID)
		return models.StatusRunning
	}
	
	// SSH works but no Hytale instance running
	return models.StatusOnline
}

// performHealthCheck performs a comprehensive health check on a server
func (h *ServerHandler) performHealthCheck(serverID string, serverDef config.ServerDefinition, sessionName string) HealthCheck {
	health := HealthCheck{
		SSHStatus: SSHHealthStatus{
			Host: serverDef.Connection.Host,
			Port: serverDef.Connection.Port,
		},
		AgentStatus: AgentHealthStatus{
			Available: false,
		},
		ProcessStatus: ProcessHealthStatus{
			Running: false,
		},
		ScreenStatus: ScreenHealthStatus{
			SessionName: sessionName,
		},
	}

	// Check SSH connectivity - try to get or establish connection
	conn := h.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		// No existing connection, try to establish one
		sshConfig := &ssh.ClientConfig{
			Host:            serverDef.Connection.Host,
			Port:            serverDef.Connection.Port,
			Username:        serverDef.Connection.Username,
			AuthMethod:      serverDef.Connection.AuthMethod,
			Password:        serverDef.Connection.Password,
			KeyPath:         serverDef.Connection.KeyPath,
			KnownHostsPath:  h.config.Security.SSH.KnownHostsPath,
			TrustOnFirstUse: h.config.Security.SSH.TrustOnFirstUse,
		}
		
		var err error
		conn, err = h.sshPool.GetConnection(serverID, sshConfig)
		if err != nil {
			health.SSHStatus.Error = fmt.Sprintf("Failed to connect: %v", err)
			health.ConnectionStatus = models.StatusDisconnected
			return health
		}
	}
	
	health.SSHStatus.Connected = true

	// Check agent status
	agentState := h.fetchAgentState(serverID, serverDef)
	if agentState != nil {
		health.AgentStatus.Available = true
		health.AgentStatus.Connected = true
		health.AgentStatus.JavaProcesses = agentState.JavaProcesses
		health.AgentStatus.ListeningPorts = agentState.Ports
		health.AgentStatus.Services = agentState.Services

		// Check for Hytale process via agent
		for _, proc := range agentState.JavaProcesses {
			if strings.Contains(proc.CommandLine, "HytaleServer.jar") {
				health.ProcessStatus.Running = true
				health.ProcessStatus.PID = proc.PID
				health.ProcessStatus.DetectionMethod = "agent"
				if len(proc.ListenPorts) > 0 {
					health.ProcessStatus.Port = fmt.Sprintf("%d", proc.ListenPorts[0])
				}
				break
			}
		}
	} else {
		health.AgentStatus.Error = "Agent not available or not responding"
	}

	// Check screen session status directly via SSH
	if health.SSHStatus.Connected {
		// Determine the service user to check screen sessions for
		serviceUser := "hytale" // default
		if serverDef.Dependencies.ServiceUser != "" {
			serviceUser = serverDef.Dependencies.ServiceUser
		}
		
		// Check if screen session exists - run as service user
		screenCheckCmd := fmt.Sprintf("sudo -u %s screen -list | grep '%s'", serviceUser, sessionName)
		output, err := conn.Client.RunCommand(screenCheckCmd)
		if err == nil && strings.TrimSpace(output) != "" {
			// grep found the session name in screen -list output
			health.ScreenStatus.SessionExists = true
			health.ScreenStatus.Streaming = true
			log.Printf("[HealthCheck] Server %s: Screen session '%s' detected for user %s: %s", 
				serverID, sessionName, serviceUser, strings.TrimSpace(output))
		} else {
			// Try alternate detection: check if session exists with direct screen -ls
			checkCmd := fmt.Sprintf("sudo -u %s screen -ls %s", serviceUser, sessionName)
			altOutput, altErr := conn.Client.RunCommand(checkCmd)
			if altErr == nil && !strings.Contains(altOutput, "No Sockets found") {
				health.ScreenStatus.SessionExists = true
				health.ScreenStatus.Streaming = true
				log.Printf("[HealthCheck] Server %s: Screen session '%s' found via screen -ls for user %s", 
					serverID, sessionName, serviceUser)
			} else {
				log.Printf("[HealthCheck] Server %s: Screen session '%s' not found for user %s. grep output: '%s', screen -ls: '%s'", 
					serverID, sessionName, serviceUser, strings.TrimSpace(output), strings.TrimSpace(altOutput))
			}
		}
	}

	// Also check via status detector for uptime info
	statusInfo, err := h.statusDetector.DetectStatus(serverID, sessionName)
	if err == nil && statusInfo.Status == server.StatusOnline {
		health.ScreenStatus.SessionExists = true
		health.ScreenStatus.Streaming = true
		
		if !health.ProcessStatus.Running {
			health.ProcessStatus.Running = true
			health.ProcessStatus.PID = statusInfo.PID
			health.ProcessStatus.UptimeSeconds = statusInfo.UptimeSeconds
			health.ProcessStatus.DetectionMethod = "screen"
		}
		
		if health.ProcessStatus.Running && statusInfo.UptimeSeconds > 0 {
			health.ProcessStatus.UptimeSeconds = statusInfo.UptimeSeconds
		}
	}

	// Fallback: check for process via SSH pgrep
	if !health.ProcessStatus.Running && health.SSHStatus.Connected {
		output, err := conn.Client.RunCommand("pgrep -f 'HytaleServer.jar'")
		if err == nil && strings.TrimSpace(output) != "" {
			pidStr := strings.TrimSpace(strings.Split(output, "\n")[0])
			if pid, err := strconv.Atoi(pidStr); err == nil {
				health.ProcessStatus.Running = true
				health.ProcessStatus.PID = pid
				health.ProcessStatus.DetectionMethod = "pgrep"
			}
		}
	}

	// Determine overall connection status
	if health.ProcessStatus.Running {
		health.ConnectionStatus = models.StatusRunning
	} else if health.SSHStatus.Connected {
		health.ConnectionStatus = models.StatusOnline
	} else {
		health.ConnectionStatus = models.StatusDisconnected
	}

	log.Printf("[HealthCheck] Server %s: SSH=%v, Agent=%v, Process=%v (PID=%d, Method=%s), Screen=%v",
		serverID,
		health.SSHStatus.Connected,
		health.AgentStatus.Connected,
		health.ProcessStatus.Running,
		health.ProcessStatus.PID,
		health.ProcessStatus.DetectionMethod,
		health.ScreenStatus.Streaming,
	)

	return health
}

// fetchAgentState fetches agent state from the agent, returns nil if unavailable
func (h *ServerHandler) fetchAgentState(serverID string, serverDef config.ServerDefinition) *AgentState {
	if strings.TrimSpace(serverDef.Connection.Host) == "" {
		return nil
	}

	clientCert, err := agentcert.GetClientCert(h.db.DB, "server-manager")
	if err != nil || clientCert == nil {
		return nil
	}

	cert, err := tls.X509KeyPair(clientCert.CertPEM, clientCert.KeyPEM)
	if err != nil {
		return nil
	}

	caPath := filepath.Join(h.config.Storage.DataDir, "agent-ca", "ca.crt")
	caData, err := os.ReadFile(caPath)
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return nil
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS12,
				RootCAs:      pool,
				Certificates: []tls.Certificate{cert},
			},
		},
	}

	url := fmt.Sprintf("https://%s:9443/state", strings.TrimSpace(serverDef.Connection.Host))
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var state AgentState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil
	}

	return &state
}

// splitArgs splits a command line string into arguments
func splitArgs(s string) []string {
	args := []string{}
	current := ""
	inQuote := false

	for _, char := range s {
		if char == '"' || char == '\'' {
			inQuote = !inQuote
		} else if char == ' ' && !inQuote {
			if current != "" {
				args = append(args, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}

	if current != "" {
		args = append(args, current)
	}

	return args
}
