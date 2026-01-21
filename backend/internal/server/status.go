package server

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// StatusDetector determines the actual state of a server
type StatusDetector struct {
	executor       CommandExecutor
	processManager ProcessManager
	db             *sql.DB
}

// ServerStatusInfo represents the detected status of a server
type ServerStatusInfo struct {
	ServerID      string
	Status        string // "unknown", "offline", "starting", "online", "stopping", "error"
	PID           int
	UptimeSeconds int64
	ErrorMessage  string
	LastChecked   time.Time
}

const (
	StatusUnknown  = "unknown"
	StatusOffline  = "offline"
	StatusStarting = "starting"
	StatusOnline   = "online"
	StatusStopping = "stopping"
	StatusError    = "error"
)

// NewStatusDetector creates a new status detector
func NewStatusDetector(executor CommandExecutor, process ProcessManager, db *sql.DB) *StatusDetector {
	return &StatusDetector{
		executor:       executor,
		processManager: process,
		db:             db,
	}
}

// DetectStatus detects the actual status of a server using multiple methods
func (sd *StatusDetector) DetectStatus(serverID, sessionName string) (*ServerStatusInfo, error) {
	info := &ServerStatusInfo{
		ServerID:    serverID,
		Status:      StatusUnknown,
		LastChecked: time.Now(),
	}

	// Step 1: Check if screen session exists
	sessionExists, err := sd.processManager.IsRunning(serverID, sessionName)
	if err != nil {
		log.Printf("[Status] Error checking screen session: %v", err)
		info.Status = StatusError
		info.ErrorMessage = fmt.Sprintf("Failed to check screen session: %v", err)
		return info, nil
	}

	if !sessionExists {
		dbStatus, dbError := sd.getLastKnownStatusInfo(serverID)
		if dbStatus == StatusError && dbError != "" {
			info.Status = StatusError
			info.ErrorMessage = dbError
			log.Printf("[Status] Server %s: Screen session not found - ERROR", serverID)
			return info, nil
		}
		if dbStatus == StatusStarting || dbStatus == StatusStopping {
			info.Status = dbStatus
			log.Printf("[Status] Server %s: Screen session not found - %s", serverID, info.Status)
			return info, nil
		}
		log.Printf("[Status] Server %s: Screen session not found - OFFLINE", serverID)
		info.Status = StatusOffline
		return info, nil
	}

	// Step 3: Check if server process is running (Java or any process in screen)
	processPID, processRunning, err := sd.checkServerProcess(serverID, sessionName)
	if err != nil {
		log.Printf("[Status] Error checking server process: %v", err)
		// Continue with detection, this is not fatal
	}

	// Determine status based on findings
	if !processRunning {
		// Screen exists but no process - likely crashed or starting
		// Check database for last known status
		dbStatus := sd.getLastKnownStatus(serverID)
		if dbStatus == StatusStarting || dbStatus == StatusStopping {
			info.Status = dbStatus
		} else {
			info.Status = StatusError
			info.ErrorMessage = "Screen session exists but server process not found (crashed?)"
		}
		log.Printf("[Status] Server %s: Screen exists but no server process - %s", serverID, info.Status)
	} else {
		// Server process is running
		info.PID = processPID

		// Check if process recently started (< 30 seconds)
		uptime, err := sd.getProcessUptime(serverID, processPID)
		if err == nil {
			info.UptimeSeconds = int64(uptime.Seconds())

			if uptime < 30*time.Second {
				info.Status = StatusStarting
				log.Printf("[Status] Server %s: Server process recently started (uptime: %v) - STARTING", serverID, uptime)
			} else {
				info.Status = StatusOnline
				log.Printf("[Status] Server %s: Server process running (PID: %d, uptime: %v) - ONLINE", serverID, processPID, uptime)
			}
		} else {
			// Couldn't get uptime, assume online if process exists
			info.Status = StatusOnline
			log.Printf("[Status] Server %s: Server process running (PID: %d) - ONLINE", serverID, processPID)
		}
	}

	// Update database with detected status
	if sd.db != nil {
		sd.updateStatusInDB(info)
	}

	return info, nil
}

