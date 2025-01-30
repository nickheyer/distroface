package middleware

import (
	"fmt"
	"net/http"
	"strings"
)

func ProxyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// GET REAL IP
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ips := strings.Split(xff, ",")
			r.RemoteAddr = strings.TrimSpace(ips[0])
		}

		// GET REAL PROTO
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			r.URL.Scheme = proto
		} else {
			r.URL.Scheme = "http"
		}

		// GET REAL HOST
		if host := r.Header.Get("X-Forwarded-Host"); host != "" {
			r.Host = host
			r.URL.Host = host
		}

		// GET REAL PORT
		if port := r.Header.Get("X-Forwarded-Port"); port != "" {
			r.Header.Set("X-Real-Port", port)
		}

		fmt.Printf("\nPROXYING REQUEST HEADERS: %v\n\n", r.Header)
		next.ServeHTTP(w, r)
	})
}
