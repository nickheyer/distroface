package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
)

// Built in issued cert lifetime
const acmeLeafDays = 90

// Bounds in flight order and nonce retention
const (
	acmeOrderTTL = 1 * time.Hour
	acmeNonceTTL = 1 * time.Hour
	acmeMaxCSR   = 1 << 16
)

// tls-alpn-01 challenge extension oid from rfc 8737
var idPeACMEIdentifier = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 31}

// ACMEServer answers the built in acme directory backed by the app ca
type ACMEServer struct {
	e *Engine

	// Validation targets, overridable for tests
	httpPort int
	tlsPort  int
	dial     func(network, addr string) (net.Conn, error)

	mu      sync.Mutex
	nonces  map[string]time.Time
	orders  map[string]*acmeOrder
	authzs  map[string]*acmeAuthz
	counter uint64
}

type acmeAuthz struct {
	id      string
	domain  string
	status  string // pending, valid, invalid
	token   string
	account string
	orderID string
	expires time.Time
	problem string
}

type acmeOrder struct {
	id          string
	account     string
	identifiers []string
	authzIDs    []string
	status      string // pending, ready, processing, valid, invalid
	certPEM     string
	expires     time.Time
	problem     string
}

func NewACMEServer(e *Engine) *ACMEServer {
	return &ACMEServer{
		e:        e,
		httpPort: 80,
		tlsPort:  443,
		dial:     safeChallengeDial,
		nonces:   map[string]time.Time{},
		orders:   map[string]*acmeOrder{},
		authzs:   map[string]*acmeAuthz{},
	}
}

// Enabled reflects the live built in acme kill switch
func (s *ACMEServer) Enabled(ctx context.Context) bool {
	return s.e.res.System(ctx).GetAcme().GetInternalEnabled()
}

// DirectoryURL is the public url clients point their acme client at
func (s *ACMEServer) DirectoryURL(ctx context.Context) string {
	return "https://" + bareHost(s.e.res.System(ctx).GetServer().GetPublicHostname()) + "/acme/directory"
}

// Absolute urls follow how the client reached us so signatures and
// redirects stay consistent behind any proxy or listener
func (s *ACMEServer) baseURL(r *http.Request) string {
	return s.scheme(r) + "://" + r.Host + "/acme"
}

func (s *ACMEServer) scheme(r *http.Request) string {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}

// Handler serves the /acme/ subtree, gating on the live toggle
func (s *ACMEServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/acme/directory", s.handleDirectory)
	mux.HandleFunc("/acme/new-nonce", s.handleNewNonce)
	mux.HandleFunc("/acme/new-account", s.handleNewAccount)
	mux.HandleFunc("/acme/new-order", s.handleNewOrder)
	mux.HandleFunc("/acme/authz/", s.handleAuthz)
	mux.HandleFunc("/acme/challenge/", s.handleChallenge)
	mux.HandleFunc("/acme/finalize/", s.handleFinalize)
	mux.HandleFunc("/acme/cert/", s.handleCert)
	mux.HandleFunc("/acme/acct/", s.handleAccount)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Enabled(r.Context()) {
			s.problem(w, http.StatusServiceUnavailable, "unauthorized", "the built in acme server is disabled")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ── Directory and nonces ────────────────────────────────────────────────

func (s *ACMEServer) handleDirectory(w http.ResponseWriter, r *http.Request) {
	base := s.baseURL(r)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"newNonce":   base + "/new-nonce",
		"newAccount": base + "/new-account",
		"newOrder":   base + "/new-order",
		"revokeCert": base + "/revoke-cert",
		"keyChange":  base + "/key-change",
	})
}

func (s *ACMEServer) handleNewNonce(w http.ResponseWriter, r *http.Request) {
	s.setNonce(w, r)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *ACMEServer) newNonce() string {
	b := make([]byte, 16)
	_, _ = io.ReadFull(rand.Reader, b)
	nonce := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.gcLocked()
	s.nonces[nonce] = time.Now().Add(acmeNonceTTL)
	s.mu.Unlock()
	return nonce
}

