package auth

import (
	"testing"
	"time"
)

func TestJWTManagerGenerateAndValidate(t *testing.T) {
	manager := NewJWTManager("test-secret", 10*time.Minute, 24*time.Hour)

	roles := []string{"Viewer", "Operator"}
	permissionsHash := ComputePermissionsHash([]string{"servers.list", "servers.get"})
	pair, tokenHash, err := manager.GenerateTokenPair(42, "tester", 7, roles, permissionsHash)
	if err != nil {
		t.Fatalf("failed to generate token pair: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("expected tokens to be generated")
	}
	if tokenHash == "" {
		t.Fatalf("expected refresh token hash to be generated")
	}

	claims, err := manager.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("failed to validate access token: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "tester" || claims.OrganizationID != 7 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if len(claims.Roles) != 2 || claims.PermissionsHash != permissionsHash {
		t.Fatalf("unexpected RBAC claims: %+v", claims)
	}

	if manager.HashRefreshToken(pair.RefreshToken) != tokenHash {
		t.Fatalf("refresh token hash mismatch")
	}
}
