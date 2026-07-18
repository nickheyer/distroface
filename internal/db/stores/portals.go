package stores

import (
	"context"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

// ── RegistryPortal operations ────────────────────────────────────────────

func (s *Store) CreateRegistryPortal(ctx context.Context, portal *db.RegistryPortal) error {
	if portal.ID == "" {
		portal.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(portal).Error
}

func (s *Store) GetRegistryPortal(ctx context.Context, id string) (*db.RegistryPortal, error) {
	var portal db.RegistryPortal
	err := s.db.WithContext(ctx).First(&portal, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &portal, nil
}

func (s *Store) ListRegistryPortalsByOrg(ctx context.Context, orgID string) ([]*db.RegistryPortal, error) {
	var portals []*db.RegistryPortal
	err := s.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at ASC").Find(&portals).Error
	return portals, err
}

// PortalsQuery allowlists registry portal list filters
var PortalsQuery = pages.Spec{
	Fields: map[string]string{
		"name":     "name",
		"hostname": "hostname",
	},
	Text: []string{"name", "hostname"},
}

// Paged variant with total for the rpc listing
func (s *Store) ListRegistryPortalsByOrgPaged(ctx context.Context, orgID string, q pages.Query, limit, offset int) ([]*db.RegistryPortal, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.RegistryPortal{}).
		Where("org_id = ?", orgID).
		Scopes(PortalsQuery.Scope(q))

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	var portals []*db.RegistryPortal
	err := tx.Order("created_at ASC").Find(&portals).Error
	return portals, total, err
}

// Returns every portal preloaded with its owning org
func (s *Store) ListRegistryPortals(ctx context.Context) ([]*db.RegistryPortal, error) {
	var portals []*db.RegistryPortal
	err := s.db.WithContext(ctx).Preload("Org").Find(&portals).Error
	return portals, err
}

// Enabled portals with the hostname on any port
func (s *Store) ListPortalsByHostname(ctx context.Context, hostname string) ([]*db.RegistryPortal, error) {
	var portals []*db.RegistryPortal
	err := s.db.WithContext(ctx).Where("hostname = ? AND enabled = ?", hostname, true).Find(&portals).Error
	return portals, err
}

// Enabled portal answering host on the listener port, exact hostname
// wins over a port only portal, exact port wins over the app port
func (s *Store) GetPortalForHost(ctx context.Context, hostname string, port int) (*db.RegistryPortal, error) {
	var portals []*db.RegistryPortal
	err := s.db.WithContext(ctx).
		Where("hostname IN ? AND enabled = ? AND port IN ?", []string{hostname, ""}, true, []int{port, 0}).
		Find(&portals).Error
	if err != nil || len(portals) == 0 {
		return nil, err
	}
	score := func(p *db.RegistryPortal) int {
		n := 0
		if p.Hostname == hostname {
			n += 2
		}
		if p.Port == port {
			n++
		}
		return n
	}
	best := portals[0]
	for _, p := range portals[1:] {
		if score(p) > score(best) {
			best = p
		}
	}
	return best, nil
}

func (s *Store) UpdateRegistryPortal(ctx context.Context, portal *db.RegistryPortal) error {
	return s.db.WithContext(ctx).Save(portal).Error
}

func (s *Store) DeleteRegistryPortal(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.RegistryPortal{}, "id = ?", id).Error
}
