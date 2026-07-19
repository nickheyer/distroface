package certs

import (
	"context"
	"crypto/tls"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func TestMTLSModeResolution(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()

	// Default off at system tier
	if m := e.mtlsModeForHost(ctx, nil); m != v1.MTLSMode_MTLS_MODE_OFF {
		t.Fatalf("expected off default, got %v", m)
	}

	// System tier turns it on for the app host
	seedSystem(t, res, &v1.Settings{Tls: &v1.TLSSettings{MtlsMode: v1.MTLSMode_MTLS_MODE_REQUIRED.Enum()}}, "tls.mtls_mode")
	if m := e.mtlsModeForHost(ctx, nil); m != v1.MTLSMode_MTLS_MODE_REQUIRED {
		t.Fatalf("expected required, got %v", m)
	}

	// Portal override wins over system
	if err := store.CreateOrganization(ctx, &storage.Organization{ID: "o1", Name: "acme", CreatedBy: "nick"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRegistryPortal(ctx, &storage.RegistryPortal{ID: "p1", OrgID: "o1", Name: "p", Hostname: "p.example.com", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := res.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL, "p1",
		&v1.Settings{Tls: &v1.TLSSettings{MtlsMode: v1.MTLSMode_MTLS_MODE_OPTIONAL.Enum()}}, []string{"tls.mtls_mode"}); err != nil {
		t.Fatal(err)
	}
	portal := &TLSPortal{ID: "p1", OrgID: "o1"}
	if m := e.mtlsModeForHost(ctx, portal); m != v1.MTLSMode_MTLS_MODE_OPTIONAL {
		t.Fatalf("expected portal override optional, got %v", m)
	}
}

func TestClientCAPool(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()

	// App host with no root ca fails
	if _, err := e.clientCAPool(ctx, nil); err == nil {
		t.Fatal("expected error without an app ca")
	}
	seedAppRoot(t, store)
	pool, err := e.clientCAPool(ctx, nil)
	if err != nil || pool == nil {
		t.Fatalf("app ca pool: %v", err)
	}

	// Portal host trusts its org ca
	if err := store.CreateOrganization(ctx, &storage.Organization{ID: "o1", Name: "acme", CreatedBy: "nick"}); err != nil {
		t.Fatal(err)
	}
	caPEM, keyPEM, _ := GenerateCA("org ca")
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: v1.TLSScope_TLS_SCOPE_ORG_CA, OrgID: "o1", CertPEM: caPEM, KeyPEM: keyPEM,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.clientCAPool(ctx, &TLSPortal{ID: "p1", OrgID: "o1"}); err != nil {
		t.Fatalf("org ca pool: %v", err)
	}
}

func TestConfigForClientModes(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	seedAppRoot(t, store)

	hello := &tls.ClientHelloInfo{ServerName: "registry.example.com"}

	// Off yields no per connection config
	cfg, err := e.configForClient(hello)
	if err != nil || cfg != nil {
		t.Fatalf("expected nil config when off, got %v %v", cfg, err)
	}

	// Required demands and verifies a client cert
	seedSystem(t, res, &v1.Settings{Tls: &v1.TLSSettings{MtlsMode: v1.MTLSMode_MTLS_MODE_REQUIRED.Enum()}}, "tls.mtls_mode")
	cfg, err = e.configForClient(hello)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("expected required client auth, got %+v", cfg)
	}
	if cfg.ClientCAs == nil {
		t.Fatal("expected a client ca pool")
	}

	// Optional verifies only when presented
	seedSystem(t, res, &v1.Settings{Tls: &v1.TLSSettings{MtlsMode: v1.MTLSMode_MTLS_MODE_OPTIONAL.Enum()}}, "tls.mtls_mode")
	cfg, _ = e.configForClient(hello)
	if cfg == nil || cfg.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("expected optional client auth, got %+v", cfg)
	}
}

func TestConfigForClientNoCADowngrades(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	// Required but no app ca must not brick the host
	seedSystem(t, res, &v1.Settings{Tls: &v1.TLSSettings{MtlsMode: v1.MTLSMode_MTLS_MODE_REQUIRED.Enum()}}, "tls.mtls_mode")
	cfg, err := e.configForClient(&tls.ClientHelloInfo{ServerName: "registry.example.com"})
	if err != nil || cfg != nil {
		t.Fatalf("expected downgrade to nil without a ca, got %v %v", cfg, err)
	}
}

func TestTrustBundlePEM(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	if e.TrustBundlePEM(context.Background()) != "" {
		t.Fatal("expected empty bundle without a root")
	}
	seedAppRoot(t, store)
	if e.TrustBundlePEM(context.Background()) == "" {
		t.Fatal("expected the root ca pem")
	}
}

func TestVerifiedIdentity(t *testing.T) {
	if VerifiedIdentity(nil) != nil {
		t.Fatal("nil state should yield nil identity")
	}
	// Unverified chain yields nothing even with a peer cert
	state := &tls.ConnectionState{}
	if VerifiedIdentity(state) != nil {
		t.Fatal("no peer cert should yield nil")
	}
	_ = proto.String // keep import parity with sibling tests
}
