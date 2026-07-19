package utils

import (
	"fmt"
	"net/http"

	"github.com/nickheyer/distroface/internal/settings"
)

// Safe without breaking the embedded spa, script-src left open on purpose
const defaultCSP = "frame-ancestors 'none'; base-uri 'self'; object-src 'none'"

// Response header middleware, toggles read live per request
func Headers(res *settings.Resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sec := res.System(r.Context()).GetSecurity().GetHeaders()
		if !sec.GetEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		csp := sec.GetContentSecurityPolicy()
		if csp == "" {
			csp = defaultCSP
		}

		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", csp)
		// HSTS only means anything on a tls response
		if r.TLS != nil || sec.GetHsts() {
			maxAge := sec.GetHstsMaxAgeSeconds()
			if maxAge <= 0 {
				maxAge = 31536000
			}
			h.Set("Strict-Transport-Security", fmt.Sprintf("max-age=%d; includeSubDomains", maxAge))
		}
		next.ServeHTTP(w, r)
	})
}
