package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServerDefinition represents a game server configuration
type ServerDefinition struct {
	ID          string           `json:"id" yaml:"id"`
	Name        string           `json:"name" yaml:"name"`
	Description string           `json:"description" yaml:"description"`
	Connection  ConnectionConfig `json:"connection" yaml:"connection"`
	Server      GameServerConfig `json:"server" yaml:"server"`
	Runtime     RuntimeConfig    `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Backups     BackupConfig     `json:"backups" yaml:"backups"`
	Monitoring  MonitoringConfig `json:"monitoring" yaml:"monitoring"`
	Dependencies DependenciesConfig `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
}

// ConnectionConfig contains SSH connection details
type ConnectionConfig struct {
	Host       string `json:"host" yaml:"host"`
	Port       int    `json:"port" yaml:"port"`
	Username   string `json:"username" yaml:"username"`
	AuthMethod string `json:"auth_method" yaml:"auth_method"` // "key" or "password"
	KeyPath    string `json:"key_path" yaml:"key_path"`
	KeyContent string `json:"key_content,omitempty" yaml:"-"`
	Password   string `json:"password,omitempty" yaml:"password,omitempty"`
}

// GameServerConfig contains game server process settings
type GameServerConfig struct {
	WorkingDirectory  string `json:"working_directory" yaml:"working_directory"`
	Executable        string `json:"executable" yaml:"executable"`
	JavaArgs          string `json:"java_args" yaml:"java_args"`
	ProcessManager    string `json:"process_manager" yaml:"process_manager"` // "screen" or "systemd"
	ScreenSessionName string `json:"screen_session_name,omitempty" yaml:"screen_session_name,omitempty"`
	SystemdService    string `json:"systemd_service_name,omitempty" yaml:"systemd_service_name,omitempty"`
}

// BackupConfig contains backup settings for a server
type BackupConfig struct {
	Enabled      bool                `json:"enabled" yaml:"enabled"`
	Schedule     string              `json:"schedule" yaml:"schedule"`
	Directories  []string            `json:"directories" yaml:"directories"`
	Retention    RetentionConfig     `json:"retention" yaml:"retention"`
	Destinations []BackupDestination `json:"destinations" yaml:"destinations"`
}

// RetentionConfig specifies backup retention policy
type RetentionConfig struct {
	Count int `json:"count" yaml:"count"` // Keep last N backups
}

// BackupDestination represents a backup storage destination
type BackupDestination struct {
	Type          string `json:"type" yaml:"type"` // "local", "sftp", "s3"
	Path          string `json:"path,omitempty" yaml:"path,omitempty"`
	Endpoint      string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Bucket        string `json:"bucket,omitempty" yaml:"bucket,omitempty"`
	Region        string `json:"region,omitempty" yaml:"region,omitempty"`
	PathPrefix    string `json:"path_prefix,omitempty" yaml:"path_prefix,omitempty"`
	CredentialsID string `json:"credentials_id,omitempty" yaml:"credentials_id,omitempty"`
}

// MonitoringConfig contains monitoring settings
type MonitoringConfig struct {
	Enabled          bool     `json:"enabled" yaml:"enabled"`
	Interval         int      `json:"interval" yaml:"interval"` // seconds
	Metrics          []string `json:"metrics" yaml:"metrics"`
	NodeExporterURL  string   `json:"node_exporter_url,omitempty" yaml:"node_exporter_url,omitempty"`
	NodeExporterPort int      `json:"node_exporter_port,omitempty" yaml:"node_exporter_port,omitempty"`
}

// RuntimeConfig contains runtime startup options for the server
type RuntimeConfig struct {
	JavaXms           string `json:"java_xms,omitempty" yaml:"java_xms,omitempty"`
	JavaXmx           string `json:"java_xmx,omitempty" yaml:"java_xmx,omitempty"`
	JavaMetaspace     string `json:"java_metaspace,omitempty" yaml:"java_metaspace,omitempty"`
	EnableStringDedup bool   `json:"enable_string_dedup,omitempty" yaml:"enable_string_dedup,omitempty"`
	EnableAOT         bool   `json:"enable_aot,omitempty" yaml:"enable_aot,omitempty"`
	EnableBackup      bool   `json:"enable_backup,omitempty" yaml:"enable_backup,omitempty"`
	BackupDir         string `json:"backup_dir,omitempty" yaml:"backup_dir,omitempty"`
	BackupFrequency   string `json:"backup_frequency,omitempty" yaml:"backup_frequency,omitempty"`
	AssetsPath        string `json:"assets_path,omitempty" yaml:"assets_path,omitempty"`
	ExtraJavaArgs     string `json:"extra_java_args,omitempty" yaml:"extra_java_args,omitempty"`
	ExtraServerArgs   string `json:"extra_server_args,omitempty" yaml:"extra_server_args,omitempty"`
}

