package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/pagination"
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
var CertDomainsQuery = pagination.Spec{
	Fields: map[string]string{"domain": "domain"},
	Text:   []string{"domain"},
}

// Returns a page of domains preloaded with their owning org
func (s *Store) ListCertificateDomains(ctx context.Context, q pagination.Query, limit, offset int) ([]*db.CertificateDomain, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.CertificateDomain{}).Scopes(CertDomainsQuery.Scope(q))

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

func (s *Store) ListCertificateDomainsByOrg(ctx context.Context, orgID string, q pagination.Query, limit, offset int) ([]*db.CertificateDomain, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.CertificateDomain{}).
		Where("org_id = ?", orgID).
		Scopes(CertDomainsQuery.Scope(q))

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

// CertHostRow is a portal hostname or orphan domain for cert management
type CertHostRow struct {
	Hostname   string
	PortalID   string
	PortalName string
}

// CertHostsQuery allowlists certificate host list filters
var CertHostsQuery = pagination.Spec{
	Fields: map[string]string{
		"hostname":    "hostname",
		"portal_name": "portal_name",
	},
	Text: []string{"hostname", "portal_name"},
}

// Org portal hostnames unioned with orphan cert domains, paged and ordered
func (s *Store) ListCertificateHosts(ctx context.Context, orgID string, q pagination.Query, limit, offset int) ([]CertHostRow, int64, error) {
	const union = `
		SELECT p.hostname AS hostname, p.id AS portal_id, p.name AS portal_name, 0 AS sort_group
		FROM registry_portals p
		WHERE p.org_id = ? AND p.hostname <> ''
		UNION
		SELECT d.domain AS hostname, '' AS portal_id, '' AS portal_name, 1 AS sort_group
		FROM certificate_domains d
		WHERE d.org_id = ? AND d.domain NOT IN (
			SELECT hostname FROM registry_portals WHERE org_id = ? AND hostname <> ''
		)`

	where := ""
	unionArgs := []any{orgID, orgID, orgID}
	if cond, condArgs := CertHostsQuery.SQL(q); cond != "" {
		where = " WHERE " + cond
		unionArgs = append(unionArgs, condArgs...)
	}

	var total int64
	if err := s.db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM ("+union+") AS hosts"+where, unionArgs...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	sel := "SELECT hostname, portal_id, portal_name FROM (" + union + ") AS hosts" + where +
		" ORDER BY sort_group ASC, hostname ASC"
	args := append([]any{}, unionArgs...)
	if limit > 0 {
		sel += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	var rows []CertHostRow
	if err := s.db.WithContext(ctx).Raw(sel, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Org cert domains matching the given hostnames
func (s *Store) GetCertificateDomainsByNames(ctx context.Context, orgID string, names []string) ([]*db.CertificateDomain, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var domains []*db.CertificateDomain
	err := s.db.WithContext(ctx).
		Where("org_id = ? AND domain IN ?", orgID, names).
		Find(&domains).Error
	return domains, err
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
var AuditQuery = pagination.Spec{
	Fields: map[string]string{
		"action":    "action",
		"actor":     "actor",
		"outcome":   "outcome",
		"resource":  "resource",
		"source_ip": "source_ip",
	},
	Text: []string{"action", "actor", "resource"},
}

// Newest first
func (s *Store) ListAuditEvents(ctx context.Context, q pagination.Query, limit, offset int) ([]*db.AuditEvent, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.AuditEvent{}).Scopes(AuditQuery.Scope(q))
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var events []*db.AuditEvent
	err := tx.Order("created_at DESC").Limit(limit).Offset(offset).Find(&events).Error
	return events, total, err
}

func (s *Store) DeleteAuditEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Delete(&db.AuditEvent{}, "created_at < ?", cutoff)
	return res.RowsAffected, res.Error
}
