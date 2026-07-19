package portal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
)

func newTestStore(t *testing.T) *stores.Store {
	t.Helper()
	store, err := stores.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestPortal(t *testing.T, store *stores.Store, portal *storage.RegistryPortal) (*storage.Organization, *storage.RegistryPortal) {
	t.Helper()
	ctx := context.Background()
	org, err := store.GetOrganization(ctx, "acme")
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org == nil {
		org = &storage.Organization{Name: "acme", DisplayName: "Acme", CreatedBy: "test"}
		if err := store.CreateOrganization(ctx, org); err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}
	}
	portal.OrgID = org.ID
	if err := store.CreateRegistryPortal(ctx, portal); err != nil {
		t.Fatalf("CreateRegistryPortal: %v", err)
	}
	return org, portal
}

// Builds a request as seen on a given listener port, 0 means unknown
func portalRequest(method, path, host string, port int) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	if port > 0 {
		addr := net.Addr(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, addr))
	}
	return r
}

func TestResolverMapName(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name:           "main",
		Hostname:       "acme.example.com",
		MapUnqualified: true,
		Rules:          `[{"pattern":"legacy/(.+)","replace":"acme/$1"},{"pattern":"grab/(.+)","replace":"evil/$1"}]`,
		AllowPush:      true,
		Enabled:        true,
	})
	res := NewResolver(store, logger.New())

	cases := []struct {
		host, name, want string
	}{
		{"acme.example.com", "myimg", "acme/myimg"},      // unqualified prefixed into org
		{"ACME.example.com:8080", "myimg", "acme/myimg"}, // case/port-insensitive host match
		{"acme.example.com", "legacy/foo", "acme/foo"},   // custom rule
		{"acme.example.com", "grab/foo", "grab/foo"},     // rule result outside org namespace refused
		{"acme.example.com", "other/foo", "other/foo"},   // qualified names pass through
		{"acme.example.com", "acme/foo", "acme/foo"},     // canonical names untouched
		{"other.example.com", "myimg", "myimg"},          // non-portal host
	}
	for _, c := range cases {
		r := portalRequest(http.MethodGet, "/", c.host, 0)
		if got := res.MapName(r, c.name); got != c.want {
			t.Errorf("MapName(%q, %q) = %q, want %q", c.host, c.name, got, c.want)
		}
	}
}

func TestResolverPortMatching(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "port-only", Hostname: "", Port: 9500, MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "hosted", Hostname: "vanity.example.com", Port: 9501, MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	// Catch-all matches any host on its port
	r := portalRequest(http.MethodGet, "/", "whatever.example.com:9500", 9500)
	if got := res.MapName(r, "myimg"); got != "acme/myimg" {
		t.Errorf("port catch-all: got %q", got)
	}
	r = portalRequest(http.MethodGet, "/", "127.0.0.1:9500", 9500)
	if got := res.MapName(r, "myimg"); got != "acme/myimg" {
		t.Errorf("port catch-all by IP: got %q", got)
	}

	// Host+port portal matches its host on its port
	r = portalRequest(http.MethodGet, "/", "vanity.example.com:9501", 9501)
	if got := res.MapName(r, "myimg"); got != "acme/myimg" {
		t.Errorf("host+port portal: got %q", got)
	}

	// Same port, unmatched host, no catch-all on 9501, no mapping
	r = portalRequest(http.MethodGet, "/", "other.example.com:9501", 9501)
	if got := res.MapName(r, "myimg"); got != "myimg" {
		t.Errorf("unmatched host on dedicated port: got %q", got)
	}

	// Port-bound portal does not match on other listeners
	r = portalRequest(http.MethodGet, "/", "whatever.example.com", 8080)
	if got := res.MapName(r, "myimg"); got != "myimg" {
		t.Errorf("catch-all must not match other ports: got %q", got)
	}
}

