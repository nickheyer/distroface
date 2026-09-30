package auth

import (
	"context"
	"path/filepath"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
)

type accessEnv struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
	access   *RepoAccess
	pub      *storage.Repository
	orgPriv  *storage.Repository
	userPriv *storage.Repository
}

func newAccessEnv(t *testing.T) *accessEnv {
	t.Helper()
	ctx := context.Background()
	store, err := stores.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := enforcer.SeedDefaultPolicies(); err != nil {
		t.Fatalf("SeedDefaultPolicies: %v", err)
	}

	for _, name := range []string{"alice", "bob", "carol"} {
		if err := store.CreateUser(ctx, &storage.User{ID: "u-" + name, Username: name, IsActive: true}); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}
	}
	org := &storage.Organization{Name: "acme", DisplayName: "Acme", CreatedBy: "u-alice"}
	if err := store.CreateOrganization(ctx, org); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := store.AddOrgMember(ctx, &storage.OrgMember{ID: "m-bob", OrgID: org.ID, UserID: "u-bob", Role: storage.OrgRoleMember}); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}

	env := &accessEnv{store: store, enforcer: enforcer, access: NewRepoAccess(store, enforcer)}
	env.pub = &storage.Repository{ID: "r-pub", Namespace: "acme", Name: "pub", OwnerID: org.ID, IsPrivate: false, IsOrgNamespace: true}
	env.orgPriv = &storage.Repository{ID: "r-priv", Namespace: "acme", Name: "app", OwnerID: org.ID, IsPrivate: true, IsOrgNamespace: true}
	env.userPriv = &storage.Repository{ID: "r-mine", Namespace: "alice", Name: "mine", OwnerID: "u-alice", IsPrivate: true}
	for _, r := range []*storage.Repository{env.pub, env.orgPriv, env.userPriv} {
		if err := store.CreateRepository(ctx, r); err != nil {
			t.Fatalf("CreateRepository %s: %v", r.Name, err)
		}
	}
	return env
}

func account(name string, roles ...string) *AuthenticatedUser {
	return &AuthenticatedUser{ID: "u-" + name, Username: name, Roles: roles, Provider: "local"}
}

func anonymous() *AuthenticatedUser {
	return &AuthenticatedUser{ID: "anonymous", Username: "anonymous", Roles: []string{"anonymous"}, Provider: "anonymous"}
}

func TestAnonymousFollowsRoleGrants(t *testing.T) {
	env := newAccessEnv(t)
	ctx := context.Background()

	if !env.access.Can(ctx, anonymous(), env.pub, rbac.ActionPull) {
		t.Error("seeded anonymous role must pull public repos")
	}
	if !env.access.CanSee(ctx, anonymous(), env.pub) {
		t.Error("seeded anonymous role must see public repos")
	}
	if env.access.CanSee(ctx, anonymous(), env.orgPriv) || env.access.Can(ctx, anonymous(), env.orgPriv, rbac.ActionPull) {
		t.Error("wildcard anonymous grants must never open private repos")
	}

	if err := env.enforcer.SetPermissionsForRole("anonymous", nil); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	if env.access.Can(ctx, anonymous(), env.pub, rbac.ActionPull) {
		t.Error("stripped anonymous role must not pull public repos")
	}
	if env.access.Can(ctx, anonymous(), env.pub, rbac.ActionRead) {
		t.Error("stripped anonymous role must not read public repos")
	}
}

func TestPrivateReposNeedRelationship(t *testing.T) {
	env := newAccessEnv(t)
	ctx := context.Background()

	carol := account("carol", "user")
	if env.access.CanSee(ctx, carol, env.orgPriv) || env.access.Can(ctx, carol, env.orgPriv, rbac.ActionPull) {
		t.Error("wildcard user grants must not open a private org repo")
	}
	if env.access.CanSee(ctx, carol, env.userPriv) {
		t.Error("wildcard user grants must not open another user's private repo")
	}

	alice := account("alice", "user")
	if !env.access.CanSee(ctx, alice, env.userPriv) || !env.access.HasRepoAccess(ctx, alice, env.userPriv, rbac.ActionDelete) {
		t.Error("owner keeps full access to their private repo")
	}

	bob := account("bob", "user")
	if !env.access.CanSee(ctx, bob, env.orgPriv) || !env.access.Can(ctx, bob, env.orgPriv, rbac.ActionPush) {
		t.Error("org member reads and pushes private org repos")
	}
	if env.access.HasRepoAccess(ctx, bob, env.orgPriv, rbac.ActionDelete) {
		t.Error("plain member must not delete org repos")
	}

	if err := env.enforcer.SetPermissionsForRole("viewer", []rbac.Permission{
		{Resource: rbac.ResourceRepositories, Action: rbac.ActionRead, ObjectID: "acme/app"},
		{Resource: rbac.ResourceRepositories, Action: rbac.ActionPull, ObjectID: "acme"},
	}); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	viewer := account("carol", "viewer")
	if !env.access.CanSee(ctx, viewer, env.orgPriv) {
		t.Error("scoped read grant opens the named private repo")
	}
	if !env.access.Can(ctx, viewer, env.orgPriv, rbac.ActionPull) {
		t.Error("namespace scoped pull grant opens private repos in that namespace")
	}
	if env.access.CanSee(ctx, viewer, env.userPriv) {
		t.Error("scoped grant must not leak into other repos")
	}

	if err := env.enforcer.SetPermissionsForRole("ops", []rbac.Permission{
		{Resource: rbac.ResourceRepositories, Action: rbac.ActionManage, ObjectID: "*"},
	}); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	if !env.access.CanSee(ctx, account("carol", "ops"), env.userPriv) {
		t.Error("manage grant sees every private repo")
	}
}

func TestPublicReposNeedCapability(t *testing.T) {
	env := newAccessEnv(t)
	ctx := context.Background()

	if env.access.Can(ctx, account("carol", "nothing"), env.pub, rbac.ActionPull) {
		t.Error("a role without pull must not pull public repos")
	}
	if !env.access.Can(ctx, account("bob", "nothing"), env.pub, rbac.ActionPull) {
		t.Error("org members pull their org's public repos regardless of role grants")
	}
	if !env.access.Can(ctx, account("carol", "user"), env.pub, rbac.ActionPull) {
		t.Error("seeded user role pulls public repos")
	}
	if env.access.Can(ctx, nil, env.pub, rbac.ActionPull) {
		t.Error("no identity never pulls")
	}
}
