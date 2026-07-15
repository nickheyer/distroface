package rpc

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/ratelimit"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

// Credential rpcs that get brute force lockout
var throttledProcedures = map[string]bool{
	distrofacev1connect.AuthServiceLoginProcedure:          true,
	distrofacev1connect.AuthServiceRegisterProcedure:       true,
	distrofacev1connect.UserServiceChangePasswordProcedure: true,
}

// Failed auth counts toward lockout success clears it
func (s *Server) rateLimitInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if s.authLimiter == nil || !throttledProcedures[req.Spec().Procedure] {
				return next(ctx, req)
			}

			clientIP := ratelimit.ClientIP(req.Peer().Addr, req.Header())
			if s.authLimiter.Blocked(clientIP) {
				return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many failed attempts, try again later"))
			}

			resp, err := next(ctx, req)
			if err != nil {
				switch connect.CodeOf(err) {
				case connect.CodeUnauthenticated, connect.CodePermissionDenied:
					s.authLimiter.Record(clientIP)
				}
			} else {
				s.authLimiter.Reset(clientIP)
			}
			return resp, err
		}
	}
}

// Creates a Connect interceptor for authentication and authorization.
func (s *Server) authInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure
			isPublic := rbac.PublicProcedures[procedure]

			// If no auth providers are enabled, bypass auth entirely
			if !s.authManager.IsAnyAuthEnabled() {
				ctx = auth.WithUser(ctx, &auth.AuthenticatedUser{
					ID:       "admin",
					Username: "admin",
					Roles:    []string{"admin"},
					Provider: "none",
				})
				return next(ctx, req)
			}

			// Resolve user from token or anonymous access — always, for every request
			token := auth.ExtractToken(req.Header())
			var user *auth.AuthenticatedUser

			if token != "" {
				var err error
				if strings.HasPrefix(token, "df_") {
					user, err = s.authManager.ValidateAPIToken(ctx, token)
				} else {
					user, err = s.authManager.ValidateSession(ctx, token)
				}
				if err != nil {
					if !isPublic {
						return nil, connect.NewError(connect.CodeUnauthenticated, err)
					}
					// Public route with bad token — proceed without user
				}
			} else if s.authManager.IsAnonymousAccessEnabled() {
				user = s.authManager.AnonymousUser()
			} else if !isPublic {
				return nil, connect.NewError(connect.CodeUnauthenticated, auth.ErrInvalidToken)
			}

			if user != nil {
				ctx = auth.WithUser(ctx, user)
			}

			// Public procedures — no further checks
			if isPublic {
				return next(ctx, req)
			}

			// Authenticated-only procedures — no specific resource permission needed
			if rbac.AuthenticatedOnlyProcedures[procedure] {
				return next(ctx, req)
			}

			// RBAC permission check
			if perm, ok := rbac.ProcedurePermissions[procedure]; ok {
				if s.enforcer != nil {
					objectID := "*"
					if perm.ObjectIDField != "" {
						objectID = rbac.ExtractObjectID(req, perm.ObjectIDField)
					}
					allowed, err := s.enforcer.Enforce(user.Roles, perm.Resource, perm.Action, objectID)
					if err != nil {
						s.log.Error("RBAC enforcement error: %v", err)
						return nil, connect.NewError(connect.CodeInternal, err)
					}
					if !allowed {
						return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("insufficient permissions for %s/%s", perm.Resource, perm.Action))
					}
				}
			}

			return next(ctx, req)
		}
	}
}

type loggingInterceptor struct {
	log *logger.Logger
}

func (i *loggingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		i.log.Info("RPC %s %s", req.Peer().Addr, req.Spec().Procedure)
		return next(ctx, req)
	}
}

func (i *loggingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *loggingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		i.log.Info("RPC Stream open %s %s", conn.Peer().Addr, conn.Spec().Procedure)
		err := next(ctx, conn)
		i.log.Info("RPC Stream closed %s %s", conn.Peer().Addr, conn.Spec().Procedure)
		return err
	}
}
