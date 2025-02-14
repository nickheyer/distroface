package middleware

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"
)

type PortType string

var portCtx PortType = "original-port"

func ProxyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// REAL CLIENT IP
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ips := strings.Split(xff, ",")
			// FIRST IP IN CHAIN
			r.RemoteAddr = strings.TrimSpace(ips[0])
		}

		// REAL PROTO
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			r.URL.Scheme = proto
		} else {
			r.URL.Scheme = "http"
		}

		// REAL HOST
		if host := r.Header.Get("X-Forwarded-Host"); host != "" {
			r.Host = host
			r.URL.Host = host
		}

		// REAL PORT
		if port := r.Header.Get("X-Forwarded-Port"); port != "" {
			r.Header.Set("X-Real-Port", port)
			ctx := context.WithValue(r.Context(), portCtx, port)
			r = r.WithContext(ctx)
		}

		// Handle SSL/TLS
		if r.Header.Get("X-Forwarded-Proto") == "https" {
			r.TLS = &tls.ConnectionState{}
		}

		next.ServeHTTP(w, r)
	})
}
