package middleware

import (
	"fmt"
	"net/http"
	"strings"
)

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ONLY APPLY CORS TO WEB UI ROUTES
		if strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Origin") != "" {
			fmt.Printf("\nUSING CORS HEADER\n\n")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