func TestResolverAccessOptions(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "ro", Hostname: "ro.example.com", Rules: "[]",
		AllowPush: false, RequireAuth: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	ro := portalRequest(http.MethodGet, "/", "ro.example.com", 0)
	other := portalRequest(http.MethodGet, "/", "other.example.com", 0)

	if res.AllowPush(ro) {
		t.Error("read-only portal must not allow push")
	}
	if res.AllowAnonymous(ro) {
		t.Error("require_auth portal must not allow anonymous")
	}
	if !res.AllowPush(other) || !res.AllowAnonymous(other) {
		t.Error("non-portal hosts keep default access")
	}
	if !res.IsPortalHost("ro.example.com:8443") {
		t.Error("portal hostname must be recognized")
	}
	if res.IsPortalHost("other.example.com") {
		t.Error("unknown hostname must not be recognized")
	}
}

func TestResolverDisabledAndInvalidate(t *testing.T) {
	store := newTestStore(t)
	_, portal := createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())
	req := func() *http.Request { return portalRequest(http.MethodGet, "/", "acme.example.com", 0) }

	if got := res.MapName(req(), "myimg"); got != "acme/myimg" {
		t.Fatalf("expected portal mapping before disable, got %q", got)
	}

	portal.Enabled = false
	if err := store.UpdateRegistryPortal(context.Background(), portal); err != nil {
		t.Fatalf("UpdateRegistryPortal: %v", err)
	}

	// Stale cache until invalidated
	if got := res.MapName(req(), "myimg"); got != "acme/myimg" {
		t.Errorf("expected cached mapping before Invalidate, got %q", got)
	}
	res.Invalidate()
	if got := res.MapName(req(), "myimg"); got != "myimg" {
		t.Errorf("disabled portal must not map, got %q", got)
	}
}

func TestMiddlewareRouteShapes(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	cases := []struct {
		path string
		want string
	}{
		// Registry data plane
		{"/v2/myimg/manifests/v1", "/v2/acme/myimg/manifests/v1"},
		{"/v2/myimg/manifests/sha256:abc", "/v2/acme/myimg/manifests/sha256:abc"},
		{"/v2/myimg/blobs/sha256:abc", "/v2/acme/myimg/blobs/sha256:abc"},
		{"/v2/myimg/blobs/uploads/", "/v2/acme/myimg/blobs/uploads/"},
		{"/v2/myimg/blobs/uploads/uuid-123", "/v2/acme/myimg/blobs/uploads/uuid-123"},
		{"/v2/myimg/tags/list", "/v2/acme/myimg/tags/list"},
		{"/v2/myimg/referrers/sha256:abc", "/v2/acme/myimg/referrers/sha256:abc"},
		{"/v2/other/thing/manifests/latest", "/v2/other/thing/manifests/latest"},
		{"/v2/acme/myimg/tags/list", "/v2/acme/myimg/tags/list"},
		{"/v2/", "/v2/"},
		{"/v2/_catalog", "/v2/_catalog"},
		// Artifact data plane
		{"/api/v1/artifacts/myrepo/upload", "/api/v1/artifacts/_ns/acme/myrepo/upload"},
		{"/api/v1/artifacts/myrepo/upload/uuid-1", "/api/v1/artifacts/_ns/acme/myrepo/upload/uuid-1"},
		{"/api/v1/artifacts/myrepo/1.0.0/some/file.txt", "/api/v1/artifacts/_ns/acme/myrepo/1.0.0/some/file.txt"},
		{"/api/v1/artifacts/myrepo/query", "/api/v1/artifacts/_ns/acme/myrepo/query"},
		{"/api/v1/artifacts/myrepo/versions", "/api/v1/artifacts/_ns/acme/myrepo/versions"},
		{"/api/v1/artifacts/myrepo/abc/metadata", "/api/v1/artifacts/_ns/acme/myrepo/abc/metadata"},
		// Control plane keywords pass through
		{"/api/v1/artifacts/repos", "/api/v1/artifacts/repos"},
		{"/api/v1/artifacts/repos/myrepo", "/api/v1/artifacts/repos/myrepo"},
		{"/api/v1/artifacts/search", "/api/v1/artifacts/search"},
		// Org qualified and marker forms both land on the marker path
		{"/api/v1/artifacts/acme/myrepo/1.0.0/f", "/api/v1/artifacts/_ns/acme/myrepo/1.0.0/f"},
		{"/api/v1/artifacts/_ns/acme/myrepo/upload", "/api/v1/artifacts/_ns/acme/myrepo/upload"},
		// Everything else untouched
		{"/orgs/acme", "/orgs/acme"},
	}

	for _, c := range cases {
		var got string
		h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
		}))
		h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, c.path, "acme.example.com", 0))
		if got != c.want {
			t.Errorf("Middleware(%q) routed %q, want %q", c.path, got, c.want)
		}
	}

	// Control plane listings pick up the org namespace
	var gotNS, gotRepo string
	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotNS = r.URL.Query().Get("namespace")
		gotRepo = r.URL.Query().Get("repo")
	}))
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/repos?namespace=elsewhere", "acme.example.com", 0))
	if gotNS != "acme" {
		t.Errorf("repos listing namespace = %q, want acme", gotNS)
	}
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/search", "acme.example.com", 0))
	if gotNS != "acme" {
		t.Errorf("search namespace = %q, want acme", gotNS)
	}

	// Repo lookups map like the data plane on a mapping portal
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/search?repo=myrepo", "acme.example.com", 0))
	if gotNS != "acme" || gotRepo != "myrepo" {
		t.Errorf("mapped search = ns %q repo %q, want acme/myrepo", gotNS, gotRepo)
	}
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/search?repo=acme/myrepo", "acme.example.com", 0))
	if gotNS != "acme" || gotRepo != "myrepo" {
		t.Errorf("qualified search = ns %q repo %q, want acme/myrepo", gotNS, gotRepo)
	}
}

