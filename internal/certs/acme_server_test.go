package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/settings"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/acme"
	"google.golang.org/protobuf/proto"
)

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// Engine plus enabled internal acme and a seeded app root ca,
// returns the root pem so tests can verify the issued chain
func newACMEEngine(t *testing.T) (*Engine, *settings.Resolver, string) {
	t.Helper()
	store := newTestStore(t)
	res := settings.NewResolver(store, nil)
	seedSystem(t, res, &v1.Settings{
		Server: &v1.ServerSettings{PublicHostname: proto.String("ca.example.com")},
		Acme:   &v1.ACMESettings{InternalEnabled: proto.Bool(true)},
	}, "server.public_hostname", "acme.internal_enabled")
	e := newTestEngine(t, store, res, "", "")

	rootPEM, rootKey, err := GenerateRootCA("distroface test root")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTLSCertificate(context.Background(), &storage.TLSCertificate{
		Scope: v1.TLSScope_TLS_SCOPE_APP_CA, CertPEM: rootPEM, KeyPEM: rootKey,
	}); err != nil {
		t.Fatal(err)
	}
	return e, res, rootPEM
}

// Registers and approves a system scope issuable domain
func approveDomain(t *testing.T, e *Engine, domain string) {
	t.Helper()
	if err := e.store.CreateCertificateDomain(context.Background(), &storage.CertificateDomain{
		Domain: domain, Scope: v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM,
		Approved: true, ApprovedBy: "admin", CreatedBy: "admin",
	}); err != nil {
		t.Fatal(err)
	}
}

// Points the acme server's validators at a local responder
func rewriteValidation(s *ACMEServer, target string) {
	s.dial = func(network, _ string) (net.Conn, error) { return net.Dial(network, target) }
}

func TestACMEFullIssuanceHTTP01(t *testing.T) {
	e, _, rootPEM := newACMEEngine(t)
	srv := NewACMEServer(e)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	domain := "issued.example.com"
	approveDomain(t, e, domain)

	// Local http-01 responder standing in for the client's server
	var keyAuthMu struct {
		token string
		auth  string
	}
	responder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			io.WriteString(w, keyAuthMu.auth)
			return
		}
		http.NotFound(w, r)
	}))
	defer responder.Close()
	rewriteValidation(srv, strings.TrimPrefix(responder.URL, "http://"))

	accountKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	client := &acme.Client{Key: accountKey, DirectoryURL: ts.URL + "/acme/directory"}
	ctx := context.Background()

	if _, err := client.Register(ctx, &acme.Account{Contact: []string{"mailto:ops@example.com"}}, acme.AcceptTOS); err != nil {
		t.Fatalf("register: %v", err)
	}

	order, err := client.AuthorizeOrder(ctx, []acme.AuthzID{{Type: "dns", Value: domain}})
	if err != nil {
		t.Fatalf("authorize order: %v", err)
	}

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			t.Fatalf("get authz: %v", err)
		}
		var chal *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "http-01" {
				chal = c
			}
		}
		if chal == nil {
			t.Fatal("no http-01 challenge offered")
		}
		resp, err := client.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			t.Fatal(err)
		}
		keyAuthMu.token = chal.Token
		keyAuthMu.auth = resp

		if _, err := client.Accept(ctx, chal); err != nil {
			t.Fatalf("accept challenge: %v", err)
		}
		if _, err := client.WaitAuthorization(ctx, authzURL); err != nil {
			t.Fatalf("wait authorization: %v", err)
		}
	}

	csrKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, csrKey)
	if err != nil {
		t.Fatal(err)
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(ders) == 0 {
		t.Fatal("empty certificate chain")
	}

	leaf, err := x509.ParseCertificate(ders[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != domain {
		t.Errorf("leaf sans = %v, want [%s]", leaf.DNSNames, domain)
	}

	// The chain must verify to the instance root
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(rootPEM)) {
		t.Fatal("bad root pem")
	}
	inters := x509.NewCertPool()
	for _, der := range ders[1:] {
		ic, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		inters.AddCert(ic)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters}); err != nil {
		t.Errorf("issued leaf does not chain to the instance root: %v", err)
	}
}

