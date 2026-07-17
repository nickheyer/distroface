package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/structpb"
)

var _ distrofacev1connect.ConfigurationServiceHandler = (*ConfigurationService)(nil)

// Only these config keys leave the public rpc
var publicKeys = []string{
	"server.hostname",
}

type ConfigurationService struct {
	store  *stores.Store
	config *config.Config
	log    *logger.Logger
}

func NewConfigurationService(store *stores.Store, cfg *config.Config, log *logger.Logger) *ConfigurationService {
	return &ConfigurationService{store: store, config: cfg, log: log}
}

func (s *ConfigurationService) GetConfiguration(ctx context.Context, req *connect.Request[v1.GetConfigurationRequest]) (*connect.Response[v1.GetConfigurationResponse], error) {
	flat, err := flattenConfig(s.config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshalling config: %w", err))
	}

	var entries []*v1.ConfigEntry
	for key, val := range flat {
		if !slices.Contains(publicKeys, key) {
			continue
		}
		pbVal, err := structpb.NewValue(val)
		if err != nil {
			s.log.Warn("skipping config key %s: %v", key, err)
			continue
		}
		entries = append(entries, &v1.ConfigEntry{Key: key, Value: pbVal})
	}

	return connect.NewResponse(&v1.GetConfigurationResponse{Entries: entries}), nil
}

func (s *ConfigurationService) GetStorageUsage(ctx context.Context, req *connect.Request[v1.GetStorageUsageRequest]) (*connect.Response[v1.GetStorageUsageResponse], error) {
	resp := &v1.GetStorageUsageResponse{}

	registryBytes, namespaces, err := registryUsage(s.config.Registry.StoragePath)
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
	sortUsageEntries(resp.ArtifactRepos)

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
	sortUsageEntries(entries)
	return total, entries, nil
}

func sortUsageEntries(entries []*v1.StorageUsageEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Bytes != entries[j].Bytes {
			return entries[i].Bytes > entries[j].Bytes
		}
		return entries[i].Name < entries[j].Name
	})
}

// Marshals the config struct
func flattenConfig(cfg *config.Config) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]any)
	flatten("", raw, out)
	return out, nil
}

func flatten(prefix string, src map[string]any, dst map[string]any) {
	for k, v := range src {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			flatten(key, nested, dst)
		} else {
			dst[key] = v
		}
	}
}
