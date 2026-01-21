package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Server   ServerConfig   `yaml:"server" json:"server"`
	Database DatabaseConfig `yaml:"database" json:"database"`
	Auth     AuthConfig     `yaml:"auth" json:"auth"`
	Security SecurityConfig `yaml:"security" json:"security"`
	Storage  StorageConfig  `yaml:"storage" json:"storage"`
	Logging  LoggingConfig  `yaml:"logging" json:"logging"`
	Metrics  MetricsConfig  `yaml:"metrics" json:"metrics"`
}

// ServerConfig contains HTTP server settings
type ServerConfig struct {
	Host string    `yaml:"host" json:"host"`
	Port int       `yaml:"port" json:"port"`
	TLS  TLSConfig `yaml:"tls" json:"tls"`
}

// TLSConfig contains TLS/HTTPS settings
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

// DatabaseConfig contains database settings
type DatabaseConfig struct {
	Path           string `yaml:"path" json:"path"`
	MaxConnections int    `yaml:"max_connections" json:"max_connections"`
}

// AuthConfig contains authentication settings
type AuthConfig struct {
	JWTSecret            string `yaml:"jwt_secret" json:"jwt_secret"`
	AccessTokenDuration  string `yaml:"access_token_duration" json:"access_token_duration"`
	RefreshTokenDuration string `yaml:"refresh_token_duration" json:"refresh_token_duration"`
	BcryptCost           int    `yaml:"bcrypt_cost" json:"bcrypt_cost"`
}

// SecurityConfig contains security settings
type SecurityConfig struct {
	RateLimit RateLimitConfig `yaml:"rate_limit" json:"rate_limit"`
	CORS      CORSConfig      `yaml:"cors" json:"cors"`
	SSH       SSHConfig       `yaml:"ssh" json:"ssh"`
}

// RateLimitConfig contains rate limiting settings
type RateLimitConfig struct {
	Enabled           bool `yaml:"enabled" json:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute" json:"requests_per_minute"`
}

// CORSConfig contains CORS settings
type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins" json:"allowed_origins"`
	AllowedMethods []string `yaml:"allowed_methods" json:"allowed_methods"`
}

// SSHConfig contains SSH security settings
type SSHConfig struct {
	KnownHostsPath  string `yaml:"known_hosts_path" json:"known_hosts_path"`
	TrustOnFirstUse bool   `yaml:"trust_on_first_use" json:"trust_on_first_use"`
}

// StorageConfig contains storage paths
type StorageConfig struct {
	ConfigDir string `yaml:"config_dir" json:"config_dir"`
	BackupDir string `yaml:"backup_dir" json:"backup_dir"`
	DataDir   string `yaml:"data_dir" json:"data_dir"`
	ReleasesDir  string `yaml:"releases_dir" json:"releases_dir"`
	DownloaderDir string `yaml:"downloader_dir" json:"downloader_dir"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level      string `yaml:"level" json:"level"`
	Format     string `yaml:"format" json:"format"`
	File       string `yaml:"file" json:"file"`
	MaxSize    int    `yaml:"max_size" json:"max_size"`
	MaxBackups int    `yaml:"max_backups" json:"max_backups"`
	MaxAge     int    `yaml:"max_age" json:"max_age"`
}

// MetricsConfig contains metrics collection settings
type MetricsConfig struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`
	DefaultInterval int  `yaml:"default_interval" json:"default_interval"` // seconds
	RetentionDays   int  `yaml:"retention_days" json:"retention_days"`
}

