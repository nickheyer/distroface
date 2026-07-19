package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Generates and stores the instance root ca for signing tests
func seedAppRoot(t *testing.T, store *stores.Store) {
	t.Helper()
	certPEM, keyPEM, err := GenerateRootCA("distroface test root")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTLSCertificate(context.Background(), &storage.TLSCertificate{
		Scope: v1.TLSScope_TLS_SCOPE_APP_CA, CertPEM: certPEM, KeyPEM: keyPEM,
	}); err != nil {
		t.Fatal(err)
	}
}

func appRootPool(t *testing.T, store *stores.Store) *x509.CertPool {
	t.Helper()
	row, err := store.GetTLSCertificate(context.Background(), v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
	if err != nil || row == nil {
		t.Fatalf("app root missing: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(row.CertPEM)) {
		t.Fatal("could not load root into pool")
	}
	return pool
}

func makeCSR(t *testing.T, cn string, sans []string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: cn},
		DNSNames: sans,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func registerApproved(t *testing.T, store *stores.Store, domain string) {
	t.Helper()
	if err := store.CreateCertificateDomain(context.Background(), &storage.CertificateDomain{
		Domain: domain, Scope: v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM, Approved: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSignACMELeafChainsToRoot(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	seedAppRoot(t, store)
	registerApproved(t, store, "svc.example.com")

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	signed, err := e.SignACMELeaf(ctx, &key.PublicKey, []string{"svc.example.com"}, 0)
	if err != nil {
		t.Fatalf("SignACMELeaf: %v", err)
	}

	// Leaf verifies through the acme issuer up to the app root
	chain, err := parseChain([]byte(signed.CertPEM))
	if err != nil {
		t.Fatal(err)
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{
		Roots: appRootPool(t, store), Intermediates: inter,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("leaf does not chain to root: %v", err)
	}
	if len(chain[0].DNSNames) != 1 || chain[0].DNSNames[0] != "svc.example.com" {
		t.Fatalf("unexpected sans: %v", chain[0].DNSNames)
	}
}

func TestSignACMELeafReusesIssuer(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	seedAppRoot(t, store)
	registerApproved(t, store, "a.example.com")
	registerApproved(t, store, "b.example.com")

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := e.SignACMELeaf(ctx, &key.PublicKey, []string{"a.example.com"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SignACMELeaf(ctx, &key.PublicKey, []string{"b.example.com"}, 0); err != nil {
		t.Fatal(err)
	}
	// Only one issuing ca is minted across calls
	var count int64
	store.DB().Model(&storage.TLSCertificate{}).
		Where("scope = ?", v1.TLSScope_TLS_SCOPE_ACME_CA).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 acme issuer, got %d", count)
	}
}

func TestSignACMELeafBlockedHost(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	seedAppRoot(t, store)

	// Never registered so policy rejects
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := e.SignACMELeaf(context.Background(), &key.PublicKey, []string{"stranger.example.com"}, 0); err == nil {
		t.Fatal("expected policy rejection for unregistered host")
	}
}

func TestSignACMELeafNeedsRoot(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	registerApproved(t, store, "svc.example.com")

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := e.SignACMELeaf(context.Background(), &key.PublicKey, []string{"svc.example.com"}, 0); err == nil {
		t.Fatal("expected failure without an app root ca")
	}
}

func TestSignServerCertFromCSR(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	seedAppRoot(t, store)
	registerApproved(t, store, "app.example.com")

	csrPEM := makeCSR(t, "app.example.com", []string{"app.example.com"})
	block, _ := pem.Decode(csrPEM)
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := e.SignServerCert(ctx, "", csr.PublicKey, []string{"app.example.com"}, 30)
	if err != nil {
		t.Fatalf("SignServerCert: %v", err)
	}
	if signed.Leaf.PublicKey == nil {
		t.Fatal("leaf missing public key")
	}
}

func TestValidateCABundle(t *testing.T) {
	rootCertPEM, rootKeyPEM, err := GenerateRootCA("root")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCABundle([]byte(rootCertPEM)); err != nil {
		t.Fatalf("self signed root should validate: %v", err)
	}

	// IssueICA returns intermediate then root, a resolvable bundle
	icaPEM, _, err := IssueICA(rootCertPEM, rootKeyPEM, "intermediate")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCABundle([]byte(icaPEM)); err != nil {
		t.Fatalf("bundled intermediate should validate: %v", err)
	}

	// Leaf (non ca) is rejected
	leaf := selfSignedNonCA(t)
	if err := ValidateCABundle(leaf); err == nil {
		t.Fatal("non ca certificate should be rejected")
	}

	// Intermediate alone without its issuer does not resolve
	icaOnly := firstBlock(t, icaPEM)
	if err := ValidateCABundle(icaOnly); err == nil {
		t.Fatal("orphan intermediate should be rejected")
	}
}

func TestRenewableHosts(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	// Primary source acme means the primary host is renewable
	seedSystem(t, res, &v1.Settings{Tls: &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_ACME.Enum()}}, "tls.primary_source")
	registerApproved(t, store, "domain.example.com")
	if err := store.CreateOrganization(ctx, &storage.Organization{ID: "o1", Name: "acme", CreatedBy: "nick"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRegistryPortal(ctx, &storage.RegistryPortal{
		ID: "p1", OrgID: "o1", Name: "p", Hostname: "portal.example.com",
		Enabled: true, CertSource: v1.CertSource_CERT_SOURCE_ACME,
	}); err != nil {
		t.Fatal(err)
	}

	hosts := e.renewableHosts(ctx)
	want := map[string]bool{"registry.example.com": true, "domain.example.com": true, "portal.example.com": true}
	for _, h := range hosts {
		delete(want, h)
	}
	if len(want) != 0 {
		t.Fatalf("missing renewable hosts: %v (got %v)", want, hosts)
	}
}

// ── small pem helpers ────────────────────────────────────────────────────

func selfSignedNonCA(t *testing.T) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := randomSerial()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "leaf"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func firstBlock(t *testing.T, bundle string) []byte {
	t.Helper()
	block, _ := pem.Decode([]byte(bundle))
	if block == nil {
		t.Fatal("no pem block")
	}
	return pem.EncodeToMemory(block)
}
