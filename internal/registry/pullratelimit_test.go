package registry

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/ratelimit"
	"github.com/nickheyer/distroface/pkg/logger"
)

type fakeVerifier struct {
	subjects map[string]string
}

func (f *fakeVerifier) VerifyTokenSubject(raw string) (string, error) {
	if sub, ok := f.subjects[raw]; ok {
		return sub, nil
	}
	return "", fmt.Errorf("bad token")
}

func newPullLimitedServer(t *testing.T, userLimit, anonLimit int) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	verifier := &fakeVerifier{subjects: map[string]string{
		"user-token": "nick",
		"anon-token": "",
	}}
	var userLimiter, anonLimiter *ratelimit.Limiter
	if userLimit > 0 {
		userLimiter = ratelimit.New(userLimit, time.Minute)
	}
	if anonLimit > 0 {
		anonLimiter = ratelimit.New(anonLimit, time.Minute)
	}
	return PullRateLimit(next, verifier, userLimiter, anonLimiter, logger.New())
}

func doManifestGet(h http.Handler, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v2/acme/app/manifests/latest", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPullRateLimitPerUser(t *testing.T) {
	h := newPullLimitedServer(t, 2, 100)

	for i := 0; i < 2; i++ {
		if w := doManifestGet(h, "user-token"); w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i+1, w.Code)
		}
	}

	w := doManifestGet(h, "user-token")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", w.Code)
	}
	if w.Header().Get("X-RateLimit-Limit") != "2" {
		t.Fatalf("X-RateLimit-Limit = %q, want 2", w.Header().Get("X-RateLimit-Limit"))
	}
	if w.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("X-RateLimit-Remaining = %q, want 0", w.Header().Get("X-RateLimit-Remaining"))
	}
	if w.Header().Get("X-RateLimit-Reset") == "" || w.Header().Get("Retry-After") == "" {
		t.Fatal("expected X-RateLimit-Reset and Retry-After headers")
	}
}

func TestPullRateLimitAnonymousByIP(t *testing.T) {
	h := newPullLimitedServer(t, 100, 1)

	if w := doManifestGet(h, "anon-token"); w.Code != http.StatusOK {
		t.Fatalf("first anonymous pull: status %d, want 200", w.Code)
	}
	if w := doManifestGet(h, "anon-token"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second anonymous pull: status %d, want 429", w.Code)
	}
	// Unverifiable tokens also land in the anonymous tier
	if w := doManifestGet(h, "garbage"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("garbage token: status %d, want 429", w.Code)
	}
}

func TestPullRateLimitSkipsUnauthenticatedAndNonManifest(t *testing.T) {
	h := newPullLimitedServer(t, 1, 1)

	// No auth header passes through never counted
	for i := 0; i < 3; i++ {
		if w := doManifestGet(h, ""); w.Code != http.StatusOK {
			t.Fatalf("challenge round-trip %d: status %d, want 200", i+1, w.Code)
		}
	}

	// Blob fetches are never limited
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodGet, "/v2/acme/app/blobs/sha256:abc", nil)
		r.Header.Set("Authorization", "Bearer user-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("blob fetch %d: status %d, want 200", i+1, w.Code)
		}
	}

	// Manifest push is not pull limited
	r := httptest.NewRequest(http.MethodPut, "/v2/acme/app/manifests/latest", nil)
	r.Header.Set("Authorization", "Bearer user-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("manifest put: status %d, want 200", w.Code)
	}
}

func TestPullRateLimitDisabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := PullRateLimit(next, nil, nil, nil, logger.New())
	for i := 0; i < 5; i++ {
		if w := doManifestGet(h, "whatever"); w.Code != http.StatusOK {
			t.Fatalf("disabled limiter blocked request %d", i+1)
		}
	}
}
