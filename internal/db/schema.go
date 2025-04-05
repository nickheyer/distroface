package db

import (
	"fmt"

	"github.com/nickheyer/distroface/internal/models"
	"gorm.io/gorm"
)

func RunSchema(db *gorm.DB, cfg *models.Config) error {
	if cfg.Init.Drop {
		if err := dropAllTables(db); err != nil {
			return fmt.Errorf("failed to drop tables: %w", err)
		}
	}

	if err := autoMigrate(db); err != nil {
		return fmt.Errorf("failed to apply schema: %w", err)
	}

	return nil
}

func dropAllTables(db *gorm.DB) error {
	// DROP TABELS IN REVERSE DEP ORDER
	tables := []any{
		&models.ArtifactProperty{},
		&models.Artifact{},
		&models.ArtifactRepository{},
		&models.ImageMetadata{},
		&models.User{},
		&models.Group{},
		&models.Role{},
		&models.Setting{},
		&models.SchemaMigration{},
	}

	for _, table := range tables {
		if err := db.Migrator().DropTable(table); err != nil {
			return err
		}
	}

	return nil
}

func autoMigrate(db *gorm.DB) error {
	db = db.Exec("PRAGMA foreign_keys = OFF")

	// MIGRATE TABLES IN DEP ORDER
	models := []any{
		&models.Setting{},
		&models.Role{},
		&models.Group{},
		&models.User{},
		&models.ImageMetadata{},
		&models.ArtifactRepository{},
		&models.Artifact{},
		&models.ArtifactProperty{},
		&models.SchemaMigration{},
	}

	// RUN AUTOMIGRATE ON EACH TABLE
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil {
			return err
		}
	}

	return nil
}
