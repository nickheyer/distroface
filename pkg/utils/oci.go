package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/manifest/manifestlist"
	"github.com/distribution/distribution/v3/manifest/ocischema"
	"github.com/distribution/distribution/v3/manifest/schema2"
	"github.com/distribution/reference"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ResolveDescriptor(ctx context.Context, repo distribution.Repository, dgst digest.Digest, mediaType string) (*v1.Descriptor, error) {
	manifestService, err := repo.Manifests(ctx)
	if err != nil {
		return nil, fmt.Errorf("accessing manifest service: %w", err)
	}

	blobStore := repo.Blobs(ctx)

	if IsManifestMediaType(mediaType) || mediaType == "" {
		manifest, err := manifestService.Get(ctx, dgst)
		if err == nil {
			return ResolveManifest(ctx, repo, dgst, manifest, blobStore)
		}
		if IsManifestMediaType(mediaType) {
			return nil, fmt.Errorf("resolving manifest: %w", err)
		}
	}

	// Resolve as a blob.
	blobDesc, err := blobStore.Stat(ctx, dgst)
	if err != nil {
		return nil, fmt.Errorf("resolving digest: %w", err)
	}

	mt := mediaType
	if mt == "" {
		mt = blobDesc.MediaType
	}

	desc := &v1.Descriptor{
		Digest:    dgst.String(),
		MediaType: mt,
		SizeBytes: blobDesc.Size,
	}
	if len(blobDesc.Annotations) > 0 {
		desc.Annotations = blobDesc.Annotations
	}

	if IsConfigMediaType(mt) {
		if content, err := blobStore.Get(ctx, dgst); err == nil {
			desc.ImageConfig = ParseImageConfig(content)
		}
	}
	return desc, nil
}

// Builds a proto Descriptor from a fetched manifest
func ResolveManifest(ctx context.Context, repo distribution.Repository, dgst digest.Digest, manifest distribution.Manifest, blobStore distribution.BlobStore) (*v1.Descriptor, error) {
	mt, _, _ := manifest.Payload()
	refs := manifest.References()

	desc := &v1.Descriptor{
		Digest:    dgst.String(),
		MediaType: mt,
		SizeBytes: ComputeManifestSize(manifest),
	}
	if ann := ManifestAnnotations(manifest); len(ann) > 0 {
		desc.Annotations = ann
	}

	// Recursively resolve every child reference.
	isIndex := mt == ocispec.MediaTypeImageIndex || mt == manifestlist.MediaTypeManifestList
	for _, ref := range refs {
		// Skip buildkit attestation manifests (unknown/unknown platform) in indexes.
		if isIndex && isUnknownPlatform(ref.Platform) {
			continue
		}

		child, err := ResolveDescriptor(ctx, repo, ref.Digest, ref.MediaType)
		if err != nil {
			// Fall back to shallow descriptor on error.
			child = &v1.Descriptor{
				Digest:    ref.Digest.String(),
				SizeBytes: ref.Size,
				MediaType: ref.MediaType,
			}
			if ref.Platform != nil {
				child.Platform = OciPlatformToProto(ref.Platform)
			}
			if len(ref.Annotations) > 0 {
				child.Annotations = ref.Annotations
			}
		}
		// Carry over platform from the index entry if the child didn't resolve one.
		if child.Platform == nil && ref.Platform != nil {
			child.Platform = OciPlatformToProto(ref.Platform)
		}
		desc.Children = append(desc.Children, child)
	}

	// Extract image config and platform from the config child
	var layers []ocispec.Descriptor
	for i := range refs {
		if !IsConfigMediaType(refs[i].MediaType) && !IsManifestMediaType(refs[i].MediaType) {
			layers = append(layers, refs[i])
		}
	}
	for i := range refs {
		if IsConfigMediaType(refs[i].MediaType) {
			configBlob, err := blobStore.Get(ctx, refs[i].Digest)
			if err == nil {
				var img ocispec.Image
				if json.Unmarshal(configBlob, &img) == nil {
					desc.ImageConfig = ImageToProto(&img, layers)
					if img.Architecture != "" || img.OS != "" {
						desc.Platform = OciPlatformToProto(&img.Platform)
					}
				}
			}
			break
		}
	}

	return desc, nil
}

func IsManifestMediaType(mediaType string) bool {
	switch mediaType {
	case ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex,
		schema2.MediaTypeManifest, manifestlist.MediaTypeManifestList:
		return true
	}
	return false
}

func IsConfigMediaType(mediaType string) bool {
	switch mediaType {
	case ocispec.MediaTypeImageConfig, schema2.MediaTypeImageConfig:
		return true
	}
	return false
}

