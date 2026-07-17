package stores

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── APIToken operations ──────────────────────────────────────────────────

func (s *Store) CreateAPIToken(ctx context.Context, token *db.APIToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(token).Error
}

func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (*db.APIToken, error) {
	var token db.APIToken
	err := s.db.WithContext(ctx).Where("token_hash = ?", hash).First(&token).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID string, limit, offset int) ([]*db.APIToken, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.APIToken{})
	if userID != "" {
		tx = tx.Where("user_id = ?", userID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := s.db.WithContext(ctx)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	var tokens []*db.APIToken
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&tokens).Error
	return tokens, total, err
}

func (s *Store) DeleteAPIToken(ctx context.Context, id, userID string) error {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	result := query.Delete(&db.APIToken{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("api token not found")
	}
	return nil
}

func (s *Store) UpdateAPITokenLastUsed(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.APIToken{}).Where("id = ?", id).Update("last_used_at", time.Now()).Error
}
