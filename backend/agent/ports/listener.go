package ports

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PortEvent struct {
	Port      int
	Open      bool
	Timestamp int64
}

type JavaProcess struct {
	PID         int    `json:"pid"`
	User        string `json:"user"`
	State       string `json:"state"`
	VSize       uint64 `json:"vsize"`
	RSS         int64  `json:"rss"`
	UTimeTicks  uint64 `json:"utime_ticks"`
	STimeTicks  uint64 `json:"stime_ticks"`
	StartTicks  uint64 `json:"start_ticks"`
	Cmdline     string `json:"cmdline"`
	ListenPorts []int  `json:"listen_ports"`
}

func Watch(ctx context.Context, ports []int, interval time.Duration, onPortEvent func(PortEvent), onJavaSnapshot func([]JavaProcess)) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	portSet := make(map[int]struct{}, len(ports))
	for _, p := range ports {
		portSet[p] = struct{}{}
	}

	lastPorts := make(map[int]bool)
	var lastJavaHash [32]byte

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			openPorts := readListeningPorts()
			for p := range portSet {
				open := openPorts[p]
				prev := lastPorts[p]
				if open != prev {
					lastPorts[p] = open
					onPortEvent(PortEvent{Port: p, Open: open, Timestamp: time.Now().Unix()})
				}
			}

			java := readJavaProcesses()
			if javaChanged(java, lastJavaHash) {
				lastJavaHash = hashJava(java)
				onJavaSnapshot(java)
			}
		}
	}
}

func Snapshot(ports []int) (map[int]bool, []JavaProcess) {
	openPorts := readListeningPorts()
	filtered := make(map[int]bool)
	for _, p := range ports {
		filtered[p] = openPorts[p]
	}
	java := readJavaProcesses()
	return filtered, java
}

func readListeningPorts() map[int]bool {
	ports := make(map[int]bool)
	parseProcNet("/proc/net/tcp", ports)
	parseProcNet("/proc/net/tcp6", ports)
	return ports
}

func parseProcNet(path string, out map[int]bool) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Scan() // header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		local := fields[1]
		state := fields[3]
		if state != "0A" {
			continue
		}
		parts := strings.Split(local, ":")
		if len(parts) != 2 {
			continue
		}
		portHex := parts[1]
		port64, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil {
			continue
		}
		out[int(port64)] = true
	}
}

func readJavaProcesses() []JavaProcess {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	java := make([]JavaProcess, 0)
	inodeMap := readListeningInodes()
	pidPorts := mapInodesToPids(inodeMap)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		cmdline := readCmdline(pid)
		if cmdline == "" || !strings.Contains(cmdline, "java") {
			continue
		}
		proc, ok := readProcStat(pid)
		if !ok {
			continue
		}
		proc.Cmdline = cmdline
		proc.ListenPorts = pidPorts[pid]
		sort.Ints(proc.ListenPorts)
		java = append(java, proc)
	}

	sort.Slice(java, func(i, j int) bool { return java[i].PID < java[j].PID })
	return java
}

func readListeningInodes() map[uint64]int {
	inodes := make(map[uint64]int)
	parseProcNetInodes("/proc/net/tcp", inodes)
	parseProcNetInodes("/proc/net/tcp6", inodes)
	return inodes
}

func parseProcNetInodes(path string, out map[uint64]int) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Scan() // header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		local := fields[1]
		state := fields[3]
		inodeField := fields[9]
		if state != "0A" {
			continue
		}
		parts := strings.Split(local, ":")
		if len(parts) != 2 {
			continue
		}
		portHex := parts[1]
		port64, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(inodeField, 10, 64)
		if err != nil {
			continue
		}
		out[inode] = int(port64)
	}
}

func mapInodesToPids(inodes map[uint64]int) map[int][]int {
	result := make(map[int][]int)
	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}
	for _, entry := range procEntries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inodeStr := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err != nil {
				continue
			}
			port, ok := inodes[inode]
			if !ok {
				continue
			}
			result[pid] = append(result[pid], port)
		}
	}
	return result
}

func readCmdline(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.TrimSpace(cmdline)
}

func readProcStat(pid int) (JavaProcess, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return JavaProcess{}, false
	}
	parts := splitProcStat(string(data))
	if len(parts) < 24 {
		return JavaProcess{}, false
	}
	state := parts[2]
	utime, _ := strconv.ParseUint(parts[13], 10, 64)
	stime, _ := strconv.ParseUint(parts[14], 10, 64)
	start, _ := strconv.ParseUint(parts[21], 10, 64)
	vsize, _ := strconv.ParseUint(parts[22], 10, 64)
	rss, _ := strconv.ParseInt(parts[23], 10, 64)

	user := lookupUser(pid)
	return JavaProcess{
		PID:        pid,
		User:       user,
		State:      state,
		VSize:      vsize,
		RSS:        rss,
		UTimeTicks: utime,
		STimeTicks: stime,
		StartTicks: start,
	}, true
}

func splitProcStat(line string) []string {
	start := strings.Index(line, "(")
	end := strings.LastIndex(line, ")")
	if start == -1 || end == -1 || end <= start {
		return strings.Fields(line)
	}
	before := strings.Fields(line[:start])
	name := line[start : end+1]
	after := strings.Fields(line[end+1:])
	fields := append(before, name)
	fields = append(fields, after...)
	return fields
}

func lookupUser(pid int) string {
	statusPath := filepath.Join("/proc", strconv.Itoa(pid), "status")
	file, err := os.Open(statusPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				uid := fields[1]
				return lookupUsername(uid)
			}
		}
	}
	return ""
}

func lookupUsername(uid string) string {
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return uid
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		if parts[2] == uid {
			return parts[0]
		}
	}
	return uid
}

func javaChanged(java []JavaProcess, lastHash [32]byte) bool {
	return hashJava(java) != lastHash
}

func hashJava(java []JavaProcess) [32]byte {
	var b strings.Builder
	for _, p := range java {
		fmt.Fprintf(&b, "%d|%s|%s|%d|%d|%d|%d|%d|%s|%v\n", p.PID, p.User, p.State, p.VSize, p.RSS, p.UTimeTicks, p.STimeTicks, p.StartTicks, p.Cmdline, p.ListenPorts)
	}
	return sha256Sum(b.String())
}

func sha256Sum(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}
