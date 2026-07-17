package utils

import (
	"fmt"
	"net/http"

	"github.com/nickheyer/distroface/pkg/config"
)

// Safe without breaking the embedded spa, script-src left open on purpose
const defaultCSP = "frame-ancestors 'none'; base-uri 'self'; object-src 'none'"

// Response header middleware, disabled config returns the handler untouched
func Headers(cfg config.SecurityHeadersConfig, tlsEnabled bool, next http.Handler) http.Handler {
	if !cfg.Enabled {
		return next
	}

	csp := cfg.ContentSecurityPolicy
	if csp == "" {
		csp = defaultCSP
	}
	hstsValue := ""
	if cfg.HSTS || tlsEnabled {
		maxAge := cfg.HSTSMaxAge
		if maxAge <= 0 {
			maxAge = 31536000
		}
		hstsValue = fmt.Sprintf("max-age=%d; includeSubDomains", maxAge)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", csp)
		// HSTS only means anything on a tls response
		if hstsValue != "" && (r.TLS != nil || cfg.HSTS) {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		next.ServeHTTP(w, r)
	})
}
