package container

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
)

func TestSeedPoliciesRunsOnce(t *testing.T) {
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
	log := logger.New()

	if err := seedPolicies(ctx, store, enforcer, log); err != nil {
		t.Fatalf("seedPolicies: %v", err)
	}
	if got := len(enforcer.GetPermissionsForRole("anonymous")); got != 4 {
		t.Fatalf("first seed gave anonymous %d grants, want 4", got)
	}

	// Admin strips the anonymous role, restarts must not restore it
	if err := enforcer.SetPermissionsForRole("anonymous", nil); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	if err := seedPolicies(ctx, store, enforcer, log); err != nil {
		t.Fatalf("seedPolicies again: %v", err)
	}
	if got := enforcer.GetPermissionsForRole("anonymous"); len(got) != 0 {
		t.Fatalf("second seed restored anonymous grants %+v", got)
	}

	// Partial removals survive too
	if err := enforcer.SetPermissionsForRole("user", []rbac.Permission{
		{Resource: rbac.ResourceRepositories, Action: rbac.ActionRead, ObjectID: "*"},
	}); err != nil {
		t.Fatalf("SetPermissionsForRole: %v", err)
	}
	if err := seedPolicies(ctx, store, enforcer, log); err != nil {
		t.Fatalf("seedPolicies third: %v", err)
	}
	if got := enforcer.GetPermissionsForRole("user"); len(got) != 1 {
		t.Fatalf("backfill re-added user grants %+v", got)
	}

	if perms := enforcer.GetPermissionsForRole("admin"); len(perms) != 1 {
		t.Fatalf("admin policy = %+v", perms)
	}
}
