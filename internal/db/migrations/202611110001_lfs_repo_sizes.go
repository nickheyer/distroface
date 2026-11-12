package migrations

import (
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202611110001",
		name:    "lfs_repo_sizes",
		migrate: recountLFSRepoSizes,
	})
}

// Totals went stale for objects pushed over ssh or mirrored
func recountLFSRepoSizes(tx *gorm.DB, log *logger.Logger) error {
	res := tx.Exec(`UPDATE git_repositories SET lfs_size_bytes = (
		SELECT COALESCE(SUM(o.size), 0) FROM lfs_objects o
		JOIN git_lfs_links l ON l.oid = o.oid
		WHERE l.repo_id = git_repositories.id)`)
	if res.Error != nil {
		return res.Error
	}
	log.Info("recounted lfs bytes on %d repositories", res.RowsAffected)
	return nil
}
