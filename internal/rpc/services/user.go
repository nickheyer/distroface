package services

import (
	"context"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"golang.org/x/crypto/bcrypt"
)

var _ distrofacev1connect.UserServiceHandler = (*UserService)(nil)

type UserService struct {
	store *storage.Store
	log   *logger.Logger
}

func NewUserService(store *storage.Store, log *logger.Logger) *UserService {
	return &UserService{store: store, log: log}
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

	return connect.NewResponse(&v1.GetUserResponse{
		User: userToProto(user),
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
		user.Email = *req.Msg.Email
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UpdateUserResponse{
		User: userToProto(user),
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

	user, err := s.store.GetUserByID(ctx, currentUser.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Msg.CurrentPassword)); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Msg.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	user.PasswordHash = string(hash)
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.ChangePasswordResponse{}), nil
}
