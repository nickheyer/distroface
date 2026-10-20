package services

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/portal"
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
	git      *gitrepo.Manager
	ssh      *gitrepo.SSHManager // Nil when git over ssh is not wired
	log      *logger.Logger
}

func NewSettingsService(store *stores.Store, resolver *settings.Resolver, enforcer *rbac.Enforcer, git *gitrepo.Manager, ssh *gitrepo.SSHManager, log *logger.Logger) *SettingsService {
	return &SettingsService{store: store, resolver: resolver, enforcer: enforcer, log: log, git: git, ssh: ssh}
}

func (s *SettingsService) isSystemAdmin(ctx context.Context) bool {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return false
	}
	allowed, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceSettings, rbac.ActionManage, "*")
	return allowed
}

// Repository tier access, admin level for writes and visibility for reads
func (s *SettingsService) requireRepoScope(ctx context.Context, user *auth.AuthenticatedUser, repoID string, write bool) error {
	if repoID == "" {
		return connect.Errorf(connect.CodeInvalidArgument, "scope_id required")
	}
	if s.git == nil {
		return connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	repo, err := s.store.GetGitRepositoryByID(ctx, repoID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil || !s.git.Access().CanSee(ctx, user, repo) {
		return connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	if s.isSystemAdmin(ctx) {
		return nil
	}
	levels := s.git.Access().Levels(ctx, user, repo)
	if write && !levels.CanAdmin && !levels.IsOwner {
		return connect.NewError(connect.CodePermissionDenied, "")
	}
	return nil
}

// Write access, org and portal scopes need an org admin
func (s *SettingsService) requireScopeAdmin(ctx context.Context, scope *v1.SettingsScope) error {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return connect.NewError(connect.CodeUnauthenticated, "")
	}
	if scope.GetType() == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO {
		return s.requireRepoScope(ctx, user, scope.GetScopeId(), true)
	}
	orgID, err := s.scopeOrgID(ctx, scope)
	if err != nil {
		return err
	}
	if orgID == "" {
		if !s.isSystemAdmin(ctx) {
			return connect.NewError(connect.CodePermissionDenied, "")
		}
		return nil
	}
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, orgID)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, orgID, user.ID)
		if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
			return connect.NewError(connect.CodePermissionDenied, "")
		}
	}
	return nil
}

// Read access, org and portal scopes need membership
func (s *SettingsService) requireScopeRead(ctx context.Context, scope *v1.SettingsScope) error {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return connect.NewError(connect.CodeUnauthenticated, "")
	}
	if scope.GetType() == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO {
		return s.requireRepoScope(ctx, user, scope.GetScopeId(), false)
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
		return connect.NewError(connect.CodePermissionDenied, "")
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
			return "", connect.Errorf(connect.CodeInvalidArgument, "scope_id required")
		}
		return scope.GetScopeId(), nil
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL:
		if scope.GetScopeId() == "" {
			return "", connect.Errorf(connect.CodeInvalidArgument, "scope_id required")
		}
		orgID, err := s.store.GetPortalOrgID(ctx, scope.GetScopeId())
		if err != nil {
			return "", connect.Errorf(connect.CodeNotFound, "portal not found")
		}
		return orgID, nil
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO:
		if scope.GetScopeId() == "" {
			return "", connect.Errorf(connect.CodeInvalidArgument, "scope_id required")
		}
		orgID, err := s.store.GetGitRepoOrgID(ctx, scope.GetScopeId())
		if err != nil {
			return "", connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		return orgID, nil
	default:
		return "", connect.Errorf(connect.CodeInvalidArgument, "scope type required")
	}
}

// Signed out readable fields, anonymous reflects the host
func publicSubset(ctx context.Context, eff *v1.Settings) *v1.Settings {
	return &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String(eff.GetServer().GetPublicHostname())},
		Tls:    &v1.TLSSettings{Mode: eff.GetTls().GetMode().Enum()},
		Auth: &v1.AuthSettings{
			LocalEnabled:           proto.Bool(eff.GetAuth().GetLocalEnabled()),
			LocalAllowRegistration: proto.Bool(eff.GetAuth().GetLocalAllowRegistration()),
			AnonymousAccess:        proto.Bool(portal.AnonymousAllowed(ctx, eff.GetAuth().GetAnonymousAccess())),
			Oidc:                   &v1.OIDCSettings{Enabled: proto.Bool(eff.GetAuth().GetOidc().GetEnabled())},
		},
		Git: &v1.GitSettings{
			Enabled:       proto.Bool(eff.GetGit().GetEnabled()),
			SshEnabled:    proto.Bool(eff.GetGit().GetSshEnabled()),
			SshPort:       proto.Int32(eff.GetGit().GetSshPort()),
			SshPublicHost: proto.String(eff.GetGit().GetSshPublicHost()),
		},
	}
}

