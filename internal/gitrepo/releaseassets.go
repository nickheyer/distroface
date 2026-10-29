package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/utils"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrAssetTooLarge is returned when an upload passes the release asset cap
var ErrAssetTooLarge = errors.New("release asset exceeds the size limit")

// ValidateAssetName applies GitHub's rules for release asset file names
func ValidateAssetName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("%w: asset name is required", ErrInvalid)
	}
	if path.Base(name) != name || strings.ContainsAny(name, "\\/:*?\"<>|\x00\r\n") {
		return fmt.Errorf("%w: asset name %q may not contain path separators or control characters", ErrInvalid, name)
	}
	if len(name) > 255 {
		return fmt.Errorf("%w: asset name is longer than 255 bytes", ErrInvalid)
	}
	return nil
}

// Per file cap on release assets for a repository, zero means none
func (m *Manager) MaxReleaseAssetBytes(ctx context.Context, repo *db.GitRepository) int64 {
	mb := m.GitSettings(ctx, repo).GetMaxReleaseAssetMb()
	if mb <= 0 {
		return 0
	}
	return mb * 1024 * 1024
}

// ReleaseAssetInput describes one file streamed onto a release
type ReleaseAssetInput struct {
	Name        string
	Label       string
	ContentType string
	UploaderID  string
	Body        io.Reader
	// Replace an existing asset of the same name instead of refusing
	Replace bool
}

// Streams a file into the blob store, records the row
func (m *Manager) StoreReleaseAsset(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, in ReleaseAssetInput) (*db.GitReleaseAsset, error) {
	if err := ValidateAssetName(in.Name); err != nil {
		return nil, err
	}
	existing, err := m.store.GetGitReleaseAssetByName(ctx, rel.ID, in.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil && !in.Replace {
		return nil, fmt.Errorf("%w: an asset named %s already exists on release %s", ErrInvalid, in.Name, rel.Tag)
	}
	blobs := m.blobs
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		return nil, err
	}
	body := in.Body
	maxBytes := m.MaxReleaseAssetBytes(ctx, repo)
	if maxBytes > 0 {
		body = io.LimitReader(in.Body, maxBytes+1)
	}
	n, err := blobs.AppendChunk(uploadID, body)
	if err != nil {
		_ = blobs.CancelUpload(uploadID)
		return nil, err
	}
	if maxBytes > 0 && n > maxBytes {
		_ = blobs.CancelUpload(uploadID)
		return nil, fmt.Errorf("%w: the limit is %d MB", ErrAssetTooLarge, maxBytes/(1024*1024))
	}
	digest, size, sniffed, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		return nil, err
	}
	ct := strings.TrimSpace(in.ContentType)
	if ct == "" || ct == "application/octet-stream" {
		ct = assetContentType(in.Name, sniffed)
	}
	asset := &db.GitReleaseAsset{
		ID: uuid.New().String(), ReleaseID: rel.ID, Name: in.Name, Label: strings.TrimSpace(in.Label),
		Size: size, Digest: digest, ContentType: ct, UploaderID: in.UploaderID,
	}
	if existing != nil {
		asset.ID, asset.CreatedAt, asset.DownloadCount = existing.ID, existing.CreatedAt, existing.DownloadCount
		if asset.Label == "" {
			asset.Label = existing.Label
		}
		if err := m.store.UpdateGitReleaseAsset(ctx, asset); err != nil {
			m.ReapBlob(ctx, digest)
			return nil, err
		}
		if existing.Digest != digest {
			m.ReapBlob(ctx, existing.Digest)
		}
		return asset, nil
	}
	if err := m.store.CreateGitReleaseAsset(ctx, asset); err != nil {
		m.ReapBlob(ctx, digest)
		return nil, err
	}
	return asset, nil
}

// Extension wins over a generic sniff, the sniff wins over nothing
func assetContentType(name, sniffed string) string {
	if ct := mime.TypeByExtension(strings.ToLower(path.Ext(name))); ct != "" {
		return ct
	}
	if sniffed != "" {
		return sniffed
	}
	return "application/octet-stream"
}

