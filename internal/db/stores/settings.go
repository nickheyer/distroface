package stores

import (
	"context"

	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── SystemSetting operations ─────────────────────────────────────────────

func (s *Store) GetSystemSetting(ctx context.Context, key string) (string, error) {
	var setting db.SystemSetting
	err := s.db.WithContext(ctx).First(&setting, "key = ?", key).Error
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *Store) SetSystemSetting(ctx context.Context, key, value string) error {
	setting := db.SystemSetting{Key: key, Value: value}
	return s.db.WithContext(ctx).Save(&setting).Error
}

// ── Org setting operations ───────────────────────────────────────────────

func (s *Store) GetOrgSetting(ctx context.Context, orgID, key string) (string, bool, error) {
	var setting db.OrgSetting
	err := s.db.WithContext(ctx).First(&setting, "org_id = ? AND key = ?", orgID, key).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return setting.Value, true, nil
}

func (s *Store) SetOrgSetting(ctx context.Context, orgID, key, value string) error {
	setting := db.OrgSetting{OrgID: orgID, Key: key, Value: value}
	return s.db.WithContext(ctx).Save(&setting).Error
}

func (s *Store) ListOrgSettings(ctx context.Context, orgID string) (map[string]string, error) {
	var rows []db.OrgSetting
	if err := s.db.WithContext(ctx).Where("org_id = ?", orgID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

func (s *Store) DeleteOrgSetting(ctx context.Context, orgID, key string) error {
	return s.db.WithContext(ctx).Delete(&db.OrgSetting{}, "org_id = ? AND key = ?", orgID, key).Error
}
