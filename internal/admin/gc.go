package admin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/distribution/distribution/v3"
	regstorage "github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/internal/registry/fsdriver"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
)

var ErrAlreadyRunning = errors.New("garbage collection is already running")

// Pauses registry writes for the length of a sweep
type WriteGate interface {
	Freeze(ctx context.Context) error
	Thaw()
}

// Longest wait for running uploads before a run gives up
const gcDrainTimeout = 2 * time.Minute

// One sweep per storage at a time across every instance
const (
	gcLock      = "gc"
	gcLease     = time.Minute
	gcStatusKey = cache.Prefix + "gc:status"
)

// Run is one completed collection
type Run struct {
	StartedAt      time.Time
	FinishedAt     time.Time
	DryRun         bool
	RemoveUntagged bool
	// Removed by the sweep, or eligible when the run was dry
	BlobsDeleted int64
	BytesFreed   int64
	Err          string
	// Instance that swept, empty without a shared cache
	RanOn string
}

// State is what every instance reports about the collector
type State struct {
	Running   bool
	RunningOn string
	Last      *Run
}

// Collector runs mark and sweep over registry storage
type Collector struct {
	driver     driver.StorageDriver
	registry   distribution.Namespace
	log        *logger.Logger
	gate       WriteGate
	resetCache func(context.Context) error
	shared     *cache.Client

	mu       sync.Mutex
	running  bool
	last     *Run
	lastDue  time.Time
	onFinish []func(*Run)
}

func NewCollector(storagePath string, log *logger.Logger) (*Collector, error) {
	// Fresh installs lack the layout mark and sweep walks
	base := filepath.Join(storagePath, "docker", "registry", "v2")
	for _, dir := range []string{"repositories", filepath.Join("blobs", "sha256")} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0755); err != nil {
			return nil, fmt.Errorf("creating registry layout: %w", err)
		}
	}

	// Sweeps only delete, the sync mode never matters here
	d := fsdriver.New(fsdriver.Params{RootDirectory: storagePath})
	reg, err := regstorage.NewRegistry(context.Background(), d)
	if err != nil {
		return nil, fmt.Errorf("creating registry namespace: %w", err)
	}
	return &Collector{driver: d, registry: reg, log: log}, nil
}

// Runs hold registry writes while they sweep
func (c *Collector) SetWriteGate(g WriteGate) {
	c.gate = g
}

// Drops blob descriptor caches once a sweep finishes
func (c *Collector) SetCacheReset(fn func(context.Context) error) {
	c.resetCache = fn
}

// Coordinates runs and status across instances on one storage
func (c *Collector) SetShared(s *cache.Client) {
	c.shared = s
}

// Runs after every completed run, dry or not
func (c *Collector) OnFinish(fn func(*Run)) {
	c.mu.Lock()
	c.onFinish = append(c.onFinish, fn)
	c.mu.Unlock()
}

// Start begins a background run rejecting overlap anywhere
func (c *Collector) Start(dryRun, removeUntagged bool) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return ErrAlreadyRunning
	}
	c.running = true
	c.mu.Unlock()

	ctx := context.Background()
	lock, ok, err := c.shared.TryLock(ctx, gcLock, gcLease)
	if err != nil {
		c.setRunning(false)
		return fmt.Errorf("claiming the gc lease: %w", err)
	}
	if !ok {
		c.setRunning(false)
		if holder, _ := c.shared.LockHolder(ctx, gcLock); holder != "" {
			return fmt.Errorf("%w on %s", ErrAlreadyRunning, holder)
		}
		return ErrAlreadyRunning
	}

	go func() {
		stop := lock.KeepAlive(ctx)
		defer func() {
			stop()
			_ = lock.Unlock(ctx)
		}()
		c.collect(dryRun, removeUntagged)
	}()
	return nil
}

func (c *Collector) setRunning(v bool) {
	c.mu.Lock()
	c.running = v
	c.mu.Unlock()
}

// Status is the local view, kept for callers that predate State
func (c *Collector) Status() (bool, *Run) {
	st := c.State()
	return st.Running, st.Last
}

// State merges what this process knows with what the cluster reports
func (c *Collector) State() State {
	if c.shared.Enabled() {
		var st State
		if hit, err := c.shared.Get(context.Background(), gcStatusKey, &st); err == nil && hit {
			return st
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st := State{Running: c.running}
	if c.running {
		st.RunningOn = c.shared.ID()
	}
	if c.last != nil {
		last := *c.last
		st.Last = &last
	}
	return st
}

func (c *Collector) publish(st State) {
	if !c.shared.Enabled() {
		return
	}
	if err := c.shared.Set(context.Background(), gcStatusKey, st, 0); err != nil {
		c.log.Warn("GC status not shared: %v", err)
	}
}

// Schedule runs collections when live settings say one is due
func (c *Collector) Schedule(ctx context.Context, res *settings.Resolver) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				gc := res.System(ctx).GetGc()
				if !gc.GetEnabled() {
					continue
				}
				interval := time.Duration(gc.GetIntervalHours()) * time.Hour
				if interval <= 0 {
					interval = 24 * time.Hour
				}
				if !c.due(ctx, interval) {
					continue
				}
				if err := c.Start(false, gc.GetRemoveUntagged()); err != nil {
					if errors.Is(err, ErrAlreadyRunning) {
						c.log.Info("Scheduled GC skipped: %v", err)
					} else {
						c.log.Warn("Scheduled GC skipped: %v", err)
					}
				}
			}
		}
	}()
}

