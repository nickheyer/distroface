package migrations

import (
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610280003",
		name:    "release_assets",
		migrate: moveReleaseAssetsOntoReleases,
	})
}

// Release assets leave the artifact package and hang off their release
func moveReleaseAssetsOntoReleases(tx *gorm.DB, log *logger.Logger) error {
	if !tx.Migrator().HasTable(&db.GitReleaseAsset{}) {
		if err := tx.Migrator().CreateTable(&db.GitReleaseAsset{}); err != nil {
			return err
		}
	}
	if !tx.Migrator().HasTable("artifacts") || !tx.Migrator().HasTable("git_releases") {
		return nil
	}
	type oldRow struct {
		ID        string
		RepoID    int64
		Path      string
		Version   string
		Digest    string
		Size      int64
		MimeType  string
		CreatedAt string
		ReleaseID string
	}
	var rows []oldRow
	q := `SELECT a.id, a.repo_id, a.path, a.version, a.digest, a.size, a.mime_type, a.created_at, r.id AS release_id
		FROM artifacts a
		JOIN artifact_repositories p ON p.id = a.repo_id
		JOIN git_releases r ON r.repo_id = p.git_repo_id AND r.tag = a.version
		WHERE p.git_repo_id IS NOT NULL
		AND EXISTS (SELECT 1 FROM artifact_properties ap WHERE ap.artifact_id = a.id AND ap.key = 'release' AND ap.value = 'true')`
	if err := tx.Raw(q).Scan(&rows).Error; err != nil {
		return err
	}
	moved := 0
	touched := map[int64]bool{}
	for _, row := range rows {
		var label string
		_ = tx.Raw(`SELECT value FROM artifact_properties WHERE artifact_id = ? AND key = 'label'`, row.ID).Scan(&label).Error
		asset := &db.GitReleaseAsset{
			ID: row.ID, ReleaseID: row.ReleaseID, Name: row.Path, Label: label, Size: row.Size, Digest: row.Digest, ContentType: row.MimeType,
		}
		if asset.ContentType == "" {
			asset.ContentType = "application/octet-stream"
		}
		var dup int64
		if err := tx.Raw(`SELECT COUNT(*) FROM git_release_assets WHERE release_id = ? AND name = ?`, row.ReleaseID, row.Path).Scan(&dup).Error; err != nil {
			return err
		}
		if dup == 0 {
			if err := tx.Create(asset).Error; err != nil {
				return err
			}
			if row.CreatedAt != "" {
				_ = tx.Exec(`UPDATE git_release_assets SET created_at = ?, updated_at = ? WHERE id = ?`, row.CreatedAt, row.CreatedAt, asset.ID).Error
			}
			moved++
		}
		if err := tx.Exec(`DELETE FROM artifact_properties WHERE artifact_id = ?`, row.ID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM artifacts WHERE id = ?`, row.ID).Error; err != nil {
			return err
		}
		touched[row.RepoID] = true
	}
	// Packages created only to hold release assets go once they are empty
	for repoID := range touched {
		var left int64
		if err := tx.Raw(`SELECT COUNT(*) FROM artifacts WHERE repo_id = ?`, repoID).Scan(&left).Error; err != nil {
			return err
		}
		if left > 0 {
			continue
		}
		res := tx.Exec(`DELETE FROM artifact_repositories WHERE id = ? AND type = ? AND (description LIKE 'Release assets of %' OR description LIKE 'Release assets mirrored from %')`, repoID, 1)
		if res.Error != nil {
			return res.Error
		}
	}
	// The kind column and its index are gone with the package detour
	if cols, err := actualColumns(tx, &db.Artifact{}); err != nil {
		return err
	} else if cols["kind"] {
		if err := tx.Exec(`DROP INDEX IF EXISTS idx_artifact_repo_kind`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE artifacts DROP COLUMN kind`).Error; err != nil {
			return err
		}
	}
	if moved > 0 {
		log.Info("moved %d release assets onto their releases", moved)
	}
	return nil
}
