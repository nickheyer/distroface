package middleware

import (
	"net/http"
	"strings"

	"github.com/nickheyer/distroface/internal/logging"
	"go.uber.org/zap"
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
		} else {
			filtered[key] = strings.Join(values, ", ")
		}
	}
	return filtered
}

func LoggingMiddleware(log *logging.LogService) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Debug("Incoming HTTP request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Any("headers", filterHeaders(r.Header)),
				zap.String("remote_addr", r.RemoteAddr),
			)
			next.ServeHTTP(w, r)
		})
	}
}
