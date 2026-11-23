package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/miekg/dns"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/acme"
	"google.golang.org/protobuf/proto"
)

func listenerPort(t *testing.T, ln net.Listener) string {
	t.Helper()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// Client engine wired to a private directory over real listeners
func newACMEClient(t *testing.T, host string, extra *v1.ACMESettings, paths ...string) (client, ca *Engine, tlsAddr, rootPEM string) {
	t.Helper()
	ca, _, rootPEM = newACMEEngine(t)
	srv := NewACMEServer(ca)
	directory := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(directory.Close)
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: directory.Certificate().Raw})
	approveDomain(t, ca, host)

	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	client = newTestEngine(t, store, res, "", "")
	challenge := httptest.NewServer(client.HTTPChallengeHandler())
	t.Cleanup(challenge.Close)
	front := httptest.NewUnstartedServer(http.NotFoundHandler())
	front.TLS = client.TLSConfig()
	front.StartTLS()
	t.Cleanup(front.Close)
	client.SetListenPort(listenerPort(t, front.Listener))

	// Ports 80 and 443 both land on the client
	srv.dial = func(network, addr string) (net.Conn, error) {
		target := challenge.Listener.Addr().String()
		if _, port, _ := net.SplitHostPort(addr); port == "443" {
			target = front.Listener.Addr().String()
		}
		return net.Dial(network, target)
	}
	acmeSettings := &v1.ACMESettings{
		Enabled:       proto.Bool(true),
		Email:         proto.String("ops@example.com"),
		DirectoryUrl:  proto.String(directory.URL + "/acme/directory"),
		ChallengePort: proto.String(listenerPort(t, challenge.Listener)),
		CaBundle:      proto.String(string(bundle)),
	}
	if extra != nil {
		proto.Merge(acmeSettings, extra)
	}
	seedSystem(t, res, &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String(host)},
		Tls:    &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_ACME.Enum()},
		Acme:   acmeSettings,
	}, append([]string{"server.public_hostname", "tls.primary_source", "acme.enabled", "acme.email",
		"acme.directory_url", "acme.challenge_port", "acme.ca_bundle"}, paths...)...)
	return client, ca, front.Listener.Addr().String(), rootPEM
}

func handshakeLeaf(t *testing.T, addr, host, rootPEM string) *x509.Certificate {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(rootPEM)) {
		t.Fatal("root pem did not parse")
	}
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, RootCAs: roots})
	if err != nil {
		t.Fatalf("handshake for %s: %v", host, err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0]
}

func TestACMEClientIssuesOnDemand(t *testing.T) {
	host := "client.example.com"
	client, _, addr, rootPEM := newACMEClient(t, host, nil)
	ctx := context.Background()

	info, err := client.EnsureCertificate(ctx, host)
	if err != nil {
		t.Fatalf("EnsureCertificate: %v", err)
	}
	if !info.GetIssued() || len(info.GetSans()) != 1 || info.GetSans()[0] != host {
		t.Fatalf("unexpected issued info %+v", info)
	}
	if st := client.AppStatus(ctx); st.GetState() != v1.CertState_CERT_STATE_READY {
		t.Fatalf("app status %v %v", st.GetState(), st.GetProblems())
	}
	leaf := handshakeLeaf(t, addr, host, rootPEM)
	if err := leaf.VerifyHostname(host); err != nil {
		t.Fatal(err)
	}

	// A fresh cert keeps the sweep from ordering
	if m := client.managerForHost(ctx, host); client.renewalDue(ctx, m, host) {
		t.Fatal("sweep wants to renew a certificate issued a moment ago")
	}
	if got := client.renewInterval(ctx); got != 10*time.Minute {
		t.Fatalf("default sweep interval %v", got)
	}

	// A second call finds the stored cert and orders nothing
	again, err := client.EnsureCertificate(ctx, host)
	if err != nil || again.GetNotAfter().AsTime() != info.GetNotAfter().AsTime() {
		t.Fatalf("repeat EnsureCertificate reissued: %v %v", again, err)
	}
}

func TestACMEClientIssuesDuringHandshake(t *testing.T) {
	host := "handshake.example.com"
	_, _, addr, rootPEM := newACMEClient(t, host, nil)
	if leaf := handshakeLeaf(t, addr, host, rootPEM); leaf.VerifyHostname(host) != nil {
		t.Fatalf("handshake served a cert for %v", leaf.DNSNames)
	}
}

func TestACMEClientRefusesUnconfiguredHost(t *testing.T) {
	client, _, addr, _ := newACMEClient(t, "client.example.com", nil)
	if _, err := client.EnsureCertificate(context.Background(), "stranger.example.com"); err == nil {
		t.Fatal("issued for a host nothing configures")
	}
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "stranger.example.com", InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
		t.Fatal("handshake succeeded for a host nothing configures")
	}
}

func TestACMEClientReusesAccountKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	// Other clients export the key as bare base64 der
	exported := base64.StdEncoding.EncodeToString(der)

	host := "imported.example.com"
	client, ca, _, _ := newACMEClient(t, host, &v1.ACMESettings{AccountKey: proto.String(exported)}, "acme.account_key")
	ctx := context.Background()

	// An unknown key is looked up, never silently registered
	if _, err := client.EnsureCertificate(ctx, host); err == nil || !strings.Contains(err.Error(), "accountDoesNotExist") {
		t.Fatalf("order with an unregistered key = %v, want accountDoesNotExist", err)
	}

	thumbprint, err := acme.JWKThumbprint(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	coord := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.FillBytes(make([]byte, 32))) }
	jwk := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":"%s","y":"%s"}`, coord(key.X), coord(key.Y))
	if err := ca.store.CreateACMEAccount(ctx, &storage.ACMEAccount{ID: thumbprint, PublicKeyJSON: jwk, Contacts: "[]", Status: "valid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureCertificate(ctx, host); err != nil {
		t.Fatalf("order with the registered key: %v", err)
	}
}

// One org portal under a registered wildcard
func TestCertSubjectUsesCoveringWildcard(t *testing.T) {
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	dnsChallenge := &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String("registry.eng.example.com")},
		Acme: &v1.ACMESettings{
			Enabled:   proto.Bool(true),
			Challenge: v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum(),
			Dns: &v1.ACMEDNSSettings{
				Provider: v1.DNSProvider_DNS_PROVIDER_CLOUDFLARE.Enum(),
				ApiToken: proto.String("token"),
			},
		},
	}
	seedSystem(t, res, dnsChallenge, "server.public_hostname", "acme.enabled", "acme.challenge", "acme.dns.provider", "acme.dns.api_token")
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()

	orgA := &storage.Organization{Name: "a", CreatedBy: "t"}
	orgB := &storage.Organization{Name: "b", CreatedBy: "t"}
	for _, o := range []*storage.Organization{orgA, orgB} {
		if err := store.CreateOrganization(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	domains := []*storage.CertificateDomain{
		{Domain: "*.eng.example.com", Scope: v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM, Approved: true, CreatedBy: "t"},
		{Domain: "*.b.example.com", Scope: v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_ORG, OrgID: &orgB.ID, Approved: true, CreatedBy: "t"},
	}
	for _, d := range domains {
		if err := store.CreateCertificateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []*storage.RegistryPortal{
		{OrgID: orgA.ID, Name: "shared", Hostname: "docker.eng.example.com", Rules: "[]", CertSource: v1.CertSource_CERT_SOURCE_ACME, Enabled: true},
		{OrgID: orgA.ID, Name: "foreign", Hostname: "x.b.example.com", Rules: "[]", CertSource: v1.CertSource_CERT_SOURCE_ACME, Enabled: true},
		{OrgID: orgB.ID, Name: "own", Hostname: "y.b.example.com", Rules: "[]", CertSource: v1.CertSource_CERT_SOURCE_ACME, Enabled: true},
	} {
		if err := store.CreateRegistryPortal(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	subject := func(host string) string {
		m := e.managerForHost(ctx, host)
		if m == nil {
			t.Fatalf("no manager for %s", host)
		}
		return e.certSubject(ctx, m, host)
	}
	for host, want := range map[string]string{
		"registry.eng.example.com": "*.eng.example.com",
		"docker.eng.example.com":   "*.eng.example.com",
		"y.b.example.com":          "*.b.example.com",
		"x.b.example.com":          "x.b.example.com",
		"deep.a.eng.example.com":   "deep.a.eng.example.com",
	} {
		if got := subject(host); got != want {
			t.Errorf("subject for %s = %s, want %s", host, got, want)
		}
	}

	// Without dns-01 hosts order their own
	seedSystem(t, res, &v1.Settings{Acme: &v1.ACMESettings{Challenge: v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_UNSPECIFIED.Enum()}}, "acme.challenge")
	e.InvalidateLocal()
	if got := subject("docker.eng.example.com"); got != "docker.eng.example.com" {
		t.Errorf("wildcard subject %s resolved without dns-01", got)
	}
	if _, err := e.EnsureCertificate(ctx, "*.eng.example.com"); err == nil || !strings.Contains(err.Error(), "dns-01 challenge") {
		t.Errorf("wildcard order without dns = %v, want a dns-01 challenge error", err)
	}
}

func TestRFC2136PublishesChallengeRecord(t *testing.T) {
	const keyName, secret = "distroface.", "c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0"
	updates := make(chan dns.RR, 1)
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		reply := new(dns.Msg).SetReply(r)
		if r.Opcode != dns.OpcodeUpdate || r.IsTsig() == nil || w.TsigStatus() != nil {
			reply.Rcode = dns.RcodeRefused
		} else {
			reply.SetTsig(keyName, dns.HmacSHA256, 300, time.Now().Unix())
			for _, rr := range r.Ns {
				updates <- rr
			}
		}
		_ = w.WriteMsg(reply)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{
		Listener: ln, Handler: handler, TsigSecret: map[string]string{keyName: secret},
		MsgAcceptFunc: func(dns.Header) dns.MsgAcceptAction { return dns.MsgAccept },
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	provider, err := dnsProvider(&v1.ACMEDNSSettings{
		Provider:    v1.DNSProvider_DNS_PROVIDER_RFC2136.Enum(),
		Server:      proto.String(ln.Addr().String()),
		TsigKeyName: proto.String("distroface"),
		TsigSecret:  proto.String(secret),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := provider.AppendRecords(ctx, "eng.example.com.", []libdns.Record{
		libdns.TXT{Name: "_acme-challenge", Text: "digest", TTL: time.Minute},
	}); err != nil {
		t.Fatalf("AppendRecords: %v", err)
	}
	select {
	case rr := <-updates:
		txt, ok := rr.(*dns.TXT)
		if !ok || txt.Hdr.Name != "_acme-challenge.eng.example.com." || len(txt.Txt) != 1 || txt.Txt[0] != "digest" {
			t.Fatalf("unexpected update record %v", rr)
		}
	case <-ctx.Done():
		t.Fatal("nameserver saw no signed update")
	}

	if _, err := dnsProvider(&v1.ACMEDNSSettings{}); err == nil {
		t.Error("an unset provider must not build a client")
	}
}

// Settings that cannot issue fail before any order
func TestBrokenChallengeSettingsSurface(t *testing.T) {
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	seedSystem(t, res, &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String("registry.example.com")},
		Tls:    &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_ACME.Enum()},
		Acme: &v1.ACMESettings{
			Enabled:   proto.Bool(true),
			Challenge: v1.ACMEChallengeType_ACME_CHALLENGE_TYPE_DNS_01.Enum(),
		},
	}, "server.public_hostname", "tls.primary_source", "acme.enabled", "acme.challenge")
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()

	if _, err := e.EnsureCertificate(ctx, "registry.example.com"); err == nil || !strings.Contains(err.Error(), "no dns provider") {
		t.Fatalf("order without a dns provider = %v", err)
	}
	st := e.AppStatus(ctx)
	if st.GetState() != v1.CertState_CERT_STATE_ERROR || !strings.Contains(strings.Join(st.GetProblems(), " "), "no dns provider") {
		t.Fatalf("status hides the broken settings: %v %v", st.GetState(), st.GetProblems())
	}
}

func TestImportLegacyCerts(t *testing.T) {
	store := newTestStore(t)
	res := newTestResolver(t, store)
	seedSystem(t, res, &v1.Settings{Tls: &v1.TLSSettings{PrimarySource: v1.CertSource_CERT_SOURCE_ACME.Enum()}}, "tls.primary_source")
	e := newTestEngine(t, store, res, "", "")
	ctx := context.Background()
	host := "registry.example.com"
	expiry := time.Now().Add(60 * 24 * time.Hour)
	for key, data := range map[string][]byte{
		host:               selfSignedPEM(t, host, expiry),
		"acme_account+key": []byte("old account key"),
		host + "+token":    []byte("old challenge"),
	} {
		if err := store.PutACMECacheEntry(ctx, key, data); err != nil {
			t.Fatal(err)
		}
	}
	e.importLegacyCerts(ctx)

	info, err := e.CertificateInfo(ctx, host)
	if err != nil || !info.GetIssued() {
		t.Fatalf("carried over cert not readable: %+v %v", info, err)
	}
	cert, err := e.getCertificate(&tls.ClientHelloInfo{ServerName: host})
	if err != nil {
		t.Fatalf("handshake did not load the carried over cert: %v", err)
	}
	if leaf := leafOf(t, cert); leaf.Subject.CommonName != host {
		t.Fatalf("served %q", leaf.Subject.CommonName)
	}
	keys, err := store.ListACMECacheKeys(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if !strings.Contains(k, "/") {
			t.Errorf("legacy row %q survived the import", k)
		}
	}
}
