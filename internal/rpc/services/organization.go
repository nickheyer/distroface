package services

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.OrganizationServiceHandler = (*OrganizationService)(nil)

type OrganizationService struct {
	store    *storage.Store
	registry *registry.RegistryAccess
	enforcer *rbac.Enforcer
	log      *logger.Logger
}

func NewOrganizationService(store *storage.Store, registry *registry.RegistryAccess, enforcer *rbac.Enforcer, log *logger.Logger) *OrganizationService {
	return &OrganizationService{store: store, registry: registry, enforcer: enforcer, log: log}
}

func (s *OrganizationService) CreateOrganization(ctx context.Context, req *connect.Request[v1.CreateOrganizationRequest]) (*connect.Response[v1.CreateOrganizationResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	if msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if !usernameRegex.MatchString(msg.Name) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization name"))
	}

	// Org name must not collide with existing usernames
	existingUser, _ := s.store.GetUserByUsername(ctx, msg.Name)
	if existingUser != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("name already taken by a user"))
	}

	existingOrg, _ := s.store.GetOrganization(ctx, msg.Name)
	if existingOrg != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("organization already exists"))
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
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Creator becomes owner
	member := &storage.OrgMember{
		OrgID:  org.ID,
		UserID: user.ID,
		Role:   storage.OrgRoleOwner,
	}
	if err := s.store.AddOrgMember(ctx, member); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.CreateOrganizationResponse{
		Organization: orgToProto(org, 1),
	}), nil
}

func (s *OrganizationService) GetOrganization(ctx context.Context, req *connect.Request[v1.GetOrganizationRequest]) (*connect.Response[v1.GetOrganizationResponse], error) {
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	memberCount, _ := s.store.GetOrgMemberCount(ctx, org.ID)

	return connect.NewResponse(&v1.GetOrganizationResponse{
		Organization: orgToProto(org, int32(memberCount)),
	}), nil
}

func (s *OrganizationService) ListOrganizations(ctx context.Context, req *connect.Request[v1.ListOrganizationsRequest]) (*connect.Response[v1.ListOrganizationsResponse], error) {
	limit, offset := parsePagination(req.Msg.PageSize, req.Msg.PageToken)

	user := auth.UserFromContext(ctx)
	var userID string
	var canManage bool
	if user != nil {
		userID = user.ID
		canManage, _ = s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, "*")
	}

	orgs, total, err := s.store.ListOrganizations(ctx, userID, canManage, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoOrgs := make([]*v1.Organization, len(orgs))
	for i, o := range orgs {
		memberCount, _ := s.store.GetOrgMemberCount(ctx, o.ID)
		protoOrgs[i] = orgToProto(o, int32(memberCount))
	}

	return connect.NewResponse(&v1.ListOrganizationsResponse{
		Organizations: protoOrgs,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    int32(total),
	}), nil
}

func (s *OrganizationService) UpdateOrganization(ctx context.Context, req *connect.Request[v1.UpdateOrganizationRequest]) (*connect.Response[v1.UpdateOrganizationResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	// Users with manage permission on this org bypass membership checks
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.Name)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}
	}

	if req.Msg.DisplayName != nil {
		org.DisplayName = *req.Msg.DisplayName
	}
	if req.Msg.Description != nil {
		org.Description = *req.Msg.Description
	}

	if err := s.store.UpdateOrganization(ctx, org); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	memberCount, _ := s.store.GetOrgMemberCount(ctx, org.ID)

	return connect.NewResponse(&v1.UpdateOrganizationResponse{
		Organization: orgToProto(org, int32(memberCount)),
	}), nil
}

func (s *OrganizationService) DeleteOrganization(ctx context.Context, req *connect.Request[v1.DeleteOrganizationRequest]) (*connect.Response[v1.DeleteOrganizationResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	// Users with manage permission on this org bypass ownership check
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.Name)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if member == nil || member.Role != storage.OrgRoleOwner {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}
	}

	_, err = s.store.DeleteOrganization(ctx, org.ID, org.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Best-effort cleanup of registry storage
	if s.registry != nil {
		_ = s.registry.DeleteNamespace(org.Name)
	}

	return connect.NewResponse(&v1.DeleteOrganizationResponse{}), nil
}

