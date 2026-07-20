package services

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.AuthServiceHandler = (*AuthService)(nil)

var usernameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,38}[a-z0-9]$`)

type AuthService struct {
	store       *stores.Store
	log         *logger.Logger
	authManager *auth.Manager
	enforcer    *rbac.Enforcer
	oidcHandler *auth.OIDCHandler
}

func NewAuthService(store *stores.Store, manager *auth.Manager, enforcer *rbac.Enforcer, oidcHandler *auth.OIDCHandler, log *logger.Logger) *AuthService {
	return &AuthService{store: store, authManager: manager, enforcer: enforcer, oidcHandler: oidcHandler, log: log}
}

func (s *AuthService) Register(ctx context.Context, req *connect.Request[v1.RegisterRequest]) (*connect.Response[v1.RegisterResponse], error) {
	msg := req.Msg

	// Validate an invite code if provided
	var invite *storage.RegistrationInvite
	if msg.InviteCode != nil && *msg.InviteCode != "" {
		var err error
		invite, err = s.store.GetRegistrationInviteByCode(ctx, *msg.InviteCode)
		if err != nil || invite == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid invite code"))
		}
		if invite.ExpiresAt != nil && invite.ExpiresAt.Before(time.Now()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invite has expired"))
		}
		if invite.MaxUses > 0 && invite.UseCount >= invite.MaxUses {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invite has reached maximum uses"))
		}
		if invite.PinHash != "" {
			if msg.InvitePin == nil || *msg.InvitePin == "" {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invite PIN is required"))
			}
			if bcrypt.CompareHashAndPassword([]byte(invite.PinHash), []byte(*msg.InvitePin)) != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid invite PIN"))
			}
		}
	}

	if !s.authManager.IsRegistrationAllowed() && invite == nil {
		// Allow first user even if registration is disabled
		count, _ := s.store.CountUsers(ctx)
		if count > 0 {
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("registration is disabled"))
		}
	}

	if msg.Username == "" || msg.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if !usernameRegex.MatchString(msg.Username) {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if len(msg.Password) < 8 {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	existing, err := s.store.GetUserByUsername(ctx, msg.Username)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, nil)
	}

	if msg.Email != "" {
		existing, err = s.store.GetUserByEmail(ctx, msg.Email)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if existing != nil {
			return nil, connect.NewError(connect.CodeAlreadyExists, nil)
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(msg.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	count, err := s.store.CountUsers(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var emailPtr *string
	if msg.Email != "" {
		emailPtr = &msg.Email
	}

	user := &storage.User{
		Username:     msg.Username,
		Email:        emailPtr,
		PasswordHash: string(hash),
		DisplayName:  msg.Username,
		AuthProvider: "local",
		IsActive:     true,
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// First user gets admin role
	if count == 0 {
		_ = s.store.AssignRole(ctx, user.ID, "admin", "local")
	} else if invite != nil {
		// Invite-specified role ids, vanished roles are skipped
		var inviteRoleIDs []string
		_ = json.Unmarshal([]byte(invite.Roles), &inviteRoleIDs)
		assigned := 0
		for _, roleID := range inviteRoleIDs {
			if role, _ := s.store.GetRole(ctx, roleID); role != nil {
				_ = s.store.AssignRole(ctx, user.ID, role.Name, "invite")
				assigned++
			}
		}
		// If invite didn't specify roles, fall through to default
		if assigned == 0 {
			defaultRoles, _ := s.store.GetDefaultRoles(ctx)
			for _, role := range defaultRoles {
				_ = s.store.AssignRole(ctx, user.ID, role.Name, "local")
			}
		}
		_ = s.store.IncrementInviteUseCount(ctx, invite.ID)
	} else {
		defaultRoles, _ := s.store.GetDefaultRoles(ctx)
		for _, role := range defaultRoles {
			_ = s.store.AssignRole(ctx, user.ID, role.Name, "local")
		}
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)

	// Login the newly registered user
	_, _, token, _, err := s.authManager.Login(ctx, msg.Username, msg.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	permissions := s.getPermissionsForRoles(roleNamesOf(roles))

	return connect.NewResponse(&v1.RegisterResponse{
		User:         userToProto(user, roles),
		SessionToken: token,
		Permissions:  permissions,
	}), nil
}

func (s *AuthService) Login(ctx context.Context, req *connect.Request[v1.LoginRequest]) (*connect.Response[v1.LoginResponse], error) {
	msg := req.Msg

	if msg.Identifier == "" || msg.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	user, roleNames, token, _, err := s.authManager.Login(ctx, msg.Identifier, msg.Password)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)
	permissions := s.getPermissionsForRoles(roleNames)

	return connect.NewResponse(&v1.LoginResponse{
		User:         userToProto(user, roles),
		SessionToken: token,
		Permissions:  permissions,
	}), nil
}

func (s *AuthService) Logout(ctx context.Context, req *connect.Request[v1.LogoutRequest]) (*connect.Response[v1.LogoutResponse], error) {
	token := auth.ExtractToken(req.Header())
	if token != "" {
		_ = s.authManager.Logout(ctx, token)
	}

	return connect.NewResponse(&v1.LogoutResponse{}), nil
}

func (s *AuthService) GetCurrentUser(ctx context.Context, req *connect.Request[v1.GetCurrentUserRequest]) (*connect.Response[v1.GetCurrentUserResponse], error) {
	authUser := auth.UserFromContext(ctx)
	if authUser == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	user, err := s.store.GetUserByID(ctx, authUser.ID)
	if err != nil || user == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)
	permissions := s.getPermissionsForRoles(roleNamesOf(roles))

	return connect.NewResponse(&v1.GetCurrentUserResponse{
		User:        userToProto(user, roles),
		Permissions: permissions,
	}), nil
}

func (s *AuthService) RefreshSession(ctx context.Context, req *connect.Request[v1.RefreshSessionRequest]) (*connect.Response[v1.RefreshSessionResponse], error) {
	token := auth.ExtractToken(req.Header())
	if token == "" || strings.HasPrefix(token, "df_") {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("a session token is required"))
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	newToken, expiresAt, err := s.authManager.IssueSession(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Old session dies once the new one exists
	_ = s.authManager.Logout(ctx, token)

	return connect.NewResponse(&v1.RefreshSessionResponse{
		SessionToken: newToken,
		ExpiresAt:    expiresAt.Unix(),
	}), nil
}

func (s *AuthService) GetAuthStatus(ctx context.Context, req *connect.Request[v1.GetAuthStatusRequest]) (*connect.Response[v1.GetAuthStatusResponse], error) {
	count, _ := s.store.CountUsers(ctx)
	authCfg := s.authManager.Settings().System(ctx).GetAuth()

	return connect.NewResponse(&v1.GetAuthStatusResponse{
		LocalEnabled:        authCfg.GetLocalEnabled(),
		OidcEnabled:         authCfg.GetOidc().GetEnabled(),
		RegistrationEnabled: authCfg.GetLocalEnabled() && authCfg.GetLocalAllowRegistration(),
		AnonymousAccess:     authCfg.GetAnonymousAccess(),
		FirstUserSetup:      count == 0,
	}), nil
}

func (s *AuthService) GetOIDCLoginURL(ctx context.Context, req *connect.Request[v1.GetOIDCLoginURLRequest]) (*connect.Response[v1.GetOIDCLoginURLResponse], error) {
	if s.oidcHandler == nil || !s.oidcHandler.IsEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("OIDC is not enabled"))
	}
	return connect.NewResponse(&v1.GetOIDCLoginURLResponse{
		RedirectUrl: "/api/v1/auth/oidc/login",
	}), nil
}

func (s *AuthService) CreateInvite(ctx context.Context, req *connect.Request[v1.CreateInviteRequest]) (*connect.Response[v1.CreateInviteResponse], error) {
	msg := req.Msg
	authUser := auth.UserFromContext(ctx)
	if authUser == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	// Validate that all specified roles exist
	for _, roleID := range msg.RoleIds {
		role, _ := s.store.GetRole(ctx, roleID)
		if role == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("role %q does not exist", roleID))
		}
	}

	// Generate a crypto-random code
	rawCode := make([]byte, 16)
	if _, err := rand.Read(rawCode); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	code := base64.RawURLEncoding.EncodeToString(rawCode)

	// Hash PIN if provided
	var pinHash string
	if msg.Pin != nil && *msg.Pin != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*msg.Pin), bcrypt.DefaultCost)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		pinHash = string(hash)
	}

	rolesJSON, _ := json.Marshal(msg.RoleIds)

	var maxUses int
	if msg.MaxUses != nil {
		maxUses = int(*msg.MaxUses)
	}

	var expiresAt *time.Time
	if msg.ExpiresInHours != nil && *msg.ExpiresInHours > 0 {
		t := time.Now().Add(time.Duration(*msg.ExpiresInHours) * time.Hour)
		expiresAt = &t
	}

	invite := &storage.RegistrationInvite{
		Code:        code,
		Description: msg.Description,
		Roles:       string(rolesJSON),
		PinHash:     pinHash,
		MaxUses:     maxUses,
		ExpiresAt:   expiresAt,
		CreatedBy:   authUser.ID,
	}

	if err := s.store.CreateRegistrationInvite(ctx, invite); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.CreateInviteResponse{
		Invite: s.inviteToProto(ctx, invite),
	}), nil
}

func (s *AuthService) ListInvites(ctx context.Context, req *connect.Request[v1.ListInvitesRequest]) (*connect.Response[v1.ListInvitesResponse], error) {
	limit, offset := pages.Parse(req.Msg.Page)

	q := pages.ParseQuery(req.Msg.Page)
	if err := stores.InvitesQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	invites, total, err := s.store.ListRegistrationInvites(ctx, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoInvites := make([]*v1.RegistrationInvite, len(invites))
	for i, inv := range invites {
		protoInvites[i] = s.inviteToProto(ctx, inv)
	}

	return connect.NewResponse(&v1.ListInvitesResponse{
		Invites: protoInvites,
		Page:    pages.Info(offset, limit, total),
	}), nil
}

func (s *AuthService) GetInvite(ctx context.Context, req *connect.Request[v1.GetInviteRequest]) (*connect.Response[v1.GetInviteResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	invite, err := s.store.GetRegistrationInvite(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if invite == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	return connect.NewResponse(&v1.GetInviteResponse{
		Invite: s.inviteToProto(ctx, invite),
	}), nil
}

func (s *AuthService) DeleteInvite(ctx context.Context, req *connect.Request[v1.DeleteInviteRequest]) (*connect.Response[v1.DeleteInviteResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if err := s.store.DeleteRegistrationInvite(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.DeleteInviteResponse{}), nil
}

func (s *AuthService) BulkDeleteInvites(ctx context.Context, req *connect.Request[v1.BulkDeleteInvitesRequest]) (*connect.Response[v1.BulkDeleteInvitesResponse], error) {
	if len(req.Msg.Ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no invite ids provided"))
	}

	existing, err := s.store.FilterExistingInviteIDs(ctx, req.Msg.Ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &v1.BulkDeleteInvitesResponse{}
	var targets []string
	for _, id := range req.Msg.Ids {
		if !existing[id] {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "invite not found"})
			continue
		}
		targets = append(targets, id)
	}

	deleted, err := s.store.BulkDeleteRegistrationInvites(ctx, targets)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp.DeletedCount = int32(deleted)
	return connect.NewResponse(resp), nil
}

func (s *AuthService) ValidateInvite(ctx context.Context, req *connect.Request[v1.ValidateInviteRequest]) (*connect.Response[v1.ValidateInviteResponse], error) {
	if req.Msg.Code == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	invite, err := s.store.GetRegistrationInviteByCode(ctx, req.Msg.Code)
	if err != nil || invite == nil {
		return connect.NewResponse(&v1.ValidateInviteResponse{Valid: false}), nil
	}

	if invite.ExpiresAt != nil && invite.ExpiresAt.Before(time.Now()) {
		return connect.NewResponse(&v1.ValidateInviteResponse{Valid: false}), nil
	}

	if invite.MaxUses > 0 && invite.UseCount >= invite.MaxUses {
		return connect.NewResponse(&v1.ValidateInviteResponse{Valid: false}), nil
	}

	return connect.NewResponse(&v1.ValidateInviteResponse{
		Valid:       true,
		RequiresPin: invite.PinHash != "",
	}), nil
}

func (s *AuthService) inviteToProto(ctx context.Context, inv *storage.RegistrationInvite) *v1.RegistrationInvite {
	var roleIDs []string
	_ = json.Unmarshal([]byte(inv.Roles), &roleIDs)
	roles, _ := s.store.GetRolesByIDs(ctx, roleIDs)

	proto := &v1.RegistrationInvite{
		Id:          inv.ID,
		Code:        inv.Code,
		Description: inv.Description,
		Roles:       roleRefsOf(roles),
		HasPin:      inv.PinHash != "",
		MaxUses:     int32(inv.MaxUses),
		UseCount:    int32(inv.UseCount),
		CreatedBy:   inv.CreatedBy,
		CreatedAt:   timestamppb.New(inv.CreatedAt),
	}
	if inv.ExpiresAt != nil {
		proto.ExpiresAt = timestamppb.New(*inv.ExpiresAt)
	}
	return proto
}

func (s *AuthService) getPermissionsForRoles(roles []string) []*v1.Permission {
	if s.enforcer == nil {
		return nil
	}
	var perms []*v1.Permission
	seen := make(map[string]bool)
	for _, role := range roles {
		for _, p := range s.enforcer.GetPermissionsForRole(role) {
			key := p.Resource + "/" + p.Action + "/" + p.ObjectID
			if seen[key] {
				continue
			}
			seen[key] = true
			perms = append(perms, &v1.Permission{
				Resource: p.Resource,
				Action:   p.Action,
				ObjectId: p.ObjectID,
			})
		}
	}
	return perms
}

// Public view hides email roles and status
func publicUserToProto(u *storage.User) *v1.User {
	return &v1.User{
		Id:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		CreatedAt:   timestamppb.New(u.CreatedAt),
		UpdatedAt:   timestamppb.New(u.UpdatedAt),
	}
}

// Wire refs for role rows
func roleRefsOf(roles []*storage.Role) []*v1.RoleRef {
	refs := make([]*v1.RoleRef, len(roles))
	for i, r := range roles {
		refs[i] = &v1.RoleRef{Id: r.ID, Name: r.Name}
	}
	return refs
}

// Casbin subject names for role rows
func roleNamesOf(roles []*storage.Role) []string {
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	return names
}

func userToProto(u *storage.User, roles []*storage.Role) *v1.User {
	proto := &v1.User{
		Id:                 u.ID,
		Username:           u.Username,
		DisplayName:        u.DisplayName,
		Roles:              roleRefsOf(roles),
		AuthProvider:       u.AuthProvider,
		IsActive:           u.IsActive,
		MustChangePassword: u.MustChangePassword,
		OidcLinked:         u.OIDCSubject != "",
		CreatedAt:          timestamppb.New(u.CreatedAt),
		UpdatedAt:          timestamppb.New(u.UpdatedAt),
	}
	if u.Email != nil {
		proto.Email = *u.Email
	}
	return proto
}
