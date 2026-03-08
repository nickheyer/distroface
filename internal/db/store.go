package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DBConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type Store struct {
	db *gorm.DB
}

func NewSQLiteStore(dbPath string, config ...DBConfig) (*Store, error) {
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database handle: %w", err)
	}

	if len(config) > 0 {
		cfg := config[0]
		if cfg.MaxOpenConns > 0 {
			sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
		}
		if cfg.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
		}
		if cfg.ConnMaxLifetime > 0 {
			sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
		}
	}

	store := &Store{db: db}

	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return store, nil
}

func (s *Store) DB() *gorm.DB {
	return s.db
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *Store) Migrate() error {
	if err := s.db.AutoMigrate(
		&User{},
		&Role{},
		&UserRole{},
		&Session{},
		&APIToken{},
		&Organization{},
		&OrgMember{},
		&Repository{},
		&SystemSetting{},
		&RegistrationInvite{},
		&Webhook{},
		&WebhookDelivery{},
	); err != nil {
		return fmt.Errorf("failed to auto-migrate: %w", err)
	}

	// Drop old indexes that conflict with new schema
	s.db.Exec("DROP INDEX IF EXISTS idx_users_username")
	s.db.Exec("DROP INDEX IF EXISTS idx_users_email")
	s.db.Exec("DROP INDEX IF EXISTS uni_users_username")
	s.db.Exec("DROP INDEX IF EXISTS uni_users_email")

	if err := s.SeedSystemRoles(); err != nil {
		return fmt.Errorf("failed to seed system roles: %w", err)
	}

	return nil
}

// ── User operations ──────────────────────────────────────────────────────

