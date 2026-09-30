package rbac

import (
	"path/filepath"
	"testing"

	"github.com/nickheyer/distroface/internal/db/stores"
)

func newTestEnforcer(t *testing.T) *Enforcer {
	t.Helper()
	store, err := stores.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	e, err := NewEnforcer(store.DB())
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := e.SeedDefaultPolicies(); err != nil {
		t.Fatalf("SeedDefaultPolicies: %v", err)
	}
	return e
}

func TestHasScopedGrantIgnoresWildcards(t *testing.T) {
	e := newTestEnforcer(t)
	if err := e.SetPermissionsForRole("viewer", []Permission{
		{Resource: ResourceRepositories, Action: ActionRead, ObjectID: "acme/api"},
		{Resource: ResourceRepositories, Action: ActionPull, ObjectID: "acme"},
	}); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}

	viewer := []string{"viewer"}
	if !e.HasScopedGrant(viewer, ResourceRepositories, ActionRead, "acme/api") {
		t.Error("exact object grant must match")
	}
	if !e.HasScopedGrant(viewer, ResourceRepositories, ActionPull, "acme/api", "acme") {
		t.Error("namespace grant must match when offered as a candidate")
	}
	if e.HasScopedGrant(viewer, ResourceRepositories, ActionRead, "acme/other") {
		t.Error("unrelated object must not match")
	}
	if e.HasScopedGrant(viewer, ResourceRepositories, ActionPush, "acme/api") {
		t.Error("action without a grant must not match")
	}

	// Seeded user role holds wildcards only
	user := []string{"user"}
	if !e.HasPermission(user, ResourceRepositories, ActionRead) {
		t.Error("wildcard grants the capability")
	}
	if e.HasScopedGrant(user, ResourceRepositories, ActionRead, "acme/api") {
		t.Error("wildcard must never count as a scoped grant")
	}
}

func TestEnsureAdminPolicyIdempotent(t *testing.T) {
	e := newTestEnforcer(t)
	for range 3 {
		if err := e.EnsureAdminPolicy(); err != nil {
			t.Fatalf("EnsureAdminPolicy: %v", err)
		}
	}
	perms := e.GetPermissionsForRole("admin")
	if len(perms) != 1 || perms[0] != (Permission{Resource: "*", Action: "*", ObjectID: "*"}) {
		t.Fatalf("admin policy = %+v, want the single wildcard", perms)
	}
}

func TestSeedDefaultPoliciesAnonymousDefaults(t *testing.T) {
	e := newTestEnforcer(t)
	perms := e.GetPermissionsForRole("anonymous")
	if len(perms) != 4 {
		t.Fatalf("anonymous seed = %+v, want read and pull on repositories and artifacts", perms)
	}
	for _, p := range perms {
		if p.ObjectID != "*" || (p.Action != ActionRead && p.Action != ActionPull) {
			t.Errorf("unexpected anonymous grant %+v", p)
		}
	}
}
