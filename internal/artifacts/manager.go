package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Per-org OrgSetting keys mirroring the global artifacts config
const (
	SettingRetentionEnabled       = "artifacts.retention.enabled"
	SettingRetentionMaxVersions   = "artifacts.retention.max_versions"
	SettingRetentionMaxAgeDays    = "artifacts.retention.max_age_days"
	SettingRetentionMaxTotalSize  = "artifacts.retention.max_total_size"
	SettingRetentionExcludeLatest = "artifacts.retention.exclude_latest"
	SettingMaxFileSizeMB          = "artifacts.max_file_size_mb"
	SettingPrivateByDefault       = "artifacts.private_by_default"
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
	p := RetentionPolicy{
		Enabled:       m.cfg.Retention.Enabled,
		MaxVersions:   m.cfg.Retention.MaxVersions,
		MaxAgeDays:    m.cfg.Retention.MaxAgeDays,
		MaxTotalSize:  m.cfg.Retention.MaxTotalSize,
		ExcludeLatest: m.cfg.Retention.ExcludeLatest,
	}
	kv := m.orgSettings(ctx, namespace)
	if kv == nil {
		return p
	}
	if v, ok := kv[SettingRetentionEnabled]; ok {
		p.Enabled = parseBool(v, p.Enabled)
	}
	if v, ok := kv[SettingRetentionMaxVersions]; ok {
		p.MaxVersions = int(parseInt64(v, int64(p.MaxVersions)))
	}
	if v, ok := kv[SettingRetentionMaxAgeDays]; ok {
		p.MaxAgeDays = int(parseInt64(v, int64(p.MaxAgeDays)))
	}
	if v, ok := kv[SettingRetentionMaxTotalSize]; ok {
		p.MaxTotalSize = parseInt64(v, p.MaxTotalSize)
	}
	if v, ok := kv[SettingRetentionExcludeLatest]; ok {
		p.ExcludeLatest = parseBool(v, p.ExcludeLatest)
	}
	return p
}

// Effective max upload size in bytes zero means unlimited
func (m *Manager) EffectiveMaxFileSizeBytes(ctx context.Context, namespace string) int64 {
	mb := m.cfg.MaxFileSizeMB
	if kv := m.orgSettings(ctx, namespace); kv != nil {
		if v, ok := kv[SettingMaxFileSizeMB]; ok {
			mb = parseInt64(v, mb)
		}
	}
	if mb <= 0 {
		return 0
	}
	return mb * 1024 * 1024
}

// Effective private-by-default for new repos in a namespace
func (m *Manager) EffectivePrivateByDefault(ctx context.Context, namespace string) bool {
	if kv := m.orgSettings(ctx, namespace); kv != nil {
		if v, ok := kv[SettingPrivateByDefault]; ok {
			return parseBool(v, false)
		}
	}
	return false
}

// Loads org overrides for a namespace, nil when not an org
func (m *Manager) orgSettings(ctx context.Context, namespace string) map[string]string {
	if namespace == "" {
		return nil
	}
	org, err := m.store.GetOrganization(ctx, namespace)
	if err != nil || org == nil {
		return nil
	}
	kv, err := m.store.ListOrgSettings(ctx, org.ID)
	if err != nil {
		return nil
	}
	return kv
}

func parseBool(v string, def bool) bool {
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return def
}

func parseInt64(v string, def int64) int64 {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	return def
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
