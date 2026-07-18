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
	"strconv"
	"strings"
	"sync"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// Terminates tls for primary server + portal listeners, every serving
// identity has one explicit certificate source, nothing falls back
type Engine struct {
	cfg    *config.Config
	store  *stores.Store
	log    *logger.Logger
	policy *HostnamePolicy

	configCert *tls.Certificate // From cert_file/key_file at startup

	mu        sync.Mutex
	acme      config.ACMEConfig            // Effective settings, db over config
	primary   string                       // Primary hostname cert source
	managers  map[string]*autocert.Manager // Keyed directory plus email
	certCache map[string]*cachedKeyPair    // Parsed uploads keyed by scope target
	leaves    map[string]*tls.Certificate  // Org ca minted leaves keyed org plus host
	challenge *http.Server
}

type cachedKeyPair struct {
	cert      *tls.Certificate
	updatedAt time.Time
}

func NewEngine(cfg *config.Config, store *stores.Store, log *logger.Logger) (*Engine, error) {
	e := &Engine{
		cfg:       cfg,
		store:     store,
		log:       log,
		policy:    NewHostnamePolicy(cfg, store),
		managers:  map[string]*autocert.Manager{},
		certCache: map[string]*cachedKeyPair{},
		leaves:    map[string]*tls.Certificate{},
	}
	if cfg.TLS.CertFile != "" || cfg.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading manual tls keypair: %w", err)
		}
		e.configCert = &cert
	}
	e.reloadSettings(context.Background())
	return e, nil
}

func (e *Engine) Policy() *HostnamePolicy {
	return e.policy
}

// Db values win over the config file acme block
func (e *Engine) reloadSettings(ctx context.Context) {
	eff := e.cfg.TLS.ACME
	if v, err := e.store.GetSystemSetting(ctx, storage.SettingACMEEnabled); err == nil {
		if b, err := strconv.ParseBool(v); err == nil {
			eff.Enabled = b
		}
	}
	if v, err := e.store.GetSystemSetting(ctx, storage.SettingACMEEmail); err == nil {
		eff.Email = v
	}
	if v, err := e.store.GetSystemSetting(ctx, storage.SettingACMEDirectory); err == nil {
		eff.DirectoryURL = v
	}
	primary := storage.PrimarySourceConfig
	if v, err := e.store.GetSystemSetting(ctx, storage.SettingPrimarySource); err == nil && v != "" {
		primary = v
	}
	e.mu.Lock()
	e.acme = eff
	e.primary = primary
	e.mu.Unlock()
}

// Where the primary hostname's certificate comes from
func (e *Engine) PrimarySource() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.primary
}

// Applies setting and material changes without a restart
func (e *Engine) Invalidate(ctx context.Context) {
	e.reloadSettings(ctx)
	e.mu.Lock()
	e.certCache = map[string]*cachedKeyPair{}
	e.leaves = map[string]*tls.Certificate{}
	e.mu.Unlock()
	e.ReconcileChallengeServer()
}

func (e *Engine) ACMEEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.acme.Enabled
}

