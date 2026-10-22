package gitrepo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoDedupesConcurrentMisses(t *testing.T) {
	m := NewMemo[int](time.Minute, 10)
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := m.Get(context.Background(), "k", func() (int, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return 42, nil
			})
			if err != nil || v != 42 {
				t.Errorf("got %d, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("compute ran %d times", calls.Load())
	}
}

func TestMemoErrorsAreNotCached(t *testing.T) {
	m := NewMemo[int](time.Minute, 10)
	boom := errors.New("boom")
	if _, err := m.Get(context.Background(), "k", func() (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("expected error, got %v", err)
	}
	v, err := m.Get(context.Background(), "k", func() (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Fatalf("got %d, %v", v, err)
	}
}

func TestMemoEvictsOverCapacityAndForgets(t *testing.T) {
	m := NewMemo[int](time.Minute, 2)
	for i, k := range []string{"a@1", "a@2", "b@1"} {
		_, _ = m.Get(context.Background(), k, func() (int, error) { return i, nil })
	}
	if len(m.entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m.entries))
	}
	m.Forget("a@")
	if _, ok := m.entries["b@1"]; !ok || len(m.entries) != 1 {
		t.Fatalf("forget kept %v", m.entries)
	}
}

func TestMemoBurstKeepsNewest(t *testing.T) {
	m := NewMemo[int](time.Minute, 50)
	for i := range 50 {
		k := "old" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		_, _ = m.Get(context.Background(), k, func() (int, error) { return i, nil })
	}
	if len(m.entries) != 50 {
		t.Fatalf("want 50 entries, got %d", len(m.entries))
	}
	for i := range 10 {
		k := "new" + string(rune('a'+i))
		_, _ = m.Get(context.Background(), k, func() (int, error) { return 100 + i, nil })
	}
	if len(m.entries) > 50 {
		t.Fatalf("over capacity with %d entries", len(m.entries))
	}
	for i := range 10 {
		k := "new" + string(rune('a'+i))
		e, ok := m.entries[k]
		if !ok || e.value != 100+i {
			t.Fatalf("burst key %s lost, entries %d", k, len(m.entries))
		}
	}
}
