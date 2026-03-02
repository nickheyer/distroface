package services

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

var _ distrofacev1connect.UserServiceHandler = (*UserService)(nil)

type UserService struct {
	store       *storage.Store
	authManager *auth.Manager
	enforcer    *rbac.Enforcer
	log         *logger.Logger
}

func NewUserService(store *storage.Store, manager *auth.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *UserService {
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

	roleNames, _ := s.store.GetUserRoleNames(ctx, user.ID)

	return connect.NewResponse(&v1.GetUserResponse{
		User: userToProto(user, roleNames),
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

	roleNames, _ := s.store.GetUserRoleNames(ctx, user.ID)

	return connect.NewResponse(&v1.UpdateUserResponse{
		User: userToProto(user, roleNames),
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
	limit, offset := parsePagination(req.Msg.PageSize, req.Msg.PageToken)

	users, total, err := s.store.ListUsers(ctx, req.Msg.Query, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoUsers := make([]*v1.User, len(users))
	for i, u := range users {
		roleNames, _ := s.store.GetUserRoleNames(ctx, u.ID)
		protoUsers[i] = userToProto(u, roleNames)
	}

	return connect.NewResponse(&v1.ListUsersResponse{
		Users:         protoUsers,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    int32(total),
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

	if msg.Email != nil {
		user.Email = msg.Email
	}
	if msg.IsActive != nil {
		user.IsActive = *msg.IsActive
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Update roles if provided
	if len(msg.Roles) > 0 {
		// Get current roles
		currentRoles, _ := s.store.GetUserRoleNames(ctx, user.ID)
		currentSet := make(map[string]bool)
		for _, r := range currentRoles {
			currentSet[r] = true
		}
		newSet := make(map[string]bool)
		for _, r := range msg.Roles {
			newSet[r] = true
		}

		// Unassign removed roles
		for _, r := range currentRoles {
			if !newSet[r] {
				_ = s.store.UnassignRole(ctx, user.ID, r)
			}
		}
		// Assign new roles
		for _, r := range msg.Roles {
			if !currentSet[r] {
				_ = s.store.AssignRole(ctx, user.ID, r, "local")
			}
		}
	}

	roleNames, _ := s.store.GetUserRoleNames(ctx, user.ID)

	return connect.NewResponse(&v1.AdminUpdateUserResponse{
		User: userToProto(user, roleNames),
	}), nil
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
