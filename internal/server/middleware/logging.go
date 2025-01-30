package middleware

import (
	"log"
	"net/http"
	"strings"
)

// SENSITIVE HEADERS THAT SHOULD BE MASKED FOR SECURITY
var sensitiveHeaders = map[string]bool{
	"Authorization": true,
	"Cookie":        true,
	"Token":         true,
	"Api-Key":       true,
}

// RETURNS CLEAN HEADERS MAP WITHOUT SENSITIVE DATA
func filterHeaders(headers http.Header) map[string]string {
	filtered := make(map[string]string)
	for key, values := range headers {
		if sensitiveHeaders[key] {
			filtered[key] = "[REDACTED]"
			continue
		}
		filtered[key] = strings.Join(values, ", ")
	}
	return filtered
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SECURITY THROUGH OBSCURITY I GUESS
		log.Printf("\n[%s] %s %s | IP=%s\n",
			r.Method,
			r.URL.Path,
			filterHeaders(r.Header),
			r.RemoteAddr,
		)
		next.ServeHTTP(w, r)
	})
}
