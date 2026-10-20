package artifacts

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
)

var ErrReaperRunning = errors.New("artifact reaper is already running")

// Repos scanned per page during a sweep
const reaperPageSize = 200

// One retention sweep per cluster at a time
const (
	reaperLock  = "artifact-reaper"
	reaperLease = 2 * time.Minute
)

// ReapRun is one completed retention sweep
type ReapRun struct {
	StartedAt           time.Time
	FinishedAt          time.Time
	ReposScanned        int64
	StaleUploadsRemoved int
	Err                 string
}

// Reaper applies retention policies across every repo on a schedule
type Reaper struct {
	mgr   *Manager
	store *stores.Store
	log   *logger.Logger

	mu      sync.Mutex
	running bool
	last    *ReapRun
	lastRun time.Time
	shared  *cache.Client
}

func NewReaper(mgr *Manager, store *stores.Store, log *logger.Logger) *Reaper {
	return &Reaper{mgr: mgr, store: store, log: log}
}

// Coordinates sweeps across instances
func (r *Reaper) SetShared(c *cache.Client) {
	r.shared = c
}

// Start begins a background sweep rejecting overlap
func (r *Reaper) Start() error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrReaperRunning
	}
	r.running = true
	r.mu.Unlock()

	ctx := context.Background()
	lock, ok, err := r.shared.TryLock(ctx, reaperLock, reaperLease)
	if err != nil {
		r.log.Warn("Artifact reaper lease: %v", err)
	} else if !ok {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		return ErrReaperRunning
	}

	go func() {
		stop := lock.KeepAlive(ctx)
		defer func() {
			stop()
			_ = lock.Unlock(ctx)
		}()
		r.sweep()
	}()
	return nil
}

// Schedule sweeps when live settings say a run is due
func (r *Reaper) Schedule(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg := r.mgr.res.System(ctx).GetArtifacts().GetReaper()
				if !cfg.GetEnabled() {
					continue
				}
				interval := time.Duration(cfg.GetIntervalHours()) * time.Hour
				if interval <= 0 {
					interval = 24 * time.Hour
				}
				if !r.due(ctx, interval) {
					continue
				}
				if err := r.Start(); err != nil {
					r.log.Warn("Scheduled artifact reap skipped: %v", err)
				}
			}
		}
	}()
}

// Shared clock when a cache is on, the local one otherwise
func (r *Reaper) due(ctx context.Context, interval time.Duration) bool {
	if r.shared.Enabled() {
		if due, err := r.shared.ClaimDue(ctx, reaperLock, interval); err == nil {
			return due
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.lastRun) < interval {
		return false
	}
	r.lastRun = time.Now()
	return true
}

func (r *Reaper) sweep() {
	run := &ReapRun{StartedAt: time.Now().UTC()}
	ctx := context.Background()
	r.log.Info("Artifact reaper started")

	byNamespace := make(map[string]RetentionPolicy)
	offset := 0
	for {
		repos, _, err := r.store.ListArtifactRepositories(ctx, stores.ArtifactRepoListOptions{
			IncludePrivate: true,
			Limit:          reaperPageSize,
			Offset:         offset,
		})
		if err != nil {
			run.Err = err.Error()
			r.log.Error("Artifact reaper repo list failed: %v", err)
			break
		}
		if len(repos) == 0 {
			break
		}
		for _, repo := range repos {
			policy, ok := byNamespace[repo.Namespace]
			if !ok {
				policy = r.mgr.EffectiveRetention(ctx, repo.Namespace)
				byNamespace[repo.Namespace] = policy
			}
			if err := r.mgr.ApplyRetentionPolicy(ctx, repo.ID, policy); err != nil {
				r.log.Error("Artifact reaper retention for repo %d: %v", repo.ID, err)
			}
			run.ReposScanned++
		}
		if len(repos) < reaperPageSize {
			break
		}
		offset += reaperPageSize
	}

	if staleAge := r.mgr.StaleUploadAge(ctx); staleAge > 0 {
		if removed, err := r.mgr.Blobs().CleanStaleUploads(staleAge); err != nil {
			r.log.Error("Artifact reaper stale upload cleanup: %v", err)
		} else {
			run.StaleUploadsRemoved = removed
		}
	}

	run.FinishedAt = time.Now().UTC()
	if run.Err == "" {
		r.log.Info("Artifact reaper finished in %s: %d repos scanned, %d stale uploads removed",
			run.FinishedAt.Sub(run.StartedAt).Round(time.Millisecond), run.ReposScanned, run.StaleUploadsRemoved)
	}

	r.mu.Lock()
	r.running = false
	r.last = run
	r.mu.Unlock()
}