type DependenciesConfig struct {
	Configured     bool     `json:"configured" yaml:"configured"`
	SkipUpdate      bool     `json:"skip_update" yaml:"skip_update"`
	UseSudo         bool     `json:"use_sudo" yaml:"use_sudo"`
	CreateUser      bool     `json:"create_user" yaml:"create_user"`
	ServiceUser     string   `json:"service_user" yaml:"service_user"`
	ServiceGroups   []string `json:"service_groups" yaml:"service_groups"`
	InstallDir      string   `json:"install_dir" yaml:"install_dir"`
}

// LoadServers loads server definitions from YAML file
func LoadServers(configDir string) ([]ServerDefinition, error) {
	serversPath := fmt.Sprintf("%s/servers.yaml", configDir)

	data, err := os.ReadFile(serversPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty list if file doesn't exist
			return []ServerDefinition{}, nil
		}
		return nil, fmt.Errorf("failed to read servers file: %w", err)
	}

	var serversFile struct {
		Servers []ServerDefinition `yaml:"servers"`
	}

	if err := yaml.Unmarshal(data, &serversFile); err != nil {
		return nil, fmt.Errorf("failed to parse servers file: %w", err)
	}

	// Validate server definitions
	for i, server := range serversFile.Servers {
		if err := ValidateServerDefinition(&server); err != nil {
			return nil, fmt.Errorf("invalid server definition at index %d: %w", i, err)
		}
	}

	return serversFile.Servers, nil
}

// SaveServers saves server definitions to YAML file
func SaveServers(configDir string, servers []ServerDefinition) error {
	serversFile := struct {
		Servers []ServerDefinition `yaml:"servers"`
	}{
		Servers: servers,
	}

	data, err := yaml.Marshal(serversFile)
	if err != nil {
		return fmt.Errorf("failed to marshal servers: %w", err)
	}

	serversPath := fmt.Sprintf("%s/servers.yaml", configDir)
	if err := os.WriteFile(serversPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write servers file: %w", err)
	}

	return nil
}

func ValidateServerDefinition(server *ServerDefinition) error {
	if server.ID == "" {
		return fmt.Errorf("server ID is required")
	}
	if server.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if server.Connection.Host == "" {
		return fmt.Errorf("connection host is required")
	}
	if server.Connection.Port == 0 {
		server.Connection.Port = 22 // Default SSH port
	}
	if server.Connection.Username == "" {
		return fmt.Errorf("connection username is required")
	}
	if server.Connection.AuthMethod != "key" && server.Connection.AuthMethod != "password" {
		return fmt.Errorf("auth_method must be 'key' or 'password'")
	}
	if server.Connection.AuthMethod == "key" && server.Connection.KeyPath == "" && server.Connection.KeyContent == "" {
		return fmt.Errorf("key_path is required when auth_method is 'key'")
	}
	if server.Server.WorkingDirectory == "" {
		return fmt.Errorf("server working_directory is required")
	}
	if !isValidPath(server.Server.WorkingDirectory) {
		return fmt.Errorf("server working_directory contains invalid characters")
	}
	if server.Server.Executable == "" {
		return fmt.Errorf("server executable is required")
	}
	if !isValidPath(server.Server.Executable) {
		return fmt.Errorf("server executable contains invalid characters")
	}
	if server.Server.JavaArgs != "" && !isValidArgs(server.Server.JavaArgs) {
		return fmt.Errorf("server java_args contains invalid characters")
	}
	if server.Server.ProcessManager != "screen" && server.Server.ProcessManager != "systemd" {
		return fmt.Errorf("process_manager must be 'screen' or 'systemd'")
	}

	return nil
}

func isValidPath(s string) bool {
	// Block shell metacharacters that could allow command injection
	// The list includes: ; | & $ ` ( ) < > " '
	// Also block newlines
	// Note: Backslash is allowed for Windows paths, but could be risky if not handled carefully
	dangerous := ";|&$`()<>\"'\n"
	return !strings.ContainsAny(s, dangerous)
}

func isValidArgs(s string) bool {
	// Arguments might contain some punctuation but definitely not command separators
	dangerous := ";|&`$()<>\\\n"
	return !strings.ContainsAny(s, dangerous)
}
