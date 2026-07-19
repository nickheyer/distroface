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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Narrow portal identity for sni certificate resolution
type TLSPortal struct {
	ID       string
	OrgID    string
	Source   v1.CertSource
	CatchAll bool
}

// Cached hostname to portal resolution the tls layer queries
type PortalLookup interface {
	ResolveForTLS(host string, port int) (TLSPortal, bool)
}

// Terminates tls for primary server + portal listeners, every serving
// identity has one explicit certificate source, nothing falls back
type Engine struct {
	store    *stores.Store
	res      *settings.Resolver
	portals  PortalLookup
	log      *logger.Logger
	policy   *HostnamePolicy
	bindHost string

	configCert *tls.Certificate // From cert_file/key_file at startup

	mu        sync.Mutex
	managers  map[string]*autocert.Manager // Keyed directory plus email
	certCache map[string]*cachedKeyPair    // Parsed uploads keyed by scope target
	leaves    map[string]*tls.Certificate  // Org ca minted leaves keyed org plus host
	challenge *http.Server
}

type cachedKeyPair struct {
	cert      *tls.Certificate
	updatedAt time.Time
}

func NewEngine(store *stores.Store, res *settings.Resolver, portals PortalLookup, certFile, keyFile, bindHost string, log *logger.Logger) (*Engine, error) {
	e := &Engine{
		store:     store,
		res:       res,
		portals:   portals,
		log:       log,
		policy:    NewHostnamePolicy(store, res),
		bindHost:  bindHost,
		managers:  map[string]*autocert.Manager{},
		certCache: map[string]*cachedKeyPair{},
		leaves:    map[string]*tls.Certificate{},
	}
	if certFile != "" || keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("loading manual tls keypair: %w", err)
		}
		e.configCert = &cert
	}
	return e, nil
}

func (e *Engine) Policy() *HostnamePolicy {
	return e.policy
}

// Bare primary hostname from live settings
func (e *Engine) primaryHost(ctx context.Context) string {
	return bareHost(e.res.System(ctx).GetServer().GetPublicHostname())
}

func (e *Engine) PrimarySource(ctx context.Context) v1.CertSource {
	return e.res.System(ctx).GetTls().GetPrimarySource()
}

// Applies setting and material changes without a restart
func (e *Engine) Invalidate(ctx context.Context) {
	e.mu.Lock()
	e.certCache = map[string]*cachedKeyPair{}
	e.leaves = map[string]*tls.Certificate{}
	e.mu.Unlock()
	e.ReconcileChallengeServer()
}

func (e *Engine) ACMEEnabled(ctx context.Context) bool {
	return e.res.System(ctx).GetAcme().GetEnabled()
}

// Config file pair only, uploads are reported separately
func (e *Engine) ManualCertLoaded() bool {
	return e != nil && e.configCert != nil
}

// Bare lowercase host from a host or host port string
func bareHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// Dotted dns name, not an ip, not localhost
func issuableHost(host string) bool {
	if host == "" || host == "localhost" || !strings.Contains(host, ".") {
		return false
	}
	return net.ParseIP(host) == nil
}

// Issuance is limited to hostnames explicitly configured somewhere,
// random sni probes never reach the acme client, policy gates on top
func (e *Engine) hostPolicy(ctx context.Context, host string) error {
	host = bareHost(host)
	if primary := e.primaryHost(ctx); primary == host && issuableHost(host) {
		return nil
	}
	if portals, err := e.store.ListPortalsByHostname(ctx, host); err == nil {
		for _, p := range portals {
			if p.CertSource == v1.CertSource_CERT_SOURCE_ACME {
				return e.policy.Allowed(ctx, host, p.OrgID)
			}
		}
	}
	d, err := e.store.GetCertificateDomainByName(ctx, host)
	if err != nil {
		return err
	}
	if d != nil {
		orgID := ""
		if d.OrgID != nil {
			orgID = *d.OrgID
		}
		return e.policy.Allowed(ctx, host, orgID)
	}
	return fmt.Errorf("host %q is not configured for acme issuance", host)
}

