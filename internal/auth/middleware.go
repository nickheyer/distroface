package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/models"
)

type Middleware struct {
	auth AuthService
	cfg  *models.Config
	log  *logging.LogService
}

func NewMiddleware(auth AuthService, cfg *models.Config, log *logging.LogService) *Middleware {
	return &Middleware{auth: auth, cfg: cfg, log: log}
}

// WEB UI AUTH
func (m *Middleware) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		// DEFAULT ANON
		username := "anonymous"

		if authHeader != "" {
			// EXTRACT TOKEN
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			// VALIDATE TOKEN
			claims, err := m.auth.ValidateToken(r.Context(), tokenString)
			if err != nil {
				http.Error(w, "INVALID TOKEN", http.StatusUnauthorized)
				return
			}

			// SET AUTHENTICATED USER
			username = claims.Subject
		}

		if !m.auth.HasPermission(r.Context(), username, models.Permission{
			Action:   models.ActionLogin,
			Resource: models.ResourceWebUI,
		}) {
			http.Error(w, "FORBIDDEN", http.StatusForbidden)
			return
		}

		// ADD USERNAME TO CONTEXT
		ctx := context.WithValue(r.Context(), constants.UsernameKey, username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// REGISTRY AUTH MIDDLEWARE
func (m *Middleware) RegistryAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			challenge := fmt.Sprintf(`Bearer realm="%s"`, m.cfg.Auth.Realm)
			w.Header().Set("WWW-Authenticate", challenge)
			w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// EXTRACT TOKEN
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// VALIDATE TOKEN
		claims, err := m.auth.ValidateToken(r.Context(), tokenString)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer error=\"invalid_token\"")
			http.Error(w, "INVALID TOKEN", http.StatusUnauthorized)
			return
		}

		// ADD USERNAME TO CONTEXT
		ctx := context.WithValue(r.Context(), constants.UsernameKey, claims.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
