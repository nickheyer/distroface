package services

import (
	"context"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/config"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.AuthServiceHandler = (*AuthService)(nil)

var usernameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,38}[a-z0-9]$`)

type AuthService struct {
	store  *storage.Store
	log    *logger.Logger
	config *config.Config
}

func NewAuthService(store *storage.Store, cfg *config.Config, log *logger.Logger) *AuthService {
	return &AuthService{store: store, config: cfg, log: log}
}

func (s *AuthService) Register(ctx context.Context, req *connect.Request[v1.RegisterRequest]) (*connect.Response[v1.RegisterResponse], error) {
	msg := req.Msg

	if msg.Username == "" || msg.Email == "" || msg.Password == "" {
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

	existing, err = s.store.GetUserByEmail(ctx, msg.Email)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, nil)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(msg.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	count, err := s.store.CountUsers(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	user := &storage.User{
		ID:           uuid.New().String(),
		Username:     msg.Username,
		Email:        msg.Email,
		PasswordHash: string(hash),
		DisplayName:  msg.Username,
		IsAdmin:      count == 0,
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	token, session, err := s.createSession(ctx, user)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	_ = session

	return connect.NewResponse(&v1.RegisterResponse{
		User:         userToProto(user),
		SessionToken: token,
	}), nil
}

func (s *AuthService) Login(ctx context.Context, req *connect.Request[v1.LoginRequest]) (*connect.Response[v1.LoginResponse], error) {
	msg := req.Msg

	if msg.Identifier == "" || msg.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	user, err := s.store.GetUserByIdentifier(ctx, msg.Identifier)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(msg.Password)); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	token, session, err := s.createSession(ctx, user)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	_ = session

	return connect.NewResponse(&v1.LoginResponse{
		User:         userToProto(user),
		SessionToken: token,
	}), nil
}

func (s *AuthService) Logout(ctx context.Context, req *connect.Request[v1.LogoutRequest]) (*connect.Response[v1.LogoutResponse], error) {
	session := auth.SessionFromContext(ctx)
	if session == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	if err := s.store.DeleteSession(ctx, session.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.LogoutResponse{}), nil
}

func (s *AuthService) GetCurrentUser(ctx context.Context, req *connect.Request[v1.GetCurrentUserRequest]) (*connect.Response[v1.GetCurrentUserResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	return connect.NewResponse(&v1.GetCurrentUserResponse{
		User: userToProto(user),
	}), nil
}

func (s *AuthService) RefreshSession(ctx context.Context, req *connect.Request[v1.RefreshSessionRequest]) (*connect.Response[v1.RefreshSessionResponse], error) {
	session := auth.SessionFromContext(ctx)
	if session == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	duration := time.Duration(s.config.Auth.SessionDuration) * time.Second
	session.ExpiresAt = time.Now().UTC().Add(duration)
	if err := s.store.UpdateSession(ctx, session); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.RefreshSessionResponse{
		ExpiresAt: session.ExpiresAt.Unix(),
	}), nil
}

func (s *AuthService) createSession(ctx context.Context, user *storage.User) (string, *storage.Session, error) {
	token, err := auth.GenerateSessionToken()
	if err != nil {
		return "", nil, err
	}

	duration := time.Duration(s.config.Auth.SessionDuration) * time.Second
	session := &storage.Session{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		TokenHash: storage.HashToken(token),
		ExpiresAt: time.Now().UTC().Add(duration),
	}

	if err := s.store.CreateSession(ctx, session); err != nil {
		return "", nil, err
	}

	return token, session, nil
}

func userToProto(u *storage.User) *v1.User {
	role := v1.Role_ROLE_USER
	if u.IsAdmin {
		role = v1.Role_ROLE_ADMIN
	}
	return &v1.User{
		Id:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        role,
		CreatedAt:   timestamppb.New(u.CreatedAt),
		UpdatedAt:   timestamppb.New(u.UpdatedAt),
	}
}
