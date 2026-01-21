package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeaders adds various security headers to the response
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Protect against XSS
		c.Header("X-XSS-Protection", "1; mode=block")
		
		// Protect against content sniffing
		c.Header("X-Content-Type-Options", "nosniff")
		
		// Protect against clickjacking
		c.Header("X-Frame-Options", "DENY")
		
		// Enforce HTTPS
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Referrer policy
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		c.Next()
	}
}

// ContentSecurityPolicy adds CSP headers
func ContentSecurityPolicy(isDev bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Define CSP directives
		// Since this is an API, we can be quite strict.
		// However, if the API serves the frontend (e.g. static files), we need to be careful.
		// Assuming this is strictly API serving JSON:
		
		// Default to none
		defaultSrc := "'none'"
		
		// Allow scripts from self (if serving frontend app from same domain)
		scriptSrc := "'self'"
		
		// Allow styles from self and potentially 'unsafe-inline' if styled-components/tailwind needs it in dev
		styleSrc := "'self' 'unsafe-inline'"
		
		// Allow images from self and data: (for avatars etc)
		imgSrc := "'self' data:"
		
		// API connections
		connectSrc := "'self'"
		
		if isDev {
			// In development (Vite), we might need more permissions
			// e.g. connecting to websocket for HMR
			connectSrc += " ws: wss:"
			// 'unsafe-eval' might be needed for some dev tools
			scriptSrc += " 'unsafe-eval'" 
		}

		policy := "default-src " + defaultSrc + "; " +
			"script-src " + scriptSrc + "; " +
			"style-src " + styleSrc + "; " +
			"img-src " + imgSrc + "; " +
			"connect-src " + connectSrc + "; " +
			"font-src 'self'; " +
			"object-src 'none'; " +
			"frame-ancestors 'none';"

		c.Header("Content-Security-Policy", policy)
		c.Next()
	}
}
