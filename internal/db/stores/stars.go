package stores

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Adds a star, reports whether one was created
func (s *Store) Star(ctx context.Context, userID, kind, targetID string) (bool, error) {
	var existing db.Star
	err := s.db.WithContext(ctx).First(&existing, "user_id = ? AND kind = ? AND target_id = ?", userID, kind, targetID).Error
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	row := &db.Star{ID: uuid.New().String(), UserID: userID, Kind: kind, TargetID: targetID}
	return true, s.db.WithContext(ctx).Create(row).Error
}

func (s *Store) Unstar(ctx context.Context, userID, kind, targetID string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND kind = ? AND target_id = ?", userID, kind, targetID).Delete(&db.Star{}).Error
}

func (s *Store) CountStars(ctx context.Context, kind, targetID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.Star{}).Where("kind = ? AND target_id = ?", kind, targetID).Count(&count).Error
	return count, err
}

// Star counts of many targets of one kind in one query
func (s *Store) StarCounts(ctx context.Context, kind string, ids []string) (map[string]int64, error) {
	counts := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	var rows []struct {
		TargetID string
		Count    int64
	}
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Select("target_id, COUNT(*) as count").
		Where("kind = ? AND target_id IN ?", kind, ids).
		Group("target_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.TargetID] = r.Count
	}
	return counts, nil
}

// Which of these targets the user starred
func (s *Store) StarredSet(ctx context.Context, userID, kind string, ids []string) (map[string]bool, error) {
	starred := make(map[string]bool, len(ids))
	if userID == "" || len(ids) == 0 {
		return starred, nil
	}
	var hit []string
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Where("user_id = ? AND kind = ? AND target_id IN ?", userID, kind, ids).
		Pluck("target_id", &hit).Error
	if err != nil {
		return nil, err
	}
	for _, id := range hit {
		starred[id] = true
	}
	return starred, nil
}

// Stars the user holds per kind
func (s *Store) CountStarsByUser(ctx context.Context, userID string) (map[string]int64, error) {
	var rows []struct {
		Kind  string
		Count int64
	}
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Select("kind, COUNT(*) as count").Where("user_id = ?", userID).Group("kind").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Kind] = r.Count
	}
	return out, nil
}

// Stars received across everything under a namespace
func (s *Store) CountStarsReceived(ctx context.Context, namespace string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.Star{}).Where(
		"(kind = ? AND target_id IN (SELECT id FROM git_repositories WHERE namespace = ?))"+
			" OR (kind = ? AND target_id IN (SELECT id FROM repositories WHERE namespace = ?))"+
			" OR (kind = ? AND target_id IN (SELECT CAST(id AS TEXT) FROM artifact_repositories WHERE namespace = ?))",
		db.StarKindGit, namespace, db.StarKindImage, namespace, db.StarKindArtifact, namespace,
	).Count(&count).Error
	return count, err
}

// Drops every star on one target
func (s *Store) DeleteStarsFor(ctx context.Context, kind, targetID string) error {
	return s.db.WithContext(ctx).Where("kind = ? AND target_id = ?", kind, targetID).Delete(&db.Star{}).Error
}

// Drops stars whose target no longer exists
func (s *Store) PruneOrphanStars(ctx context.Context) error {
	q := s.db.WithContext(ctx)
	stmts := []struct{ kind, sub string }{
		{db.StarKindGit, "SELECT id FROM git_repositories"},
		{db.StarKindImage, "SELECT id FROM repositories"},
		{db.StarKindArtifact, "SELECT CAST(id AS TEXT) FROM artifact_repositories"},
	}
	for _, st := range stmts {
		if err := q.Where("kind = ? AND target_id NOT IN ("+st.sub+")", st.kind).Delete(&db.Star{}).Error; err != nil {
			return err
		}
	}
	return nil
}
