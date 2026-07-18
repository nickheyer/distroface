package services

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

var _ distrofacev1connect.UserServiceHandler = (*UserService)(nil)

type UserService struct {
	store       *stores.Store
	authManager *auth.Manager
	enforcer    *rbac.Enforcer
	log         *logger.Logger
}

func NewUserService(store *stores.Store, manager *auth.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *UserService {
	return &UserService{store: store, authManager: manager, enforcer: enforcer, log: log}
}

func (s *UserService) GetUser(ctx context.Context, req *connect.Request[v1.GetUserRequest]) (*connect.Response[v1.GetUserResponse], error) {
	if req.Msg.Username == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	user, err := s.store.GetUserByUsername(ctx, req.Msg.Username)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	// Only self or users read permission sees private fields
	caller := auth.UserFromContext(ctx)
	privileged := false
	if caller != nil {
		if caller.ID == user.ID {
			privileged = true
		} else if s.enforcer != nil {
			privileged, _ = s.enforcer.Enforce(caller.Roles, rbac.ResourceUsers, rbac.ActionRead, "*")
		}
	}

	if !privileged {
		return connect.NewResponse(&v1.GetUserResponse{
			User: publicUserToProto(user),
		}), nil
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)

	return connect.NewResponse(&v1.GetUserResponse{
		User: userToProto(user, roles),
	}), nil
}

func (s *UserService) UpdateUser(ctx context.Context, req *connect.Request[v1.UpdateUserRequest]) (*connect.Response[v1.UpdateUserResponse], error) {
	currentUser := auth.UserFromContext(ctx)
	if currentUser == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	user, err := s.store.GetUserByID(ctx, currentUser.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if req.Msg.DisplayName != nil {
		user.DisplayName = *req.Msg.DisplayName
	}
	if req.Msg.Email != nil {
		existing, err := s.store.GetUserByEmail(ctx, *req.Msg.Email)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if existing != nil && existing.ID != user.ID {
			return nil, connect.NewError(connect.CodeAlreadyExists, nil)
		}
		user.Email = req.Msg.Email
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)

	return connect.NewResponse(&v1.UpdateUserResponse{
		User: userToProto(user, roles),
	}), nil
}

func (s *UserService) ChangePassword(ctx context.Context, req *connect.Request[v1.ChangePasswordRequest]) (*connect.Response[v1.ChangePasswordResponse], error) {
	currentUser := auth.UserFromContext(ctx)
	if currentUser == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if req.Msg.CurrentPassword == "" || req.Msg.NewPassword == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if len(req.Msg.NewPassword) < 8 {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	if err := s.authManager.ChangePassword(ctx, currentUser.ID, req.Msg.CurrentPassword, req.Msg.NewPassword); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&v1.ChangePasswordResponse{}), nil
}

func (s *UserService) ListUsers(ctx context.Context, req *connect.Request[v1.ListUsersRequest]) (*connect.Response[v1.ListUsersResponse], error) {
	limit, offset := pages.Parse(req.Msg.Page)
	q := pages.ParseQuery(req.Msg.Page)
	if err := stores.UsersQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	users, total, err := s.store.ListUsers(ctx, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoUsers := make([]*v1.User, len(users))
	for i, u := range users {
		roles, _ := s.store.GetUserRoles(ctx, u.ID)
		protoUsers[i] = userToProto(u, roles)
	}

	return connect.NewResponse(&v1.ListUsersResponse{
		Users: protoUsers,
		Page:  pages.Info(offset, limit, total),
	}), nil
}

func (s *UserService) AdminUpdateUser(ctx context.Context, req *connect.Request[v1.AdminUpdateUserRequest]) (*connect.Response[v1.AdminUpdateUserResponse], error) {
	msg := req.Msg
	if msg.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	user, err := s.store.GetUserByID(ctx, msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if msg.Email != nil && *msg.Email != "" {
		existing, err := s.store.GetUserByEmail(ctx, *msg.Email)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if existing != nil && existing.ID != user.ID {
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("email already in use"))
		}
	}
	if msg.Email != nil {
		user.Email = msg.Email
	}
	if msg.IsActive != nil {
		user.IsActive = *msg.IsActive
	}

	// Validate the requested role set before mutating anything
	newRoles, err := s.resolveRoleIDs(ctx, msg.RoleIds)
	if err != nil {
		return nil, err
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Update roles if provided
	if len(newRoles) > 0 {
		currentRoles, _ := s.store.GetUserRoleNames(ctx, user.ID)
		currentSet := make(map[string]bool)
		for _, r := range currentRoles {
			currentSet[r] = true
		}
		newSet := make(map[string]bool)
		for _, r := range newRoles {
			newSet[r.Name] = true
		}

		// Unassign removed roles
		for _, r := range currentRoles {
			if !newSet[r] {
				if err := s.store.UnassignRole(ctx, user.ID, r); err != nil {
					return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("unassign role %q: %w", r, err))
				}
			}
		}
		// Assign new roles
		for _, r := range newRoles {
			if !currentSet[r.Name] {
				if err := s.store.AssignRole(ctx, user.ID, r.Name, "local"); err != nil {
					return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("assign role %q: %w", r.Name, err))
				}
			}
		}
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)

	return connect.NewResponse(&v1.AdminUpdateUserResponse{
		User: userToProto(user, roles),
	}), nil
}

// Resolves role ids to rows, invalid argument on unknown ids
func (s *UserService) resolveRoleIDs(ctx context.Context, ids []string) ([]*storage.Role, error) {
	roles := make([]*storage.Role, 0, len(ids))
	for _, id := range ids {
		role, err := s.store.GetRole(ctx, id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if role == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("role %q does not exist", id))
		}
		roles = append(roles, role)
	}
	return roles, nil
}

func (s *UserService) AdminDeleteUser(ctx context.Context, req *connect.Request[v1.AdminDeleteUserRequest]) (*connect.Response[v1.AdminDeleteUserResponse], error) {
	if req.Msg.UserId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	// Don't allow deleting self
	currentUser := auth.UserFromContext(ctx)
	if currentUser != nil && currentUser.ID == req.Msg.UserId {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cannot delete your own account"))
	}

	if err := s.store.DeleteUser(ctx, req.Msg.UserId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.AdminDeleteUserResponse{}), nil
}

func (s *UserService) AdminCreateUser(ctx context.Context, req *connect.Request[v1.AdminCreateUserRequest]) (*connect.Response[v1.AdminCreateUserResponse], error) {
	msg := req.Msg
	if msg.Username == "" || msg.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("username and password are required"))
	}
	if !usernameRegex.MatchString(msg.Username) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid username"))
	}
	if len(msg.Password) < 8 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("password must be at least 8 characters"))
	}

	existing, err := s.store.GetUserByUsername(ctx, msg.Username)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("username already taken"))
	}
	if msg.Email != "" {
		existing, err = s.store.GetUserByEmail(ctx, msg.Email)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if existing != nil {
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("email already in use"))
		}
	}

	// Validate roles before mutating anything
	requestedRoles, err := s.resolveRoleIDs(ctx, msg.RoleIds)
	if err != nil {
		return nil, err
	}

	user, err := s.authManager.AdminCreateLocalUser(ctx, msg.Username, msg.Email, msg.DisplayName, msg.Password, msg.MustChangePassword)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if len(requestedRoles) > 0 {
		for _, r := range requestedRoles {
			if err := s.store.AssignRole(ctx, user.ID, r.Name, "local"); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("assign role %q: %w", r.Name, err))
			}
		}
	} else {
		defaultRoles, _ := s.store.GetDefaultRoles(ctx)
		for _, role := range defaultRoles {
			_ = s.store.AssignRole(ctx, user.ID, role.Name, "local")
		}
	}

	roles, _ := s.store.GetUserRoles(ctx, user.ID)

	return connect.NewResponse(&v1.AdminCreateUserResponse{
		User: userToProto(user, roles),
	}), nil
}

