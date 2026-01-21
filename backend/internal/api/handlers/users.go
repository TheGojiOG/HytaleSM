package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/TheGojiOG/HytaleSM/internal/auth"
	"github.com/TheGojiOG/HytaleSM/internal/models"
)

// UserHandler handles user management requests
type UserHandler struct {
	db          *sql.DB
	rbacManager *auth.RBACManager
	bcryptCost  int
}

// NewUserHandler creates a new user handler
func NewUserHandler(db *sql.DB, rbacManager *auth.RBACManager, bcryptCost int) *UserHandler {
	return &UserHandler{
		db:          db,
		rbacManager: rbacManager,
		bcryptCost:  bcryptCost,
	}
}

// ListUsers returns all users with roles
func (h *UserHandler) ListUsers(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, organization_id, username, email, is_active, created_at, updated_at
		FROM users
		ORDER BY username
	`)
	if err != nil {
		log.Printf("[Users] list users query failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list users"})
		return
	}
	defer rows.Close()

	type roleSummary struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}

	type userWithRoles struct {
		models.User
		Roles []roleSummary `json:"roles"`
	}

	var users []userWithRoles
	userIDs := make([]int64, 0)
	for rows.Next() {
		var user models.User
		if err := rows.Scan(
			&user.ID,
			&user.OrganizationID,
			&user.Username,
			&user.Email,
			&user.IsActive,
			&user.CreatedAt,
			&user.UpdatedAt,
		); err != nil {
			log.Printf("[Users] scan user failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan user"})
			return
		}
		users = append(users, userWithRoles{User: user})
		userIDs = append(userIDs, user.ID)
	}

	if err := rows.Err(); err != nil {
		log.Printf("[Users] list users rows error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list users"})
		return
	}

	if len(userIDs) > 0 {
		placeholders := make([]string, len(userIDs))
		args := make([]interface{}, 0, len(userIDs))
		for i, id := range userIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}

		query := "SELECT ur.user_id, r.id, r.name FROM user_roles ur JOIN roles r ON ur.role_id = r.id WHERE ur.user_id IN (" + strings.Join(placeholders, ",") + ") ORDER BY r.name"
		roleRows, err := h.db.Query(query, args...)
		if err != nil {
			log.Printf("[Users] load user roles query failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user roles"})
			return
		}
		defer roleRows.Close()

		roleMap := map[int64][]roleSummary{}
		for roleRows.Next() {
			var userID int64
			var roleID int64
			var roleName string
			if err := roleRows.Scan(&userID, &roleID, &roleName); err != nil {
				log.Printf("[Users] scan user role failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan user roles"})
				return
			}
			roleMap[userID] = append(roleMap[userID], roleSummary{ID: roleID, Name: roleName})
		}

		if err := roleRows.Err(); err != nil {
			log.Printf("[Users] load user roles rows error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user roles"})
			return
		}

		for i := range users {
			users[i].Roles = roleMap[users[i].ID]
		}
	}

	c.JSON(http.StatusOK, users)
}

// GetUser returns a specific user with roles
func (h *UserHandler) GetUser(c *gin.Context) {
	id := c.Param("id")

	var user models.User
	err := h.db.QueryRow(`
		SELECT id, organization_id, username, email, is_active, created_at, updated_at
		FROM users WHERE id = ?
	`, id).Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Username,
		&user.Email,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if err != nil {
		log.Printf("[Users] get user failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	rows, err := h.db.Query(`
		SELECT r.id, r.name
		FROM user_roles ur
		JOIN roles r ON ur.role_id = r.id
		WHERE ur.user_id = ?
		ORDER BY r.name
	`, id)
	if err != nil {
		log.Printf("[Users] load user roles failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user roles"})
		return
	}
	defer rows.Close()

	roles := []gin.H{}
	for rows.Next() {
		var roleID int64
		var roleName string
		if err := rows.Scan(&roleID, &roleName); err != nil {
			log.Printf("[Users] scan user roles failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan user roles"})
			return
		}
		roles = append(roles, gin.H{"id": roleID, "name": roleName})
	}

	if err := rows.Err(); err != nil {
		log.Printf("[Users] user roles rows error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user roles"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              user.ID,
		"organization_id": user.OrganizationID,
		"username":        user.Username,
		"email":           user.Email,
		"is_active":       user.IsActive,
		"created_at":      user.CreatedAt,
		"updated_at":      user.UpdatedAt,
		"roles":           roles,
	})
}

// CreateUser creates a new user
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req models.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Hash password
	passwordHash, err := auth.HashPassword(req.Password, h.bcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Insert user
	result, err := h.db.Exec(`
		INSERT INTO users (username, email, password_hash)
		VALUES (?, ?, ?)
	`, req.Username, req.Email, passwordHash)

	if err != nil {
		// Check for unique constraint violation
		c.JSON(http.StatusConflict, gin.H{"error": "Username or email already exists"})
		return
	}

	userID, _ := result.LastInsertId()
	_, _ = h.db.Exec(`
		INSERT INTO user_roles (user_id, role_id)
		SELECT ?, id FROM roles WHERE name = 'Viewer'
	`, userID)

	c.JSON(http.StatusCreated, gin.H{
		"id":      userID,
		"message": "User created successfully",
	})
}

// UpdateUser updates an existing user
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id := c.Param("id")

	var req models.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Build dynamic update query
	query := "UPDATE users SET updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{}

	if req.Email != nil {
		query += ", email = ?"
		args = append(args, *req.Email)
	}

	if req.Password != nil {
		passwordHash, err := auth.HashPassword(*req.Password, h.bcryptCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		query += ", password_hash = ?"
		args = append(args, passwordHash)
	}

	if req.IsActive != nil {
		query += ", is_active = ?"
		args = append(args, *req.IsActive)
	}

	query += " WHERE id = ?"
	args = append(args, id)

	result, err := h.db.Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "User updated successfully"})
}

// DeleteUser deletes a user
func (h *UserHandler) DeleteUser(c *gin.Context) {
	id := c.Param("id")

	result, err := h.db.Exec("DELETE FROM users WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "User deleted successfully"})
}

// AssignRoles assigns roles to a user
func (h *UserHandler) AssignRoles(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		RoleIDs []int64 `json:"role_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Begin transaction
	tx, err := h.db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback()

	// Remove existing roles
	if _, err := tx.Exec("DELETE FROM user_roles WHERE user_id = ?", id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove existing roles"})
		return
	}

	// Assign new roles
	for _, roleID := range req.RoleIDs {
		if _, err := tx.Exec("INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)", id, roleID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign role"})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Roles assigned successfully"})
}
