package services

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.OrganizationServiceHandler = (*OrganizationService)(nil)

type OrganizationService struct {
	store    *stores.Store
	registry *registry.RegistryAccess
	enforcer *rbac.Enforcer
	res      *settings.Resolver
	log      *logger.Logger
}

func NewOrganizationService(store *stores.Store, registry *registry.RegistryAccess, enforcer *rbac.Enforcer, res *settings.Resolver, log *logger.Logger) *OrganizationService {
	return &OrganizationService{store: store, registry: registry, enforcer: enforcer, res: res, log: log}
}

func (s *OrganizationService) CreateOrganization(ctx context.Context, req *v1.CreateOrganizationRequest) (*v1.CreateOrganizationResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	msg := req
	if msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	if !usernameRegex.MatchString(msg.Name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid organization name")
	}

	// Org name must not collide with existing usernames
	existingUser, _ := s.store.GetUserByUsername(ctx, msg.Name)
	if existingUser != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "name already taken by a user")
	}

	existingOrg, _ := s.store.GetOrganization(ctx, msg.Name)
	if existingOrg != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "organization already exists")
	}

	displayName := msg.DisplayName
	if displayName == "" {
		displayName = msg.Name
	}

	org := &storage.Organization{
		Name:        msg.Name,
		DisplayName: displayName,
		Description: msg.Description,
		CreatedBy:   user.ID,
	}

	if err := s.store.CreateOrganization(ctx, org); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	// Creator becomes owner
	member := &storage.OrgMember{
		OrgID:  org.ID,
		UserID: user.ID,
		Role:   storage.OrgRoleOwner,
	}
	if err := s.store.AddOrgMember(ctx, member); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.CreateOrganizationResponse{
		Organization: orgToProto(org, 1, storage.OrgRoleOwner),
	}, nil
}

func (s *OrganizationService) GetOrganization(ctx context.Context, req *v1.GetOrganizationRequest) (*v1.GetOrganizationResponse, error) {
	if req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganization(ctx, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	// Object scoped read check happens here, slug resolution needs the id
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	allowed, err := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionRead, org.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if !allowed {
		return nil, connect.NewError(connect.CodePermissionDenied, "")
	}

	memberCount, _ := s.store.GetOrgMemberCount(ctx, org.ID)

	return &v1.GetOrganizationResponse{
		Organization: orgToProto(org, int32(memberCount), s.currentRole(ctx, org.ID)),
	}, nil
}

func (s *OrganizationService) ListOrganizations(ctx context.Context, req *v1.ListOrganizationsRequest) (*v1.ListOrganizationsResponse, error) {
	// Portal hosts see only their own org
	if p := portal.FromContext(ctx); p != nil {
		org, err := s.store.GetOrganization(ctx, p.OrgName)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		resp := &v1.ListOrganizationsResponse{Page: &v1.PageInfo{}}
		if org != nil {
			memberCount, _ := s.store.GetOrgMemberCount(ctx, org.ID)
			resp.Organizations = []*v1.Organization{orgToProto(org, int32(memberCount), s.currentRole(ctx, org.ID))}
			resp.Page.TotalCount = 1
		}
		return resp, nil
	}

	limit, offset := pages.Parse(req.Page)

	user := auth.UserFromContext(ctx)
	var userID string
	var canManage bool
	roles := map[string]string{}
	if user != nil {
		userID = user.ID
		canManage, _ = s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, "*")
		roles, _ = s.store.ListUserOrgRoles(ctx, user.ID)
	}

	q := pages.ParseQuery(req.Page)
	if err := stores.OrgsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	orgs, total, err := s.store.ListOrganizations(ctx, userID, canManage, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoOrgs := make([]*v1.Organization, len(orgs))
	for i, o := range orgs {
		memberCount, _ := s.store.GetOrgMemberCount(ctx, o.ID)
		protoOrgs[i] = orgToProto(o, int32(memberCount), roles[o.ID])
	}

	return &v1.ListOrganizationsResponse{
		Organizations: protoOrgs,
		Page:          pages.Info(offset, limit, total),
	}, nil
}

func (s *OrganizationService) UpdateOrganization(ctx context.Context, req *v1.UpdateOrganizationRequest) (*v1.UpdateOrganizationResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganizationByID(ctx, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	// Users with manage permission on this org bypass membership checks
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, "")
		}
	}

	if req.DisplayName != nil {
		org.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		org.Description = *req.Description
	}

	if err := s.store.UpdateOrganization(ctx, org); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	memberCount, _ := s.store.GetOrgMemberCount(ctx, org.ID)

	return &v1.UpdateOrganizationResponse{
		Organization: orgToProto(org, int32(memberCount), s.currentRole(ctx, org.ID)),
	}, nil
}

