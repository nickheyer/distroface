package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	"github.com/libdns/rfc2136"
	"github.com/mholt/acmez/v3"
	"github.com/mholt/acmez/v3/acme"
	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Longest one blocking order may run
const issueTimeout = 5 * time.Minute

// One acme account at one directory with its challenge setup
type acmeManager struct {
	cfg       *certmagic.Config
	issuer    *certmagic.ACMEIssuer
	challenge v1.ACMEChallengeType
	err       error // Settings no order can succeed with
}

func (m *acmeManager) dns01() bool {
	return m.challenge == v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01
}

// Publishing client for a tier's dns block
func dnsProvider(dns *v1.ACMEDNSSettings) (certmagic.DNSProvider, error) {
	switch dns.GetProvider() {
	case v1.DNSProvider_DNS_PROVIDER_RFC2136:
		server := strings.TrimSpace(dns.GetServer())
		if server == "" {
			return nil, fmt.Errorf("rfc2136 dns has no nameserver configured")
		}
		if _, _, err := net.SplitHostPort(server); err != nil {
			server = net.JoinHostPort(server, "53")
		}
		alg := strings.TrimSpace(dns.GetTsigAlgorithm())
		if alg == "" {
			alg = "hmac-sha256"
		}
		return &rfc2136.Provider{
			Server:  server,
			KeyName: strings.TrimSpace(dns.GetTsigKeyName()),
			KeyAlg:  alg,
			Key:     strings.TrimSpace(dns.GetTsigSecret()),
		}, nil
	case v1.DNSProvider_DNS_PROVIDER_CLOUDFLARE:
		token := strings.TrimSpace(dns.GetApiToken())
		if token == "" {
			return nil, fmt.Errorf("cloudflare dns has no api token configured")
		}
		return &cloudflare.Provider{APIToken: token}, nil
	}
	return nil, fmt.Errorf("the dns-01 challenge has no dns provider configured")
}

// Dns-01 solver from a tier's dns block
func (e *Engine) dnsSolver(dns *v1.ACMEDNSSettings) (acmez.Solver, error) {
	provider, err := dnsProvider(dns)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(dns.GetPropagationTimeoutSeconds()) * time.Second
	if timeout <= 0 {
		timeout = -1
	}
	var resolvers []string
	for _, r := range dns.GetResolvers() {
		if r = strings.TrimSpace(r); r != "" {
			resolvers = append(resolvers, r)
		}
	}
	return &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{
		DNSProvider:        provider,
		PropagationTimeout: timeout,
		Resolvers:          resolvers,
		Logger:             e.acmeLog,
	}}, nil
}

// Issuer for one tier's effective settings
func (e *Engine) issuerTemplate(eff *v1.ACMESettings) (certmagic.ACMEIssuer, error) {
	directory := strings.TrimSpace(eff.GetDirectoryUrl())
	if directory == "" {
		directory = certmagic.LetsEncryptProductionCA
	}
	challenge, challengePort := eff.GetChallenge(), strings.TrimSpace(eff.GetChallengePort())
	tmpl := certmagic.ACMEIssuer{
		CA:         directory,
		TestCA:     directory,
		Email:      strings.TrimSpace(eff.GetEmail()),
		Agreed:     true,
		ListenHost: e.bindHost,
		Logger:     e.acmeLog,
		DisableHTTPChallenge: challengePort == "" ||
			challenge == v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_TLS_ALPN_01,
		DisableTLSALPNChallenge: challenge == v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_HTTP_01,
	}
	// Solvers find our listeners already bound and stand down
	if port, err := strconv.Atoi(challengePort); err == nil {
		tmpl.AltHTTPPort = port
	}
	if port, err := strconv.Atoi(e.listenPort); err == nil {
		tmpl.AltTLSALPNPort = port
	}
	if id, mac := strings.TrimSpace(eff.GetEabKeyId()), strings.TrimSpace(eff.GetEabHmacKey()); id != "" && mac != "" {
		tmpl.ExternalAccount = &acme.EAB{KeyID: id, MACKey: mac}
	}
	if key := strings.TrimSpace(eff.GetAccountKey()); key != "" {
		keyPEM, err := settings.ACMEAccountKeyPEM(key)
		if err != nil {
			return tmpl, err
		}
		tmpl.AccountKeyPEM = keyPEM
	}
	if bundle := strings.TrimSpace(eff.GetCaBundle()); bundle != "" {
		tmpl.TrustedRoots = x509.NewCertPool()
		if !tmpl.TrustedRoots.AppendCertsFromPEM([]byte(bundle)) {
			return tmpl, fmt.Errorf("the acme ca bundle holds no pem certificate")
		}
	}
	switch challenge {
	case v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_HTTP_01:
		if challengePort == "" {
			return tmpl, fmt.Errorf("the http-01 challenge is selected but the challenge port is empty")
		}
	case v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01:
		solver, err := e.dnsSolver(eff.GetDns())
		if err != nil {
			return tmpl, err
		}
		tmpl.DNS01Solver = solver
	}
	return tmpl, nil
}

