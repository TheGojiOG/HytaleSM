package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// IAMHandler handles roles and permissions management
type IAMHandler struct {
	db *sql.DB
}

// NewIAMHandler creates a new IAM handler
func NewIAMHandler(db *sql.DB) *IAMHandler {
	return &IAMHandler{db: db}
}

type roleResponse struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// ListPermissions returns all permissions
func (h *IAMHandler) ListPermissions(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, name, description, category
		FROM permissions
		ORDER BY category, name
	`)
	if err != nil {
		log.Printf("[IAM] list permissions query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list permissions"})
		return
	}
	defer rows.Close()

	var permissions []gin.H
	for rows.Next() {
		var id int64
		var name string
		var description sql.NullString
		var category sql.NullString
		if err := rows.Scan(&id, &name, &description, &category); err != nil {
			log.Printf("[IAM] scan permission failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan permission"})
			return
		}
		permissions = append(permissions, gin.H{
			"id":          id,
			"name":        name,
			"description": nullToString(description),
			"category":    nullToString(category),
		})
	}

	if err := rows.Err(); err != nil {
		log.Printf("[IAM] list permissions rows error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list permissions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"permissions": permissions})
}

// ListRoles returns all roles with permissions
func (h *IAMHandler) ListRoles(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT r.id, r.name, r.description, p.name
		FROM roles r
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		LEFT JOIN permissions p ON p.id = rp.permission_id
		ORDER BY r.name, p.name
	`)
	if err != nil {
		log.Printf("[IAM] list roles query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list roles"})
		return
	}
	defer rows.Close()

	roles := map[int64]*roleResponse{}
	for rows.Next() {
		var roleID int64
		var roleName string
		var roleDescription sql.NullString
		var permissionName sql.NullString
		if err := rows.Scan(&roleID, &roleName, &roleDescription, &permissionName); err != nil {
			log.Printf("[IAM] scan role failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan role"})
			return
		}
		role, exists := roles[roleID]
		if !exists {
			role = &roleResponse{ID: roleID, Name: roleName, Description: nullToString(roleDescription)}
			roles[roleID] = role
		}
		if permissionName.Valid {
			role.Permissions = append(role.Permissions, permissionName.String)
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("[IAM] list roles rows error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list roles"})
		return
	}

	result := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		result = append(result, *role)
	}

	c.JSON(http.StatusOK, gin.H{"roles": result})
}

// GetRole returns a specific role with permissions
func (h *IAMHandler) GetRole(c *gin.Context) {
	roleID := c.Param("id")
	rows, err := h.db.Query(`
		SELECT r.id, r.name, r.description, p.name
		FROM roles r
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		LEFT JOIN permissions p ON p.id = rp.permission_id
		WHERE r.id = ?
		ORDER BY p.name
	`, roleID)
	if err != nil {
		log.Printf("[IAM] get role query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get role"})
		return
	}
	defer rows.Close()

	var role *roleResponse
	for rows.Next() {
		var id int64
		var name string
		var description sql.NullString
		var permissionName sql.NullString
		if err := rows.Scan(&id, &name, &description, &permissionName); err != nil {
			log.Printf("[IAM] scan role detail failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan role"})
			return
		}
		if role == nil {
			role = &roleResponse{ID: id, Name: name, Description: nullToString(description)}
		}
		if permissionName.Valid {
			role.Permissions = append(role.Permissions, permissionName.String)
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("[IAM] get role rows error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get role"})
		return
	}
	if role == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role not found"})
		return
	}

	c.JSON(http.StatusOK, role)
}

