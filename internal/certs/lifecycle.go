package certs

import (
	"context"
	"crypto/x509"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// appRootPool builds a verify pool from the current instance root
func (e *Engine) appRootPool(ctx context.Context) (*x509.CertPool, bool) {
	row, err := e.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
	if err != nil || row == nil {
		return nil, false
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(row.CertPEM)) {
		return nil, false
	}
	return pool, true
}

// Orphaned reports whether a stored ca is an intermediate that no longer
// chains to the current instance root, self signed cas are never orphaned
func (e *Engine) Orphaned(ctx context.Context, certPEM string) bool {
	certs, err := parseChain([]byte(certPEM))
	if err != nil || len(certs) == 0 {
		return false
	}
	leaf := certs[0]
	// A self signed ca stands alone, nothing to orphan
	if leaf.CheckSignatureFrom(leaf) == nil {
		return false
	}
	root, ok := e.appRootPool(ctx)
	if !ok {
		return true
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:         root,
		Intermediates: inter,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err != nil
}

// ReconcileAppCADependents re-mints the acme issuer against the current
// root and reports org ids whose intermediates no longer chain, called
// after the instance root is generated, imported, or removed
func (e *Engine) ReconcileAppCADependents(ctx context.Context) []string {
	// Re-derive an existing acme issuer, creation stays lazy on first use
	if row, err := e.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_ACME_CA, "", ""); err == nil && row != nil && e.Orphaned(ctx, row.CertPEM) {
		if _, err := e.ensureACMEIssuer(ctx); err != nil {
			e.log.Warn("acme issuer reconcile deferred: %v", err)
		}
	}

	var orphaned []string
	orgs, err := e.store.ListOrgCACertificates(ctx)
	if err != nil {
		e.log.Warn("could not scan org cas for orphans: %v", err)
		return orphaned
	}
	for _, row := range orgs {
		if e.Orphaned(ctx, row.CertPEM) {
			orphaned = append(orphaned, row.OrgID)
		}
	}
	e.Invalidate(ctx)
	return orphaned
}

// OrgICADependents lists org ids holding intermediates chaining to the
// current root, used to warn before the root is deleted
func (e *Engine) OrgICADependents(ctx context.Context) []string {
	var deps []string
	orgs, err := e.store.ListOrgCACertificates(ctx)
	if err != nil {
		return deps
	}
	for _, row := range orgs {
		certs, perr := parseChain([]byte(row.CertPEM))
		if perr != nil || len(certs) == 0 {
			continue
		}
		// Only intermediates depend on the root, self signed cas do not
		if certs[0].CheckSignatureFrom(certs[0]) != nil {
			deps = append(deps, row.OrgID)
		}
	}
	return deps
}
