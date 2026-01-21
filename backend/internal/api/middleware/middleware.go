package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/TheGojiOG/HytaleSM/internal/config"
	"github.com/TheGojiOG/HytaleSM/internal/logging"
)

// CORS middleware adds CORS headers
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		allowed := isOriginAllowed(origin, cfg.AllowedOrigins)

		if allowed {
			if origin != "" {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			} else if containsWildcard(cfg.AllowedOrigins) {
				c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			}
		}

		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, Authorization, Accept, Origin, Cache-Control, X-Requested-With")

		// Set allowed methods
		methods := "GET, POST, PUT, DELETE, OPTIONS"
		if len(cfg.AllowedMethods) > 0 {
			methods = ""
			for i, method := range cfg.AllowedMethods {
				if i > 0 {
					methods += ", "
				}
				methods += method
			}
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", methods)

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// Logger is a custom logging middleware
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Build log entry
		if raw != "" {
			path = path + "?" + raw
		}

		c.Writer.Header().Set("X-Response-Time", latency.String())

		if path != "/health" || gin.Mode() == gin.DebugMode {
			logging.L().Info("http_request",
				"method", c.Request.Method,
				"path", path,
				"status", c.Writer.Status(),
				"latency", latency.String(),
				"ip", c.ClientIP(),
			)
		}
	}
}

// RateLimit middleware (simple in-memory implementation)
func RateLimit(enabled bool, requestsPerMinute int) gin.HandlerFunc {
	limiter := newRateLimiter(enabled, requestsPerMinute)

	return func(c *gin.Context) {
		if !limiter.enabled {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if path == "/api/v1/auth/setup-status" || path == "/api/v1/auth/refresh" || (c.Request.Method == http.MethodGet && path == "/api/v1/auth/me") {
			c.Next()
			return
		}

		if !limiter.allow(c.ClientIP()) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}

func isOriginAllowed(origin string, allowedOrigins []string) bool {
	if origin == "" {
		return true
	}

	for _, allowedOrigin := range allowedOrigins {
		normalized := strings.TrimSpace(allowedOrigin)
		if normalized == "" {
			continue
		}
		if normalized == "*" || normalized == "0.0.0.0/0" || normalized == origin {
			return true
		}
	}

	return false
}

func containsWildcard(allowedOrigins []string) bool {
	for _, allowedOrigin := range allowedOrigins {
		normalized := strings.TrimSpace(allowedOrigin)
		if normalized == "*" || normalized == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

type rateLimiter struct {
	enabled           bool
	requestsPerMinute int
	window            time.Duration
	mu                sync.Mutex
	entries           map[string]*rateLimitEntry
	lastCleanup       time.Time
}

type rateLimitEntry struct {
	windowStart time.Time
	count       int
}

func newRateLimiter(enabled bool, requestsPerMinute int) *rateLimiter {
	return &rateLimiter{
		enabled:           enabled && requestsPerMinute > 0,
		requestsPerMinute: requestsPerMinute,
		window:            time.Minute,
		entries:           make(map[string]*rateLimitEntry),
		lastCleanup:       time.Now(),
	}
}

func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if now.Sub(rl.lastCleanup) > time.Minute {
		rl.cleanup(now)
	}

	entry, exists := rl.entries[key]
	if !exists || now.Sub(entry.windowStart) >= rl.window {
		rl.entries[key] = &rateLimitEntry{windowStart: now, count: 1}
		return true
	}

	if entry.count >= rl.requestsPerMinute {
		return false
	}

	entry.count++
	return true
}

func (rl *rateLimiter) cleanup(now time.Time) {
	for key, entry := range rl.entries {
		if now.Sub(entry.windowStart) >= rl.window {
			delete(rl.entries, key)
		}
	}
	rl.lastCleanup = now
}
