package db

import (
	"fmt"
	"time"

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
	ID      string
	Migrate func(*gorm.DB) error
}

func applyMigration(db *gorm.DB, m Migration) error {
	// TRACK MIGRATIONS
	db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	// CHECK IF MIGRATION APPLIED
	var migration models.SchemaMigration
	result := db.Table("schema_migrations").Where("id = ?", m.ID).First(&migration)

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

		return tx.Table("schema_migrations").Create(&models.SchemaMigration{ID: m.ID}).Error
	})
}

var migrations = []Migration{
	{
		ID: "000_ensure_tables",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS user_images (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				username TEXT NOT NULL,
				name TEXT NOT NULL,
				tag TEXT NOT NULL,
				image_id TEXT NOT NULL,
				private BOOLEAN DEFAULT FALSE,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(username, name, tag)
			)`).Error; err != nil {
				return err
			}

			tx.Exec("CREATE INDEX IF NOT EXISTS idx_user_images_image_id ON user_images(image_id)")

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS image_metadata (
				id TEXT PRIMARY KEY,
				size INTEGER NOT NULL,
				labels TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)`).Error; err != nil {
				return err
			}

			return nil
		},
	},
	{
		ID: "001_initial_setup",
		Migrate: func(tx *gorm.DB) error {
			tx.Exec("DROP INDEX IF EXISTS idx_groups_scope")

			if err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS groups_new (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					name TEXT UNIQUE NOT NULL,
					description TEXT NOT NULL,
					roles TEXT NOT NULL,
					scope TEXT DEFAULT 'system:all',
					target TEXT DEFAULT '',
					created_at DATETIME,
					updated_at DATETIME
				)
			`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`
				INSERT INTO groups_new(id, name, description, roles, scope, target, created_at, updated_at)
				SELECT id, name, description, roles, COALESCE(scope, 'system:all'), COALESCE(target, ''), created_at, updated_at 
				FROM groups
			`).Error; err != nil {
				return err
			}

			if err := tx.Exec("DROP TABLE groups").Error; err != nil {
				return err
			}

			if err := tx.Exec("ALTER TABLE groups_new RENAME TO groups").Error; err != nil {
				return err
			}

			if err := tx.Exec("CREATE INDEX idx_groups_scope ON groups(scope)").Error; err != nil {
				return err
			}

			return nil
		},
	},
	{
		ID: "002_migrate_image_metadata_to_user_images",
		Migrate: func(tx *gorm.DB) error {
			var count int64
			if err := tx.Table("image_metadata").Count(&count).Error; err != nil {
				return fmt.Errorf("failed to check image_metadata count: %w", err)
			}

			if count == 0 {
				return nil
			}

			// FETCH ALL IMAGE METADATA FROM DEPR TABLE
			type OldImageMetadata struct {
				ID        string
				Name      string
				Tags      models.StringArray
				Size      int64
				Owner     string
				Labels    models.StringMap
				Private   bool
				CreatedAt time.Time
				UpdatedAt time.Time
			}

			var oldMetadata []OldImageMetadata
			if err := tx.Table("image_metadata").Find(&oldMetadata).Error; err != nil {
				return fmt.Errorf("failed to fetch image_metadata records: %w", err)
			}

			// WE GOTTA RECREATE THE STRUCTURE OF THE OLD METADATA VERSION
			for _, oldMeta := range oldMetadata {
				// CHECK IF METADATA EXISTS IN OLD TABLE
				var existingCount int64
				if err := tx.Model(&models.ImageMetadata{}).Where("id = ?", oldMeta.ID).Count(&existingCount).Error; err != nil {
					return fmt.Errorf("failed to check if metadata exists: %w", err)
				}

				if existingCount == 0 {
					newImageMetadata := models.ImageMetadata{
						ID:        oldMeta.ID,
						Size:      oldMeta.Size,
						Labels:    oldMeta.Labels,
						CreatedAt: oldMeta.CreatedAt,
						UpdatedAt: oldMeta.UpdatedAt,
					}

					if err := tx.Create(&newImageMetadata).Error; err != nil {
						return fmt.Errorf("failed to create new metadata: %w", err)
					}
				} else {
					// UPDATE EXISTING
					if err := tx.Model(&models.ImageMetadata{}).Where("id = ?", oldMeta.ID).
						Updates(map[string]interface{}{
							"size":   oldMeta.Size,
							"labels": oldMeta.Labels,
						}).Error; err != nil {
						return fmt.Errorf("failed to update image metadata %s: %w", oldMeta.ID, err)
					}
				}

				for _, tag := range oldMeta.Tags {
					userImage := models.UserImage{
						Username:  oldMeta.Owner,
						Name:      oldMeta.Name,
						Tag:       tag,
						ImageID:   oldMeta.ID,
						Private:   oldMeta.Private,
						CreatedAt: oldMeta.CreatedAt,
						UpdatedAt: oldMeta.UpdatedAt,
					}

					// CHECK IF USER IMAGE EXISTS
					var userImageCount int64
					if err := tx.Model(&models.UserImage{}).
						Where("username = ? AND name = ? AND tag = ?", oldMeta.Owner, oldMeta.Name, tag).
						Count(&userImageCount).Error; err != nil {
						return fmt.Errorf("failed to check if user image exists: %w", err)
					}

					if userImageCount == 0 {
						if err := tx.Create(&userImage).Error; err != nil {
							return fmt.Errorf("failed to create user image for %s/%s:%s: %w",
								oldMeta.Owner, oldMeta.Name, tag, err)
						}
					}
				}
			}

			// DONT DELETE OLD TABLE, WE MIGHT NEED IT....
			return nil
		},
	},
}
