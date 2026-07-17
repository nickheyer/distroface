package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── Certificate domain operations ────────────────────────────────────────

func (s *Store) CreateCertificateDomain(ctx context.Context, d *db.CertificateDomain) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(d).Error
}

func (s *Store) GetCertificateDomain(ctx context.Context, id string) (*db.CertificateDomain, error) {
	var d db.CertificateDomain
	err := s.db.WithContext(ctx).Preload("Org").First(&d, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) UpdateCertificateDomain(ctx context.Context, d *db.CertificateDomain) error {
	return s.db.WithContext(ctx).Omit(clause.Associations).Save(d).Error
}

func (s *Store) GetCertificateDomainByName(ctx context.Context, domain string) (*db.CertificateDomain, error) {
	var d db.CertificateDomain
	err := s.db.WithContext(ctx).First(&d, "domain = ?", domain).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// Returns every domain preloaded with its owning org
func (s *Store) ListCertificateDomains(ctx context.Context) ([]*db.CertificateDomain, error) {
	var domains []*db.CertificateDomain
	err := s.db.WithContext(ctx).Preload("Org").Order("created_at ASC").Find(&domains).Error
	return domains, err
}

func (s *Store) ListCertificateDomainsByOrg(ctx context.Context, orgID string) ([]*db.CertificateDomain, error) {
	var domains []*db.CertificateDomain
	err := s.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at ASC").Find(&domains).Error
	return domains, err
}

func (s *Store) DeleteCertificateDomain(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.CertificateDomain{}, "id = ?", id).Error
}

// ── ACME cache operations ────────────────────────────────────────────────

// Nil data means miss
func (s *Store) GetACMECacheEntry(ctx context.Context, key string) ([]byte, error) {
	var entry db.ACMECacheEntry
	err := s.db.WithContext(ctx).First(&entry, "key = ?", key).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return entry.Data, nil
}

func (s *Store) PutACMECacheEntry(ctx context.Context, key string, data []byte) error {
	entry := db.ACMECacheEntry{Key: key, Data: data}
	return s.db.WithContext(ctx).Save(&entry).Error
}

func (s *Store) DeleteACMECacheEntry(ctx context.Context, key string) error {
	return s.db.WithContext(ctx).Delete(&db.ACMECacheEntry{}, "key = ?", key).Error
}

// ── Audit event operations ───────────────────────────────────────────────

func (s *Store) CreateAuditEvent(ctx context.Context, ev *db.AuditEvent) error {
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(ev).Error
}

// Newest first, action/actor filters optional
func (s *Store) ListAuditEvents(ctx context.Context, action, actor string, limit, offset int) ([]*db.AuditEvent, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.AuditEvent{})
	if action != "" {
		q = q.Where("action = ?", action)
	}
	if actor != "" {
		q = q.Where("actor = ?", actor)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var events []*db.AuditEvent
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&events).Error
	return events, total, err
}

func (s *Store) DeleteAuditEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Delete(&db.AuditEvent{}, "created_at < ?", cutoff)
	return res.RowsAffected, res.Error
}