func (s *OrganizationService) ListOrgMembers(ctx context.Context, req *connect.Request[v1.ListOrgMembersRequest]) (*connect.Response[v1.ListOrgMembersResponse], error) {
	if req.Msg.OrgName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.OrgName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	limit, offset := parsePagination(req.Msg.PageSize, req.Msg.PageToken)

	members, total, err := s.store.ListOrgMembers(ctx, org.ID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoMembers := make([]*v1.OrgMember, len(members))
	for i, m := range members {
		protoMembers[i] = orgMemberToProto(m)
	}

	return connect.NewResponse(&v1.ListOrgMembersResponse{
		Members:       protoMembers,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    int32(total),
	}), nil
}

func (s *OrganizationService) AddOrgMember(ctx context.Context, req *connect.Request[v1.AddOrgMemberRequest]) (*connect.Response[v1.AddOrgMemberResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.OrgName == "" || req.Msg.Username == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.OrgName)
	if err != nil || org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.Name)
	if !canManage {
		requesterMember, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if requesterMember == nil || (requesterMember.Role != storage.OrgRoleOwner && requesterMember.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}
	}

	targetUser, _ := s.store.GetUserByUsername(ctx, req.Msg.Username)
	if targetUser == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("user not found"))
	}

	// Check if already a member
	existing, _ := s.store.GetOrgMember(ctx, org.ID, targetUser.ID)
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("user is already a member"))
	}

	role := orgRoleToString(req.Msg.Role)
	member := &storage.OrgMember{
		OrgID:  org.ID,
		UserID: targetUser.ID,
		Role:   role,
	}

	if err := s.store.AddOrgMember(ctx, member); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	member.User = targetUser

	return connect.NewResponse(&v1.AddOrgMemberResponse{
		Member: orgMemberToProto(member),
	}), nil
}

func (s *OrganizationService) RemoveOrgMember(ctx context.Context, req *connect.Request[v1.RemoveOrgMemberRequest]) (*connect.Response[v1.RemoveOrgMemberResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.OrgName == "" || req.Msg.Username == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.OrgName)
	if err != nil || org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.Name)
	if !canManage {
		requesterMember, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if requesterMember == nil || (requesterMember.Role != storage.OrgRoleOwner && requesterMember.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}
	}

	targetUser, _ := s.store.GetUserByUsername(ctx, req.Msg.Username)
	if targetUser == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if err := s.store.RemoveOrgMember(ctx, org.ID, targetUser.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.RemoveOrgMemberResponse{}), nil
}

func (s *OrganizationService) UpdateOrgMemberRole(ctx context.Context, req *connect.Request[v1.UpdateOrgMemberRoleRequest]) (*connect.Response[v1.UpdateOrgMemberRoleResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.OrgName == "" || req.Msg.Username == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	org, err := s.store.GetOrganization(ctx, req.Msg.OrgName)
	if err != nil || org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.Name)
	if !canManage {
		requesterMember, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if requesterMember == nil || requesterMember.Role != storage.OrgRoleOwner {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}
	}

	targetUser, _ := s.store.GetUserByUsername(ctx, req.Msg.Username)
	if targetUser == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	member, _ := s.store.GetOrgMember(ctx, org.ID, targetUser.ID)
	if member == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("user is not a member"))
	}

	member.Role = orgRoleToString(req.Msg.Role)
	if err := s.store.UpdateOrgMember(ctx, member); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	member.User = targetUser

	return connect.NewResponse(&v1.UpdateOrgMemberRoleResponse{
		Member: orgMemberToProto(member),
	}), nil
}

func orgToProto(o *storage.Organization, memberCount int32) *v1.Organization {
	return &v1.Organization{
		Id:          o.ID,
		Name:        o.Name,
		DisplayName: o.DisplayName,
		Description: o.Description,
		MemberCount: memberCount,
		CreatedAt:   timestamppb.New(o.CreatedAt),
		UpdatedAt:   timestamppb.New(o.UpdatedAt),
	}
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
