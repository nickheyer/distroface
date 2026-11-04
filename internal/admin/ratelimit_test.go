package admin

import (
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/cache/cachetest"
)

func TestSharedLimiterCountsAcrossInstances(t *testing.T) {
	client, srv := cachetest.New(t)
	peer := cachetest.Attach(t, srv)
	a := NewLimiter(2, time.Minute)
	a.SetShared(client, "auth")
	b := NewLimiter(2, time.Minute)
	b.SetShared(peer, "auth")

	if ok, remaining, _ := a.Take("1.2.3.4"); !ok || remaining != 1 {
		t.Fatalf("first take = %v, %d", ok, remaining)
	}
	if ok, remaining, _ := b.Take("1.2.3.4"); !ok || remaining != 0 {
		t.Fatalf("second take on the peer = %v, %d", ok, remaining)
	}
	if ok, _, resetAt := a.Take("1.2.3.4"); ok || time.Until(resetAt) <= 0 {
		t.Fatalf("third take = %v, reset %s", ok, resetAt)
	}
	if !b.Blocked("1.2.3.4") {
		t.Error("peer does not see the block")
	}
	if a.Blocked("5.6.7.8") {
		t.Error("unrelated key blocked")
	}

	// The window slides and frees the key
	srv.FastForward(2 * time.Minute)
	if b.Blocked("1.2.3.4") {
		t.Error("still blocked after the window")
	}

	b.Record("9.9.9.9")
	b.Record("9.9.9.9")
	if !a.Blocked("9.9.9.9") {
		t.Error("recorded failures not shared")
	}
	a.Reset("9.9.9.9")
	if b.Blocked("9.9.9.9") {
		t.Error("reset did not reach the peer")
	}
}

// Limiters under different names never see each other's counts
func TestSharedLimiterNamespaces(t *testing.T) {
	client, _ := cachetest.New(t)
	pull := NewLimiter(1, time.Minute)
	pull.SetShared(client, "pull")
	auth := NewLimiter(1, time.Minute)
	auth.SetShared(client, "auth")
	if ok, _, _ := pull.Take("u"); !ok {
		t.Fatal("first pull refused")
	}
	if ok, _, _ := auth.Take("u"); !ok {
		t.Fatal("auth limiter shares the pull count")
	}
}

// A dead redis degrades to the in process window
func TestSharedLimiterFallsBackWhenRedisDies(t *testing.T) {
	client, srv := cachetest.New(t)
	l := NewLimiter(1, time.Minute)
	l.SetShared(client, "auth")
	srv.Close()
	if ok, _, _ := l.Take("k"); !ok {
		t.Fatal("first local take refused")
	}
	if ok, _, _ := l.Take("k"); ok {
		t.Fatal("local window did not refuse")
	}
}
