package services

import (
	"context"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
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

func (s *TokenService) CreateAPIToken(ctx context.Context, req *connect.Request[v1.CreateAPITokenRequest]) (*connect.Response[v1.CreateAPITokenResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	plaintext, token, err := s.authManager.GenerateAPIToken(ctx, user.ID, req.Msg.Name, req.Msg.ExpiresInDays)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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

	return connect.NewResponse(&v1.CreateAPITokenResponse{
		PlaintextToken: plaintext,
		Token:          protoToken,
	}), nil
}

func (s *TokenService) ListAPITokens(ctx context.Context, req *connect.Request[v1.ListAPITokensRequest]) (*connect.Response[v1.ListAPITokensResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	limit, offset := parsePagination(req.Msg.PageSize, req.Msg.PageToken)

	// Users with manage permission see all tokens
	userID := user.ID
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceTokens, rbac.ActionManage, "*")
	if canManage {
		userID = ""
	}

	tokens, total, err := s.authManager.GetStore().ListAPITokens(ctx, userID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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

	return connect.NewResponse(&v1.ListAPITokensResponse{
		Tokens:        protoTokens,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    int32(total),
	}), nil
}

func (s *TokenService) DeleteAPIToken(ctx context.Context, req *connect.Request[v1.DeleteAPITokenRequest]) (*connect.Response[v1.DeleteAPITokenResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	// Users with manage permission can delete any token
	userID := user.ID
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceTokens, rbac.ActionManage, "*")
	if canManage {
		userID = ""
	}

	if err := s.authManager.GetStore().DeleteAPIToken(ctx, req.Msg.Id, userID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&v1.DeleteAPITokenResponse{}), nil
}
