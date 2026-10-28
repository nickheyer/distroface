package stores

import (
	"context"
	"errors"

	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) GetUserAvatar(ctx context.Context, userID string) (*db.UserAvatar, error) {
	var row db.UserAvatar
	err := s.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *Store) SetUserAvatar(ctx context.Context, row *db.UserAvatar) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"content_type", "data", "updated_at"}),
	}).Create(row).Error
}

func (s *Store) DeleteUserAvatar(ctx context.Context, userID string) error {
	return s.db.WithContext(ctx).Delete(&db.UserAvatar{}, "user_id = ?", userID).Error
}
