package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Claims a number and inserts, or answers the existing row
func (s *Store) CreateActionsArtifactOrReuse(ctx context.Context, repoID string, a *db.ActionsArtifact) (*db.ActionsArtifact, bool, error) {
	var err error
	// Insert race losers retry and find the winner
	for attempt := 0; attempt < 3; attempt++ {
		var existing db.ActionsArtifact
		created := false
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			err := tx.Where("run_id = ? AND name = ?", a.RunID, a.Name).Order("created_at DESC").First(&existing).Error
			if err == nil {
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Model(&db.GitRepository{}).Where("id = ?", repoID).
				UpdateColumn("artifact_counter", gorm.Expr("artifact_counter + 1")).Error; err != nil {
				return err
			}
			if err := tx.Model(&db.GitRepository{}).Where("id = ?", repoID).Pluck("artifact_counter", &a.Number).Error; err != nil {
				return err
			}
			if a.ID == "" {
				a.ID = uuid.New().String()
			}
			created = true
			return tx.Create(a).Error
		})
		if err == nil {
			if created {
				return a, true, nil
			}
			return &existing, false, nil
		}
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, false, err
		}
	}
	return nil, false, err
}

// ListLiveActionsArtifactsForRepo lists confirmed, unexpired artifacts newest first, optionally one name
func (s *Store) ListLiveActionsArtifactsForRepo(ctx context.Context, repoID, name string, now time.Time, limit, offset int) ([]*db.ActionsArtifact, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ActionsArtifact{}).
		Where("status = ? AND run_id IN (SELECT id FROM actions_runs WHERE repo_id = ?)", "confirmed", repoID).
		Where("expires_at IS NULL OR expires_at > ?", now.UTC())
	if name != "" {
		q = q.Where("name = ?", name)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.ActionsArtifact
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

// ListActionsCachesForRepo lists every cache row of a repository in any state
func (s *Store) ListActionsCachesForRepo(ctx context.Context, repoID string) ([]*db.ActionsCache, error) {
	var out []*db.ActionsCache
	err := s.db.WithContext(ctx).Where("repo_id = ?", repoID).Order("id ASC").Find(&out).Error
	return out, err
}
