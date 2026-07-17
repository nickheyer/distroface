package services

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/pagination"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

var _ distrofacev1connect.RoleServiceHandler = (*RoleService)(nil)

type RoleService struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
	log      *logger.Logger
}

func NewRoleService(store *stores.Store, enforcer *rbac.Enforcer, log *logger.Logger) *RoleService {
	return &RoleService{store: store, enforcer: enforcer, log: log}
}

func (s *RoleService) ListRoles(ctx context.Context, req *connect.Request[v1.ListRolesRequest]) (*connect.Response[v1.ListRolesResponse], error) {
	limit, offset := pagination.Parse(req.Msg.Page)
	q := pagination.ParseQuery(req.Msg.Page)
	if err := stores.RolesQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	roles, total, err := s.store.ListRoles(ctx, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoRoles := make([]*v1.Role, len(roles))
	for i, r := range roles {
		perms := s.enforcer.GetPermissionsForRole(r.Name)
		protoRoles[i] = roleToProto(r, perms)
	}

	return connect.NewResponse(&v1.ListRolesResponse{
		Roles: protoRoles,
		Page:  pagination.Info(offset, limit, total),
	}), nil
}

func (s *RoleService) GetRole(ctx context.Context, req *connect.Request[v1.GetRoleRequest]) (*connect.Response[v1.GetRoleResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	role, err := s.store.GetRole(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if role == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	perms := s.enforcer.GetPermissionsForRole(role.Name)

	return connect.NewResponse(&v1.GetRoleResponse{
		Role: roleToProto(role, perms),
	}), nil
}

func (s *RoleService) CreateRole(ctx context.Context, req *connect.Request[v1.CreateRoleRequest]) (*connect.Response[v1.CreateRoleResponse], error) {
	msg := req.Msg
	if msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	existing, _ := s.store.GetRoleByName(ctx, msg.Name)
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, nil)
	}

	role := &storage.Role{
		Name:        msg.Name,
		Description: msg.Description,
		IsDefault:   msg.IsDefault,
	}

	if err := s.store.CreateRole(ctx, role); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Set permissions if provided
	if len(msg.Permissions) > 0 {
		perms := protoToRBACPermissions(msg.Permissions)
		if err := s.enforcer.SetPermissionsForRole(msg.Name, perms); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	currentPerms := s.enforcer.GetPermissionsForRole(role.Name)

	return connect.NewResponse(&v1.CreateRoleResponse{
		Role: roleToProto(role, currentPerms),
	}), nil
}

func (s *RoleService) UpdateRole(ctx context.Context, req *connect.Request[v1.UpdateRoleRequest]) (*connect.Response[v1.UpdateRoleResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	role, err := s.store.GetRole(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if role == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if role.IsSystem {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot modify system role"))
	}

	oldName := role.Name
	if req.Msg.Name != nil && *req.Msg.Name != "" && *req.Msg.Name != oldName {
		existing, err := s.store.GetRoleByName(ctx, *req.Msg.Name)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if existing != nil {
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("role %q already exists", *req.Msg.Name))
		}
		role.Name = *req.Msg.Name
	}
	if req.Msg.Description != nil {
		role.Description = *req.Msg.Description
	}
	if req.Msg.IsDefault != nil {
		role.IsDefault = *req.Msg.IsDefault
	}

	if role.Name != oldName {
		// Repoint assignments and policies so nothing gets orphaned
		if err := s.store.RenameRole(ctx, role, oldName); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if err := s.enforcer.RenameRole(oldName, role.Name); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("role renamed but policy migration failed: %w", err))
		}
	} else if err := s.store.UpdateRole(ctx, role); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	perms := s.enforcer.GetPermissionsForRole(role.Name)

	return connect.NewResponse(&v1.UpdateRoleResponse{
		Role: roleToProto(role, perms),
	}), nil
}

func (s *RoleService) DeleteRole(ctx context.Context, req *connect.Request[v1.DeleteRoleRequest]) (*connect.Response[v1.DeleteRoleResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	role, err := s.store.GetRole(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if role == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	if role.IsSystem {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot delete system role"))
	}

	// Clear Casbin policies before deleting the DB record
	if err := s.enforcer.SetPermissionsForRole(role.Name, nil); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.store.DeleteRole(ctx, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.DeleteRoleResponse{}), nil
}

func (s *RoleService) GetPermissionMatrix(ctx context.Context, req *connect.Request[v1.GetPermissionMatrixRequest]) (*connect.Response[v1.GetPermissionMatrixResponse], error) {
	// Get resource actions
	entries := rbac.ResourceActions
	resourceActions := make([]*v1.ResourceActions, len(entries))
	for i, e := range entries {
		resourceActions[i] = &v1.ResourceActions{
			Resource: e.Resource,
			Actions:  e.Actions,
		}
	}

	// Casbin subjects are role names, the wire keys by role id
	allRoles, _, err := s.store.ListRoles(ctx, pagination.Query{}, 0, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	idByName := make(map[string]string, len(allRoles))
	for _, r := range allRoles {
		idByName[r.Name] = r.ID
	}

	matrix := s.enforcer.GetPermissionMatrix()
	rolePerms := make(map[string]*v1.RolePermissions)
	for role, perms := range matrix {
		id, ok := idByName[role]
		if !ok {
			continue
		}
		protoPerms := make([]*v1.Permission, len(perms))
		for i, p := range perms {
			protoPerms[i] = &v1.Permission{
				Resource: p.Resource,
				Action:   p.Action,
				ObjectId: p.ObjectID,
			}
		}
		rolePerms[id] = &v1.RolePermissions{Permissions: protoPerms}
	}

	return connect.NewResponse(&v1.GetPermissionMatrixResponse{
		ResourceActions: resourceActions,
		RolePermissions: rolePerms,
	}), nil
}

func (s *RoleService) ListScopeableObjects(ctx context.Context, req *connect.Request[v1.ListScopeableObjectsRequest]) (*connect.Response[v1.ListScopeableObjectsResponse], error) {
	limit, offset := pagination.Parse(req.Msg.Page)
	// Object pickers search free text only
	query := pagination.Query{Text: pagination.ParseQuery(req.Msg.Page).Text}

	var objects []*v1.ScopeableObject
	var total int64

	switch req.Msg.Resource {
	case rbac.ResourceRepositories:
		repos, t, err := s.store.ListRepositories(ctx, "", query, "", true, nil, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		total = t
		for _, r := range repos {
			fullName := r.Namespace + "/" + r.Name
			objects = append(objects, &v1.ScopeableObject{
				Id:          fullName,
				Name:        fullName,
				Resource:    rbac.ResourceRepositories,
				ScopeSource: rbac.ResourceRepositories,
			})
		}
	case rbac.ResourceArtifacts:
		repos, t, err := s.store.ListArtifactRepositories(ctx, stores.ArtifactRepoListOptions{
			IncludePrivate: true,
			Query:          query,
			Limit:          limit,
			Offset:         offset,
		})
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		total = t
		for _, r := range repos {
			fullName := r.Namespace + "/" + r.Name
			objects = append(objects, &v1.ScopeableObject{
				Id:          fullName,
				Name:        fullName,
				Resource:    rbac.ResourceArtifacts,
				ScopeSource: rbac.ResourceArtifacts,
			})
		}
	case rbac.ResourceOrganizations:
		orgs, t, err := s.store.ListOrganizations(ctx, "", true, query, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		total = t
		for _, o := range orgs {
			objects = append(objects, &v1.ScopeableObject{
				Id:          o.ID,
				Name:        o.Name,
				Resource:    rbac.ResourceOrganizations,
				ScopeSource: rbac.ResourceOrganizations,
			})
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("resource must be one of %s, %s, %s",
			rbac.ResourceRepositories, rbac.ResourceArtifacts, rbac.ResourceOrganizations))
	}

	return connect.NewResponse(&v1.ListScopeableObjectsResponse{
		Objects: objects,
		Page:    pagination.Info(offset, limit, total),
	}), nil
}

func (s *RoleService) UpdatePermissions(ctx context.Context, req *connect.Request[v1.UpdatePermissionsRequest]) (*connect.Response[v1.UpdatePermissionsResponse], error) {
	role, err := s.requireRole(ctx, req.Msg.RoleId)
	if err != nil {
		return nil, err
	}

	perms := protoToRBACPermissions(req.Msg.Permissions)
	if err := s.enforcer.SetPermissionsForRole(role.Name, perms); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UpdatePermissionsResponse{}), nil
}

// Resolves a role id, invalid argument when empty, not found when unknown
func (s *RoleService) requireRole(ctx context.Context, id string) (*storage.Role, error) {
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	role, err := s.store.GetRole(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if role == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("role %q does not exist", id))
	}
	return role, nil
}

func (s *RoleService) AssignRole(ctx context.Context, req *connect.Request[v1.AssignRoleRequest]) (*connect.Response[v1.AssignRoleResponse], error) {
	if req.Msg.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	role, err := s.requireRole(ctx, req.Msg.RoleId)
	if err != nil {
		return nil, err
	}

	if err := s.store.AssignRole(ctx, req.Msg.UserId, role.Name, "local"); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.AssignRoleResponse{}), nil
}

func (s *RoleService) UnassignRole(ctx context.Context, req *connect.Request[v1.UnassignRoleRequest]) (*connect.Response[v1.UnassignRoleResponse], error) {
	if req.Msg.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	role, err := s.requireRole(ctx, req.Msg.RoleId)
	if err != nil {
		return nil, err
	}

	if err := s.store.UnassignRole(ctx, req.Msg.UserId, role.Name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UnassignRoleResponse{}), nil
}

func (s *RoleService) GetUserRoles(ctx context.Context, req *connect.Request[v1.GetUserRolesRequest]) (*connect.Response[v1.GetUserRolesResponse], error) {
	if req.Msg.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	roles, err := s.store.GetUserRoles(ctx, req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.GetUserRolesResponse{
		Roles: roleRefsOf(roles),
	}), nil
}

func roleToProto(r *storage.Role, perms []rbac.Permission) *v1.Role {
	protoPerms := make([]*v1.Permission, len(perms))
	for i, p := range perms {
		protoPerms[i] = &v1.Permission{
			Resource: p.Resource,
			Action:   p.Action,
			ObjectId: p.ObjectID,
		}
	}

	return &v1.Role{
		Id:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		IsSystem:    r.IsSystem,
		IsDefault:   r.IsDefault,
		Permissions: protoPerms,
	}
}

func protoToRBACPermissions(protoPerms []*v1.Permission) []rbac.Permission {
	perms := make([]rbac.Permission, len(protoPerms))
	for i, p := range protoPerms {
		perms[i] = rbac.Permission{
			Resource: p.Resource,
			Action:   p.Action,
			ObjectID: p.ObjectId,
		}
	}
	return perms
}
