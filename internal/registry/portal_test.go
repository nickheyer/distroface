package registry

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
)

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestPortal(t *testing.T, store *storage.Store, portal *storage.RegistryPortal) (*storage.Organization, *storage.RegistryPortal) {
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

func TestPortalResolverMapName(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name:           "main",
		Hostname:       "acme.example.com",
		MapUnqualified: true,
		Rules:          `[{"pattern":"legacy/(.+)","replace":"acme/$1"},{"pattern":"grab/(.+)","replace":"evil/$1"}]`,
		AllowPush:      true,
		Enabled:        true,
	})
	pr := NewPortalResolver(store, logger.New())

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
		if got := pr.MapName(r, c.name); got != c.want {
			t.Errorf("MapName(%q, %q) = %q, want %q", c.host, c.name, got, c.want)
		}
	}
}

func TestPortalResolverPortMatching(t *testing.T) {
	store := newTestStore(t)
	_, catchAll := createTestPortal(t, store, &storage.RegistryPortal{
		Name: "port-only", Hostname: "", Port: 9500, MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "hosted", Hostname: "vanity.example.com", Port: 9501, MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	// Catch-all matches any host on its port
	r := portalRequest(http.MethodGet, "/", "whatever.example.com:9500", 9500)
	if got := pr.MapName(r, "myimg"); got != "acme/myimg" {
		t.Errorf("port catch-all: got %q", got)
	}
	r = portalRequest(http.MethodGet, "/", "127.0.0.1:9500", 9500)
	if got := pr.MapName(r, "myimg"); got != "acme/myimg" {
		t.Errorf("port catch-all by IP: got %q", got)
	}

	// Host+port portal matches its host on its port
	r = portalRequest(http.MethodGet, "/", "vanity.example.com:9501", 9501)
	if got := pr.MapName(r, "myimg"); got != "acme/myimg" {
		t.Errorf("host+port portal: got %q", got)
	}

	// Same port, unmatched host, no catch-all on 9501, no mapping
	r = portalRequest(http.MethodGet, "/", "other.example.com:9501", 9501)
	if got := pr.MapName(r, "myimg"); got != "myimg" {
		t.Errorf("unmatched host on dedicated port: got %q", got)
	}

	// Port-bound portal does not match on other listeners
	r = portalRequest(http.MethodGet, "/", "whatever.example.com", 8080)
	if got := pr.MapName(r, "myimg"); got != "myimg" {
		t.Errorf("catch-all must not match other ports: got %q", got)
	}

	_ = catchAll
}

func TestPortalResolverAccessOptions(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "ro", Hostname: "ro.example.com", Rules: "[]",
		AllowPush: false, RequireAuth: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	ro := portalRequest(http.MethodGet, "/", "ro.example.com", 0)
	other := portalRequest(http.MethodGet, "/", "other.example.com", 0)

	if pr.AllowPush(ro) {
		t.Error("read-only portal must not allow push")
	}
	if pr.AllowAnonymous(ro) {
		t.Error("require_auth portal must not allow anonymous")
	}
	if !pr.AllowPush(other) || !pr.AllowAnonymous(other) {
		t.Error("non-portal hosts keep default access")
	}
}

func TestPortalResolverDisabledAndInvalidate(t *testing.T) {
	store := newTestStore(t)
	_, portal := createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())
	req := func() *http.Request { return portalRequest(http.MethodGet, "/", "acme.example.com", 0) }

	if got := pr.MapName(req(), "myimg"); got != "acme/myimg" {
		t.Fatalf("expected portal mapping before disable, got %q", got)
	}

	portal.Enabled = false
	if err := store.UpdateRegistryPortal(context.Background(), portal); err != nil {
		t.Fatalf("UpdateRegistryPortal: %v", err)
	}

	// Stale cache until invalidated
	if got := pr.MapName(req(), "myimg"); got != "acme/myimg" {
		t.Errorf("expected cached mapping before Invalidate, got %q", got)
	}
	pr.Invalidate()
	if got := pr.MapName(req(), "myimg"); got != "myimg" {
		t.Errorf("disabled portal must not map, got %q", got)
	}
}

func TestPortalMiddlewareRouteShapes(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	cases := []struct {
		path string
		want string
	}{
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
	}

	for _, c := range cases {
		var got string
		h := pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
		}))
		h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, c.path, "acme.example.com", 0))
		if got != c.want {
			t.Errorf("Middleware(%q) routed %q, want %q", c.path, got, c.want)
		}
	}
}

func TestPortalMiddlewareReadOnly(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "ro", Hostname: "ro.example.com", MapUnqualified: true, Rules: "[]", AllowPush: false, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	nextCalled := false
	h := pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, portalRequest(http.MethodPost, "/v2/myimg/blobs/uploads/", "ro.example.com", 0))
	if nextCalled || rec.Code != http.StatusForbidden {
		t.Errorf("write through read-only portal: nextCalled=%v code=%d", nextCalled, rec.Code)
	}

	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/v2/myimg/manifests/v1", "ro.example.com", 0))
	if !nextCalled {
		t.Error("read through read-only portal must pass")
	}
}

func TestRealmRewrite(t *testing.T) {
	store := newTestStore(t)
	pr := NewPortalResolver(store, logger.New())

	h := pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	h = pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestPortalProxyManagerReconcile(t *testing.T) {
	store := newTestStore(t)
	pr := NewPortalResolver(store, logger.New())
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "portal-surface")
	})
	m := NewPortalProxyManager(pr, handler, "127.0.0.1", logger.New())
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
