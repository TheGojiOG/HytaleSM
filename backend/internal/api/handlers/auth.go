package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yourusername/hytale-server-manager/internal/auth"
	"github.com/yourusername/hytale-server-manager/internal/models"
)

const (
	accessTokenCookieName  = "hsm_access"
	refreshTokenCookieName = "hsm_refresh"
)

func isSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := c.GetHeader("X-Forwarded-Proto")
	return strings.EqualFold(proto, "https")
}

func setAuthCookies(c *gin.Context, jwtManager *auth.JWTManager, tokens *auth.TokenPair) {
	secure := isSecureRequest(c)
	accessMaxAge := int(jwtManager.GetAccessTokenExpiry().Sub(time.Now()).Seconds())
	if accessMaxAge < 0 {
		accessMaxAge = 0
	}
	refreshMaxAge := int(jwtManager.GetRefreshTokenExpiry().Sub(time.Now()).Seconds())
	if refreshMaxAge < 0 {
		refreshMaxAge = 0
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessTokenCookieName, tokens.AccessToken, accessMaxAge, "/api/v1", "", secure, true)
	c.SetCookie(refreshTokenCookieName, tokens.RefreshToken, refreshMaxAge, "/api/v1", "", secure, true)
}

func clearAuthCookies(c *gin.Context) {
	secure := isSecureRequest(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessTokenCookieName, "", -1, "/api/v1", "", secure, true)
	c.SetCookie(refreshTokenCookieName, "", -1, "/api/v1", "", secure, true)
}

// AuthHandler handles authentication requests
type AuthHandler struct {
	db          *sql.DB
	jwtManager  *auth.JWTManager
	rbacManager *auth.RBACManager
	bcryptCost  int
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(db *sql.DB, jwtManager *auth.JWTManager, rbacManager *auth.RBACManager, bcryptCost int) *AuthHandler {
	return &AuthHandler{
		db:         db,
		jwtManager: jwtManager,
		rbacManager: rbacManager,
		bcryptCost: bcryptCost,
	}
}

// Register handles user registration
func (h *AuthHandler) Register(c *gin.Context) {
	needsSetup, err := h.needsSetup()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if needsSetup {
		c.JSON(http.StatusForbidden, gin.H{"error": "Initial setup required"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required,min=3,max=50"`
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
		FullName string `json:"full_name" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if username already exists
	var exists bool
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)", req.Username).Scan(&exists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
		return
	}

	// Check if email already exists
	err = h.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE email = ?)", req.Email).Scan(&exists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already exists"})
		return
	}

	// Create user
	user, err := models.NewUser(req.Username, req.Email, req.Password, req.FullName, h.bcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	result, err := h.db.Exec(`
		INSERT INTO users (username, email, password_hash, full_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, user.Username, user.Email, user.PasswordHash, user.FullName, user.CreatedAt, user.UpdatedAt)
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save user"})
		return
	}

	userID, err := result.LastInsertId()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user ID"})
		return
	}
	user.ID = userID

	// Assign default "Viewer" role (least privilege)
	_, err = h.db.Exec(`
		INSERT INTO user_roles (user_id, role_id)
		SELECT ?, id FROM roles WHERE name = 'Viewer'
	`, userID)
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign default role"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User registered successfully",
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
			"fullName": user.FullName,
		},
	})
}

