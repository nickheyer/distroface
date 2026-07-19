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
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Resolved retention rules for a single repo
type RetentionPolicy struct {
	Enabled       bool
	MaxVersions   int
	MaxAgeDays    int
	MaxTotalSize  int64
	ExcludeLatest bool
}

// Caller errors that map to 400 or InvalidArgument
var ErrInvalid = errors.New("invalid argument")

var ErrUploadNotFound = errors.New("upload session not found")

// Artifact business logic shared by rpc service and v1 facade
type Manager struct {
	store *stores.Store
	blobs *BlobStore
	res   *settings.Resolver
	log   *logger.Logger
}

func NewManager(store *stores.Store, blobs *BlobStore, res *settings.Resolver, log *logger.Logger) *Manager {
	return &Manager{store: store, blobs: blobs, res: res, log: log}
}

// Effective artifact settings for an org namespace or the system
func (m *Manager) artifactSettings(ctx context.Context, namespace string) *v1.ArtifactSettings {
	if namespace != "" {
		if org, err := m.store.GetOrganization(ctx, namespace); err == nil && org != nil {
			return m.res.Org(ctx, org.ID).GetArtifacts()
		}
	}
	return m.res.System(ctx).GetArtifacts()
}

func (m *Manager) Blobs() *BlobStore { return m.blobs }

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

	if maxBytes := m.EffectiveMaxFileSizeBytes(ctx, repo.Namespace); maxBytes > 0 {
		size, err := m.blobs.UploadSize(uploadID)
		if err != nil {
			return nil, ErrUploadNotFound
		}
		if size > maxBytes {
			m.blobs.CancelUpload(uploadID)
			return nil, fmt.Errorf("%w: artifact exceeds maximum size of %dMB", ErrInvalid, maxBytes/(1024*1024))
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

	if err := m.ApplyRetention(ctx, repo); err != nil {
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

// Resolves the effective retention policy for a namespace
func (m *Manager) EffectiveRetention(ctx context.Context, namespace string) RetentionPolicy {
	r := m.artifactSettings(ctx, namespace).GetRetention()
	return RetentionPolicy{
		Enabled:       r.GetEnabled(),
		MaxVersions:   int(r.GetMaxVersions()),
		MaxAgeDays:    int(r.GetMaxAgeDays()),
		MaxTotalSize:  r.GetMaxTotalSizeBytes(),
		ExcludeLatest: r.GetExcludeLatest(),
	}
}

// Effective max upload size in bytes zero means unlimited
func (m *Manager) EffectiveMaxFileSizeBytes(ctx context.Context, namespace string) int64 {
	mb := m.artifactSettings(ctx, namespace).GetMaxFileSizeMb()
	if mb <= 0 {
		return 0
	}
	return mb * 1024 * 1024
}

// Effective private-by-default for new repos in a namespace
func (m *Manager) EffectivePrivateByDefault(ctx context.Context, namespace string) bool {
	return m.artifactSettings(ctx, namespace).GetPrivateByDefault()
}

// Abandoned upload age before sweep zero disables
func (m *Manager) StaleUploadAge(ctx context.Context) time.Duration {
	hours := m.res.System(ctx).GetArtifacts().GetStaleUploadCleanupHours()
	return time.Duration(hours) * time.Hour
}

// Resolves the effective policy then prunes the repo
func (m *Manager) ApplyRetention(ctx context.Context, repo *storage.ArtifactRepository) error {
	return m.ApplyRetentionPolicy(ctx, repo.ID, m.EffectiveRetention(ctx, repo.Namespace))
}

// Prunes per path plus property set group then caps total size
func (m *Manager) ApplyRetentionPolicy(ctx context.Context, repoID int64, p RetentionPolicy) error {
	if !p.Enabled || (p.MaxVersions <= 0 && p.MaxAgeDays <= 0 && p.MaxTotalSize <= 0) {
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
	if p.MaxAgeDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -p.MaxAgeDays)
	}

	// Phase 1 prunes by version count and age, tracks survivors
	type survivor struct {
		a         *storage.Artifact
		protected bool
	}
	var survivors []survivor
	for _, group := range byGroup {
		sort.Slice(group, func(i, j int) bool {
			return group[i].CreatedAt.After(group[j].CreatedAt)
		})
		for i, artifact := range group {
			prune := false
			if p.MaxVersions > 0 && i >= p.MaxVersions {
				prune = true
			}
			if !cutoff.IsZero() && artifact.CreatedAt.Before(cutoff) && !(p.ExcludeLatest && i == 0) {
				prune = true
			}
			if !prune {
				survivors = append(survivors, survivor{a: artifact, protected: p.ExcludeLatest && i == 0})
				continue
			}
			if err := m.DeleteArtifact(ctx, artifact); err != nil {
				return err
			}
			m.log.Info("retention pruned artifact %s (%s@%s) from repo %d", artifact.ID, artifact.Path, artifact.Version, repoID)
		}
	}

	// Phase 2 caps total size, deletes oldest unprotected first
	if p.MaxTotalSize > 0 {
		var total int64
		for _, s := range survivors {
			total += s.a.Size
		}
		if total > p.MaxTotalSize {
			sort.Slice(survivors, func(i, j int) bool {
				return survivors[i].a.CreatedAt.Before(survivors[j].a.CreatedAt)
			})
			for _, s := range survivors {
				if total <= p.MaxTotalSize {
					break
				}
				if s.protected {
					continue
				}
				if err := m.DeleteArtifact(ctx, s.a); err != nil {
					return err
				}
				total -= s.a.Size
				m.log.Info("retention size-capped artifact %s (%s@%s) from repo %d", s.a.ID, s.a.Path, s.a.Version, repoID)
			}
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