// One acme client per directory and account email
func (e *Engine) managerFor(directory, email string) *autocert.Manager {
	key := directory + "|" + email
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.managers[key]; ok {
		return m
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      email,
		Cache:      dbCache{store: e.store},
		HostPolicy: e.hostPolicy,
	}
	if directory != "" {
		m.Client = &acme.Client{DirectoryURL: directory}
	}
	e.managers[key] = m
	return m
}

// Client for the system tier account, nil when acme is off
func (e *Engine) defaultManager(ctx context.Context) *autocert.Manager {
	eff := e.res.System(ctx).GetAcme()
	if !eff.GetEnabled() {
		return nil
	}
	return e.managerFor(eff.GetDirectoryUrl(), eff.GetEmail())
}

// Account resolution rides the settings tier chain, enabled stays
// a system only kill switch
func (e *Engine) portalManager(ctx context.Context, portalID string) *autocert.Manager {
	eff := e.res.Portal(ctx, portalID).GetAcme()
	if !eff.GetEnabled() {
		return nil
	}
	return e.managerFor(eff.GetDirectoryUrl(), eff.GetEmail())
}

// Shared server config, sni picks the right cert per hostname
func (e *Engine) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
		GetCertificate: e.getCertificate,
	}
}

// Serves exactly the configured source for the hostname or nothing
func (e *Engine) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	ctx := context.Background()
	if c := hello.Context(); c != nil {
		ctx = c
	}
	host := bareHost(hello.ServerName)
	port := 0
	if hello.Conn != nil {
		if addr, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok {
			port = addr.Port
		}
	}

	var portal *TLSPortal
	if host != "" && e.portals != nil {
		if p, ok := e.portals.ResolveForTLS(host, port); ok {
			portal = &p
		}
	}
	// A port only portal never hijacks the primary hostname's cert
	if portal != nil && portal.CatchAll && host == e.primaryHost(ctx) {
		portal = nil
	}
	if portal == nil {
		return e.appCertificate(ctx, hello, host)
	}

	switch portal.Source {
	case v1.CertSource_CERT_SOURCE_ACME:
		if m := e.portalManager(ctx, portal.ID); m != nil {
			return m.GetCertificate(hello)
		}
		return nil, fmt.Errorf("acme is disabled, no certificate for portal host %q", host)
	case v1.CertSource_CERT_SOURCE_ORG_CA:
		return e.caLeaf(ctx, v1.TLSScope_TLS_SCOPE_ORG_CA, portal.OrgID, host)
	case v1.CertSource_CERT_SOURCE_ORG_CERT:
		cert, err := e.storedCert(ctx, v1.TLSScope_TLS_SCOPE_ORG, portal.OrgID, "")
		if err != nil {
			return nil, err
		}
		if cert == nil {
			return nil, fmt.Errorf("organization has no uploaded certificate for portal host %q", host)
		}
		return cert, nil
	case v1.CertSource_CERT_SOURCE_MANUAL:
		cert, err := e.storedCert(ctx, v1.TLSScope_TLS_SCOPE_PORTAL, portal.OrgID, portal.ID)
		if err != nil {
			return nil, err
		}
		if cert == nil {
			return nil, fmt.Errorf("no uploaded certificate for portal host %q", host)
		}
		return cert, nil
	}
	return nil, fmt.Errorf("portal host %q has no certificate source", host)
}

