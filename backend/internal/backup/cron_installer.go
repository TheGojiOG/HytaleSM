package backup

import (
	"fmt"
	"strings"

	"github.com/yourusername/hytale-server-manager/internal/config"
	"github.com/yourusername/hytale-server-manager/internal/ssh"
)

const cronMarkerPrefix = "# hsm-backup:"

// InstallCronJob ensures a cron entry exists for the schedule on the target server.
func InstallCronJob(cfg *config.Config, pool *ssh.ConnectionPool, serverDef *config.ServerDefinition, schedule *BackupSchedule) error {
	if schedule == nil || !schedule.Enabled || schedule.Schedule == "" {
		return nil
	}

	if serverDef == nil {
		return fmt.Errorf("server definition is required")
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		KnownHostsPath:  cfg.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: cfg.Security.SSH.TrustOnFirstUse,
	}

	switch serverDef.Connection.AuthMethod {
	case "key":
		sshConfig.KeyPath = serverDef.Connection.KeyPath
	case "password":
		sshConfig.Password = serverDef.Connection.Password
	default:
		return fmt.Errorf("invalid SSH auth method: %s", serverDef.Connection.AuthMethod)
	}

	conn, err := pool.GetConnection(serverDef.ID, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	runAsUser := strings.TrimSpace(schedule.RunAsUser)
	useSudo := schedule.UseSudo
	if runAsUser != "" {
		useSudo = true
	}

	cronLine, err := buildCronLine(serverDef, schedule)
	if err != nil {
		return err
	}

	marker := cronMarkerPrefix + serverDef.ID
	if schedule != nil && schedule.ID != "" {
		marker = cronMarkerPrefix + serverDef.ID + ":" + schedule.ID
	}
	current, _ := runCronCommand(conn, "crontab -l 2>/dev/null || true", runAsUser, useSudo)
	filtered := filterCronLines(current, marker)
	filtered = append(filtered, cronLine+" "+marker)
	installCmd := buildCrontabInstallCommand(filtered)

	if _, err := runCronCommand(conn, installCmd, runAsUser, useSudo); err != nil {
		return fmt.Errorf("failed to install cron job: %w", err)
	}

	return nil
}

// RemoveCronJob removes the backup cron entry for a server.
func RemoveCronJob(cfg *config.Config, pool *ssh.ConnectionPool, serverDef *config.ServerDefinition, schedule *BackupSchedule) error {
	if serverDef == nil {
		return fmt.Errorf("server definition is required")
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		KnownHostsPath:  cfg.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: cfg.Security.SSH.TrustOnFirstUse,
	}

	switch serverDef.Connection.AuthMethod {
	case "key":
		sshConfig.KeyPath = serverDef.Connection.KeyPath
	case "password":
		sshConfig.Password = serverDef.Connection.Password
	default:
		return fmt.Errorf("invalid SSH auth method: %s", serverDef.Connection.AuthMethod)
	}

	conn, err := pool.GetConnection(serverDef.ID, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	runAsUser := ""
	useSudo := true
	if schedule != nil {
		runAsUser = strings.TrimSpace(schedule.RunAsUser)
		useSudo = schedule.UseSudo || runAsUser != ""
	}

	marker := cronMarkerPrefix + serverDef.ID
	if schedule != nil && schedule.ID != "" {
		marker = cronMarkerPrefix + serverDef.ID + ":" + schedule.ID
	}
	current, _ := runCronCommand(conn, "crontab -l 2>/dev/null || true", runAsUser, useSudo)
	filtered := filterCronLines(current, marker)
	installCmd := buildCrontabInstallCommand(filtered)

	if _, err := runCronCommand(conn, installCmd, runAsUser, useSudo); err != nil {
		return fmt.Errorf("failed to remove cron job: %w", err)
	}

	return nil
}

// ReadCronTab returns the current crontab for the given user (via sudo when required).
func ReadCronTab(cfg *config.Config, pool *ssh.ConnectionPool, serverDef *config.ServerDefinition, runAsUser string, useSudo bool) (string, error) {
	if serverDef == nil {
		return "", fmt.Errorf("server definition is required")
	}

	sshConfig := &ssh.ClientConfig{
		Host:            serverDef.Connection.Host,
		Port:            serverDef.Connection.Port,
		Username:        serverDef.Connection.Username,
		AuthMethod:      serverDef.Connection.AuthMethod,
		KnownHostsPath:  cfg.Security.SSH.KnownHostsPath,
		TrustOnFirstUse: cfg.Security.SSH.TrustOnFirstUse,
	}

	switch serverDef.Connection.AuthMethod {
	case "key":
		sshConfig.KeyPath = serverDef.Connection.KeyPath
	case "password":
		sshConfig.Password = serverDef.Connection.Password
	default:
		return "", fmt.Errorf("invalid SSH auth method: %s", serverDef.Connection.AuthMethod)
	}

	conn, err := pool.GetConnection(serverDef.ID, sshConfig)
	if err != nil {
		return "", fmt.Errorf("failed to connect to server: %w", err)
	}

	command := "crontab -l 2>/dev/null || true"
	output, err := runCronCommand(conn, command, runAsUser, useSudo)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

func buildCronLine(serverDef *config.ServerDefinition, schedule *BackupSchedule) (string, error) {
	compression := normalizeCompression(schedule.Compression)
	archiveExt := compressionArchiveExtension(compression)
	archivePrefix := "backup_$(date +\\%F_\\%H-\\%M-\\%S)." + archiveExt

	destPath := schedule.Destination.Path
	if strings.TrimSpace(destPath) == "" {
		return "", fmt.Errorf("destination path is required")
	}

	installDir := strings.TrimSpace(serverDef.Dependencies.InstallDir)
	if installDir == "" {
		installDir = strings.TrimSpace(serverDef.Runtime.BackupDir)
	}

	paths := schedule.Directories
	if len(paths) == 0 {
		return "", fmt.Errorf("directories are required")
	}

	baseDir := ""
	var relPaths []string
	if installDir != "" {
		allUnder := true
		for _, entry := range paths {
			if !strings.HasPrefix(entry, installDir) {
				allUnder = false
				break
			}
		}
		if allUnder {
			baseDir = installDir
			for _, entry := range paths {
				rel := strings.TrimPrefix(entry, installDir)
				rel = strings.TrimPrefix(rel, "/")
				relPaths = append(relPaths, rel)
			}
		}
	}

	var targetList []string
	if baseDir != "" {
		targetList = relPaths
	} else {
		targetList = paths
	}

	tarFlag := tarCreateFlag(compression)
	excludeArgs := buildExcludeArgs(schedule.Exclude)

	commandParts := []string{
		"mkdir -p \"" + escapeDoubleQuotes(destPath) + "\"",
	}
	if baseDir != "" {
		commandParts = append(commandParts, "cd \""+escapeDoubleQuotes(baseDir)+"\"")
	}

	tarTargets := "\"" + strings.Join(mapEscapeDoubleQuotes(targetList), "\" \"") + "\""
	tarCmd := fmt.Sprintf("tar -%s \"%s/%s\" %s%s", tarFlag, escapeDoubleQuotes(destPath), archivePrefix, excludeArgs, tarTargets)
	commandParts = append(commandParts, tarCmd)

	command := strings.Join(commandParts, " && ")
	wrapped := fmt.Sprintf("/bin/bash -lc \"%s\"", escapeDoubleQuotes(command))

	return fmt.Sprintf("%s %s", schedule.Schedule, wrapped), nil
}

func filterCronLines(existing string, marker string) []string {
	var lines []string
	for _, line := range strings.Split(existing, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, marker) {
			continue
		}
		lines = append(lines, trimmed)
	}
	return lines
}

func buildCrontabInstallCommand(lines []string) string {
	if len(lines) == 0 {
		return "crontab -r || true"
	}

	var parts []string
	for _, line := range lines {
		parts = append(parts, "'"+escapeSingleQuotes(line)+"'")
	}

	return fmt.Sprintf("printf '%%s\\n' %s | crontab -", strings.Join(parts, " "))
}

func runCronCommand(conn *ssh.PooledConnection, command string, runAsUser string, useSudo bool) (string, error) {
	if !useSudo {
		return conn.Client.RunCommand(command)
	}

	escaped := escapeSingleQuotes(command)
	if strings.TrimSpace(runAsUser) != "" {
		return conn.Client.RunCommand(fmt.Sprintf("sudo -u %s -- sh -c '%s'", escapeSingleQuotes(runAsUser), escaped))
	}

	return conn.Client.RunCommand(fmt.Sprintf("sudo -- sh -c '%s'", escaped))
}

func mapEscapeDoubleQuotes(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, escapeDoubleQuotes(value))
	}
	return out
}

func escapeDoubleQuotes(value string) string {
	return strings.ReplaceAll(value, "\"", "\\\"")
}
