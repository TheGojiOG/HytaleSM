package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/yourusername/hytale-server-manager/agent/config"
	"github.com/yourusername/hytale-server-manager/agent/ports"
	"github.com/yourusername/hytale-server-manager/agent/systemd"
)

const agentVersion = "0.1.0"

type metrics struct {
	eventsSent     uint64
	configAck      uint64
	configNack     uint64
	streamErrors   uint64
	lastConfigVers uint64
}

func main() {
	bootstrapPath := flag.String("bootstrap", "/etc/hytale-agent/bootstrap.json", "bootstrap config path")
	metricsAddr := flag.String("metrics-addr", "127.0.0.1:9098", "metrics bind address")
	stateAddr := flag.String("state-addr", "0.0.0.0:9443", "state HTTPS bind address")
	stateCert := flag.String("state-cert", "/etc/hytale-agent/https/server.crt", "state HTTPS cert path")
	stateKey := flag.String("state-key", "/etc/hytale-agent/https/server.key", "state HTTPS key path")
	stateCA := flag.String("state-ca", "/etc/hytale-agent/https/ca.crt", "client CA cert for mTLS")
	statePath := flag.String("state-path", "/var/lib/hytale-agent/state.json", "state output path")
	flag.Parse()

	boot, err := config.LoadBootstrap(*bootstrapPath)
	if err != nil {
		log.Fatalf("bootstrap config error: %v", err)
	}

	hostID := boot.HostUUID
	if hostID == "" {
		id, err := readMachineID()
		if err != nil {
			hostID = uuid.NewString()
		} else {
			hostID = id
		}
	}

	monitorCfg := boot.MonitorConfig
	if monitorCfg == nil && boot.MonitorConfigPath != "" {
		monitorCfg, err = config.LoadMonitorConfig(boot.MonitorConfigPath)
		if err != nil {
			log.Printf("monitor config load error: %v", err)
		}
	}
	if monitorCfg == nil {
		monitorCfg = &config.MonitorConfig{Version: 1, IntervalMs: config.DefaultIntervalMs}
		_ = monitorCfg.Validate()
		monitorCfg.Normalize()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &metrics{}

	_ = boot

	stateWriter := newStateWriter(*statePath)
	currentState := &agentState{
		HostUUID: hostID,
		Services: make(map[string]string),
		Ports:    make(map[int]bool),
		Java:     []ports.JavaProcess{},
	}

	initialServices, _ := systemd.Snapshot(monitorCfg.Services)
	for svc, st := range initialServices {
		currentState.Services[svc] = st
	}
	initialPorts, initialJava := ports.Snapshot(monitorCfg.Ports)
	for p, open := range initialPorts {
		currentState.Ports[p] = open
	}
	currentState.Java = initialJava
	store := newStateStore(currentState, stateWriter)
	store.WriteNow()

	go serveMetrics(*metricsAddr, m)
	go serveStateTLS(*stateAddr, *stateCert, *stateKey, *stateCA, store)
	var watcherCancel context.CancelFunc

	applyConfig := func(cfg *config.MonitorConfig) {
		if watcherCancel != nil {
			watcherCancel()
		}
		watchCtx, cancel := context.WithCancel(ctx)
		watcherCancel = cancel

		interval := time.Duration(cfg.IntervalMs) * time.Millisecond

		go func() {
			_ = systemd.Watch(watchCtx, cfg.Services, func(ev systemd.Event) {
				store.Update(func(st *agentState) {
					st.Services[ev.Service] = ev.NewState
					st.Timestamp = ev.Timestamp
				})
				atomic.AddUint64(&m.eventsSent, 1)
			})
		}()

		go ports.Watch(watchCtx, cfg.Ports, interval, func(pe ports.PortEvent) {
			store.Update(func(st *agentState) {
				st.Ports[pe.Port] = pe.Open
				st.Timestamp = pe.Timestamp
			})
			atomic.AddUint64(&m.eventsSent, 1)
		}, func(java []ports.JavaProcess) {
			store.Update(func(st *agentState) {
				st.Java = java
				st.Timestamp = time.Now().Unix()
			})
			atomic.AddUint64(&m.eventsSent, 1)
		})
	}

	applyConfig(monitorCfg)

	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			store.Update(func(st *agentState) {
				st.Timestamp = time.Now().Unix()
			})
		}
	}
}

