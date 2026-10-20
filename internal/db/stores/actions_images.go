package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Records or refreshes one tag a run pushed
func (s *Store) UpsertActionsRunImage(ctx context.Context, img *db.ActionsRunImage) error {
	if img.ID == "" {
		img.ID = uuid.New().String()
	}
	if img.CreatedAt.IsZero() {
		img.CreatedAt = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "run_id"}, {Name: "image_repo_id"}, {Name: "tag"}},
		DoUpdates: clause.AssignmentColumns([]string{"job_id", "digest", "created_at"}),
	}).Create(img).Error
}

func (s *Store) ListActionsRunImages(ctx context.Context, runID string) ([]*db.ActionsRunImage, error) {
	var out []*db.ActionsRunImage
	err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// Images pushed by any run of a repository on one ref
func (s *Store) ListActionsRunImagesForRef(ctx context.Context, repoID, ref string) ([]*db.ActionsRunImage, error) {
	var out []*db.ActionsRunImage
	err := s.db.WithContext(ctx).
		Joins("JOIN actions_runs ON actions_runs.id = actions_run_images.run_id").
		Where("actions_runs.repo_id = ? AND actions_runs.ref = ?", repoID, ref).
		Order("actions_run_images.created_at ASC").
		Find(&out).Error
	return out, err
}

func (s *Store) GetActionsRunImage(ctx context.Context, id string) (*db.ActionsRunImage, error) {
	var img db.ActionsRunImage
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&img).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &img, nil
}
