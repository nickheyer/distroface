package certs

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
)

const (
	CertStateReady   = "ready"
	CertStatePending = "pending"
	CertStateError   = "error"
	CertStateNone    = "none"
)

// Resolved certificate state for one serving identity, store reads only
type Status struct {
	Source    string
	State     string
	Problems  []string
	Directory string
	Email     string
	Material  *storage.TLSCertificate
	ACMEInfo  *CertInfo
}

func (s *Status) problem(format string, args ...any) {
	s.Problems = append(s.Problems, fmt.Sprintf(format, args...))
}

// Uploaded material must exist, parse, cover the host, and be current
func (e *Engine) checkMaterial(ctx context.Context, st *Status, scope, orgID, portalID, host, missing string) {
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, portalID)
	if err != nil || row == nil {
		st.State = CertStateError
		st.problem("%s", missing)
		return
	}
	st.Material = row
	leaf := firstCertificate([]byte(row.CertPEM))
	if leaf == nil {
		st.State = CertStateError
		st.problem("stored certificate does not parse")
		return
	}
	if time.Now().After(leaf.NotAfter) {
		st.State = CertStateError
		st.problem("certificate expired %s", leaf.NotAfter.Format("2006-01-02"))
		return
	}
	if host != "" && !leaf.IsCA {
		if err := leaf.VerifyHostname(host); err != nil {
			st.State = CertStateError
			st.problem("certificate does not cover %q", host)
			return
		}
	}
	st.State = CertStateReady
}

// Signing ca material only needs to exist, parse as a ca, and be current
func (e *Engine) checkCA(ctx context.Context, st *Status, scope, orgID, missing string) *x509.Certificate {
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, "")
	if err != nil || row == nil {
		st.State = CertStateError
		st.problem("%s", missing)
		return nil
	}
	st.Material = row
	leaf := firstCertificate([]byte(row.CertPEM))
	if leaf == nil || !leaf.IsCA {
		st.State = CertStateError
		st.problem("stored ca material is not a ca certificate")
		return nil
	}
	if time.Now().After(leaf.NotAfter) {
		st.State = CertStateError
		st.problem("ca expired %s", leaf.NotAfter.Format("2006-01-02"))
		return nil
	}
	st.State = CertStateReady
	return leaf
}

func (e *Engine) checkACME(ctx context.Context, st *Status, host, orgID string) {
	if !e.ACMEEnabled() {
		st.State = CertStateError
		st.problem("acme is disabled at the app tier")
		return
	}
	if !issuableHost(host) {
		st.State = CertStateError
		st.problem("hostname %q is not publicly issuable", host)
		return
	}
	if err := e.policy.Allowed(ctx, host, orgID); err != nil {
		st.State = CertStateError
		// A registered entry awaiting approval reads as pending, not broken
		if e.policy.RequireApproval(ctx) {
			if d, derr := e.store.GetCertificateDomainByName(ctx, host); derr == nil && d != nil && !d.Approved {
				st.State = CertStatePending
			}
		}
		st.problem("%s", err.Error())
		return
	}
	info, err := e.CertificateInfo(ctx, host)
	if err == nil && info != nil {
		st.ACMEInfo = info
	}
	if info == nil || !info.Issued {
		st.State = CertStatePending
		st.problem("certificate not yet issued")
		return
	}
	if time.Now().After(info.NotAfter) {
		st.State = CertStatePending
		st.problem("certificate expired, renewal happens on the next handshake")
		return
	}
	st.State = CertStateReady
}

// What the engine would serve for this portal right now
func (e *Engine) PortalStatus(ctx context.Context, p *storage.RegistryPortal) *Status {
	st := &Status{Source: p.CertSource}
	st.Directory, st.Email = e.resolveACMEAccount(ctx, p)
	host := bareHost(p.Hostname)

	switch p.CertSource {
	case storage.CertSourceNone, "":
		st.Source = storage.CertSourceNone
		st.State = CertStateNone
	case storage.CertSourceManual:
		e.checkMaterial(ctx, st, storage.TLSCertScopePortal, p.OrgID, p.ID, host,
			"no certificate uploaded for this portal")
	case storage.CertSourceOrgCert:
		e.checkMaterial(ctx, st, storage.TLSCertScopeOrg, p.OrgID, "", host,
			"organization has no uploaded certificate")
	case storage.CertSourceOrgCA:
		e.checkCA(ctx, st, storage.TLSCertScopeOrgCA, p.OrgID,
			"organization has no signing ca")
	case storage.CertSourceACME:
		e.checkACME(ctx, st, host, p.OrgID)
	default:
		st.State = CertStateError
		st.problem("unknown certificate source %q", p.CertSource)
	}
	return st
}

// What the primary hostname serves right now
func (e *Engine) AppStatus(ctx context.Context) *Status {
	st := &Status{Source: e.PrimarySource()}
	eff := e.EffectiveACME()
	st.Directory, st.Email = eff.DirectoryURL, eff.Email
	host := bareHost(e.cfg.Server.Hostname)

	switch st.Source {
	case storage.PrimarySourceManual:
		e.checkMaterial(ctx, st, storage.TLSCertScopeApp, "", "", host,
			"no app certificate uploaded")
	case storage.PrimarySourceACME:
		e.checkACME(ctx, st, host, "")
	case storage.PrimarySourceAppCA:
		e.checkCA(ctx, st, storage.TLSCertScopeAppCA, "",
			"no instance ca generated or uploaded")
	default:
		st.Source = storage.PrimarySourceConfig
		if e.configCert == nil {
			st.State = CertStateNone
			st.problem("no cert_file/key_file pair in the config")
			return st
		}
		leaf, err := x509.ParseCertificate(e.configCert.Certificate[0])
		if err == nil && time.Now().After(leaf.NotAfter) {
			st.State = CertStateError
			st.problem("config certificate expired %s", leaf.NotAfter.Format("2006-01-02"))
			return st
		}
		st.State = CertStateReady
	}
	return st
}
