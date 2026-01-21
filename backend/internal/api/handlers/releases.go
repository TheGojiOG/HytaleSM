package handlers

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/TheGojiOG/HytaleSM/internal/auth"
	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/database"
	"github.com/TheGojiOG/HytaleSM/internal/logging"
	"github.com/TheGojiOG/HytaleSM/internal/releases"
	ws "github.com/TheGojiOG/HytaleSM/internal/websocket"
)

type ReleaseHandler struct {
	cfg             *config.Config
	manager         *releases.Manager
	activityLogger  *logging.ActivityLogger
	hub             *ws.Hub
}

type ReleaseJobResponse struct {
	Job *releases.Job `json:"job"`
}

type ReleaseListResponse struct {
	Releases []*releases.Release `json:"releases"`
}

type ReleaseRequest struct {
	Patchline    string `json:"patchline"`
	DownloadPath string `json:"download_path"`
}

type DownloaderInitRequest struct {
	Force bool `json:"force"`
}

type DownloaderAuthStatusResponse struct {
	Exists    bool   `json:"exists"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Branch    string `json:"branch,omitempty"`
}

const downloaderZipURL = "https://downloader.hytale.com/hytale-downloader.zip"

func NewReleaseHandler(cfg *config.Config, db *database.DB, logger *logging.ActivityLogger, hub *ws.Hub) *ReleaseHandler {
	h := &ReleaseHandler{
		cfg:            cfg,
		manager:        releases.NewManager(cfg, db),
		activityLogger: logger,
		hub:            hub,
	}

	go func() {
		job := h.manager.CreateJob("releases.sync")
		h.manager.SetStatus(job, releases.StatusRunning, nil)
		officialDir := filepath.Join(h.cfg.Storage.ReleasesDir, "official_server_files")
		if err := h.manager.SyncReleasesFromDisk(officialDir, job); err != nil {
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}
		h.manager.SetStatus(job, releases.StatusComplete, nil)
	}()

	return h
}

// HandleReleaseJobWebSocket streams release job output via WebSocket
// WS /ws/releases/jobs/:id
func (h *ReleaseHandler) HandleReleaseJobWebSocket(c *gin.Context) {
	jobID := c.Param("id")
	job, ok := h.manager.GetJob(jobID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	userClaims, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	claims := userClaims.(*auth.Claims)

	upgrader := buildUpgrader(h.cfg.Security.CORS.AllowedOrigins)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	room := fmt.Sprintf("release-job:%s", jobID)
	client := &ws.Client{
		ID:       fmt.Sprintf("release-job-%s-%d", jobID, time.Now().UnixNano()),
		UserID:   claims.UserID,
		Username: claims.Username,
		Conn:     conn,
		Room:     room,
		Send:     make(chan *ws.Message, 256),
		Hub:      h.hub,
	}

	h.hub.Register <- client

	sendEvent := func(event, data string) {
		_ = client.SendMessage("release_job_event", map[string]interface{}{
			"job_id": jobID,
			"event":  event,
			"data":   data,
		})
	}

	for _, line := range job.Output {
		sendEvent("log", line)
	}
	sendEvent("status", string(job.Status))
	if job.NeedsAuth {
		payload := fmt.Sprintf("{\"auth_url\":\"%s\",\"auth_code\":\"%s\"}", escapeJSON(job.AuthURL), escapeJSON(job.AuthCode))
		sendEvent("auth", payload)
	}

	ch, unsubscribe := h.manager.Subscribe(jobID)
	go func() {
		defer unsubscribe()
		for ev := range ch {
			sendEvent(ev.Event, ev.Data)
			if ev.Event == "status" && (ev.Data == string(releases.StatusComplete) || ev.Data == string(releases.StatusFailed)) {
				return
			}
		}
	}()

	go client.WritePump()
	go client.ReadPump()
}

func (h *ReleaseHandler) ListReleases(c *gin.Context) {
	limitParam := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit <= 0 || limit > 500 {
		limit = 50
	}

	includeRemoved := false
	includeParam := strings.ToLower(strings.TrimSpace(c.Query("include_removed")))
	if includeParam == "1" || includeParam == "true" || includeParam == "yes" {
		includeRemoved = true
	}

	items, err := h.manager.ListReleases(limit, includeRemoved)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load releases"})
		return
	}

	c.JSON(http.StatusOK, ReleaseListResponse{Releases: items})
}

func (h *ReleaseHandler) GetRelease(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid release id"})
		return
	}

	release, err := h.manager.GetRelease(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Release not found"})
		return
	}

	c.JSON(http.StatusOK, release)
}

func (h *ReleaseHandler) DeleteRelease(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid release id"})
		return
	}

	release, err := h.manager.GetRelease(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Release not found"})
		return
	}

	if release.FilePath != "" {
		if err := os.Remove(release.FilePath); err != nil && !os.IsNotExist(err) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete release file", "details": err.Error()})
			return
		}
	}

	if err := h.manager.DeleteRelease(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete release"})
		return
	}

	_ = h.activityLogger.LogActivity(&logging.Activity{
		ServerID:     "",
		ActivityType: logging.ActivityConfigUpdate,
		Description:  "Release deleted",
		Metadata: map[string]interface{}{
			"id":      id,
			"version": release.Version,
			"path":    release.FilePath,
		},
		Success: true,
	})

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *ReleaseHandler) ListJobs(c *gin.Context) {
	limitParam := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit <= 0 || limit > 200 {
		limit = 20
	}

	jobs := h.manager.ListJobs(limit)
	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

func (h *ReleaseHandler) GetJob(c *gin.Context) {
	jobID := c.Param("id")
	job, ok := h.manager.GetJob(jobID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	c.JSON(http.StatusOK, ReleaseJobResponse{Job: job})
}

func (h *ReleaseHandler) DownloadRelease(c *gin.Context) {
	var req ReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	patchline := strings.TrimSpace(req.Patchline)
	if patchline == "" {
		patchline = "default"
	}

	job := h.manager.CreateJob("releases.download")
	go func() {
		h.manager.SetStatus(job, releases.StatusRunning, nil)
		if h.cfg != nil {
			h.manager.AppendOutput(job, fmt.Sprintf("Downloader dir: %s", h.cfg.Storage.DownloaderDir))
			h.manager.AppendOutput(job, fmt.Sprintf("Releases dir: %s", h.cfg.Storage.ReleasesDir))
			h.manager.AppendOutput(job, fmt.Sprintf("Credentials path: %s", h.manager.CredentialsPath()))
		}

		downloadPath := strings.TrimSpace(req.DownloadPath)

		args := []string{}
		if downloadPath != "" {
			args = append(args, "-download-path", downloadPath)
		}
		if patchline != "" && patchline != "default" {
			args = append(args, "-patchline", patchline)
		}
		err := h.manager.RunCommand(job, args)
		if err != nil {
			if isAuthFailure(err) || isAuthFailureOutput(job.Output) {
				h.manager.AppendOutput(job, "Authentication failed or expired. Please reset auth and try again.")
				h.manager.SetStatus(job, releases.StatusFailed, fmt.Errorf("authentication failed or expired; reset auth and try again"))
				return
			}
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}

		if downloadPath == "" {
			downloadPath = extractDownloadPathFromOutput(job.Output)
			if downloadPath == "" && h.cfg != nil {
				fallback, err := findLatestZip(h.cfg.Storage.DownloaderDir)
				if err == nil {
					downloadPath = fallback
				}
			}
		}

			if downloadPath == "" {
				h.manager.SetStatus(job, releases.StatusFailed, fmt.Errorf("download completed but output path could not be determined"))
				return
			}

			if h.cfg != nil && !filepath.IsAbs(downloadPath) {
				downloadPath = filepath.Join(h.cfg.Storage.DownloaderDir, downloadPath)
			}

			version := deriveVersionFromFilename(filepath.Base(downloadPath))
			if version == "" {
				version = "unknown"
			}

		if h.cfg != nil {
			officialDir := filepath.Join(h.cfg.Storage.ReleasesDir, "official_server_files")
			if err := os.MkdirAll(officialDir, 0755); err != nil {
				h.manager.SetStatus(job, releases.StatusFailed, err)
				return
			}
			finalPath := filepath.Join(officialDir, filepath.Base(downloadPath))
				if downloadPath != finalPath {
					if err := os.Rename(downloadPath, finalPath); err != nil {
						h.manager.SetStatus(job, releases.StatusFailed, err)
						return
					}
					downloadPath = finalPath
				}
		}

		downloaderVersion, _ := h.manager.GetDownloaderVersion()
		sha, size, err := h.manager.ComputeSHA256(downloadPath)
		if err != nil {
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}

		release := &releases.Release{
			Version:           version,
			Patchline:         patchline,
			FilePath:          downloadPath,
			FileSize:          size,
			SHA256:            sha,
			DownloaderVersion: downloaderVersion,
			DownloadedAt:      time.Now().UTC(),
			Status:            "ready",
			Source:            "downloaded",
			Removed:           false,
		}

		if existing, err := h.manager.GetReleaseByVersionPatchline(version, patchline); err == nil && existing != nil {
			release.ID = existing.ID
			if err := h.manager.UpdateRelease(release); err != nil {
				h.manager.SetStatus(job, releases.StatusFailed, err)
				return
			}
		} else {
			if err := h.manager.InsertRelease(release); err != nil {
				h.manager.SetStatus(job, releases.StatusFailed, err)
				return
			}
		}

		h.manager.SetStatus(job, releases.StatusComplete, nil)
		_ = h.activityLogger.LogActivity(&logging.Activity{
			ServerID:     "",
			ActivityType: logging.ActivityConfigUpdate,
			Description:  "Release downloaded",
			Metadata: map[string]interface{}{
				"version":   version,
				"patchline": patchline,
				"path":      downloadPath,
			},
			Success: true,
		})
	}()

	c.JSON(http.StatusAccepted, ReleaseJobResponse{Job: job})
}

func (h *ReleaseHandler) PrintVersion(c *gin.Context) {
	var req ReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	patchline := strings.TrimSpace(req.Patchline)
	if patchline == "" {
		patchline = "default"
	}

	job := h.manager.CreateJob("releases.print_version")
	go func() {
		h.manager.SetStatus(job, releases.StatusRunning, nil)
		_, err := h.printVersion(job, patchline)
		if err != nil {
			if isAuthFailure(err) || isAuthFailureOutput(job.Output) {
				h.manager.AppendOutput(job, "Authentication failed or expired. Please reset auth and try again.")
				h.manager.SetStatus(job, releases.StatusFailed, fmt.Errorf("authentication failed or expired; reset auth and try again"))
				return
			}
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}
		h.manager.SetStatus(job, releases.StatusComplete, nil)
	}()

	c.JSON(http.StatusAccepted, ReleaseJobResponse{Job: job})
}

func (h *ReleaseHandler) CheckUpdate(c *gin.Context) {
	job := h.manager.CreateJob("releases.check_update")
	go func() {
		h.manager.SetStatus(job, releases.StatusRunning, nil)
		err := h.manager.RunCommand(job, []string{"-check-update"})
		if err != nil {
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}
		h.manager.SetStatus(job, releases.StatusComplete, nil)
	}()

	c.JSON(http.StatusAccepted, ReleaseJobResponse{Job: job})
}

func (h *ReleaseHandler) InitDownloader(c *gin.Context) {
	var req DownloaderInitRequest
	_ = c.ShouldBindJSON(&req)

	job := h.manager.CreateJob("releases.downloader_init")
	go func() {
		h.manager.SetStatus(job, releases.StatusRunning, nil)
		if err := h.installDownloader(job, req.Force); err != nil {
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}
		h.manager.SetStatus(job, releases.StatusComplete, nil)
	}()

	c.JSON(http.StatusAccepted, ReleaseJobResponse{Job: job})
}

func (h *ReleaseHandler) DownloaderVersion(c *gin.Context) {
	job := h.manager.CreateJob("releases.downloader_version")
	go func() {
		h.manager.SetStatus(job, releases.StatusRunning, nil)
		err := h.manager.RunCommand(job, []string{"-version"})
		if err != nil {
			h.manager.SetStatus(job, releases.StatusFailed, err)
			return
		}
		h.manager.SetStatus(job, releases.StatusComplete, nil)
	}()

	c.JSON(http.StatusAccepted, ReleaseJobResponse{Job: job})
}

func (h *ReleaseHandler) DownloaderStatus(c *gin.Context) {
	exists, path := h.manager.DownloaderExists()
	c.JSON(http.StatusOK, gin.H{
		"exists": exists,
		"path":   path,
	})
}

func (h *ReleaseHandler) DownloaderAuthStatus(c *gin.Context) {
	path := h.manager.CredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, DownloaderAuthStatusResponse{Exists: false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read auth status"})
		return
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		c.JSON(http.StatusOK, DownloaderAuthStatusResponse{Exists: true})
		return
	}

	var expiresAt int64
	if value, ok := raw["expires_at"]; ok {
		switch v := value.(type) {
		case float64:
			expiresAt = int64(v)
		case int64:
			expiresAt = v
		case int:
			expiresAt = int64(v)
		}
	}

	branch := ""
	if value, ok := raw["branch"].(string); ok {
		branch = value
	}

	c.JSON(http.StatusOK, DownloaderAuthStatusResponse{
		Exists:    true,
		ExpiresAt: expiresAt,
		Branch:    branch,
	})
}

func (h *ReleaseHandler) ResetDownloaderAuth(c *gin.Context) {
	path := h.manager.CredentialsPath()
	if err := deleteIfExists(path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset credentials", "details": err.Error()})
		return
	}
	_ = h.activityLogger.LogActivity(&logging.Activity{
		ServerID:     "",
		ActivityType: logging.ActivityConfigUpdate,
		Description:  "Downloader credentials reset",
		Metadata: map[string]interface{}{
			"path": path,
		},
		Success: true,
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *ReleaseHandler) installDownloader(job *releases.Job, force bool) error {
	zipPath, err := h.downloadDownloaderZip(job)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)

	if err := os.MkdirAll(h.cfg.Storage.ReleasesDir, 0755); err != nil {
		return fmt.Errorf("failed to create releases dir: %w", err)
	}

	baseTemp := filepath.Join(os.TempDir(), fmt.Sprintf("hsm-downloader-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(baseTemp, 0755); err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(baseTemp)

	if err := unzipToDir(zipPath, baseTemp); err != nil {
		return err
	}

	targetDir := h.cfg.Storage.DownloaderDir
	if targetDir == "" {
		return fmt.Errorf("downloader dir not configured")
	}

	if _, err := os.Stat(targetDir); err == nil {
		if !force {
			return fmt.Errorf("downloader already installed; use update to replace it")
		}
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("failed to remove existing downloader: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return fmt.Errorf("failed to create downloader parent dir: %w", err)
	}

	if err := os.Rename(baseTemp, targetDir); err != nil {
		if copyErr := copyDir(baseTemp, targetDir); copyErr != nil {
			return fmt.Errorf("failed to place downloader: %w", err)
		}
	}

	h.manager.AppendOutput(job, fmt.Sprintf("Downloader installed at %s", targetDir))
	return nil
}

func (h *ReleaseHandler) downloadDownloaderZip(job *releases.Job) (string, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodGet, downloaderZipURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("hytale-downloader-%d.zip", time.Now().UnixNano()))
	out, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", err
	}

	h.manager.AppendOutput(job, fmt.Sprintf("Downloaded %s", downloaderZipURL))
	return zipPath, nil
}

func unzipToDir(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		targetPath := filepath.Join(destDir, cleanName)
		if !strings.HasPrefix(targetPath, destDir+string(os.PathSeparator)) && targetPath != destDir {
			return fmt.Errorf("invalid zip path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create dir: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent dir: %w", err)
		}

		src, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			src.Close()
			return err
		}

		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			src.Close()
			return err
		}
		out.Close()
		src.Close()
	}

	return nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		info, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		in, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			in.Close()
			return err
		}
		out.Close()
		in.Close()
	}

	return nil
}

func (h *ReleaseHandler) printVersion(job *releases.Job, patchline string) (string, error) {
	args := []string{"-print-version"}
	if patchline != "" {
		args = append(args, "-patchline", patchline)
	}
	if err := h.manager.RunCommand(job, args); err != nil {
		return "", err
	}
	return extractVersion(job.Output), nil
}

func extractVersion(lines []string) string {
	if len(lines) == 0 {
		return "unknown"
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "Version:")
		line = strings.TrimPrefix(line, "Latest version:")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "unknown"
}

func sanitizeFilename(value string) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, value)
	clean = strings.Trim(clean, "-")
	if clean == "" {
		return "unknown"
	}
	return clean
}

func escapeJSON(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}

func extractDownloadPathFromOutput(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if !strings.Contains(lower, " to ") {
			continue
		}
		start := strings.Index(line, "to \"")
		if start == -1 {
			continue
		}
		start += len("to \"")
		end := strings.Index(line[start:], "\"")
		if end == -1 {
			continue
		}
		candidate := strings.TrimSpace(line[start : start+end])
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func deriveVersionFromFilename(filename string) string {
	name := strings.TrimSpace(filename)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return strings.TrimSpace(name)
}

func findLatestZip(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var latestPath string
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if latestPath == "" || info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestPath = filepath.Join(dir, entry.Name())
		}
	}
	if latestPath == "" {
		return "", fmt.Errorf("no zip files found in %s", dir)
	}
	return latestPath, nil
}

func deleteIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Remove(path)
}

func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "http status: 403") || strings.Contains(msg, "403 forbidden") {
		return true
	}
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authorization") {
		return true
	}
	if strings.Contains(msg, "oauth") && strings.Contains(msg, "forbidden") {
		return true
	}
	return false
}

func isAuthFailureOutput(lines []string) bool {
	for _, line := range lines {
		msg := strings.ToLower(strings.TrimSpace(line))
		if msg == "" {
			continue
		}
		if strings.Contains(msg, "http status: 403") || strings.Contains(msg, "403 forbidden") {
			return true
		}
		if strings.Contains(msg, "could not get signed url") || strings.Contains(msg, "error fetching server manifest") {
			return true
		}
		if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authorization") {
			return true
		}
		if strings.Contains(msg, "oauth") && strings.Contains(msg, "forbidden") {
			return true
		}
	}
	return false
}