func (s *UserService) AdminBulkUpdateUsers(ctx context.Context, req *connect.Request[v1.AdminBulkUpdateUsersRequest]) (*connect.Response[v1.AdminBulkUpdateUsersResponse], error) {
	msg := req.Msg
	if len(msg.UserIds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no user ids provided"))
	}
	if msg.IsActive == nil && len(msg.AddRoleIds) == 0 && len(msg.RemoveRoleIds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no changes requested"))
	}

	// Validate roles before mutating anything
	addRoles, err := s.resolveRoleIDs(ctx, msg.AddRoleIds)
	if err != nil {
		return nil, err
	}
	removeRoles, err := s.resolveRoleIDs(ctx, msg.RemoveRoleIds)
	if err != nil {
		return nil, err
	}

	existing, err := s.store.FilterExistingUserIDs(ctx, msg.UserIds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	currentUser := auth.UserFromContext(ctx)
	resp := &v1.AdminBulkUpdateUsersResponse{}
	var targets []string
	for _, id := range msg.UserIds {
		if !existing[id] {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "user not found"})
			continue
		}
		// Deactivating your own account locks you out mid-session
		if msg.IsActive != nil && !*msg.IsActive && currentUser != nil && currentUser.ID == id {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "cannot deactivate your own account"})
			continue
		}
		targets = append(targets, id)
	}

	if msg.IsActive != nil {
		if err := s.store.BulkSetUsersActive(ctx, targets, *msg.IsActive); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	for _, id := range targets {
		for _, r := range addRoles {
			if err := s.store.AssignRole(ctx, id, r.Name, "local"); err != nil {
				resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: fmt.Sprintf("assign role %q failed", r.Name)})
			}
		}
		for _, r := range removeRoles {
			if err := s.store.UnassignRole(ctx, id, r.Name); err != nil {
				resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: fmt.Sprintf("remove role %q failed", r.Name)})
			}
		}
	}

	resp.UpdatedCount = int32(len(targets))
	return connect.NewResponse(resp), nil
}

func (s *UserService) AdminBulkDeleteUsers(ctx context.Context, req *connect.Request[v1.AdminBulkDeleteUsersRequest]) (*connect.Response[v1.AdminBulkDeleteUsersResponse], error) {
	if len(req.Msg.UserIds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no user ids provided"))
	}

	existing, err := s.store.FilterExistingUserIDs(ctx, req.Msg.UserIds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	currentUser := auth.UserFromContext(ctx)
	resp := &v1.AdminBulkDeleteUsersResponse{}
	var targets []string
	for _, id := range req.Msg.UserIds {
		if !existing[id] {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "user not found"})
			continue
		}
		if currentUser != nil && currentUser.ID == id {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "cannot delete your own account"})
			continue
		}
		targets = append(targets, id)
	}

	if err := s.store.BulkDeleteUsers(ctx, targets); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp.DeletedCount = int32(len(targets))
	return connect.NewResponse(resp), nil
}
