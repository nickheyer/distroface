package db

import (
	"fmt"

	"github.com/nickheyer/distroface/internal/models"
	"gorm.io/gorm"
)

func RunMigrations(db *gorm.DB, cfg *models.Config) error {
	if !cfg.Init.Migrations {
		return nil
	}
	for _, m := range migrations {
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("failed to run migration %s: %w", m.ID, err)
		}
	}

	return nil
}

type Migration struct {
	ID       string
	Migrate  func(*gorm.DB) error
	Rollback func(*gorm.DB) error
}

func applyMigration(db *gorm.DB, m Migration) error {
	// CHECK IF MIGRATION APPLIED
	var migration models.SchemaMigration
	result := db.Where("id = ?", m.ID).First(&migration)

	// IF APPLIED, SKIP IT
	if result.Error == nil {
		return nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return result.Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := m.Migrate(tx); err != nil {
			return err
		}

		return tx.Create(&models.SchemaMigration{ID: m.ID}).Error
	})
}

var migrations = []Migration{
	{
		ID: "001_initial_setup",
		Migrate: func(tx *gorm.DB) error {
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return nil
		},
	},
}
