package rpc

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

// publicProcedures are RPCs that don't require authentication.
var publicProcedures = map[string]bool{
	distrofacev1connect.AuthServiceRegisterProcedure:       true,
	distrofacev1connect.AuthServiceLoginProcedure:          true,
	distrofacev1connect.HealthServiceHealthCheckProcedure:  true,
	distrofacev1connect.UserServiceGetUserProcedure:        true,
	distrofacev1connect.RepositoryServiceGetRepositoryProcedure:    true,
	distrofacev1connect.RepositoryServiceListRepositoriesProcedure: true,
	distrofacev1connect.RepositoryServiceListTagsProcedure:         true,
	distrofacev1connect.RepositoryServiceGetTagDetailProcedure:     true,
}

type sessionInterceptor struct {
	store *storage.Store
	log   *logger.Logger
}

func newSessionInterceptor(store *storage.Store, log *logger.Logger) *sessionInterceptor {
	return &sessionInterceptor{store: store, log: log}
}

func (i *sessionInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx = i.authenticate(ctx, req.Header())

		if !publicProcedures[req.Spec().Procedure] {
			if auth.UserFromContext(ctx) == nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, nil)
			}
		}

		return next(ctx, req)
	}
}

func (i *sessionInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *sessionInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx = i.authenticate(ctx, conn.RequestHeader())
		return next(ctx, conn)
	}
}

func (i *sessionInterceptor) authenticate(ctx context.Context, headers interface{ Get(string) string }) context.Context {
	token := extractToken(headers)
	if token == "" {
		return ctx
	}

	tokenHash := storage.HashToken(token)
	session, err := i.store.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		i.log.Error("session auth: failed to look up session: %v", err)
		return ctx
	}
	if session == nil {
		return ctx
	}

	if session.ExpiresAt.Before(time.Now().UTC()) {
		return ctx
	}

	ctx = auth.WithUser(ctx, &session.User)
	ctx = auth.WithSession(ctx, session)
	return ctx
}

func extractToken(headers interface{ Get(string) string }) string {
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
