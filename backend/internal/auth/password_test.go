package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("secret", 12)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	if err := VerifyPassword("secret", hash); err != nil {
		t.Fatalf("expected password to verify, got %v", err)
	}

	if err := VerifyPassword("wrong", hash); err == nil {
		t.Fatalf("expected wrong password to fail")
	}
}
