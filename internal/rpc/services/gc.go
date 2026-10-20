package services

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.GCServiceHandler = (*GCService)(nil)

// Usage walks the whole blob store, dashboards poll it, so a stale copy
// is served while one refresh runs in the background
const (
	usageLocalTTL    = 30 * time.Second
	usageSharedTTL   = 2 * time.Minute
	usageKey         = cache.Prefix + "gc:usage"
	usageWalkTimeout = 10 * time.Minute
	usageWalkers     = 16
	// Longest a cold caller waits for the walk before answering pending
	usageColdWait = 2 * time.Second
)

// Walks the registry layout, swapped by tests
type usageWalkFunc func(ctx context.Context, root string) (int64, []*v1.StorageUsageEntry, error)

type GCService struct {
	collector    *admin.Collector
	store        *stores.Store
	registryPath string
	res          *settings.Resolver
	log          *logger.Logger
	shared       *cache.Client
	usageWalk    usageWalkFunc

	usageMu sync.Mutex
	usage   *v1.GetStorageUsageResponse
	usageAt time.Time
	// One walk at a time, closed when it ends, nil while idle
	refreshMu   sync.Mutex
	refreshDone chan struct{}
	refreshErr  error
}

func NewGCService(collector *admin.Collector, store *stores.Store, registryPath string, res *settings.Resolver, log *logger.Logger) *GCService {
	s := &GCService{collector: collector, store: store, registryPath: registryPath, res: res, log: log, usageWalk: registryUsage}
	if collector != nil {
		// Freed blobs change the numbers right away
		collector.OnFinish(func(run *admin.Run) {
			if !run.DryRun {
				s.dropUsage()
			}
		})
	}
	return s
}

// Shares usage numbers across instances and reports the backend
func (s *GCService) SetShared(c *cache.Client) {
	s.shared = c
}

// Fills the cache in the background so no dashboard waits, numbers a
// peer already walked are taken over a walk of our own
func (s *GCService) Warm() {
	if _, fresh := s.cachedUsage(context.Background()); fresh {
		return
	}
	s.refreshUsage()
}

func (s *GCService) RunGC(ctx context.Context, req *v1.RunGCRequest) (*v1.RunGCResponse, error) {
	if s.collector == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "garbage collection is not wired")
	}
	if err := s.collector.Start(req.DryRun, req.RemoveUntagged); err != nil {
		if errors.Is(err, admin.ErrAlreadyRunning) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.RunGCResponse{}, nil
}

func (s *GCService) GetGCStatus(ctx context.Context, req *v1.GetGCStatusRequest) (*v1.GetGCStatusResponse, error) {
	gc := s.res.System(ctx).GetGc()
	resp := &v1.GetGCStatusResponse{
		Scheduled:     gc.GetEnabled(),
		IntervalHours: gc.GetIntervalHours(),
		Cache:         s.shared.Summary(),
	}
	if err := s.shared.Err(); err != nil {
		resp.CacheError = err.Error()
	}
	if s.collector == nil {
		return resp, nil
	}
	st := s.collector.State()
	resp.Running, resp.RunningOn = st.Running, st.RunningOn
	if last := st.Last; last != nil {
		resp.LastRun = &v1.GCRun{
			StartedAt:      timestamppb.New(last.StartedAt),
			FinishedAt:     timestamppb.New(last.FinishedAt),
			DryRun:         last.DryRun,
			RemoveUntagged: last.RemoveUntagged,
			BlobsDeleted:   last.BlobsDeleted,
			BytesFreed:     last.BytesFreed,
			Error:          last.Err,
			RanOn:          last.RanOn,
		}
	}
	return resp, nil
}

