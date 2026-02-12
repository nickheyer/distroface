package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

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
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
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

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *Store) Migrate() error {
	return s.db.AutoMigrate(&User{}, &Session{}, &Repository{})
}

// User operations

func (s *Store) CreateUser(ctx context.Context, user *User) error {
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

func (s *Store) UpdateUser(ctx context.Context, user *User) error {
	return s.db.WithContext(ctx).Save(user).Error
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&User{}).Count(&count).Error
	return count, err
}

// Session operations

func (s *Store) CreateSession(ctx context.Context, session *Session) error {
	return s.db.WithContext(ctx).Create(session).Error
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	var session Session
	err := s.db.WithContext(ctx).Preload("User").First(&session, "token_hash = ?", tokenHash).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&Session{}, "id = ?", id).Error
}

func (s *Store) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	return s.db.WithContext(ctx).Delete(&Session{}, "token_hash = ?", tokenHash).Error
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	return s.db.WithContext(ctx).Delete(&Session{}, "expires_at < ?", time.Now().UTC()).Error
}

func (s *Store) UpdateSession(ctx context.Context, session *Session) error {
	return s.db.WithContext(ctx).Save(session).Error
}

// Repository operations

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

func (s *Store) ListRepositories(ctx context.Context, namespace, query string, visibility *bool, limit, offset int) ([]*Repository, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Repository{})

	if namespace != "" {
		tx = tx.Where("namespace = ?", namespace)
	}
	if query != "" {
		tx = tx.Where("name LIKE ? OR namespace LIKE ? OR description LIKE ?", "%"+query+"%", "%"+query+"%", "%"+query+"%")
	}
	if visibility != nil {
		tx = tx.Where("is_private = ?", !*visibility)
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

// HashToken computes SHA-256 of a raw token string.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