func (s *ACMEServer) consumeNonce(nonce string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.nonces[nonce]
	if !ok || time.Now().After(exp) {
		delete(s.nonces, nonce)
		return false
	}
	delete(s.nonces, nonce)
	return true
}

func (s *ACMEServer) setNonce(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Replay-Nonce", s.newNonce())
	w.Header().Set("Cache-Control", "no-store")
	_ = r
}

// Drops expired nonces, orders, and authorizations
func (s *ACMEServer) gcLocked() {
	now := time.Now()
	for k, exp := range s.nonces {
		if now.After(exp) {
			delete(s.nonces, k)
		}
	}
	for k, o := range s.orders {
		if now.After(o.expires) {
			delete(s.orders, k)
		}
	}
	for k, a := range s.authzs {
		if now.After(a.expires) {
			delete(s.authzs, k)
		}
	}
}

// ── Account ─────────────────────────────────────────────────────────────

func (s *ACMEServer) handleNewAccount(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, false)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	var payload struct {
		Contact              []string `json:"contact"`
		OnlyReturnExisting   bool     `json:"onlyReturnExisting"`
		TermsOfServiceAgreed bool     `json:"termsOfServiceAgreed"`
	}
	if len(jws.payload) > 0 {
		if err := json.Unmarshal(jws.payload, &payload); err != nil {
			s.problem(w, http.StatusBadRequest, "malformed", "invalid account payload")
			return
		}
	}

	ctx := r.Context()
	existing, err := s.e.store.GetACMEAccount(ctx, jws.thumbprint)
	if err != nil {
		s.problem(w, http.StatusInternalServerError, "serverInternal", err.Error())
		return
	}
	acctURL := s.baseURL(r) + "/acct/" + jws.thumbprint
	if existing != nil {
		w.Header().Set("Location", acctURL)
		s.writeJSON(w, r, http.StatusOK, s.accountBody(existing))
		return
	}
	if payload.OnlyReturnExisting {
		s.problem(w, http.StatusBadRequest, "accountDoesNotExist", "no account for this key")
		return
	}

	contacts, _ := json.Marshal(payload.Contact)
	acct := &storage.ACMEAccount{
		ID:            jws.thumbprint,
		PublicKeyJSON: jws.jwkJSON,
		Contacts:      string(contacts),
		Status:        "valid",
	}
	if err := s.e.store.CreateACMEAccount(ctx, acct); err != nil {
		s.problem(w, http.StatusInternalServerError, "serverInternal", err.Error())
		return
	}
	s.e.log.Info("acme account registered %s", jws.thumbprint)
	w.Header().Set("Location", acctURL)
	s.writeJSON(w, r, http.StatusCreated, s.accountBody(acct))
}

func (s *ACMEServer) handleAccount(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, true)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	acct, err := s.e.store.GetACMEAccount(r.Context(), jws.accountID)
	if err != nil || acct == nil {
		s.problem(w, http.StatusBadRequest, "accountDoesNotExist", "unknown account")
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.accountBody(acct))
}

func (s *ACMEServer) accountBody(a *storage.ACMEAccount) map[string]any {
	var contacts []string
	_ = json.Unmarshal([]byte(a.Contacts), &contacts)
	return map[string]any{
		"status":  a.Status,
		"contact": contacts,
		"orders":  "",
	}
}

// ── Orders ──────────────────────────────────────────────────────────────

