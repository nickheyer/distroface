package db

import (
	"context"
	"testing"
)

// Same repo name is allowed across namespaces, blocked within one
func TestArtifactRepoNamespaceUniqueness(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	a := &ArtifactRepository{Namespace: "alice", Name: "tools", OwnerID: "u1"}
	if err := store.CreateArtifactRepository(ctx, a); err != nil {
		t.Fatalf("create alice/tools: %v", err)
	}
	b := &ArtifactRepository{Namespace: "bob", Name: "tools", OwnerID: "u2"}
	if err := store.CreateArtifactRepository(ctx, b); err != nil {
		t.Fatalf("create bob/tools should succeed across namespaces: %v", err)
	}
	dup := &ArtifactRepository{Namespace: "alice", Name: "tools", OwnerID: "u1"}
	if err := store.CreateArtifactRepository(ctx, dup); err == nil {
		t.Fatal("duplicate alice/tools must violate the composite unique index")
	}

	got, err := store.GetArtifactRepository(ctx, "bob", "tools")
	if err != nil || got == nil || got.ID != b.ID {
		t.Fatalf("GetArtifactRepository(bob, tools) = %v, %v", got, err)
	}
	if miss, _ := store.GetArtifactRepository(ctx, "carol", "tools"); miss != nil {
		t.Fatal("GetArtifactRepository(carol, tools) should be nil")
	}
}

func TestOrgSettingCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	org := &Organization{Name: "acme", CreatedBy: "test"}
	if err := store.CreateOrganization(ctx, org); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	oid := org.ID

	if _, found, err := store.GetOrgSetting(ctx, oid, "k"); err != nil || found {
		t.Fatalf("missing setting: found=%v err=%v", found, err)
	}

	if err := store.SetOrgSetting(ctx, oid, "k", "v1"); err != nil {
		t.Fatalf("SetOrgSetting: %v", err)
	}
	v, found, err := store.GetOrgSetting(ctx, oid, "k")
	if err != nil || !found || v != "v1" {
		t.Fatalf("GetOrgSetting = %q, %v, %v", v, found, err)
	}

	// Save upserts on the composite key
	if err := store.SetOrgSetting(ctx, oid, "k", "v2"); err != nil {
		t.Fatalf("SetOrgSetting upsert: %v", err)
	}
	if v, _, _ := store.GetOrgSetting(ctx, oid, "k"); v != "v2" {
		t.Fatalf("upsert value = %q, want v2", v)
	}

	store.SetOrgSetting(ctx, oid, "k2", "x")
	kv, err := store.ListOrgSettings(ctx, oid)
	if err != nil || len(kv) != 2 || kv["k"] != "v2" || kv["k2"] != "x" {
		t.Fatalf("ListOrgSettings = %v, %v", kv, err)
	}

	if err := store.DeleteOrgSetting(ctx, oid, "k"); err != nil {
		t.Fatalf("DeleteOrgSetting: %v", err)
	}
	if _, found, _ := store.GetOrgSetting(ctx, oid, "k"); found {
		t.Fatal("deleted setting still present")
	}

	// Settings cascade with the org
	if _, err := store.DeleteOrganization(ctx, oid, "acme"); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}
	if kv, _ := store.ListOrgSettings(ctx, oid); len(kv) != 0 {
		t.Fatalf("org settings should cascade on org delete, got %v", kv)
	}
}

// Namespace backfill rewrites scoped casbin grants to namespaced form
func TestBackfillRewritesArtifactGrants(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	alice := createTestUser(t, store, "alice", nil)
	repo := &ArtifactRepository{Namespace: "alice", Name: "tools", OwnerID: alice.ID}
	if err := store.CreateArtifactRepository(ctx, repo); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Simulate a pre-namespace deployment, bare name and bare grant
	if err := store.db.Exec("UPDATE artifact_repositories SET namespace = '' WHERE id = ?", repo.ID).Error; err != nil {
		t.Fatalf("clear namespace: %v", err)
	}
	if err := store.db.Exec("CREATE TABLE IF NOT EXISTS casbin_rule (id INTEGER PRIMARY KEY AUTOINCREMENT, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT)").Error; err != nil {
		t.Fatalf("create casbin table: %v", err)
	}
	if err := store.db.Exec("INSERT INTO casbin_rule (ptype, v0, v1, v2, v3) VALUES ('p', 'ci-role', 'artifacts', 'read', 'tools')").Error; err != nil {
		t.Fatalf("insert grant: %v", err)
	}

	if err := store.backfillArtifactRepoNamespace(); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	got, err := store.GetArtifactRepository(ctx, "alice", "tools")
	if err != nil || got == nil {
		t.Fatalf("repo not namespaced to owner: %v %v", got, err)
	}
	var obj string
	if err := store.db.Raw("SELECT v3 FROM casbin_rule WHERE ptype = 'p' AND v1 = 'artifacts'").Scan(&obj).Error; err != nil {
		t.Fatalf("read grant: %v", err)
	}
	if obj != "alice/tools" {
		t.Fatalf("grant object = %q, want alice/tools", obj)
	}
}
