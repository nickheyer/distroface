package certs

import (
	"fmt"

	storage "github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Wire enum to the db string the engine stores
func CertSourceFromProto(s v1.CertSource) (string, error) {
	switch s {
	case v1.CertSource_CERT_SOURCE_NONE:
		return storage.CertSourceNone, nil
	case v1.CertSource_CERT_SOURCE_CONFIG:
		return storage.PrimarySourceConfig, nil
	case v1.CertSource_CERT_SOURCE_MANUAL:
		return storage.CertSourceManual, nil
	case v1.CertSource_CERT_SOURCE_ACME:
		return storage.CertSourceACME, nil
	case v1.CertSource_CERT_SOURCE_ORG_CA:
		return storage.CertSourceOrgCA, nil
	case v1.CertSource_CERT_SOURCE_ORG_CERT:
		return storage.CertSourceOrgCert, nil
	case v1.CertSource_CERT_SOURCE_APP_CA:
		return storage.PrimarySourceAppCA, nil
	}
	return "", fmt.Errorf("invalid certificate source %v", s)
}

func CertSourceToProto(s string) v1.CertSource {
	switch s {
	case storage.CertSourceNone:
		return v1.CertSource_CERT_SOURCE_NONE
	case storage.PrimarySourceConfig:
		return v1.CertSource_CERT_SOURCE_CONFIG
	case storage.CertSourceManual:
		return v1.CertSource_CERT_SOURCE_MANUAL
	case storage.CertSourceACME:
		return v1.CertSource_CERT_SOURCE_ACME
	case storage.CertSourceOrgCA:
		return v1.CertSource_CERT_SOURCE_ORG_CA
	case storage.CertSourceOrgCert:
		return v1.CertSource_CERT_SOURCE_ORG_CERT
	case storage.PrimarySourceAppCA:
		return v1.CertSource_CERT_SOURCE_APP_CA
	}
	return v1.CertSource_CERT_SOURCE_UNSPECIFIED
}

func TLSScopeFromProto(s v1.TLSScope) (string, error) {
	switch s {
	case v1.TLSScope_TLS_SCOPE_APP:
		return storage.TLSCertScopeApp, nil
	case v1.TLSScope_TLS_SCOPE_APP_CA:
		return storage.TLSCertScopeAppCA, nil
	case v1.TLSScope_TLS_SCOPE_ORG:
		return storage.TLSCertScopeOrg, nil
	case v1.TLSScope_TLS_SCOPE_ORG_CA:
		return storage.TLSCertScopeOrgCA, nil
	case v1.TLSScope_TLS_SCOPE_PORTAL:
		return storage.TLSCertScopePortal, nil
	}
	return "", fmt.Errorf("invalid tls scope %v", s)
}

func DomainScopeFromProto(s v1.CertificateDomainScope) (string, error) {
	switch s {
	case v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM:
		return storage.CertDomainScopeSystem, nil
	case v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_ORG:
		return storage.CertDomainScopeOrg, nil
	}
	return "", fmt.Errorf("invalid domain scope %v", s)
}

func DomainScopeToProto(s string) v1.CertificateDomainScope {
	switch s {
	case storage.CertDomainScopeSystem:
		return v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM
	case storage.CertDomainScopeOrg:
		return v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_ORG
	}
	return v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_UNSPECIFIED
}

func CertStateToProto(s string) v1.CertState {
	switch s {
	case CertStateNone:
		return v1.CertState_CERT_STATE_NONE
	case CertStatePending:
		return v1.CertState_CERT_STATE_PENDING
	case CertStateReady:
		return v1.CertState_CERT_STATE_READY
	case CertStateError:
		return v1.CertState_CERT_STATE_ERROR
	}
	return v1.CertState_CERT_STATE_UNSPECIFIED
}
