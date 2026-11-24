package migrations

import (
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202611230001",
		name:    "release_assets_to_artifacts",
		migrate: releaseAssetsToArtifacts,
	})
}

type legacyReleaseAsset struct {
	ID            string
	Name          string
	Label         string
	Size          int64
	Digest        string
	ContentType   string
	DownloadCount int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Tag           string
	RepoID        string
}

// Release assets become flagged artifacts of the linked package
func releaseAssetsToArtifacts(tx *gorm.DB, log *logger.Logger) error {
	if !tx.Migrator().HasTable(legacyAssetTable) {
		return nil
	}
	var rows []legacyReleaseAsset
	err := tx.Table(legacyAssetTable + " AS a").
		Select("a.id, a.name, a.label, a.size, a.digest, a.content_type, a.download_count, a.created_at, a.updated_at, r.tag, r.repo_id").
		Joins("JOIN git_releases r ON r.id = a.release_id").
		Order("a.created_at ASC").Scan(&rows).Error
	if err != nil {
		return err
	}
	hash := db.PropsFingerprint(map[string]string{"release": "true"})
	escape := strings.NewReplacer("%", "%25", "/", "%2F")
	packages := map[string]*db.ArtifactRepository{}
	moved, left := 0, 0
	for _, row := range rows {
		pkg, seen := packages[row.RepoID]
		if !seen {
			if pkg, err = releasePackageFor(tx, row.RepoID); err != nil {
				return err
			}
			packages[row.RepoID] = pkg
		}
		if pkg == nil {
			log.Warn("release asset %s of tag %s stays in %s, the artifact repository of that name belongs to another repository", row.Name, row.Tag, legacyAssetTable)
			left++
			continue
		}
		version := escape.Replace(row.Tag)
		var clash db.Artifact
		err := tx.First(&clash, "repo_id = ? AND version = ? AND path = ? AND props_hash = ?", pkg.ID, version, row.Name, hash).Error
		switch {
		case err == gorm.ErrRecordNotFound:
		case err != nil:
			return err
		case clash.Digest == row.Digest:
			if err := mergeLegacyAsset(tx, &clash, row); err != nil {
				return err
			}
			moved++
			continue
		default:
			freed, err := unflagArtifact(tx, &clash)
			if err != nil {
				return err
			}
			if !freed {
				log.Warn("release asset %s of tag %s stays in %s, artifact %s holds its place", row.Name, row.Tag, legacyAssetTable, clash.ID)
				left++
				continue
			}
			log.Warn("artifact %s gave its release flag to the asset %s of tag %s", clash.ID, row.Name, row.Tag)
		}
		artifact := &db.Artifact{
			ID: row.ID, RepoID: pkg.ID, Name: row.Name, Path: row.Name, UploadID: row.ID, Version: version, PropsHash: hash,
			Digest: row.Digest, Size: row.Size, MimeType: row.ContentType, Metadata: "{}", Label: row.Label,
			DownloadCount: row.DownloadCount, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		if err := tx.Create(artifact).Error; err != nil {
			return err
		}
		if err := tx.Create(&db.ArtifactProperty{ArtifactID: artifact.ID, Key: "release", Value: "true"}).Error; err != nil {
			return err
		}
		if err := dropLegacyAsset(tx, row.ID); err != nil {
			return err
		}
		moved++
	}
	if moved > 0 {
		log.Info("moved %d release assets into artifact repositories", moved)
	}
	if left > 0 {
		log.Warn("%d release assets could not move and remain in %s with their files", left, legacyAssetTable)
		return nil
	}
	return tx.Migrator().DropTable(legacyAssetTable)
}

const legacyAssetTable = "git_release_assets"

func dropLegacyAsset(tx *gorm.DB, id string) error {
	return tx.Exec("DELETE FROM "+legacyAssetTable+" WHERE id = ?", id).Error
}

// Same file already flagged, counters and label fold in
func mergeLegacyAsset(tx *gorm.DB, kept *db.Artifact, row legacyReleaseAsset) error {
	cols := map[string]any{"download_count": kept.DownloadCount + row.DownloadCount}
	if kept.Label == "" {
		cols["label"] = row.Label
	}
	if err := tx.Model(&db.Artifact{}).Where("id = ?", kept.ID).UpdateColumns(cols).Error; err != nil {
		return err
	}
	return dropLegacyAsset(tx, row.ID)
}

// Different file keeps its row but loses the flag
func unflagArtifact(tx *gorm.DB, a *db.Artifact) (bool, error) {
	plain := db.PropsFingerprint(map[string]string{"release": "false"})
	var taken int64
	if err := tx.Model(&db.Artifact{}).Where("repo_id = ? AND version = ? AND path = ? AND props_hash = ?", a.RepoID, a.Version, a.Path, plain).Count(&taken).Error; err != nil {
		return false, err
	}
	if taken > 0 {
		return false, nil
	}
	if err := tx.Model(&db.ArtifactProperty{}).Where("artifact_id = ?", a.ID).UpdateColumn("value", "false").Error; err != nil {
		return false, err
	}
	return true, tx.Model(&db.Artifact{}).Where("id = ?", a.ID).UpdateColumn("props_hash", plain).Error
}

// Same named artifact repository, created when missing
func releasePackageFor(tx *gorm.DB, gitRepoID string) (*db.ArtifactRepository, error) {
	var repo db.GitRepository
	if err := tx.First(&repo, "id = ?", gitRepoID).Error; err != nil {
		return nil, err
	}
	var pkg db.ArtifactRepository
	err := tx.First(&pkg, "namespace = ? AND name = ?", repo.Namespace, repo.Name).Error
	if err == gorm.ErrRecordNotFound {
		pkg = db.ArtifactRepository{
			Namespace: repo.Namespace, Name: repo.Name, Description: "Release assets of " + repo.Namespace + "/" + repo.Name,
			OwnerID: repo.OwnerID, IsPrivate: repo.IsPrivate, Type: v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_FILE, GitRepoID: &repo.ID,
		}
		return &pkg, tx.Create(&pkg).Error
	}
	if err != nil {
		return nil, err
	}
	if pkg.GitRepoID != nil && *pkg.GitRepoID != repo.ID {
		return nil, nil
	}
	// Linking shares the package, left to someone who manages it
	if !repo.IsPrivate || pkg.IsPrivate {
		return &pkg, nil
	}
	return &pkg, tx.Model(&db.ArtifactRepository{}).Where("id = ?", pkg.ID).Update("is_private", true).Error
}
