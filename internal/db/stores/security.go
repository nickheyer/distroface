package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
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

// CertDomainsQuery allowlists certificate domain list filters
var CertDomainsQuery = pages.Spec{
	Fields: map[string]string{"domain": "domain"},
	Text:   []string{"domain"},
}

// Returns a page of domains preloaded with their owning org,
// empty scope lists both scopes
func (s *Store) ListCertificateDomains(ctx context.Context, q pages.Query, scope string, pendingOnly bool, limit, offset int) ([]*db.CertificateDomain, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.CertificateDomain{}).Scopes(CertDomainsQuery.Scope(q))
	if scope != "" {
		tx = tx.Where("scope = ?", scope)
	}
	if pendingOnly {
		tx = tx.Where("approved = ?", false)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	var domains []*db.CertificateDomain
	err := tx.Preload("Org").Order("created_at ASC").Find(&domains).Error
	return domains, total, err
}

func (s *Store) ListCertificateDomainsByOrg(ctx context.Context, orgID string, q pages.Query, pendingOnly bool, limit, offset int) ([]*db.CertificateDomain, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.CertificateDomain{}).
		Where("org_id = ?", orgID).
		Scopes(CertDomainsQuery.Scope(q))
	if pendingOnly {
		tx = tx.Where("approved = ?", false)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	var domains []*db.CertificateDomain
	err := tx.Order("created_at ASC").Find(&domains).Error
	return domains, total, err
}

func (s *Store) DeleteCertificateDomain(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.CertificateDomain{}, "id = ?", id).Error
}

func (s *Store) GetCertificateDomainsByIDs(ctx context.Context, ids []string) ([]*db.CertificateDomain, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var domains []*db.CertificateDomain
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&domains).Error
	return domains, err
}

func (s *Store) DeleteCertificateDomains(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Delete(&db.CertificateDomain{}, "id IN ?", ids).Error
}

// ── Uploaded TLS material operations ─────────────────────────────────────

// Nil when no material stored for the target
func (s *Store) GetTLSCertificate(ctx context.Context, scope, orgID, portalID string) (*db.TLSCertificate, error) {
	var cert db.TLSCertificate
	err := s.db.WithContext(ctx).
		First(&cert, "scope = ? AND org_id = ? AND portal_id = ?", scope, orgID, portalID).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cert, nil
}

// Upserts by scope target
func (s *Store) SaveTLSCertificate(ctx context.Context, cert *db.TLSCertificate) error {
	existing, err := s.GetTLSCertificate(ctx, cert.Scope, cert.OrgID, cert.PortalID)
	if err != nil {
		return err
	}
	if existing != nil {
		cert.ID = existing.ID
		cert.CreatedAt = existing.CreatedAt
		return s.db.WithContext(ctx).Save(cert).Error
	}
	if cert.ID == "" {
		cert.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(cert).Error
}

func (s *Store) DeleteTLSCertificate(ctx context.Context, scope, orgID, portalID string) error {
	return s.db.WithContext(ctx).
		Delete(&db.TLSCertificate{}, "scope = ? AND org_id = ? AND portal_id = ?", scope, orgID, portalID).Error
}

func (s *Store) DeleteTLSCertificatesByPortal(ctx context.Context, portalID string) error {
	return s.db.WithContext(ctx).Delete(&db.TLSCertificate{}, "portal_id = ?", portalID).Error
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

// AuditQuery allowlists audit event list filters
var AuditQuery = pages.Spec{
	Fields: map[string]string{
		"action":    "action",
		"actor":     "actor",
		"outcome":   "outcome",
		"resource":  "resource",
		"source_ip": "source_ip",
	},
	Text: []string{"action", "actor", "resource"},
}

var AuditSortColumns = map[string]bool{"created_at": true}

func (s *Store) ListAuditEvents(ctx context.Context, q pages.Query, orderBy string, limit, offset int) ([]*db.AuditEvent, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.AuditEvent{}).Scopes(AuditQuery.Scope(q))
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var events []*db.AuditEvent
	err := tx.Order(orderBy).Limit(limit).Offset(offset).Find(&events).Error
	return events, total, err
}

func (s *Store) DeleteAuditEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Delete(&db.AuditEvent{}, "created_at < ?", cutoff)
	return res.RowsAffected, res.Error
}
