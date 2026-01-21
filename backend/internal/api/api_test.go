package api

import (
	"testing"
)

func TestParseDurationFallback(t *testing.T) {
	result := parseDuration("not-a-duration")
	if result.Minutes() != 15 {
		t.Fatalf("expected 15 minute fallback, got %v", result)
	}
}
