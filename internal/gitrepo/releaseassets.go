package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Property flagging a package artifact as a release asset
const ReleaseAssetProperty = "release"

// Upload passed the artifact size cap
var ErrAssetTooLarge = errors.New("release asset exceeds the size limit")

var (
	assetVersionEscaper   = strings.NewReplacer("%", "%25", "/", "%2F")
	assetVersionUnescaper = strings.NewReplacer("%25", "%", "%2F", "/")
)

// Longest artifact version the artifact store accepts
const maxAssetVersion = 128

// Tag as an artifact version, slashes escaped
func AssetVersion(tag string) string { return assetVersionEscaper.Replace(tag) }

// Tag behind an artifact version
func AssetTag(version string) string { return assetVersionUnescaper.Replace(version) }

// True when the artifact carries the release flag
func IsReleaseAsset(a *db.Artifact) bool {
	return a != nil && a.Properties[ReleaseAssetProperty] == "true"
}

// GitHub rules for release asset file names
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

// Per file cap, the namespace artifact limit
func (m *Manager) MaxReleaseAssetBytes(ctx context.Context, repo *db.GitRepository) int64 {
	return m.artifacts.EffectiveMaxFileSizeBytes(ctx, repo.Namespace)
}

// Same named artifact repository, nil when another repository owns it
func (m *Manager) ReleasePackage(ctx context.Context, repo *db.GitRepository) (*db.ArtifactRepository, error) {
	pkg, err := m.store.GetArtifactRepository(ctx, repo.Namespace, repo.Name)
	if err != nil || pkg == nil {
		return nil, err
	}
	if pkg.GitRepoID != nil && *pkg.GitRepoID != repo.ID {
		return nil, nil
	}
	return pkg, nil
}

// Creates or links the package, linking needs a package manager
func (m *Manager) linkReleasePackage(ctx context.Context, repo *db.GitRepository, user *auth.AuthenticatedUser) (*db.ArtifactRepository, error) {
	pkg, err := m.store.GetArtifactRepository(ctx, repo.Namespace, repo.Name)
	if err != nil {
		return nil, err
	}
	if pkg != nil && pkg.GitRepoID != nil && *pkg.GitRepoID != repo.ID {
		return nil, fmt.Errorf("%w: artifact repository %s/%s belongs to another source repository", ErrInvalid, pkg.Namespace, pkg.Name)
	}
	if pkg == nil {
		pkg = &db.ArtifactRepository{
			Namespace: repo.Namespace, Name: repo.Name, Description: "Release assets of " + repo.Namespace + "/" + repo.Name,
			OwnerID: repo.OwnerID, IsPrivate: repo.IsPrivate, Type: v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_FILE, GitRepoID: &repo.ID,
		}
		if err := m.store.CreateArtifactRepository(ctx, pkg); err != nil {
			// A concurrent upload may have created it first
			if raced, _ := m.store.GetArtifactRepository(ctx, repo.Namespace, repo.Name); raced != nil {
				return m.linkReleasePackage(ctx, repo, user)
			}
			return nil, err
		}
		return pkg, nil
	}
	if pkg.GitRepoID == nil {
		if !m.CanManageArtifact(ctx, user, pkg) {
			return nil, fmt.Errorf("%w: artifact repository %s/%s is not linked to this repository, linking it takes manage access on that package", ErrInvalid, pkg.Namespace, pkg.Name)
		}
		if err := m.store.SetArtifactRepositoryGitRepo(ctx, pkg.ID, &repo.ID); err != nil {
			return nil, err
		}
		pkg.GitRepoID = &repo.ID
	}
	if repo.IsPrivate && !pkg.IsPrivate {
		pkg.IsPrivate = true
		if err := m.store.UpdateArtifactRepository(ctx, pkg); err != nil {
			return nil, err
		}
	}
	return pkg, nil
}

// Private repository assets never sit in a public package
func (m *Manager) SealReleasePackage(ctx context.Context, repo *db.GitRepository) error {
	if !repo.IsPrivate {
		return nil
	}
	pkg, err := m.ReleasePackage(ctx, repo)
	if err != nil || pkg == nil || pkg.IsPrivate {
		return err
	}
	pkg.IsPrivate = true
	return m.store.UpdateArtifactRepository(ctx, pkg)
}

// Flagged artifacts at the tag version, by name
func (m *Manager) ReleaseAssets(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease) ([]*db.Artifact, error) {
	pkg, err := m.ReleasePackage(ctx, repo)
	if err != nil || pkg == nil {
		return nil, err
	}
	return m.store.ListArtifactsByProperty(ctx, pkg.ID, AssetVersion(rel.Tag), ReleaseAssetProperty, "true")
}

// Every flagged artifact of the package, across releases
func (m *Manager) RepoReleaseAssets(ctx context.Context, repo *db.GitRepository) ([]*db.Artifact, error) {
	pkg, err := m.ReleasePackage(ctx, repo)
	if err != nil || pkg == nil {
		return nil, err
	}
	return m.store.ListArtifactsByProperty(ctx, pkg.ID, "", ReleaseAssetProperty, "true")
}

