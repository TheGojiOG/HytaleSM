package models

import "time"

// ServerConnectionStatus represents the connection and runtime status of a server
type ServerConnectionStatus string

const (
	// StatusDisconnected - SSH connection failed (Red)
	StatusDisconnected ServerConnectionStatus = "disconnected"
	// StatusOnline - SSH connected, server reachable, but no Hytale instance running (Yellow)
	StatusOnline ServerConnectionStatus = "online"
	// StatusRunning - SSH connected AND Hytale java process is running with console streaming (Green)
	StatusRunning ServerConnectionStatus = "running"
)

// ServerStatus represents the current status of a game server
type ServerStatus struct {
	ServerID         string                 `json:"server_id"`
	Name             string                 `json:"name"`
	Status           string                 `json:"status"` // "online", "offline", "starting", "stopping"
	ConnectionStatus ServerConnectionStatus `json:"connection_status"` // "disconnected", "online", "running"
	PlayerCount      int                    `json:"player_count"`
	MaxPlayers       int                    `json:"max_players"`
	Uptime           int64                  `json:"uptime"` // seconds
	LastChecked      time.Time              `json:"last_checked"`
	ErrorMessage     string                 `json:"error_message,omitempty"`
	HealthCheck      interface{}            `json:"health_check,omitempty"` // Detailed health information
}

// ServerMetrics represents server performance metrics
type ServerMetrics struct {
	ServerID    string    `json:"server_id"`
	CPUUsage    float64   `json:"cpu_usage"`
	MemoryUsed  int64     `json:"memory_used"`  // bytes
	MemoryTotal int64     `json:"memory_total"` // bytes
	DiskUsed    int64     `json:"disk_used"`    // bytes
	DiskTotal   int64     `json:"disk_total"`   // bytes
	NetworkRx   int64     `json:"network_rx"`   // bytes
	NetworkTx   int64     `json:"network_tx"`   // bytes
	Timestamp   time.Time `json:"timestamp"`
}

// Player represents an online player
type Player struct {
	Username  string    `json:"username"`
	UUID      string    `json:"uuid"`
	JoinedAt  time.Time `json:"joined_at"`
	IPAddress string    `json:"ip_address,omitempty"`
}

// CommandRequest represents a console command request
type CommandRequest struct {
	Command string `json:"command" binding:"required"`
}

// CommandResponse represents the response to a command
type CommandResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// ServerStartRequest represents runtime start options for a server.
type ServerStartRequest struct {
	InstallDir        *string `json:"install_dir"`
	ServiceUser       *string `json:"service_user"`
	UseSudo           *bool   `json:"use_sudo"`
	JavaXms           *string `json:"java_xms"`
	JavaXmx           *string `json:"java_xmx"`
	JavaMetaspace     *string `json:"java_metaspace"`
	EnableStringDedup *bool   `json:"enable_string_dedup"`
	EnableAot         *bool   `json:"enable_aot"`
	EnableBackup      *bool   `json:"enable_backup"`
	BackupDir         *string `json:"backup_dir"`
	BackupFrequency   *string `json:"backup_frequency"`
	AssetsPath        *string `json:"assets_path"`
	ExtraJavaArgs     *string `json:"extra_java_args"`
	ExtraServerArgs   *string `json:"extra_server_args"`
}

// ServerListItem represents a server in the list with its current status
type ServerListItem struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	ConnectionStatus ServerConnectionStatus `json:"connection_status"`
	Host             string                 `json:"host"`
	Port             int                    `json:"port"`
}
