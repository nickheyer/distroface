package stores

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

// Cache entry states
const (
	ActionsCacheReserved = "reserved"
	ActionsCacheComplete = "complete"
)

func (s *Store) CreateActionsCache(ctx context.Context, c *db.ActionsCache) error {
	if c.LastUsedAt.IsZero() {
		c.LastUsedAt = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *Store) GetActionsCache(ctx context.Context, id int64) (*db.ActionsCache, error) {
	var c db.ActionsCache
	if err := s.db.WithContext(ctx).First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// Entry by id confined to one repository
func (s *Store) GetActionsCacheInRepo(ctx context.Context, repoID string, id int64) (*db.ActionsCache, error) {
	var c db.ActionsCache
	if err := s.db.WithContext(ctx).Where("id = ? AND repo_id = ?", id, repoID).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// Complete or live reserved entry already holding the key and version
func (s *Store) ActionsCacheExists(ctx context.Context, repoID, key, version string, reservedSince time.Time) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.ActionsCache{}).
		Where("repo_id = ? AND cache_key = ? AND version = ?", repoID, key, version).
		Where("state = ? OR created_at > ?", ActionsCacheComplete, reservedSince).
		Count(&n).Error
	return n > 0, err
}

// Complete entries whose key starts with the prefix, newest first
func (s *Store) ListActionsCachesByPrefix(ctx context.Context, repoID, prefix, version string, limit int) ([]*db.ActionsCache, error) {
	var out []*db.ActionsCache
	q := s.db.WithContext(ctx).
		Where("repo_id = ? AND version = ? AND state = ?", repoID, version, ActionsCacheComplete).
		Where("cache_key "+pages.LikeOp(s.db)+" ? ESCAPE '\\'", likePrefix(prefix)).
		Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return out, q.Find(&out).Error
}

// Escapes like wildcards so the key matches literally
func likePrefix(prefix string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(prefix) + "%"
}

func (s *Store) UpdateActionsCacheColumns(ctx context.Context, id int64, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.ActionsCache{}).Where("id = ?", id).Updates(cols).Error
}

func (s *Store) DeleteActionsCache(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Delete(&db.ActionsCache{}, "id = ?", id).Error
}

// Reservations nobody committed before the cutoff
func (s *Store) ListReservedActionsCachesBefore(ctx context.Context, cutoff time.Time, limit int) ([]*db.ActionsCache, error) {
	var out []*db.ActionsCache
	q := s.db.WithContext(ctx).Where("state = ? AND created_at < ?", ActionsCacheReserved, cutoff).Order("created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return out, q.Find(&out).Error
}

// Bytes held by complete entries of one repository
func (s *Store) SumActionsCacheBytes(ctx context.Context, repoID string) (int64, error) {
	var total *int64
	err := s.db.WithContext(ctx).Model(&db.ActionsCache{}).
		Where("repo_id = ? AND state = ?", repoID, ActionsCacheComplete).
		Select("SUM(size)").Scan(&total).Error
	if err != nil || total == nil {
		return 0, err
	}
	return *total, nil
}

// Complete entries of a repository, least recently used first
func (s *Store) ListActionsCachesLRU(ctx context.Context, repoID string, limit int) ([]*db.ActionsCache, error) {
	var out []*db.ActionsCache
	q := s.db.WithContext(ctx).Where("repo_id = ? AND state = ?", repoID, ActionsCacheComplete).Order("last_used_at ASC, id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return out, q.Find(&out).Error
}

// Complete entries nobody restored since the cutoff
func (s *Store) ListActionsCachesUnusedBefore(ctx context.Context, cutoff time.Time, limit int) ([]*db.ActionsCache, error) {
	var out []*db.ActionsCache
	q := s.db.WithContext(ctx).Where("state = ? AND last_used_at < ?", ActionsCacheComplete, cutoff).Order("last_used_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return out, q.Find(&out).Error
}

func (s *Store) CountActionsCachesByDigest(ctx context.Context, digest string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.ActionsCache{}).Where("digest = ?", digest).Count(&n).Error
	return n, err
}
