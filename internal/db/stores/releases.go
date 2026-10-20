package stores

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

func (s *Store) CreateGitRelease(ctx context.Context, r *db.GitRelease) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *Store) GetGitRelease(ctx context.Context, repoID, tag string) (*db.GitRelease, error) {
	var r db.GitRelease
	err := s.db.WithContext(ctx).First(&r, "repo_id = ? AND tag = ?", repoID, tag).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) GetGitReleaseByID(ctx context.Context, id string) (*db.GitRelease, error) {
	var r db.GitRelease
	err := s.db.WithContext(ctx).First(&r, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

// ReleasesQuery allowlists release list filters
var ReleasesQuery = pages.Spec{
	Fields: map[string]string{"tag": "tag", "name": "name"},
	Text:   []string{"tag", "name"},
}

func (s *Store) ListGitReleases(ctx context.Context, repoID string, drafts, prereleases bool, q pages.Query, limit, offset int) ([]*db.GitRelease, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.GitRelease{}).Where("repo_id = ?", repoID).Scopes(ReleasesQuery.Scope(q))
	if !drafts {
		tx = tx.Where("draft = ?", false)
	}
	if !prereleases {
		tx = tx.Where("prerelease = ?", false)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitRelease
	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	err := tx.Order("COALESCE(published_at, created_at) DESC").Find(&out).Error
	return out, total, err
}

// Tags that have a release, for badges
func (s *Store) GitReleaseTags(ctx context.Context, repoID string) (map[string]bool, error) {
	var tags []string
	if err := s.db.WithContext(ctx).Model(&db.GitRelease{}).Where("repo_id = ?", repoID).Pluck("tag", &tags).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(tags))
	for _, t := range tags {
		out[t] = true
	}
	return out, nil
}

func (s *Store) CountGitReleases(ctx context.Context, repoIDs []string) (map[string]int32, error) {
	out := map[string]int32{}
	if len(repoIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		RepoID string
		Count  int32
	}
	err := s.db.WithContext(ctx).Model(&db.GitRelease{}).Select("repo_id, COUNT(*) as count").
		Where("repo_id IN ? AND draft = ?", repoIDs, false).Group("repo_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.RepoID] = r.Count
	}
	return out, nil
}

func (s *Store) LatestGitRelease(ctx context.Context, repoID string) (*db.GitRelease, error) {
	var r db.GitRelease
	err := s.db.WithContext(ctx).Where("repo_id = ? AND draft = ? AND prerelease = ?", repoID, false, false).
		Order("COALESCE(published_at, created_at) DESC").First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) UpdateGitRelease(ctx context.Context, r *db.GitRelease) error {
	return s.db.WithContext(ctx).Save(r).Error
}

func (s *Store) DeleteGitRelease(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitRelease{}, "id = ?", id).Error
}

func (s *Store) AttachGitReleaseImage(ctx context.Context, img *db.GitReleaseImage) error {
	if img.ID == "" {
		img.ID = uuid.New().String()
	}
	var existing db.GitReleaseImage
	err := s.db.WithContext(ctx).First(&existing, "release_id = ? AND image_repo_id = ? AND tag = ?", img.ReleaseID, img.ImageRepoID, img.Tag).Error
	if err == nil {
		return s.db.WithContext(ctx).Model(&existing).Update("digest", img.Digest).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return s.db.WithContext(ctx).Create(img).Error
}

func (s *Store) DetachGitReleaseImage(ctx context.Context, releaseID, imageRepoID, tag string) error {
	return s.db.WithContext(ctx).Where("release_id = ? AND image_repo_id = ? AND tag = ?", releaseID, imageRepoID, tag).Delete(&db.GitReleaseImage{}).Error
}

func (s *Store) ListGitReleaseImages(ctx context.Context, releaseID string) ([]*db.GitReleaseImage, error) {
	var out []*db.GitReleaseImage
	err := s.db.WithContext(ctx).Where("release_id = ?", releaseID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// Tags pinned by any release of the repo, for badges on the image page
func (s *Store) GitReleaseImageTags(ctx context.Context, imageRepoID string) (map[string]string, error) {
	var rows []struct {
		Tag        string
		ReleaseTag string
	}
	err := s.db.WithContext(ctx).Table("git_release_images").Select("git_release_images.tag, git_releases.tag as release_tag").
		Joins("JOIN git_releases ON git_releases.id = git_release_images.release_id").
		Where("git_release_images.image_repo_id = ?", imageRepoID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Tag] = r.ReleaseTag
	}
	return out, nil
}

func (s *Store) CreateGitReleaseAsset(ctx context.Context, a *db.GitReleaseAsset) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *Store) GetGitReleaseAsset(ctx context.Context, id string) (*db.GitReleaseAsset, error) {
	var a db.GitReleaseAsset
	err := s.db.WithContext(ctx).First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

func (s *Store) GetGitReleaseAssetByName(ctx context.Context, releaseID, name string) (*db.GitReleaseAsset, error) {
	var a db.GitReleaseAsset
	err := s.db.WithContext(ctx).First(&a, "release_id = ? AND name = ?", releaseID, name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

// Assets of one release, by name
func (s *Store) ListGitReleaseAssets(ctx context.Context, releaseID string) ([]*db.GitReleaseAsset, error) {
	var out []*db.GitReleaseAsset
	err := s.db.WithContext(ctx).Where("release_id = ?", releaseID).Order("name ASC").Find(&out).Error
	return out, err
}

// Assets of every release of a repository, for cleanup
func (s *Store) ListGitReleaseAssetsByRepo(ctx context.Context, repoID string) ([]*db.GitReleaseAsset, error) {
	var out []*db.GitReleaseAsset
	err := s.db.WithContext(ctx).Table("git_release_assets").
		Joins("JOIN git_releases ON git_releases.id = git_release_assets.release_id").
		Where("git_releases.repo_id = ?", repoID).Find(&out).Error
	return out, err
}

// Asset counts per release, for listings
func (s *Store) CountGitReleaseAssets(ctx context.Context, releaseIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(releaseIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ReleaseID string
		Count     int64
	}
	err := s.db.WithContext(ctx).Model(&db.GitReleaseAsset{}).Select("release_id, COUNT(*) as count").
		Where("release_id IN ?", releaseIDs).Group("release_id").Scan(&rows).Error
	for _, r := range rows {
		out[r.ReleaseID] = r.Count
	}
	return out, err
}

func (s *Store) UpdateGitReleaseAsset(ctx context.Context, a *db.GitReleaseAsset) error {
	return s.db.WithContext(ctx).Save(a).Error
}

func (s *Store) IncrementGitReleaseAssetDownloads(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.GitReleaseAsset{}).Where("id = ?", id).
		UpdateColumn("download_count", gorm.Expr("download_count + 1")).Error
}

func (s *Store) DeleteGitReleaseAsset(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitReleaseAsset{}, "id = ?", id).Error
}

func (s *Store) CountGitReleaseAssetsByDigest(ctx context.Context, digest string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.GitReleaseAsset{}).Where("digest = ?", digest).Count(&n).Error
	return n, err
}
