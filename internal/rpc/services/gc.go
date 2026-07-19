package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.GCServiceHandler = (*GCService)(nil)

type GCService struct {
	collector    *admin.Collector
	store        *stores.Store
	registryPath string
	res          *settings.Resolver
	log          *logger.Logger
}

func NewGCService(collector *admin.Collector, store *stores.Store, registryPath string, res *settings.Resolver, log *logger.Logger) *GCService {
	return &GCService{collector: collector, store: store, registryPath: registryPath, res: res, log: log}
}

func (s *GCService) RunGC(ctx context.Context, req *connect.Request[v1.RunGCRequest]) (*connect.Response[v1.RunGCResponse], error) {
	if err := s.collector.Start(req.Msg.DryRun, req.Msg.RemoveUntagged); err != nil {
		if errors.Is(err, admin.ErrAlreadyRunning) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.RunGCResponse{}), nil
}

func (s *GCService) GetGCStatus(ctx context.Context, req *connect.Request[v1.GetGCStatusRequest]) (*connect.Response[v1.GetGCStatusResponse], error) {
	running, last := s.collector.Status()
	gc := s.res.System(ctx).GetGc()

	resp := &v1.GetGCStatusResponse{
		Running:       running,
		Scheduled:     gc.GetEnabled(),
		IntervalHours: gc.GetIntervalHours(),
	}
	if last != nil {
		resp.LastRun = &v1.GCRun{
			StartedAt:      timestamppb.New(last.StartedAt),
			FinishedAt:     timestamppb.New(last.FinishedAt),
			DryRun:         last.DryRun,
			RemoveUntagged: last.RemoveUntagged,
			BlobsDeleted:   last.BlobsDeleted,
			BytesFreed:     last.BytesFreed,
			Error:          last.Err,
		}
	}
	return connect.NewResponse(resp), nil
}

func (s *GCService) GetStorageUsage(ctx context.Context, req *connect.Request[v1.GetStorageUsageRequest]) (*connect.Response[v1.GetStorageUsageResponse], error) {
	resp := &v1.GetStorageUsageResponse{}

	registryBytes, namespaces, err := registryUsage(s.registryPath)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scanning registry storage: %w", err))
	}
	resp.RegistryBytes = registryBytes
	resp.RegistryNamespaces = namespaces

	if resp.ArtifactBytes, err = s.store.ArtifactUniqueBlobBytes(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	repos, _, err := s.store.ListArtifactRepositories(ctx, stores.ArtifactRepoListOptions{IncludePrivate: true})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
	}
	stats, err := s.store.GetArtifactRepoStats(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	for _, r := range repos {
		st := stats[r.ID]
		resp.ArtifactRepos = append(resp.ArtifactRepos, &v1.StorageUsageEntry{
			Name: r.Name, Bytes: st.Size, Count: int32(st.Count),
		})
	}
	resp.ArtifactRepos = largestUsageEntries(resp.ArtifactRepos)

	return connect.NewResponse(resp), nil
}

// Walks distribution v3 filesystem layout attributing blob bytes per namespace
func registryUsage(root string) (int64, []*v1.StorageUsageEntry, error) {
	base := filepath.Join(root, "docker", "registry", "v2")

	// Unique blob bytes on disk keyed by hex digest
	blobSizes := map[string]int64{}
	var total int64
	blobDir := filepath.Join(base, "blobs", "sha256")
	shards, err := os.ReadDir(blobDir)
	if err != nil && !os.IsNotExist(err) {
		return 0, nil, err
	}
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		digests, err := os.ReadDir(filepath.Join(blobDir, shard.Name()))
		if err != nil {
			return 0, nil, err
		}
		for _, d := range digests {
			info, err := os.Stat(filepath.Join(blobDir, shard.Name(), d.Name(), "data"))
			if err != nil {
				continue
			}
			blobSizes[d.Name()] = info.Size()
			total += info.Size()
		}
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
