package middleware

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/TheGojiOG/HytaleSM/internal/auth"
)

const accessTokenCookieName = "hsm_access"

// Auth middleware validates JWT tokens
func Auth(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get authorization header or query token (for WebSocket clients)
		authHeader := c.GetHeader("Authorization")
		token := ""
		if authHeader != "" {
			// Check Bearer token format
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || parts[0] != "Bearer" {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format"})
				c.Abort()
				return
			}
			token = parts[1]
		}

		if token == "" {
			if cookie, err := c.Cookie(accessTokenCookieName); err == nil && cookie != "" {
				token = cookie
			}
		}

		if token == "" {
			token = c.Query("token")
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}

		// Validate token
		claims, err := jwtManager.ValidateAccessToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Store user info in context
		c.Set("user", claims)  // Store full claims object
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("organization_id", claims.OrganizationID)

		c.Next()
	}
}

// RequirePermission checks if the user has a specific permission
func RequirePermission(rbacManager *auth.RBACManager, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			c.Abort()
			return
		}

		permissionsToCheck := append([]string{permission}, legacyPermissions(permission)...)
		allowed := false
		for _, perm := range permissionsToCheck {
			hasPermission, err := rbacManager.HasPermission(userID.(int64), perm)
			if err != nil {
				log.Printf("[RBAC] permission check failed: user=%v permission=%s err=%v", userID, perm, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check permissions"})
				c.Abort()
				return
			}
			if hasPermission {
				allowed = true
				break
			}
		}

		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireServerPermission checks if the user has permission for a specific server
func RequireServerPermission(rbacManager *auth.RBACManager, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			c.Abort()
			return
		}

		serverID := c.Param("id")
		if serverID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Server ID is required"})
			c.Abort()
			return
		}

		permissionsToCheck := append([]string{permission}, legacyPermissions(permission)...)
		allowed := false
		for _, perm := range permissionsToCheck {
			hasPermission, err := rbacManager.HasServerPermission(userID.(int64), serverID, perm)
			if err != nil {
				log.Printf("[RBAC] server permission check failed: user=%v server=%s permission=%s err=%v", userID, serverID, perm, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check permissions"})
				c.Abort()
				return
			}
			if hasPermission {
				allowed = true
				break
			}
		}

		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions for this server"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func legacyPermissions(permission string) []string {
	switch permission {
	case "servers.list", "servers.get", "servers.metrics.read", "servers.metrics.latest", "servers.metrics.live", "servers.activity.read", "servers.status.read":
		return []string{"manage_servers", "server.view"}
	case "servers.create", "servers.update", "servers.delete", "servers.node_exporter.install", "servers.dependencies.install", "servers.releases.deploy":
		return []string{"manage_servers"}
	case "servers.test_connection", "servers.node_exporter.status", "servers.dependencies.check":
		return []string{"manage_servers", "server.view"}
	case "servers.start":
		return []string{"server.start", "manage_servers"}
	case "servers.stop":
		return []string{"server.stop", "manage_servers"}
	case "servers.restart":
		return []string{"server.restart", "manage_servers"}
	case "servers.console.view":
		return []string{"server.console.view", "server.view"}
	case "servers.console.execute":
		return []string{"server.console.execute", "server.start"}
	case "servers.console.history.read", "servers.console.history.search", "servers.console.autocomplete":
		return []string{"server.console.view"}
	case "servers.tasks.read", "servers.transfer.benchmark":
		return []string{"manage_tasks", "server.view"}
	case "servers.backups.create":
		return []string{"server.backup.create", "manage_backups"}
	case "servers.backups.restore":
		return []string{"server.backup.restore", "manage_backups"}
	case "servers.backups.list", "servers.backups.get", "servers.backups.delete", "servers.backups.retention.enforce":
		return []string{"manage_backups", "server.view"}
	case "settings.get", "settings.update":
		return []string{"system_settings"}
	default:
		return nil
	}
}
