package stores

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db"
)

func TestAddRepositoryCounts(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, name := range []string{"a", "b"} {
		if err := store.CreateRepository(ctx, &db.Repository{ID: "r-" + name, Namespace: "ns", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	pushedAt := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	err = store.AddRepositoryCounts(ctx, []RepoCount{
		{Namespace: "ns", Name: "a", Pulls: 5},
		{Namespace: "ns", Name: "b", Pushes: 2, LastPush: pushedAt},
		{Namespace: "ns", Name: "missing", Pulls: 1},
		{Namespace: "ns", Name: "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.GetRepository(ctx, "ns", "a")
	if a.PullCount != 5 || a.PushCount != 0 || a.LastPush != nil {
		t.Errorf("a = pulls %d pushes %d last %v", a.PullCount, a.PushCount, a.LastPush)
	}
	b, _ := store.GetRepository(ctx, "ns", "b")
	if b.PullCount != 0 || b.PushCount != 2 || b.LastPush == nil || !b.LastPush.Equal(pushedAt) {
		t.Errorf("b = pulls %d pushes %d last %v", b.PullCount, b.PushCount, b.LastPush)
	}
	if err := store.AddRepositoryCounts(ctx, nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
	// Deltas accumulate across batches
	if err := store.AddRepositoryCounts(ctx, []RepoCount{{Namespace: "ns", Name: "a", Pulls: 1, Pushes: 1, LastPush: pushedAt}}); err != nil {
		t.Fatal(err)
	}
	a, _ = store.GetRepository(ctx, "ns", "a")
	if a.PullCount != 6 || a.PushCount != 1 || a.LastPush == nil {
		t.Errorf("a after second batch = pulls %d pushes %d last %v", a.PullCount, a.PushCount, a.LastPush)
	}
}
