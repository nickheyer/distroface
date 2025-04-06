package db

import (
	"fmt"
	"log"

	"github.com/nickheyer/distroface/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func RunInit(db *gorm.DB, cfg *models.Config) error {
	var err error

	if cfg.Init.Migrations {
		log.Println("Starting migrations...")
		if err = RunMigrations(db); err != nil {
			return fmt.Errorf("failed to run migrations: %w", err)
		}
		log.Println("Migrations completed successfully")
	}

	// IF ROLES ENABLED
	if cfg.Init.Roles {
		if err = createDefaultRoles(db); err != nil {
			return fmt.Errorf("failed to init roles: %w", err)
		}
	}

	// IF GROUPS ENABLED
	if cfg.Init.Groups {
		if err = createDefaultGroups(db); err != nil {
			return fmt.Errorf("failed to init groups: %w", err)
		}
	}

	// IF USER ENABLED
	if cfg.Init.User {
		if err = createAdminUser(db, cfg); err != nil {
			return fmt.Errorf("failed to init user: %w", err)
		}
	}

	return nil
}

func createDefaultRoles(db *gorm.DB) error {
	roles := []models.Role{
		{
			Name:        "anonymous",
			Description: "Unauthenticated access",
			Permissions: []models.Permission{
				{Action: models.ActionView, Resource: models.ResourceWebUI},
				{Action: models.ActionLogin, Resource: models.ResourceWebUI},
				{Action: models.ActionPull, Resource: models.ResourceImage},
				{Action: models.ActionView, Resource: models.ResourceTag},
				{Action: models.ActionView, Resource: models.ResourceArtifact},
				{Action: models.ActionDownload, Resource: models.ResourceArtifact},
				{Action: models.ActionView, Resource: models.ResourceRepo},
			},
		},
		{
			Name:        "reader",
			Description: "Basic read access",
			Permissions: []models.Permission{
				{Action: models.ActionView, Resource: models.ResourceWebUI},
				{Action: models.ActionLogin, Resource: models.ResourceWebUI},
				{Action: models.ActionPull, Resource: models.ResourceImage},
				{Action: models.ActionView, Resource: models.ResourceTag},
				{Action: models.ActionView, Resource: models.ResourceUser},
				{Action: models.ActionView, Resource: models.ResourceGroup},
				{Action: models.ActionView, Resource: models.ResourceArtifact},
				{Action: models.ActionDownload, Resource: models.ResourceArtifact},
				{Action: models.ActionView, Resource: models.ResourceRepo},
			},
		},
		{
			Name:        "developer",
			Description: "Standard developer access",
			Permissions: []models.Permission{
				{Action: models.ActionMigrate, Resource: models.ResourceTask},
				{Action: models.ActionView, Resource: models.ResourceWebUI},
				{Action: models.ActionLogin, Resource: models.ResourceWebUI},
				{Action: models.ActionUpdate, Resource: models.ResourceWebUI},
				{Action: models.ActionPull, Resource: models.ResourceImage},
				{Action: models.ActionPush, Resource: models.ResourceImage},
				{Action: models.ActionView, Resource: models.ResourceImage},
				{Action: models.ActionView, Resource: models.ResourceTag},
				{Action: models.ActionCreate, Resource: models.ResourceTag},
				{Action: models.ActionDelete, Resource: models.ResourceTag},
				{Action: models.ActionView, Resource: models.ResourceUser},
				{Action: models.ActionView, Resource: models.ResourceGroup},
				{Action: models.ActionView, Resource: models.ResourceArtifact},
				{Action: models.ActionUpload, Resource: models.ResourceArtifact},
				{Action: models.ActionUpdate, Resource: models.ResourceArtifact},
				{Action: models.ActionDownload, Resource: models.ResourceArtifact},
				{Action: models.ActionDelete, Resource: models.ResourceArtifact},
				{Action: models.ActionView, Resource: models.ResourceRepo},
				{Action: models.ActionCreate, Resource: models.ResourceRepo},
				{Action: models.ActionDelete, Resource: models.ResourceRepo},
			},
		},
		{
			Name:        "administrator",
			Description: "Full system access",
			Permissions: []models.Permission{
				{Action: models.ActionAdmin, Resource: models.ResourceSystem},
				{Action: models.ActionView, Resource: models.ResourceWebUI},
				{Action: models.ActionLogin, Resource: models.ResourceWebUI},
				{Action: models.ActionView, Resource: models.ResourceUser},
				{Action: models.ActionCreate, Resource: models.ResourceUser},
				{Action: models.ActionUpdate, Resource: models.ResourceUser},
				{Action: models.ActionDelete, Resource: models.ResourceUser},
				{Action: models.ActionView, Resource: models.ResourceGroup},
				{Action: models.ActionCreate, Resource: models.ResourceGroup},
				{Action: models.ActionUpdate, Resource: models.ResourceGroup},
				{Action: models.ActionDelete, Resource: models.ResourceGroup},
				{Action: models.ActionUpdate, Resource: models.ResourceWebUI},
				{Action: models.ActionPull, Resource: models.ResourceImage},
				{Action: models.ActionPush, Resource: models.ResourceImage},
				{Action: models.ActionMigrate, Resource: models.ResourceTask},
				{Action: models.ActionDelete, Resource: models.ResourceImage},
				{Action: models.ActionView, Resource: models.ResourceTag},
				{Action: models.ActionCreate, Resource: models.ResourceTag},
				{Action: models.ActionDelete, Resource: models.ResourceTag},
				{Action: models.ActionView, Resource: models.ResourceArtifact},
				{Action: models.ActionUpload, Resource: models.ResourceArtifact},
				{Action: models.ActionUpdate, Resource: models.ResourceArtifact},
				{Action: models.ActionDownload, Resource: models.ResourceArtifact},
				{Action: models.ActionDelete, Resource: models.ResourceArtifact},
				{Action: models.ActionView, Resource: models.ResourceRepo},
				{Action: models.ActionCreate, Resource: models.ResourceRepo},
				{Action: models.ActionDelete, Resource: models.ResourceRepo},
			},
		},
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, role := range roles {
			var existingRole models.Role
			result := tx.Where("name = ?", role.Name).First(&existingRole)

			if result.RowsAffected > 0 {
				if err := tx.Save(&existingRole).Error; err != nil {
					return err
				}
			} else {
				// CREATE NEW
				if err := tx.Create(&role).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func createDefaultGroups(db *gorm.DB) error {
	groups := []models.Group{
		{
			Name:        "admins",
			Description: "System Administrators",
			Roles:       []string{"administrator"},
			Scope:       "system:all",
		},
		{
			Name:        "developers",
			Description: "Development Team",
			Roles:       []string{"developer"},
			Scope:       "system:all",
		},
		{
			Name:        "readers",
			Description: "Read-only Users",
			Roles:       []string{"reader"},
			Scope:       "system:all",
		},
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// CREATE/UPDATE DEFAULT GROUPS
		for _, group := range groups {
			var existingGroup models.Group
			result := tx.Where("name = ?", group.Name).First(&existingGroup)

			if result.RowsAffected > 0 {
				if err := tx.Save(&existingGroup).Error; err != nil {
					return err
				}
			} else {
				// CREATE NEW
				if err := tx.Create(&group).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func createAdminUser(db *gorm.DB, cfg *models.Config) error {
	// GENERATE BCRYPT HASH FOR PASS
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.Init.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password for user %q: %w", cfg.Init.Username, err)
	}

	// PUT USER IN ADMINS FOR EZ
	user := models.User{
		Username: cfg.Init.Username,
		Password: hash,
		Groups:   []string{"admins"},
	}

	result := db.Where("username = ?", user.Username).FirstOrCreate(&user)
	return result.Error
}