func TestACMETLSALPN01Digest(t *testing.T) {
	e, _, _ := newACMEEngine(t)
	srv := NewACMEServer(e)

	domain := "alpn.example.com"
	token := "tok123"
	thumb := "thumbprint"
	keyAuth := token + "." + thumb

	// Build a challenge cert exactly as a compliant client would
	sum := sha256.Sum256([]byte(keyAuth))
	extVal, err := asn1.Marshal(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtraExtensions: []pkix.Extension{{
			Id: idPeACMEIdentifier, Critical: true, Value: extVal,
		}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	chalCert := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}

	// Stand up a tls-alpn responder and validate against it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				tlsConn := tls.Server(conn, &tls.Config{
					NextProtos: []string{"acme-tls/1"},
					GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
						return chalCert, nil
					},
				})
				_ = tlsConn.Handshake()
				tlsConn.Close()
			}()
		}
	}()

	srv.dial = func(network, _ string) (net.Conn, error) { return net.Dial(network, ln.Addr().String()) }
	if err := srv.validateTLSALPN01(domain, keyAuth); err != nil {
		t.Errorf("tls-alpn-01 validation failed: %v", err)
	}

	// A digest for a different key authorization must be rejected
	if err := srv.validateTLSALPN01(domain, "other."+thumb); err == nil {
		t.Error("tls-alpn-01 accepted a mismatched digest")
	}
}

func TestACMEDisabledReturnsProblem(t *testing.T) {
	e, res, _ := newACMEEngine(t)
	seedSystem(t, res, &v1.Settings{
		Acme: &v1.ACMESettings{InternalEnabled: proto.Bool(false)},
	}, "acme.internal_enabled")
	srv := NewACMEServer(e)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/acme/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled directory = %d, want 503", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content type = %q, want problem+json", ct)
	}
}

func TestACMEBadNonceRejected(t *testing.T) {
	e, _, _ := newACMEEngine(t)
	srv := NewACMEServer(e)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	accountKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	client := &acme.Client{Key: accountKey, DirectoryURL: ts.URL + "/acme/directory"}
	ctx := context.Background()
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil {
		t.Fatalf("register: %v", err)
	}

	// A hand crafted post carrying a bogus nonce must be refused
	badReq := `{"protected":"` + b64(`{"alg":"ES256","kid":"x","nonce":"deadbeef","url":"`+
		ts.URL+`/acme/new-order"}`) + `","payload":"","signature":"AAAA"}`
	resp, err := http.Post(ts.URL+"/acme/new-order", "application/jose+json", strings.NewReader(badReq))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad nonce request = %d, want 400", resp.StatusCode)
	}
}

func TestACMEPolicyRejectsUnapprovedIdentifier(t *testing.T) {
	e, _, _ := newACMEEngine(t)
	srv := NewACMEServer(e)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// This domain is never registered, so validation and policy deny it
	domain := "denied.example.com"
	responder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "wrong-value")
	}))
	defer responder.Close()
	rewriteValidation(srv, strings.TrimPrefix(responder.URL, "http://"))

	accountKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	client := &acme.Client{Key: accountKey, DirectoryURL: ts.URL + "/acme/directory"}
	ctx := context.Background()
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil {
		t.Fatal(err)
	}
	order, err := client.AuthorizeOrder(ctx, []acme.AuthzID{{Type: "dns", Value: domain}})
	if err != nil {
		t.Fatal(err)
	}
	authz, err := client.GetAuthorization(ctx, order.AuthzURLs[0])
	if err != nil {
		t.Fatal(err)
	}
	var chal *acme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "http-01" {
			chal = c
		}
	}
	if _, err := client.Accept(ctx, chal); err != nil {
		t.Fatal(err)
	}
	// The authorization must resolve to invalid, never valid
	if _, err := client.WaitAuthorization(ctx, order.AuthzURLs[0]); err == nil {
		t.Error("expected authorization to fail for an unapproved identifier")
	}
}
