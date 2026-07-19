package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Replaces the instance root with a freshly generated one
func rotateRoot(t *testing.T, store *stores.Store) {
	t.Helper()
	certPEM, keyPEM, err := GenerateRootCA("distroface rotated root")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTLSCertificate(context.Background(), &storage.TLSCertificate{
		Scope: v1.TLSScope_TLS_SCOPE_APP_CA, CertPEM: certPEM, KeyPEM: keyPEM,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestACMEIssuerSelfHealsAfterRootRotation(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	seedAppRoot(t, store)
	registerApproved(t, store, "svc.example.com")

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	first, err := e.SignACMELeaf(ctx, &key.PublicKey, []string{"svc.example.com"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	firstIssuer, _ := store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_ACME_CA, "", "")

	// A leaf signed now chains to the original root
	if e.Orphaned(ctx, first.CertPEM) {
		t.Fatal("fresh leaf should not be orphaned")
	}

	// Rotate the root, the stored issuer is now orphaned
	rotateRoot(t, store)
	if !e.Orphaned(ctx, firstIssuer.CertPEM) {
		t.Fatal("issuer should be orphaned after root rotation")
	}

	// Next issuance heals the issuer against the new root
	second, err := e.SignACMELeaf(ctx, &key.PublicKey, []string{"svc.example.com"}, 0)
	if err != nil {
		t.Fatalf("re-issue after rotation: %v", err)
	}
	if e.Orphaned(ctx, second.CertPEM) {
		t.Fatal("healed leaf should chain to the new root")
	}
	newIssuer, _ := store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_ACME_CA, "", "")
	if newIssuer.CertPEM == firstIssuer.CertPEM {
		t.Fatal("issuer should have been re-minted")
	}
}

func TestOrphanDetection(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	rootCert, rootKey, _ := GenerateRootCA("root")
	if err := store.SaveTLSCertificate(ctx, &storage.TLSCertificate{
		Scope: v1.TLSScope_TLS_SCOPE_APP_CA, CertPEM: rootCert, KeyPEM: rootKey,
	}); err != nil {
		t.Fatal(err)
	}

	// A standalone self signed org ca is never orphaned
	standalone, _, _ := GenerateCA("standalone")
	if e.Orphaned(ctx, standalone) {
		t.Fatal("self signed ca must not be orphaned")
	}

	// An ica chaining to the current root is not orphaned
	ica, _, _ := IssueICA(rootCert, rootKey, "ica")
	if e.Orphaned(ctx, ica) {
		t.Fatal("ica chaining to current root must not be orphaned")
	}

	// After rotation that same ica is orphaned
	rotateRoot(t, store)
	if !e.Orphaned(ctx, ica) {
		t.Fatal("ica must be orphaned after the root rotates")
	}
}

func TestOrgICADependents(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	rootCert, rootKey, _ := GenerateRootCA("root")
	store.SaveTLSCertificate(ctx, &storage.TLSCertificate{Scope: v1.TLSScope_TLS_SCOPE_APP_CA, CertPEM: rootCert, KeyPEM: rootKey})

	if err := store.CreateOrganization(ctx, &storage.Organization{ID: "o1", Name: "a", CreatedBy: "n"}); err != nil {
		t.Fatal(err)
	}
	// An org holding an ica depends on the root
	ica, ikey, _ := IssueICA(rootCert, rootKey, "o1 ica")
	store.SaveTLSCertificate(ctx, &storage.TLSCertificate{Scope: v1.TLSScope_TLS_SCOPE_ORG_CA, OrgID: "o1", CertPEM: ica, KeyPEM: ikey})
	if deps := e.OrgICADependents(ctx); len(deps) != 1 || deps[0] != "o1" {
		t.Fatalf("expected o1 as a dependent, got %v", deps)
	}

	// A standalone org ca is not a dependent
	if err := store.CreateOrganization(ctx, &storage.Organization{ID: "o2", Name: "b", CreatedBy: "n"}); err != nil {
		t.Fatal(err)
	}
	sc, sk, _ := GenerateCA("o2 standalone")
	store.SaveTLSCertificate(ctx, &storage.TLSCertificate{Scope: v1.TLSScope_TLS_SCOPE_ORG_CA, OrgID: "o2", CertPEM: sc, KeyPEM: sk})
	deps := e.OrgICADependents(ctx)
	for _, d := range deps {
		if d == "o2" {
			t.Fatal("standalone org ca must not be a root dependent")
		}
	}
}
