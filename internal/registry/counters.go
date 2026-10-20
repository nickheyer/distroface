package registry

import (
	"context"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Flush cadence and the time one flush may take
const (
	counterFlushInterval = time.Second
	counterFlushTimeout  = 10 * time.Second
)

// Pull and push counters batched off the request path, one update per
// repository per flush instead of one per request
type repoCounters struct {
	store    *stores.Store
	log      *logger.Logger
	interval time.Duration

	mu      sync.Mutex
	pending map[repoKey]*stores.RepoCount

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

type repoKey struct{ namespace, name string }

func newRepoCounters(store *stores.Store, log *logger.Logger, interval time.Duration) *repoCounters {
	c := &repoCounters{
		store:    store,
		log:      log,
		interval: interval,
		pending:  map[repoKey]*stores.RepoCount{},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go c.run()
	return c
}

func (c *repoCounters) pulled(namespace, name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entry(namespace, name).Pulls++
	c.mu.Unlock()
}

func (c *repoCounters) pushed(namespace, name string) {
	if c == nil {
		return
	}
	now := time.Now().UTC()
	c.mu.Lock()
	e := c.entry(namespace, name)
	e.Pushes++
	if now.After(e.LastPush) {
		e.LastPush = now
	}
	c.mu.Unlock()
}

func (c *repoCounters) entry(namespace, name string) *stores.RepoCount {
	key := repoKey{namespace, name}
	e := c.pending[key]
	if e == nil {
		e = &stores.RepoCount{Namespace: namespace, Name: name}
		c.pending[key] = e
	}
	return e
}

func (c *repoCounters) run() {
	defer close(c.done)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.flush()
		case <-c.stop:
			c.flush()
			return
		}
	}
}

// Writes everything pending, a failed write puts the deltas back
func (c *repoCounters) flush() {
	c.mu.Lock()
	batch := c.pending
	c.pending = map[repoKey]*stores.RepoCount{}
	c.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	counts := make([]stores.RepoCount, 0, len(batch))
	for _, e := range batch {
		counts = append(counts, *e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), counterFlushTimeout)
	defer cancel()
	if err := c.store.AddRepositoryCounts(ctx, counts); err != nil {
		c.log.Error("listener: flushing repository counters: %v", err)
		c.requeue(batch)
	}
}

// Failed deltas merge back so a database blip loses nothing
func (c *repoCounters) requeue(batch map[repoKey]*stores.RepoCount) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range batch {
		cur := c.pending[key]
		if cur == nil {
			c.pending[key] = e
			continue
		}
		cur.Pulls += e.Pulls
		cur.Pushes += e.Pushes
		if e.LastPush.After(cur.LastPush) {
			cur.LastPush = e.LastPush
		}
	}
}

// Stops the ticker and writes what is left
func (c *repoCounters) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		close(c.stop)
		<-c.done
	})
}

// Stops the counters once requests stopped, the final flush lands
func closeListenerCounters() {
	if deps := listenerDepsPtr.Load(); deps != nil {
		deps.counters.Close()
	}
}
