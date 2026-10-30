package certs

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

const sysScope = v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM

func TestValidateSettingsNeedsInstanceRoot(t *testing.T) {
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	e := newTestEngine(t, store, res, "", "")
	res.AddValidator(e.ValidateSettings)
	ctx := context.Background()

	acmeServer := &v1.Settings{Ca: &v1.CASettings{AcmeEnabled: proto.Bool(true)}}
	if _, err := res.Update(ctx, sysScope, "", acmeServer, []string{"ca.acme_enabled"}); err == nil || !strings.Contains(err.Error(), "instance ca") {
		t.Fatalf("expected root requirement, got %v", err)
	}
	rootSource := &v1.Settings{Tls: &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_APP_CA.Enum()}}
	if _, err := res.Update(ctx, sysScope, "", rootSource, []string{"tls.primary_source"}); err == nil || !strings.Contains(err.Error(), "instance ca") {
		t.Fatalf("expected root requirement, got %v", err)
	}

	rootPEM, rootKey, err := GenerateRootCA("root")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: v1.TLSScope_TLS_SCOPE_APP_CA, CertPEM: rootPEM, KeyPEM: rootKey,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := res.Update(ctx, sysScope, "", acmeServer, []string{"ca.acme_enabled"}); err != nil {
		t.Fatalf("with a root present: %v", err)
	}
	if _, err := res.Update(ctx, sysScope, "", rootSource, []string{"tls.primary_source"}); err != nil {
		t.Fatalf("with a root present: %v", err)
	}
}

func TestValidateSettingsChallengePortPortalClash(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()

	org := &storage.Organization{Name: "acme", CreatedBy: "t"}
	if err := store.CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRegistryPortal(ctx, &storage.RegistryPortal{
		OrgID: org.ID, Name: "edge", Hostname: "edge.example.com", Port: 9443, Rules: "[]",
		CertSource: v1.CertSource_CERT_SOURCE_NONE, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	res.AddValidator(e.ValidateSettings)

	clash := &v1.Settings{Acme: &v1.ACMESettings{ChallengePort: proto.String("9443")}}
	if _, err := res.Update(ctx, sysScope, "", clash, []string{"acme.challenge_port"}); err == nil || !strings.Contains(err.Error(), "edge") {
		t.Fatalf("expected portal clash, got %v", err)
	}
	free := &v1.Settings{Acme: &v1.ACMESettings{ChallengePort: proto.String("8081")}}
	if _, err := res.Update(ctx, sysScope, "", free, []string{"acme.challenge_port"}); err != nil {
		t.Fatalf("free port: %v", err)
	}
}

// Primary on acme against a directory nothing listens on
func newFailingACMEEngine(t *testing.T) *Engine {
	t.Helper()
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	seedSystem(t, res, &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String("registry.example.com:8443")},
		Tls:    &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_ACME.Enum()},
		Acme: &v1.ACMESettings{
			Enabled:       proto.Bool(true),
			Email:         proto.String("ops@example.com"),
			DirectoryUrl:  proto.String("http://127.0.0.1:1/directory"),
			ChallengePort: proto.String(""),
		},
	}, "server.public_hostname", "tls.primary_source", "acme.enabled", "acme.email", "acme.directory_url", "acme.challenge_port")
	e := newTestEngine(t, store, res, "", "")
	e.SetListenPort("8080")
	return e
}

func TestStatusExplainsFailedIssuance(t *testing.T) {
	e := newFailingACMEEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st := e.AppStatus(ctx)
	if st.State != v1.CertState_CERT_STATE_PENDING || !strings.Contains(st.Problems[0], "Issue certificate") {
		t.Fatalf("fresh acme source must read pending with a next step, got %v %v", st.State, st.Problems)
	}
	joined := strings.Join(st.Problems, "\n")
	for _, want := range []string{"tls-alpn-01", "8080", "8443"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hints must mention %q, got %q", want, joined)
		}
	}

	if _, err := e.EnsureCertificate(ctx, "registry.example.com"); err == nil {
		t.Fatal("expected the closed directory to fail issuance")
	}
	st = e.AppStatus(ctx)
	if st.State != v1.CertState_CERT_STATE_ERROR || !strings.Contains(st.Problems[0], "issuance failed") {
		t.Fatalf("failed order must surface first, got %v %v", st.State, st.Problems)
	}
	if e.AppCertReady(ctx) {
		t.Fatal("a failed order never reads ready")
	}
}

func TestIssueFailuresOnlyForConfiguredHosts(t *testing.T) {
	e := newFailingACMEEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Unconfigured hosts never pass policy, nothing remembered
	if _, err := e.EnsureCertificate(ctx, "scan.example.net"); err == nil {
		t.Fatal("unconfigured host must fail")
	}
	if _, ok := e.lastIssueFailure("scan.example.net"); ok {
		t.Fatal("unconfigured host must not be remembered")
	}
	if _, err := e.EnsureCertificate(ctx, "registry.example.com"); err == nil {
		t.Fatal("expected failure")
	}
	if _, ok := e.lastIssueFailure("registry.example.com"); !ok {
		t.Fatal("configured host failure must be remembered")
	}
}

func TestChallengeListenerFailureSurfaces(t *testing.T) {
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	_, port, _ := net.SplitHostPort(taken.Addr().String())
	seedSystem(t, res, &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String("registry.example.com")},
		Tls:    &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_ACME.Enum()},
		Acme: &v1.ACMESettings{
			Enabled:       proto.Bool(true),
			Email:         proto.String("ops@example.com"),
			DirectoryUrl:  proto.String("http://127.0.0.1:1/directory"),
			ChallengePort: proto.String(port),
		},
	}, "server.public_hostname", "tls.primary_source", "acme.enabled", "acme.email", "acme.directory_url", "acme.challenge_port")
	e := newTestEngine(t, store, res, "", "")
	defer e.Close()
	ctx := context.Background()

	e.ReconcileChallengeServer()
	if e.ChallengeListenerError() == "" {
		t.Fatal("bind failure must be recorded")
	}
	if joined := strings.Join(e.AppStatus(ctx).Problems, "\n"); !strings.Contains(joined, "http-01 listener") {
		t.Fatalf("status must explain the stopped listener, got %q", joined)
	}

	// Freeing the port and reconciling again heals it
	taken.Close()
	e.ReconcileChallengeServer()
	if err := e.ChallengeListenerError(); err != "" {
		t.Fatalf("listener should start once the port is free, got %q", err)
	}
}

func TestHTTPSOnlyHintWhileNotReady(t *testing.T) {
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	seedSystem(t, res, &v1.Settings{
		Tls: &v1.TLSSettings{Mode: v1.TLSMode_TLS_MODE_HTTPS_ONLY.Enum()},
	}, "tls.mode")
	e := newTestEngine(t, store, res, "", "")
	st := e.AppStatus(context.Background())
	if st.State != v1.CertState_CERT_STATE_NONE {
		t.Fatalf("config source without a pair reads none, got %v", st.State)
	}
	if joined := strings.Join(st.Problems, "\n"); !strings.Contains(joined, "cleartext keeps working") {
		t.Fatalf("https only must explain the deferred redirect, got %q", joined)
	}
}
