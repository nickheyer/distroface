package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestUser(t *testing.T, store *Store, username string, email *string) *User {
	t.Helper()
	u := &User{
		ID:           uuid.New().String(),
		Username:     username,
		Email:        email,
		AuthProvider: "local",
		IsActive:     true,
	}
	if err := store.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(%s): %v", username, err)
	}
	return u
}

func TestAssignRoleValidatesRoleExists(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	user := createTestUser(t, store, "nick", nil)

	if err := store.AssignRole(ctx, user.ID, "does-not-exist", "local"); err == nil {
		t.Fatal("expected error assigning nonexistent role")
	}

	// Migrate seeds system roles so this must work
	if err := store.AssignRole(ctx, user.ID, "user", "local"); err != nil {
		t.Fatalf("AssignRole(user): %v", err)
	}

	roles, err := store.GetUserRoleNames(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserRoleNames: %v", err)
	}
	if len(roles) != 1 || roles[0] != "user" {
		t.Fatalf("roles = %v, want [user]", roles)
	}
}

func TestUnassignRoleBySourceKeepsOtherSources(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	user := createTestUser(t, store, "nick", nil)

	if err := store.AssignRole(ctx, user.ID, "user", "local"); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}

	// Removing with different source does nothing
	if err := store.UnassignRoleBySource(ctx, user.ID, "user", "oidc"); err != nil {
		t.Fatalf("UnassignRoleBySource: %v", err)
	}
	roles, _ := store.GetUserRoleNames(ctx, user.ID)
	if len(roles) != 1 {
		t.Fatalf("locally-assigned role was removed by oidc-source unassign: %v", roles)
	}

	if err := store.UnassignRoleBySource(ctx, user.ID, "user", "local"); err != nil {
		t.Fatalf("UnassignRoleBySource: %v", err)
	}
	roles, _ = store.GetUserRoleNames(ctx, user.ID)
	if len(roles) != 0 {
		t.Fatalf("roles = %v, want empty", roles)
	}
}

func TestRenameRoleRepointsUserRoles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	user := createTestUser(t, store, "nick", nil)

	role := &Role{Name: "builders", Description: "ci"}
	if err := store.CreateRole(ctx, role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err := store.AssignRole(ctx, user.ID, "builders", "local"); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}

	role.Name = "ci-builders"
	if err := store.RenameRole(ctx, role, "builders"); err != nil {
		t.Fatalf("RenameRole: %v", err)
	}

	roles, _ := store.GetUserRoleNames(ctx, user.ID)
	if len(roles) != 1 || roles[0] != "ci-builders" {
		t.Fatalf("roles = %v, want [ci-builders]", roles)
	}
}

func TestEmailUniqueIndex(t *testing.T) {
	store := newTestStore(t)
	email := "nick@example.com"
	createTestUser(t, store, "nick", &email)

	dup := &User{
		ID:           uuid.New().String(),
		Username:     "nick2",
		Email:        &email,
		AuthProvider: "local",
		IsActive:     true,
	}
	if err := store.CreateUser(context.Background(), dup); err == nil {
		t.Fatal("expected unique violation for duplicate email")
	}

	// Multiple users without email are fine
	createTestUser(t, store, "no-email-1", nil)
	createTestUser(t, store, "no-email-2", nil)
}

func TestCountOrgMembersByRole(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	owner := createTestUser(t, store, "owner", nil)
	member := createTestUser(t, store, "member", nil)

	org := &Organization{Name: "acme", DisplayName: "Acme", CreatedBy: owner.ID}
	if err := store.CreateOrganization(ctx, org); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := store.AddOrgMember(ctx, &OrgMember{OrgID: org.ID, UserID: owner.ID, Role: OrgRoleOwner}); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}
	if err := store.AddOrgMember(ctx, &OrgMember{OrgID: org.ID, UserID: member.ID, Role: OrgRoleMember}); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}

	owners, err := store.CountOrgMembersByRole(ctx, org.ID, OrgRoleOwner)
	if err != nil {
		t.Fatalf("CountOrgMembersByRole: %v", err)
	}
	if owners != 1 {
		t.Fatalf("owners = %d, want 1", owners)
	}
}
