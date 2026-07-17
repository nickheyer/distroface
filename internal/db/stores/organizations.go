package stores

import (
	"context"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/pagination"
	"gorm.io/gorm"
)

// ── Organization operations ──────────────────────────────────────────────

func (s *Store) CreateOrganization(ctx context.Context, org *db.Organization) error {
	if org.ID == "" {
		org.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(org).Error
}

func (s *Store) GetOrganization(ctx context.Context, name string) (*db.Organization, error) {
	var org db.Organization
	err := s.db.WithContext(ctx).First(&org, "name = ?", name).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

func (s *Store) GetOrganizationByID(ctx context.Context, id string) (*db.Organization, error) {
	var org db.Organization
	err := s.db.WithContext(ctx).First(&org, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

// ListOrganizations returns organizations with visibility filtering.
// If canManage is true, all organizations are returned.
// Otherwise, only organizations the user is a member of are returned.
// OrgsQuery allowlists organization list filters
var OrgsQuery = pagination.Spec{
	Fields: map[string]string{
		"name":         "name",
		"display_name": "display_name",
		"description":  "description",
	},
	Text: []string{"name", "display_name"},
}

func (s *Store) ListOrganizations(ctx context.Context, userID string, canManage bool, q pagination.Query, limit, offset int) ([]*db.Organization, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Organization{}).Scopes(OrgsQuery.Scope(q))

	if !canManage && userID != "" {
		tx = tx.Where("id IN (SELECT org_id FROM org_members WHERE user_id = ?)", userID)
	} else if !canManage {
		// Anonymous or no user - return nothing
		return nil, 0, nil
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var orgs []*db.Organization
	err := tx.Order("name ASC").Limit(limit).Offset(offset).Find(&orgs).Error
	return orgs, total, err
}

func (s *Store) UpdateOrganization(ctx context.Context, org *db.Organization) error {
	return s.db.WithContext(ctx).Save(org).Error
}

func (s *Store) DeleteOrganization(ctx context.Context, id, orgName string) ([]db.Repository, error) {
	var deletedRepos []db.Repository
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Find repos under this org namespace before deleting
		if err := tx.Where("namespace = ?", orgName).Find(&deletedRepos).Error; err != nil {
			return err
		}
		// Delete repos
		if err := tx.Where("namespace = ?", orgName).Delete(&db.Repository{}).Error; err != nil {
			return err
		}
		// Delete members
		if err := tx.Where("org_id = ?", id).Delete(&db.OrgMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&db.Organization{}, "id = ?", id).Error
	})
	return deletedRepos, err
}

func (s *Store) GetOrgMemberCount(ctx context.Context, orgID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.OrgMember{}).Where("org_id = ?", orgID).Count(&count).Error
	return count, err
}

// ── OrgMember operations ─────────────────────────────────────────────────

func (s *Store) AddOrgMember(ctx context.Context, member *db.OrgMember) error {
	if member.ID == "" {
		member.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(member).Error
}

func (s *Store) GetOrgMember(ctx context.Context, orgID, userID string) (*db.OrgMember, error) {
	var member db.OrgMember
	err := s.db.WithContext(ctx).First(&member, "org_id = ? AND user_id = ?", orgID, userID).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &member, nil
}

// OrgMembersQuery allowlists org member list filters
var OrgMembersQuery = pagination.Spec{
	Fields: map[string]string{
		"username": "users.username",
		"role":     "org_members.role",
	},
	Text: []string{"users.username"},
}

func (s *Store) ListOrgMembers(ctx context.Context, orgID string, q pagination.Query, limit, offset int) ([]*db.OrgMember, int64, error) {
	base := func() *gorm.DB {
		tx := s.db.WithContext(ctx).Model(&db.OrgMember{}).Where("org_members.org_id = ?", orgID)
		if !q.IsZero() {
			tx = tx.Joins("JOIN users ON users.id = org_members.user_id").
				Scopes(OrgMembersQuery.Scope(q))
		}
		return tx
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var members []*db.OrgMember
	err := base().Preload("User").Limit(limit).Offset(offset).Find(&members).Error
	return members, total, err
}

// Caller's role per org id, single query
func (s *Store) ListUserOrgRoles(ctx context.Context, userID string) (map[string]string, error) {
	var members []db.OrgMember
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&members).Error; err != nil {
		return nil, err
	}
	roles := make(map[string]string, len(members))
	for _, m := range members {
		roles[m.OrgID] = m.Role
	}
	return roles, nil
}

func (s *Store) CountOrgMembersByRole(ctx context.Context, orgID, role string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.OrgMember{}).
		Where("org_id = ? AND role = ?", orgID, role).
		Count(&count).Error
	return count, err
}

func (s *Store) UpdateOrgMember(ctx context.Context, member *db.OrgMember) error {
	return s.db.WithContext(ctx).Save(member).Error
}

func (s *Store) RemoveOrgMember(ctx context.Context, orgID, userID string) error {
	return s.db.WithContext(ctx).Where("org_id = ? AND user_id = ?", orgID, userID).Delete(&db.OrgMember{}).Error
}

// Memberships a user got from one source with orgs preloaded
func (s *Store) GetOrgMembershipsBySource(ctx context.Context, userID, source string) ([]*db.OrgMember, error) {
	var members []*db.OrgMember
	err := s.db.WithContext(ctx).Preload("Org").Where("user_id = ? AND source = ?", userID, source).Find(&members).Error
	return members, err
}

func (s *Store) IsOrgMember(ctx context.Context, orgName, userID string) (bool, string, error) {
	org, err := s.GetOrganization(ctx, orgName)
	if err != nil || org == nil {
		return false, "", err
	}
	member, err := s.GetOrgMember(ctx, org.ID, userID)
	if err != nil || member == nil {
		return false, "", err
	}
	return true, member.Role, nil
}
