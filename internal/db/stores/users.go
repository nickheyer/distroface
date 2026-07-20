package stores

import (
	"context"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

// ── User operations ──────────────────────────────────────────────────────

func (s *Store) CreateUser(ctx context.Context, user *db.User) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(user).Error
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*db.User, error) {
	var user db.User
	err := s.db.WithContext(ctx).First(&user, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*db.User, error) {
	var user db.User
	err := s.db.WithContext(ctx).First(&user, "username = ?", username).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByUsernameAndProvider(ctx context.Context, username, provider string) (*db.User, error) {
	var user db.User
	err := s.db.WithContext(ctx).First(&user, "username = ? AND auth_provider = ?", username, provider).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*db.User, error) {
	var user db.User
	err := s.db.WithContext(ctx).First(&user, "email = ?", email).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByIdentifier(ctx context.Context, identifier string) (*db.User, error) {
	var user db.User
	err := s.db.WithContext(ctx).First(&user, "username = ? OR email = ?", identifier, identifier).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// Subjects are only unique per issuer
func (s *Store) GetUserByOIDCSubject(ctx context.Context, subject, issuer string) (*db.User, error) {
	var user db.User
	err := s.db.WithContext(ctx).First(&user, "oidc_subject = ? AND oidc_issuer = ?", subject, issuer).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// UsersQuery allowlists user list filters
var UsersQuery = pages.Spec{
	Fields: map[string]string{
		"username":      "username",
		"email":         "email",
		"display_name":  "display_name",
		"auth_provider": "auth_provider",
	},
	Text: []string{"username", "email", "display_name"},
}

func (s *Store) ListUsers(ctx context.Context, q pages.Query, limit, offset int) ([]*db.User, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.User{}).Scopes(UsersQuery.Scope(q))

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []*db.User
	err := tx.Order("created_at DESC").Limit(limit).Offset(offset).Find(&users).Error
	return users, total, err
}

func (s *Store) UpdateUser(ctx context.Context, user *db.User) error {
	return s.db.WithContext(ctx).Save(user).Error
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&db.Session{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&db.APIToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&db.UserRole{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&db.OrgMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&db.User{}, "id = ?", id).Error
	})
}

// Removes users and their dependent rows in one transaction
func (s *Store) BulkDeleteUsers(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{&db.Session{}, &db.APIToken{}, &db.UserRole{}, &db.OrgMember{}} {
			if err := tx.Where("user_id IN ?", ids).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&db.User{}, "id IN ?", ids).Error
	})
}

func (s *Store) BulkSetUsersActive(ctx context.Context, ids []string, active bool) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&db.User{}).Where("id IN ?", ids).Update("is_active", active).Error
}

// Existing subset of the requested ids
func (s *Store) FilterExistingUserIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}
	var found []string
	err := s.db.WithContext(ctx).Model(&db.User{}).Where("id IN ?", ids).Pluck("id", &found).Error
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(found))
	for _, id := range found {
		existing[id] = true
	}
	return existing, nil
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.User{}).Count(&count).Error
	return count, err
}