// Cached numbers come back at once, stale ones trigger a refresh behind
// the response. A cold instance waits briefly for the walk and then
// answers pending, so the dashboard shows that instead of hanging
func (s *GCService) GetStorageUsage(ctx context.Context, req *v1.GetStorageUsageRequest) (*v1.GetStorageUsageResponse, error) {
	cached, fresh := s.cachedUsage(ctx)
	switch {
	case cached == nil:
		select {
		case <-s.refreshUsage():
		case <-ctx.Done():
		case <-time.After(usageColdWait):
		}
		cached, _ = s.cachedUsage(ctx)
	case !fresh:
		s.refreshUsage()
	}
	running, err := s.refreshState()
	if cached == nil {
		if err != nil && !running {
			return nil, err
		}
		return &v1.GetStorageUsageResponse{Pending: true}, nil
	}
	cached.Pending = running
	return cached, nil
}

func (s *GCService) computeUsage(ctx context.Context) (*v1.GetStorageUsageResponse, error) {
	resp := &v1.GetStorageUsageResponse{ComputedAt: timestamppb.Now()}

	registryBytes, namespaces, err := s.usageWalk(ctx, s.registryPath)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scanning registry storage: %w", err).Error())
	}
	resp.RegistryBytes = registryBytes
	resp.RegistryNamespaces = namespaces

	if resp.ArtifactBytes, err = s.store.ArtifactUniqueBlobBytes(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	repos, _, err := s.store.ListArtifactRepositories(ctx, stores.ArtifactRepoListOptions{IncludePrivate: true})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
	}
	stats, err := s.store.GetArtifactRepoStats(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	for _, r := range repos {
		st := stats[r.ID]
		resp.ArtifactRepos = append(resp.ArtifactRepos, &v1.StorageUsageEntry{
			Name: r.Name, Bytes: st.Size, Count: int32(st.Count),
		})
	}
	resp.ArtifactRepos = largestUsageEntries(resp.ArtifactRepos)
	return resp, nil
}

// Walks once in the background, calls while it runs join that walk.
// The channel closes once the numbers are stored or the walk failed
func (s *GCService) refreshUsage() <-chan struct{} {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshDone != nil {
		return s.refreshDone
	}
	done := make(chan struct{})
	s.refreshDone = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), usageWalkTimeout)
		defer cancel()
		resp, err := s.computeUsage(ctx)
		if err != nil {
			s.log.Warn("Storage usage refresh failed: %v", err)
		} else {
			s.rememberUsage(ctx, resp)
		}
		s.refreshMu.Lock()
		s.refreshDone, s.refreshErr = nil, err
		s.refreshMu.Unlock()
		close(done)
	}()
	return done
}

// Whether a walk is running and how the last one ended
func (s *GCService) refreshState() (bool, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	return s.refreshDone != nil, s.refreshErr
}

// Local copy first, then whatever another instance computed, then a
// stale local copy flagged for refresh
func (s *GCService) cachedUsage(ctx context.Context) (resp *v1.GetStorageUsageResponse, fresh bool) {
	s.usageMu.Lock()
	local, at := s.usage, s.usageAt
	s.usageMu.Unlock()
	if local != nil && time.Since(at) < usageLocalTTL {
		return proto.Clone(local).(*v1.GetStorageUsageResponse), true
	}
	if raw, ok, err := s.shared.GetString(ctx, usageKey); err == nil && ok {
		out := &v1.GetStorageUsageResponse{}
		if protojson.Unmarshal([]byte(raw), out) == nil {
			s.usageMu.Lock()
			s.usage, s.usageAt = proto.Clone(out).(*v1.GetStorageUsageResponse), time.Now()
			s.usageMu.Unlock()
			return out, true
		}
	}
	if local != nil {
		return proto.Clone(local).(*v1.GetStorageUsageResponse), false
	}
	return nil, false
}

func (s *GCService) rememberUsage(ctx context.Context, resp *v1.GetStorageUsageResponse) {
	s.usageMu.Lock()
	s.usage, s.usageAt = proto.Clone(resp).(*v1.GetStorageUsageResponse), time.Now()
	s.usageMu.Unlock()
	if raw, err := protojson.Marshal(resp); err == nil {
		_ = s.shared.SetString(ctx, usageKey, string(raw), usageSharedTTL)
	}
}