// CreateRole creates a new role
func (h *IAMHandler) CreateRole(c *gin.Context) {
	var req struct {
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Permissions []string `json:"permission_names"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	result, err := tx.Exec("INSERT INTO roles (name, description) VALUES (?, ?)", req.Name, req.Description)
	if err != nil {
		log.Printf("[IAM] create role failed: %v", err)
		c.JSON(http.StatusConflict, gin.H{"error": "Role already exists"})
		return
	}

	roleID, err := result.LastInsertId()
	if err != nil {
		log.Printf("[IAM] create role id failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create role"})
		return
	}

	if len(req.Permissions) > 0 {
		if err := assignRolePermissions(tx, roleID, req.Permissions); err != nil {
			log.Printf("[IAM] assign role permissions failed: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[IAM] create role commit failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": roleID, "message": "Role created successfully"})
}

// UpdateRole updates role metadata
func (h *IAMHandler) UpdateRole(c *gin.Context) {
	roleID := c.Param("id")
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query := "UPDATE roles SET name = COALESCE(?, name), description = COALESCE(?, description) WHERE id = ?"
	result, err := h.db.Exec(query, req.Name, req.Description, roleID)
	if err != nil {
		log.Printf("[IAM] update role failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update role"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Role updated successfully"})
}

// DeleteRole deletes a role
func (h *IAMHandler) DeleteRole(c *gin.Context) {
	roleID := c.Param("id")
	result, err := h.db.Exec("DELETE FROM roles WHERE id = ?", roleID)
	if err != nil {
		log.Printf("[IAM] delete role failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete role"})
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Role deleted successfully"})
}

// SetRolePermissions replaces role permissions
func (h *IAMHandler) SetRolePermissions(c *gin.Context) {
	roleID := c.Param("id")
	roleIDInt, err := strconv.ParseInt(roleID, 10, 64)
	if err != nil {
		log.Printf("[IAM] invalid role id: %s", roleID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role id"})
		return
	}
	var req struct {
		Permissions []string `json:"permission_names" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM role_permissions WHERE role_id = ?", roleIDInt); err != nil {
		log.Printf("[IAM] clear role permissions failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear role permissions"})
		return
	}

	if err := assignRolePermissions(tx, roleIDInt, req.Permissions); err != nil {
		log.Printf("[IAM] set role permissions failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[IAM] set role permissions commit failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Role permissions updated successfully"})
}

// ListAuditLogs returns audit log entries
func (h *IAMHandler) ListAuditLogs(c *gin.Context) {
	limit := 100
	offset := 0
	if value := c.Query("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			if parsed > 0 && parsed <= 500 {
				limit = parsed
			}
		}
	}
	if value := c.Query("offset"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	rows, err := h.db.Query(`
		SELECT id, user_id, action, resource_type, resource_id, ip_address, user_agent, success, details, created_at
		FROM audit_logs
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		log.Printf("[IAM] list audit logs query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list audit logs"})
		return
	}
	defer rows.Close()

	var logs []gin.H
	for rows.Next() {
		var id int64
		var userID sql.NullInt64
		var action, resourceType, resourceID, ipAddress, userAgent, details string
		var success bool
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &action, &resourceType, &resourceID, &ipAddress, &userAgent, &success, &details, &createdAt); err != nil {
			log.Printf("[IAM] scan audit log failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan audit log"})
			return
		}

		var uid *int64
		if userID.Valid {
			value := userID.Int64
			uid = &value
		}

		logs = append(logs, gin.H{
			"id":            id,
			"user_id":       uid,
			"action":        action,
			"resource_type": resourceType,
			"resource_id":   resourceID,
			"ip_address":    ipAddress,
			"user_agent":    userAgent,
			"success":       success,
			"details":       details,
			"created_at":    createdAt,
		})
	}

	if err := rows.Err(); err != nil {
		log.Printf("[IAM] list audit logs rows error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list audit logs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"audit_logs": logs, "count": len(logs)})
}

func assignRolePermissions(tx *sql.Tx, roleID int64, permissionNames []string) error {
	if len(permissionNames) == 0 {
		return nil
	}

	placeholders := make([]string, len(permissionNames))
	args := make([]interface{}, 0, len(permissionNames))
	for i, name := range permissionNames {
		placeholders[i] = "?"
		args = append(args, name)
	}

	query := "SELECT id, name FROM permissions WHERE name IN (" + strings.Join(placeholders, ",") + ")"
	rows, err := tx.Query(query, args...)
	if err != nil {
		return fmt.Errorf("failed to resolve permissions")
	}
	defer rows.Close()

	permissionIDs := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("failed to resolve permissions")
		}
		permissionIDs[name] = id
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to resolve permissions")
	}

	for _, name := range permissionNames {
		permissionID, ok := permissionIDs[name]
		if !ok {
			return fmt.Errorf("unknown permission: %s", name)
		}
		if _, err := tx.Exec("INSERT INTO role_permissions (role_id, permission_id) VALUES (?, ?)", roleID, permissionID); err != nil {
			return fmt.Errorf("failed to assign role permission")
		}
	}

	return nil
}

func nullToString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}
