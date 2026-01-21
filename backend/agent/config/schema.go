package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

const (
	DefaultIntervalMs = 500
)

type BootstrapConfig struct {
	ServerAddr        string         `json:"server_addr"`
	HostUUID          string         `json:"host_uuid,omitempty"`
	CertFile          string         `json:"cert_file"`
	KeyFile           string         `json:"key_file"`
	CAFile            string         `json:"ca_file"`
	MonitorConfigPath string         `json:"monitor_config_path,omitempty"`
	MonitorConfig     *MonitorConfig `json:"monitor_config,omitempty"`
}

type MonitorConfig struct {
	Version    int      `json:"version"`
	Services   []string `json:"services"`
	Ports      []int    `json:"ports"`
	IntervalMs int      `json:"interval_ms"`
}

func LoadBootstrap(path string) (*BootstrapConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap config: %w", err)
	}
	var cfg BootstrapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse bootstrap config: %w", err)
	}
	if cfg.ServerAddr == "" {
		return nil, errors.New("bootstrap config missing server_addr")
	}
	if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "" {
		return nil, errors.New("bootstrap config missing cert_file/key_file/ca_file")
	}
	return &cfg, nil
}

func LoadMonitorConfig(path string) (*MonitorConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read monitor config: %w", err)
	}
	return ParseMonitorConfig(data)
}

func ParseMonitorConfig(data []byte) (*MonitorConfig, error) {
	var cfg MonitorConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse monitor config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.Normalize()
	return &cfg, nil
}

func (c *MonitorConfig) Validate() error {
	if c.Version < 1 {
		return errors.New("monitor config version must be >= 1")
	}
	if c.IntervalMs == 0 {
		c.IntervalMs = DefaultIntervalMs
	}
	if c.IntervalMs < 100 {
		return errors.New("interval_ms must be >= 100")
	}
	for _, p := range c.Ports {
		if p <= 0 || p > 65535 {
			return fmt.Errorf("invalid port: %d", p)
		}
	}
	return nil
}

func (c *MonitorConfig) Normalize() {
	c.Services = dedupStrings(c.Services)
	sort.Ints(c.Ports)
	c.Ports = dedupInts(c.Ports)
}

func dedupStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, v := range items {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func dedupInts(items []int) []int {
	if len(items) == 0 {
		return items
	}
	out := make([]int, 0, len(items))
	prev := items[0] - 1
	for _, v := range items {
		if v == prev {
			continue
		}
		out = append(out, v)
		prev = v
	}
	return out
}
