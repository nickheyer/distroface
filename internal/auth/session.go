package auth

import (
	"context"
	"strings"
)

type contextKey string

const userContextKey contextKey = "authenticated_user"

// AuthenticatedUser represents a validated user in context.
type AuthenticatedUser struct {
	ID                 string
	Username           string
	Email              string
	Roles              []string
	Provider           string // "local", "oidc", "anonymous"
	MustChangePassword bool   // rpc access pending pw rotation
}

// WithUser attaches an authenticated user to the context.
func WithUser(ctx context.Context, user *AuthenticatedUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext retrieves the authenticated user from the context.
func UserFromContext(ctx context.Context) *AuthenticatedUser {
	user, _ := ctx.Value(userContextKey).(*AuthenticatedUser)
	return user
}

// ExtractToken extracts a bearer token from Authorization header or session cookie.
func ExtractToken(headers interface{ Get(string) string }) string {
	authHeader := headers.Get("Authorization")
	if authHeader != "" {
		prefix, token, ok := strings.Cut(authHeader, " ")
		if ok && strings.EqualFold(prefix, "Bearer") {
			return strings.TrimSpace(token)
		}
	}

	cookie := headers.Get("Cookie")
	if cookie != "" {
		for _, part := range strings.Split(cookie, ";") {
			part = strings.TrimSpace(part)
			if name, value, ok := strings.Cut(part, "="); ok && name == "distroface_session" {
				return value
			}
		}
	}

	return ""
}