func TestMiddlewareSearchUnmappedPortal(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "plain", Hostname: "plain.example.com", MapUnqualified: false, Rules: "[]", AllowPush: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	var gotNS, gotRepo string
	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotNS = r.URL.Query().Get("namespace")
		gotRepo = r.URL.Query().Get("repo")
	}))

	// Unmapped repo lookups stay caller scoped, matching where uploads land
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/search?repo=ci-cache", "plain.example.com", 0))
	if gotNS != "" || gotRepo != "ci-cache" {
		t.Errorf("unmapped search = ns %q repo %q, want empty ns and ci-cache", gotNS, gotRepo)
	}

	// Browsing without a repo still scopes to the org viewport
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/search", "plain.example.com", 0))
	if gotNS != "acme" {
		t.Errorf("repo-less search namespace = %q, want acme", gotNS)
	}
}

func TestScopeRepoRef(t *testing.T) {
	mapping := WithPortal(context.Background(), &Portal{OrgName: "acme", MapUnqualified: true})
	plain := WithPortal(context.Background(), &Portal{OrgName: "acme"})

	cases := []struct {
		ctx              context.Context
		ns, name         string
		wantNS, wantName string
	}{
		{context.Background(), "", "repo", "", "repo"},
		{mapping, "", "repo", "acme", "repo"},
		{mapping, "team", "repo", "team", "repo"},
		{plain, "", "repo", "", "repo"},
	}
	for _, c := range cases {
		ns, name := ScopeRepoRef(c.ctx, c.ns, c.name)
		if ns != c.wantNS || name != c.wantName {
			t.Errorf("ScopeRepoRef(%q, %q) = %q/%q, want %q/%q", c.ns, c.name, ns, name, c.wantNS, c.wantName)
		}
	}
}