// Load loads configuration from file and environment variables
func Load() (*Config, error) {
	// Default configuration
	cfg := &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
			TLS: TLSConfig{
				Enabled: false,
			},
		},
		Database: DatabaseConfig{
			Path:           "./data/hytale-manager.db",
			MaxConnections: 25,
		},
		Auth: AuthConfig{
			JWTSecret:            getEnv("JWT_SECRET", "change-me-in-production"),
			AccessTokenDuration:  "15m",
			RefreshTokenDuration: "168h", // 7 days
			BcryptCost:           12,
		},
		Security: SecurityConfig{
			RateLimit: RateLimitConfig{
				Enabled:           true,
				RequestsPerMinute: 60,
			},
			CORS: CORSConfig{
				AllowedOrigins: []string{"http://localhost:5173"},
				AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			},
			SSH: SSHConfig{
				KnownHostsPath:  "./data/known_hosts",
				TrustOnFirstUse: true,
			},
		},
		Storage: StorageConfig{
			ConfigDir: "./configs",
			BackupDir: "./data/backups",
			DataDir:   "./data",
			ReleasesDir:  "./hytale_repo",
			DownloaderDir: "./hytale_repo/hytale-downloader",
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "json",
			File:       "",
			MaxSize:    100,
			MaxBackups: 5,
			MaxAge:     30,
		},
		Metrics: MetricsConfig{
			Enabled:         true,
			DefaultInterval: 60,
			RetentionDays:   2,
		},
	}

	// Load from config file if it exists
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = resolveConfigPath()
	}

	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with environment variables
	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		cfg.Auth.JWTSecret = jwtSecret
	}

	if dbPath := os.Getenv("DATABASE_PATH"); dbPath != "" {
		cfg.Database.Path = dbPath
	}

	if configDir := os.Getenv("CONFIG_DIR"); configDir != "" {
		cfg.Storage.ConfigDir = configDir
	}

	if dataDir := os.Getenv("DATA_DIR"); dataDir != "" {
		cfg.Storage.DataDir = dataDir
	}

	if backupDir := os.Getenv("BACKUP_DIR"); backupDir != "" {
		cfg.Storage.BackupDir = backupDir
	}

	if knownHostsPath := os.Getenv("KNOWN_HOSTS_PATH"); knownHostsPath != "" {
		cfg.Security.SSH.KnownHostsPath = knownHostsPath
	}

	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.Logging.Level = logLevel
	}

	// Normalize storage paths based on config location
	cfg.normalizeStoragePaths(configPath)

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Auth.JWTSecret == "" || c.Auth.JWTSecret == "change-me-in-production" {
		return fmt.Errorf("JWT_SECRET must be set to a secure value")
	}

	// Check for unexpanded environment variables
	if len(c.Auth.JWTSecret) > 1 && c.Auth.JWTSecret[0] == '$' && c.Auth.JWTSecret[1] == '{' {
		return fmt.Errorf("JWT_SECRET contains unexpanded environment variable")
	}

	if c.Server.TLS.Enabled {
		if c.Server.TLS.CertFile == "" || c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("TLS is enabled but cert_file or key_file is missing")
		}
	}

	if c.Auth.BcryptCost < 10 || c.Auth.BcryptCost > 14 {
		return fmt.Errorf("bcrypt_cost must be between 10 and 14")
	}

	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func resolveConfigPath() string {
	candidates := []string{"../configs/config.yaml", "./configs/config.yaml"}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return "./configs/config.yaml"
}

// GetConfigPath returns the resolved config path
func GetConfigPath() string {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = resolveConfigPath()
	}
	return configPath
}

// Save writes the configuration back to disk
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (c *Config) normalizeStoragePaths(configPath string) {
	baseDir := filepath.Dir(configPath)
	if !filepath.IsAbs(baseDir) {
		if absBase, err := filepath.Abs(baseDir); err == nil {
			baseDir = absBase
		}
	}

	rootDir := baseDir
	if filepath.Base(baseDir) == "configs" {
		rootDir = filepath.Dir(baseDir)
	}

	resolvePath := func(value string) string {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return ""
		}
		if filepath.IsAbs(trimmed) {
			return filepath.Clean(trimmed)
		}
		return filepath.Clean(filepath.Join(rootDir, trimmed))
	}

	configDir := c.Storage.ConfigDir
	if strings.TrimSpace(configDir) == "" {
		configDir = baseDir
	}
	configDir = resolvePath(configDir)
	c.Storage.ConfigDir = configDir

	if strings.TrimSpace(c.Storage.DataDir) == "" {
		c.Storage.DataDir = filepath.Join(rootDir, "data")
	}
	c.Storage.DataDir = resolvePath(c.Storage.DataDir)

	if strings.TrimSpace(c.Storage.BackupDir) == "" {
		c.Storage.BackupDir = filepath.Join(c.Storage.DataDir, "backups")
	}
	c.Storage.BackupDir = resolvePath(c.Storage.BackupDir)

	if strings.TrimSpace(c.Storage.ReleasesDir) == "" {
		c.Storage.ReleasesDir = filepath.Join(rootDir, "hytale_repo")
	}
	c.Storage.ReleasesDir = resolvePath(c.Storage.ReleasesDir)

	if strings.TrimSpace(c.Storage.DownloaderDir) == "" {
		c.Storage.DownloaderDir = filepath.Join(c.Storage.ReleasesDir, "hytale-downloader")
	}
	c.Storage.DownloaderDir = resolvePath(c.Storage.DownloaderDir)

	if strings.TrimSpace(c.Security.SSH.KnownHostsPath) == "" {
		c.Security.SSH.KnownHostsPath = filepath.Join(c.Storage.DataDir, "known_hosts")
	}
	c.Security.SSH.KnownHostsPath = resolvePath(c.Security.SSH.KnownHostsPath)
}
