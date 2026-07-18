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
	t.Cleanup(func() { _ = store.Close() })
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
		{"pending.example.com", true},        // Registered, approval flow off by default
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

	// Approval toggle gates unapproved entries only
	if err := store.SetSystemSetting(ctx, storage.SettingRequireApproval, "true"); err != nil {
		t.Fatal(err)
	}
	if err := e.hostPolicy(ctx, "pending.example.com"); err == nil {
		t.Error("unapproved entry must be denied once approval is required")
	}
	if err := e.hostPolicy(ctx, "portal.example.com"); err != nil {
		t.Errorf("approved entry denied under approval mode: %v", err)
	}
}

func TestHostnamePolicyGates(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	p := NewHostnamePolicy(cfg, store)
	ctx := context.Background()

	orgA := &storage.Organization{Name: "a", CreatedBy: "t"}
	orgB := &storage.Organization{Name: "b", CreatedBy: "t"}
	for _, o := range []*storage.Organization{orgA, orgB} {
		if err := store.CreateOrganization(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	// Blacklist blocks exact matches and wildcard suffixes
	if err := store.SetSystemSetting(ctx, storage.SettingHostnameBlacklist,
		`["blocked.io", "*.corp.internal"]`); err != nil {
		t.Fatal(err)
	}
	if err := p.AllowedClaim(ctx, "blocked.io", orgA.ID); err == nil {
		t.Error("exact blacklist match must deny")
	}
	if err := p.AllowedClaim(ctx, "deep.sub.corp.internal", orgA.ID); err == nil {
		t.Error("wildcard blacklist match must deny")
	}
	if err := p.AllowedClaim(ctx, "corp.internal", orgA.ID); err != nil {
		t.Errorf("*.suffix must not match the bare suffix: %v", err)
	}
	if err := p.AllowedClaim(ctx, "fine.example.org", orgA.ID); err != nil {
		t.Errorf("unlisted hostname denied: %v", err)
	}

	// Cross org claims fail loudly
	if err := store.CreateCertificateDomain(ctx, &storage.CertificateDomain{
		Domain: "claimed.example.org", Scope: storage.CertDomainScopeOrg, OrgID: &orgA.ID, CreatedBy: "t",
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.AllowedClaim(ctx, "claimed.example.org", orgB.ID); err == nil {
		t.Error("hostname registered to another org must deny")
	}
	if err := p.AllowedClaim(ctx, "claimed.example.org", orgA.ID); err != nil {
		t.Errorf("owning org denied its own registration: %v", err)
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

// Splits the combined pem into separate cert and key strings
func pemPair(t *testing.T, domain string) (string, string) {
	t.Helper()
	combined := selfSignedPEM(t, domain, time.Now().Add(time.Hour))
	var key, cert []byte
	rest := combined
	for len(rest) > 0 {
		block, r := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			cert = append(cert, pem.EncodeToMemory(block)...)
		} else {
			key = append(key, pem.EncodeToMemory(block)...)
		}
		rest = r
	}
	return string(cert), string(key)
}

func leafOf(t *testing.T, cert *tls.Certificate) *x509.Certificate {
	t.Helper()
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatal("nil certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf
}

func TestGetCertificateSources(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	cfg.TLS.ACME.Enabled = false
	e := newTestEngine(t, cfg, store)
	ctx := context.Background()

	org := &storage.Organization{Name: "acme", CreatedBy: "t"}
	if err := store.CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}

	// Manual source serves the portal's uploaded pair
	manualPortal := &storage.RegistryPortal{
		OrgID: org.ID, Name: "m", Hostname: "manual.acme.io", Rules: "[]",
		CertSource: storage.CertSourceManual, Enabled: true,
	}
	if err := store.CreateRegistryPortal(ctx, manualPortal); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := pemPair(t, "manual.acme.io")
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: storage.TLSCertScopePortal, OrgID: org.ID, PortalID: manualPortal.ID,
		CertPEM: certPEM, KeyPEM: keyPEM,
	}); err != nil {
		t.Fatal(err)
	}
	cert, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: "manual.acme.io"})
	if err != nil {
		t.Fatalf("manual source: %v", err)
	}
	if cn := leafOf(t, cert).Subject.CommonName; cn != "manual.acme.io" {
		t.Errorf("manual source served %q", cn)
	}

	// Org ca source mints a leaf signed by the org's ca
	caPortal := &storage.RegistryPortal{
		OrgID: org.ID, Name: "c", Hostname: "internal.acme.io", Rules: "[]",
		CertSource: storage.CertSourceOrgCA, Enabled: true,
	}
	if err := store.CreateRegistryPortal(ctx, caPortal); err != nil {
		t.Fatal(err)
	}
	caCertPEM, caKeyPEM, err := GenerateCA("acme ca")
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: storage.TLSCertScopeOrgCA, OrgID: org.ID, CertPEM: caCertPEM, KeyPEM: caKeyPEM,
	}); err != nil {
		t.Fatal(err)
	}
	cert, err = e.getCertificate(&tls.ClientHelloInfo{ServerName: "internal.acme.io"})
	if err != nil {
		t.Fatalf("org ca source: %v", err)
	}
	leaf := leafOf(t, cert)
	if leaf.Issuer.CommonName != "acme ca" || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "internal.acme.io" {
		t.Errorf("org ca leaf: issuer %q sans %v", leaf.Issuer.CommonName, leaf.DNSNames)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
		t.Fatal("bad ca pem")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Errorf("leaf does not chain to the org ca: %v", err)
	}

	// Org cert source serves the org's uploaded wildcard
	orgCertPortal := &storage.RegistryPortal{
		OrgID: org.ID, Name: "w", Hostname: "wild.acme.io", Rules: "[]",
		CertSource: storage.CertSourceOrgCert, Enabled: true,
	}
	if err := store.CreateRegistryPortal(ctx, orgCertPortal); err != nil {
		t.Fatal(err)
	}
	orgCertPEM, orgKeyPEM := pemPair(t, "wild.acme.io")
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: storage.TLSCertScopeOrg, OrgID: org.ID, CertPEM: orgCertPEM, KeyPEM: orgKeyPEM,
	}); err != nil {
		t.Fatal(err)
	}
	cert, err = e.getCertificate(&tls.ClientHelloInfo{ServerName: "wild.acme.io"})
	if err != nil {
		t.Fatalf("org cert source: %v", err)
	}
	if cn := leafOf(t, cert).Subject.CommonName; cn != "wild.acme.io" {
		t.Errorf("org cert source served %q", cn)
	}

	// Source none serves nothing even with material around
	nonePortal := &storage.RegistryPortal{
		OrgID: org.ID, Name: "n", Hostname: "plain.acme.io", Rules: "[]",
		CertSource: storage.CertSourceNone, Enabled: true,
	}
	if err := store.CreateRegistryPortal(ctx, nonePortal); err != nil {
		t.Fatal(err)
	}
	if _, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: "plain.acme.io"}); err == nil {
		t.Error("source none must not serve a certificate")
	}

	// Unknown sni with the default config source and no config pair fails
	if _, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: "unknown.example.com"}); err == nil {
		t.Error("no app tier material configured, expected an error")
	}

	// Primary source manual serves the uploaded app cert for unmatched sni
	appCertPEM, appKeyPEM := pemPair(t, "fallback.example.com")
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: storage.TLSCertScopeApp, CertPEM: appCertPEM, KeyPEM: appKeyPEM,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSystemSetting(ctx, storage.SettingPrimarySource, storage.PrimarySourceManual); err != nil {
		t.Fatal(err)
	}
	e.Invalidate(ctx)
	cert, err = e.getCertificate(&tls.ClientHelloInfo{ServerName: "unknown.example.com"})
	if err != nil {
		t.Fatalf("manual primary source: %v", err)
	}
	if cn := leafOf(t, cert).Subject.CommonName; cn != "fallback.example.com" {
		t.Errorf("manual primary source served %q", cn)
	}
}

