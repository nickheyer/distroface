package registry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
)

func TestArtifactMiddlewareRouteShapes(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "main", Hostname: "acme.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	cases := []struct {
		path string
		want string
	}{
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
	}

	for _, c := range cases {
		var got string
		h := pr.ArtifactMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
		}))
		h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, c.path, "acme.example.com", 0))
		if got != c.want {
			t.Errorf("ArtifactMiddleware(%q) routed %q, want %q", c.path, got, c.want)
		}
	}
}

func TestArtifactMiddlewareNonPortalPassthrough(t *testing.T) {
	store := newTestStore(t)
	pr := NewPortalResolver(store, logger.New())

	var got string
	h := pr.ArtifactMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
	}))
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/f", "other.example.com", 0))
	if got != "/api/v1/artifacts/myrepo/1.0.0/f" {
		t.Errorf("non-portal traffic rewritten to %q", got)
	}
}

func TestArtifactMiddlewareReadOnly(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "ro", Hostname: "ro.example.com", MapUnqualified: true, Rules: "[]", AllowPush: false, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	nextCalled := false
	h := pr.ArtifactMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		nextCalled = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, portalRequest(method, "/api/v1/artifacts/myrepo/upload", "ro.example.com", 0))
		if nextCalled || rec.Code != http.StatusForbidden {
			t.Errorf("%s through read-only portal: nextCalled=%v code=%d", method, nextCalled, rec.Code)
		}
	}

	nextCalled = false
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/f", "ro.example.com", 0))
	if !nextCalled {
		t.Error("read through read-only portal must pass")
	}
}

func TestArtifactMiddlewareRequireAuth(t *testing.T) {
	store := newTestStore(t)
	createTestPortal(t, store, &storage.RegistryPortal{
		Name: "auth", Hostname: "auth.example.com", MapUnqualified: true, Rules: "[]", AllowPush: true, RequireAuth: true, Enabled: true,
	})
	pr := NewPortalResolver(store, logger.New())

	nextCalled := false
	h := pr.ArtifactMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, portalRequest(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/f", "auth.example.com", 0))
	if nextCalled || rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous through require-auth portal: nextCalled=%v code=%d", nextCalled, rec.Code)
	}

	nextCalled = false
	r := portalRequest(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/f", "auth.example.com", 0)
	r.Header.Set("Authorization", "Bearer df_token")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !nextCalled {
		t.Error("authenticated request through require-auth portal must pass")
	}
}
