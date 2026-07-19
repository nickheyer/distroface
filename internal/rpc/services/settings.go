package services

import (
	"context"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/proto"
)

var _ distrofacev1connect.SettingsServiceHandler = (*SettingsService)(nil)

type SettingsService struct {
	store    *stores.Store
	resolver *settings.Resolver
	enforcer *rbac.Enforcer
	log      *logger.Logger
}

func NewSettingsService(store *stores.Store, resolver *settings.Resolver, enforcer *rbac.Enforcer, log *logger.Logger) *SettingsService {
	return &SettingsService{store: store, resolver: resolver, enforcer: enforcer, log: log}
}

func (s *SettingsService) isSystemAdmin(ctx context.Context) bool {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return false
	}
	allowed, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceSettings, rbac.ActionManage, "*")
	return allowed
}

// Write access, org and portal scopes need an org admin
func (s *SettingsService) requireScopeAdmin(ctx context.Context, scope *v1.SettingsScope) error {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	orgID, err := s.scopeOrgID(ctx, scope)
	if err != nil {
		return err
	}
	if orgID == "" {
		if !s.isSystemAdmin(ctx) {
			return connect.NewError(connect.CodePermissionDenied, nil)
		}
		return nil
	}
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, orgID)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, orgID, user.ID)
		if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
			return connect.NewError(connect.CodePermissionDenied, nil)
		}
	}
	return nil
}

// Read access, org and portal scopes need membership
func (s *SettingsService) requireScopeRead(ctx context.Context, scope *v1.SettingsScope) error {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	orgID, err := s.scopeOrgID(ctx, scope)
	if err != nil {
		return err
	}
	if orgID == "" || s.isSystemAdmin(ctx) {
		return nil
	}
	member, _ := s.store.GetOrgMember(ctx, orgID, user.ID)
	if member == nil {
		return connect.NewError(connect.CodePermissionDenied, nil)
	}
	return nil
}

// Empty for system scope, owning org otherwise
func (s *SettingsService) scopeOrgID(ctx context.Context, scope *v1.SettingsScope) (string, error) {
	switch scope.GetType() {
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM:
		return "", nil
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG:
		if scope.GetScopeId() == "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scope_id required"))
		}
		return scope.GetScopeId(), nil
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL:
		if scope.GetScopeId() == "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scope_id required"))
		}
		orgID, err := s.store.GetPortalOrgID(ctx, scope.GetScopeId())
		if err != nil {
			return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("portal not found"))
		}
		return orgID, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scope type required"))
	}
}

// Fields anonymous callers may read from system effective settings
func publicSubset(eff *v1.Settings) *v1.Settings {
	return &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String(eff.GetServer().GetPublicHostname())},
		Tls:    &v1.TLSSettings{Mode: eff.GetTls().GetMode().Enum()},
		Auth: &v1.AuthSettings{
			LocalEnabled:           proto.Bool(eff.GetAuth().GetLocalEnabled()),
			LocalAllowRegistration: proto.Bool(eff.GetAuth().GetLocalAllowRegistration()),
			AnonymousAccess:        proto.Bool(eff.GetAuth().GetAnonymousAccess()),
			Oidc:                   &v1.OIDCSettings{Enabled: proto.Bool(eff.GetAuth().GetOidc().GetEnabled())},
		},
	}
}

func (s *SettingsService) GetSettings(ctx context.Context, req *connect.Request[v1.GetSettingsRequest]) (*connect.Response[v1.GetSettingsResponse], error) {
	if err := s.requireScopeRead(ctx, req.Msg.GetScope()); err != nil {
		return nil, err
	}
	stored, err := s.resolver.Stored(ctx, req.Msg.GetScope().GetType(), req.Msg.GetScope().GetScopeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := proto.Clone(stored).(*v1.Settings)
	settings.Redact(out)
	return connect.NewResponse(&v1.GetSettingsResponse{
		Settings:    out,
		LockedPaths: s.resolver.LockedPaths(),
	}), nil
}

func (s *SettingsService) GetEffectiveSettings(ctx context.Context, req *connect.Request[v1.GetEffectiveSettingsRequest]) (*connect.Response[v1.GetEffectiveSettingsResponse], error) {
	scope := req.Msg.GetScope()
	if scope.GetType() == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_UNSPECIFIED {
		scope = &v1.SettingsScope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM}
	}

	// Anonymous callers get the public system subset only
	if auth.UserFromContext(ctx) == nil {
		if scope.GetType() != v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM {
			return nil, connect.NewError(connect.CodeUnauthenticated, nil)
		}
		eff, _, err := s.resolver.Effective(ctx, scope.GetType(), "")
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&v1.GetEffectiveSettingsResponse{Settings: publicSubset(eff)}), nil
	}

	if err := s.requireScopeRead(ctx, scope); err != nil {
		return nil, err
	}
	eff, prov, err := s.resolver.Effective(ctx, scope.GetType(), scope.GetScopeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := proto.Clone(eff).(*v1.Settings)
	settings.Redact(out)
	return connect.NewResponse(&v1.GetEffectiveSettingsResponse{Settings: out, Provenance: prov}), nil
}

func (s *SettingsService) UpdateSettings(ctx context.Context, req *connect.Request[v1.UpdateSettingsRequest]) (*connect.Response[v1.UpdateSettingsResponse], error) {
	scope := req.Msg.GetScope()
	if err := s.requireScopeAdmin(ctx, scope); err != nil {
		return nil, err
	}
	patch := req.Msg.GetSettings()
	if patch == nil {
		patch = &v1.Settings{}
	}
	if err := validateSettingsPatch(patch); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	stored, err := s.resolver.Update(ctx, scope.GetType(), scope.GetScopeId(), patch, req.Msg.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if user := auth.UserFromContext(ctx); user != nil {
		s.log.Info("settings updated scope=%v/%s by=%s paths=%v",
			scope.GetType(), scope.GetScopeId(), user.Username, req.Msg.GetUpdateMask().GetPaths())
	}

	eff, prov, err := s.resolver.Effective(ctx, scope.GetType(), scope.GetScopeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	storedOut := proto.Clone(stored).(*v1.Settings)
	effOut := proto.Clone(eff).(*v1.Settings)
	settings.Redact(storedOut)
	settings.Redact(effOut)
	return connect.NewResponse(&v1.UpdateSettingsResponse{
		Stored:    &v1.GetSettingsResponse{Settings: storedOut, LockedPaths: s.resolver.LockedPaths()},
		Effective: &v1.GetEffectiveSettingsResponse{Settings: effOut, Provenance: prov},
	}), nil
}

// Cross field sanity on values present in a patch
func validateSettingsPatch(patch *v1.Settings) error {
	if a := patch.GetAuth(); a != nil {
		if a.SessionTimeoutSeconds != nil && *a.SessionTimeoutSeconds < 300 {
			return fmt.Errorf("session timeout must be at least 300 seconds")
		}
		if a.TokenExpirySeconds != nil && *a.TokenExpirySeconds < 60 {
			return fmt.Errorf("token expiry must be at least 60 seconds")
		}
	}
	if acme := patch.GetAcme(); acme != nil && acme.ChallengePort != nil && *acme.ChallengePort != "" {
		port, err := strconv.Atoi(*acme.ChallengePort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid acme challenge port")
		}
	}
	for _, pattern := range patch.GetPortals().GetHostnameBlacklist() {
		if pattern == "" {
			return fmt.Errorf("empty hostname blacklist pattern")
		}
	}
	return nil
}
