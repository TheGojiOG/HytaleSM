package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yourusername/hytale-server-manager/internal/config"
)

type SettingsHandler struct {
	cfg        *config.Config
	configPath string
}

type SettingsPayload struct {
	Security config.SecurityConfig `json:"security"`
	Logging  config.LoggingConfig  `json:"logging"`
	Metrics  config.MetricsConfig  `json:"metrics"`
}

type SettingsResponse struct {
	Security        config.SecurityConfig `json:"security"`
	Logging         config.LoggingConfig  `json:"logging"`
	Metrics         config.MetricsConfig  `json:"metrics"`
	RequiresRestart bool                 `json:"requires_restart"`
}

func NewSettingsHandler(cfg *config.Config) *SettingsHandler {
	return &SettingsHandler{
		cfg:        cfg,
		configPath: config.GetConfigPath(),
	}
}

func (h *SettingsHandler) GetSettings(c *gin.Context) {
	c.JSON(http.StatusOK, SettingsResponse{
		Security:        h.cfg.Security,
		Logging:         h.cfg.Logging,
		Metrics:         h.cfg.Metrics,
		RequiresRestart: true,
	})
}

func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	var payload SettingsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	payload.Security.CORS.AllowedOrigins = normalizeList(payload.Security.CORS.AllowedOrigins)
	payload.Security.CORS.AllowedMethods = normalizeList(payload.Security.CORS.AllowedMethods)

	if payload.Metrics.DefaultInterval <= 0 {
		payload.Metrics.DefaultInterval = h.cfg.Metrics.DefaultInterval
	}
	if payload.Metrics.RetentionDays <= 0 {
		payload.Metrics.RetentionDays = h.cfg.Metrics.RetentionDays
	}

	updated := *h.cfg
	updated.Security = payload.Security
	updated.Logging = payload.Logging
	updated.Metrics = payload.Metrics

	if err := config.Save(&updated, h.configPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save settings", "details": err.Error()})
		return
	}

	h.cfg.Security = updated.Security
	h.cfg.Logging = updated.Logging
	h.cfg.Metrics = updated.Metrics

	c.JSON(http.StatusOK, SettingsResponse{
		Security:        h.cfg.Security,
		Logging:         h.cfg.Logging,
		Metrics:         h.cfg.Metrics,
		RequiresRestart: true,
	})
}

func normalizeList(values []string) []string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		clean = append(clean, trimmed)
	}
	return clean
}
