package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Verified client identity attached to a request context
type ClientIdentity struct {
	CommonName string
	DNSNames   []string
	Issuer     string
}

type clientIDKey struct{}

// WithClientIdentity stores a verified mtls identity on the context
func WithClientIdentity(ctx context.Context, id *ClientIdentity) context.Context {
	return context.WithValue(ctx, clientIDKey{}, id)
}

// ClientIdentityFrom returns the verified mtls identity, nil when absent
func ClientIdentityFrom(ctx context.Context) *ClientIdentity {
	id, _ := ctx.Value(clientIDKey{}).(*ClientIdentity)
	return id
}

// Resolves the mtls mode for the target, portal scope overrides system
func (e *Engine) mtlsModeForHost(ctx context.Context, portal *TLSPortal) v1.MTLSMode {
	if portal != nil {
		mode := e.res.Portal(ctx, portal.ID).GetTls().GetMtlsMode()
		if mode != v1.MTLSMode_MTLS_MODE_UNSPECIFIED {
			return mode
		}
	}
	return e.res.System(ctx).GetTls().GetMtlsMode()
}

// Builds the client trust pool for the sni host, portals trust their
// org ca, the app host trusts the instance root
func (e *Engine) clientCAPool(ctx context.Context, portal *TLSPortal) (*x509.CertPool, error) {
	scope := v1.TLSScope_TLS_SCOPE_APP_CA
	orgID := ""
	if portal != nil {
		scope = v1.TLSScope_TLS_SCOPE_ORG_CA
		orgID = portal.OrgID
	}
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, "")
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("no client trust ca for this host")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(row.CertPEM)) {
		return nil, fmt.Errorf("client trust ca is unparsable")
	}
	return pool, nil
}

// Per connection config so each sni host applies its own client policy
func (e *Engine) configForClient(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	ctx := context.Background()
	if c := hello.Context(); c != nil {
		ctx = c
	}
	host := bareHost(hello.ServerName)
	port := localPort(hello)

	var portal *TLSPortal
	if host != "" && e.portals != nil {
		if p, ok := e.portals.ResolveForTLS(host, port); ok && !(p.CatchAll && host == e.primaryHost(ctx)) {
			portal = &p
		}
	}

	mode := e.mtlsModeForHost(ctx, portal)
	if mode == v1.MTLSMode_MTLS_MODE_UNSPECIFIED || mode == v1.MTLSMode_MTLS_MODE_OFF {
		return nil, nil
	}

	// A missing trust ca downgrades to off rather than bricking the host
	pool, err := e.clientCAPool(ctx, portal)
	if err != nil {
		e.log.Warn("mtls disabled for %q: %v", host, err)
		return nil, nil
	}
	base := e.TLSConfig()
	base.ClientCAs = pool
	base.ClientAuth = tls.VerifyClientCertIfGiven
	if mode == v1.MTLSMode_MTLS_MODE_REQUIRED {
		base.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return base, nil
}

// Local listener port from the client hello connection
func localPort(hello *tls.ClientHelloInfo) int {
	if hello.Conn != nil {
		if addr, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok {
			return addr.Port
		}
	}
	return 0
}

// VerifiedIdentity extracts the client identity from a completed handshake
func VerifiedIdentity(state *tls.ConnectionState) *ClientIdentity {
	if state == nil || len(state.PeerCertificates) == 0 {
		return nil
	}
	leaf := state.PeerCertificates[0]
	// Only present when the chain verified against the host's client ca
	if len(state.VerifiedChains) == 0 {
		return nil
	}
	return &ClientIdentity{
		CommonName: leaf.Subject.CommonName,
		DNSNames:   leaf.DNSNames,
		Issuer:     leaf.Issuer.CommonName,
	}
}
