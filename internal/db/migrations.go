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