// Primary hostname and unmatched sni serve the app tier source
func (e *Engine) appCertificate(ctx context.Context, hello *tls.ClientHelloInfo, host string) (*tls.Certificate, error) {
	switch e.PrimarySource(ctx) {
	case v1.CertSource_CERT_SOURCE_MANUAL:
		cert, err := e.storedCert(ctx, v1.TLSScope_TLS_SCOPE_APP, "", "")
		if err != nil {
			return nil, err
		}
		if cert == nil {
			return nil, fmt.Errorf("no uploaded app certificate for %q", host)
		}
		return cert, nil
	case v1.CertSource_CERT_SOURCE_ACME:
		if m := e.defaultManager(ctx); m != nil {
			return m.GetCertificate(hello)
		}
		return nil, fmt.Errorf("acme is disabled, no app certificate for %q", host)
	case v1.CertSource_CERT_SOURCE_APP_CA:
		return e.caLeaf(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", e.primaryHost(ctx))
	}
	if e.configCert != nil {
		return e.configCert, nil
	}
	return nil, fmt.Errorf("no certificate configured for %q", host)
}

// Parsed uploaded material, nil when none stored for the target
func (e *Engine) storedCert(ctx context.Context, scope v1.TLSScope, orgID, portalID string) (*tls.Certificate, error) {
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, portalID)
	if err != nil || row == nil {
		return nil, err
	}
	key := fmt.Sprintf("%d|%s|%s", scope, orgID, portalID)
	e.mu.Lock()
	if c, ok := e.certCache[key]; ok && c.updatedAt.Equal(row.UpdatedAt) {
		e.mu.Unlock()
		return c.cert, nil
	}
	e.mu.Unlock()

	pair, err := tls.X509KeyPair([]byte(row.CertPEM), []byte(row.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("stored %v certificate invalid: %w", scope, err)
	}
	e.mu.Lock()
	e.certCache[key] = &cachedKeyPair{cert: &pair, updatedAt: row.UpdatedAt}
	e.mu.Unlock()
	return &pair, nil
}

// Short lived leaf for host minted from a stored signing ca
func (e *Engine) caLeaf(ctx context.Context, scope v1.TLSScope, orgID, host string) (*tls.Certificate, error) {
	key := fmt.Sprintf("%d|%s|%s", scope, orgID, host)
	e.mu.Lock()
	if leaf, ok := e.leaves[key]; ok && leaf.Leaf != nil && time.Until(leaf.Leaf.NotAfter) > 7*24*time.Hour {
		e.mu.Unlock()
		return leaf, nil
	}
	e.mu.Unlock()

	caPair, err := e.storedCert(ctx, scope, orgID, "")
	if err != nil {
		return nil, err
	}
	if caPair == nil {
		return nil, fmt.Errorf("no %v signing ca for host %q", scope, host)
	}
	caCert := caPair.Leaf
	if caCert == nil {
		if caCert, err = x509.ParseCertificate(caPair.Certificate[0]); err != nil {
			return nil, err
		}
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if tmpl.NotAfter.After(caCert.NotAfter) {
		tmpl.NotAfter = caCert.NotAfter
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caPair.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("signing leaf for %q: %w", host, err)
	}
	leafCert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	// Full stored chain rides along so clients can path build
	cert := &tls.Certificate{
		Certificate: append([][]byte{der}, caPair.Certificate...),
		PrivateKey:  leafKey,
		Leaf:        leafCert,
	}
	e.mu.Lock()
	e.leaves[key] = cert
	e.mu.Unlock()
	e.log.Info("minted %v leaf for %s", scope, host)
	return cert, nil
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func encodeKeyPair(der []byte, key *ecdsa.PrivateKey) (certPEM, keyPEM string, err error) {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, nil
}

// Ten year self signed ecdsa ca, path length one allows intermediates
func generateSelfSignedCA(commonName string, maxPathLen int) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            maxPathLen,
		MaxPathLenZero:        maxPathLen == 0,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	return encodeKeyPair(der, key)
}

// Standalone org signing ca, only mints leaves
func GenerateCA(commonName string) (certPEM, keyPEM string, err error) {
	return generateSelfSignedCA(commonName, 0)
}

// Instance root able to sign org intermediates
func GenerateRootCA(commonName string) (certPEM, keyPEM string, err error) {
	return generateSelfSignedCA(commonName, 1)
}

// Intermediate signed by the root pair, root cert appended for the chain
func IssueICA(rootCertPEM, rootKeyPEM, commonName string) (certPEM, keyPEM string, err error) {
	rootPair, err := tls.X509KeyPair([]byte(rootCertPEM), []byte(rootKeyPEM))
	if err != nil {
		return "", "", fmt.Errorf("parsing root ca pair: %w", err)
	}
	rootCert, err := x509.ParseCertificate(rootPair.Certificate[0])
	if err != nil {
		return "", "", err
	}
	if !rootCert.IsCA {
		return "", "", fmt.Errorf("instance material is not a ca certificate")
	}
	if rootCert.MaxPathLenZero {
		return "", "", fmt.Errorf("instance ca cannot sign intermediates, path length is zero")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	if tmpl.NotAfter.After(rootCert.NotAfter) {
		tmpl.NotAfter = rootCert.NotAfter
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, rootCert, &key.PublicKey, rootPair.PrivateKey)
	if err != nil {
		return "", "", err
	}
	certPEM, keyPEM, err = encodeKeyPair(der, key)
	if err != nil {
		return "", "", err
	}
	rootDER := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCert.Raw})
	return certPEM + string(rootDER), keyPEM, nil
}

// Handler for the cleartext port, answers http-01 then redirects
func (e *Engine) HTTPChallengeHandler() http.Handler {
	forbid := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "https required", http.StatusForbidden)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sys := e.res.System(ctx)
		redirect := sys.GetAcme().GetRedirectHttp() &&
			sys.GetTls().GetMode() == v1.TLSMode_TLS_MODE_HTTPS_ONLY
		var fallback http.Handler
		if !redirect {
			fallback = forbid
		}
		if m := e.defaultManager(ctx); m != nil {
			m.HTTPHandler(fallback).ServeHTTP(w, r)
			return
		}
		if redirect {
			target := "https://" + bareHost(r.Host) + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		forbid.ServeHTTP(w, r)
	})
}

