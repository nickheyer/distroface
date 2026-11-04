package registry

import (
	"context"
	"testing"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/pkg/logger"
)

func TestRepoCountersBatchAndFlush(t *testing.T) {
	store := storetest.New(t)
	ctx := context.Background()
	if err := store.CreateRepository(ctx, &storage.Repository{ID: "r1", Namespace: "ns", Name: "app"}); err != nil {
		t.Fatal(err)
	}
	counts := func() (int64, int64, bool) {
		t.Helper()
		got, err := store.GetRepository(ctx, "ns", "app")
		if err != nil || got == nil {
			t.Fatalf("GetRepository = %v, %v", got, err)
		}
		return got.PullCount, got.PushCount, got.LastPush != nil
	}

	c := newRepoCounters(store, logger.New(), time.Hour)
	defer c.Close()
	for range 3 {
		c.pulled("ns", "app")
	}
	for range 2 {
		c.pushed("ns", "app")
	}
	c.pulled("ns", "missing")
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 2 {
		t.Fatalf("pending entries = %d, want one per repository", pending)
	}
	if pulls, pushes, pushed := counts(); pulls != 0 || pushes != 0 || pushed {
		t.Fatalf("counts reached the database before a flush: %d %d %v", pulls, pushes, pushed)
	}
	c.flush()
	if pulls, pushes, pushed := counts(); pulls != 3 || pushes != 2 || !pushed {
		t.Fatalf("after flush pulls=%d pushes=%d pushed=%v", pulls, pushes, pushed)
	}
	c.flush()
	if pulls, pushes, _ := counts(); pulls != 3 || pushes != 2 {
		t.Fatalf("empty flush changed counts to %d %d", pulls, pushes)
	}

	// The ticker flushes on its own
	ticking := newRepoCounters(store, logger.New(), 10*time.Millisecond)
	ticking.pulled("ns", "app")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pulls, _, _ := counts(); pulls == 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pulls, _, _ := counts(); pulls != 4 {
		t.Errorf("ticker never flushed, pulls=%d", pulls)
	}
	ticking.Close()

	// Close writes what is left
	last := newRepoCounters(store, logger.New(), time.Hour)
	last.pushed("ns", "app")
	last.Close()
	last.Close()
	if _, pushes, _ := counts(); pushes != 3 {
		t.Errorf("close lost the final push, pushes=%d", pushes)
	}
	var none *repoCounters
	none.pulled("ns", "app")
	none.Close()
}

func TestRepoCountersKeepDeltasWhenTheWriteFails(t *testing.T) {
	store := storetest.New(t)
	c := newRepoCounters(store, logger.New(), time.Hour)
	c.pulled("ns", "app")
	c.pushed("ns", "app")
	c.pushed("ns", "app")
	_ = store.Close()
	c.flush()
	c.mu.Lock()
	e := c.pending[repoKey{"ns", "app"}]
	c.mu.Unlock()
	if e == nil || e.Pulls != 1 || e.Pushes != 2 || e.LastPush.IsZero() {
		t.Fatalf("failed flush left %+v", e)
	}
	// A second failure merges rather than replaces
	c.pulled("ns", "app")
	c.flush()
	c.mu.Lock()
	e = c.pending[repoKey{"ns", "app"}]
	c.mu.Unlock()
	if e == nil || e.Pulls != 2 || e.Pushes != 2 {
		t.Fatalf("second failed flush left %+v", e)
	}
	c.Close()
}
