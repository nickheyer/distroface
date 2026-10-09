package services

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.TokenServiceHandler = (*TokenService)(nil)

type TokenService struct {
	authManager *auth.Manager
	enforcer    *rbac.Enforcer
	log         *logger.Logger
}

func NewTokenService(manager *auth.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *TokenService {
	return &TokenService{authManager: manager, enforcer: enforcer, log: log}
}

func (s *TokenService) CreateAPIToken(ctx context.Context, req *v1.CreateAPITokenRequest) (*v1.CreateAPITokenResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	plaintext, token, err := s.authManager.GenerateAPIToken(ctx, user.ID, req.Name, req.ExpiresInDays)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoToken := &v1.APIToken{
		Id:        token.ID,
		Name:      token.Name,
		CreatedBy: token.UserID,
		CreatedAt: timestamppb.New(token.CreatedAt),
	}
	if token.ExpiresAt != nil {
		protoToken.ExpiresAt = timestamppb.New(*token.ExpiresAt)
	}

	return &v1.CreateAPITokenResponse{
		PlaintextToken: plaintext,
		Token:          protoToken,
	}, nil
}

func (s *TokenService) ListAPITokens(ctx context.Context, req *v1.ListAPITokensRequest) (*v1.ListAPITokensResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	limit, offset := pages.Parse(req.Page)

	// Users with manage permission see all tokens
	userID := user.ID
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceTokens, rbac.ActionManage, "*")
	if canManage {
		userID = ""
	}

	q := pages.ParseQuery(req.Page)
	if err := stores.TokensQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	tokens, total, err := s.authManager.GetStore().ListAPITokens(ctx, userID, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoTokens := make([]*v1.APIToken, len(tokens))
	for i, t := range tokens {
		protoTokens[i] = &v1.APIToken{
			Id:        t.ID,
			Name:      t.Name,
			CreatedBy: t.UserID,
			CreatedAt: timestamppb.New(t.CreatedAt),
		}
		if t.ExpiresAt != nil {
			protoTokens[i].ExpiresAt = timestamppb.New(*t.ExpiresAt)
		}
		if t.LastUsedAt != nil {
			protoTokens[i].LastUsedAt = timestamppb.New(*t.LastUsedAt)
		}
	}

	return &v1.ListAPITokensResponse{
		Tokens: protoTokens,
		Page:   pages.Info(offset, limit, total),
	}, nil
}

func (s *TokenService) DeleteAPIToken(ctx context.Context, req *v1.DeleteAPITokenRequest) (*v1.DeleteAPITokenResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	// Users with manage permission can delete any token
	userID := user.ID
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceTokens, rbac.ActionManage, "*")
	if canManage {
		userID = ""
	}

	if err := s.authManager.GetStore().DeleteAPIToken(ctx, req.Id, userID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	}

	return &v1.DeleteAPITokenResponse{}, nil
}
