package stores

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── RegistrationInvite operations ────────────────────────────────────────

func (s *Store) CreateRegistrationInvite(ctx context.Context, invite *db.RegistrationInvite) error {
	if invite.ID == "" {
		invite.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(invite).Error
}

func (s *Store) GetRegistrationInvite(ctx context.Context, id string) (*db.RegistrationInvite, error) {
	var invite db.RegistrationInvite
	err := s.db.WithContext(ctx).First(&invite, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &invite, nil
}

func (s *Store) GetRegistrationInviteByCode(ctx context.Context, code string) (*db.RegistrationInvite, error) {
	var invite db.RegistrationInvite
	err := s.db.WithContext(ctx).First(&invite, "code = ?", code).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &invite, nil
}

func (s *Store) ListRegistrationInvites(ctx context.Context, limit, offset int) ([]*db.RegistrationInvite, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.RegistrationInvite{})

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var invites []*db.RegistrationInvite
	err := tx.Order("created_at DESC").Limit(limit).Offset(offset).Find(&invites).Error
	return invites, total, err
}

func (s *Store) IncrementInviteUseCount(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.RegistrationInvite{}).
		Where("id = ?", id).
		UpdateColumn("use_count", gorm.Expr("use_count + 1")).Error
}

func (s *Store) DeleteRegistrationInvite(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Delete(&db.RegistrationInvite{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("invite not found")
	}
	return nil
}