func TestMiddlewareInjectsContext(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", Rules: "[]", AllowPush: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	var p *Portal
	var scoped string
	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p = FromContext(r.Context())
		scoped = ScopeNamespace(r.Context(), "someone-else")
	}))

	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/anything", "acme.example.com", 0))
	if p == nil || p.OrgName != "acme" || p.OrgDisplayName != "Acme" {
		t.Fatalf("portal context missing or wrong: %+v", p)
	}
	if scoped != "acme" {
		t.Errorf("ScopeNamespace on portal traffic = %q, want acme", scoped)
	}

	p = nil
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/anything", "other.example.com", 0))
	if p != nil {
		t.Error("non-portal traffic must not carry portal context")
	}
	if got := ScopeNamespace(context.Background(), "me"); got != "me" {
		t.Errorf("ScopeNamespace off portal = %q, want me", got)
	}
}

func TestMiddlewareReadOnly(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "ro", Hostname: "ro.example.com", MapUnqualified: true, Rules: "[]", AllowPush: false, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	nextCalled := false
	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	writes := []struct {
		method, path string
	}{
		{http.MethodPost, "/v2/myimg/blobs/uploads/"},
		{http.MethodPost, "/api/v1/artifacts/myrepo/upload"},
		{http.MethodPatch, "/api/v1/artifacts/myrepo/upload/uuid-1"},
		{http.MethodPut, "/api/v1/artifacts/myrepo/upload/uuid-1"},
		{http.MethodDelete, "/api/v1/artifacts/myrepo/1.0.0/f"},
		// Marker paths from upload Locations must not bypass enforcement
		{http.MethodPatch, "/api/v1/artifacts/_ns/acme/myrepo/upload/uuid-1"},
	}
	for _, c := range writes {
		nextCalled = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, portalRequest(c.method, c.path, "ro.example.com", 0))
		if nextCalled || rec.Code != http.StatusForbidden {
			t.Errorf("%s %s through read-only portal: nextCalled=%v code=%d", c.method, c.path, nextCalled, rec.Code)
		}
	}

	for _, path := range []string{"/v2/myimg/manifests/v1", "/api/v1/artifacts/myrepo/1.0.0/f"} {
		nextCalled = false
		h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, path, "ro.example.com", 0))
		if !nextCalled {
			t.Errorf("read of %s through read-only portal must pass", path)
		}
	}
}

func TestMiddlewareRequireAuth(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "auth", Hostname: "auth.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, RequireAuth: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	nextCalled := false
	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	anon := []string{
		"/api/v1/artifacts/myrepo/1.0.0/f",
		"/api/v1/artifacts/_ns/acme/myrepo/1.0.0/f",
	}
	for _, path := range anon {
		nextCalled = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, portalRequest(http.MethodGet, path, "auth.example.com", 0))
		if nextCalled || rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s through require-auth portal: nextCalled=%v code=%d", path, nextCalled, rec.Code)
		}
	}

	nextCalled = false
	r := portalRequest(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/f", "auth.example.com", 0)
	r.Header.Set("Authorization", "Bearer df_token")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !nextCalled {
		t.Error("authenticated request through require-auth portal must pass")
	}

	// UI and login stay reachable, auth is enforced at the data planes and RPC
	nextCalled = false
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/login", "auth.example.com", 0))
	if !nextCalled {
		t.Error("login page through require-auth portal must pass")
	}
}

