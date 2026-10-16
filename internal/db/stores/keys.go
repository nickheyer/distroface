package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

func (s *Store) CreateSSHKey(ctx context.Context, k *db.SSHKey) error {
	if k.ID == "" {
		k.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(k).Error
}

func (s *Store) GetSSHKey(ctx context.Context, id string) (*db.SSHKey, error) {
	var k db.SSHKey
	err := s.db.WithContext(ctx).First(&k, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &k, err
}

func (s *Store) GetSSHKeyByFingerprint(ctx context.Context, fp string) (*db.SSHKey, error) {
	var k db.SSHKey
	err := s.db.WithContext(ctx).First(&k, "fingerprint = ?", fp).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &k, err
}

func (s *Store) ListSSHKeys(ctx context.Context, ownerType, ownerID string, limit, offset int) ([]*db.SSHKey, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.SSHKey{}).Where("owner_type = ? AND owner_id = ?", ownerType, ownerID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.SSHKey
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

// Signing capable keys of a user
func (s *Store) ListUserSigningKeys(ctx context.Context, userID string) ([]*db.SSHKey, error) {
	var out []*db.SSHKey
	err := s.db.WithContext(ctx).Where("owner_type = ? AND owner_id = ? AND usage IN ?", db.SSHKeyOwnerUser, userID, []string{"signing", "both"}).Find(&out).Error
	return out, err
}

func (s *Store) TouchSSHKey(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.SSHKey{}).Where("id = ?", id).Update("last_used_at", time.Now().UTC()).Error
}

func (s *Store) DeleteSSHKey(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.SSHKey{}, "id = ?", id).Error
}

func (s *Store) DeleteSSHKeysByOwner(ctx context.Context, ownerType, ownerID string) error {
	return s.db.WithContext(ctx).Where("owner_type = ? AND owner_id = ?", ownerType, ownerID).Delete(&db.SSHKey{}).Error
}

func (s *Store) CreateGPGKey(ctx context.Context, k *db.GPGKey) error {
	if k.ID == "" {
		k.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(k).Error
}

func (s *Store) GetGPGKey(ctx context.Context, userID, id string) (*db.GPGKey, error) {
	var k db.GPGKey
	err := s.db.WithContext(ctx).First(&k, "user_id = ? AND id = ?", userID, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &k, err
}

func (s *Store) GetGPGKeyByKeyID(ctx context.Context, keyID string) (*db.GPGKey, error) {
	var k db.GPGKey
	err := s.db.WithContext(ctx).First(&k, "key_id = ? OR fingerprint = ?", keyID, keyID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &k, err
}

func (s *Store) ListGPGKeys(ctx context.Context, userID string, limit, offset int) ([]*db.GPGKey, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GPGKey{}).Where("user_id = ?", userID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GPGKey
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

func (s *Store) ListAllGPGKeys(ctx context.Context) ([]*db.GPGKey, error) {
	var out []*db.GPGKey
	err := s.db.WithContext(ctx).Find(&out).Error
	return out, err
}

func (s *Store) UpdateGPGKey(ctx context.Context, k *db.GPGKey) error {
	return s.db.WithContext(ctx).Save(k).Error
}

func (s *Store) DeleteGPGKey(ctx context.Context, userID, id string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).Delete(&db.GPGKey{}).Error
}