// checkServerProcess checks if a server process is running in the screen session
// This works for both Java servers and bash script mock servers
func (sd *StatusDetector) checkServerProcess(serverID, sessionName string) (int, bool, error) {
	// Get screen session PID first
	screenPID, err := sd.processManager.GetPID(serverID, sessionName)
	if err != nil {
		return 0, false, fmt.Errorf("failed to get screen PID: %w", err)
	}

	// Find all processes to build process tree
	psCmd := "ps -o pid,ppid,comm --no-headers -A"

	output, err := sd.executor.Execute(serverID, psCmd)
	if err != nil {
		return 0, false, fmt.Errorf("failed to list processes: %w", err)
	}

	// Build a map of PID -> PPID and PID -> Command
	type procInfo struct {
		ppid int
		comm string
	}
	processes := make(map[int]procInfo)
	
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		comm := parts[2]
		
		processes[pid] = procInfo{ppid: ppid, comm: comm}
	}
	
	// Find all descendants of the screen session
	var findDescendants func(parentPID int) []int
	findDescendants = func(parentPID int) []int {
		var descendants []int
		for pid, info := range processes {
			if info.ppid == parentPID {
				descendants = append(descendants, pid)
				descendants = append(descendants, findDescendants(pid)...)
			}
		}
		return descendants
	}
	
	descendants := findDescendants(screenPID)
	if len(descendants) == 0 {
		log.Printf("[Status] No processes found for screen session %d", screenPID)
		return 0, false, nil
	}
	
	// Find the best candidate process (prefer java, then bash/sh, then any)
	var candidatePID int
	var candidateComm string
	
	for _, pid := range descendants {
		info := processes[pid]
		comm := strings.ToLower(info.comm)
		
		// Prefer Java processes
		if strings.Contains(comm, "java") {
			log.Printf("[Status] Found Java process: PID=%d", pid)
			return pid, true, nil
		}
		
		// Next prefer bash/sh processes (but not just plain bash shells)
		if (strings.Contains(comm, "bash") || strings.Contains(comm, "sh")) && 
		   !strings.Contains(comm, "screen") {
			candidatePID = pid
			candidateComm = comm
		}
	}
	
	// If we found a bash/sh candidate, use it
	if candidatePID != 0 {
		log.Printf("[Status] Found server process: PID=%d, Command=%s", candidatePID, candidateComm)
		return candidatePID, true, nil
	}
	
	// Otherwise use the first descendant
	if len(descendants) > 0 {
		candidatePID = descendants[0]
		log.Printf("[Status] Using first descendant process: PID=%d", candidatePID)
		return candidatePID, true, nil
	}

	return 0, false, nil
}

// isProcessDescendant checks if a process is a descendant of another
func (sd *StatusDetector) isProcessDescendant(serverID string, childPID, ancestorPID int) bool {
	if childPID == ancestorPID {
		return true
	}

	// Get parent PID of the child
	psCmd := fmt.Sprintf("ps -o ppid= -p %d 2>/dev/null", childPID)
	output, err := sd.executor.Execute(serverID, psCmd)
	if err != nil {
		return false
	}

	ppid, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return false
	}

	if ppid == 0 || ppid == 1 {
		return false // Reached init or no parent
	}

	if ppid == ancestorPID {
		return true
	}

	// Recursive check (limit depth to prevent infinite loop)
	return sd.isProcessDescendant(serverID, ppid, ancestorPID)
}

// getProcessUptime calculates how long a process has been running
func (sd *StatusDetector) getProcessUptime(serverID string, pid int) (time.Duration, error) {
	// Get process start time using ps
	// Format: elapsed time in seconds
	psCmd := fmt.Sprintf("ps -o etimes= -p %d 2>/dev/null", pid)

	output, err := sd.executor.Execute(serverID, psCmd)
	if err != nil {
		return 0, fmt.Errorf("failed to get process uptime: %w", err)
	}

	seconds, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse uptime: %w", err)
	}

	return time.Duration(seconds) * time.Second, nil
}

// CheckPort checks if a specific port is listening
func (sd *StatusDetector) CheckPort(serverID string, port int) (bool, error) {
	// Check if port is listening using netstat or ss
	// Try netstat first (more commonly available)
	netstatCmd := fmt.Sprintf("netstat -tuln 2>/dev/null | grep -q ':%d ' || ss -tuln 2>/dev/null | grep -q ':%d '", port, port)

	_, err := sd.executor.Execute(serverID, netstatCmd)
	if err != nil {
		// grep returns non-zero if no match
		if strings.Contains(err.Error(), "exit status") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check port: %w", err)
	}

	return true, nil
}

// SendHealthPing sends a test command to verify the server is responsive
func (sd *StatusDetector) SendHealthPing(serverID, sessionName string) (bool, error) {
	// Send a harmless command and check if we get a response
	// This depends on the game server type
	// For now, we'll just verify we can send a command without error

	if err := sd.processManager.SendCommand(serverID, sessionName, "help"); err != nil {
		return false, err
	}

	// If we could send the command, consider it healthy
	// A more sophisticated check would parse console output
	return true, nil
}

// getLastKnownStatus retrieves the last known status from the database
func (sd *StatusDetector) getLastKnownStatus(serverID string) string {
	if sd.db == nil {
		return StatusUnknown
	}

	var status string
	err := sd.db.QueryRow(`
		SELECT status FROM server_status WHERE server_id = ?
	`, serverID).Scan(&status)

	if err != nil {
		if err == sql.ErrNoRows {
			return StatusUnknown
		}
		log.Printf("[Status] Error querying last known status: %v", err)
		return StatusUnknown
	}

	return status
}

// getLastKnownStatusInfo retrieves the last known status and error message from the database
func (sd *StatusDetector) getLastKnownStatusInfo(serverID string) (string, string) {
	if sd.db == nil {
		return "", ""
	}

	query := `SELECT status, error_message FROM server_status WHERE server_id = ?`
	var status string
	var errorMessage sql.NullString
	if err := sd.db.QueryRow(query, serverID).Scan(&status, &errorMessage); err != nil {
		return "", ""
	}

	if errorMessage.Valid {
		return status, errorMessage.String
	}
	return status, ""
}

