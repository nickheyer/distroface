package stores

import (
	"context"
	"strings"
)

// CountBlobReferences sums every row that still names a blob digest
func (s *Store) CountBlobReferences(ctx context.Context, digest string) (int64, error) {
	if digest == "" {
		return 0, nil
	}
	var total int64
	counts := []func() (int64, error){
		func() (int64, error) { return s.CountArtifactsByDigest(ctx, digest) },
		func() (int64, error) { return s.CountActionsArtifactsByDigest(ctx, digest) },
		func() (int64, error) { return s.CountActionsJobsByLogDigest(ctx, digest) },
		func() (int64, error) { return s.CountActionsCachesByDigest(ctx, digest) },
		func() (int64, error) { return s.CountGitReleaseAssetsByDigest(ctx, digest) },
		func() (int64, error) { return s.CountLFSLinksByOID(ctx, strings.TrimPrefix(digest, "sha256:")) },
	}
	for _, count := range counts {
		n, err := count()
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// BlobReferenced reports whether any table still points at a digest
func (s *Store) BlobReferenced(ctx context.Context, digest string) (bool, error) {
	n, err := s.CountBlobReferences(ctx, digest)
	return n > 0, err
}
