package registry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/distribution/distribution/v3/registry/api/errcode"

	dfcache "github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/pkg/logger"
)

var ErrGateFrozen = errors.New("registry writes are already paused")

// Cluster freeze lease, peers thaw on their own once it lapses
const (
	gateLock       = "gate"
	gateDrainedKey = dfcache.Prefix + "gc:drained:"
	gateChannel    = "gate"
	gateLease      = 60 * time.Second
	gateDrainPoll  = 200 * time.Millisecond
	gateOpTimeout  = 2 * time.Second
)

// Safety net cadence for missed freeze messages, tests shorten it per gate
const defaultGatePoll = 5 * time.Second

// Retry-After without a measured freeze or once the estimate ran out
const defaultFreezeEstimate = 5 * time.Second

// Pauses registry writes so garbage collection sweeps a still tree.
// With a shared cache the pause reaches every instance on the storage
type WriteGate struct {
	mu       sync.Mutex
	frozen   bool
	remote   bool // Paused on behalf of another instance
	inflight int
	drained  chan struct{}
	lock     *dfcache.Lock
	lease    func()

	// Start of the current freeze and the length of the previous one,
	// together they answer how long a bounced writer should wait
	frozenAt   time.Time
	lastFreeze time.Duration

	// Watcher cadence, written only before watch starts, so the
	// watcher goroutine never races a late write to a shared var
	poll time.Duration

	shared *dfcache.Client
	log    *logger.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

func NewWriteGate() *WriteGate {
	return &WriteGate{poll: defaultGatePoll}
}

// setPoll shortens the safety net cadence. Only valid before SetShared
// starts the watcher, later calls are ignored so the goroutine never
// sees a racing write.
func (g *WriteGate) setPoll(d time.Duration) {
	if d <= 0 {
		return
	}
	g.mu.Lock()
	g.poll = d
	g.mu.Unlock()
}

// Joins the cluster wide freeze, remote sweeps pause this instance too
func (g *WriteGate) SetShared(ctx context.Context, shared *dfcache.Client, log *logger.Logger) {
	if !shared.Enabled() {
		return
	}
	if log == nil {
		log = logger.New()
	}
	g.ctx, g.cancel = context.WithCancel(ctx)
	g.shared, g.log = shared, log
	shared.Subscribe(g.ctx, gateChannel, g.onMessage)
	go g.watch()
}

// Stops the freeze watcher
func (g *WriteGate) Close() {
	if g.cancel != nil {
		g.cancel()
	}
}

// Rejects new writes everywhere then waits for running ones
func (g *WriteGate) Freeze(ctx context.Context) error {
	g.mu.Lock()
	if g.frozen {
		g.mu.Unlock()
		return ErrGateFrozen
	}
	g.frozen = true
	g.frozenAt = time.Now()
	var drained chan struct{}
	if g.inflight > 0 {
		drained = make(chan struct{})
		g.drained = drained
	}
	g.mu.Unlock()

	if g.shared.Enabled() {
		if err := g.freezeCluster(ctx); err != nil {
			g.release(false)
			return err
		}
	}
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
			g.release(false)
			return fmt.Errorf("waiting for in flight registry writes: %w", ctx.Err())
		}
	}
	if g.shared.Enabled() {
		if err := g.awaitPeers(ctx); err != nil {
			g.release(false)
			return err
		}
	}
	return nil
}

// Lets writes through again, peers follow the announcement
func (g *WriteGate) Thaw() {
	g.release(true)
}

// Clears the freeze, a measured one feeds the Retry-After estimate
func (g *WriteGate) release(measured bool) {
	g.mu.Lock()
	lock, lease := g.lock, g.lease
	if measured && !g.frozenAt.IsZero() {
		g.lastFreeze = time.Since(g.frozenAt)
	}
	g.frozen, g.remote, g.drained, g.lock, g.lease = false, false, nil, nil, nil
	g.frozenAt = time.Time{}
	g.mu.Unlock()
	if lease != nil {
		lease()
	}
	if lock != nil {
		ctx, cancel := context.WithTimeout(context.Background(), gateOpTimeout)
		defer cancel()
		_ = lock.Unlock(ctx)
		_ = g.shared.Publish(ctx, gateChannel, "thaw")
	}
}

// Takes the cluster lease and announces the pause
func (g *WriteGate) freezeCluster(ctx context.Context) error {
	lock, ok, err := g.shared.TryLock(ctx, gateLock, gateLease)
	if err != nil {
		return fmt.Errorf("announcing the write freeze: %w", err)
	}
	if !ok {
		holder, _ := g.shared.LockHolder(ctx, gateLock)
		return fmt.Errorf("%w by %s", ErrGateFrozen, holder)
	}
	g.mu.Lock()
	g.lock, g.lease = lock, lock.KeepAlive(g.ctx)
	g.mu.Unlock()
	if err := g.shared.Publish(ctx, gateChannel, "freeze"); err != nil {
		return fmt.Errorf("announcing the write freeze: %w", err)
	}
	return nil
}