// Starts or stops the cleartext challenge listener to match settings
func (e *Engine) ReconcileChallengeServer() {
	acmeEff := e.res.System(context.Background()).GetAcme()
	port := acmeEff.GetChallengePort()
	want := acmeEff.GetEnabled() && port != ""

	e.mu.Lock()
	running := e.challenge
	e.mu.Unlock()

	// A port change needs a stop then start
	if running != nil && want && running.Addr != net.JoinHostPort(e.bindHost, port) {
		_ = running.Close()
		e.mu.Lock()
		e.challenge = nil
		e.mu.Unlock()
		running = nil
	}

	if want && running == nil {
		srv := &http.Server{
			Addr:              net.JoinHostPort(e.bindHost, port),
			Handler:           e.HTTPChallengeHandler(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		e.mu.Lock()
		e.challenge = srv
		e.mu.Unlock()
		go func() {
			e.log.Info("ACME challenge listener on %s", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				e.log.Error("ACME challenge listener stopped: %v", err)
			}
		}()
	} else if !want && running != nil {
		_ = running.Close()
		e.mu.Lock()
		e.challenge = nil
		e.mu.Unlock()
		e.log.Info("ACME challenge listener stopped")
	}
}

// Stops the challenge listener
func (e *Engine) Close() {
	e.mu.Lock()
	srv := e.challenge
	e.challenge = nil
	e.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

// Modern ecdsa capable hello so the cache key matches real clients
func syntheticHello(domain string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		ServerName:        domain,
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		SupportedCurves:   []tls.CurveID{tls.CurveP256},
		SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}
}

// Blocks until issued or failed, autocert caps this at five minutes
func (e *Engine) EnsureCertificate(ctx context.Context, domain string) (*v1.CertificateInfo, error) {
	domain = bareHost(domain)
	if !issuableHost(domain) {
		return nil, fmt.Errorf("domain %q is not a publicly issuable hostname", domain)
	}
	manager := e.defaultManager(ctx)
	// An acme sourced portal issues through its resolved account
	if portals, err := e.store.ListPortalsByHostname(ctx, domain); err == nil {
		for _, p := range portals {
			if p.CertSource == v1.CertSource_CERT_SOURCE_ACME {
				manager = e.portalManager(ctx, p.ID)
				break
			}
		}
	}
	if manager == nil {
		return nil, fmt.Errorf("acme is not enabled")
	}
	if _, err := manager.GetCertificate(syntheticHello(domain)); err != nil {
		return nil, err
	}
	return e.CertificateInfo(ctx, domain)
}

// Reads the cached chain without triggering issuance
func (e *Engine) CertificateInfo(ctx context.Context, domain string) (*v1.CertificateInfo, error) {
	domain = bareHost(domain)
	info := &v1.CertificateInfo{}
	if e.store == nil {
		return info, nil
	}
	data, err := e.store.GetACMECacheEntry(ctx, domain)
	if err != nil {
		return nil, err
	}
	if data == nil {
		if data, err = e.store.GetACMECacheEntry(ctx, domain+"+rsa"); err != nil || data == nil {
			return info, err
		}
	}
	leaf := firstCertificate(data)
	if leaf == nil {
		return info, nil
	}
	return certInfoFromLeaf(leaf), nil
}

// Issued details from a parsed leaf
func certInfoFromLeaf(leaf *x509.Certificate) *v1.CertificateInfo {
	return &v1.CertificateInfo{
		Issued:    true,
		Issuer:    leaf.Issuer.CommonName,
		NotBefore: timestamppb.New(leaf.NotBefore),
		NotAfter:  timestamppb.New(leaf.NotAfter),
		Sans:      leaf.DNSNames,
	}
}

// Certificate a handshake for host would observe, resolved like sni
// routing, nil when nothing would serve
func (e *Engine) ServingCertInfo(ctx context.Context, host string) *v1.CertificateInfo {
	host = bareHost(host)
	if host == "" || e.store == nil {
		return nil
	}
	var st *v1.GetCertStatusResponse
	if portals, err := e.store.ListPortalsByHostname(ctx, host); err == nil {
		for _, p := range portals {
			if !p.Enabled || p.CertSource == v1.CertSource_CERT_SOURCE_NONE ||
				p.CertSource == v1.CertSource_CERT_SOURCE_UNSPECIFIED {
				continue
			}
			st = e.PortalStatus(ctx, p)
			break
		}
	}
	if st == nil {
		st = e.AppStatus(ctx)
	}
	return e.statusCertInfo(st)
}

// Observable cert details behind a resolved status, any source
func (e *Engine) statusCertInfo(st *v1.GetCertStatusResponse) *v1.CertificateInfo {
	if st.AcmeCert.GetIssued() {
		return st.AcmeCert
	}
	if m := st.ServingCert; m != nil && m.NotAfter != nil {
		info := &v1.CertificateInfo{
			Issued:    true,
			Issuer:    m.Issuer,
			NotBefore: m.NotBefore,
			NotAfter:  m.NotAfter,
			Sans:      m.Sans,
		}
		// Ca material signs on demand leaves, report the signer not its issuer
		if m.IsCa {
			info.Issuer = m.Subject
			info.Sans = nil
		}
		return info
	}
	if st.Source == v1.CertSource_CERT_SOURCE_CONFIG && e.configCert != nil {
		if leaf, err := x509.ParseCertificate(e.configCert.Certificate[0]); err == nil {
			return certInfoFromLeaf(leaf)
		}
	}
	return nil
}

// Leaf cert from autocert's key then chain pem layout
func firstCertificate(data []byte) *x509.Certificate {
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil
		}
		return cert
	}
	return nil
}

