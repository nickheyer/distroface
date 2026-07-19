package certs

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	db "github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Default leaf lifetime when a caller passes zero
const defaultLeafDays = 90

// Signed leaf plus the issuing chain in pem
type SignedLeaf struct {
	Leaf    *x509.Certificate
	CertPEM string // Leaf then issuing chain
}

// A parsed ca ready to sign, chain excludes the ca cert itself
type caMaterial struct {
	cert  *x509.Certificate
	key   crypto.PrivateKey
	chain [][]byte // Issuing certs above the ca, for path building
}

// Loads and parses the signing ca stored at a scope
func (e *Engine) caMaterial(ctx context.Context, scope v1.TLSScope, orgID string) (*caMaterial, error) {
	pair, err := e.storedCert(ctx, scope, orgID, "")
	if err != nil {
		return nil, err
	}
	if pair == nil {
		return nil, fmt.Errorf("no %v signing ca stored", scope)
	}
	caCert := pair.Leaf
	if caCert == nil {
		if caCert, err = x509.ParseCertificate(pair.Certificate[0]); err != nil {
			return nil, err
		}
	}
	if !caCert.IsCA {
		return nil, fmt.Errorf("stored %v material is not a ca", scope)
	}
	return &caMaterial{cert: caCert, key: pair.PrivateKey, chain: pair.Certificate[1:]}, nil
}

// Signs a public key into a server leaf for the given sans
func (ca *caMaterial) signLeaf(pub any, commonName string, dnsNames []string, ips []net.IP, days int) (*SignedLeaf, error) {
	if days <= 0 {
		days = defaultLeafDays
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	if tmpl.NotAfter.After(ca.cert.NotAfter) {
		tmpl.NotAfter = ca.cert.NotAfter
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, fmt.Errorf("signing leaf: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
	for _, extra := range ca.chain {
		pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: extra})
	}
	return &SignedLeaf{Leaf: leaf, CertPEM: b.String()}, nil
}

// Parses every certificate block in a pem bundle
func parseChain(pemData []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := pemData
	for len(rest) > 0 {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("bad certificate in bundle: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificate found in pem")
	}
	return certs, nil
}

// ValidateCABundle checks an uploaded ca is a ca and its chain resolves,
// self signed roots pass alone, intermediates need their issuers present
func ValidateCABundle(pemData []byte) error {
	certs, err := parseChain(pemData)
	if err != nil {
		return err
	}
	leaf := certs[0]
	if !leaf.IsCA {
		return fmt.Errorf("uploaded certificate is not a ca")
	}
	// Self signed root verifies against itself
	if err := leaf.CheckSignatureFrom(leaf); err == nil {
		return nil
	}
	// Otherwise the issuing chain must be bundled below the leaf
	intermediates := x509.NewCertPool()
	roots := x509.NewCertPool()
	for _, c := range certs[1:] {
		if err := c.CheckSignatureFrom(c); err == nil {
			roots.AddCert(c)
		} else {
			intermediates.AddCert(c)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("ca chain does not resolve, include issuing certificates: %w", err)
	}
	return nil
}

// Splits requested sans into dns names and ip addresses
func splitSANs(names []string) (dns []string, ips []net.IP) {
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
		} else {
			dns = append(dns, n)
		}
	}
	return dns, ips
}

// Enforces hostname policy across every non ip subject name
func (e *Engine) checkSANs(ctx context.Context, sans []string) error {
	if len(sans) == 0 {
		return fmt.Errorf("certificate needs at least one subject name")
	}
	for _, name := range sans {
		if net.ParseIP(name) != nil {
			continue
		}
		if err := e.hostPolicy(ctx, bareHost(name)); err != nil {
			return fmt.Errorf("host %q not allowed: %w", name, err)
		}
	}
	return nil
}

// SignServerCert validates the sans against policy and signs from a ca,
// empty orgID uses the app ca, populated uses that org's signing ca
func (e *Engine) SignServerCert(ctx context.Context, orgID string, pub any, sans []string, days int) (*SignedLeaf, error) {
	if err := e.checkSANs(ctx, sans); err != nil {
		return nil, err
	}
	scope := v1.TLSScope_TLS_SCOPE_APP_CA
	if orgID != "" {
		scope = v1.TLSScope_TLS_SCOPE_ORG_CA
	}
	ca, err := e.caMaterial(ctx, scope, orgID)
	if err != nil {
		return nil, err
	}
	dns, ips := splitSANs(sans)
	return ca.signLeaf(pub, sans[0], dns, ips, days)
}

// SignACMELeaf signs an issued acme certificate from the built in
// issuing ca, minting that ca off the app root on first use
func (e *Engine) SignACMELeaf(ctx context.Context, pub any, sans []string, days int) (*SignedLeaf, error) {
	if err := e.checkSANs(ctx, sans); err != nil {
		return nil, err
	}
	ca, err := e.ensureACMEIssuer(ctx)
	if err != nil {
		return nil, err
	}
	dns, ips := splitSANs(sans)
	return ca.signLeaf(pub, sans[0], dns, ips, days)
}

// Loads the acme issuing ca, minting it from the app root when absent
func (e *Engine) ensureACMEIssuer(ctx context.Context) (*caMaterial, error) {
	e.acmeIssuerMu.Lock()
	defer e.acmeIssuerMu.Unlock()

	ca, err := e.caMaterial(ctx, v1.TLSScope_TLS_SCOPE_ACME_CA, "")
	if err == nil {
		return ca, nil
	}
	root, err := e.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("no instance root ca, generate one before enabling the built in acme server")
	}
	certPEM, keyPEM, err := IssueICA(root.CertPEM, root.KeyPEM, "distroface acme issuer")
	if err != nil {
		return nil, err
	}
	record := &db.TLSCertificate{
		Scope:     v1.TLSScope_TLS_SCOPE_ACME_CA,
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		CreatedBy: "acme",
	}
	if err := e.store.SaveTLSCertificate(ctx, record); err != nil {
		return nil, err
	}
	e.log.Info("minted built in acme issuing ca from instance root")
	return e.caMaterial(ctx, v1.TLSScope_TLS_SCOPE_ACME_CA, "")
}
