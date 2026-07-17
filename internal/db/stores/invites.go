package stores

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/pagination"
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

// InvitesQuery allowlists registration invite list filters
var InvitesQuery = pagination.Spec{
	Fields: map[string]string{
		"code":        "code",
		"description": "description",
		"created_by":  "created_by",
	},
	Text: []string{"code", "description"},
}

func (s *Store) ListRegistrationInvites(ctx context.Context, q pagination.Query, limit, offset int) ([]*db.RegistrationInvite, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.RegistrationInvite{}).Scopes(InvitesQuery.Scope(q))

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

// Existing subset of the requested ids
func (s *Store) FilterExistingInviteIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}
	var found []string
	err := s.db.WithContext(ctx).Model(&db.RegistrationInvite{}).Where("id IN ?", ids).Pluck("id", &found).Error
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(found))
	for _, id := range found {
		existing[id] = true
	}
	return existing, nil
}

// Returns how many of the requested invites were removed
func (s *Store) BulkDeleteRegistrationInvites(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	result := s.db.WithContext(ctx).Delete(&db.RegistrationInvite{}, "id IN ?", ids)
	return result.RowsAffected, result.Error
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
