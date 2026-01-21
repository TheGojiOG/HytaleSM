package models

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestNewUserCreatesHash(t *testing.T) {
	user, err := NewUser("test", "test@example.com", "secret", "Tester", bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	if user.PasswordHash == "" {
		t.Fatalf("expected password hash to be set")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("secret")); err != nil {
		t.Fatalf("password hash did not match: %v", err)
	}
}
