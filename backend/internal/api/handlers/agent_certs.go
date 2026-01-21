package handlers

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"archive/tar"

	"github.com/gin-gonic/gin"
	"github.com/TheGojiOG/HytaleSM/internal/agentcert"
	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/database"
)

type AgentHandler struct {
	cfg *config.Config
	db  *database.DB
}

type agentCertRequest struct {
	Token    string `json:"token"`
	HostUUID string `json:"host_uuid"`
}

func NewAgentHandler(cfg *config.Config, db *database.DB) *AgentHandler {
	return &AgentHandler{cfg: cfg, db: db}
}

func (h *AgentHandler) DownloadBinary(c *gin.Context) {
	arch := c.Query("arch")
	if arch == "" {
		arch = "amd64"
	}
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch != "amd64" && arch != "arm64" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported arch"})
		return
	}

	path := filepath.Join(h.cfg.Storage.DataDir, "agent-binaries", "hytale-agent-linux-"+arch)
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent binary not found"})
		return
	}

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename=hytale-agent-linux-"+arch)
	c.File(path)
}

func (h *AgentHandler) IssueCertificate(c *gin.Context) {
	var req agentCertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Token == "" || req.HostUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token and host_uuid are required"})
		return
	}

	caDir := filepath.Join(h.cfg.Storage.DataDir, "agent-ca")
	ca, err := agentcert.LoadOrCreateCA(caDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load CA"})
		return
	}

	tx, err := h.db.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start transaction"})
		return
	}
	defer tx.Rollback()

	serverID, err := agentcert.ConsumeRequest(tx, req.Token, req.HostUUID, c.ClientIP())
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "request not found or expired"})
		return
	}

	certPEM, keyPEM, serial, notAfter, fingerprint, err := agentcert.IssueAgentCert(ca, req.HostUUID, serverID, 365*24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue cert"})
		return
	}

	if err := agentcert.InsertCertificate(tx, serverID, req.HostUUID, serial, fingerprint, certPEM, notAfter); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store cert"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize cert"})
		return
	}

	payload, err := buildCertArchive(certPEM, keyPEM, ca.CertPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build response"})
		return
	}

	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", "attachment; filename=agent-certs.tgz")
	c.Data(http.StatusOK, "application/gzip", payload)
}

func buildCertArchive(certPEM, keyPEM, caPEM []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gz)

	if err := writeTarFile(tarWriter, "agent.crt", 0644, certPEM); err != nil {
		return nil, err
	}
	if err := writeTarFile(tarWriter, "agent.key", 0600, keyPEM); err != nil {
		return nil, err
	}
	if err := writeTarFile(tarWriter, "ca.crt", 0644, caPEM); err != nil {
		return nil, err
	}

	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func writeTarFile(tw *tar.Writer, name string, mode int64, data []byte) error {
	hdr := &tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