// Blocks until every live peer reports its writes drained
func (g *WriteGate) awaitPeers(ctx context.Context) error {
	ticker := time.NewTicker(gateDrainPoll)
	defer ticker.Stop()
	for {
		pending, err := g.undrainedPeers(ctx)
		if err == nil && len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("checking peer drains: %w", err)
			}
			return fmt.Errorf("waiting for registry writes on %s: %w", strings.Join(pending, ", "), ctx.Err())
		case <-ticker.C:
		}
	}
}

func (g *WriteGate) undrainedPeers(ctx context.Context) ([]string, error) {
	ids, err := g.shared.Instances(ctx)
	if err != nil {
		return nil, err
	}
	var peers, keys []string
	for _, id := range ids {
		if id == g.shared.ID() {
			continue
		}
		peers = append(peers, id)
		keys = append(keys, gateDrainedKey+id)
	}
	if len(peers) == 0 {
		return nil, nil
	}
	_, present, err := g.shared.GetMany(ctx, keys...)
	if err != nil {
		return nil, err
	}
	var pending []string
	for i, ok := range present {
		if !ok {
			pending = append(pending, peers[i])
		}
	}
	return pending, nil
}

func (g *WriteGate) onMessage(payload string) {
	switch payload {
	case "freeze":
		g.freezeRemote()
	case "thaw":
		g.thawRemote()
	}
}

// Another instance sweeps, bounce writes and report once idle
func (g *WriteGate) freezeRemote() {
	g.mu.Lock()
	if g.frozen {
		g.mu.Unlock()
		return
	}
	g.frozen, g.remote = true, true
	g.frozenAt = time.Now()
	idle := g.inflight == 0
	g.mu.Unlock()
	if idle {
		g.markDrained()
	}
}

func (g *WriteGate) thawRemote() {
	g.mu.Lock()
	if !g.remote {
		g.mu.Unlock()
		return
	}
	if !g.frozenAt.IsZero() {
		g.lastFreeze = time.Since(g.frozenAt)
	}
	g.frozen, g.remote, g.drained = false, false, nil
	g.frozenAt = time.Time{}
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(g.ctx, gateOpTimeout)
	defer cancel()
	_ = g.shared.Delete(ctx, gateDrainedKey+g.shared.ID())
}

func (g *WriteGate) markDrained() {
	ctx, cancel := context.WithTimeout(g.ctx, gateOpTimeout)
	defer cancel()
	if err := g.shared.SetString(ctx, gateDrainedKey+g.shared.ID(), "1", 2*gateLease); err != nil {
		g.log.Warn("Reporting drained registry writes failed: %v", err)
	}
}

// Polls the lease so a missed message or a dead leader still resolves
func (g *WriteGate) watch() {
	g.reconcile()
	g.mu.Lock()
	ticker := time.NewTicker(g.poll)
	g.mu.Unlock()
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			g.reconcile()
		}
	}
}

func (g *WriteGate) reconcile() {
	ctx, cancel := context.WithTimeout(g.ctx, gateOpTimeout)
	defer cancel()
	holder, err := g.shared.LockHolder(ctx, gateLock)
	if err != nil {
		return
	}
	g.mu.Lock()
	remote, leader := g.remote, g.lock != nil
	g.mu.Unlock()
	switch {
	case holder != "" && holder != g.shared.ID() && !remote && !leader:
		g.freezeRemote()
	case holder == "" && remote:
		g.thawRemote()
	}
}

func (g *WriteGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.frozen {
		return false
	}
	g.inflight++
	return true
}

func (g *WriteGate) leave() {
	g.mu.Lock()
	g.inflight--
	idle := g.frozen && g.inflight == 0
	remote := g.remote
	if idle && g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
	g.mu.Unlock()
	if idle && remote {
		go g.markDrained()
	}
}

// Seconds a bounced writer should wait, the previous freeze length
// minus the time already frozen, back at the default once that ran out
func (g *WriteGate) retryAfter() int {
	g.mu.Lock()
	frozenAt, last := g.frozenAt, g.lastFreeze
	g.mu.Unlock()
	estimate := last
	if estimate <= 0 {
		estimate = defaultFreezeEstimate
	}
	remaining := estimate
	if !frozenAt.IsZero() {
		remaining = estimate - time.Since(frozenAt)
	}
	if remaining <= 0 {
		remaining = defaultFreezeEstimate
	}
	return int(math.Ceil(min(remaining, gateLease).Seconds()))
}

// Reads pass, writes bounce while frozen
func (g *WriteGate) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRegistryWrite(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !g.enter() {
			w.Header().Set("Retry-After", strconv.Itoa(g.retryAfter()))
			_ = errcode.ServeJSON(w, errcode.ErrorCodeUnavailable.WithMessage("registry is read only while garbage collection runs, retry shortly"))
			return
		}
		defer g.leave()
		next.ServeHTTP(w, r)
	})
}

func isRegistryWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return strings.HasPrefix(r.URL.Path, "/v2/")
	}
	return false
}
