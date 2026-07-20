package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func newOIDCTestHandler(t *testing.T, issuer string) (*OIDCHandler, *stores.Store) {
	t.Helper()
	store, err := stores.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	res := settings.NewResolver(store, &v1.Settings{
		Auth: &v1.AuthSettings{
			Oidc: &v1.OIDCSettings{
				Enabled:   proto.Bool(true),
				IssuerUri: proto.String(issuer),
			},
		},
	})
	return NewOIDCHandler(nil, store, res, nil, logger.New()), store
}

func TestFindOrCreateOIDCUserLinksByEmail(t *testing.T) {
	h, store := newOIDCTestHandler(t, "https://idp.example.com")
	ctx := context.Background()

	email := "nick@example.com"
	existing := &db.User{
		ID:           uuid.New().String(),
		Username:     "nick",
		Email:        &email,
		AuthProvider: "local",
		IsActive:     true,
	}
	if err := store.CreateUser(ctx, existing); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	user, err := h.findOrCreateOIDCUser(ctx, "sub-123", "nick", email)
	if err != nil {
		t.Fatalf("findOrCreateOIDCUser: %v", err)
	}
	if user.ID != existing.ID {
		t.Fatalf("created duplicate user %s, want link to %s", user.ID, existing.ID)
	}
	if user.OIDCSubject != "sub-123" || user.OIDCIssuer != "https://idp.example.com" {
		t.Fatalf("identity not linked, got subject %q issuer %q", user.OIDCSubject, user.OIDCIssuer)
	}
	if user.AuthProvider != "local" {
		t.Fatalf("auth provider changed to %q, password login would break", user.AuthProvider)
	}

	// Second login must hit the persisted subject not the email
	again, err := h.findOrCreateOIDCUser(ctx, "sub-123", "nick", email)
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if again.ID != existing.ID {
		t.Fatalf("second login resolved %s, want %s", again.ID, existing.ID)
	}
}

func TestFindOrCreateOIDCUserRelinksOnIssuerChange(t *testing.T) {
	h, store := newOIDCTestHandler(t, "https://new-idp.example.com")
	ctx := context.Background()

	email := "sam@example.com"
	existing := &db.User{
		ID:           uuid.New().String(),
		Username:     "sam",
		Email:        &email,
		AuthProvider: "oidc",
		OIDCSubject:  "old-sub",
		OIDCIssuer:   "https://old-idp.example.com",
		IsActive:     true,
	}
	if err := store.CreateUser(ctx, existing); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	user, err := h.findOrCreateOIDCUser(ctx, "new-sub", "sam", email)
	if err != nil {
		t.Fatalf("findOrCreateOIDCUser: %v", err)
	}
	if user.ID != existing.ID {
		t.Fatalf("created duplicate user %s, want relink of %s", user.ID, existing.ID)
	}
	if user.OIDCSubject != "new-sub" || user.OIDCIssuer != "https://new-idp.example.com" {
		t.Fatalf("identity not relinked, got subject %q issuer %q", user.OIDCSubject, user.OIDCIssuer)
	}
}

func TestFindOrCreateOIDCUserCreatesNew(t *testing.T) {
	h, _ := newOIDCTestHandler(t, "https://idp.example.com")
	ctx := context.Background()

	user, err := h.findOrCreateOIDCUser(ctx, "sub-9", "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("findOrCreateOIDCUser: %v", err)
	}
	if user.AuthProvider != "oidc" || user.OIDCSubject != "sub-9" || user.OIDCIssuer != "https://idp.example.com" {
		t.Fatalf("unexpected new user %+v", user)
	}
}

func TestFindOrCreateOIDCUserRejectsInactive(t *testing.T) {
	h, store := newOIDCTestHandler(t, "https://idp.example.com")
	ctx := context.Background()

	email := "bob@example.com"
	bob := &db.User{
		ID:           uuid.New().String(),
		Username:     "bob",
		Email:        &email,
		AuthProvider: "local",
	}
	if err := store.CreateUser(ctx, bob); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Gorm default true wins on insert deactivate after
	if err := store.BulkSetUsersActive(ctx, []string{bob.ID}, false); err != nil {
		t.Fatalf("BulkSetUsersActive: %v", err)
	}

	if _, err := h.findOrCreateOIDCUser(ctx, "sub-bob", "bob", email); !errors.Is(err, ErrUserNotActive) {
		t.Fatalf("got %v, want ErrUserNotActive", err)
	}
}