func TestICAChain(t *testing.T) {
	rootCert, rootKey, err := GenerateRootCA("distroface root")
	if err != nil {
		t.Fatal(err)
	}
	icaCert, icaKey, err := IssueICA(rootCert, rootKey, "acme ica")
	if err != nil {
		t.Fatalf("IssueICA: %v", err)
	}

	store := newTestStore(t)
	cfg := testConfig()
	cfg.TLS.ACME.Enabled = false
	e := newTestEngine(t, cfg, store)
	ctx := context.Background()

	org := &storage.Organization{Name: "acme", CreatedBy: "t"}
	if err := store.CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: storage.TLSCertScopeOrgCA, OrgID: org.ID, CertPEM: icaCert, KeyPEM: icaKey,
	}); err != nil {
		t.Fatal(err)
	}
	portal := &storage.RegistryPortal{
		OrgID: org.ID, Name: "i", Hostname: "ica.acme.io", Rules: "[]",
		CertSource: storage.CertSourceOrgCA, Enabled: true,
	}
	if err := store.CreateRegistryPortal(ctx, portal); err != nil {
		t.Fatal(err)
	}

	cert, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: "ica.acme.io"})
	if err != nil {
		t.Fatalf("ica leaf: %v", err)
	}
	leaf := leafOf(t, cert)

	// The leaf must verify through the ica to the instance root alone
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(rootCert)) {
		t.Fatal("bad root pem")
	}
	inters := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		ic, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		inters.AddCert(ic)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters}); err != nil {
		t.Errorf("leaf does not chain to the instance root: %v", err)
	}

	// A path length zero ca must refuse to sign intermediates
	orgCACert, orgCAKey, err := GenerateCA("standalone")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := IssueICA(orgCACert, orgCAKey, "nested"); err == nil {
		t.Error("standalone ca signed an intermediate, path length must forbid it")
	}
}

