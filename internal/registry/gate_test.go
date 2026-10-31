package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