// One asset by artifact id, nil when absent
func (m *Manager) ReleaseAsset(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, id string) (*db.Artifact, error) {
	pkg, err := m.ReleasePackage(ctx, repo)
	if err != nil || pkg == nil {
		return nil, err
	}
	a, err := m.store.GetArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil || a.RepoID != pkg.ID || a.Version != AssetVersion(rel.Tag) || !IsReleaseAsset(a) {
		return nil, nil
	}
	return a, nil
}

// One asset by file name, nil when absent
func (m *Manager) ReleaseAssetByName(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, name string) (*db.Artifact, error) {
	assets, err := m.ReleaseAssets(ctx, repo, rel)
	if err != nil {
		return nil, err
	}
	for _, a := range assets {
		if a.Path == name {
			return a, nil
		}
	}
	// Flagged artifacts in subdirectories answer to their base name
	for _, a := range assets {
		if a.Name == name {
			return a, nil
		}
	}
	return nil, nil
}

// One file streamed onto a release
type ReleaseAssetInput struct {
	Name        string
	Label       string
	ContentType string
	Body        io.Reader
	// Uploader, nil never links an unlinked package
	User *auth.AuthenticatedUser
	// Overwrites a same named asset instead of refusing
	Replace bool
}

// Streams a file into the package as a flagged artifact
func (m *Manager) StoreReleaseAsset(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, in ReleaseAssetInput) (*db.Artifact, error) {
	if err := ValidateAssetName(in.Name); err != nil {
		return nil, err
	}
	version := AssetVersion(rel.Tag)
	if len(version) > maxAssetVersion {
		return nil, fmt.Errorf("%w: tag %s is too long to carry assets, the limit is %d characters with each slash counting as three", ErrInvalid, rel.Tag, maxAssetVersion)
	}
	pkg, err := m.linkReleasePackage(ctx, repo, in.User)
	if err != nil {
		return nil, err
	}
	existing, err := m.ReleaseAssetByName(ctx, repo, rel, in.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil && !in.Replace {
		return nil, fmt.Errorf("%w: an asset named %s already exists on release %s", ErrInvalid, in.Name, rel.Tag)
	}
	// Replace reuses path and properties to hit same identity
	assetPath, props := in.Name, map[string]string{ReleaseAssetProperty: "true"}
	if existing != nil {
		assetPath, props = existing.Path, existing.Properties
	}
	a, err := m.artifacts.StoreStream(ctx, pkg, in.Body, artifacts.Upload{
		Version: version, Path: assetPath, Label: in.Label, MimeType: assetContentType(in.Name, in.ContentType), Properties: props,
	})
	switch {
	case errors.Is(err, artifacts.ErrTooLarge):
		return nil, fmt.Errorf("%w: the limit is %d MB", ErrAssetTooLarge, m.MaxReleaseAssetBytes(ctx, repo)/(1024*1024))
	case errors.Is(err, artifacts.ErrInvalid):
		return nil, fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), artifacts.ErrInvalid.Error()+": "))
	case err != nil:
		return nil, err
	}
	return a, nil
}

// Specific header wins, then extension, empty keeps the sniff
func assetContentType(name, header string) string {
	if ct := strings.TrimSpace(header); ct != "" && ct != "application/octet-stream" {
		return ct
	}
	return mime.TypeByExtension(strings.ToLower(path.Ext(name)))
}

// Renames or relabels an asset
func (m *Manager) UpdateReleaseAsset(ctx context.Context, a *db.Artifact, name, label *string) error {
	if name != nil {
		a.Name, a.Path = *name, path.Join(path.Dir(a.Path), *name)
	}
	if label != nil {
		clean, err := artifacts.NormalizeLabel(*label)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), artifacts.ErrInvalid.Error()+": "))
		}
		a.Label = clean
	}
	return m.store.UpdateArtifact(ctx, a)
}

// Streams an asset and counts the download
func (m *Manager) ServeReleaseAsset(w http.ResponseWriter, r *http.Request, a *db.Artifact) {
	w.Header().Set("Cache-Control", "private, max-age=0")
	m.artifacts.ServeArtifact(w, r, a)
}

// Removes the artifact, and its blob once unreferenced
func (m *Manager) DeleteReleaseAsset(ctx context.Context, a *db.Artifact) error {
	return m.artifacts.DeleteArtifact(ctx, a)
}

// Removes every asset of a release
func (m *Manager) DeleteReleaseAssets(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease) error {
	assets, err := m.ReleaseAssets(ctx, repo, rel)
	if err != nil {
		return err
	}
	for _, a := range assets {
		if err := m.DeleteReleaseAsset(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// Deletes a blob once no table references it
func (m *Manager) ReapBlob(ctx context.Context, digest string) {
	if digest == "" {
		return
	}
	referenced, err := m.store.BlobReferenced(ctx, digest)
	if err != nil || referenced {
		return
	}
	if err := m.Blobs().DeleteBlob(digest); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log.Error("blob delete %s: %v", digest, err)
	}
}

// Audits an http asset upload, failures included
func (m *Manager) recordAssetUpload(ctx context.Context, req *gitRequest, rel *db.GitRelease, name string, err error) {
	m.audit(ctx, req.user, "Git/release_asset", req.repo.Namespace+"/"+req.repo.Name+"@"+rel.Tag+"/"+name, outcomeFor(err))
}
