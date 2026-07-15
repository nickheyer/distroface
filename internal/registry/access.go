package registry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/distribution/distribution/v3"
	regstorage "github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	"github.com/distribution/reference"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/utils"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RegistryAccess provides read access to the registry's storage layer.
type RegistryAccess struct {
	registry    distribution.Namespace
	storagePath string
}

// NewRegistryAccess creates a RegistryAccess backed by the filesystem storage driver
// at the given path. This path must match the registry's configured storage path.
func NewRegistryAccess(storagePath string) (*RegistryAccess, error) {
	driver := filesystem.New(filesystem.DriverParameters{
		RootDirectory: storagePath,
		MaxThreads:    100,
	})

	reg, err := regstorage.NewRegistry(context.Background(), driver)
	if err != nil {
		return nil, fmt.Errorf("creating registry namespace: %w", err)
	}

	return &RegistryAccess{registry: reg, storagePath: storagePath}, nil
}

// DeleteNamespace removes all registry storage for a given namespace.
func (r *RegistryAccess) DeleteNamespace(namespace string) error {
	repoPath := filepath.Join(r.storagePath, "docker", "registry", "v2", "repositories", namespace)
	return os.RemoveAll(repoPath)
}

// ListTags returns all tags for a repository as proto Tag messages.
// Returns nil with no error if the repository has no tags or doesn't exist in storage.
func (r *RegistryAccess) ListTags(ctx context.Context, namespace, name string) ([]*v1.Tag, error) {
	repoRef, err := reference.WithName(namespace + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("invalid repository name: %w", err)
	}

	repo, err := r.registry.Repository(ctx, repoRef)
	if err != nil {
		return nil, fmt.Errorf("accessing repository: %w", err)
	}

	tagService := repo.Tags(ctx)
	tags, err := tagService.All(ctx)
	if err != nil {
		return nil, nil
	}

	sort.Strings(tags)

	manifestService, err := repo.Manifests(ctx)
	if err != nil {
		return nil, fmt.Errorf("accessing manifest service: %w", err)
	}

	blobStore := repo.Blobs(ctx)

	result := make([]*v1.Tag, 0, len(tags))
	for _, tag := range tags {
		desc, err := tagService.Get(ctx, tag)
		if err != nil {
			continue
		}

		t := &v1.Tag{
			Name:         tag,
			Digest:       desc.Digest.String(),
			MediaType:    desc.MediaType,
			ArtifactType: desc.ArtifactType,
		}

		if len(desc.Annotations) > 0 {
			t.Annotations = desc.Annotations
		}
		if desc.Platform != nil {
			t.Platforms = []*v1.Platform{utils.OciPlatformToProto(desc.Platform)}
		}
		if ts, ok := desc.Annotations[ocispec.AnnotationCreated]; ok {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				t.PushedAt = timestamppb.New(parsed)
			}
		}

		manifest, err := manifestService.Get(ctx, desc.Digest)
		if err == nil {
			t.SizeBytes = utils.ComputeManifestSize(manifest)

			if t.MediaType == "" {
				mt, _, _ := manifest.Payload()
				t.MediaType = mt
			}

			utils.EnrichTagPlatforms(ctx, t, manifest, blobStore)
		}

		result = append(result, t)
	}

	return result, nil
}

// ResolveTag resolves a tag to its manifest descriptor with children populated.
func (r *RegistryAccess) ResolveTag(ctx context.Context, namespace, name, tag string) (*v1.Descriptor, error) {
	repoRef, err := reference.WithName(namespace + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("invalid repository name: %w", err)
	}

	repo, err := r.registry.Repository(ctx, repoRef)
	if err != nil {
		return nil, fmt.Errorf("accessing repository: %w", err)
	}

	desc, err := repo.Tags(ctx).Get(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("tag not found: %w", err)
	}

	return utils.ResolveDescriptor(ctx, repo, desc.Digest, desc.MediaType)
}
