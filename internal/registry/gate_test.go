package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteGateRejectsWritesWhileFrozen(t *testing.T) {
	g := NewWriteGate()
	var served atomic.Int32
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	if err := g.Freeze(context.Background()); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v2/ns/app/manifests/latest", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("frozen PUT status = %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("frozen PUT lacks Retry-After")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/ns/app/manifests/latest", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("frozen GET status = %d", rec.Code)
	}

	g.Thaw()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v2/ns/app/manifests/latest", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("thawed PUT status = %d", rec.Code)
	}
	if served.Load() != 2 {
		t.Errorf("served = %d, want 2", served.Load())
	}
}

func TestWriteGateFreezeWaitsForInflightWrites(t *testing.T) {
	g := NewWriteGate()
	entered := make(chan struct{})
	release := make(chan struct{})
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPatch, "/v2/ns/app/blobs/uploads/abc", nil))
	<-entered

	frozen := make(chan error, 1)
	go func() { frozen <- g.Freeze(context.Background()) }()
	select {
	case err := <-frozen:
		t.Fatalf("Freeze returned %v while a write was running", err)
	case <-time.After(50 * time.Millisecond):
	}

	// New writes bounce as soon as the drain starts
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v2/ns/app/blobs/uploads/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("write during drain status = %d", rec.Code)
	}

	close(release)
	if err := <-frozen; err != nil {
		t.Fatal(err)
	}
	if err := g.Freeze(context.Background()); !errors.Is(err, ErrGateFrozen) {
		t.Errorf("second Freeze error = %v", err)
	}
	g.Thaw()
}

func TestWriteGateFreezeGivesUpOnTimeout(t *testing.T) {
	g := NewWriteGate()
	entered := make(chan struct{})
	release := make(chan struct{})
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/v2/ns/app/blobs/uploads/abc", nil))
	<-entered
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := g.Freeze(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Freeze error = %v", err)
	}
	if !g.enter() {
		t.Error("gate stayed frozen after a failed drain")
	}
	g.leave()
}

// Locks the write classification so GC's pause guarantee keeps covering
// every write route docker clients use, and nothing else
func TestIsRegistryWriteClassification(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		// Reads pass during a freeze
		{http.MethodGet, "/v2/", false},
		{http.MethodGet, "/v2/ns/app/manifests/latest", false},
		{http.MethodHead, "/v2/ns/app/manifests/latest", false},
		{http.MethodGet, "/v2/ns/app/blobs/sha256:aa", false},
		{http.MethodGet, "/v2/ns/app/tags/list", false},
		{http.MethodGet, "/v2/auth", false},
		{http.MethodGet, "/v2", false},
		// Prefix lookalikes are not the registry
		{http.MethodGet, "/v2x/", false},
		{http.MethodPut, "/v2x/ns/app/manifests/latest", false},
		// Upload lifecycle writes are gated
		{http.MethodPost, "/v2/ns/app/blobs/uploads/", true},
		{http.MethodPost, "/v2/ns/app/blobs/uploads/?mount=sha256:aa&from=ns/other", true},
		{http.MethodPost, "/v2/ns/app/blobs/uploads/?digest=sha256:aa", true},
		{http.MethodPatch, "/v2/ns/app/blobs/uploads/abc", true},
		{http.MethodPut, "/v2/ns/app/blobs/uploads/abc?digest=sha256:aa", true},
		{http.MethodDelete, "/v2/ns/app/blobs/uploads/abc", true},
		// Manifest writes are gated
		{http.MethodPut, "/v2/ns/app/manifests/latest", true},
		{http.MethodDelete, "/v2/ns/app/manifests/sha256:aa", true},
		// Preflight and non registry paths are never gated
		{http.MethodOptions, "/v2/ns/app/blobs/uploads/", false},
		{http.MethodDelete, "/api/v1/repositories/123", false},
		{http.MethodPost, "/auth/token", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := isRegistryWrite(r); got != tc.want {
			t.Errorf("isRegistryWrite(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// Retry-After follows the previous freeze length minus the time frozen
func TestWriteGateRetryAfterTracksFreezeLength(t *testing.T) {
	g := NewWriteGate()
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	retry := func() int {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v2/ns/app/manifests/latest", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("frozen PUT status = %d", rec.Code)
		}
		n, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		if err != nil {
			t.Fatalf("Retry-After %q: %v", rec.Header().Get("Retry-After"), err)
		}
		return n
	}
	set := func(last time.Duration, frozenFor time.Duration) {
		g.mu.Lock()
		g.lastFreeze, g.frozenAt = last, time.Now().Add(-frozenFor)
		g.mu.Unlock()
	}
	if err := g.Freeze(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := retry(); got != int(defaultFreezeEstimate/time.Second) {
		t.Errorf("fresh gate Retry-After = %d, want %d", got, defaultFreezeEstimate/time.Second)
	}
	// Ten second sweeps, seven seconds in
	set(10*time.Second, 7*time.Second)
	if got := retry(); got != 3 {
		t.Errorf("Retry-After = %d, want 3", got)
	}
	// Back at the default cadence once the estimate ran out
	set(10*time.Second, time.Hour)
	if got := retry(); got != int(defaultFreezeEstimate/time.Second) {
		t.Errorf("Retry-After past the estimate = %d, want %d", got, defaultFreezeEstimate/time.Second)
	}
	// Never past the lease, a dead leader thaws peers by then
	set(time.Hour, 0)
	if got := retry(); got != int(gateLease/time.Second) {
		t.Errorf("Retry-After for a long sweep = %d, want %d", got, gateLease/time.Second)
	}
	// Thaw measures the freeze for the next run
	set(0, 2*time.Second)
	g.Thaw()
	g.mu.Lock()
	last, frozenAt := g.lastFreeze, g.frozenAt
	g.mu.Unlock()
	if last < 2*time.Second || !frozenAt.IsZero() {
		t.Errorf("after thaw lastFreeze=%v frozenAt=%v", last, frozenAt)
	}
}

// A freeze that fails to drain leaves no estimate behind
func TestWriteGateFailedFreezeLeavesEstimateAlone(t *testing.T) {
	g := NewWriteGate()
	entered := make(chan struct{})
	release := make(chan struct{})
	h := g.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/v2/ns/app/blobs/uploads/abc", nil))
	<-entered
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := g.Freeze(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Freeze error = %v", err)
	}
	g.mu.Lock()
	last, frozenAt := g.lastFreeze, g.frozenAt
	g.mu.Unlock()
	if last != 0 || !frozenAt.IsZero() {
		t.Errorf("failed freeze recorded lastFreeze=%v frozenAt=%v", last, frozenAt)
	}
}
