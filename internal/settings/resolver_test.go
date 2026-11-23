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

func (m *memStore) GetGitRepoOrgID(_ context.Context, _ string) (string, error) {
	return "", nil
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

func TestRedactACME(t *testing.T) {
	s := &v1.Settings{Acme: &v1.ACMESettings{
		AccountKey: proto.String("key"),
		EabHmacKey: proto.String("mac"),
		Dns:        &v1.ACMEDNSSettings{TsigSecret: proto.String("tsig"), ApiToken: proto.String("token")},
	}}
	Redact(s)
	acme := s.GetAcme()
	if acme.GetAccountKey() != "" || acme.GetEabHmacKey() != "" || acme.GetDns().GetTsigSecret() != "" || acme.GetDns().GetApiToken() != "" {
		t.Fatalf("acme secret not stripped: %v", acme)
	}
	if !acme.GetAccountKeySet() || !acme.GetEabHmacKeySet() || !acme.GetDns().GetTsigSecretSet() || !acme.GetDns().GetApiTokenSet() {
		t.Fatalf("acme set flags missing: %v", acme)
	}
}

// Credentials follow the directory and dns provider
func TestACMESealing(t *testing.T) {
	ctx := t.Context()
	store := newMemStore()
	store.portalOrg["p1"] = "o1"
	r := NewResolver(store, nil)
	sys, org := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG
	if _, err := r.Update(ctx, sys, "", &v1.Settings{Acme: &v1.ACMESettings{
		Email:      proto.String("sys@example.com"),
		AccountKey: proto.String("sys-key"),
		EabKeyId:   proto.String("sys-kid"),
		EabHmacKey: proto.String("sys-mac"),
		Challenge:  v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum(),
		Dns: &v1.ACMEDNSSettings{
			Provider:    v1.DNSProvider_DNS_PROVIDER_RFC2136.Enum(),
			Server:      proto.String("ns.sys.example"),
			TsigKeyName: proto.String("sys"),
			TsigSecret:  proto.String("sys-tsig"),
		},
	}}, []string{"acme.email", "acme.account_key", "acme.eab_key_id", "acme.eab_hmac_key", "acme.challenge",
		"acme.dns.provider", "acme.dns.server", "acme.dns.tsig_key_name", "acme.dns.tsig_secret"}); err != nil {
		t.Fatal(err)
	}

	// An org email alone keeps the instance account
	if _, err := r.Update(ctx, org, "o1", &v1.Settings{Acme: &v1.ACMESettings{Email: proto.String("org@example.com")}}, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}
	eff, _, err := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if a := eff.GetAcme(); a.GetAccountKey() != "sys-key" || a.GetDns().GetTsigSecret() != "sys-tsig" || a.GetEmail() != "org@example.com" {
		t.Fatalf("org email alone must inherit the rest: %v", a)
	}

	// Another directory or provider never reuses the instance credentials
	if _, err := r.Update(ctx, org, "o1", &v1.Settings{Acme: &v1.ACMESettings{
		DirectoryUrl: proto.String("https://ca.org.example/directory"),
		Dns: &v1.ACMEDNSSettings{
			Provider: v1.DNSProvider_DNS_PROVIDER_CLOUDFLARE.Enum(),
			ApiToken: proto.String("org-token"),
		},
	}}, []string{"acme.directory_url", "acme.dns.provider", "acme.dns.api_token"}); err != nil {
		t.Fatal(err)
	}
	eff, prov, err := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL, "p1")
	if err != nil {
		t.Fatal(err)
	}
	a := eff.GetAcme()
	if a.GetAccountKey() != "" || a.GetEabKeyId() != "" || a.GetEabHmacKey() != "" {
		t.Fatalf("instance account credentials leaked to another directory: %v", a)
	}
	if d := a.GetDns(); d.GetTsigSecret() != "" || d.GetServer() != "" || d.GetApiToken() != "org-token" {
		t.Fatalf("instance dns credentials leaked to another provider: %v", d)
	}
	if a.GetDns().GetPropagationTimeoutSeconds() == 0 {
		t.Fatal("an owned dns block lost the default propagation timeout")
	}
	if got := provFor(prov, "acme.dns.api_token"); got != v1.SettingsTier_SETTINGS_TIER_ORG {
		t.Fatalf("dns token provenance %v", got)
	}
	if got := provFor(prov, "acme.account_key"); got != v1.SettingsTier_SETTINGS_TIER_DEFAULT {
		t.Fatalf("dropped account key still credited to %v", got)
	}
}

