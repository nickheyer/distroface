package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// V1Storage reads the v1 on-disk layout, which differs from distribution v3:
//
//	<root>/blobs/sha256/<hex>                                        blob content (flat, unsharded)
//	<root>/repositories/<name>/_manifests/revisions/sha256:<hex>     raw manifest bytes
//	<root>/repositories/<name>/_manifests/tags/<tag>/current/link    manifest digest string
//	<root>/repositories/<name>/_layers/...                           link tree (ignored; blobs read directly)
//	<root>/artifacts/repos/<repo>/versions/<ver>/files/<uploadID>/<path>
type V1Storage struct {
	root string
}

func NewV1Storage(root string) (*V1Storage, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("v1 storage root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("v1 storage root %s is not a directory", root)
	}
	return &V1Storage{root: root}, nil
}

// ListRepos walks <root>/repositories for dirs containing _manifests.
// Repo names are one or two path segments deep ("foo" or "foo/bar").
func (s *V1Storage) ListRepos() ([]string, error) {
	base := filepath.Join(s.root, "repositories")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var repos []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s.isRepoDir(filepath.Join(base, e.Name())) {
			repos = append(repos, e.Name())
			continue
		}
		subEntries, err := os.ReadDir(filepath.Join(base, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, sub := range subEntries {
			if sub.IsDir() && s.isRepoDir(filepath.Join(base, e.Name(), sub.Name())) {
				repos = append(repos, e.Name()+"/"+sub.Name())
			}
		}
	}
	sort.Strings(repos)
	return repos, nil
}

func (s *V1Storage) isRepoDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "_manifests"))
	return err == nil && info.IsDir()
}

func (s *V1Storage) ListTags(repo string) ([]string, error) {
	dir := filepath.Join(s.root, "repositories", filepath.FromSlash(repo), "_manifests", "tags")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() {
			tags = append(tags, e.Name())
		}
	}
	sort.Strings(tags)
	return tags, nil
}

// TagDigest reads the digest a tag points at.
func (s *V1Storage) TagDigest(repo, tag string) (string, error) {
	link := filepath.Join(s.root, "repositories", filepath.FromSlash(repo), "_manifests", "tags", tag, "current", "link")
	data, err := os.ReadFile(link)
	if err != nil {
		return "", err
	}
	digest := strings.TrimSpace(string(data))
	if !strings.HasPrefix(digest, "sha256:") {
		return "", fmt.Errorf("tag %s/%s: malformed digest link %q", repo, tag, digest)
	}
	return digest, nil
}

// ManifestBytes reads raw manifest bytes for a digest and verifies content hash.
func (s *V1Storage) ManifestBytes(repo, digest string) ([]byte, error) {
	path := filepath.Join(s.root, "repositories", filepath.FromSlash(repo), "_manifests", "revisions", digest)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != digest {
		return nil, fmt.Errorf("manifest %s/%s: content hash %s does not match filename", repo, digest, got)
	}
	return data, nil
}

func (s *V1Storage) BlobPath(digest string) string {
	return filepath.Join(s.root, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

// StatBlob returns the blob size, or an error if missing.
func (s *V1Storage) StatBlob(digest string) (int64, error) {
	info, err := os.Stat(s.BlobPath(digest))
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ArtifactFilePath returns the on-disk path for a v1 artifact row.
func (s *V1Storage) ArtifactFilePath(a V1Artifact) string {
	return filepath.Join(s.root, "artifacts", "repos", a.RepoName, "versions", a.Version, "files", a.UploadID, filepath.FromSlash(a.Path))
}

// ── Manifest parsing ────────────────────────────────────────────────────────

const (
	mtDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	mtDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	mtOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mtOCIIndex       = "application/vnd.oci.image.index.v1+json"
)

type V1Descriptor struct {
	MediaType string   `json:"mediaType"`
	Size      int64    `json:"size"`
	Digest    string   `json:"digest"`
	URLs      []string `json:"urls,omitempty"`
}

type V1Manifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	MediaType     string         `json:"mediaType"`
	Config        *V1Descriptor  `json:"config"`
	Layers        []V1Descriptor `json:"layers"`
	Manifests     []V1Descriptor `json:"manifests"` // set for manifest lists / indexes
}

// IsIndex reports whether this is a manifest list / OCI index.
func (m *V1Manifest) IsIndex() bool { return len(m.Manifests) > 0 && m.Config == nil }

// IsSchema1 reports legacy schema1 manifests, which distribution v3 rejects.
func (m *V1Manifest) IsSchema1() bool { return m.SchemaVersion == 1 }

// EffectiveMediaType returns the declared media type, or infers one:
// docker schema2 always declares it, OCI artifacts may omit it.
func (m *V1Manifest) EffectiveMediaType() string {
	if m.MediaType != "" {
		return m.MediaType
	}
	if m.IsIndex() {
		return mtOCIIndex
	}
	return mtOCIManifest
}

// Blobs returns config + layer descriptors that must exist in the blob store.
// Foreign / nondistributable layers and URL-backed layers are excluded.
func (m *V1Manifest) Blobs() []V1Descriptor {
	var blobs []V1Descriptor
	if m.Config != nil {
		blobs = append(blobs, *m.Config)
	}
	for _, l := range m.Layers {
		if len(l.URLs) > 0 || strings.Contains(l.MediaType, "foreign") || strings.Contains(l.MediaType, "nondistributable") {
			continue
		}
		blobs = append(blobs, l)
	}
	return blobs
}

func ParseV1Manifest(data []byte) (*V1Manifest, error) {
	var m V1Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