// Marks the numbers stale everywhere and starts replacing them
func (s *GCService) dropUsage() {
	s.usageMu.Lock()
	s.usageAt = time.Time{}
	s.usageMu.Unlock()
	_ = s.shared.Delete(context.Background(), usageKey)
	s.refreshUsage()
}

// Walks distribution v3 filesystem layout attributing blob bytes per namespace
func registryUsage(ctx context.Context, root string) (int64, []*v1.StorageUsageEntry, error) {
	base := filepath.Join(root, "docker", "registry", "v2")

	blobSizes, total, err := blobSizes(ctx, filepath.Join(base, "blobs", "sha256"))
	if err != nil {
		return 0, nil, err
	}

	// Namespace usage counts each digest once per namespace
	type nsUsage struct {
		digests map[string]bool
		repos   int32
	}
	perNS := map[string]*nsUsage{}
	repoBase := filepath.Join(base, "repositories")
	nsEntries, err := os.ReadDir(repoBase)
	if err != nil && !os.IsNotExist(err) {
		return 0, nil, err
	}
	for _, nsEntry := range nsEntries {
		if !nsEntry.IsDir() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		ns := nsEntry.Name()
		repoEntries, err := os.ReadDir(filepath.Join(repoBase, ns))
		if err != nil {
			return 0, nil, err
		}
		for _, repoEntry := range repoEntries {
			repoDir := filepath.Join(repoBase, ns, repoEntry.Name())
			if !repoEntry.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(repoDir, "_manifests")); err != nil {
				continue
			}
			u := perNS[ns]
			if u == nil {
				u = &nsUsage{digests: map[string]bool{}}
				perNS[ns] = u
			}
			u.repos++
			for _, linkDir := range []string{
				filepath.Join(repoDir, "_layers", "sha256"),
				filepath.Join(repoDir, "_manifests", "revisions", "sha256"),
			} {
				links, err := os.ReadDir(linkDir)
				if err != nil {
					continue
				}
				for _, l := range links {
					u.digests[l.Name()] = true
				}
			}
		}
	}

	entries := make([]*v1.StorageUsageEntry, 0, len(perNS))
	for ns, u := range perNS {
		var bytes int64
		for digest := range u.digests {
			bytes += blobSizes[digest]
		}
		entries = append(entries, &v1.StorageUsageEntry{Name: ns, Bytes: bytes, Count: u.repos})
	}
	return total, largestUsageEntries(entries), nil
}

// Unique blob bytes on disk keyed by hex digest, shards walk in parallel
func blobSizes(ctx context.Context, blobDir string) (map[string]int64, int64, error) {
	shards, err := os.ReadDir(blobDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int64{}, 0, nil
		}
		return nil, 0, err
	}
	var (
		mu       sync.Mutex
		sizes    = map[string]int64{}
		total    int64
		firstErr error
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, min(runtime.GOMAXPROCS(0), usageWalkers))
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			local := map[string]int64{}
			digests, err := os.ReadDir(filepath.Join(blobDir, name))
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			var sum int64
			for _, d := range digests {
				info, err := os.Stat(filepath.Join(blobDir, name, d.Name(), "data"))
				if err != nil {
					continue
				}
				local[d.Name()] = info.Size()
				sum += info.Size()
			}
			mu.Lock()
			maps.Copy(sizes, local)
			total += sum
			mu.Unlock()
		}(shard.Name())
	}
	wg.Wait()
	if firstErr != nil {
		return nil, 0, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return sizes, total, nil
}

const maxUsageEntries = 5

// Biggest first, truncated for the dashboard card
func largestUsageEntries(entries []*v1.StorageUsageEntry) []*v1.StorageUsageEntry {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Bytes != entries[j].Bytes {
			return entries[i].Bytes > entries[j].Bytes
		}
		return entries[i].Name < entries[j].Name
	})
	if len(entries) > maxUsageEntries {
		entries = entries[:maxUsageEntries]
	}
	return entries
}
