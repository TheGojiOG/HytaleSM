package releases

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/database"
)

type JobStatus string

const (
	StatusQueued   JobStatus = "queued"
	StatusRunning  JobStatus = "running"
	StatusFailed   JobStatus = "failed"
	StatusComplete JobStatus = "complete"
)

type Job struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	Status     JobStatus  `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Output     []string   `json:"output"`
	Error      string     `json:"error,omitempty"`
	NeedsAuth  bool       `json:"needs_auth"`
	AuthURL    string     `json:"auth_url,omitempty"`
	AuthCode   string     `json:"auth_code,omitempty"`
}

type StreamEvent struct {
	Event string
	Data  string
}

type Release struct {
	ID               int64     `json:"id"`
	Version          string    `json:"version"`
	Patchline        string    `json:"patchline"`
	FilePath         string    `json:"file_path"`
	FileSize         int64     `json:"file_size"`
	SHA256           string    `json:"sha256"`
	DownloaderVersion string   `json:"downloader_version"`
	DownloadedAt     time.Time `json:"downloaded_at"`
	Status           string    `json:"status"`
	Source           string    `json:"source"`
	Removed          bool      `json:"removed"`
}

type Manager struct {
	cfg *config.Config
	db  *database.DB

	mu   sync.Mutex
	jobs map[string]*Job
	subs map[string]map[chan StreamEvent]struct{}
}

func NewManager(cfg *config.Config, db *database.DB) *Manager {
	return &Manager{
		cfg:  cfg,
		db:   db,
		jobs: make(map[string]*Job),
		subs: make(map[string]map[chan StreamEvent]struct{}),
	}
}

func (m *Manager) CreateJob(action string) *Job {
	job := &Job{
		ID:        fmt.Sprintf("job-%d", time.Now().UnixNano()),
		Action:    action,
		Status:    StatusQueued,
		CreatedAt: time.Now(),
		Output:    []string{},
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	if _, ok := m.subs[job.ID]; !ok {
		m.subs[job.ID] = make(map[chan StreamEvent]struct{})
	}
	m.mu.Unlock()

	_ = m.insertJob(job)
	return job
}

func (m *Manager) GetJob(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	return job, ok
}

func (m *Manager) ListJobs(limit int) []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	if limit > 0 && len(jobs) > limit {
		return jobs[:limit]
	}
	return jobs
}

func (m *Manager) AppendOutput(job *Job, line string) {
	m.mu.Lock()
	job.Output = append(job.Output, line)
	m.parseAuthPrompt(job, line)
	m.mu.Unlock()
	m.emit(job.ID, StreamEvent{Event: "log", Data: line})
	if job.NeedsAuth {
		payload := fmt.Sprintf("{\"auth_url\":\"%s\",\"auth_code\":\"%s\"}", escapeJSON(job.AuthURL), escapeJSON(job.AuthCode))
		m.emit(job.ID, StreamEvent{Event: "auth", Data: payload})
	}
}

func (m *Manager) SetStatus(job *Job, status JobStatus, err error) {
	now := time.Now()
	m.mu.Lock()
	job.Status = status
	if status == StatusRunning {
		job.StartedAt = &now
	}
	if status == StatusFailed || status == StatusComplete {
		job.FinishedAt = &now
		if err != nil {
			job.Error = err.Error()
		}
	}
	m.mu.Unlock()
	m.emit(job.ID, StreamEvent{Event: "status", Data: string(status)})

	_ = m.updateJob(job)
}

func (m *Manager) Subscribe(jobID string) (chan StreamEvent, func()) {
	ch := make(chan StreamEvent, 64)
	m.mu.Lock()
	if _, ok := m.subs[jobID]; !ok {
		m.subs[jobID] = make(map[chan StreamEvent]struct{})
	}
	m.subs[jobID][ch] = struct{}{}
	m.mu.Unlock()

	return ch, func() {
		m.mu.Lock()
		if subs, ok := m.subs[jobID]; ok {
			delete(subs, ch)
		}
		m.mu.Unlock()
		close(ch)
	}
}

func (m *Manager) emit(jobID string, event StreamEvent) {
	m.mu.Lock()
	subs := m.subs[jobID]
	m.mu.Unlock()

	for ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (m *Manager) RunCommand(job *Job, args []string) error {
	binaryPath, err := m.getDownloaderPath()
	if err != nil {
		return err
	}

	if job != nil {
		m.AppendOutput(job, fmt.Sprintf("Running command: %s %s", binaryPath, strings.Join(args, " ")))
	}

	cmd := exec.Command(binaryPath, args...)
	if strings.TrimSpace(m.cfg.Storage.DownloaderDir) != "" {
		cmd.Dir = m.cfg.Storage.DownloaderDir
	} else if strings.TrimSpace(m.cfg.Storage.ReleasesDir) != "" {
		cmd.Dir = m.cfg.Storage.ReleasesDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	outputCh := make(chan string, 32)
	waitCh := make(chan error, 1)
	readPipe := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Split(splitOnNewlineOrCarriageReturn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			outputCh <- scanner.Text()
		}
	}

	go readPipe(stdout)
	go readPipe(stderr)

	go func() {
		waitCh <- cmd.Wait()
		close(outputCh)
	}()

	for line := range outputCh {
		m.AppendOutput(job, line)
	}

	return <-waitCh
}

func (m *Manager) getDownloaderPath() (string, error) {
	base := m.cfg.Storage.DownloaderDir
	if base == "" {
		base = filepath.Join(m.cfg.Storage.ReleasesDir, "hytale-downloader")
	}
	binary := "hytale-downloader-linux-amd64"
	if runtime.GOOS == "windows" {
		binary = "hytale-downloader-windows-amd64.exe"
	}
	path := filepath.Join(base, binary)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("downloader binary not found: %s", path)
	}
	return path, nil
}

// DownloaderExists checks whether the downloader binary is present.
func (m *Manager) DownloaderExists() (bool, string) {
	base := m.cfg.Storage.DownloaderDir
	if base == "" {
		base = filepath.Join(m.cfg.Storage.ReleasesDir, "hytale-downloader")
	}
	binary := "hytale-downloader-linux-amd64"
	if runtime.GOOS == "windows" {
		binary = "hytale-downloader-windows-amd64.exe"
	}
	path := filepath.Join(base, binary)
	if _, err := os.Stat(path); err != nil {
		return false, path
	}
	return true, path
}

func (m *Manager) EnsureReleaseDir(patchline string) (string, error) {
	root := filepath.Join(m.cfg.Storage.ReleasesDir, "releases", patchline)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	return root, nil
}

func (m *Manager) CredentialsPath() string {
	base := m.cfg.Storage.DownloaderDir
	if strings.TrimSpace(base) == "" {
		base = m.cfg.Storage.ReleasesDir
	}
	return filepath.Join(base, ".hytale-downloader-credentials.json")
}

func (m *Manager) ComputeSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func (m *Manager) InsertRelease(release *Release) error {
	if m.db == nil {
		return nil
	}
	if strings.TrimSpace(release.Source) == "" {
		release.Source = "downloaded"
	}

	result, err := m.db.Exec(`
		INSERT INTO releases (version, patchline, file_path, file_size, sha256, downloader_version, downloaded_at, status, source, removed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, release.Version, release.Patchline, release.FilePath, release.FileSize, release.SHA256, release.DownloaderVersion, release.DownloadedAt, release.Status, release.Source, boolToInt(release.Removed))
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err == nil {
		release.ID = id
	}
	return nil
}