// Effective acme settings after db overrides
func (e *Engine) EffectiveACME() config.ACMEConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.acme
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
	for _, d := range e.cfg.TLS.ACME.Domains {
		if bareHost(d) == host {
			return nil
		}
	}
	if primary := bareHost(e.cfg.Server.Hostname); primary == host && issuableHost(host) {
		return nil
	}
	if portals, err := e.store.ListPortalsByHostname(ctx, host); err == nil {
		for _, p := range portals {
			if p.CertSource == storage.CertSourceACME {
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

// Client for the effective settings, nil when acme is off
func (e *Engine) defaultManager() *autocert.Manager {
	eff := e.EffectiveACME()
	if !eff.Enabled {
		return nil
	}
	return e.managerFor(eff.DirectoryURL, eff.Email)
}

// Directory and email for a portal, portal over org over app tier
func (e *Engine) resolveACMEAccount(ctx context.Context, p *storage.RegistryPortal) (directory, email string) {
	eff := e.EffectiveACME()
	directory, email = eff.DirectoryURL, eff.Email
	if p == nil {
		return
	}
	if v, ok, err := e.store.GetOrgSetting(ctx, p.OrgID, storage.OrgSettingACMEDirectory); err == nil && ok && v != "" {
		directory = v
	}
	if v, ok, err := e.store.GetOrgSetting(ctx, p.OrgID, storage.OrgSettingACMEEmail); err == nil && ok && v != "" {
		email = v
	}
	if p.ACMEDirectory != "" {
		directory = p.ACMEDirectory
	}
	if p.ACMEEmail != "" {
		email = p.ACMEEmail
	}
	return
}

// App level enabled is the kill switch for every tier
func (e *Engine) portalManager(ctx context.Context, p *storage.RegistryPortal) *autocert.Manager {
	if !e.ACMEEnabled() {
		return nil
	}
	dir, email := e.resolveACMEAccount(ctx, p)
	return e.managerFor(dir, email)
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

	var portal *storage.RegistryPortal
	if host != "" {
		portal, _ = e.store.GetPortalForHost(ctx, host, port)
	}
	// A port only portal never hijacks the primary hostname's cert
	if portal != nil && portal.Hostname == "" && host == bareHost(e.cfg.Server.Hostname) {
		portal = nil
	}
	if portal == nil {
		return e.appCertificate(ctx, hello, host)
	}

	switch portal.CertSource {
	case storage.CertSourceACME:
		if m := e.portalManager(ctx, portal); m != nil {
			return m.GetCertificate(hello)
		}
		return nil, fmt.Errorf("acme is disabled, no certificate for portal host %q", host)
	case storage.CertSourceOrgCA:
		return e.caLeaf(ctx, storage.TLSCertScopeOrgCA, portal.OrgID, host)
	case storage.CertSourceOrgCert:
		cert, err := e.storedCert(ctx, storage.TLSCertScopeOrg, portal.OrgID, "")
		if err != nil {
			return nil, err
		}
		if cert == nil {
			return nil, fmt.Errorf("organization has no uploaded certificate for portal host %q", host)
		}
		return cert, nil
	case storage.CertSourceManual:
		cert, err := e.storedCert(ctx, storage.TLSCertScopePortal, portal.OrgID, portal.ID)
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
	switch e.PrimarySource() {
	case storage.PrimarySourceManual:
		cert, err := e.storedCert(ctx, storage.TLSCertScopeApp, "", "")
		if err != nil {
			return nil, err
		}
		if cert == nil {
			return nil, fmt.Errorf("no uploaded app certificate for %q", host)
		}
		return cert, nil
	case storage.PrimarySourceACME:
		if m := e.defaultManager(); m != nil {
			return m.GetCertificate(hello)
		}
		return nil, fmt.Errorf("acme is disabled, no app certificate for %q", host)
	case storage.PrimarySourceAppCA:
		return e.caLeaf(ctx, storage.TLSCertScopeAppCA, "", bareHost(e.cfg.Server.Hostname))
	}
	if e.configCert != nil {
		return e.configCert, nil
	}
	return nil, fmt.Errorf("no certificate configured for %q", host)
}

// Parsed uploaded material, nil when none stored for the target
func (e *Engine) storedCert(ctx context.Context, scope, orgID, portalID string) (*tls.Certificate, error) {
	row, err := e.store.GetTLSCertificate(ctx, scope, orgID, portalID)
	if err != nil || row == nil {
		return nil, err
	}
	key := scope + "|" + orgID + "|" + portalID
	e.mu.Lock()
	if c, ok := e.certCache[key]; ok && c.updatedAt.Equal(row.UpdatedAt) {
		e.mu.Unlock()
		return c.cert, nil
	}
	e.mu.Unlock()

	pair, err := tls.X509KeyPair([]byte(row.CertPEM), []byte(row.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("stored %s certificate invalid: %w", scope, err)
	}
	e.mu.Lock()
	e.certCache[key] = &cachedKeyPair{cert: &pair, updatedAt: row.UpdatedAt}
	e.mu.Unlock()
	return &pair, nil
}

// Short lived leaf for host minted from a stored signing ca
func (e *Engine) caLeaf(ctx context.Context, scope, orgID, host string) (*tls.Certificate, error) {
	key := scope + "|" + orgID + "|" + host
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
		return nil, fmt.Errorf("no %s signing ca for host %q", scope, host)
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
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
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
	e.log.Info("minted %s leaf for %s", scope, host)
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
	var fallback http.Handler
	if e.cfg.TLS.Enabled && e.cfg.TLS.ACME.RedirectHTTP {
		fallback = nil // Autocert default redirects to https
	} else {
		fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "https required", http.StatusForbidden)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := e.defaultManager(); m != nil {
			m.HTTPHandler(fallback).ServeHTTP(w, r)
			return
		}
		if fallback == nil {
			target := "https://" + bareHost(r.Host) + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

// Starts or stops the cleartext challenge listener to match settings
func (e *Engine) ReconcileChallengeServer() {
	port := e.cfg.TLS.ACME.HTTPPort
	e.mu.Lock()
	running := e.challenge
	enabled := e.acme.Enabled
	e.mu.Unlock()

	want := enabled && port != ""
	if want && running == nil {
		srv := &http.Server{
			Addr:              net.JoinHostPort(e.cfg.Server.Host, port),
			Handler:           e.HTTPChallengeHandler(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       time.Duration(e.cfg.Server.IdleTimeout) * time.Second,
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
func (e *Engine) EnsureCertificate(ctx context.Context, domain string) (*CertInfo, error) {
	domain = bareHost(domain)
	if !issuableHost(domain) {
		return nil, fmt.Errorf("domain %q is not a publicly issuable hostname", domain)
	}
	manager := e.defaultManager()
	// An acme sourced portal issues through its resolved account
	if portals, err := e.store.ListPortalsByHostname(ctx, domain); err == nil {
		for _, p := range portals {
			if p.CertSource == storage.CertSourceACME {
				manager = e.portalManager(ctx, p)
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

type CertInfo struct {
	Domain    string
	Issued    bool
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	SANs      []string
}

// Reads the cached chain without triggering issuance
func (e *Engine) CertificateInfo(ctx context.Context, domain string) (*CertInfo, error) {
	domain = bareHost(domain)
	info := &CertInfo{Domain: domain}
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
	info.Issued = true
	info.Issuer = leaf.Issuer.CommonName
	info.NotBefore = leaf.NotBefore
	info.NotAfter = leaf.NotAfter
	info.SANs = leaf.DNSNames
	return info, nil
}

// Certificate a handshake for host would observe, resolved like sni
// routing, nil when nothing would serve
func (e *Engine) ServingCertInfo(ctx context.Context, host string) *CertInfo {
	host = bareHost(host)
	if host == "" || e.store == nil {
		return nil
	}
	var st *Status
	if portals, err := e.store.ListPortalsByHostname(ctx, host); err == nil {
		for _, p := range portals {
			if !p.Enabled || p.CertSource == storage.CertSourceNone || p.CertSource == "" {
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
func (e *Engine) statusCertInfo(st *Status) *CertInfo {
	if st.ACMEInfo != nil && st.ACMEInfo.Issued {
		return st.ACMEInfo
	}
	if st.Material != nil {
		leaf := firstCertificate([]byte(st.Material.CertPEM))
		if leaf == nil {
			return nil
		}
		info := &CertInfo{
			Issued:    true,
			Issuer:    leaf.Issuer.CommonName,
			NotBefore: leaf.NotBefore,
			NotAfter:  leaf.NotAfter,
			SANs:      leaf.DNSNames,
		}
		// Ca material signs on demand leaves, report the signer not its issuer
		if leaf.IsCA {
			info.Issuer = leaf.Subject.CommonName
			info.SANs = nil
		}
		return info
	}
	if st.Source == storage.PrimarySourceConfig && e.configCert != nil {
		if leaf, err := x509.ParseCertificate(e.configCert.Certificate[0]); err == nil {
			return &CertInfo{
				Issued:    true,
				Issuer:    leaf.Issuer.CommonName,
				NotBefore: leaf.NotBefore,
				NotAfter:  leaf.NotAfter,
				SANs:      leaf.DNSNames,
			}
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
