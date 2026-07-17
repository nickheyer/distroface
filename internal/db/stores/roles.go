package stores

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/pagination"
	"gorm.io/gorm"
)

// ── Role operations ──────────────────────────────────────────────────────

func (s *Store) SeedSystemRoles() error {
	roles := []db.Role{
		{ID: "role-admin", Name: "admin", Description: "Full system access", IsSystem: true},
		{ID: "role-user", Name: "user", Description: "Standard user access", IsSystem: true, IsDefault: true},
		{ID: "role-anonymous", Name: "anonymous", Description: "Unauthenticated user access", IsSystem: true},
	}
	for _, role := range roles {
		var existing db.Role
		if err := s.db.Where("name = ?", role.Name).First(&existing).Error; err == gorm.ErrRecordNotFound {
			if err := s.db.Create(&role).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) CreateRole(ctx context.Context, role *db.Role) error {
	if role.ID == "" {
		role.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(role).Error
}

func (s *Store) GetRole(ctx context.Context, id string) (*db.Role, error) {
	var role db.Role
	err := s.db.WithContext(ctx).First(&role, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &role, nil
}

func (s *Store) GetRoleByName(ctx context.Context, name string) (*db.Role, error) {
	var role db.Role
	err := s.db.WithContext(ctx).First(&role, "name = ?", name).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &role, nil
}

// RolesQuery allowlists role list filters
var RolesQuery = pagination.Spec{
	Fields: map[string]string{
		"name":        "name",
		"description": "description",
	},
	Text: []string{"name", "description"},
}

func (s *Store) ListRoles(ctx context.Context, q pagination.Query, limit, offset int) ([]*db.Role, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Role{}).Scopes(RolesQuery.Scope(q))

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	var roles []*db.Role
	err := tx.Order("is_system DESC, name ASC").Find(&roles).Error
	return roles, total, err
}

func (s *Store) GetDefaultRoles(ctx context.Context) ([]*db.Role, error) {
	var roles []*db.Role
	err := s.db.WithContext(ctx).Where("is_default = ?", true).Find(&roles).Error
	return roles, err
}

func (s *Store) UpdateRole(ctx context.Context, role *db.Role) error {
	return s.db.WithContext(ctx).Save(role).Error
}

// Rename role and repoint user rows one transaction
func (s *Store) RenameRole(ctx context.Context, role *db.Role, oldName string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(role).Error; err != nil {
			return err
		}
		return tx.Model(&db.UserRole{}).Where("role_name = ?", oldName).Update("role_name", role.Name).Error
	})
}

func (s *Store) DeleteRole(ctx context.Context, id string) error {
	var role db.Role
	if err := s.db.WithContext(ctx).First(&role, "id = ?", id).Error; err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("cannot delete system role")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_name = ?", role.Name).Delete(&db.UserRole{}).Error; err != nil {
			return err
		}
		return tx.Delete(&db.Role{}, "id = ?", id).Error
	})
}

func (s *Store) AssignRole(ctx context.Context, userID, roleName, source string) error {
	role, err := s.GetRoleByName(ctx, roleName)
	if err != nil {
		return err
	}
	if role == nil {
		return fmt.Errorf("role %q does not exist", roleName)
	}

	var existing db.UserRole
	err = s.db.WithContext(ctx).Where("user_id = ? AND role_name = ?", userID, roleName).First(&existing).Error
	if err == nil {
		return nil // Already assigned
	}
	ur := &db.UserRole{
		ID:       uuid.New().String(),
		UserID:   userID,
		RoleName: roleName,
		Source:   source,
	}
	return s.db.WithContext(ctx).Create(ur).Error
}

func (s *Store) UnassignRole(ctx context.Context, userID, roleName string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND role_name = ?", userID, roleName).Delete(&db.UserRole{}).Error
}

// Remove role only when granted by this source
func (s *Store) UnassignRoleBySource(ctx context.Context, userID, roleName, source string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND role_name = ? AND source = ?", userID, roleName, source).Delete(&db.UserRole{}).Error
}

// Role names user got from one source
func (s *Store) GetUserRoleNamesBySource(ctx context.Context, userID, source string) ([]string, error) {
	var names []string
	err := s.db.WithContext(ctx).
		Model(&db.UserRole{}).
		Where("user_id = ? AND source = ?", userID, source).
		Pluck("role_name", &names).Error
	if err != nil {
		return nil, err
	}
	return names, nil
}

func (s *Store) GetUserRoleNames(ctx context.Context, userID string) ([]string, error) {
	var names []string
	err := s.db.WithContext(ctx).
		Model(&db.UserRole{}).
		Where("user_id = ?", userID).
		Pluck("role_name", &names).Error
	if err != nil {
		return nil, err
	}
	return names, nil
}

// Role rows matching the given ids
func (s *Store) GetRolesByIDs(ctx context.Context, ids []string) ([]*db.Role, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var roles []*db.Role
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Order("name ASC").Find(&roles).Error
	return roles, err
}

// Full role rows a user holds, joined through assignments
func (s *Store) GetUserRoles(ctx context.Context, userID string) ([]*db.Role, error) {
	var roles []*db.Role
	err := s.db.WithContext(ctx).
		Model(&db.Role{}).
		Joins("JOIN user_roles ON user_roles.role_name = roles.name").
		Where("user_roles.user_id = ?", userID).
		Order("roles.name ASC").
		Find(&roles).Error
	return roles, err
}