func (m *Manager) UpdateRelease(release *Release) error {
	if m.db == nil {
		return nil
	}
	if strings.TrimSpace(release.Source) == "" {
		release.Source = "downloaded"
	}
	_, err := m.db.Exec(`
		UPDATE releases
		SET version = ?, patchline = ?, file_path = ?, file_size = ?, sha256 = ?, downloader_version = ?, downloaded_at = ?, status = ?, source = ?, removed = ?
		WHERE id = ?
	`, release.Version, release.Patchline, release.FilePath, release.FileSize, release.SHA256, release.DownloaderVersion, release.DownloadedAt, release.Status, release.Source, boolToInt(release.Removed), release.ID)
	return err
}

func (m *Manager) ListReleases(limit int, includeRemoved bool) ([]*Release, error) {
	if m.db == nil {
		return []*Release{}, nil
	}

	query := `
		SELECT id, version, patchline, file_path, file_size, sha256, downloader_version, downloaded_at, status, source, removed
		FROM releases
		ORDER BY downloaded_at DESC
	`
	if !includeRemoved {
		query = strings.Replace(query, "FROM releases", "FROM releases\n\t\tWHERE removed = 0", 1)
	}
	if limit > 0 {
		query += " LIMIT ?"
	}

	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = m.db.Query(query, limit)
	} else {
		rows, err = m.db.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	releases := []*Release{}
	for rows.Next() {
		release := &Release{}
		var removed int
		if err := rows.Scan(&release.ID, &release.Version, &release.Patchline, &release.FilePath, &release.FileSize, &release.SHA256, &release.DownloaderVersion, &release.DownloadedAt, &release.Status, &release.Source, &removed); err != nil {
			continue
		}
		release.Removed = removed != 0
		releases = append(releases, release)
	}
	return releases, nil
}