// A relay and its sender belong to one tier
func TestMailSealing(t *testing.T) {
	ctx := t.Context()
	r := NewResolver(newMemStore(), nil)
	sys, org := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG
	if _, err := r.Update(ctx, sys, "", &v1.Settings{Mail: &v1.MailSettings{
		SmtpHost: proto.String("smtp.sys.example"),
		SmtpPort: proto.Int32(2525),
		Username: proto.String("sys-user"),
		Password: proto.String("sys-pass"),
	}}, []string{"mail.smtp_host", "mail.smtp_port", "mail.username", "mail.password"}); err != nil {
		t.Fatal(err)
	}

	// A sender without a relay cannot ride the instance relay
	if _, err := r.Update(ctx, org, "o1", &v1.Settings{Mail: &v1.MailSettings{FromName: proto.String("Org")}}, []string{"mail.from_name"}); err != nil {
		t.Fatal(err)
	}
	eff, prov, err := r.Effective(ctx, org, "o1")
	if err != nil {
		t.Fatal(err)
	}
	if m := eff.GetMail(); m.GetFromName() != "Distroface" || m.GetSmtpHost() != "smtp.sys.example" || m.GetPassword() != "sys-pass" {
		t.Fatalf("org sender applied without its own relay: %v", m)
	}
	if got := provFor(prov, "mail.from_name"); got != v1.SettingsTier_SETTINGS_TIER_DEFAULT {
		t.Fatalf("ignored sender credited to %v", got)
	}

	if _, err := r.Update(ctx, org, "o1", &v1.Settings{Mail: &v1.MailSettings{SmtpHost: proto.String("smtp.org.example")}}, []string{"mail.smtp_host"}); err != nil {
		t.Fatal(err)
	}
	eff, prov, err = r.Effective(ctx, org, "o1")
	if err != nil {
		t.Fatal(err)
	}
	m := eff.GetMail()
	if m.GetSmtpHost() != "smtp.org.example" || m.GetFromName() != "Org" {
		t.Fatalf("org relay lost its own values: %v", m)
	}
	if m.GetUsername() != "" || m.GetPassword() != "" || m.GetSmtpPort() != 587 {
		t.Fatalf("instance relay credentials leaked to the org relay: %v", m)
	}
	if got := provFor(prov, "mail.smtp_port"); got != v1.SettingsTier_SETTINGS_TIER_DEFAULT {
		t.Fatalf("reset port credited to %v", got)
	}
	if got := provFor(prov, "mail.from_name"); got != v1.SettingsTier_SETTINGS_TIER_ORG {
		t.Fatalf("org sender credited to %v", got)
	}
}

func TestSeedSystemOnce(t *testing.T) {
	store := newMemStore()
	r := NewResolver(store, nil)
	ctx := t.Context()

	seed := &v1.Settings{Server: &v1.ServerSettings{PublicHostname: proto.String("reg.example.com")}}
	if err := r.SeedSystem(ctx, seed); err != nil {
		t.Fatal(err)
	}
	eff, _, _ := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if eff.GetServer().GetPublicHostname() != "reg.example.com" {
		t.Fatal("seed not applied")
	}

	// Second seed with different value must not overwrite
	r2 := NewResolver(store, nil)
	if err := r2.SeedSystem(ctx, &v1.Settings{Server: &v1.ServerSettings{PublicHostname: proto.String("other")}}); err != nil {
		t.Fatal(err)
	}
	eff, _, _ = r2.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "")
	if eff.GetServer().GetPublicHostname() != "reg.example.com" {
		t.Fatal("seed overwrote existing row")
	}
}

func TestEffectiveCachedCopiesStayApart(t *testing.T) {
	r := NewResolver(newMemStore(), nil)
	ctx := t.Context()
	sys := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM

	first, _, err := r.Effective(ctx, sys, "")
	if err != nil {
		t.Fatal(err)
	}
	// A caller editing its copy must not poison later readers
	first.Acme = &v1.ACMESettings{Email: proto.String("edited@x.com")}
	second := r.System(ctx)
	if second.GetAcme().GetEmail() == "edited@x.com" {
		t.Fatal("cached settings leaked a caller's edit")
	}
	if _, ok := r.effective[scopeKey{sys, ""}]; !ok {
		t.Fatal("system scope should be cached after a read")
	}

	patch := &v1.Settings{Acme: &v1.ACMESettings{Email: proto.String("sys@x.com")}}
	if _, err := r.Update(ctx, sys, "", patch, []string{"acme.email"}); err != nil {
		t.Fatal(err)
	}
	if got := r.System(ctx).GetAcme().GetEmail(); got != "sys@x.com" {
		t.Fatalf("write must drop the cache, got %q", got)
	}
}

// Local writes announce themselves, remote refreshes rerun dependents without echoing
func TestBroadcastAndRefresh(t *testing.T) {
	store := newMemStore()
	r := NewResolver(store, nil)
	ctx := context.Background()
	announced, notified := 0, 0
	r.SetBroadcaster(func() { announced++ })
	r.Subscribe(func() { notified++ })

	if _, err := r.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", &v1.Settings{
		Auth: &v1.AuthSettings{AnonymousAccess: proto.Bool(true)},
	}, []string{"auth.anonymous_access"}); err != nil {
		t.Fatal(err)
	}
	if announced != 1 || notified != 1 {
		t.Fatalf("after update announced=%d notified=%d", announced, notified)
	}

	// Another instance flips the row underneath us
	peer := NewResolver(store, nil)
	if _, err := peer.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", &v1.Settings{
		Auth: &v1.AuthSettings{AnonymousAccess: proto.Bool(false)},
	}, []string{"auth.anonymous_access"}); err != nil {
		t.Fatal(err)
	}
	if !r.System(ctx).GetAuth().GetAnonymousAccess() {
		t.Fatal("stale cache expected before refresh")
	}
	r.Refresh()
	if r.System(ctx).GetAuth().GetAnonymousAccess() {
		t.Error("refresh kept the stale row")
	}
	if announced != 1 || notified != 2 {
		t.Errorf("after refresh announced=%d notified=%d", announced, notified)
	}
}