func (s *SettingsService) GetSettings(ctx context.Context, req *v1.GetSettingsRequest) (*v1.GetSettingsResponse, error) {
	if err := s.requireScopeRead(ctx, req.GetScope()); err != nil {
		return nil, err
	}
	stored, err := s.resolver.Stored(ctx, req.GetScope().GetType(), req.GetScope().GetScopeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := proto.Clone(stored).(*v1.Settings)
	settings.Redact(out)
	return &v1.GetSettingsResponse{
		Settings:    out,
		LockedPaths: s.resolver.LockedPaths(),
	}, nil
}

func (s *SettingsService) GetEffectiveSettings(ctx context.Context, req *v1.GetEffectiveSettingsRequest) (*v1.GetEffectiveSettingsResponse, error) {
	scope := req.GetScope()
	if scope.GetType() == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_UNSPECIFIED {
		scope = &v1.SettingsScope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM}
	}

	// Signed out and anonymous callers get the public subset
	if user := auth.UserFromContext(ctx); user == nil || user.IsAnonymous() {
		if scope.GetType() != v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM {
			return nil, connect.NewError(connect.CodeUnauthenticated, "")
		}
		eff, _, err := s.resolver.Effective(ctx, scope.GetType(), "")
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		return &v1.GetEffectiveSettingsResponse{Settings: publicSubset(ctx, eff)}, nil
	}

	if err := s.requireScopeRead(ctx, scope); err != nil {
		return nil, err
	}
	eff, prov, err := s.resolver.Effective(ctx, scope.GetType(), scope.GetScopeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := proto.Clone(eff).(*v1.Settings)
	settings.Redact(out)
	// Members see that mail works, only scope admins see the relay behind it
	if s.requireScopeAdmin(ctx, scope) != nil {
		out.Mail = &v1.MailSettings{Enabled: proto.Bool(eff.GetMail().GetEnabled())}
		kept := prov[:0]
		for _, p := range prov {
			if !strings.HasPrefix(p.GetPath(), "mail.") || p.GetPath() == "mail.enabled" {
				kept = append(kept, p)
			}
		}
		prov = kept
	}
	return &v1.GetEffectiveSettingsResponse{Settings: out, Provenance: prov}, nil
}

// Host keys, signing key, storage path and usage for the admin git page
func (s *SettingsService) GetGitServerInfo(ctx context.Context, req *v1.GetGitServerInfoRequest) (*v1.GetGitServerInfoResponse, error) {
	if err := s.requireScopeAdmin(ctx, &v1.SettingsScope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM}); err != nil {
		return nil, err
	}
	out := &v1.GetGitServerInfoResponse{}
	if s.git != nil {
		out.StoragePath = s.git.Engine().ReposDir()
		out.SigningPublicKey = s.git.Signing().InstancePublicKey()
	}
	if s.ssh != nil {
		out.SshHostKeys = s.ssh.HostKeys()
		out.SshHostKeyFingerprints = s.ssh.HostKeyFingerprints()
	}
	count, bytes, lfs, err := s.store.GitStorageTotals(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out.RepositoryCount, out.RepositoryBytes, out.LfsBytes = count, bytes, lfs
	return out, nil
}

func (s *SettingsService) UpdateSettings(ctx context.Context, req *v1.UpdateSettingsRequest) (*v1.UpdateSettingsResponse, error) {
	scope := req.GetScope()
	if err := s.requireScopeAdmin(ctx, scope); err != nil {
		return nil, err
	}
	patch := req.GetSettings()
	if patch == nil {
		patch = &v1.Settings{}
	}
	if err := validateSettingsPatch(patch); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if scope.GetType() == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG {
		if err := s.checkOrgTightens(ctx, patch); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
	}
	if err := s.checkDirectoryNotSelf(ctx, patch); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := s.checkSSHPortBinds(ctx, scope, patch, req.GetUpdateMask().GetPaths()); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	}

	stored, err := s.resolver.Update(ctx, scope.GetType(), scope.GetScopeId(), patch, req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	if user := auth.UserFromContext(ctx); user != nil {
		s.log.Info("settings updated scope=%v/%s by=%s paths=%v",
			scope.GetType(), scope.GetScopeId(), user.Username, req.GetUpdateMask().GetPaths())
	}

	eff, prov, err := s.resolver.Effective(ctx, scope.GetType(), scope.GetScopeId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	storedOut := proto.Clone(stored).(*v1.Settings)
	effOut := proto.Clone(eff).(*v1.Settings)
	settings.Redact(storedOut)
	settings.Redact(effOut)
	return &v1.UpdateSettingsResponse{
		Stored:    &v1.GetSettingsResponse{Settings: storedOut, LockedPaths: s.resolver.LockedPaths()},
		Effective: &v1.GetEffectiveSettingsResponse{Settings: effOut, Provenance: prov},
	}, nil
}

// Ssh settings only save when the resulting port binds on this host
func (s *SettingsService) checkSSHPortBinds(ctx context.Context, scope *v1.SettingsScope, patch *v1.Settings, paths []string) error {
	if scope.GetType() != v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM || s.ssh == nil {
		return nil
	}
	touched := false
	for _, p := range paths {
		if p == "git" || p == "git.ssh_enabled" || p == "git.ssh_port" || p == "git.enabled" {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}
	eff := s.resolver.System(ctx).GetGit()
	enabled := eff.GetEnabled()
	sshOn := eff.GetSshEnabled()
	port := int(eff.GetSshPort())
	if g := patch.GetGit(); g != nil {
		if g.Enabled != nil {
			enabled = *g.Enabled
		}
		if g.SshEnabled != nil {
			sshOn = *g.SshEnabled
		}
		if g.SshPort != nil {
			port = int(*g.SshPort)
		}
	}
	if !enabled || !sshOn {
		return nil
	}
	if _, err := s.ssh.Probe(port); err != nil {
		return fmt.Errorf("git ssh not saved: %w", err)
	}
	return nil
}

func (s *SettingsService) ProbeGitSSHPort(ctx context.Context, req *v1.ProbeGitSSHPortRequest) (*v1.ProbeGitSSHPortResponse, error) {
	if err := s.requireScopeAdmin(ctx, &v1.SettingsScope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM}); err != nil {
		return nil, err
	}
	port := int(req.GetPort())
	if port == 0 {
		port = int(s.resolver.System(ctx).GetGit().GetSshPort())
	}
	out := &v1.ProbeGitSSHPortResponse{Port: int32(port)}
	if s.ssh == nil {
		out.Message = "git over ssh is not available on this instance"
		return out, nil
	}
	listening, err := s.ssh.Probe(port)
	out.Listening = listening
	if err != nil {
		out.Message = err.Error()
		return out, nil
	}
	out.Ok = true
	return out, nil
}

// An acme client pointed at this instance dials itself
func (s *SettingsService) checkDirectoryNotSelf(ctx context.Context, patch *v1.Settings) error {
	dir := patch.GetAcme().GetDirectoryUrl()
	if dir == "" {
		return nil
	}
	u, err := url.Parse(dir)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("acme directory url must be absolute")
	}
	self := s.resolver.System(ctx).GetServer().GetPublicHostname()
	if host, _, err := net.SplitHostPort(self); err == nil {
		self = host
	}
	if strings.EqualFold(u.Hostname(), strings.TrimSpace(self)) {
		return fmt.Errorf("acme directory cannot point at this instance, use the org ca or instance ca certificate source instead")
	}
	return nil
}

// An org may tighten the instance git limits, never loosen them
func (s *SettingsService) checkOrgTightens(ctx context.Context, patch *v1.Settings) error {
	g := patch.GetGit()
	if g == nil {
		return nil
	}
	sys := s.resolver.System(ctx).GetGit()
	if g.MaxRepoSizeMb != nil && sys.GetMaxRepoSizeMb() > 0 && (*g.MaxRepoSizeMb <= 0 || *g.MaxRepoSizeMb > sys.GetMaxRepoSizeMb()) {
		return fmt.Errorf("max repository size cannot exceed the instance limit of %d MB", sys.GetMaxRepoSizeMb())
	}
	if g.LfsEnabled != nil && *g.LfsEnabled && !sys.GetLfsEnabled() {
		return fmt.Errorf("lfs is disabled on this instance")
	}
	return nil
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
	if g := patch.GetGit(); g != nil {
		if g.SshPort != nil && (*g.SshPort < 1 || *g.SshPort > 65535) {
			return fmt.Errorf("ssh port must be between 1 and 65535")
		}
		if b := g.GetActions().GetBuiltinRunner(); b != nil {
			if b.Concurrency != nil && (*b.Concurrency < 1 || *b.Concurrency > 64) {
				return fmt.Errorf("built in runner concurrency must be between 1 and 64")
			}
		}
		if g.SshPublicHost != nil && *g.SshPublicHost != "" {
			host := *g.SshPublicHost
			if h, p, err := net.SplitHostPort(host); err == nil {
				host = h
				if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
					return fmt.Errorf("ssh public host port must be between 1 and 65535")
				}
			}
			if strings.ContainsAny(host, "/ \t") {
				return fmt.Errorf("ssh public host must be a host or host:port")
			}
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
