package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
		{"other.example.com", "myimg", "myimg"},          // non-portal host, no static rules
	}
	for _, c := range cases {
		if got := pr.MapName(c.host, c.name); got != c.want {
			t.Errorf("MapName(%q, %q) = %q, want %q", c.host, c.name, got, c.want)
		}
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
		// Unqualified names rewritten across every API route shape
		{"/v2/myimg/manifests/v1", "/v2/acme/myimg/manifests/v1"},
		{"/v2/myimg/manifests/sha256:abc", "/v2/acme/myimg/manifests/sha256:abc"},
		{"/v2/myimg/blobs/sha256:abc", "/v2/acme/myimg/blobs/sha256:abc"},
		{"/v2/myimg/blobs/uploads/", "/v2/acme/myimg/blobs/uploads/"},
		{"/v2/myimg/blobs/uploads/uuid-123", "/v2/acme/myimg/blobs/uploads/uuid-123"},
		{"/v2/myimg/tags/list", "/v2/acme/myimg/tags/list"},
		{"/v2/myimg/referrers/sha256:abc", "/v2/acme/myimg/referrers/sha256:abc"},
		// Qualified names pass through
		{"/v2/other/thing/manifests/latest", "/v2/other/thing/manifests/latest"},
		{"/v2/acme/myimg/tags/list", "/v2/acme/myimg/tags/list"},
		// Non-repository endpoints untouched
		{"/v2/", "/v2/"},
		{"/v2/_catalog", "/v2/_catalog"},
	}

	for _, c := range cases {
		var got string
		h := pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
		}))
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		r.Host = "acme.example.com"
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("Middleware(%q) routed %q, want %q", c.path, got, c.want)
		}
	}
}

func TestPortalResolverAccessOptions(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "ro", Hostname: "ro.example.com", Rules: "[]",
		AllowPush: false, RequireAuth: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	if pr.AllowPush("ro.example.com") {
		t.Error("read-only portal must not allow push")
	}
	if pr.AllowAnonymous("ro.example.com") {
		t.Error("require_auth portal must not allow anonymous")
	}
	if !pr.AllowPush("other.example.com") || !pr.AllowAnonymous("other.example.com") {
		t.Error("non-portal hosts keep default access")
	}
}

func TestPortalResolverDisabledAndInvalidate(t *testing.T) {
	store := newTestStore(t)
	_, portal := createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	if got := pr.MapName("acme.example.com", "myimg"); got != "acme/myimg" {
		t.Fatalf("expected portal mapping before disable, got %q", got)
	}

	portal.Enabled = false
	if err := store.UpdateRegistryPortal(context.Background(), portal); err != nil {
		t.Fatalf("UpdateRegistryPortal: %v", err)
	}

	// Stale cache until invalidated
	if got := pr.MapName("acme.example.com", "myimg"); got != "acme/myimg" {
		t.Errorf("expected cached mapping before Invalidate, got %q", got)
	}
	pr.Invalidate()
	if got := pr.MapName("acme.example.com", "myimg"); got != "myimg" {
		t.Errorf("disabled portal must not map, got %q", got)
	}
}

func TestPortalMiddleware(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	var gotPath string
	h := pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))

	r := httptest.NewRequest(http.MethodGet, "/v2/myimg/manifests/v1", nil)
	r.Host = "acme.example.com"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if gotPath != "/v2/acme/myimg/manifests/v1" {
		t.Errorf("portal middleware routed %q", gotPath)
	}

	r = httptest.NewRequest(http.MethodGet, "/v2/myimg/manifests/v1", nil)
	r.Host = "other.example.com"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if gotPath != "/v2/myimg/manifests/v1" {
		t.Errorf("non-portal host must not map, routed %q", gotPath)
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

	r := httptest.NewRequest(http.MethodPost, "/v2/myimg/blobs/uploads/", nil)
	r.Host = "ro.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if nextCalled || rec.Code != http.StatusForbidden {
		t.Errorf("write through read-only portal: nextCalled=%v code=%d", nextCalled, rec.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/v2/myimg/manifests/v1", nil)
	r.Host = "ro.example.com"
	h.ServeHTTP(httptest.NewRecorder(), r)
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

	r := httptest.NewRequest(http.MethodGet, "/v2/acme/myimg/manifests/v1", nil)
	r.Host = "registry.acme.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	want := `Bearer realm="https://registry.acme.com/auth/token",service="distroface-registry",scope="repository:acme/myimg:pull"`
	if got := rec.Header().Get("Www-Authenticate"); got != want {
		t.Errorf("realm rewrite:\n got %q\nwant %q", got, want)
	}

	// Non-401 responses keep their headers untouched
	h = pr.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 passthrough, got %d", rec.Code)
	}
}