func (s *Store) CreateUser(ctx context.Context, user *User) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(user).Error
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	var user User
	err := s.db.WithContext(ctx).First(&user, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	var user User
	err := s.db.WithContext(ctx).First(&user, "username = ?", username).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByUsernameAndProvider(ctx context.Context, username, provider string) (*User, error) {
	var user User
	err := s.db.WithContext(ctx).First(&user, "username = ? AND auth_provider = ?", username, provider).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	err := s.db.WithContext(ctx).First(&user, "email = ?", email).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByIdentifier(ctx context.Context, identifier string) (*User, error) {
	var user User
	err := s.db.WithContext(ctx).First(&user, "username = ? OR email = ?", identifier, identifier).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByOIDCSubject(ctx context.Context, subject string) (*User, error) {
	var user User
	err := s.db.WithContext(ctx).First(&user, "oidc_subject = ?", subject).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Store) ListUsers(ctx context.Context, query string, limit, offset int) ([]*User, int64, error) {
	tx := s.db.WithContext(ctx).Model(&User{})

	if query != "" {
		tx = tx.Where("username LIKE ?", query+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []*User
	err := tx.Order("created_at DESC").Limit(limit).Offset(offset).Find(&users).Error
	return users, total, err
}

func (s *Store) UpdateUser(ctx context.Context, user *User) error {
	return s.db.WithContext(ctx).Save(user).Error
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&Session{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&APIToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&UserRole{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&OrgMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&User{}, "id = ?", id).Error
	})
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&User{}).Count(&count).Error
	return count, err
}

// ── Role operations ──────────────────────────────────────────────────────

func (s *Store) SeedSystemRoles() error {
	roles := []Role{
		{ID: "role-admin", Name: "admin", Description: "Full system access", IsSystem: true},
		{ID: "role-user", Name: "user", Description: "Standard user access", IsSystem: true, IsDefault: true},
		{ID: "role-anonymous", Name: "anonymous", Description: "Unauthenticated user access", IsSystem: true},
	}
	for _, role := range roles {
		var existing Role
		if err := s.db.Where("name = ?", role.Name).First(&existing).Error; err == gorm.ErrRecordNotFound {
			if err := s.db.Create(&role).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) CreateRole(ctx context.Context, role *Role) error {
	if role.ID == "" {
		role.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(role).Error
}

func (s *Store) GetRole(ctx context.Context, id string) (*Role, error) {
	var role Role
	err := s.db.WithContext(ctx).First(&role, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &role, nil
}

func (s *Store) GetRoleByName(ctx context.Context, name string) (*Role, error) {
	var role Role
	err := s.db.WithContext(ctx).First(&role, "name = ?", name).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &role, nil
}

func (s *Store) ListRoles(ctx context.Context) ([]*Role, error) {
	var roles []*Role
	err := s.db.WithContext(ctx).Order("is_system DESC, name ASC").Find(&roles).Error
	return roles, err
}

func (s *Store) GetDefaultRoles(ctx context.Context) ([]*Role, error) {
	var roles []*Role
	err := s.db.WithContext(ctx).Where("is_default = ?", true).Find(&roles).Error
	return roles, err
}

func (s *Store) UpdateRole(ctx context.Context, role *Role) error {
	return s.db.WithContext(ctx).Save(role).Error
}

func (s *Store) DeleteRole(ctx context.Context, id string) error {
	var role Role
	if err := s.db.WithContext(ctx).First(&role, "id = ?", id).Error; err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("cannot delete system role")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_name = ?", role.Name).Delete(&UserRole{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Role{}, "id = ?", id).Error
	})
}

// ── UserRole operations ──────────────────────────────────────────────────

func (s *Store) AssignRole(ctx context.Context, userID, roleName, source string) error {
	var existing UserRole
	err := s.db.WithContext(ctx).Where("user_id = ? AND role_name = ?", userID, roleName).First(&existing).Error
	if err == nil {
		return nil // Already assigned
	}
	ur := &UserRole{
		ID:       uuid.New().String(),
		UserID:   userID,
		RoleName: roleName,
		Source:   source,
	}
	return s.db.WithContext(ctx).Create(ur).Error
}

func (s *Store) UnassignRole(ctx context.Context, userID, roleName string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND role_name = ?", userID, roleName).Delete(&UserRole{}).Error
}

func (s *Store) GetUserRoleNames(ctx context.Context, userID string) ([]string, error) {
	var names []string
	err := s.db.WithContext(ctx).
		Model(&UserRole{}).
		Where("user_id = ?", userID).
		Pluck("role_name", &names).Error
	if err != nil {
		return nil, err
	}
	return names, nil
}

// ── Session operations ───────────────────────────────────────────────────

func (s *Store) CreateSession(ctx context.Context, session *Session) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(session).Error
}

func (s *Store) GetSession(ctx context.Context, token string) (*Session, error) {
	var session Session
	err := s.db.WithContext(ctx).Preload("User").Where("token = ? AND expires_at > ?", token, time.Now()).First(&session).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	var session Session
	err := s.db.WithContext(ctx).Preload("User").First(&session, "token = ?", tokenHash).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return s.db.WithContext(ctx).Where("token = ?", token).Delete(&Session{}).Error
}

func (s *Store) DeleteSessionByID(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&Session{}, "id = ?", id).Error
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	return s.db.WithContext(ctx).Delete(&Session{}, "expires_at < ?", time.Now().UTC()).Error
}

func (s *Store) CleanAllSessions(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("1 = 1").Delete(&Session{}).Error
}

func (s *Store) UpdateSession(ctx context.Context, session *Session) error {
	return s.db.WithContext(ctx).Save(session).Error
}

// ── APIToken operations ──────────────────────────────────────────────────

func (s *Store) CreateAPIToken(ctx context.Context, token *APIToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(token).Error
}

func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	var token APIToken
	err := s.db.WithContext(ctx).Where("token_hash = ?", hash).First(&token).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID string, limit, offset int) ([]*APIToken, int64, error) {
	tx := s.db.WithContext(ctx).Model(&APIToken{})
	if userID != "" {
		tx = tx.Where("user_id = ?", userID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := s.db.WithContext(ctx)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	var tokens []*APIToken
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&tokens).Error
	return tokens, total, err
}

func (s *Store) DeleteAPIToken(ctx context.Context, id, userID string) error {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	result := query.Delete(&APIToken{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("api token not found")
	}
	return nil
}

func (s *Store) UpdateAPITokenLastUsed(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&APIToken{}).Where("id = ?", id).Update("last_used_at", time.Now()).Error
}

// ── SystemSetting operations ─────────────────────────────────────────────

func (s *Store) GetSystemSetting(ctx context.Context, key string) (string, error) {
	var setting SystemSetting
	err := s.db.WithContext(ctx).First(&setting, "key = ?", key).Error
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *Store) SetSystemSetting(ctx context.Context, key, value string) error {
	setting := SystemSetting{Key: key, Value: value}
	return s.db.WithContext(ctx).Save(&setting).Error
}

// ── Organization operations ──────────────────────────────────────────────

func (s *Store) CreateOrganization(ctx context.Context, org *Organization) error {
	if org.ID == "" {
		org.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(org).Error
}

func (s *Store) GetOrganization(ctx context.Context, name string) (*Organization, error) {
	var org Organization
	err := s.db.WithContext(ctx).First(&org, "name = ?", name).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

func (s *Store) GetOrganizationByID(ctx context.Context, id string) (*Organization, error) {
	var org Organization
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
func (s *Store) ListOrganizations(ctx context.Context, userID string, canManage bool, limit, offset int) ([]*Organization, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Organization{})

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

	var orgs []*Organization
	err := tx.Order("name ASC").Limit(limit).Offset(offset).Find(&orgs).Error
	return orgs, total, err
}

func (s *Store) UpdateOrganization(ctx context.Context, org *Organization) error {
	return s.db.WithContext(ctx).Save(org).Error
}

func (s *Store) DeleteOrganization(ctx context.Context, id, orgName string) ([]Repository, error) {
	var deletedRepos []Repository
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Find repos under this org namespace before deleting
		if err := tx.Where("namespace = ?", orgName).Find(&deletedRepos).Error; err != nil {
			return err
		}
		// Delete repos
		if err := tx.Where("namespace = ?", orgName).Delete(&Repository{}).Error; err != nil {
			return err
		}
		// Delete members
		if err := tx.Where("org_id = ?", id).Delete(&OrgMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Organization{}, "id = ?", id).Error
	})
	return deletedRepos, err
}

func (s *Store) GetOrgMemberCount(ctx context.Context, orgID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&OrgMember{}).Where("org_id = ?", orgID).Count(&count).Error
	return count, err
}

// ── OrgMember operations ─────────────────────────────────────────────────

func (s *Store) AddOrgMember(ctx context.Context, member *OrgMember) error {
	if member.ID == "" {
		member.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(member).Error
}

func (s *Store) GetOrgMember(ctx context.Context, orgID, userID string) (*OrgMember, error) {
	var member OrgMember
	err := s.db.WithContext(ctx).First(&member, "org_id = ? AND user_id = ?", orgID, userID).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &member, nil
}

func (s *Store) ListOrgMembers(ctx context.Context, orgID string, limit, offset int) ([]*OrgMember, int64, error) {
	tx := s.db.WithContext(ctx).Model(&OrgMember{}).Where("org_id = ?", orgID)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var members []*OrgMember
	err := s.db.WithContext(ctx).Preload("User").Where("org_id = ?", orgID).Limit(limit).Offset(offset).Find(&members).Error
	return members, total, err
}

func (s *Store) UpdateOrgMember(ctx context.Context, member *OrgMember) error {
	return s.db.WithContext(ctx).Save(member).Error
}

func (s *Store) RemoveOrgMember(ctx context.Context, orgID, userID string) error {
	return s.db.WithContext(ctx).Where("org_id = ? AND user_id = ?", orgID, userID).Delete(&OrgMember{}).Error
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

// ── Repository operations ────────────────────────────────────────────────

func (s *Store) CreateRepository(ctx context.Context, repo *Repository) error {
	return s.db.WithContext(ctx).Create(repo).Error
}

func (s *Store) GetRepository(ctx context.Context, namespace, name string) (*Repository, error) {
	var repo Repository
	err := s.db.WithContext(ctx).First(&repo, "namespace = ? AND name = ?", namespace, name).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &repo, nil
}

// ListRepositories returns repositories with visibility filtering.
//
// If canManage is true, all repositories are returned (no visibility filtering).
// Otherwise, the returned set is:
//   - All public repositories
//   - Private repos owned by userID (owner_id matches)
//   - Private repos whose namespace is a user's username (personal repos)
//   - Private repos in organizations the user is a member of
//   - Private repos explicitly granted via RBAC (grantedRepos contains "namespace/name")
//
// If userID is empty (anonymous), only public repos are returned.
func (s *Store) ListRepositories(ctx context.Context, namespace, query, userID string, canManage bool, grantedRepos []string, limit, offset int) ([]*Repository, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Repository{})

	if namespace != "" {
		tx = tx.Where("namespace = ?", namespace)
	}
	if query != "" {
		tx = tx.Where("name LIKE ? OR namespace LIKE ? OR description LIKE ?", "%"+query+"%", "%"+query+"%", "%"+query+"%")
	}

	if !canManage {
		if userID != "" {
			// Authenticated user: public + owned + org membership + RBAC grants
			conditions := "is_private = ? OR owner_id = ? OR namespace IN (SELECT o.name FROM organizations o JOIN org_members om ON o.id = om.org_id WHERE om.user_id = ?)"
			args := []interface{}{false, userID, userID}

			if len(grantedRepos) > 0 {
				conditions += " OR (namespace || '/' || name) IN ?"
				args = append(args, grantedRepos)
			}

			tx = tx.Where(conditions, args...)
		} else {
			// Anonymous: public only
			tx = tx.Where("is_private = ?", false)
		}
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var repos []*Repository
	err := tx.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&repos).Error
	return repos, total, err
}

func (s *Store) DeleteRepository(ctx context.Context, namespace, name string) error {
	return s.db.WithContext(ctx).Delete(&Repository{}, "namespace = ? AND name = ?", namespace, name).Error
}

func (s *Store) UpdateRepository(ctx context.Context, repo *Repository) error {
	return s.db.WithContext(ctx).Save(repo).Error
}

func (s *Store) IncrementPullCount(ctx context.Context, namespace, name string) error {
	return s.db.WithContext(ctx).Model(&Repository{}).
		Where("namespace = ? AND name = ?", namespace, name).
		UpdateColumn("pull_count", gorm.Expr("pull_count + 1")).Error
}

func (s *Store) IncrementPushCount(ctx context.Context, namespace, name string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&Repository{}).
		Where("namespace = ? AND name = ?", namespace, name).
		Updates(map[string]interface{}{
			"push_count": gorm.Expr("push_count + 1"),
			"last_push":  now,
		}).Error
}

// ── RegistrationInvite operations ─────────────────────────────────────

func (s *Store) CreateRegistrationInvite(ctx context.Context, invite *RegistrationInvite) error {
	if invite.ID == "" {
		invite.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(invite).Error
}

func (s *Store) GetRegistrationInvite(ctx context.Context, id string) (*RegistrationInvite, error) {
	var invite RegistrationInvite
	err := s.db.WithContext(ctx).First(&invite, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &invite, nil
}

func (s *Store) GetRegistrationInviteByCode(ctx context.Context, code string) (*RegistrationInvite, error) {
	var invite RegistrationInvite
	err := s.db.WithContext(ctx).First(&invite, "code = ?", code).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &invite, nil
}

func (s *Store) ListRegistrationInvites(ctx context.Context, limit, offset int) ([]*RegistrationInvite, int64, error) {
	tx := s.db.WithContext(ctx).Model(&RegistrationInvite{})

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var invites []*RegistrationInvite
	err := tx.Order("created_at DESC").Limit(limit).Offset(offset).Find(&invites).Error
	return invites, total, err
}

func (s *Store) IncrementInviteUseCount(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&RegistrationInvite{}).
		Where("id = ?", id).
		UpdateColumn("use_count", gorm.Expr("use_count + 1")).Error
}

func (s *Store) DeleteRegistrationInvite(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Delete(&RegistrationInvite{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("invite not found")
	}
	return nil
}

// ── Webhook operations ────────────────────────────────────────────────────

func (s *Store) CreateWebhook(ctx context.Context, webhook *Webhook) error {
	if webhook.ID == "" {
		webhook.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(webhook).Error
}

func (s *Store) GetWebhook(ctx context.Context, id string) (*Webhook, error) {
	var webhook Webhook
	err := s.db.WithContext(ctx).First(&webhook, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &webhook, nil
}

func (s *Store) ListWebhooksByRepo(ctx context.Context, repoID string, limit, offset int) ([]*Webhook, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Webhook{}).Where("repo_id = ? AND scope = ?", repoID, WebhookScopeRepository)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var webhooks []*Webhook
	err := s.db.WithContext(ctx).Where("repo_id = ? AND scope = ?", repoID, WebhookScopeRepository).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&webhooks).Error
	return webhooks, total, err
}

func (s *Store) ListWebhooksByOrg(ctx context.Context, orgID string, limit, offset int) ([]*Webhook, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Webhook{}).Where("org_id = ? AND scope = ?", orgID, WebhookScopeOrganization)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var webhooks []*Webhook
	err := s.db.WithContext(ctx).Where("org_id = ? AND scope = ?", orgID, WebhookScopeOrganization).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&webhooks).Error
	return webhooks, total, err
}

func (s *Store) UpdateWebhook(ctx context.Context, webhook *Webhook) error {
	return s.db.WithContext(ctx).Save(webhook).Error
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&Webhook{}, "id = ?", id).Error
}

// GetActiveWebhooksForRepo returns active webhooks for a repo: direct repo-scoped
// webhooks plus org-scoped webhooks matching the repo's namespace.
func (s *Store) GetActiveWebhooksForRepo(ctx context.Context, namespace, name string) ([]*Webhook, error) {
	var webhooks []*Webhook

	// Get the repo
	repo, err := s.GetRepository(ctx, namespace, name)
	if err != nil || repo == nil {
		return nil, err
	}

	// Repo-scoped webhooks
	err = s.db.WithContext(ctx).Where("repo_id = ? AND scope = ? AND active = ?", repo.ID, WebhookScopeRepository, true).Find(&webhooks).Error
	if err != nil {
		return nil, err
	}

	// Org-scoped webhooks: find org by namespace
	org, err := s.GetOrganization(ctx, namespace)
	if err != nil {
		return nil, err
	}
	if org != nil {
		var orgWebhooks []*Webhook
		err = s.db.WithContext(ctx).Where("org_id = ? AND scope = ? AND active = ?", org.ID, WebhookScopeOrganization, true).Find(&orgWebhooks).Error
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, orgWebhooks...)
	}

	return webhooks, nil
}

// ── WebhookDelivery operations ────────────────────────────────────────────

func (s *Store) CreateWebhookDelivery(ctx context.Context, delivery *WebhookDelivery) error {
	if delivery.ID == "" {
		delivery.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(delivery).Error
}

func (s *Store) ListWebhookDeliveries(ctx context.Context, webhookID string, limit, offset int) ([]*WebhookDelivery, int64, error) {
	tx := s.db.WithContext(ctx).Model(&WebhookDelivery{}).Where("webhook_id = ?", webhookID)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var deliveries []*WebhookDelivery
	err := s.db.WithContext(ctx).Where("webhook_id = ?", webhookID).
		Order("delivered_at DESC").Limit(limit).Offset(offset).Find(&deliveries).Error
	return deliveries, total, err
}

func (s *Store) GetWebhookDelivery(ctx context.Context, id string) (*WebhookDelivery, error) {
	var delivery WebhookDelivery
	err := s.db.WithContext(ctx).First(&delivery, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &delivery, nil
}

// HashToken computes SHA-256 of a raw token string.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
