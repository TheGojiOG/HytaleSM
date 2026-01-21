package metrics

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/database"
)

type Collector struct {
	cfg           *config.Config
	serverManager *config.ServerManager
	db            *database.DB
	client        *http.Client
	stopCh        chan struct{}
	wg            sync.WaitGroup
	mu            sync.Mutex
	lastCollected map[string]time.Time
	cpuSamples    map[string]cpuSample
	lastCleanup   time.Time
}

type cpuSample struct {
	timestamp time.Time
	idle      float64
	total     float64
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

func NewCollector(cfg *config.Config, serverManager *config.ServerManager, db *database.DB) *Collector {
	return &Collector{
		cfg:           cfg,
		serverManager: serverManager,
		db:            db,
		client:        &http.Client{Timeout: 5 * time.Second},
		stopCh:        make(chan struct{}),
		lastCollected: make(map[string]time.Time),
		cpuSamples:    make(map[string]cpuSample),
	}
}

func (c *Collector) Start() {
	if !c.cfg.Metrics.Enabled {
		return
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.collectAll()
			case <-c.stopCh:
				return
			}
		}
	}()
}

func (c *Collector) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

func (c *Collector) collectAll() {
	if !c.cfg.Metrics.Enabled {
		return
	}

	servers := c.serverManager.GetAll()
	now := time.Now()
	for _, serverDef := range servers {
		serverID := serverDef.ID
		if serverID == "" {
			continue
		}

		interval := serverDef.Monitoring.Interval
		if interval <= 0 {
			interval = c.cfg.Metrics.DefaultInterval
		}
		if interval <= 0 {
			interval = 60
		}

		if !c.shouldCollect(serverID, now, time.Duration(interval)*time.Second) {
			continue
		}

		metrics, err := c.collectNodeExporterMetrics(serverID, serverDef)
		if err != nil || len(metrics) == 0 {
			continue
		}

		_ = c.recordMetrics(serverID, metrics, "online")
		c.setCollected(serverID, now)
	}

	c.cleanupOldMetrics(now)
}

func (c *Collector) shouldCollect(serverID string, now time.Time, interval time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.lastCollected[serverID]
	if !ok {
		return true
	}
	return now.Sub(last) >= interval
}

func (c *Collector) setCollected(serverID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastCollected[serverID] = now
}

func (c *Collector) cleanupOldMetrics(now time.Time) {
	if c.db == nil || c.cfg.Metrics.RetentionDays <= 0 {
		return
	}

	if !c.lastCleanup.IsZero() && now.Sub(c.lastCleanup) < 6*time.Hour {
		return
	}

	cutoff := now.Add(-time.Duration(c.cfg.Metrics.RetentionDays) * 24 * time.Hour)
	_, _ = c.db.Exec("DELETE FROM server_metrics WHERE timestamp < ?", cutoff.Format(time.RFC3339))
	c.lastCleanup = now
}

func (c *Collector) collectNodeExporterMetrics(serverID string, serverDef config.ServerDefinition) (map[string]interface{}, error) {
	url := resolveNodeExporterURL(serverDef)
	if url == "" {
		return nil, fmt.Errorf("node exporter URL not resolved")
	}

	resp, err := c.client.Get(url)
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
		if usage, ok := c.calculateCPUUsage(serverID, parsed.cpuIdle, parsed.cpuTotal); ok {
			metrics["cpu_usage"] = usage
		}
	}

	metrics["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	return metrics, nil
}

func (c *Collector) calculateCPUUsage(serverID string, idle float64, total float64) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	prev, ok := c.cpuSamples[serverID]
	c.cpuSamples[serverID] = cpuSample{timestamp: now, idle: idle, total: total}
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

func (c *Collector) recordMetrics(serverID string, metrics map[string]interface{}, status string) error {
	if c.db == nil {
		return nil
	}

	_, err := c.db.Exec(`
		INSERT INTO server_metrics (
			server_id, cpu_usage, memory_used, memory_total, disk_used, disk_total, network_rx, network_tx, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
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
