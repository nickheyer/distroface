package certs

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func problem(st *v1.GetCertStatusResponse, state v1.CertState, format string, args ...any) {
	st.State = state
	st.Problems = append(st.Problems, fmt.Sprintf(format, args...))
}

// Parsed public details of stored pem material, key never included
func materialInfo(record *storage.TLSCertificate, includePEM bool) *v1.TLSMaterialInfo {
	if record == nil {
		return nil
	}
	info := &v1.TLSMaterialInfo{
		CreatedBy: record.CreatedBy,
		UpdatedAt: timestamppb.New(record.UpdatedAt),
	}
	if leaf := firstCertificate([]byte(record.CertPEM)); leaf != nil {
		info.Subject = leaf.Subject.CommonName
		info.Issuer = leaf.Issuer.CommonName
		info.NotBefore = timestamppb.New(leaf.NotBefore)
		info.NotAfter = timestamppb.New(leaf.NotAfter)
		info.Sans = leaf.DNSNames
		info.IsCa = leaf.IsCA
	}
	if includePEM {
		info.CertPem = record.CertPEM
	}
	return info
}

// Uploaded material must exist, parse, cover the host, and be current
func (e *Engine) checkMaterial(ctx context.Context, st *v1.GetCertStatusResponse, scope v1.TLSScope, orgID, portalID, host, missing string) {
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, portalID)
	if err != nil || row == nil {
		problem(st, v1.CertState_CERT_STATE_ERROR, "%s", missing)
		return
	}
	st.ServingCert = materialInfo(row, false)
	leaf := firstCertificate([]byte(row.CertPEM))
	if leaf == nil {
		problem(st, v1.CertState_CERT_STATE_ERROR, "stored certificate does not parse")
		return
	}
	if time.Now().After(leaf.NotAfter) {
		problem(st, v1.CertState_CERT_STATE_ERROR, "certificate expired %s", leaf.NotAfter.Format("2006-01-02"))
		return
	}
	if host != "" && !leaf.IsCA {
		if err := leaf.VerifyHostname(host); err != nil {
			problem(st, v1.CertState_CERT_STATE_ERROR, "certificate does not cover %q", host)
			return
		}
	}
	st.State = v1.CertState_CERT_STATE_READY
}

// Signing ca material only needs to exist, parse as a ca, and be current
func (e *Engine) checkCA(ctx context.Context, st *v1.GetCertStatusResponse, scope v1.TLSScope, orgID, missing string) {
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, "")
	if err != nil || row == nil {
		problem(st, v1.CertState_CERT_STATE_ERROR, "%s", missing)
		return
	}
	st.ServingCert = materialInfo(row, false)
	leaf := firstCertificate([]byte(row.CertPEM))
	if leaf == nil || !leaf.IsCA {
		problem(st, v1.CertState_CERT_STATE_ERROR, "stored ca material is not a ca certificate")
		return
	}
	if time.Now().After(leaf.NotAfter) {
		problem(st, v1.CertState_CERT_STATE_ERROR, "ca expired %s", leaf.NotAfter.Format("2006-01-02"))
		return
	}
	st.State = v1.CertState_CERT_STATE_READY
}