func (s *ACMEServer) handleNewOrder(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, true)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	if err := s.requireAccount(r.Context(), jws.accountID); err != nil {
		s.problem(w, http.StatusBadRequest, "accountDoesNotExist", err.Error())
		return
	}
	var payload struct {
		Identifiers []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"identifiers"`
	}
	if err := json.Unmarshal(jws.payload, &payload); err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", "invalid order payload")
		return
	}
	if len(payload.Identifiers) == 0 {
		s.problem(w, http.StatusBadRequest, "malformed", "order needs at least one identifier")
		return
	}

	order := &acmeOrder{
		id:      s.nextID("order"),
		account: jws.accountID,
		status:  "pending",
		expires: time.Now().Add(acmeOrderTTL),
	}
	for _, id := range payload.Identifiers {
		if id.Type != "dns" {
			s.problem(w, http.StatusBadRequest, "rejectedIdentifier", "only dns identifiers are supported")
			return
		}
		authz := &acmeAuthz{
			id:      s.nextID("authz"),
			domain:  strings.ToLower(id.Value),
			status:  "pending",
			token:   s.randToken(),
			account: jws.accountID,
			orderID: order.id,
			expires: order.expires,
		}
		order.identifiers = append(order.identifiers, authz.domain)
		order.authzIDs = append(order.authzIDs, authz.id)
		s.mu.Lock()
		s.authzs[authz.id] = authz
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.orders[order.id] = order
	s.mu.Unlock()

	w.Header().Set("Location", s.orderURL(r, order.id))
	s.writeJSON(w, r, http.StatusCreated, s.orderBody(r, order))
}

func (s *ACMEServer) orderURL(r *http.Request, id string) string {
	return s.baseURL(r) + "/order/" + id
}

func (s *ACMEServer) orderBody(r *http.Request, o *acmeOrder) map[string]any {
	base := s.baseURL(r)
	ids := make([]map[string]string, 0, len(o.identifiers))
	for _, d := range o.identifiers {
		ids = append(ids, map[string]string{"type": "dns", "value": d})
	}
	authzURLs := make([]string, 0, len(o.authzIDs))
	for _, a := range o.authzIDs {
		authzURLs = append(authzURLs, base+"/authz/"+a)
	}
	body := map[string]any{
		"status":         o.status,
		"expires":        o.expires.UTC().Format(time.RFC3339),
		"identifiers":    ids,
		"authorizations": authzURLs,
		"finalize":       base + "/finalize/" + o.id,
	}
	if o.certPEM != "" {
		body["certificate"] = base + "/cert/" + o.id
	}
	if o.problem != "" {
		body["error"] = s.problemDoc("rejectedIdentifier", o.problem)
	}
	return body
}

// ── Authorizations and challenges ───────────────────────────────────────

func (s *ACMEServer) handleAuthz(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, true)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/acme/authz/")
	s.mu.Lock()
	authz := s.authzs[id]
	s.mu.Unlock()
	if authz == nil || authz.account != jws.accountID {
		s.problem(w, http.StatusNotFound, "malformed", "unknown authorization")
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.authzBody(r, authz))
}

func (s *ACMEServer) authzBody(r *http.Request, a *acmeAuthz) map[string]any {
	base := s.baseURL(r)
	chalURL := base + "/challenge/" + a.id
	challenges := []map[string]any{
		{"type": "http-01", "url": chalURL + "/http-01", "token": a.token, "status": a.status},
		{"type": "tls-alpn-01", "url": chalURL + "/tls-alpn-01", "token": a.token, "status": a.status},
	}
	body := map[string]any{
		"identifier": map[string]string{"type": "dns", "value": a.domain},
		"status":     a.status,
		"expires":    a.expires.UTC().Format(time.RFC3339),
		"challenges": challenges,
	}
	if a.problem != "" {
		body["error"] = s.problemDoc("unauthorized", a.problem)
	}
	return body
}

func (s *ACMEServer) handleChallenge(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, true)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/acme/challenge/")
	authzID, chalType, ok := strings.Cut(rest, "/")
	if !ok {
		s.problem(w, http.StatusNotFound, "malformed", "unknown challenge")
		return
	}
	s.mu.Lock()
	authz := s.authzs[authzID]
	s.mu.Unlock()
	if authz == nil || authz.account != jws.accountID {
		s.problem(w, http.StatusNotFound, "malformed", "unknown challenge")
		return
	}

	keyAuth := authz.token + "." + jws.thumbprint
	// A POST-as-GET just reports status, a payload triggers validation
	if len(jws.payload) > 0 && string(jws.payload) != "{}" {
		s.problem(w, http.StatusBadRequest, "malformed", "unexpected challenge payload")
		return
	}
	// Flip out of pending under the lock so repeated posts spawn one validator
	s.mu.Lock()
	launch := authz.status == "pending"
	if launch {
		authz.status = "processing"
	}
	s.mu.Unlock()
	if launch {
		go s.validate(context.Background(), authz.id, chalType, authz.domain, keyAuth)
	}

	base := s.baseURL(r)
	w.Header().Set("Link", fmt.Sprintf(`<%s/authz/%s>;rel="up"`, base, authz.id))
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"type":   chalType,
		"url":    base + "/challenge/" + authz.id + "/" + chalType,
		"token":  authz.token,
		"status": s.authzStatus(authz.id),
	})
}

func (s *ACMEServer) authzStatus(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.authzs[id]; a != nil {
		return a.status
	}
	return "invalid"
}

// Reaches out to the client, then marks the authz and order
func (s *ACMEServer) validate(ctx context.Context, authzID, chalType, domain, keyAuth string) {
	// Policy gates before any outbound dial so blocked hosts are never contacted
	err := s.e.hostPolicy(ctx, domain)
	if err != nil {
		err = fmt.Errorf("host not allowed: %w", err)
	}
	if err == nil {
		switch chalType {
		case "http-01":
			err = s.validateHTTP01(domain, keyAuth)
		case "tls-alpn-01":
			err = s.validateTLSALPN01(domain, keyAuth)
		default:
			err = fmt.Errorf("unsupported challenge type %q", chalType)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	authz := s.authzs[authzID]
	if authz == nil {
		return
	}
	if err != nil {
		authz.status = "invalid"
		authz.problem = err.Error()
		if o := s.orders[authz.orderID]; o != nil {
			o.status = "invalid"
			o.problem = err.Error()
		}
		s.e.log.Warn("acme validation failed for %s: %v", domain, err)
		return
	}
	authz.status = "valid"
	s.e.log.Info("acme validation succeeded for %s", domain)
	if o := s.orders[authz.orderID]; o != nil {
		allValid := true
		for _, aid := range o.authzIDs {
			if a := s.authzs[aid]; a == nil || a.status != "valid" {
				allValid = false
				break
			}
		}
		if allValid && o.status == "pending" {
			o.status = "ready"
		}
	}
}

// Dials a challenge target rejecting loopback, metadata, and multicast
// at connect time, private enclave ranges stay reachable on purpose
func safeChallengeDial(network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("invalid dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("dial address %q is not an ip", host)
			}
			switch {
			case ip.IsLoopback():
				return fmt.Errorf("challenge target %s is loopback", ip)
			case ip.IsUnspecified():
				return fmt.Errorf("challenge target %s is unspecified", ip)
			case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
				return fmt.Errorf("challenge target %s is link-local (metadata range)", ip)
			case ip.IsMulticast():
				return fmt.Errorf("challenge target %s is multicast", ip)
			}
			return nil
		},
	}
	return dialer.Dial(network, addr)
}

func (s *ACMEServer) validateHTTP01(domain, keyAuth string) error {
	addr := net.JoinHostPort(domain, fmt.Sprint(s.httpPort))
	conn, err := s.dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial http-01 target: %w", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		},
		Timeout: 10 * time.Second,
	}
	url := "http://" + domain + "/.well-known/acme-challenge/" + strings.SplitN(keyAuth, ".", 2)[0]
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetch http-01 token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) != keyAuth {
		return fmt.Errorf("http-01 key authorization mismatch")
	}
	return nil
}

func (s *ACMEServer) validateTLSALPN01(domain, keyAuth string) error {
	addr := net.JoinHostPort(domain, fmt.Sprint(s.tlsPort))
	conn, err := s.dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial tls-alpn-01 target: %w", err)
	}
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: true,
		NextProtos:         []string{"acme-tls/1"},
	})
	defer tlsConn.Close()
	tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("tls-alpn-01 handshake: %w", err)
	}
	state := tlsConn.ConnectionState()
	if state.NegotiatedProtocol != "acme-tls/1" {
		return fmt.Errorf("tls-alpn-01 wrong alpn %q", state.NegotiatedProtocol)
	}
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("tls-alpn-01 no certificate presented")
	}
	want := sha256.Sum256([]byte(keyAuth))
	return verifyACMEIdentifier(state.PeerCertificates[0], domain, want[:])
}

// Confirms the challenge cert carries the expected key auth digest
func verifyACMEIdentifier(cert *x509.Certificate, domain string, want []byte) error {
	if err := cert.VerifyHostname(domain); err != nil {
		return fmt.Errorf("tls-alpn-01 wrong san: %w", err)
	}
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(idPeACMEIdentifier) {
			continue
		}
		var got []byte
		if _, err := asn1.Unmarshal(ext.Value, &got); err != nil {
			return fmt.Errorf("tls-alpn-01 bad extension: %w", err)
		}
		if subtle.ConstantTimeCompare(got, want) == 1 {
			return nil
		}
		return fmt.Errorf("tls-alpn-01 digest mismatch")
	}
	return fmt.Errorf("tls-alpn-01 challenge extension missing")
}

// ── Finalize and certificate ────────────────────────────────────────────

func (s *ACMEServer) handleFinalize(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, true)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/acme/finalize/")
	s.mu.Lock()
	order := s.orders[id]
	s.mu.Unlock()
	if order == nil || order.account != jws.accountID {
		s.problem(w, http.StatusNotFound, "malformed", "unknown order")
		return
	}
	if order.status != "ready" {
		s.problem(w, http.StatusForbidden, "orderNotReady", "order is not ready to finalize")
		return
	}

	var payload struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(jws.payload, &payload); err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", "invalid finalize payload")
		return
	}
	der, err := base64.RawURLEncoding.DecodeString(payload.CSR)
	if err != nil || len(der) > acmeMaxCSR {
		s.problem(w, http.StatusBadRequest, "badCSR", "invalid csr encoding")
		return
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil || csr.CheckSignature() != nil {
		s.problem(w, http.StatusBadRequest, "badCSR", "invalid csr")
		return
	}
	if err := csrMatchesOrder(csr, order.identifiers); err != nil {
		s.problem(w, http.StatusBadRequest, "badCSR", err.Error())
		return
	}

	signed, err := s.e.SignACMELeaf(r.Context(), csr.PublicKey, order.identifiers, acmeLeafDays)
	if err != nil {
		s.mu.Lock()
		order.status = "invalid"
		order.problem = err.Error()
		s.mu.Unlock()
		s.problem(w, http.StatusForbidden, "unauthorized", err.Error())
		return
	}
	s.mu.Lock()
	order.certPEM = signed.CertPEM
	order.status = "valid"
	s.mu.Unlock()

	w.Header().Set("Location", s.orderURL(r, order.id))
	s.writeJSON(w, r, http.StatusOK, s.orderBody(r, order))
}

// SANs of the csr must equal the order identifiers as a set
func csrMatchesOrder(csr *x509.CertificateRequest, identifiers []string) error {
	want := map[string]bool{}
	for _, id := range identifiers {
		want[strings.ToLower(id)] = true
	}
	got := map[string]bool{}
	for _, d := range csr.DNSNames {
		got[strings.ToLower(d)] = true
	}
	if csr.Subject.CommonName != "" {
		got[strings.ToLower(csr.Subject.CommonName)] = true
	}
	for d := range want {
		if !got[d] {
			return fmt.Errorf("csr missing identifier %q", d)
		}
	}
	for d := range got {
		if !want[d] {
			return fmt.Errorf("csr requests unauthorized identifier %q", d)
		}
	}
	return nil
}

func (s *ACMEServer) handleCert(w http.ResponseWriter, r *http.Request) {
	jws, err := s.verifyJWS(r, true)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "malformed", err.Error())
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/acme/cert/")
	s.mu.Lock()
	order := s.orders[id]
	s.mu.Unlock()
	if order == nil || order.account != jws.accountID || order.certPEM == "" {
		s.problem(w, http.StatusNotFound, "malformed", "unknown certificate")
		return
	}
	w.Header().Set("Replay-Nonce", s.newNonce())
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	_, _ = io.WriteString(w, order.certPEM)
}

// ── JWS verification ────────────────────────────────────────────────────

type jwsResult struct {
	payload    []byte
	thumbprint string
	accountID  string // From kid
	jwkJSON    string // Canonical jwk for new-account persistence
}

type jwsProtected struct {
	Alg   string          `json:"alg"`
	Nonce string          `json:"nonce"`
	URL   string          `json:"url"`
	KID   string          `json:"kid"`
	JWK   json.RawMessage `json:"jwk"`
}

// Verifies signature, nonce, and url, resolving the signing key from
// the embedded jwk or the referenced account
func (s *ACMEServer) verifyJWS(r *http.Request, requireKID bool) (*jwsResult, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("expected POST")
	}
	var body struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("invalid jws envelope")
	}
	protectedBytes, err := base64.RawURLEncoding.DecodeString(body.Protected)
	if err != nil {
		return nil, fmt.Errorf("invalid protected header")
	}
	var ph jwsProtected
	if err := json.Unmarshal(protectedBytes, &ph); err != nil {
		return nil, fmt.Errorf("invalid protected header json")
	}

	// The url header binds the signature to this exact endpoint
	if ph.URL != s.requestURL(r) {
		return nil, fmt.Errorf("jws url mismatch")
	}
	if !s.consumeNonce(ph.Nonce) {
		return nil, fmt.Errorf("bad or replayed nonce")
	}
	if (ph.KID == "") == (len(ph.JWK) == 0) {
		return nil, fmt.Errorf("jws must carry exactly one of kid or jwk")
	}

	res := &jwsResult{}
	var pub crypto.PublicKey
	if len(ph.JWK) > 0 {
		if requireKID {
			return nil, fmt.Errorf("this endpoint requires an account kid")
		}
		pub, res.thumbprint, res.jwkJSON, err = parseJWK(ph.JWK)
		if err != nil {
			return nil, err
		}
	} else {
		res.accountID = accountIDFromKID(ph.KID)
		res.thumbprint = res.accountID
		acct, err := s.e.store.GetACMEAccount(r.Context(), res.accountID)
		if err != nil || acct == nil {
			return nil, fmt.Errorf("unknown account")
		}
		pub, _, _, err = parseJWK([]byte(acct.PublicKeyJSON))
		if err != nil {
			return nil, err
		}
	}

	signed := body.Protected + "." + body.Payload
	sig, err := base64.RawURLEncoding.DecodeString(body.Signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding")
	}
	if err := verifyJWSSignature(ph.Alg, pub, []byte(signed), sig); err != nil {
		return nil, err
	}

	if body.Payload != "" {
		if res.payload, err = base64.RawURLEncoding.DecodeString(body.Payload); err != nil {
			return nil, fmt.Errorf("invalid payload encoding")
		}
	}
	return res, nil
}

func (s *ACMEServer) requestURL(r *http.Request) string {
	return s.scheme(r) + "://" + r.Host + r.URL.Path
}

func (s *ACMEServer) requireAccount(ctx context.Context, id string) error {
	acct, err := s.e.store.GetACMEAccount(ctx, id)
	if err != nil || acct == nil {
		return fmt.Errorf("unknown account")
	}
	return nil
}

func accountIDFromKID(kid string) string {
	idx := strings.LastIndex(kid, "/acct/")
	if idx < 0 {
		return ""
	}
	return kid[idx+len("/acct/"):]
}

// ── Response helpers ────────────────────────────────────────────────────

func (s *ACMEServer) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Replay-Nonce", s.newNonce())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *ACMEServer) problemDoc(typ, detail string) map[string]any {
	return map[string]any{
		"type":   "urn:ietf:params:acme:error:" + typ,
		"detail": detail,
	}
}

func (s *ACMEServer) problem(w http.ResponseWriter, status int, typ, detail string) {
	w.Header().Set("Replay-Nonce", s.newNonce())
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(s.problemDoc(typ, detail))
}

func (s *ACMEServer) nextID(prefix string) string {
	s.mu.Lock()
	s.counter++
	n := s.counter
	s.mu.Unlock()
	return fmt.Sprintf("%s-%d-%s", prefix, n, s.randToken()[:8])
}

func (s *ACMEServer) randToken() string {
	b := make([]byte, 24)
	_, _ = io.ReadFull(rand.Reader, b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ── JWK parsing and signature verification ──────────────────────────────

type jwkFields struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// Returns the public key, its rfc 7638 thumbprint, and canonical json
func parseJWK(raw []byte) (crypto.PublicKey, string, string, error) {
	var j jwkFields
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, "", "", fmt.Errorf("invalid jwk")
	}
	switch j.Kty {
	case "EC":
		pub, canonical, err := ecJWK(j)
		if err != nil {
			return nil, "", "", err
		}
		return pub, jwkThumbprint(canonical), canonical, nil
	case "RSA":
		pub, canonical, err := rsaJWK(j)
		if err != nil {
			return nil, "", "", err
		}
		return pub, jwkThumbprint(canonical), canonical, nil
	}
	return nil, "", "", fmt.Errorf("unsupported jwk key type %q", j.Kty)
}

func ecJWK(j jwkFields) (*ecdsa.PublicKey, string, error) {
	var curve elliptic.Curve
	switch j.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, "", fmt.Errorf("unsupported ec curve %q", j.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(j.X)
	if err != nil {
		return nil, "", fmt.Errorf("invalid ec x")
	}
	y, err := base64.RawURLEncoding.DecodeString(j.Y)
	if err != nil {
		return nil, "", fmt.Errorf("invalid ec y")
	}
	pub := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !curve.IsOnCurve(pub.X, pub.Y) {
		return nil, "", fmt.Errorf("ec point not on curve")
	}
	// Canonical field order per rfc 7638
	canonical := fmt.Sprintf(`{"crv":"%s","kty":"EC","x":"%s","y":"%s"}`, j.Crv, j.X, j.Y)
	return pub, canonical, nil
}

func rsaJWK(j jwkFields) (*rsa.PublicKey, string, error) {
	n, err := base64.RawURLEncoding.DecodeString(j.N)
	if err != nil {
		return nil, "", fmt.Errorf("invalid rsa n")
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(j.E)
	if err != nil {
		return nil, "", fmt.Errorf("invalid rsa e")
	}
	e := new(big.Int).SetBytes(eBytes)
	if e.BitLen() > 32 {
		return nil, "", fmt.Errorf("rsa exponent too large")
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(e.Int64())}
	if pub.N.Sign() <= 0 || pub.E < 3 {
		return nil, "", fmt.Errorf("invalid rsa key")
	}
	canonical := fmt.Sprintf(`{"e":"%s","kty":"RSA","n":"%s"}`, j.E, j.N)
	return pub, canonical, nil
}

func jwkThumbprint(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func verifyJWSSignature(alg string, pub crypto.PublicKey, signed, sig []byte) error {
	switch alg {
	case "ES256":
		key, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("es256 needs an ec key")
		}
		// Bind the algorithm to its curve, no p-384 key under es256
		if key.Curve != elliptic.P256() {
			return fmt.Errorf("es256 requires a p-256 key")
		}
		if len(sig) != 64 {
			return fmt.Errorf("bad ecdsa signature length")
		}
		hash := sha256.Sum256(signed)
		rInt := new(big.Int).SetBytes(sig[:32])
		sInt := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(key, hash[:], rInt, sInt) {
			return fmt.Errorf("ecdsa signature invalid")
		}
		return nil
	case "RS256":
		key, ok := pub.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("rs256 needs an rsa key")
		}
		hash := sha256.Sum256(signed)
		return rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], sig)
	}
	return fmt.Errorf("unsupported jws algorithm %q", alg)
}
