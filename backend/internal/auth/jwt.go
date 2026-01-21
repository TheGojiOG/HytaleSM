package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents JWT claims
type Claims struct {
	UserID         int64  `json:"user_id"`
	Username       string `json:"username"`
	OrganizationID int64  `json:"organization_id"`
	Roles          []string `json:"roles,omitempty"`
	PermissionsHash string  `json:"permissions_hash,omitempty"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

// JWTManager handles JWT token operations
type JWTManager struct {
	secretKey            []byte
	accessTokenDuration  time.Duration
	refreshTokenDuration time.Duration
}

// NewJWTManager creates a new JWT manager
func NewJWTManager(secretKey string, accessTokenDuration, refreshTokenDuration time.Duration) *JWTManager {
	return &JWTManager{
		secretKey:            []byte(secretKey),
		accessTokenDuration:  accessTokenDuration,
		refreshTokenDuration: refreshTokenDuration,
	}
}

// GenerateTokenPair generates a new access and refresh token pair
func (m *JWTManager) GenerateTokenPair(userID int64, username string, organizationID int64, roles []string, permissionsHash string) (*TokenPair, string, error) {
	// Generate access token
	accessToken, err := m.generateAccessToken(userID, username, organizationID, roles, permissionsHash)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate access token: %w", err)
	}

	// Generate refresh token
	refreshToken, tokenHash, err := m.generateRefreshToken()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, tokenHash, nil
}

// generateAccessToken creates a new access token
func (m *JWTManager) generateAccessToken(userID int64, username string, organizationID int64, roles []string, permissionsHash string) (string, error) {
	claims := &Claims{
		UserID:         userID,
		Username:       username,
		OrganizationID: organizationID,
		Roles:          roles,
		PermissionsHash: permissionsHash,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.accessTokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secretKey)
}

// generateRefreshToken creates a new refresh token
func (m *JWTManager) generateRefreshToken() (string, string, error) {
	// Generate random bytes
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}

	// Encode as base64
	token := base64.URLEncoding.EncodeToString(b)

	// Create hash for storage
	hash := sha256.Sum256([]byte(token))
	tokenHash := base64.URLEncoding.EncodeToString(hash[:])

	return token, tokenHash, nil
}

// ValidateAccessToken validates an access token and returns the claims
func (m *JWTManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secretKey, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}

// HashRefreshToken creates a hash of a refresh token for comparison
func (m *JWTManager) HashRefreshToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return base64.URLEncoding.EncodeToString(hash[:])
}

// ComputePermissionsHash returns a stable hash for a permission set.
func ComputePermissionsHash(permissions []string) string {
	if len(permissions) == 0 {
		return ""
	}

	sorted := append([]string{}, permissions...)
	sort.Strings(sorted)
	joined := strings.Join(sorted, "|")
	hash := sha256.Sum256([]byte(joined))
	return base64.URLEncoding.EncodeToString(hash[:])
}

// GetRefreshTokenExpiry returns the expiry time for refresh tokens
func (m *JWTManager) GetRefreshTokenExpiry() time.Time {
	return time.Now().Add(m.refreshTokenDuration)
}

// GetAccessTokenExpiry returns the expiry time for access tokens
func (m *JWTManager) GetAccessTokenExpiry() time.Time {
	return time.Now().Add(m.accessTokenDuration)
}