func (e *Engine) checkACME(ctx context.Context, st *v1.GetCertStatusResponse, host, orgID string) {
	if !e.ACMEEnabled(ctx) {
		problem(st, v1.CertState_CERT_STATE_ERROR, "acme is disabled at the app tier")
		return
	}
	if !issuableHost(host) {
		problem(st, v1.CertState_CERT_STATE_ERROR, "hostname %q is not publicly issuable", host)
		return
	}
	if e.managerForHost(ctx, host) == nil {
		problem(st, v1.CertState_CERT_STATE_ERROR,
			"acme directory url points at this instance, use the org ca source instead")
		return
	}
	if err := e.policy.Allowed(ctx, host, orgID); err != nil {
		state := v1.CertState_CERT_STATE_ERROR
		// A registered entry awaiting approval reads as pending, not broken
		if e.policy.RequireApproval(ctx) {
			if d, derr := e.store.GetCertificateDomainByName(ctx, host); derr == nil && d != nil && !d.Approved {
				state = v1.CertState_CERT_STATE_PENDING
			}
		}
		problem(st, state, "%s", err.Error())
		return
	}
	info, err := e.CertificateInfo(ctx, host)
	if err == nil && info.GetIssued() {
		st.AcmeCert = info
	}
	if !info.GetIssued() {
		problem(st, v1.CertState_CERT_STATE_PENDING, "certificate not yet issued")
		return
	}
	if time.Now().After(info.NotAfter.AsTime()) {
		problem(st, v1.CertState_CERT_STATE_PENDING, "certificate expired, renewal happens on the next handshake")
		return
	}
	st.State = v1.CertState_CERT_STATE_READY
}

// What the engine would serve for this portal right now
func (e *Engine) PortalStatus(ctx context.Context, p *storage.RegistryPortal) *v1.GetCertStatusResponse {
	st := &v1.GetCertStatusResponse{Source: p.CertSource}
	host := bareHost(p.Hostname)

	switch p.CertSource {
	case v1.CertSource_CERT_SOURCE_NONE, v1.CertSource_CERT_SOURCE_UNSPECIFIED:
		st.Source = v1.CertSource_CERT_SOURCE_NONE
		st.State = v1.CertState_CERT_STATE_NONE
	case v1.CertSource_CERT_SOURCE_MANUAL:
		e.checkMaterial(ctx, st, v1.TLSScope_TLS_SCOPE_PORTAL, p.OrgID, p.ID, host,
			"no certificate uploaded for this portal")
	case v1.CertSource_CERT_SOURCE_ORG_CERT:
		e.checkMaterial(ctx, st, v1.TLSScope_TLS_SCOPE_ORG, p.OrgID, "", host,
			"organization has no uploaded certificate")
	case v1.CertSource_CERT_SOURCE_ORG_CA:
		e.checkCA(ctx, st, v1.TLSScope_TLS_SCOPE_ORG_CA, p.OrgID,
			"organization has no signing ca")
	case v1.CertSource_CERT_SOURCE_ACME:
		e.checkACME(ctx, st, host, p.OrgID)
	default:
		problem(st, v1.CertState_CERT_STATE_ERROR, "unknown certificate source %v", p.CertSource)
	}
	return st
}

// What the primary hostname serves right now
func (e *Engine) AppStatus(ctx context.Context) *v1.GetCertStatusResponse {
	st := &v1.GetCertStatusResponse{Source: e.PrimarySource(ctx)}
	host := e.primaryHost(ctx)

	switch st.Source {
	case v1.CertSource_CERT_SOURCE_MANUAL:
		e.checkMaterial(ctx, st, v1.TLSScope_TLS_SCOPE_APP, "", "", host,
			"no app certificate uploaded")
	case v1.CertSource_CERT_SOURCE_ACME:
		e.checkACME(ctx, st, host, "")
	case v1.CertSource_CERT_SOURCE_APP_CA:
		e.checkCA(ctx, st, v1.TLSScope_TLS_SCOPE_APP_CA, "",
			"no instance ca generated or uploaded")
	default:
		st.Source = v1.CertSource_CERT_SOURCE_CONFIG
		if e.configCert == nil {
			problem(st, v1.CertState_CERT_STATE_NONE, "no cert_file/key_file pair in the config")
			return st
		}
		leaf, err := x509.ParseCertificate(e.configCert.Certificate[0])
		if err == nil && time.Now().After(leaf.NotAfter) {
			problem(st, v1.CertState_CERT_STATE_ERROR, "config certificate expired %s", leaf.NotAfter.Format("2006-01-02"))
			return st
		}
		st.State = v1.CertState_CERT_STATE_READY
	}
	return st
}
