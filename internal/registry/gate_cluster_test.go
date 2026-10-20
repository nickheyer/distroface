package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	dfcache "github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/internal/cache/cachetest"
	"github.com/nickheyer/distroface/pkg/logger"
)

func clusterGate(t *testing.T, srv *miniredis.Miniredis) (*WriteGate, *dfcache.Client) {
	t.Helper()
	client := cachetest.Attach(t, srv)
	g := NewWriteGate()
	g.SetShared(context.Background(), client, logger.New())
	t.Cleanup(g.Close)
	return g, client
}

// clusterGatePoll is clusterGate with a shortened safety net cadence.
// setPoll must run before SetShared starts the watcher so the goroutine
// never races the write.
func clusterGatePoll(t *testing.T, srv *miniredis.Miniredis, poll time.Duration) (*WriteGate, *dfcache.Client) {
	t.Helper()
	client := cachetest.Attach(t, srv)
	g := NewWriteGate()
	g.setPoll(poll)
	g.SetShared(context.Background(), client, logger.New())
	t.Cleanup(g.Close)
	return g, client
}

func putStatus(h http.Handler) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v2/ns/app/manifests/latest", nil))
	return rec.Code
}

func waitStatus(t *testing.T, h http.Handler, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if putStatus(h) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("peer never reached status %d, last %d", want, putStatus(h))
}

func TestWriteGateFreezePausesPeers(t *testing.T) {
	srv := miniredis.RunT(t)
	leader, _ := clusterGate(t, srv)
	peer, _ := clusterGate(t, srv)
	accept := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	peerHandler := peer.Handler(accept)
	time.Sleep(100 * time.Millisecond)

	if err := leader.Freeze(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, peerHandler, http.StatusServiceUnavailable)
	if err := peer.Freeze(context.Background()); !errors.Is(err, ErrGateFrozen) {
		t.Errorf("peer Freeze error = %v", err)
	}

	leader.Thaw()
	waitStatus(t, peerHandler, http.StatusAccepted)
	if putStatus(leader.Handler(accept)) != http.StatusAccepted {
		t.Error("leader still frozen after thaw")
	}
}

func TestWriteGateFreezeWaitsForPeerWrites(t *testing.T) {
	srv := miniredis.RunT(t)
	leader, _ := clusterGate(t, srv)
	peer, _ := clusterGate(t, srv)
	entered := make(chan struct{})
	release := make(chan struct{})
	peerHandler := peer.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	time.Sleep(100 * time.Millisecond)
	go peerHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPatch, "/v2/ns/app/blobs/uploads/abc", nil))
	<-entered

	frozen := make(chan error, 1)
	go func() { frozen <- leader.Freeze(context.Background()) }()
	select {
	case err := <-frozen:
		t.Fatalf("Freeze returned %v while a peer write was running", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-frozen; err != nil {
		t.Fatal(err)
	}
	leader.Thaw()
}

func TestWriteGateFreezeGivesUpOnStuckPeer(t *testing.T) {
	srv := miniredis.RunT(t)
	leader, _ := clusterGate(t, srv)
	peer, _ := clusterGate(t, srv)
	entered := make(chan struct{})
	release := make(chan struct{})
	peerHandler := peer.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	defer close(release)
	time.Sleep(100 * time.Millisecond)
	go peerHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/v2/ns/app/blobs/uploads/abc", nil))
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := leader.Freeze(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Freeze error = %v", err)
	}
	if !leader.enter() {
		t.Error("leader stayed frozen after a failed drain")
	}
	leader.leave()
	if holder, _ := leader.shared.LockHolder(context.Background(), gateLock); holder != "" {
		t.Errorf("lease kept by %q after a failed freeze", holder)
	}
}

func TestWriteGatePeerThawsWhenLeaseLapses(t *testing.T) {
	srv := miniredis.RunT(t)
	leader, leaderClient := clusterGate(t, srv)
	// The peer is the one that must notice the lapse, so it polls fast
	peer, _ := clusterGatePoll(t, srv, 50*time.Millisecond)
	accept := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	peerHandler := peer.Handler(accept)
	time.Sleep(100 * time.Millisecond)

	if err := leader.Freeze(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, peerHandler, http.StatusServiceUnavailable)

	// The leader dies without thawing, its lease runs out
	leader.Close()
	_ = leaderClient.Close()
	srv.FastForward(2 * gateLease)
	waitStatus(t, peerHandler, http.StatusAccepted)
}
