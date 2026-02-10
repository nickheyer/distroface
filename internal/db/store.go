package db

import (
	"context"
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
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
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
	return s.db.AutoMigrate(&Item{})
}

func (s *Store) CreateItem(ctx context.Context, item *Item) error {
	return s.db.WithContext(ctx).Create(item).Error
}

func (s *Store) GetItem(ctx context.Context, id string) (*Item, error) {
	var item Item
	err := s.db.WithContext(ctx).First(&item, "id = ?", id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("item not found")
		}
		return nil, err
	}
	return &item, nil
}

func (s *Store) ListItems(ctx context.Context) ([]*Item, error) {
	var items []*Item
	err := s.db.WithContext(ctx).Order("created_at DESC").Find(&items).Error
	return items, err
}

func (s *Store) UpdateItem(ctx context.Context, item *Item) error {
	return s.db.WithContext(ctx).Save(item).Error
}

func (s *Store) DeleteItem(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&Item{}, "id = ?", id).Error
}