func (s *OrganizationService) DeleteOrganization(ctx context.Context, req *v1.DeleteOrganizationRequest) (*v1.DeleteOrganizationResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganizationByID(ctx, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	// Users with manage permission on this org bypass ownership check
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if member == nil || member.Role != storage.OrgRoleOwner {
			return nil, connect.NewError(connect.CodePermissionDenied, "")
		}
	}

	_, err = s.store.DeleteOrganization(ctx, org.ID, org.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	// Drop the org's settings document with the org
	_ = s.res.DeleteScope(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, org.ID)

	// Best-effort cleanup of registry storage
	if s.registry != nil {
		_ = s.registry.DeleteNamespace(org.Name)
	}

	return &v1.DeleteOrganizationResponse{}, nil
}

func (s *OrganizationService) ListOrgMembers(ctx context.Context, req *v1.ListOrgMembersRequest) (*v1.ListOrgMembersResponse, error) {
	if req.OrgId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganizationByID(ctx, req.OrgId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	limit, offset := pages.Parse(req.Page)

	q := pages.ParseQuery(req.Page)
	if err := stores.OrgMembersQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	members, total, err := s.store.ListOrgMembers(ctx, org.ID, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoMembers := make([]*v1.OrgMember, len(members))
	for i, m := range members {
		protoMembers[i] = orgMemberToProto(m)
	}

	return &v1.ListOrgMembersResponse{
		Members: protoMembers,
		Page:    pages.Info(offset, limit, total),
	}, nil
}

func (s *OrganizationService) AddOrgMember(ctx context.Context, req *v1.AddOrgMemberRequest) (*v1.AddOrgMemberResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.OrgId == "" || req.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganizationByID(ctx, req.OrgId)
	if err != nil || org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	role := orgRoleToString(req.Role)

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	if !canManage {
		requesterMember, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if requesterMember == nil || (requesterMember.Role != storage.OrgRoleOwner && requesterMember.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, "")
		}
		// Only owner can grant owner
		if role == storage.OrgRoleOwner && requesterMember.Role != storage.OrgRoleOwner {
			return nil, connect.Errorf(connect.CodePermissionDenied, "only owners can grant the owner role")
		}
	}

	targetUser, _ := s.store.GetUserByID(ctx, req.UserId)
	if targetUser == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user not found")
	}

	// Check if already a member
	existing, _ := s.store.GetOrgMember(ctx, org.ID, targetUser.ID)
	if existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "user is already a member")
	}
	member := &storage.OrgMember{
		OrgID:  org.ID,
		UserID: targetUser.ID,
		Role:   role,
	}

	if err := s.store.AddOrgMember(ctx, member); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	member.User = targetUser

	return &v1.AddOrgMemberResponse{
		Member: orgMemberToProto(member),
	}, nil
}

func (s *OrganizationService) RemoveOrgMember(ctx context.Context, req *v1.RemoveOrgMemberRequest) (*v1.RemoveOrgMemberResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.OrgId == "" || req.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganizationByID(ctx, req.OrgId)
	if err != nil || org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	if !canManage {
		requesterMember, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if requesterMember == nil || (requesterMember.Role != storage.OrgRoleOwner && requesterMember.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, "")
		}
	}

	targetUser, _ := s.store.GetUserByID(ctx, req.UserId)
	if targetUser == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	member, _ := s.store.GetOrgMember(ctx, org.ID, targetUser.ID)
	if member == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user is not a member")
	}

	// Last owner cannot be removed
	if member.Role == storage.OrgRoleOwner {
		owners, err := s.store.CountOrgMembersByRole(ctx, org.ID, storage.OrgRoleOwner)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if owners <= 1 {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "cannot remove the last owner of an organization")
		}
	}

	if err := s.store.RemoveOrgMember(ctx, org.ID, targetUser.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.RemoveOrgMemberResponse{}, nil
}

