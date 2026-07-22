package portal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
)

func backendMiddleware(t *testing.T, portal *storage.RegistryPortal) (http.Handler, *bool) {
	t.Helper()
	store := newTestStore(t)
	createTestPortal(t, store, portal)
	res := NewResolver(store, nil, logger.New())
	nextCalled := false
	h := res.Middleware(func() string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))
	return h, &nextCalled
}

func TestMiddlewareBackendProxy(t *testing.T) {
	var seen *http.Request
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		w.Header().Set("X-Backend", "yes")
		_, _ = io.WriteString(w, "hello from backend")
	}))
	t.Cleanup(backend.Close)

	h, nextCalled := backendMiddleware(t, &storage.RegistryPortal{
		Name: "svc", Hostname: "svc.example.com", Rules: "[]", BackendURL: backend.URL, Enabled: true,
	})

	r := portalRequest(http.MethodGet, "/any/path?x=1", "svc.example.com", 0)
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	r.Header.Set("X-Client-Cert-Cn", "spoofed")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if *nextCalled {
		t.Fatal("backend portal traffic must not reach the app")
	}
	if body := rec.Body.String(); body != "hello from backend" {
		t.Fatalf("proxied body = %q", body)
	}
	if rec.Header().Get("X-Backend") != "yes" {
		t.Error("backend response headers must pass through")
	}
	if seen.Host != "svc.example.com" {
		t.Errorf("backend saw Host %q, want the client's host", seen.Host)
	}
	if got := seen.Header.Get("X-Forwarded-For"); got == "" || got == "6.6.6.6" {
		t.Errorf("X-Forwarded-For = %q, client value must be replaced", got)
	}
	if seen.Header.Get("X-Client-Cert-Cn") != "" {
		t.Error("client supplied identity header must be stripped")
	}
	if seen.Header.Get("X-Forwarded-Proto") != "http" {
		t.Errorf("X-Forwarded-Proto = %q", seen.Header.Get("X-Forwarded-Proto"))
	}
	if seen.Header.Get("Via") != viaToken {
		t.Errorf("Via = %q", seen.Header.Get("Via"))
	}
	if seen.URL.RequestURI() != "/any/path?x=1" {
		t.Errorf("backend saw %q, path and query must pass through", seen.URL.RequestURI())
	}

	// Other hosts stay on the app
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/", "other.example.com", 0))
	if !*nextCalled {
		t.Error("non-portal traffic must reach the app")
	}
}

func TestMiddlewareBackendRewriteHost(t *testing.T) {
	var seenHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
	}))
	t.Cleanup(backend.Close)

	h, _ := backendMiddleware(t, &storage.RegistryPortal{
		Name: "svc", Hostname: "svc.example.com", Rules: "[]",
		BackendURL: backend.URL, BackendRewriteHost: true, Enabled: true,
	})
	h.ServeHTTP(httptest.NewRecorder(), portalRequest(http.MethodGet, "/", "svc.example.com", 0))
	if seenHost != backend.Listener.Addr().String() {
		t.Errorf("backend saw Host %q, want its own %q", seenHost, backend.Listener.Addr().String())
	}
}

func TestMiddlewareBackendLoop(t *testing.T) {
	backendHit := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHit = true
	}))
	t.Cleanup(backend.Close)

	h, nextCalled := backendMiddleware(t, &storage.RegistryPortal{
		Name: "svc", Hostname: "svc.example.com", Rules: "[]", BackendURL: backend.URL, Enabled: true,
	})
	r := portalRequest(http.MethodGet, "/", "svc.example.com", 0)
	r.Header.Set("Via", viaToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusLoopDetected {
		t.Fatalf("looped request code = %d, want 508", rec.Code)
	}
	if backendHit || *nextCalled {
		t.Error("looped request must stop at the edge")
	}
}

func TestMiddlewareBackendInvalidStored(t *testing.T) {
	h, nextCalled := backendMiddleware(t, &storage.RegistryPortal{
		Name: "svc", Hostname: "svc.example.com", Rules: "[]", BackendURL: "::/not-a-url", Enabled: true,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, portalRequest(http.MethodGet, "/", "svc.example.com", 0))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("invalid stored backend code = %d, want 502", rec.Code)
	}
	if *nextCalled {
		t.Error("invalid backend must not fall back to the app")
	}
}

func TestMiddlewareBackendTLSRedirect(t *testing.T) {
	backendHit := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHit = true
	}))
	t.Cleanup(backend.Close)

	h, _ := backendMiddleware(t, &storage.RegistryPortal{
		Name: "svc", Hostname: "svc.example.com", Rules: "[]",
		BackendURL: backend.URL, TLS: true, Enabled: true,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, portalRequest(http.MethodGet, "/page", "svc.example.com", 0))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://svc.example.com/page" {
		t.Fatalf("cleartext on https portal: code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if backendHit {
		t.Error("redirect must happen before the backend")
	}
}

func TestParseBackendURL(t *testing.T) {
	valid := []string{"", "http://127.0.0.1:3000", "https://10.0.0.5:8443/base", " http://localhost:9000 "}
	for _, raw := range valid {
		if _, err := parseBackendURL(raw); err != nil {
			t.Errorf("parseBackendURL(%q) rejected: %v", raw, err)
		}
	}
	invalid := []string{"127.0.0.1:3000", "ftp://x", "http://", "http://user:pw@host", "http://h?q=1", "http://h#f", "::/bad"}
	for _, raw := range invalid {
		if _, err := parseBackendURL(raw); err == nil {
			t.Errorf("parseBackendURL(%q) accepted", raw)
		}
	}
}
