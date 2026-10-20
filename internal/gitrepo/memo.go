package gitrepo

import (
	"context"
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

// Drops expired entries, then the oldest while over capacity
func (m *Memo[V]) evictLocked() {
	now := time.Now()
	for k, e := range m.entries {
		if now.Sub(e.at) >= m.ttl {
			delete(m.entries, k)
		}
	}
	for m.max > 0 && len(m.entries) >= m.max {
		oldestKey := ""
		var oldest time.Time
		for k, e := range m.entries {
			if oldestKey == "" || e.at.Before(oldest) {
				oldestKey, oldest = k, e.at
			}
		}
		delete(m.entries, oldestKey)
	}
}
