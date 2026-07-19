package stores

import (
	"context"

	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Nil when no account exists for the thumbprint
func (s *Store) GetACMEAccount(ctx context.Context, id string) (*db.ACMEAccount, error) {
	var acct db.ACMEAccount
	err := s.db.WithContext(ctx).First(&acct, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &acct, nil
}

func (s *Store) CreateACMEAccount(ctx context.Context, acct *db.ACMEAccount) error {
	return s.db.WithContext(ctx).Create(acct).Error
}
