package auth

import (
	"database/sql"
	"fmt"
)

// RBACManager handles role-based access control
type RBACManager struct {
	db *sql.DB
}

// NewRBACManager creates a new RBAC manager
func NewRBACManager(db *sql.DB) *RBACManager {
	return &RBACManager{db: db}
}

// HasPermission checks if a user has a specific permission
func (m *RBACManager) HasPermission(userID int64, permission string) (bool, error) {
	query := `
		SELECT COUNT(*) FROM permissions p
		INNER JOIN role_permissions rp ON p.id = rp.permission_id
		INNER JOIN user_roles ur ON rp.role_id = ur.role_id
		WHERE ur.user_id = ? AND p.name = ?
	`

	var count int
	err := m.db.QueryRow(query, userID, permission).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}

// HasServerPermission checks if a user has a specific permission for a server
func (m *RBACManager) HasServerPermission(userID int64, serverID, permission string) (bool, error) {
	// First check if user has global permission
	hasGlobal, err := m.HasPermission(userID, permission)
	if err != nil {
		return false, err
	}
	if hasGlobal {
		return true, nil
	}

	// Check server-specific permission
	query := `
		SELECT COUNT(*) FROM permissions p
		INNER JOIN server_role_permissions srp ON p.id = srp.permission_id
		INNER JOIN server_roles sr ON srp.server_role_id = sr.id
		INNER JOIN user_server_roles usr ON sr.id = usr.server_role_id
		WHERE usr.user_id = ? AND sr.server_id = ? AND p.name = ?
	`

	var count int
	err = m.db.QueryRow(query, userID, serverID, permission).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check server permission: %w", err)
	}

	return count > 0, nil
}

// GetUserPermissions returns all permissions for a user
func (m *RBACManager) GetUserPermissions(userID int64) ([]string, error) {
	query := `
		SELECT DISTINCT p.name FROM permissions p
		INNER JOIN role_permissions rp ON p.id = rp.permission_id
		INNER JOIN user_roles ur ON rp.role_id = ur.role_id
		WHERE ur.user_id = ?
	`

	rows, err := m.db.Query(query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user permissions: %w", err)
	}
	defer rows.Close()

	var permissions []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}

	return permissions, rows.Err()
}

// GetUserRoles returns all role names for a user
func (m *RBACManager) GetUserRoles(userID int64) ([]string, error) {
	query := `
		SELECT DISTINCT r.name FROM roles r
		INNER JOIN user_roles ur ON r.id = ur.role_id
		WHERE ur.user_id = ?
		ORDER BY r.name
	`

	rows, err := m.db.Query(query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user roles: %w", err)
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}

	return roles, rows.Err()
}

// GetUserServerPermissions returns all server-specific permissions for a user
func (m *RBACManager) GetUserServerPermissions(userID int64, serverID string) ([]string, error) {
	query := `
		SELECT DISTINCT p.name FROM permissions p
		INNER JOIN server_role_permissions srp ON p.id = srp.permission_id
		INNER JOIN server_roles sr ON srp.server_role_id = sr.id
		INNER JOIN user_server_roles usr ON sr.id = usr.server_role_id
		WHERE usr.user_id = ? AND sr.server_id = ?
	`

	rows, err := m.db.Query(query, userID, serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user server permissions: %w", err)
	}
	defer rows.Close()

	var permissions []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}

	return permissions, rows.Err()
}

// AssignRoleToUser assigns a role to a user
func (m *RBACManager) AssignRoleToUser(userID, roleID int64) error {
	query := `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`
	_, err := m.db.Exec(query, userID, roleID)
	if err != nil {
		return fmt.Errorf("failed to assign role: %w", err)
	}
	return nil
}

// RemoveRoleFromUser removes a role from a user
func (m *RBACManager) RemoveRoleFromUser(userID, roleID int64) error {
	query := `DELETE FROM user_roles WHERE user_id = ? AND role_id = ?`
	_, err := m.db.Exec(query, userID, roleID)
	if err != nil {
		return fmt.Errorf("failed to remove role: %w", err)
	}
	return nil
}

// AssignServerRoleToUser assigns a server-specific role to a user
func (m *RBACManager) AssignServerRoleToUser(userID, serverRoleID int64) error {
	query := `INSERT INTO user_server_roles (user_id, server_role_id) VALUES (?, ?)`
	_, err := m.db.Exec(query, userID, serverRoleID)
	if err != nil {
		return fmt.Errorf("failed to assign server role: %w", err)
	}
	return nil
}

// RemoveServerRoleFromUser removes a server-specific role from a user
func (m *RBACManager) RemoveServerRoleFromUser(userID, serverRoleID int64) error {
	query := `DELETE FROM user_server_roles WHERE user_id = ? AND server_role_id = ?`
	_, err := m.db.Exec(query, userID, serverRoleID)
	if err != nil {
		return fmt.Errorf("failed to remove server role: %w", err)
	}
	return nil
}
