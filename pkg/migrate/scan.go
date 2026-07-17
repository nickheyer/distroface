package migrate

import (
	"fmt"
	"strings"
)

// TagScan is the result of validating one v1 tag against the storage tree.
type TagScan struct {
	Tag          string
	Digest       string
	Err          error    // link/manifest unreadable or corrupt
	Schema1      bool     // unsupported by distribution v3
	MissingBlobs []string // blobs referenced by the manifest but absent on disk
}

// Issue returns a human-readable problem description, or "" if replayable.
func (t TagScan) Issue() string {
	switch {
	case t.Err != nil:
		return t.Err.Error()
	case t.Schema1:
		return "docker schema1 manifest (unsupported by distribution v3)"
	case len(t.MissingBlobs) > 0:
		return fmt.Sprintf("%d missing blob(s): %s", len(t.MissingBlobs), strings.Join(t.MissingBlobs, ", "))
	}
	return ""
}

type RepoScan struct {
	Repo   string
	Mapped string
	Tags   []TagScan
}

// ScanRepo validates that every tag of a v1 repo can be replayed: tag link
// resolves, manifest bytes hash-verify, and every referenced blob exists
// (recursing through manifest lists).
func ScanRepo(v1s *V1Storage, repo, legacyNS string) RepoScan {
	scan := RepoScan{Repo: repo, Mapped: MapRepoName(repo, legacyNS)}
	tags, err := v1s.ListTags(repo)
	if err != nil {
		scan.Tags = append(scan.Tags, TagScan{Err: err})
		return scan
	}
	for _, tag := range tags {
		t := TagScan{Tag: tag}
		t.Digest, err = v1s.TagDigest(repo, tag)
		if err != nil {
			t.Err = err
			scan.Tags = append(scan.Tags, t)
			continue
		}
		scanManifestTree(v1s, repo, t.Digest, &t, map[string]bool{})
		scan.Tags = append(scan.Tags, t)
	}
	return scan
}

func scanManifestTree(v1s *V1Storage, repo, digest string, t *TagScan, seen map[string]bool) {
	if seen[digest] {
		return
	}
	seen[digest] = true

	raw, err := v1s.ManifestBytes(repo, digest)
	if err != nil {
		t.Err = err
		return
	}
	m, err := ParseV1Manifest(raw)
	if err != nil {
		t.Err = fmt.Errorf("manifest %s: %w", digest, err)
		return
	}
	if m.IsSchema1() {
		t.Schema1 = true
		return
	}
	if m.IsIndex() {
		for _, child := range m.Manifests {
			scanManifestTree(v1s, repo, child.Digest, t, seen)
			if t.Err != nil {
				return
			}
		}
		return
	}
	for _, blob := range m.Blobs() {
		if _, err := v1s.StatBlob(blob.Digest); err != nil {
			t.MissingBlobs = append(t.MissingBlobs, blob.Digest)
		}
	}
}