// Tls handshakes open with this record type
const tlsRecordHandshake = 0x16

// Accepts tls and cleartext on one port, first byte of each conn decides
type dualListener struct {
	raw    net.Listener
	tlsCfg *tls.Config
	sniff  time.Duration
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

// Any hostname may speak https while others stay cleartext
func DualSchemeListener(raw net.Listener, cfg *tls.Config, sniffTimeout time.Duration) net.Listener {
	if sniffTimeout <= 0 {
		sniffTimeout = 10 * time.Second
	}
	l := &dualListener{
		raw:    raw,
		tlsCfg: cfg,
		sniff:  sniffTimeout,
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
	go l.acceptLoop()
	return l
}

func (l *dualListener) acceptLoop() {
	for {
		conn, err := l.raw.Accept()
		if err != nil {
			_ = l.Close()
			return
		}
		go l.classify(conn)
	}
}

func (l *dualListener) classify(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(l.sniff))
	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	c := net.Conn(&sniffedConn{Conn: conn, peeked: first[:]})
	if first[0] == tlsRecordHandshake {
		c = tls.Server(c, l.tlsCfg)
	}
	select {
	case l.conns <- c:
	case <-l.closed:
		conn.Close()
	}
}

func (l *dualListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *dualListener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.closed)
		err = l.raw.Close()
	})
	return err
}

func (l *dualListener) Addr() net.Addr { return l.raw.Addr() }

// Replays the sniffed byte before the rest of the stream
type sniffedConn struct {
	net.Conn
	peeked []byte
}

func (c *sniffedConn) Read(p []byte) (int, error) {
	if len(c.peeked) > 0 {
		n := copy(p, c.peeked)
		c.peeked = c.peeked[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}
