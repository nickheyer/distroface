package settings

import (
	"context"
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

type memStore struct {
	rows      map[string]string
	portalOrg map[string]string
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]string{}, portalOrg: map[string]string{}}
}

func key(scope v1.SettingsScopeType, id string) string {
	return scope.String() + "|" + id
}

func (m *memStore) GetSettingsValue(_ context.Context, scope v1.SettingsScopeType, id string) (string, bool, error) {
	v, ok := m.rows[key(scope, id)]
	return v, ok, nil
}

func (m *memStore) SetSettingsValue(_ context.Context, scope v1.SettingsScopeType, id, value string) error {
	m.rows[key(scope, id)] = value
	return nil
}

func (m *memStore) DeleteSettingsValue(_ context.Context, scope v1.SettingsScopeType, id string) error {
	delete(m.rows, key(scope, id))
	return nil
}

func (m *memStore) GetPortalOrgID(_ context.Context, portalID string) (string, error) {
	return m.portalOrg[portalID], nil
}

func provFor(list []*v1.FieldProvenance, path string) v1.SettingsTier {
	for _, p := range list {
		if p.Path == path {
			return p.Tier
		}
	}
	return v1.SettingsTier_SETTINGS_TIER_UNSPECIFIED
}

func TestDefaultsResolve(t *testing.T) {
	r := NewResolver(newMemStore(), nil)
	eff, prov, err := r.Effective(t.Context(), v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if err != nil {
		t.Fatal(err)
	}
	if eff.GetAuth().GetSessionTimeoutSeconds() != 86400 {
		t.Fatalf("expected default session timeout, got %d", eff.GetAuth().GetSessionTimeoutSeconds())
	}
	if got := provFor(prov, "auth.session_timeout_seconds"); got != v1.SettingsTier_SETTINGS_TIER_DEFAULT {
		t.Fatalf("expected default tier, got %v", got)
	}
}

func TestTierPrecedence(t *testing.T) {
	store := newMemStore()
	store.portalOrg["p1"] = "o1"
	r := NewResolver(store, nil)
	ctx := t.Context()

	sys := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM
	org := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG
	portal := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL

	patch := &v1.Settings{Acme: &v1.ACMESettings{Email: proto.String("sys@x.com")}}
	if _, err := r.Update(ctx, sys, "", patch, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}
	patch = &v1.Settings{Acme: &v1.ACMESettings{Email: proto.String("org@x.com")}}
	if _, err := r.Update(ctx, org, "o1", patch, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}

	eff, prov, err := r.Effective(ctx, portal, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if eff.GetAcme().GetEmail() != "org@x.com" {
		t.Fatalf("expected org override, got %q", eff.GetAcme().GetEmail())
	}
	if got := provFor(prov, "acme.email"); got != v1.SettingsTier_SETTINGS_TIER_ORG {
		t.Fatalf("expected org tier, got %v", got)
	}

	patch = &v1.Settings{Acme: &v1.ACMESettings{Email: proto.String("portal@x.com")}}
	if _, err := r.Update(ctx, portal, "p1", patch, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}
	eff, _, _ = r.Effective(ctx, portal, "p1")
	if eff.GetAcme().GetEmail() != "portal@x.com" {
		t.Fatalf("expected portal override, got %q", eff.GetAcme().GetEmail())
	}

	// Clearing the portal override falls back to org
	patch = &v1.Settings{}
	if _, err := r.Update(ctx, portal, "p1", patch, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}
	eff, _, _ = r.Effective(ctx, portal, "p1")
	if eff.GetAcme().GetEmail() != "org@x.com" {
		t.Fatalf("expected fallback to org, got %q", eff.GetAcme().GetEmail())
	}
}

func TestFilePins(t *testing.T) {
	pins := &v1.Settings{Acme: &v1.ACMESettings{DirectoryUrl: proto.String("https://internal-ca/dir")}}
	r := NewResolver(newMemStore(), pins)
	ctx := t.Context()
	sys := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM

	if _, err := r.Update(ctx, sys, "",
		&v1.Settings{Acme: &v1.ACMESettings{DirectoryUrl: proto.String("https://evil/dir")}},
		[]string{"acme.directory_url"}); err == nil {
		t.Fatal("expected pinned path rejection")
	}

	eff, prov, _ := r.Effective(ctx, sys, "")
	if eff.GetAcme().GetDirectoryUrl() != "https://internal-ca/dir" {
		t.Fatalf("expected pinned value, got %q", eff.GetAcme().GetDirectoryUrl())
	}
	if got := provFor(prov, "acme.directory_url"); got != v1.SettingsTier_SETTINGS_TIER_FILE {
		t.Fatalf("expected file tier, got %v", got)
	}
	if len(r.LockedPaths()) != 1 || r.LockedPaths()[0] != "acme.directory_url" {
		t.Fatalf("unexpected locked paths %v", r.LockedPaths())
	}
}

func TestScopeRules(t *testing.T) {
	r := NewResolver(newMemStore(), nil)
	ctx := t.Context()
	org := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG

	if _, err := r.Update(ctx, org, "o1",
		&v1.Settings{Gc: &v1.GCSettings{Enabled: proto.Bool(true)}},
		[]string{"gc.enabled"}); err == nil {
		t.Fatal("expected org scope to reject gc settings")
	}
	if _, err := r.Update(ctx, org, "o1",
		&v1.Settings{Artifacts: &v1.ArtifactSettings{Retention: &v1.ArtifactRetentionSettings{MaxVersions: proto.Int32(3)}}},
		[]string{"artifacts.retention.max_versions"}); err != nil {
		t.Fatalf("expected org retention write to pass: %v", err)
	}
	if _, err := r.Update(ctx, org, "o1",
		&v1.Settings{}, []string{"bogus.path"}); err == nil {
		t.Fatal("expected unknown path rejection")
	}
}

func TestSubtreeMaskAndPrune(t *testing.T) {
	r := NewResolver(newMemStore(), nil)
	ctx := t.Context()
	sys := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM

	patch := &v1.Settings{Artifacts: &v1.ArtifactSettings{
		Retention: &v1.ArtifactRetentionSettings{Enabled: proto.Bool(true), MaxVersions: proto.Int32(2)},
	}}
	if _, err := r.Update(ctx, sys, "", patch, []string{"artifacts.retention"}); err != nil {
		t.Fatal(err)
	}
	eff, _, _ := r.Effective(ctx, sys, "")
	if !eff.GetArtifacts().GetRetention().GetEnabled() || eff.GetArtifacts().GetRetention().GetMaxVersions() != 2 {
		t.Fatal("subtree replace failed")
	}

	// Clearing the subtree prunes the stored row empty
	if _, err := r.Update(ctx, sys, "", &v1.Settings{}, []string{"artifacts.retention"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := r.Stored(ctx, sys, "")
	if proto.Size(stored) != 0 {
		t.Fatalf("expected pruned empty row, got %v", stored)
	}
}

func TestRedact(t *testing.T) {
	s := &v1.Settings{Auth: &v1.AuthSettings{Oidc: &v1.OIDCSettings{ClientSecret: proto.String("hunter2")}}}
	Redact(s)
	if s.GetAuth().GetOidc().GetClientSecret() != "" {
		t.Fatal("secret not stripped")
	}
	if !s.GetAuth().GetOidc().GetClientSecretSet() {
		t.Fatal("secret_set flag not set")
	}
}

func TestSeedSystemOnce(t *testing.T) {
	store := newMemStore()
	r := NewResolver(store, nil)
	ctx := t.Context()

	seed := &v1.Settings{Server: &v1.ServerSettings{PublicHostname: proto.String("reg.example.com")}}
	seeded, err := r.SeedSystem(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}
	if !seeded {
		t.Fatal("first seed reported ignored")
	}
	eff, _, _ := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if eff.GetServer().GetPublicHostname() != "reg.example.com" {
		t.Fatal("seed not applied")
	}

	// Second seed with different value must not overwrite
	r2 := NewResolver(store, nil)
	seeded, err = r2.SeedSystem(ctx, &v1.Settings{Server: &v1.ServerSettings{PublicHostname: proto.String("other")}})
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		t.Fatal("second seed reported applied")
	}
	eff, _, _ = r2.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if eff.GetServer().GetPublicHostname() != "reg.example.com" {
		t.Fatal("seed overwrote existing row")
	}
}