func ComputeManifestSize(manifest distribution.Manifest) int64 {
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

// isUnknownPlatform returns true for buildkit attestation manifests that have
// "unknown" as both OS and architecture (e.g. SBOM, provenance).
func isUnknownPlatform(p *ocispec.Platform) bool {
	return p != nil && p.OS == "unknown" && p.Architecture == "unknown"
}

func OciPlatformToProto(p *ocispec.Platform) *v1.Platform {
	if p == nil {
		return nil
	}
	return &v1.Platform{
		Architecture: p.Architecture,
		Os:           p.OS,
		OsVersion:    p.OSVersion,
		OsFeatures:   p.OSFeatures,
		Variant:      p.Variant,
	}
}

// Populates platform info on a tag for the list view
func EnrichTagPlatforms(ctx context.Context, t *v1.Tag, manifest distribution.Manifest, blobStore distribution.BlobStore) {
	if len(t.Platforms) > 0 {
		return
	}

	for _, ref := range manifest.References() {
		if ref.Platform != nil && !isUnknownPlatform(ref.Platform) {
			t.Platforms = append(t.Platforms, OciPlatformToProto(ref.Platform))
		} else if IsConfigMediaType(ref.MediaType) {
			if p := platformFromConfigBlob(ctx, blobStore, ref.Digest); p != nil {
				t.Platforms = []*v1.Platform{p}
			}
		}
	}

	if t.PushedAt == nil {
		if ann := ManifestAnnotations(manifest); ann != nil {
			if ts, ok := ann[ocispec.AnnotationCreated]; ok {
				if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
					t.PushedAt = timestamppb.New(parsed)
				}
			}
		}
	}
}

func platformFromConfigBlob(ctx context.Context, blobStore distribution.BlobStore, configDigest digest.Digest) *v1.Platform {
	configBlob, err := blobStore.Get(ctx, configDigest)
	if err != nil {
		return nil
	}
	var img ocispec.Image
	if json.Unmarshal(configBlob, &img) != nil {
		return nil
	}
	if img.Architecture == "" && img.OS == "" {
		return nil
	}
	return OciPlatformToProto(&img.Platform)
}

// Parses raw bytes as an OCI image config
func ParseImageConfig(content []byte) *v1.ImageConfig {
	var img ocispec.Image
	if json.Unmarshal(content, &img) != nil {
		return nil
	}
	return ImageToProto(&img, nil)
}

// Converts an ocispec.Image to a proto ImageConfig
func ImageToProto(img *ocispec.Image, layers []ocispec.Descriptor) *v1.ImageConfig {
	cfg := &v1.ImageConfig{
		Author:       img.Author,
		Architecture: img.Architecture,
		Os:           img.OS,
		WorkingDir:   img.Config.WorkingDir,
		Cmd:          img.Config.Cmd,
		Entrypoint:   img.Config.Entrypoint,
		Env:          img.Config.Env,
	}
	if img.Created != nil {
		cfg.Created = timestamppb.New(*img.Created)
	}
	layerIdx := 0
	for _, h := range img.History {
		entry := &v1.HistoryEntry{
			CreatedBy:  h.CreatedBy,
			EmptyLayer: h.EmptyLayer,
			Comment:    h.Comment,
		}
		if h.Created != nil {
			entry.Created = timestamppb.New(*h.Created)
		}
		if !h.EmptyLayer && layerIdx < len(layers) {
			entry.SizeBytes = layers[layerIdx].Size
			layerIdx++
		}
		cfg.History = append(cfg.History, entry)
	}
	if len(img.Config.Labels) > 0 {
		cfg.Labels = img.Config.Labels
	}
	for port := range img.Config.ExposedPorts {
		cfg.ExposedPorts = append(cfg.ExposedPorts, port)
	}
	for vol := range img.Config.Volumes {
		cfg.Volumes = append(cfg.Volumes, vol)
	}
	sort.Strings(cfg.ExposedPorts)
	sort.Strings(cfg.Volumes)
	return cfg
}

func ManifestAnnotations(manifest distribution.Manifest) map[string]string {
	switch m := manifest.(type) {
	case *ocischema.DeserializedManifest:
		return m.Annotations
	case *ocischema.DeserializedImageIndex:
		return m.Annotations
	default:
		return nil
	}
}

func ExtractRef(repo reference.Named, m distribution.Manifest) (tag string, dgst string) {
	if tagged, ok := repo.(reference.Tagged); ok {
		tag = tagged.Tag()
	}
	if canonical, ok := repo.(reference.Canonical); ok {
		dgst = canonical.Digest().String()
	} else if m != nil {
		_, payload, err := m.Payload()
		if err == nil {
			dgst = digest.FromBytes(payload).String()
		}
	}
	return
}

// Extracts the tag from manifest service options
func TagFromOptions(options []distribution.ManifestServiceOption) string {
	for _, opt := range options {
		if tagOpt, ok := opt.(distribution.WithTagOption); ok {
			return tagOpt.Tag
		}
	}
	return ""
}
