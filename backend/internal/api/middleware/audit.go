package middleware

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// Audit logs every API action into audit_logs
func Audit(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		if path == "/health" {
			return
		}

		action := fmt.Sprintf("%s %s", c.Request.Method, path)
		status := c.Writer.Status()
		success := status < 400

		var userIDValue interface{}
		if value, exists := c.Get("user_id"); exists {
			userIDValue = value.(int64)
		} else {
			userIDValue = nil
		}

		resourceType, resourceID := deriveResource(path, c)

		detailsJSON, _ := json.Marshal(map[string]interface{}{
			"status": status,
		})

		_, _ = db.Exec(`
			INSERT INTO audit_logs (user_id, action, resource_type, resource_id, ip_address, user_agent, success, details)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, userIDValue, action, resourceType, resourceID, c.ClientIP(), c.Request.UserAgent(), success, string(detailsJSON))
	}
}

func deriveResource(path string, c *gin.Context) (string, string) {
	resourceID := ""
	if id := c.Param("id"); id != "" {
		resourceID = id
	} else if id := c.Param("backupId"); id != "" {
		resourceID = id
	}

	resourceType := ""
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) >= 3 && segments[0] == "api" {
		resourceType = segments[2]
	} else if len(segments) > 0 {
		resourceType = segments[0]
	}

	return resourceType, resourceID
}
