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
	run.FinishedAt = time.Now().UTC()

	if err == nil {
		c.log.Info("GC finished in %s: %d blobs removed, %d bytes freed",
			run.FinishedAt.Sub(run.StartedAt).Round(time.Millisecond), run.BlobsDeleted, run.BytesFreed)
	}

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