// One acme client per distinct effective account and challenge setup
func (e *Engine) managerFor(eff *v1.ACMESettings) *acmeManager {
	raw, _ := proto.MarshalOptions{Deterministic: true}.Marshal(eff)
	key := e.listenPort + "|" + string(raw)

	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.managers[key]; ok {
		return m
	}
	m := &acmeManager{challenge: eff.GetChallenge()}
	m.cfg = certmagic.New(e.magic, certmagic.Config{
		Storage:  e.storage,
		OnDemand: &certmagic.OnDemandConfig{DecisionFunc: e.hostPolicy},
		Logger:   e.acmeLog,
		SubjectTransformer: func(ctx context.Context, name string) string {
			return e.certSubject(ctx, m, name)
		},
	})
	tmpl, err := e.issuerTemplate(eff)
	m.err = err
	m.issuer = certmagic.NewACMEIssuer(m.cfg, tmpl)
	m.cfg.Issuers = []certmagic.Issuer{m.issuer}
	e.managers[key] = m
	return m
}

// Nil when acme is off or the directory is ours
func (e *Engine) tierManager(ctx context.Context, eff *v1.ACMESettings) *acmeManager {
	if !eff.GetEnabled() || e.selfDirectory(ctx, eff.GetDirectoryUrl()) {
		return nil
	}
	return e.managerFor(eff)
}

// System tier account, nil when acme is off
func (e *Engine) defaultManager(ctx context.Context) *acmeManager {
	return e.tierManager(ctx, e.res.System(ctx).GetAcme())
}

// Enabled stays a system only kill switch
func (e *Engine) portalManager(ctx context.Context, portalID string) *acmeManager {
	return e.tierManager(ctx, e.res.Portal(ctx, portalID).GetAcme())
}

// Org tier account, nil when acme is off
func (e *Engine) orgManager(ctx context.Context, orgID string) *acmeManager {
	return e.tierManager(ctx, e.res.Org(ctx, orgID).GetAcme())
}

// Portal and org claiming host, empty for instance
func (e *Engine) acmeOwner(ctx context.Context, host string) (portalID, orgID string) {
	if portals, err := e.store.ListPortalsByHostname(ctx, host); err == nil {
		for _, p := range portals {
			if p.CertSource == v1.CertSource_CERT_SOURCE_ACME {
				return p.ID, p.OrgID
			}
		}
	}
	if d, err := e.store.GetCertificateDomainByName(ctx, host); err == nil && d != nil && d.OrgID != nil {
		return "", *d.OrgID
	}
	return "", ""
}

// Portal account wins, then org domain, then system
func (e *Engine) managerForHost(ctx context.Context, host string) *acmeManager {
	switch portalID, orgID := e.acmeOwner(ctx, host); {
	case portalID != "":
		return e.portalManager(ctx, portalID)
	case orgID != "":
		return e.orgManager(ctx, orgID)
	}
	return e.defaultManager(ctx)
}

// Approved wildcard its owner may use, empty when none
func (e *Engine) coveringWildcard(ctx context.Context, host string) string {
	_, parent, ok := strings.Cut(host, ".")
	if !ok || strings.HasPrefix(host, "*.") {
		return ""
	}
	d, err := e.store.GetCertificateDomainByName(ctx, "*."+parent)
	if err != nil || d == nil || (!d.Approved && e.policy.RequireApproval(ctx)) {
		return ""
	}
	if d.OrgID != nil && *d.OrgID != "" {
		if _, orgID := e.acmeOwner(ctx, host); orgID != *d.OrgID {
			return ""
		}
	}
	return d.Domain
}

// Covering wildcard under dns-01, otherwise host itself
func (e *Engine) certSubject(ctx context.Context, m *acmeManager, host string) string {
	if !m.dns01() {
		return host
	}
	if wildcard := e.coveringWildcard(ctx, host); wildcard != "" {
		return wildcard
	}
	return host
}

