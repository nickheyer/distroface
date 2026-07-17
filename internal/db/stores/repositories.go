package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/pagination"
	"gorm.io/gorm"
)

// ── Repository operations ────────────────────────────────────────────────

func (s *Store) CreateRepository(ctx context.Context, repo *db.Repository) error {
	return s.db.WithContext(ctx).Create(repo).Error
}

func (s *Store) GetRepository(ctx context.Context, namespace, name string) (*db.Repository, error) {
	var repo db.Repository
	err := s.db.WithContext(ctx).First(&repo, "namespace = ? AND name = ?", namespace, name).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &repo, nil
}

// ListRepositories returns repositories with visibility filtering.
//
// If canManage is true, all repositories are returned (no visibility filtering).
// Otherwise, the returned set is:
//   - All public repositories
//   - Private repos owned by userID (owner_id matches)
//   - Private repos whose namespace is a user's username (personal repos)
//   - Private repos in organizations the user is a member of
//   - Private repos explicitly granted via RBAC (grantedRepos contains "namespace/name")
//
// ReposQuery allowlists docker repository list filters
var ReposQuery = pagination.Spec{
	Fields: map[string]string{
		"name":        "name",
		"namespace":   "namespace",
		"description": "description",
	},
	Text: []string{"name", "namespace", "description"},
}

// If userID is empty (anonymous), only public repos are returned.
func (s *Store) ListRepositories(ctx context.Context, namespace string, q pagination.Query, userID string, canManage bool, grantedRepos []string, limit, offset int) ([]*db.Repository, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Repository{})

	if namespace != "" {
		tx = tx.Where("namespace = ?", namespace)
	}
	tx = tx.Scopes(ReposQuery.Scope(q))

	if !canManage {
		if userID != "" {
			// Authenticated user: public + owned + org membership + RBAC grants
			conditions := "is_private = ? OR owner_id = ? OR namespace IN (SELECT o.name FROM organizations o JOIN org_members om ON o.id = om.org_id WHERE om.user_id = ?)"
			args := []interface{}{false, userID, userID}

			if len(grantedRepos) > 0 {
				conditions += " OR (namespace || '/' || name) IN ?"
				args = append(args, grantedRepos)
			}

			tx = tx.Where(conditions, args...)
		} else {
			// Anonymous: public only
			tx = tx.Where("is_private = ?", false)
		}
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var repos []*db.Repository
	err := tx.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&repos).Error
	return repos, total, err
}

func (s *Store) DeleteRepository(ctx context.Context, namespace, name string) error {
	return s.db.WithContext(ctx).Delete(&db.Repository{}, "namespace = ? AND name = ?", namespace, name).Error
}

func (s *Store) UpdateRepository(ctx context.Context, repo *db.Repository) error {
	return s.db.WithContext(ctx).Save(repo).Error
}

func (s *Store) IncrementPullCount(ctx context.Context, namespace, name string) error {
	return s.db.WithContext(ctx).Model(&db.Repository{}).
		Where("namespace = ? AND name = ?", namespace, name).
		UpdateColumn("pull_count", gorm.Expr("pull_count + 1")).Error
}

func (s *Store) IncrementPushCount(ctx context.Context, namespace, name string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&db.Repository{}).
		Where("namespace = ? AND name = ?", namespace, name).
		Updates(map[string]interface{}{
			"push_count": gorm.Expr("push_count + 1"),
			"last_push":  now,
		}).Error
}

// ── Star operations ──────────────────────────────────────────────────────

func (s *Store) StarRepository(ctx context.Context, userID, repoID string) error {
	var existing db.Star
	err := s.db.WithContext(ctx).First(&existing, "user_id = ? AND repo_id = ?", userID, repoID).Error
	if err == nil {
		return nil // Already starred
	}
	star := &db.Star{ID: uuid.New().String(), UserID: userID, RepoID: repoID}
	return s.db.WithContext(ctx).Create(star).Error
}

func (s *Store) UnstarRepository(ctx context.Context, userID, repoID string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND repo_id = ?", userID, repoID).Delete(&db.Star{}).Error
}

func (s *Store) CountStars(ctx context.Context, repoID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.Star{}).Where("repo_id = ?", repoID).Count(&count).Error
	return count, err
}

// Star counts for many repos in one query
func (s *Store) GetStarCounts(ctx context.Context, repoIDs []string) (map[string]int64, error) {
	counts := make(map[string]int64)
	if len(repoIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		RepoID string
		Count  int64
	}
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Select("repo_id, COUNT(*) as count").
		Where("repo_id IN ?", repoIDs).
		Group("repo_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.RepoID] = r.Count
	}
	return counts, nil
}

// Which of these repos the user starred
func (s *Store) GetStarredSet(ctx context.Context, userID string, repoIDs []string) (map[string]bool, error) {
	starred := make(map[string]bool)
	if userID == "" || len(repoIDs) == 0 {
		return starred, nil
	}
	var ids []string
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Where("user_id = ? AND repo_id IN ?", userID, repoIDs).
		Pluck("repo_id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		starred[id] = true
	}
	return starred, nil
}

// Newest starred first
func (s *Store) ListStarredRepositories(ctx context.Context, userID string, limit, offset int) ([]*db.Repository, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Repository{}).
		Joins("JOIN stars ON stars.repo_id = repositories.id").
		Where("stars.user_id = ?", userID)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var repos []*db.Repository
	err := tx.Order("stars.created_at DESC").Limit(limit).Offset(offset).Find(&repos).Error
	return repos, total, err
}
