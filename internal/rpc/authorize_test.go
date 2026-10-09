package rpc

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

func newAuthorizeServer(t *testing.T) *Server {
	t.Helper()
	store, err := stores.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := enforcer.SeedDefaultPolicies(); err != nil {
		t.Fatalf("SeedDefaultPolicies: %v", err)
	}
	return &Server{ServerDeps: ServerDeps{Store: store, Enforcer: enforcer, Log: logger.New()}}
}

func anonymousUser() *auth.AuthenticatedUser {
	return &auth.AuthenticatedUser{ID: "anonymous", Username: "anonymous", Roles: []string{"anonymous"}, Provider: "anonymous"}
}

func TestAuthorizeAnonymousBrowsing(t *testing.T) {
	s := newAuthorizeServer(t)
	ctx := context.Background()
	list := distrofacev1connect.RepositoryServiceListRepositoriesProcedure
	req := &v1.ListRepositoriesRequest{}

	if err := s.authorize(ctx, req, anonymousUser(), list); err != nil {
		t.Fatalf("seeded anonymous role must list repositories: %v", err)
	}
	if err := s.authorize(ctx, req, nil, list); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("no identity must be unauthenticated, got %v", err)
	}

	if err := s.Enforcer.SetPermissionsForRole("anonymous", nil); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	if err := s.authorize(ctx, req, anonymousUser(), list); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("stripped anonymous role must be denied, got %v", err)
	}
	tags := &v1.ListTagsRequest{Namespace: "acme", Name: "app"}
	if err := s.authorize(ctx, tags, anonymousUser(), distrofacev1connect.RepositoryServiceListTagsProcedure); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("stripped anonymous role must not list tags, got %v", err)
	}

	// Scoped grants still admit the caller, the service filters visibility
	if err := s.Enforcer.SetPermissionsForRole("anonymous", []rbac.Permission{
		{Resource: rbac.ResourceRepositories, Action: rbac.ActionRead, ObjectID: "acme/app"},
	}); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	if err := s.authorize(ctx, req, anonymousUser(), list); err != nil {
		t.Fatalf("scoped read grant must admit listing: %v", err)
	}
}

func TestAuthorizeAnonymousTiers(t *testing.T) {
	s := newAuthorizeServer(t)
	ctx := context.Background()

	star := &v1.StarRepositoryRequest{Namespace: "acme", Name: "app"}
	if err := s.authorize(ctx, star, anonymousUser(), distrofacev1connect.RepositoryServiceStarRepositoryProcedure); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("account only rpc must refuse anonymous, got %v", err)
	}
	if err := s.authorize(ctx, star, &auth.AuthenticatedUser{ID: "u1", Username: "alice", Roles: []string{"user"}, Provider: "local"},
		distrofacev1connect.RepositoryServiceStarRepositoryProcedure); err != nil {
		t.Fatalf("account passes account only rpc: %v", err)
	}

	settings := &v1.GetSettingsRequest{}
	if err := s.authorize(ctx, settings, anonymousUser(), distrofacev1connect.SettingsServiceGetSettingsProcedure); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("stored settings must refuse anonymous, got %v", err)
	}

	user := &v1.GetUserRequest{Username: "alice"}
	if err := s.authorize(ctx, user, anonymousUser(), distrofacev1connect.UserServiceGetUserProcedure); err != nil {
		t.Fatalf("identity rpc admits anonymous: %v", err)
	}
}

func TestAuthorizeSearchUsersIsAccountTier(t *testing.T) {
	s := newAuthorizeServer(t)
	ctx := context.Background()
	search := &v1.SearchUsersRequest{}
	proc := distrofacev1connect.UserServiceSearchUsersProcedure

	if err := s.authorize(ctx, search, anonymousUser(), proc); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous must not search users, got %v", err)
	}
	member := &auth.AuthenticatedUser{ID: "u1", Username: "alice", Roles: []string{"user"}, Provider: "local"}
	if err := s.authorize(ctx, search, member, proc); err != nil {
		t.Fatalf("plain account searches users without users/read: %v", err)
	}
	list := &v1.ListUsersRequest{}
	if err := s.authorize(ctx, list, member, distrofacev1connect.UserServiceListUsersProcedure); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("admin listing still needs users/read, got %v", err)
	}
}