// Handshake issuance with the outcome remembered per host
func (e *Engine) acmeCertificate(ctx context.Context, m *acmeManager, hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if m.err != nil {
		return nil, m.err
	}
	cert, err := m.cfg.GetCertificateWithContext(ctx, hello)
	// Challenge hellos are not an order outcome
	if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acmez.ACMETLS1Protocol {
		return cert, err
	}
	host := bareHost(hello.ServerName)
	if err == nil {
		e.clearIssueFailure(host)
		return cert, nil
	}
	// Only policy approved hosts are remembered
	if host != "" && e.hostPolicy(ctx, host) == nil {
		e.recordIssueFailure(host, err)
	}
	return nil, err
}

// Blocks until issued, renewed or failed
func (e *Engine) EnsureCertificate(ctx context.Context, domain string) (*v1.CertificateInfo, error) {
	domain = bareHost(domain)
	if !issuableHost(domain) {
		return nil, fmt.Errorf("domain %q is not a publicly issuable hostname", domain)
	}
	m := e.managerForHost(ctx, domain)
	if m == nil {
		return nil, fmt.Errorf("acme is disabled or the directory url points back at this instance")
	}
	if m.err != nil {
		return nil, m.err
	}
	if err := e.hostPolicy(ctx, domain); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()
	subject := e.certSubject(ctx, m, domain)
	if strings.HasPrefix(subject, "*.") && !m.dns01() {
		return nil, fmt.Errorf("wildcard %q needs the dns-01 challenge, a ca validates wildcards no other way", subject)
	}
	err := m.cfg.ObtainCertSync(ctx, subject)
	if err == nil {
		err = m.cfg.RenewCertSync(ctx, subject, false)
	}
	if err != nil {
		e.recordIssueFailure(domain, err)
		return nil, err
	}
	e.clearIssueFailure(domain)
	return e.CertificateInfo(ctx, domain)
}

// Reads the stored chain without triggering issuance
func (e *Engine) CertificateInfo(ctx context.Context, domain string) (*v1.CertificateInfo, error) {
	domain = bareHost(domain)
	info := &v1.CertificateInfo{}
	if e.store == nil {
		return info, nil
	}
	m := e.managerForHost(ctx, domain)
	if m == nil {
		return info, nil
	}
	key := certmagic.StorageKeys.SiteCert(m.issuer.IssuerKey(), e.certSubject(ctx, m, domain))
	data, err := e.store.GetACMECacheEntry(ctx, key)
	if err != nil || data == nil {
		return info, err
	}
	leaf := firstCertificate(data)
	if leaf == nil {
		return info, nil
	}
	return certInfoFromLeaf(leaf), nil
}

// Rehomes certs the previous client stored under bare hostnames
func (e *Engine) importLegacyCerts(ctx context.Context) {
	if e.legacyImported.Load() {
		return
	}
	keys, err := e.store.ListACMECacheKeys(ctx, "")
	if err != nil {
		return
	}
	e.legacyImported.Store(true)
	for _, key := range keys {
		if strings.Contains(key, "/") {
			continue
		}
		host := strings.TrimSuffix(key, "+rsa")
		// Old account key and challenge tokens have no new home
		if strings.Contains(host, "+") {
			_ = e.store.DeleteACMECacheEntry(ctx, key)
			continue
		}
		m := e.managerForHost(ctx, host)
		if m == nil {
			continue
		}
		data, err := e.store.GetACMECacheEntry(ctx, key)
		if err != nil {
			continue
		}
		var keyPEM, chainPEM []byte
		for rest := data; ; {
			var block *pem.Block
			if block, rest = pem.Decode(rest); block == nil {
				break
			}
			if block.Type == "CERTIFICATE" {
				chainPEM = append(chainPEM, pem.EncodeToMemory(block)...)
			} else {
				keyPEM = append(keyPEM, pem.EncodeToMemory(block)...)
			}
		}
		leaf := firstCertificate(chainPEM)
		issuer := m.issuer.IssuerKey()
		if leaf != nil && len(keyPEM) > 0 && !e.storage.Exists(ctx, certmagic.StorageKeys.SiteCert(issuer, host)) {
			meta, _ := json.Marshal(certmagic.CertificateResource{SANs: leaf.DNSNames})
			for name, value := range map[string][]byte{
				certmagic.StorageKeys.SitePrivateKey(issuer, host): keyPEM,
				certmagic.StorageKeys.SiteCert(issuer, host):       chainPEM,
				certmagic.StorageKeys.SiteMeta(issuer, host):       meta,
			} {
				if err = e.storage.Store(ctx, name, value); err != nil {
					break
				}
			}
			if err != nil {
				e.log.Warn("could not carry over the certificate for %s: %v", host, err)
				continue
			}
			e.log.Info("carried over the acme certificate for %s", host)
		}
		_ = e.store.DeleteACMECacheEntry(ctx, key)
	}
}