func (m *Manager) GetRelease(id int64) (*Release, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	row := m.db.QueryRow(`
		SELECT id, version, patchline, file_path, file_size, sha256, downloader_version, downloaded_at, status, source, removed
		FROM releases
		WHERE id = ?
	`, id)
	release := &Release{}
	var removed int
	if err := row.Scan(&release.ID, &release.Version, &release.Patchline, &release.FilePath, &release.FileSize, &release.SHA256, &release.DownloaderVersion, &release.DownloadedAt, &release.Status, &release.Source, &removed); err != nil {
		return nil, err
	}
	release.Removed = removed != 0
	return release, nil
}

func (m *Manager) DeleteRelease(id int64) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.Exec(`
		DELETE FROM releases WHERE id = ?
	`, id)
	return err
}

func (m *Manager) ListAllReleases() ([]*Release, error) {
	if m.db == nil {
		return []*Release{}, nil
	}
	rows, err := m.db.Query(`
		SELECT id, version, patchline, file_path, file_size, sha256, downloader_version, downloaded_at, status, source, removed
		FROM releases
		ORDER BY downloaded_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	releases := []*Release{}
	for rows.Next() {
		release := &Release{}
		var removed int
		if err := rows.Scan(&release.ID, &release.Version, &release.Patchline, &release.FilePath, &release.FileSize, &release.SHA256, &release.DownloaderVersion, &release.DownloadedAt, &release.Status, &release.Source, &removed); err != nil {
			continue
		}
		release.Removed = removed != 0
		releases = append(releases, release)
	}
	return releases, nil
}

func (m *Manager) GetDownloaderVersion() (string, error) {
	job := &Job{Output: []string{}}
	if err := m.RunCommand(job, []string{"-version"}); err != nil {
		return "", err
	}
	return strings.Join(job.Output, "\n"), nil
}

func (m *Manager) GetReleaseByVersionPatchline(version string, patchline string) (*Release, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	row := m.db.QueryRow(`
		SELECT id, version, patchline, file_path, file_size, sha256, downloader_version, downloaded_at, status, source, removed
		FROM releases
		WHERE version = ? AND patchline = ?
		ORDER BY downloaded_at DESC
		LIMIT 1
	`, version, patchline)
	release := &Release{}
	var removed int
	if err := row.Scan(&release.ID, &release.Version, &release.Patchline, &release.FilePath, &release.FileSize, &release.SHA256, &release.DownloaderVersion, &release.DownloadedAt, &release.Status, &release.Source, &removed); err != nil {
		return nil, err
	}
	release.Removed = removed != 0
	return release, nil
}

func (m *Manager) parseAuthPrompt(job *Job, line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	if strings.HasPrefix(trimmed, "https://") && strings.Contains(trimmed, "oauth.accounts.hytale.com") {
		job.AuthURL = trimmed
		job.NeedsAuth = true
	}

	if strings.HasPrefix(trimmed, "Authorization code:") {
		code := strings.TrimSpace(strings.TrimPrefix(trimmed, "Authorization code:"))
		if code != "" {
			job.AuthCode = code
			job.NeedsAuth = true
		}
	}

	if strings.Contains(trimmed, "Please visit the following URL to authenticate") {
		job.NeedsAuth = true
	}
}

func (m *Manager) insertJob(job *Job) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.Exec(`
		INSERT INTO release_jobs (id, action, status, created_at)
		VALUES (?, ?, ?, ?)
	`, job.ID, job.Action, job.Status, job.CreatedAt)
	return err
}

func (m *Manager) updateJob(job *Job) error {
	if m.db == nil {
		return nil
	}
	output := strings.Join(job.Output, "\n")
	_, err := m.db.Exec(`
		UPDATE release_jobs
		SET status = ?, started_at = ?, finished_at = ?, output = ?, error = ?
		WHERE id = ?
	`, job.Status, job.StartedAt, job.FinishedAt, output, job.Error, job.ID)
	return err
}

func escapeJSON(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}

func (m *Manager) SyncReleasesFromDisk(officialDir string, job *Job) error {
	if m.db == nil {
		return nil
	}
	if strings.TrimSpace(officialDir) == "" {
		return fmt.Errorf("official releases directory not configured")
	}

	if err := os.MkdirAll(officialDir, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(officialDir)
	if err != nil {
		return err
	}

	releases, err := m.ListAllReleases()
	if err != nil {
		return err
	}

	byPath := make(map[string]*Release)
	byVersion := make(map[string]*Release)
	for _, rel := range releases {
		byPath[filepath.Clean(rel.FilePath)] = rel
		if rel.Version != "" {
			if existing, ok := byVersion[rel.Version]; !ok || (existing.Removed && !rel.Removed) {
				byVersion[rel.Version] = rel
			}
		}
	}

	found := make(map[int64]struct{})
	cleanDir := filepath.Clean(officialDir)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			continue
		}
		path := filepath.Join(officialDir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sha, size, err := m.ComputeSHA256(path)
		if err != nil {
			continue
		}
		version := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
		modTime := info.ModTime().UTC()
		var target *Release
		if existing, ok := byPath[filepath.Clean(path)]; ok {
			target = existing
		} else if version != "" {
			if existing, ok := byVersion[version]; ok {
				target = existing
			}
		}

		if target == nil {
			newRelease := &Release{
				Version:     version,
				Patchline:   "manual",
				FilePath:    path,
				FileSize:    size,
				SHA256:      sha,
				DownloadedAt: modTime,
				Status:      "ready",
				Source:      "user_added",
				Removed:     false,
			}
			if err := m.InsertRelease(newRelease); err != nil {
				return err
			}
			found[newRelease.ID] = struct{}{}
			if job != nil {
				m.AppendOutput(job, fmt.Sprintf("Detected manual release: %s", path))
			}
			continue
		}

		target.FilePath = path
		target.FileSize = size
		target.SHA256 = sha
		target.DownloadedAt = modTime
		target.Status = "ready"
		target.Removed = false
		if strings.TrimSpace(target.Source) == "" {
			target.Source = "user_added"
		}
		if err := m.UpdateRelease(target); err != nil {
			return err
		}
		found[target.ID] = struct{}{}
	}

	for _, rel := range releases {
		if _, ok := found[rel.ID]; ok {
			continue
		}
		cleanPath := filepath.Clean(rel.FilePath)
		if !strings.HasPrefix(cleanPath, cleanDir) {
			if rel.Removed {
				continue
			}
			rel.Removed = true
			rel.Status = "removed"
			if err := m.UpdateRelease(rel); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(rel.FilePath); err != nil {
			if rel.Removed {
				continue
			}
			rel.Removed = true
			rel.Status = "removed"
			if err := m.UpdateRelease(rel); err != nil {
				return err
			}
		}
	}

	if job != nil {
		m.AppendOutput(job, "Release sync complete")
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func splitOnNewlineOrCarriageReturn(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