func TestRuntimeACMESettings(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	cfg.TLS.ACME.Enabled = false
	e := newTestEngine(t, cfg, store)
	ctx := context.Background()

	if e.ACMEEnabled() {
		t.Fatal("acme must start disabled")
	}
	if err := store.SetSystemSetting(ctx, storage.SettingACMEEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSystemSetting(ctx, storage.SettingACMEEmail, "ops@acme.io"); err != nil {
		t.Fatal(err)
	}
	e.Invalidate(ctx)
	if !e.ACMEEnabled() || e.EffectiveACME().Email != "ops@acme.io" {
		t.Fatalf("runtime settings not applied: %+v", e.EffectiveACME())
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

func TestAppCASource(t *testing.T) {
	store := newTestStore(t)
	cfg := testConfig()
	cfg.TLS.ACME.Enabled = false
	e := newTestEngine(t, cfg, store)
	ctx := context.Background()

	caPEM, caKey, err := GenerateCA("instance root")
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: storage.TLSCertScopeAppCA, CertPEM: caPEM, KeyPEM: caKey,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSystemSetting(ctx, storage.SettingPrimarySource, storage.PrimarySourceAppCA); err != nil {
		t.Fatal(err)
	}
	e.Invalidate(ctx)

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		t.Fatal("bad ca pem")
	}

	// The app tier has one identity, every sni serves the primary leaf
	// chained to the instance root, portal hostnames never reach here
	for _, sni := range []string{"registry.example.com", "probe.example.net", ""} {
		cert, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: sni})
		if err != nil {
			t.Fatalf("app ca source for %q: %v", sni, err)
		}
		leaf := leafOf(t, cert)
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
			t.Fatalf("leaf for %q does not chain to the instance ca: %v", sni, err)
		}
		if leaf.VerifyHostname("registry.example.com") != nil {
			t.Errorf("sni %q served %v, want the primary identity", sni, leaf.DNSNames)
		}
	}
}
