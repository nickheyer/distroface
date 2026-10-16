package services

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
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
	gitAccess   *gitrepo.Access
	log         *logger.Logger
}

func NewTokenService(manager *auth.Manager, enforcer *rbac.Enforcer, git *gitrepo.Manager, log *logger.Logger) *TokenService {
	s := &TokenService{authManager: manager, enforcer: enforcer, log: log}
	if git != nil {
		s.gitAccess = git.Access()
	}
	return s
}

func (s *TokenService) CreateAPIToken(ctx context.Context, req *v1.CreateAPITokenRequest) (*v1.CreateAPITokenResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	scope, err := s.validateScope(ctx, user, req.Scope)
	if err != nil {
		return nil, err
	}
	plaintext, token, err := s.authManager.GenerateScopedAPIToken(ctx, user.ID, req.Name, req.ExpiresInDays, scope)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.CreateAPITokenResponse{
		PlaintextToken: plaintext,
		Token:          tokenToProto(token),
	}, nil
}

// Checks a requested credential scope names an object the caller may reach
func (s *TokenService) validateScope(ctx context.Context, user *auth.AuthenticatedUser, scope *v1.CredentialScope) (*auth.CredentialScope, error) {
	if scope == nil || scope.Type == v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_UNSPECIFIED {
		return nil, nil
	}
	if user.IsScoped() {
		return nil, connect.Errorf(connect.CodePermissionDenied, "scoped credentials cannot mint tokens")
	}
	store := s.authManager.GetStore()
	switch scope.Type {
	case v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO:
		ns, name, ok := strings.Cut(scope.Id, "/")
		if !ok || ns == "" || name == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "git repository scope must be namespace/name")
		}
		repo, err := store.GetGitRepository(ctx, ns, name)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if repo == nil || (s.gitAccess != nil && !s.gitAccess.CanSee(ctx, user, repo)) {
			return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		if s.gitAccess != nil && !scope.ReadOnly && s.gitAccess.Level(ctx, user, repo, storage.GitUnitCode) < gitrepo.LevelWrite {
			return nil, connect.Errorf(connect.CodePermissionDenied, "write access to the repository is required for a push token")
		}
		return &auth.CredentialScope{Type: scope.Type, ID: ns + "/" + name, ReadOnly: scope.ReadOnly}, nil
	case v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_ORG:
		org, err := store.GetOrganization(ctx, scope.Id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if org == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "organization not found")
		}
		if member, _ := store.GetOrgMember(ctx, org.ID, user.ID); member == nil && !s.enforcer.HasPermission(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage) {
			return nil, connect.Errorf(connect.CodePermissionDenied, "you are not a member of that organization")
		}
		return &auth.CredentialScope{Type: scope.Type, ID: org.Name, ReadOnly: scope.ReadOnly}, nil
	}
	return nil, connect.Errorf(connect.CodeInvalidArgument, "unknown scope type")
}

func tokenToProto(t *storage.APIToken) *v1.APIToken {
	out := &v1.APIToken{
		Id:        t.ID,
		Name:      t.Name,
		CreatedBy: t.UserID,
		CreatedAt: timestamppb.New(t.CreatedAt),
	}
	if t.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*t.ExpiresAt)
	}
	if t.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*t.LastUsedAt)
	}
	if sc := auth.TokenScope(t); sc != nil {
		out.Scope = &v1.CredentialScope{Type: sc.Type, Id: sc.ID, ReadOnly: sc.ReadOnly}
	}
	return out
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

	var tokens []*storage.APIToken
	var total int64
	var err error
	if req.GitRepo != "" {
		// Deploy tokens of a repository are visible to its admins
		ns, name, _ := strings.Cut(req.GitRepo, "/")
		repo, err := s.authManager.GetStore().GetGitRepository(ctx, ns, name)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if repo == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		if s.gitAccess != nil {
			lv := s.gitAccess.Levels(ctx, user, repo)
			if lv.IsOwner || lv.CanAdmin {
				userID = ""
			}
		}
		tokens, total, err = s.authManager.GetStore().ListAPITokensScoped(ctx, userID, int32(v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO), repo.Namespace+"/"+repo.Name, q, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	} else {
		tokens, total, err = s.authManager.GetStore().ListAPITokens(ctx, userID, q, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}

	protoTokens := make([]*v1.APIToken, len(tokens))
	for i, t := range tokens {
		protoTokens[i] = tokenToProto(t)
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
