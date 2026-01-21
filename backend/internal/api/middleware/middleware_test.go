package middleware

import (
	"testing"
	"time"
)

func TestIsOriginAllowed(t *testing.T) {
	allowed := []string{"0.0.0.0/0", "https://example.com"}

	if !isOriginAllowed("https://example.com", allowed) {
		t.Fatalf("expected origin to be allowed")
	}

	if !isOriginAllowed("https://anything.local", allowed) {
		t.Fatalf("expected wildcard allowlist to permit origin")
	}

	if !isOriginAllowed("", allowed) {
		t.Fatalf("expected empty origin to be allowed")
	}
}

func TestContainsWildcard(t *testing.T) {
	if !containsWildcard([]string{"0.0.0.0/0"}) {
		t.Fatalf("expected wildcard to be detected")
	}

	if containsWildcard([]string{"https://example.com"}) {
		t.Fatalf("did not expect wildcard to be detected")
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := newRateLimiter(true, 2)
	key := "127.0.0.1"

	if !limiter.allow(key) {
		t.Fatalf("expected first request to be allowed")
	}
	if !limiter.allow(key) {
		t.Fatalf("expected second request to be allowed")
	}
	if limiter.allow(key) {
		t.Fatalf("expected third request to be rate limited")
	}

	limiter.entries[key].windowStart = time.Now().Add(-limiter.window)
	if !limiter.allow(key) {
		t.Fatalf("expected request to be allowed after window reset")
	}
}
