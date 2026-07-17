package stores

import (
	"fmt"
	"time"

	"github.com/nickheyer/distroface/internal/db"
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
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_txlock=immediate"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	sqlDB, err := gdb.DB()
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

	store := &Store{db: gdb}

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
	// Rename old webhook secret column keeping stored values
	if s.db.Migrator().HasTable("webhooks") &&
		s.db.Migrator().HasColumn(&db.Webhook{}, "secret_hash") &&
		!s.db.Migrator().HasColumn(&db.Webhook{}, "secret") {
		if err := s.db.Exec("ALTER TABLE webhooks RENAME COLUMN secret_hash TO secret").Error; err != nil {
			return fmt.Errorf("failed to rename webhook secret column: %w", err)
		}
	}

	// Identity index gains props_hash, drop stale three column version
	if s.db.Migrator().HasTable(&db.Artifact{}) && !s.db.Migrator().HasColumn(&db.Artifact{}, "props_hash") {
		if err := s.db.Exec("DROP INDEX IF EXISTS idx_artifact_identity").Error; err != nil {
			return fmt.Errorf("failed to drop artifact identity index: %w", err)
		}
	}

	// Artifact repos gain a namespace, identity moves from global name to composite
	if s.db.Migrator().HasTable(&db.ArtifactRepository{}) && !s.db.Migrator().HasColumn(&db.ArtifactRepository{}, "namespace") {
		if err := s.db.Exec("ALTER TABLE artifact_repositories ADD COLUMN namespace TEXT NOT NULL DEFAULT ''").Error; err != nil {
			return fmt.Errorf("failed to add artifact repo namespace column: %w", err)
		}
		s.db.Exec("DROP INDEX IF EXISTS idx_artifact_repositories_name")
		s.db.Exec("DROP INDEX IF EXISTS uni_artifact_repositories_name")
	}

	if err := s.db.AutoMigrate(
		&db.User{},
		&db.Role{},
		&db.UserRole{},
		&db.Session{},
		&db.APIToken{},
		&db.Organization{},
		&db.OrgMember{},
		&db.Repository{},
		&db.Star{},
		&db.SystemSetting{},
		&db.OrgSetting{},
		&db.RegistrationInvite{},
		&db.Webhook{},
		&db.WebhookDelivery{},
		&db.RegistryPortal{},
		&db.ArtifactRepository{},
		&db.Artifact{},
		&db.ArtifactProperty{},
		&db.CertificateDomain{},
		&db.ACMECacheEntry{},
		&db.AuditEvent{},
	); err != nil {
		return fmt.Errorf("failed to auto-migrate: %w", err)
	}

	// Drop old indexes that conflict with new schema
	s.db.Exec("DROP INDEX IF EXISTS idx_users_username")
	s.db.Exec("DROP INDEX IF EXISTS idx_users_email")
	s.db.Exec("DROP INDEX IF EXISTS uni_users_username")
	s.db.Exec("DROP INDEX IF EXISTS uni_users_email")

	if err := s.backfillArtifactPropsHash(); err != nil {
		return fmt.Errorf("failed to backfill artifact props hash: %w", err)
	}

	if err := s.backfillArtifactRepoNamespace(); err != nil {
		return fmt.Errorf("failed to backfill artifact repo namespace: %w", err)
	}

	// Org scoped casbin objects moved from org name to org id
	if s.db.Migrator().HasTable("casbin_rule") {
		if err := s.db.Exec(`UPDATE casbin_rule
			SET v3 = (SELECT o.id FROM organizations o WHERE o.name = casbin_rule.v3)
			WHERE ptype = 'p' AND v1 = 'organizations' AND v3 <> '*'
			AND EXISTS (SELECT 1 FROM organizations o WHERE o.name = casbin_rule.v3)`).Error; err != nil {
			return fmt.Errorf("failed to migrate casbin org objects: %w", err)
		}
	}

	if err := s.SeedSystemRoles(); err != nil {
		return fmt.Errorf("failed to seed system roles: %w", err)
	}

	return nil
}