// Decides whether a scheduled run is due and claims the slot.
// The shared clock survives restarts and keeps instances from piling on
func (c *Collector) due(ctx context.Context, interval time.Duration) bool {
	if c.shared.Enabled() {
		if due, err := c.shared.ClaimDue(ctx, gcLock, interval); err == nil {
			return due
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.lastDue) < interval {
		return false
	}
	c.lastDue = time.Now()
	return true
}

func (c *Collector) collect(dryRun, removeUntagged bool) {
	run := &Run{StartedAt: time.Now().UTC(), DryRun: dryRun, RemoveUntagged: removeUntagged, RanOn: c.shared.ID()}
	c.log.Info("GC started (dry_run=%v remove_untagged=%v)", dryRun, removeUntagged)
	c.publish(State{Running: true, RunningOn: c.shared.ID(), Last: c.State().Last})
	defer c.finish(run)

	// A manifest landing mid sweep could lose layers
	if !dryRun && c.gate != nil {
		ctx, cancel := context.WithTimeout(context.Background(), gcDrainTimeout)
		err := c.gate.Freeze(ctx)
		cancel()
		if err != nil {
			run.Err = fmt.Sprintf("pausing registry writes: %v", err)
			c.log.Error("GC aborted: %s", run.Err)
			return
		}
		defer c.gate.Thaw()
		c.log.Info("GC paused registry writes")
	}

	// Every removal goes through the driver, so a dry run sweeps for
	// real against a driver that counts and deletes nothing
	counter := &sweepCounter{StorageDriver: c.driver, dryRun: dryRun}
	err := regstorage.MarkAndSweep(context.Background(), counter, c.registry, regstorage.GCOpts{
		RemoveUntagged: removeUntagged,
		Quiet:          true,
	})
	if err != nil {
		run.Err = err.Error()
		c.log.Error("GC failed: %v", err)
	}
	run.BlobsDeleted, run.BytesFreed = counter.blobs.Load(), counter.bytes.Load()

	// Cached descriptors would keep reporting swept blobs as present
	if !dryRun && c.resetCache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		rerr := c.resetCache(ctx)
		cancel()
		if rerr != nil {
			c.log.Error("GC blob cache reset failed: %v", rerr)
			if run.Err == "" {
				run.Err = fmt.Sprintf("resetting blob cache: %v", rerr)
			}
		}
	}

	if err == nil {
		c.log.Info("GC finished in %s: %d blobs removed, %d bytes freed",
			time.Since(run.StartedAt).Round(time.Millisecond), run.BlobsDeleted, run.BytesFreed)
	}
}

// Hooks run before the run reports done, so Running false means
// every finish hook has returned
func (c *Collector) finish(run *Run) {
	run.FinishedAt = time.Now().UTC()
	c.mu.Lock()
	hooks := append([]func(*Run){}, c.onFinish...)
	c.mu.Unlock()
	for _, fn := range hooks {
		fn(run)
	}
	c.mu.Lock()
	c.running = false
	c.last = run
	c.mu.Unlock()
	last := *run
	c.publish(State{Last: &last})
}

// Measures blob directories as the sweep removes them, so a run reports
// exact numbers without walking the store inside the write freeze.
// A dry run measures the same way and leaves everything in place
type sweepCounter struct {
	driver.StorageDriver
	dryRun bool
	blobs  atomic.Int64
	bytes  atomic.Int64
}

// Global blob directories sit three segments under the blob root
const blobRoot = "/docker/registry/v2/blobs/"

func isBlobDir(p string) bool {
	rest, ok := strings.CutPrefix(p, blobRoot)
	return ok && strings.Count(rest, "/") == 2 && !strings.HasSuffix(rest, "/")
}

func (c *sweepCounter) Delete(ctx context.Context, p string) error {
	var size int64
	blob := isBlobDir(p)
	if blob {
		if fi, err := c.StorageDriver.Stat(ctx, path.Join(p, "data")); err == nil {
			size = fi.Size()
		}
	}
	if !c.dryRun {
		if err := c.StorageDriver.Delete(ctx, p); err != nil {
			return err
		}
	}
	if blob {
		c.blobs.Add(1)
		c.bytes.Add(size)
	}
	return nil
}
