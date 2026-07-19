package stores

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
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

// TokensQuery allowlists api token list filters
var TokensQuery = pages.Spec{
	Fields: map[string]string{"name": "name"},
	Text:   []string{"name"},
}

func (s *Store) ListAPITokens(ctx context.Context, userID string, q pages.Query, limit, offset int) ([]*db.APIToken, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.APIToken{}).Scopes(TokensQuery.Scope(q))
	if userID != "" {
		tx = tx.Where("user_id = ?", userID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var tokens []*db.APIToken
	err := tx.Order("created_at DESC").Limit(limit).Offset(offset).Find(&tokens).Error
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