type agentState struct {
	HostUUID  string              `json:"host_uuid"`
	Timestamp int64               `json:"timestamp"`
	Services  map[string]string   `json:"services"`
	Ports     map[int]bool        `json:"ports"`
	Java      []ports.JavaProcess `json:"java"`
}

type stateWriter struct {
	path string
}

func newStateWriter(path string) *stateWriter {
	return &stateWriter{path: path}
}

func (w *stateWriter) Write(state *agentState) {
	if state == nil || w.path == "" {
		return
	}
	if state.Timestamp == 0 {
		state.Timestamp = time.Now().Unix()
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(w.path), 0755)
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, w.path)
}

type stateStore struct {
	mu     sync.RWMutex
	state  agentState
	writer *stateWriter
}

func newStateStore(initial *agentState, writer *stateWriter) *stateStore {
	if initial == nil {
		initial = &agentState{Services: map[string]string{}, Ports: map[int]bool{}, Java: []ports.JavaProcess{}}
	}
	return &stateStore{state: cloneAgentState(initial), writer: writer}
}

func (s *stateStore) Update(fn func(*agentState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn != nil {
		fn(&s.state)
	}
	if s.state.Timestamp == 0 {
		s.state.Timestamp = time.Now().Unix()
	}
	if s.writer != nil {
		s.writer.Write(&s.state)
	}
}

func (s *stateStore) Snapshot() agentState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneAgentState(&s.state)
}

func (s *stateStore) WriteNow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Timestamp == 0 {
		s.state.Timestamp = time.Now().Unix()
	}
	if s.writer != nil {
		s.writer.Write(&s.state)
	}
}

func cloneAgentState(src *agentState) agentState {
	if src == nil {
		return agentState{Services: map[string]string{}, Ports: map[int]bool{}, Java: []ports.JavaProcess{}}
	}
	clone := agentState{
		HostUUID:  src.HostUUID,
		Timestamp: src.Timestamp,
		Services:  make(map[string]string, len(src.Services)),
		Ports:     make(map[int]bool, len(src.Ports)),
		Java:      make([]ports.JavaProcess, len(src.Java)),
	}
	for k, v := range src.Services {
		clone.Services[k] = v
	}
	for k, v := range src.Ports {
		clone.Ports[k] = v
	}
	copy(clone.Java, src.Java)
	return clone
}

func readMachineID() (string, error) {
	paths := []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("machine-id not found")
}

func serveMetrics(addr string, m *metrics) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "agent_events_sent %d\n", atomic.LoadUint64(&m.eventsSent))
		fmt.Fprintf(w, "agent_config_ack_total %d\n", atomic.LoadUint64(&m.configAck))
		fmt.Fprintf(w, "agent_config_nack_total %d\n", atomic.LoadUint64(&m.configNack))
		fmt.Fprintf(w, "agent_stream_errors_total %d\n", atomic.LoadUint64(&m.streamErrors))
		fmt.Fprintf(w, "agent_config_version %d\n", atomic.LoadUint64(&m.lastConfigVers))
	})
	_ = http.ListenAndServe(addr, mux)
}

func serveStateTLS(addr, certPath, keyPath, caPath string, store *stateStore) {
	if addr == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state := store.Snapshot()
		_ = json.NewEncoder(w).Encode(state)
	})
	pool := x509.NewCertPool()
	if caPath != "" {
		if data, err := os.ReadFile(caPath); err == nil {
			pool.AppendCertsFromPEM(data)
		}
	}
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs:  pool,
		},
	}
	if err := server.ListenAndServeTLS(certPath, keyPath); err != nil {
		log.Printf("state https server error: %v", err)
	}
}