// Login handles user login
func (h *AuthHandler) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Get user from database
	var user models.User
	query := `SELECT id, organization_id, username, email, password_hash, is_active FROM users WHERE username = ?`
	err := h.db.QueryRow(query, req.Username).Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.IsActive,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Check if user is active
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "Account is disabled"})
		return
	}

	// Verify password
	if err := auth.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	roles, err := h.rbacManager.GetUserRoles(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user roles"})
		return
	}

	permissions, err := h.rbacManager.GetUserPermissions(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user permissions"})
		return
	}
	permissionsHash := auth.ComputePermissionsHash(permissions)

	// Generate tokens
	tokens, tokenHash, err := h.jwtManager.GenerateTokenPair(user.ID, user.Username, user.OrganizationID, roles, permissionsHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate tokens"})
		return
	}

	// Store refresh token in database
	expiresAt := h.jwtManager.GetRefreshTokenExpiry()
	_, err = h.db.Exec(
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES (?, ?, ?)`,
		user.ID, tokenHash, expiresAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store refresh token"})
		return
	}

	setAuthCookies(c, h.jwtManager, tokens)

	// Return response
	c.JSON(http.StatusOK, models.LoginResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User:         &user,
	})
}

// SetupStatus reports whether the system requires initial setup
func (h *AuthHandler) SetupStatus(c *gin.Context) {
	needsSetup, err := h.needsSetup()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"requires_setup": needsSetup})
}

// SetupInitialAdmin creates the first admin user when no users exist
func (h *AuthHandler) SetupInitialAdmin(c *gin.Context) {
	needsSetup, err := h.needsSetup()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if !needsSetup {
		c.JSON(http.StatusConflict, gin.H{"error": "Setup already completed"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required,min=3,max=50"`
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
		FullName string `json:"full_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := models.NewUser(req.Username, req.Email, req.Password, req.FullName, h.bcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO users (username, email, password_hash, full_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, user.Username, user.Email, user.PasswordHash, user.FullName, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save user"})
		return
	}

	userID, err := result.LastInsertId()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user ID"})
		return
	}

	_, err = tx.Exec(`
		INSERT OR IGNORE INTO user_roles (user_id, role_id)
		SELECT ?, id FROM roles WHERE name IN ('Admin', 'ReleaseManager')
	`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign roles"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to finalize setup"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Admin user created",
		"user": gin.H{
			"id":       userID,
			"username": user.Username,
			"email":    user.Email,
			"fullName": user.FullName,
		},
	})
}

func (h *AuthHandler) needsSetup() (bool, error) {
	var count int
	if err := h.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

// RefreshToken handles token refresh
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req models.RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if cookieToken, cookieErr := c.Cookie(refreshTokenCookieName); cookieErr == nil {
			req.RefreshToken = cookieToken
		}
	}
	if req.RefreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Hash the provided token
	tokenHash := h.jwtManager.HashRefreshToken(req.RefreshToken)

	// Verify token exists and is not expired or revoked
	var userID int64
	var username string
	var organizationID int64
	var expiresAt time.Time
	var revoked bool

	query := `
		SELECT u.id, u.username, u.organization_id, rt.expires_at, rt.revoked
		FROM refresh_tokens rt
		INNER JOIN users u ON rt.user_id = u.id
		WHERE rt.token_hash = ?
	`
	err := h.db.QueryRow(query, tokenHash).Scan(&userID, &username, &organizationID, &expiresAt, &revoked)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Check if token is revoked or expired
	if revoked {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token has been revoked"})
		return
	}
	if time.Now().After(expiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token has expired"})
		return
	}

	// Revoke old refresh token (rotation)
	_, err = h.db.Exec(`UPDATE refresh_tokens SET revoked = 1 WHERE token_hash = ?`, tokenHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke old token"})
		return
	}

	roles, err := h.rbacManager.GetUserRoles(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user roles"})
		return
	}

	permissions, err := h.rbacManager.GetUserPermissions(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load user permissions"})
		return
	}
	permissionsHash := auth.ComputePermissionsHash(permissions)

	// Generate new tokens
	tokens, newTokenHash, err := h.jwtManager.GenerateTokenPair(userID, username, organizationID, roles, permissionsHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate tokens"})
		return
	}

	// Store new refresh token
	newExpiresAt := h.jwtManager.GetRefreshTokenExpiry()
	_, err = h.db.Exec(
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES (?, ?, ?)`,
		userID, newTokenHash, newExpiresAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store refresh token"})
		return
	}

	setAuthCookies(c, h.jwtManager, tokens)

	c.JSON(http.StatusOK, gin.H{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
	})
}

// Logout handles user logout
func (h *AuthHandler) Logout(c *gin.Context) {
	var req models.RefreshTokenRequest
	_ = c.ShouldBindJSON(&req)
	if req.RefreshToken == "" {
		if cookieToken, cookieErr := c.Cookie(refreshTokenCookieName); cookieErr == nil {
			req.RefreshToken = cookieToken
		}
	}

	if req.RefreshToken != "" {
		// Hash and revoke the refresh token
		tokenHash := h.jwtManager.HashRefreshToken(req.RefreshToken)
		_, _ = h.db.Exec(`UPDATE refresh_tokens SET revoked = 1 WHERE token_hash = ?`, tokenHash)
	}

	clearAuthCookies(c)

	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

// GetCurrentUser returns the current authenticated user
func (h *AuthHandler) GetCurrentUser(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// Get user from database
	var user models.User
	query := `SELECT id, organization_id, username, email, is_active, created_at, updated_at FROM users WHERE id = ?`
	err := h.db.QueryRow(query, userID).Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Username,
		&user.Email,
		&user.IsActive,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get user"})
		return
	}

	c.JSON(http.StatusOK, user)
}