// OpenReleaseAsset opens the blob behind an asset
func (m *Manager) OpenReleaseAsset(a *db.GitReleaseAsset) (*os.File, os.FileInfo, error) {
	return m.blobs.OpenBlob(a.Digest)
}

// ServeReleaseAsset streams an asset with hardened headers and counts the download
func (m *Manager) ServeReleaseAsset(w http.ResponseWriter, r *http.Request, a *db.GitReleaseAsset) {
	f, info, err := m.OpenReleaseAsset(a)
	if err != nil {
		http.Error(w, "asset data is missing", http.StatusNotFound)
		return
	}
	defer f.Close()
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	utils.HardenUserContent(w.Header(), ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.Name))
	w.Header().Set("ETag", `"`+strings.TrimPrefix(a.Digest, "sha256:")+`"`)
	w.Header().Set("Cache-Control", "private, max-age=0")
	if r.Method == http.MethodGet {
		_ = m.store.IncrementGitReleaseAssetDownloads(r.Context(), a.ID)
	}
	http.ServeContent(w, r, a.Name, info.ModTime(), f)
}

// DeleteReleaseAsset removes the row and the blob when nothing else holds it
func (m *Manager) DeleteReleaseAsset(ctx context.Context, a *db.GitReleaseAsset) error {
	if err := m.store.DeleteGitReleaseAsset(ctx, a.ID); err != nil {
		return err
	}
	m.ReapBlob(ctx, a.Digest)
	return nil
}

// DeleteReleaseAssets removes every asset of a release
func (m *Manager) DeleteReleaseAssets(ctx context.Context, rel *db.GitRelease) error {
	rows, err := m.store.ListGitReleaseAssets(ctx, rel.ID)
	if err != nil {
		return err
	}
	for _, a := range rows {
		if err := m.DeleteReleaseAsset(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// Reclaims the asset blobs of every release before the rows cascade away
func (m *Manager) deleteRepoReleaseAssets(ctx context.Context, repo *db.GitRepository) error {
	rows, err := m.store.ListGitReleaseAssetsByRepo(ctx, repo.ID)
	if err != nil {
		return err
	}
	for _, a := range rows {
		if err := m.DeleteReleaseAsset(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// ReapBlob deletes a blob once no table references it
func (m *Manager) ReapBlob(ctx context.Context, digest string) {
	if digest == "" {
		return
	}
	referenced, err := m.store.BlobReferenced(ctx, digest)
	if err != nil || referenced {
		return
	}
	if err := m.blobs.DeleteBlob(digest); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log.Error("blob delete %s: %v", digest, err)
	}
}

// ReleaseAssetProto is the wire shape of an asset with its browser link
func (m *Manager) ReleaseAssetProto(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, a *db.GitReleaseAsset) *v1.ReleaseAsset {
	out := &v1.ReleaseAsset{
		Id: a.ID, Name: a.Name, Label: a.Label, Size: a.Size, Digest: a.Digest, ContentType: a.ContentType,
		DownloadUrl:   m.HTMLURL(ctx, repo) + "/releases/download/" + url.PathEscape(rel.Tag) + "/" + url.PathEscape(a.Name),
		DownloadCount: a.DownloadCount,
		CreatedAt:     timestamppb.New(a.CreatedAt), UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
	if a.UploaderID != "" {
		if u, err := m.store.GetUserByID(ctx, a.UploaderID); err == nil && u != nil {
			out.Uploader = u.Username
		}
	}
	return out
}

// Audits an asset upload that came in over http
func (m *Manager) recordAssetUpload(ctx context.Context, req *gitRequest, rel *db.GitRelease, a *db.GitReleaseAsset) {
	m.audit(ctx, req.user, "Git/release_asset", req.repo.Namespace+"/"+req.repo.Name+"@"+rel.Tag+"/"+a.Name, audit.OutcomeSuccess)
}
