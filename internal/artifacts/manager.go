package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Caller errors that map to 400 or InvalidArgument
var ErrInvalid = errors.New("invalid argument")

var ErrUploadNotFound = errors.New("upload session not found")

// Artifact business logic shared by rpc service and v1 facade
type Manager struct {
	store *storage.Store
	blobs *BlobStore
	cfg   config.ArtifactsConfig
	log   *logger.Logger
}

func NewManager(store *storage.Store, blobs *BlobStore, cfg config.ArtifactsConfig, log *logger.Logger) *Manager {
	return &Manager{store: store, blobs: blobs, cfg: cfg, log: log}
}

func (m *Manager) Blobs() *BlobStore { return m.blobs }

func (m *Manager) MaxFileSizeBytes() int64 {
	if m.cfg.MaxFileSizeMB <= 0 {
		return 0
	}
	return m.cfg.MaxFileSizeMB * 1024 * 1024
}

// Rejects traversal, absolute, and oversized paths
func ValidatePath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: path is required", ErrInvalid)
	}
	if len(p) > 255 {
		return fmt.Errorf("%w: path exceeds 255 characters", ErrInvalid)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: absolute paths not allowed", ErrInvalid)
	}
	cleaned := path.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(p, "\\") {
		return fmt.Errorf("%w: invalid path", ErrInvalid)
	}
	return nil
}

// Rejects empty, oversized, or path unsafe versions
func ValidateVersion(v string) error {
	if v == "" {
		return fmt.Errorf("%w: version is required", ErrInvalid)
	}
	if len(v) > 128 {
		return fmt.Errorf("%w: version exceeds 128 characters", ErrInvalid)
	}
	if strings.ContainsAny(v, "/\\") {
		return fmt.Errorf("%w: version must not contain slashes", ErrInvalid)
	}
	return nil
}

// Safe default artifact path from a free form name
func SanitizePath(name string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "artifact"
	}
	return s
}

// Finalizes upload, replaces existing same version path properties
func (m *Manager) CompleteUpload(ctx context.Context, repo *storage.ArtifactRepository, uploadID, version, artifactPath, metadata string, properties map[string]string) (*storage.Artifact, error) {
	if err := ValidateVersion(version); err != nil {
		return nil, err
	}
	if artifactPath == "" {
		artifactPath = SanitizePath(repo.Name)
	}
	if err := ValidatePath(artifactPath); err != nil {
		return nil, err
	}
	if metadata == "" {
		metadata = "{}"
	} else if !json.Valid([]byte(metadata)) {
		return nil, fmt.Errorf("%w: metadata must be valid JSON", ErrInvalid)
	}

	if maxBytes := m.MaxFileSizeBytes(); maxBytes > 0 {
		size, err := m.blobs.UploadSize(uploadID)
		if err != nil {
			return nil, ErrUploadNotFound
		}
		if size > maxBytes {
			m.blobs.CancelUpload(uploadID)
			return nil, fmt.Errorf("%w: artifact exceeds maximum size of %dMB", ErrInvalid, m.cfg.MaxFileSizeMB)
		}
	}

	digest, size, mimeType, err := m.blobs.CompleteUpload(uploadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUploadNotFound
		}
		return nil, err
	}

	artifact := &storage.Artifact{
		RepoID:   repo.ID,
		Name:     path.Base(artifactPath),
		Path:     artifactPath,
		UploadID: uploadID,
		Version:  version,
		Digest:   digest,
		Size:     size,
		MimeType: mimeType,
		Metadata: metadata,
	}

	replacedDigest, err := m.store.CreateArtifact(ctx, artifact, properties)
	if err != nil {
		m.gcBlob(ctx, digest)
		return nil, err
	}
	if replacedDigest != "" && replacedDigest != digest {
		m.gcBlob(ctx, replacedDigest)
	}

	if err := m.ApplyRetention(ctx, repo.ID); err != nil {
		m.log.Error("artifact retention for repo %d: %v", repo.ID, err)
	}

	return artifact, nil
}

// Deletes row then GCs blob when unreferenced
func (m *Manager) DeleteArtifact(ctx context.Context, artifact *storage.Artifact) error {
	if err := m.store.DeleteArtifact(ctx, artifact.ID); err != nil {
		return err
	}
	m.gcBlob(ctx, artifact.Digest)
	return nil
}

// Cascades repo delete then GCs unreferenced blobs
func (m *Manager) DeleteRepository(ctx context.Context, repo *storage.ArtifactRepository) error {
	digests, err := m.store.DeleteArtifactRepository(ctx, repo.ID)
	if err != nil {
		return err
	}
	for _, d := range digests {
		m.gcBlob(ctx, d)
	}
	return nil
}

// Prunes synchronously per path plus property set group
func (m *Manager) ApplyRetention(ctx context.Context, repoID int64) error {
	r := m.cfg.Retention
	if !r.Enabled || (r.MaxVersions <= 0 && r.MaxAgeDays <= 0) {
		return nil
	}

	all, _, err := m.store.ListArtifacts(ctx, repoID, "", 0, 0)
	if err != nil {
		return err
	}

	byGroup := make(map[string][]*storage.Artifact)
	for _, a := range all {
		key := a.Path + "\x00" + a.PropsHash
		byGroup[key] = append(byGroup[key], a)
	}

	var cutoff time.Time
	if r.MaxAgeDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -r.MaxAgeDays)
	}

	for _, group := range byGroup {
		sort.Slice(group, func(i, j int) bool {
			return group[i].CreatedAt.After(group[j].CreatedAt)
		})
		for i, artifact := range group {
			prune := false
			if r.MaxVersions > 0 && i >= r.MaxVersions {
				prune = true
			}
			if !cutoff.IsZero() && artifact.CreatedAt.Before(cutoff) && !(r.ExcludeLatest && i == 0) {
				prune = true
			}
			if !prune {
				continue
			}
			if err := m.DeleteArtifact(ctx, artifact); err != nil {
				return err
			}
			m.log.Info("retention pruned artifact %s (%s@%s) from repo %d", artifact.ID, artifact.Path, artifact.Version, repoID)
		}
	}
	return nil
}

// Deletes blob once digest has no references
func (m *Manager) gcBlob(ctx context.Context, digest string) {
	if digest == "" {
		return
	}
	count, err := m.store.CountArtifactsByDigest(ctx, digest)
	if err != nil {
		m.log.Error("blob refcount for %s: %v", digest, err)
		return
	}
	if count > 0 {
		return
	}
	if err := m.blobs.DeleteBlob(digest); err != nil {
		m.log.Error("blob delete for %s: %v", digest, err)
	}
}
