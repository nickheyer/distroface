package gitrepo

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

// Caches expensive per commit results and dedupes concurrent misses
type Memo[V any] struct {
	ttl      time.Duration
	max      int
	mu       sync.Mutex
	entries  map[string]*memoEntry[V]
	inflight map[string]*memoCall[V]
}

type memoEntry[V any] struct {
	value V
	at    time.Time
}

type memoCall[V any] struct {
	done  chan struct{}
	value V
	err   error
}

func NewMemo[V any](ttl time.Duration, size int) *Memo[V] {
	return &Memo[V]{ttl: ttl, max: size, entries: map[string]*memoEntry[V]{}, inflight: map[string]*memoCall[V]{}}
}

// Returns the cached value or computes it once for every concurrent caller
func (m *Memo[V]) Get(ctx context.Context, key string, compute func() (V, error)) (V, error) {
	m.mu.Lock()
	if e, ok := m.entries[key]; ok && time.Since(e.at) < m.ttl {
		m.mu.Unlock()
		return e.value, nil
	}
	if c, ok := m.inflight[key]; ok {
		m.mu.Unlock()
		select {
		case <-c.done:
			return c.value, c.err
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		}
	}
	c := &memoCall[V]{done: make(chan struct{})}
	m.inflight[key] = c
	m.mu.Unlock()

	c.value, c.err = compute()

	m.mu.Lock()
	delete(m.inflight, key)
	if c.err == nil {
		m.evictLocked()
		m.entries[key] = &memoEntry[V]{value: c.value, at: time.Now()}
	}
	m.mu.Unlock()
	close(c.done)
	return c.value, c.err
}

// Drops every entry whose key starts with the prefix
func (m *Memo[V]) Forget(prefix string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.entries {
		if strings.HasPrefix(k, prefix) {
			delete(m.entries, k)
		}
	}
}

// Makes room once full, expired first then a tenth of the oldest
func (m *Memo[V]) evictLocked() {
	if m.max <= 0 || len(m.entries) < m.max {
		return
	}
	now := time.Now()
	for k, e := range m.entries {
		if now.Sub(e.at) >= m.ttl {
			delete(m.entries, k)
		}
	}
	if len(m.entries) < m.max {
		return
	}
	// One sort per batch beats a full scan per insert
	type aged struct {
		key string
		at  time.Time
	}
	ordered := make([]aged, 0, len(m.entries))
	for k, e := range m.entries {
		ordered = append(ordered, aged{k, e.at})
	}
	slices.SortFunc(ordered, func(a, b aged) int { return a.at.Compare(b.at) })
	batch := max(m.max/10, 1)
	for i := 0; i < batch && i < len(ordered); i++ {
		delete(m.entries, ordered[i].key)
	}
}
