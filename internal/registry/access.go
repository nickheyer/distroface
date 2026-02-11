package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/manifest/manifestlist"
	"github.com/distribution/distribution/v3/manifest/ocischema"
	"github.com/distribution/distribution/v3/manifest/schema2"
	regstorage "github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	"github.com/distribution/reference"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RegistryAccess provides read access to the registry's storage layer.
type RegistryAccess struct {
	registry distribution.Namespace
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

	return &RegistryAccess{registry: reg}, nil
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

	result := make([]*v1.Tag, 0, len(tags))
	for _, tag := range tags {
		desc, err := tagService.Get(ctx, tag)
		if err != nil {
			continue
		}

		var totalSize int64
		manifest, err := manifestService.Get(ctx, desc.Digest)
		if err == nil {
			totalSize = computeManifestSize(manifest)
		}

		result = append(result, &v1.Tag{
			Name:      tag,
			Digest:    desc.Digest.String(),
			SizeBytes: totalSize,
		})
	}

	return result, nil
}

// GetTagDetail returns detailed manifest information for a specific tag as a proto TagDetail.
func (r *RegistryAccess) GetTagDetail(ctx context.Context, namespace, name, tag string) (*v1.TagDetail, error) {
	repoRef, err := reference.WithName(namespace + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("invalid repository name: %w", err)
	}

	repo, err := r.registry.Repository(ctx, repoRef)
	if err != nil {
		return nil, fmt.Errorf("accessing repository: %w", err)
	}

	tagService := repo.Tags(ctx)
	desc, err := tagService.Get(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("tag not found: %w", err)
	}

	manifestService, err := repo.Manifests(ctx)
	if err != nil {
		return nil, fmt.Errorf("accessing manifest service: %w", err)
	}

	manifest, err := manifestService.Get(ctx, desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	mediaType, _, _ := manifest.Payload()
	blobStore := repo.Blobs(ctx)

	detail := &v1.TagDetail{
		Name:      tag,
		Digest:    desc.Digest.String(),
		MediaType: mediaType,
		SizeBytes: computeManifestSize(manifest),
	}

	switch m := manifest.(type) {
	case *schema2.DeserializedManifest:
		detail.Layers = descriptorsToLayers(m.Layers)
		if configBlob, err := blobStore.Get(ctx, m.Config.Digest); err == nil {
			var img ocispec.Image
			if json.Unmarshal(configBlob, &img) == nil {
				detail.Architecture = img.Architecture
				detail.Os = img.OS
				if img.Created != nil {
					detail.CreatedAt = timestamppb.New(*img.Created)
				}
			}
		}

	case *ocischema.DeserializedManifest:
		detail.Layers = descriptorsToLayers(m.Layers)
		if configBlob, err := blobStore.Get(ctx, m.Config.Digest); err == nil {
			var img ocispec.Image
			if json.Unmarshal(configBlob, &img) == nil {
				detail.Architecture = img.Architecture
				detail.Os = img.OS
				if img.Created != nil {
					detail.CreatedAt = timestamppb.New(*img.Created)
				}
			}
		}

	case *manifestlist.DeserializedManifestList:
		if len(m.Manifests) > 0 {
			detail.Architecture = m.Manifests[0].Platform.Architecture
			detail.Os = m.Manifests[0].Platform.OS
		}
		detail.Layers = descriptorsToLayers(manifest.References())

	case *ocischema.DeserializedImageIndex:
		if len(m.Manifests) > 0 && m.Manifests[0].Platform != nil {
			detail.Architecture = m.Manifests[0].Platform.Architecture
			detail.Os = m.Manifests[0].Platform.OS
		}
		detail.Layers = descriptorsToLayers(manifest.References())
	}

	return detail, nil
}

func computeManifestSize(manifest distribution.Manifest) int64 {
	var total int64
	for _, ref := range manifest.References() {
		total += ref.Size
	}
	_, payload, err := manifest.Payload()
	if err == nil {
		total += int64(len(payload))
	}
	return total
}

func descriptorsToLayers(descs []ocispec.Descriptor) []*v1.Layer {
	layers := make([]*v1.Layer, len(descs))
	for i, d := range descs {
		layers[i] = &v1.Layer{
			Digest:    d.Digest.String(),
			SizeBytes: d.Size,
			MediaType: d.MediaType,
		}
	}
	return layers
}