// updateStatusInDB updates the server_status table with detected status
func (sd *StatusDetector) updateStatusInDB(info *ServerStatusInfo) {
	if sd.db == nil {
		return
	}

	query := `
		INSERT INTO server_status (
			server_id, status, pid, uptime_seconds, error_message, last_checked, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET
			status = excluded.status,
			pid = excluded.pid,
			uptime_seconds = excluded.uptime_seconds,
			error_message = excluded.error_message,
			last_checked = excluded.last_checked,
			updated_at = excluded.updated_at
	`

	_, err := sd.db.Exec(
		query,
		info.ServerID,
		info.Status,
		info.PID,
		info.UptimeSeconds,
		info.ErrorMessage,
		info.LastChecked,
		time.Now(),
	)

	if err != nil {
		log.Printf("[Status] Error updating status in database: %v", err)
	}
}

// GetServerStatus retrieves the current status from the database
func (sd *StatusDetector) GetServerStatus(serverID string) (*ServerStatusInfo, error) {
	if sd.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	info := &ServerStatusInfo{
		ServerID: serverID,
	}

	err := sd.db.QueryRow(`
		SELECT status, pid, uptime_seconds, error_message, last_checked
		FROM server_status
		WHERE server_id = ?
	`, serverID).Scan(
		&info.Status,
		&info.PID,
		&info.UptimeSeconds,
		&info.ErrorMessage,
		&info.LastChecked,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no status found for server %s", serverID)
		}
		return nil, fmt.Errorf("failed to query status: %w", err)
	}

	return info, nil
}

// MonitorServerStatus continuously monitors a server's status
func (sd *StatusDetector) MonitorServerStatus(serverID, sessionName string, interval time.Duration, stopChan <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[Status] Started monitoring server %s (interval: %v)", serverID, interval)

	for {
		select {
		case <-stopChan:
			log.Printf("[Status] Stopped monitoring server %s", serverID)
			return
		case <-ticker.C:
			_, err := sd.DetectStatus(serverID, sessionName)
			if err != nil {
				log.Printf("[Status] Error detecting status for %s: %v", serverID, err)
			}
		}
	}
}

// GetAllServerStatuses retrieves status for all servers in the database
func (sd *StatusDetector) GetAllServerStatuses() ([]*ServerStatusInfo, error) {
	if sd.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := sd.db.Query(`
		SELECT server_id, status, pid, uptime_seconds, error_message, last_checked
		FROM server_status
		ORDER BY server_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query statuses: %w", err)
	}
	defer rows.Close()

	statuses := make([]*ServerStatusInfo, 0)

	for rows.Next() {
		info := &ServerStatusInfo{}
		err := rows.Scan(
			&info.ServerID,
			&info.Status,
			&info.PID,
			&info.UptimeSeconds,
			&info.ErrorMessage,
			&info.LastChecked,
		)
		if err != nil {
			log.Printf("[Status] Error scanning row: %v", err)
			continue
		}

		statuses = append(statuses, info)
	}

	return statuses, nil
}

// parseServerLog parses server logs for specific patterns (e.g., startup complete, errors)
func (sd *StatusDetector) parseServerLog(serverID, logPath string, pattern string) (bool, error) {
	// Tail the log file and grep for pattern
	// This can be used to detect when server has fully started
	grepCmd := fmt.Sprintf("tail -n 100 %s | grep -q '%s'", logPath, pattern)

	_, err := sd.executor.Execute(serverID, grepCmd)
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return false, nil // Pattern not found
		}
		return false, fmt.Errorf("failed to parse log: %w", err)
	}

	return true, nil
}

// GetPlayerCount attempts to extract player count from server console or logs
func (sd *StatusDetector) GetPlayerCount(serverID, sessionName string) (int, error) {
	// This is game-specific and would need to be customized
	// For now, return 0 as a placeholder
	// A real implementation would parse "list" command output or logs

	// Example for Minecraft-like servers:
	// 1. Send "list" command
	// 2. Parse output for "There are X/Y players online"
	// 3. Extract X

	return 0, fmt.Errorf("player count detection not implemented")
}

// extractPlayerCount parses player count from command output
func (sd *StatusDetector) extractPlayerCount(output string) (int, error) {
	// Example patterns:
	// "There are 5/20 players online"
	// "Players online: 5"

	// Pattern: "There are X/Y" or "There are X of Y"
	re := regexp.MustCompile(`There are (\d+)[/\s]`)
	matches := re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		count, err := strconv.Atoi(matches[1])
		if err == nil {
			return count, nil
		}
	}

	// Pattern: "Players online: X"
	re = regexp.MustCompile(`Players online:\s*(\d+)`)
	matches = re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		count, err := strconv.Atoi(matches[1])
		if err == nil {
			return count, nil
		}
	}

	return 0, fmt.Errorf("could not parse player count from output")
}
