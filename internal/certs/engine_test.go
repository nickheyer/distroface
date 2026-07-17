package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/crypto/acme/autocert"
)

func newTestStore(t *testing.T) *stores.Store {
	t.Helper()
	store, err := stores.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Server.Hostname = "registry.example.com:443"
	cfg.TLS.Enabled = true
	cfg.TLS.ACME.Enabled = true
	cfg.TLS.ACME.Domains = []string{"static.example.com"}
	cfg.TLS.ACME.RedirectHTTP = true
	return cfg
}

func newTestEngine(t *testing.T, cfg *config.Config, store *stores.Store) *Engine {
	t.Helper()
	log := logger.NewWithConfig(&logger.Config{Enabled: false})
	e, err := NewEngine(cfg, store, log)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// Self signed leaf plus key in autocert's cache pem layout
func selfSignedPEM(t *testing.T, domain string, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		Issuer:       pkix.Name{CommonName: "test-ca"},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	out = append(out, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	return out
}

func TestHostPolicy(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	e := newTestEngine(t, cfg, store)
	ctx := context.Background()

	orgID := "org-1"
	if err := store.CreateOrganization(ctx, &storage.Organization{ID: orgID, Name: "acme", CreatedBy: "nick"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCertificateDomain(ctx, &storage.CertificateDomain{
		Domain: "portal.example.com", Scope: storage.CertDomainScopeOrg, OrgID: &orgID, CreatedBy: "nick",
		Approved: true, ApprovedBy: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCertificateDomain(ctx, &storage.CertificateDomain{
		Domain: "pending.example.com", Scope: storage.CertDomainScopeOrg, OrgID: &orgID, CreatedBy: "nick",
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		host  string
		allow bool
	}{
		{"static.example.com", true},         // Config domain
		{"STATIC.example.com", true},         // Case insensitive
		{"registry.example.com", true},       // Primary hostname sans port
		{"portal.example.com", true},         // DB registered and approved
		{"pending.example.com", false},       // Registered but unapproved
		{"evil.example.com", false},          // Unknown
		{"registry.example.com.evil", false}, // Suffix trick
	}
	for _, c := range cases {
		err := e.hostPolicy(ctx, c.host)
		if c.allow && err != nil {
			t.Errorf("hostPolicy(%s) = %v, want allow", c.host, err)
		}
		if !c.allow && err == nil {
			t.Errorf("hostPolicy(%s) allowed, want deny", c.host)
		}
	}
}

func TestHostPolicySkipsUnissuablePrimary(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	cfg.Server.Hostname = "localhost:8080"
	e := newTestEngine(t, cfg, store)
	if err := e.hostPolicy(context.Background(), "localhost"); err == nil {
		t.Error("localhost primary hostname should not be issuable")
	}
}

func TestDBCacheRoundTrip(t *testing.T) {
	store := newTestStore(t)
	cache := dbCache{store: store}
	ctx := context.Background()

	if _, err := cache.Get(ctx, "missing"); err != autocert.ErrCacheMiss {
		t.Fatalf("Get(missing) = %v, want ErrCacheMiss", err)
	}
	if err := cache.Put(ctx, "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, "k", []byte("v2")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	data, err := cache.Get(ctx, "k")
	if err != nil || string(data) != "v2" {
		t.Fatalf("Get(k) = %q, %v", data, err)
	}
	if err := cache.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "k"); err != autocert.ErrCacheMiss {
		t.Fatalf("Get after delete = %v, want ErrCacheMiss", err)
	}
}

func TestCertificateInfo(t *testing.T) {
	store := newTestStore(t)
	e := newTestEngine(t, testConfig(), store)
	ctx := context.Background()

	expiry := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)
	if err := store.PutACMECacheEntry(ctx, "portal.example.com", selfSignedPEM(t, "portal.example.com", expiry)); err != nil {
		t.Fatal(err)
	}

	info, err := e.CertificateInfo(ctx, "portal.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Issued {
		t.Fatal("expected issued cert")
	}
	if info.Issuer != "test-ca" && info.Issuer != "portal.example.com" {
		t.Errorf("unexpected issuer %q", info.Issuer)
	}
	if len(info.SANs) != 1 || info.SANs[0] != "portal.example.com" {
		t.Errorf("unexpected sans %v", info.SANs)
	}
	if !info.NotAfter.Equal(expiry) && info.NotAfter.Unix() != expiry.Unix() {
		t.Errorf("NotAfter = %v, want %v", info.NotAfter, expiry)
	}

	missing, err := e.CertificateInfo(ctx, "nocert.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Issued {
		t.Error("expected unissued for uncached domain")
	}
}

func TestManualFallback(t *testing.T) {
	store := newTestStore(t)
	dir := t.TempDir()
	certPEM := selfSignedPEM(t, "manual.example.com", time.Now().Add(time.Hour))

	// Split the combined pem into key and cert files
	var keyPEM, chainPEM []byte
	rest := certPEM
	for len(rest) > 0 {
		block, r := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			chainPEM = append(chainPEM, pem.EncodeToMemory(block)...)
		} else {
			keyPEM = append(keyPEM, pem.EncodeToMemory(block)...)
		}
		rest = r
	}
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, chainPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.TLS.ACME.Enabled = false
	cfg.TLS.CertFile = certFile
	cfg.TLS.KeyFile = keyFile
	e := newTestEngine(t, cfg, store)

	if !e.ManualCertLoaded() || e.ACMEEnabled() {
		t.Fatal("expected manual only engine")
	}
	cert, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: "anything.example.com"})
	if err != nil || cert == nil {
		t.Fatalf("manual fallback: %v", err)
	}
}

func TestEngineRequiresACertSource(t *testing.T) {
	cfg := testConfig()
	cfg.TLS.ACME.Enabled = false
	log := logger.NewWithConfig(&logger.Config{Enabled: false})
	if _, err := NewEngine(cfg, newTestStore(t), log); err == nil {
		t.Fatal("expected error with no cert source")
	}
}

func TestHTTPChallengeHandlerRedirects(t *testing.T) {
	store := newTestStore(t)
	e := newTestEngine(t, testConfig(), store)

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/v2/", nil)
	rec := httptest.NewRecorder()
	e.HTTPChallengeHandler().ServeHTTP(rec, req)
	if rec.Code < 300 || rec.Code > 399 {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" || loc[:8] != "https://" {
		t.Fatalf("expected https redirect, got %q", loc)
	}
}

func TestHTTPChallengeHandlerNoRedirectWithoutTLSServing(t *testing.T) {
	cfg := testConfig()
	cfg.TLS.Enabled = false
	e := newTestEngine(t, cfg, newTestStore(t))

	req := httptest.NewRequest(http.MethodGet, "http://portal.example.com/v2/", nil)
	rec := httptest.NewRecorder()
	e.HTTPChallengeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 in pre-provisioning mode, got %d", rec.Code)
	}
}

func TestIssuableHost(t *testing.T) {
	cases := map[string]bool{
		"example.com":     true,
		"a.b.example.com": true,
		"localhost":       false,
		"registry":        false,
		"10.0.0.1":        false,
		"::1":             false,
		"":                false,
	}
	for host, want := range cases {
		if got := issuableHost(host); got != want {
			t.Errorf("issuableHost(%q) = %v, want %v", host, got, want)
		}
	}
}
