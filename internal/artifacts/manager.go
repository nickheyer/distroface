package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/utils"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// Size cap breach, matches ErrInvalid too
var ErrTooLarge = errors.New("artifact exceeds the size limit")

type tooLargeError struct{ maxBytes int64 }

func (e *tooLargeError) Error() string {
	return fmt.Sprintf("%s: artifact exceeds maximum size of %dMB", ErrInvalid, e.maxBytes/(1024*1024))
}

func (e *tooLargeError) Is(target error) bool { return target == ErrInvalid || target == ErrTooLarge }

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

// Fields of one artifact being stored
type Upload struct {
	Version    string
	Path       string
	Metadata   string
	Label      string
	MimeType   string
	Properties map[string]string
}

// Checks shared by every upload route
func (in *Upload) normalize(repo *storage.ArtifactRepository) error {
	if err := ValidateVersion(in.Version); err != nil {
		return err
	}
	if in.Path == "" {
		in.Path = SanitizePath(repo.Name)
	}
	if err := ValidatePath(in.Path); err != nil {
		return err
	}
	if in.Metadata == "" {
		in.Metadata = "{}"
	} else if !json.Valid([]byte(in.Metadata)) {
		return fmt.Errorf("%w: metadata must be valid JSON", ErrInvalid)
	}
	label, err := NormalizeLabel(in.Label)
	if err != nil {
		return err
	}
	in.Label = label
	return nil
}

// Trims a display label and rejects oversized or multiline ones
func NormalizeLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if len(label) > 255 {
		return "", fmt.Errorf("%w: label exceeds 255 characters", ErrInvalid)
	}
	if strings.ContainsAny(label, "\r\n\x00") {
		return "", fmt.Errorf("%w: label must be a single line", ErrInvalid)
	}
	return label, nil
}

func tooLarge(maxBytes int64) error { return &tooLargeError{maxBytes: maxBytes} }

// Finalizes upload, replaces existing same version path properties
func (m *Manager) CompleteUpload(ctx context.Context, repo *storage.ArtifactRepository, uploadID string, in Upload) (*storage.Artifact, error) {
	if err := in.normalize(repo); err != nil {
		return nil, err
	}

	if maxBytes := m.EffectiveMaxFileSizeBytes(ctx, repo.Namespace); maxBytes > 0 {
		size, err := m.blobs.UploadSize(uploadID)
		if err != nil {
			return nil, ErrUploadNotFound
		}
		if size > maxBytes {
			_ = m.blobs.CancelUpload(uploadID)
			return nil, tooLarge(maxBytes)
		}
	}

	digest, size, mimeType, err := m.blobs.CompleteUpload(uploadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUploadNotFound
		}
		return nil, err
	}
	return m.recordArtifact(ctx, repo, uploadID, in, digest, size, mimeType)
}

// Stores a stream as an artifact in one pass
func (m *Manager) StoreStream(ctx context.Context, repo *storage.ArtifactRepository, body io.Reader, in Upload) (*storage.Artifact, error) {
	if err := in.normalize(repo); err != nil {
		return nil, err
	}
	maxBytes := m.EffectiveMaxFileSizeBytes(ctx, repo.Namespace)
	digest, size, mimeType, err := m.blobs.Ingest(body, maxBytes)
	if errors.Is(err, ErrBlobTooLarge) {
		return nil, tooLarge(maxBytes)
	}
	if err != nil {
		return nil, err
	}
	return m.recordArtifact(ctx, repo, uuid.New().String(), in, digest, size, mimeType)
}

func (m *Manager) recordArtifact(ctx context.Context, repo *storage.ArtifactRepository, uploadID string, in Upload, digest string, size int64, sniffed string) (*storage.Artifact, error) {
	mimeType := in.MimeType
	if mimeType == "" {
		mimeType = sniffed
	}
	artifact := &storage.Artifact{
		RepoID:   repo.ID,
		Name:     path.Base(in.Path),
		Path:     in.Path,
		UploadID: uploadID,
		Version:  in.Version,
		Digest:   digest,
		Size:     size,
		MimeType: mimeType,
		Metadata: in.Metadata,
		Label:    in.Label,
	}

	replacedDigest, err := m.store.CreateArtifact(ctx, artifact, in.Properties)
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

// Streams a hardened attachment and counts the download
func (m *Manager) ServeArtifact(w http.ResponseWriter, r *http.Request, artifact *storage.Artifact) {
	f, info, err := m.blobs.OpenBlob(artifact.Digest)
	if err != nil {
		m.log.Error("blob missing for artifact %s (%s)", artifact.ID, artifact.Digest)
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	ct := artifact.MimeType
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(artifact.Name))
	}
	if ct == "" {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		ct = http.DetectContentType(head[:n])
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
			return
		}
	}
	utils.HardenUserContent(w.Header(), ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", artifact.Name))
	etag := `"` + strings.TrimPrefix(artifact.Digest, "sha256:") + `"`
	w.Header().Set("ETag", etag)
	// Ranges and cache revalidations are not new downloads
	if r.Method == http.MethodGet && r.Header.Get("Range") == "" && !revalidates(r, etag, info.ModTime()) {
		if err := m.store.IncrementArtifactDownloads(r.Context(), artifact.ID); err != nil {
			m.log.Error("download count for artifact %s: %v", artifact.ID, err)
		}
	}
	http.ServeContent(w, r, artifact.Name, info.ModTime(), f)
}

// True when the conditional headers will answer 304
func revalidates(r *http.Request, etag string, modTime time.Time) bool {
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		for _, tag := range strings.Split(inm, ",") {
			tag = strings.TrimPrefix(strings.TrimSpace(tag), "W/")
			if tag == "*" || tag == etag {
				return true
			}
		}
		return false
	}
	since, err := http.ParseTime(r.Header.Get("If-Modified-Since"))
	return err == nil && !modTime.Truncate(time.Second).After(since)
}

// Wire shape shared by the artifact and release services
func ToProto(a *storage.Artifact) *v1.Artifact {
	return &v1.Artifact{
		Id:            a.ID,
		RepoId:        a.RepoID,
		Name:          a.Name,
		Path:          a.Path,
		UploadId:      a.UploadID,
		Version:       a.Version,
		Size:          a.Size,
		MimeType:      a.MimeType,
		Metadata:      a.Metadata,
		Properties:    a.Properties,
		Digest:        a.Digest,
		Label:         a.Label,
		DownloadCount: a.DownloadCount,
		CreatedAt:     timestamppb.New(a.CreatedAt),
		UpdatedAt:     timestamppb.New(a.UpdatedAt),
	}
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
			if !cutoff.IsZero() && artifact.CreatedAt.Before(cutoff) && (!p.ExcludeLatest || i != 0) {
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
	referenced, err := m.store.BlobReferenced(ctx, digest)
	if err != nil {
		m.log.Error("blob refcount for %s: %v", digest, err)
		return
	}
	if referenced {
		return
	}
	if err := m.blobs.DeleteBlob(digest); err != nil {
		m.log.Error("blob delete for %s: %v", digest, err)
	}
}