func TestMiddlewareOIDCBounce(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", Rules: "[]", AllowPush: true, Enabled: true,
	})
	res := NewResolver(store, logger.New())

	h := res.Middleware(func() string { return "registry.example.com" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("bounced request must not reach the app")
	}))

	r := portalRequest(http.MethodGet, "/api/v1/auth/oidc/login", "acme.example.com", 0)
	r.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	want := "https://registry.example.com/api/v1/auth/oidc/login?return_to=https%3A%2F%2Facme.example.com"
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Errorf("OIDC bounce: code=%d location=%q, want 302 %q", rec.Code, rec.Header().Get("Location"), want)
	}

	// Off portals the login route passes through untouched
	passed := false
	h = res.Middleware(func() string { return "registry.example.com" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/auth/oidc/login", "registry.example.com", 0))
	if !passed {
		t.Error("primary-host OIDC login must pass through")
	}
}

func TestRealmRewrite(t *testing.T) {
	store := newTestStore(t)
	res := NewResolver(store, logger.New())

	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Www-Authenticate", `Bearer realm="http://0.0.0.0:8080/auth/token",service="distroface-registry",scope="repository:acme/myimg:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))

	r := portalRequest(http.MethodGet, "/v2/acme/myimg/manifests/v1", "registry.acme.com", 0)
	r.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	want := `Bearer realm="https://registry.acme.com/auth/token",service="distroface-registry",scope="repository:acme/myimg:pull"`
	if got := rec.Header().Get("Www-Authenticate"); got != want {
		t.Errorf("realm rewrite:\n got %q\nwant %q", got, want)
	}

	h = res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, portalRequest(http.MethodGet, "/", "registry.acme.com", 0))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 passthrough, got %d", rec.Code)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// Minimal self signed server config for listener tests
func selfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

// Both schemes on one shared port, tls enforced per hostname
func TestManagerReconcileTLS(t *testing.T) {
	store := newTestStore(t)
	res := NewResolver(store, logger.New())
	m := NewManager(res, "127.0.0.1", logger.New())
	m.SetHandler(res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "portal-surface")
	})))
	t.Cleanup(m.Close)

	port := freePort(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "secure", Hostname: "secure.example.com", Port: port,
		MapUnqualified: true, Rules: "[]", AllowPush: true, TLS: true, Enabled: true,
	})
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "plain", Hostname: "plain.example.com", Port: port,
		MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})

	m.SetTLSConfig(selfSignedTLSConfig(t))
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	get := func(scheme, host string) *http.Response {
		t.Helper()
		client := &http.Client{
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s://127.0.0.1:%d/", scheme, port), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Host = fmt.Sprintf("%s:%d", host, port)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s to %s failed: %v", scheme, host, err)
		}
		resp.Body.Close()
		return resp
	}

	// Https and cleartext coexist on the same port
	if resp := get("https", "secure.example.com"); resp.StatusCode != http.StatusOK {
		t.Errorf("https on tls portal: got %d, want 200", resp.StatusCode)
	}
	if resp := get("http", "plain.example.com"); resp.StatusCode != http.StatusOK {
		t.Errorf("http on plain portal: got %d, want 200", resp.StatusCode)
	}

	// Cleartext to the tls portal bounces to https on the same address
	resp := get("http", "secure.example.com")
	wantLoc := fmt.Sprintf("https://secure.example.com:%d/", port)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != wantLoc {
		t.Errorf("http on tls portal: got %d %q, want 302 %q", resp.StatusCode, resp.Header.Get("Location"), wantLoc)
	}

	// Https also works for the plain portal, just not required
	if resp := get("https", "plain.example.com"); resp.StatusCode != http.StatusOK {
		t.Errorf("https on plain portal: got %d, want 200", resp.StatusCode)
	}
}

func TestManagerReconcile(t *testing.T) {
	store := newTestStore(t)
	res := NewResolver(store, logger.New())
	m := NewManager(res, "127.0.0.1", logger.New())
	m.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "portal-surface")
	}))
	t.Cleanup(m.Close)

	port := freePort(t)
	_, portal := createTestPortal(t, store, &storage.RegistryPortal{
		Name: "proxy", Hostname: "", Port: port, MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("portal listener not serving: %v", err)
	}
	resp.Body.Close()

	// Shared port stays open while any portal wants it
	if err := m.ProbePort(port); err != nil {
		t.Errorf("ProbePort on owned port must succeed, got %v", err)
	}

	// Deleting the portal closes the listener
	if err := store.DeleteRegistryPortal(context.Background(), portal.ID); err != nil {
		t.Fatalf("DeleteRegistryPortal: %v", err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile after delete: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := http.Get(url); err != nil {
			return // Listener closed
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("listener still serving after portal delete")
}
