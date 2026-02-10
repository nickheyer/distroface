package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	storage "github.com/nickheyer/distroface/internal/db"
)

type contextKey string

const userContextKey contextKey = "user"
const sessionContextKey contextKey = "session"

// GenerateSessionToken creates a cryptographically random session token.
func GenerateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WithUser attaches a user to the context.
func WithUser(ctx context.Context, user *storage.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext retrieves the authenticated user from the context.
func UserFromContext(ctx context.Context) *storage.User {
	user, _ := ctx.Value(userContextKey).(*storage.User)
	return user
}

// WithSession attaches a session to the context.
func WithSession(ctx context.Context, session *storage.Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, session)
}

// SessionFromContext retrieves the session from the context.
func SessionFromContext(ctx context.Context) *storage.Session {
	session, _ := ctx.Value(sessionContextKey).(*storage.Session)
	return session
}
