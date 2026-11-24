package stores

import (
	"context"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
)

// BlobReferenced reports whether any table still points at a digest
func (s *Store) BlobReferenced(ctx context.Context, digest string) (bool, error) {
	if digest == "" {
		return false, nil
	}
	oid := strings.TrimPrefix(digest, "sha256:")
	checks := []struct {
		model any
		where string
		arg   string
	}{
		{&db.Artifact{}, "digest = ?", digest},
		{&db.ActionsArtifact{}, "digest = ?", digest},
		{&db.ActionsCache{}, "digest = ?", digest},
		{&db.GitLFSLink{}, "oid = ?", oid},
		{&db.ActionsJob{}, "log_digest = ?", digest},
	}
	for _, c := range checks {
		var hit []int
		if err := s.db.WithContext(ctx).Model(c.model).Select("1").Where(c.where, c.arg).Limit(1).Scan(&hit).Error; err != nil {
			return false, err
		}
		if len(hit) > 0 {
			return true, nil
		}
	}
	return s.legacyAssetBlob(ctx, digest)
}

// Assets the release migration could not move keep their blobs
func (s *Store) legacyAssetBlob(ctx context.Context, digest string) (bool, error) {
	const table = "git_release_assets"
	if !s.db.Migrator().HasTable(table) {
		return false, nil
	}
	var hit []int
	err := s.db.WithContext(ctx).Table(table).Select("1").Where("digest = ?", digest).Limit(1).Scan(&hit).Error
	return len(hit) > 0, err
}
