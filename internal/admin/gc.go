package admin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/distribution/distribution/v3"
	regstorage "github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
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

// Run is one completed collection
type Run struct {
	StartedAt      time.Time
	FinishedAt     time.Time
	DryRun         bool
	RemoveUntagged bool
	BlobsDeleted   int64
	BytesFreed     int64
	Err            string
}

// Collector runs mark and sweep over registry storage
type Collector struct {
	driver      driver.StorageDriver
	registry    distribution.Namespace
	storagePath string
	log         *logger.Logger
	gate        WriteGate
	resetCache  func(context.Context) error

	mu      sync.Mutex
	running bool
	last    *Run
	lastDue time.Time
}

func NewCollector(storagePath string, log *logger.Logger) (*Collector, error) {
	// Fresh installs lack the layout mark and sweep walks
	base := filepath.Join(storagePath, "docker", "registry", "v2")
	for _, dir := range []string{"repositories", filepath.Join("blobs", "sha256")} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0755); err != nil {
			return nil, fmt.Errorf("creating registry layout: %w", err)
		}
	}

	d := filesystem.New(filesystem.DriverParameters{
		RootDirectory: storagePath,
		MaxThreads:    100,
	})
	reg, err := regstorage.NewRegistry(context.Background(), d)
	if err != nil {
		return nil, fmt.Errorf("creating registry namespace: %w", err)
	}
	return &Collector{driver: d, registry: reg, storagePath: storagePath, log: log}, nil
}

// Runs hold registry writes while they sweep
func (c *Collector) SetWriteGate(g WriteGate) {
	c.gate = g
}

// Drops blob descriptor caches once a sweep finishes
func (c *Collector) SetCacheReset(fn func(context.Context) error) {
	c.resetCache = fn
}

// Start begins a background run rejecting overlap
func (c *Collector) Start(dryRun, removeUntagged bool) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return ErrAlreadyRunning
	}
	c.running = true
	c.mu.Unlock()

	go c.collect(dryRun, removeUntagged)
	return nil
}

func (c *Collector) Status() (bool, *Run) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		return c.running, nil
	}
	last := *c.last
	return c.running, &last
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
				c.mu.Lock()
				due := time.Since(c.lastDue) >= interval
				if due {
					c.lastDue = time.Now()
				}
				c.mu.Unlock()
				if !due {
					continue
				}
				if err := c.Start(false, gc.GetRemoveUntagged()); err != nil {
					c.log.Warn("Scheduled GC skipped: %v", err)
				}
			}
		}
	}()
}

func (c *Collector) collect(dryRun, removeUntagged bool) {
	run := &Run{StartedAt: time.Now().UTC(), DryRun: dryRun, RemoveUntagged: removeUntagged}
	c.log.Info("GC started (dry_run=%v remove_untagged=%v)", dryRun, removeUntagged)
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

	beforeCount, beforeBytes := c.blobStats()

	err := regstorage.MarkAndSweep(context.Background(), c.driver, c.registry, regstorage.GCOpts{
		DryRun:         dryRun,
		RemoveUntagged: removeUntagged,
		Quiet:          true,
	})
	if err != nil {
		run.Err = err.Error()
		c.log.Error("GC failed: %v", err)
	}

	afterCount, afterBytes := c.blobStats()
	run.BlobsDeleted = beforeCount - afterCount
	run.BytesFreed = beforeBytes - afterBytes

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

func (c *Collector) finish(run *Run) {
	run.FinishedAt = time.Now().UTC()
	c.mu.Lock()
	c.running = false
	c.last = run
	c.mu.Unlock()
}

// Counts blobs and bytes under the sha256 store
func (c *Collector) blobStats() (int64, int64) {
	var count, bytes int64
	blobDir := filepath.Join(c.storagePath, "docker", "registry", "v2", "blobs", "sha256")
	shards, err := os.ReadDir(blobDir)
	if err != nil {
		return 0, 0
	}
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		digests, err := os.ReadDir(filepath.Join(blobDir, shard.Name()))
		if err != nil {
			continue
		}
		for _, d := range digests {
			info, err := os.Stat(filepath.Join(blobDir, shard.Name(), d.Name(), "data"))
			if err != nil {
				continue
			}
			count++
			bytes += info.Size()
		}
	}
	return count, bytes
}