func (s *OrganizationService) UpdateOrgMemberRole(ctx context.Context, req *v1.UpdateOrgMemberRoleRequest) (*v1.UpdateOrgMemberRoleResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	if req.OrgId == "" || req.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	org, err := s.store.GetOrganizationByID(ctx, req.OrgId)
	if err != nil || org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	if !canManage {
		orgAuthority := []string{storage.OrgRoleOwner, storage.OrgRoleAdmin}
		requesterMember, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if requesterMember == nil {
			return nil, connect.NewError(connect.CodePermissionDenied, "user not found")
		}
		if !slices.Contains(orgAuthority, requesterMember.Role) {
			return nil, connect.NewError(
				connect.CodePermissionDenied,
				fmt.Sprintf(
					"user's org role (%s) is not an org authority role: %s",
					requesterMember.Role,
					strings.Join(orgAuthority, ", ")),
			)
		}
	}

	targetUser, _ := s.store.GetUserByID(ctx, req.UserId)
	if targetUser == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	member, _ := s.store.GetOrgMember(ctx, org.ID, targetUser.ID)
	if member == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user is not a member")
	}

	newRole := orgRoleToString(req.Role)

	// Last owner cannot be demoted
	if member.Role == storage.OrgRoleOwner && newRole != storage.OrgRoleOwner {
		owners, err := s.store.CountOrgMembersByRole(ctx, org.ID, storage.OrgRoleOwner)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if owners <= 1 {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "cannot demote the last owner of an organization")
		}
	}

	member.Role = newRole
	if err := s.store.UpdateOrgMember(ctx, member); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	member.User = targetUser

	return &v1.UpdateOrgMemberRoleResponse{
		Member: orgMemberToProto(member),
	}, nil
}

func (s *OrganizationService) TransferOrgOwnership(ctx context.Context, req *v1.TransferOrgOwnershipRequest) (*v1.TransferOrgOwnershipResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if req.OrgId == "" || req.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}
	org, err := s.store.GetOrganizationByID(ctx, req.OrgId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	// Only owners hand off ownership, manage permission bypasses
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	requester, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
	if !canManage && (requester == nil || requester.Role != storage.OrgRoleOwner) {
		return nil, connect.NewError(connect.CodePermissionDenied, "")
	}

	targetUser, _ := s.store.GetUserByID(ctx, req.UserId)
	if targetUser == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user not found")
	}
	if targetUser.ID == user.ID {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "cannot transfer ownership to yourself")
	}
	target, _ := s.store.GetOrgMember(ctx, org.ID, targetUser.ID)
	if target == nil {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "new owner must be a member of the organization")
	}

	target.Role = storage.OrgRoleOwner
	if err := s.store.UpdateOrgMember(ctx, target); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if requester != nil && requester.Role == storage.OrgRoleOwner {
		requester.Role = storage.OrgRoleAdmin
		if err := s.store.UpdateOrgMember(ctx, requester); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}

	return &v1.TransferOrgOwnershipResponse{}, nil
}

func orgToProto(o *storage.Organization, memberCount int32, currentRole string) *v1.Organization {
	proto := &v1.Organization{
		Id:          o.ID,
		Name:        o.Name,
		DisplayName: o.DisplayName,
		Description: o.Description,
		MemberCount: memberCount,
		CreatedAt:   timestamppb.New(o.CreatedAt),
		UpdatedAt:   timestamppb.New(o.UpdatedAt),
	}
	if currentRole != "" {
		proto.CurrentUserRole = stringToOrgRole(currentRole)
	}
	return proto
}

// Caller's membership role in an org, empty when anonymous or not a member
func (s *OrganizationService) currentRole(ctx context.Context, orgID string) string {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return ""
	}
	member, _ := s.store.GetOrgMember(ctx, orgID, user.ID)
	if member == nil {
		return ""
	}
	return member.Role
}

func orgMemberToProto(m *storage.OrgMember) *v1.OrgMember {
	proto := &v1.OrgMember{
		UserId:   m.UserID,
		Role:     stringToOrgRole(m.Role),
		JoinedAt: timestamppb.New(m.CreatedAt),
	}
	if m.User != nil {
		proto.Username = m.User.Username
	}
	return proto
}

func orgRoleToString(role v1.OrgRole) string {
	switch role {
	case v1.OrgRole_ORG_ROLE_OWNER:
		return storage.OrgRoleOwner
	case v1.OrgRole_ORG_ROLE_ADMIN:
		return storage.OrgRoleAdmin
	default:
		return storage.OrgRoleMember
	}
}

func stringToOrgRole(s string) v1.OrgRole {
	switch s {
	case storage.OrgRoleOwner:
		return v1.OrgRole_ORG_ROLE_OWNER
	case storage.OrgRoleAdmin:
		return v1.OrgRole_ORG_ROLE_ADMIN
	default:
		return v1.OrgRole_ORG_ROLE_MEMBER
	}
}
