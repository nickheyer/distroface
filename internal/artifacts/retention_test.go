package artifacts

import (
	"context"
	"fmt"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/config"
)

// Total size cap prunes oldest unprotected artifacts, keeps newest
func TestRetentionMaxTotalSize(t *testing.T) {
	e := newTestEnv(t, config.ArtifactRetentionConfig{
		Enabled:       true,
		MaxTotalSize:  10, // bytes
		ExcludeLatest: true,
	})
	token := e.newUser("alice", "user")
	e.doJSON("POST", "/api/v1/artifacts/repos", token, map[string]any{"name": "cap"})

	// Four 4-byte versions of one path, total 16 bytes over the 10 byte cap
	for i := 1; i <= 4; i++ {
		e.uploadArtifact(token, "cap", fmt.Sprintf("%d.0", i), "app.bin", fmt.Sprintf("dat%d", i), nil)
	}

	ctx := context.Background()
	repo := e.repoByName("cap")
	list, _, err := e.store.ListArtifacts(ctx, repo.ID, "", 0, 0)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}

	var total int64
	newestKept := false
	for _, a := range list {
		total += a.Size
		if a.Version == "4.0" {
			newestKept = true
		}
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 artifacts after size cap, got %d", len(list))
	}
	if total > 10 {
		t.Fatalf("total size %d exceeds cap 10", total)
	}
	if !newestKept {
		t.Fatal("newest version 4.0 must survive under ExcludeLatest")
	}
}

// Per-org OrgSetting overrides the global retention default
func TestEffectiveRetentionOrgOverride(t *testing.T) {
	e := newTestEnv(t, config.ArtifactRetentionConfig{
		Enabled:     false, // global default off
		MaxVersions: 5,
	})
	ctx := context.Background()

	org := &storage.Organization{Name: "acme", DisplayName: "Acme", CreatedBy: "test"}
	if err := e.store.CreateOrganization(ctx, org); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	// Personal namespace resolves to the global default
	if p := e.manager.EffectiveRetention(ctx, "alice"); p.Enabled {
		t.Fatal("personal namespace should inherit global (disabled) retention")
	}

	// Org overrides enable retention and tighten max_versions
	if err := e.store.SetOrgSetting(ctx, org.ID, SettingRetentionEnabled, "true"); err != nil {
		t.Fatalf("SetOrgSetting: %v", err)
	}
	if err := e.store.SetOrgSetting(ctx, org.ID, SettingRetentionMaxVersions, "2"); err != nil {
		t.Fatalf("SetOrgSetting: %v", err)
	}

	p := e.manager.EffectiveRetention(ctx, "acme")
	if !p.Enabled || p.MaxVersions != 2 {
		t.Fatalf("org override not applied: enabled=%v max_versions=%d", p.Enabled, p.MaxVersions)
	}
}
