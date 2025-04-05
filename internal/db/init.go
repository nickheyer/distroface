package db

import (
	"fmt"

	"github.com/nickheyer/distroface/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func RunInit(db *gorm.DB, cfg *models.Config) error {
	var err error

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

	// RUN MIGRATIONS
	if err = RunMigrations(db, cfg); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func createDefaultRoles(db *gorm.DB) error {
	roles := []models.Role{
		{
			Name:        "anonymous",
			Description: "Unauthenticated access",
			Permissions: models.PermissionArray{
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
			Permissions: models.PermissionArray{
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
			Permissions: models.PermissionArray{
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
			Permissions: models.PermissionArray{
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

	for _, role := range roles {
		result := db.Where("name = ?", role.Name).FirstOrCreate(&role)
		if result.Error != nil {
			return result.Error
		}
	}

	return nil
}

func createDefaultGroups(db *gorm.DB) error {
	if err := db.Exec("UPDATE groups SET scope = 'system:all' WHERE scope IS NULL OR scope = ''").Error; err != nil {
		return err
	}

	groups := []models.Group{
		{
			Name:        "admins",
			Description: "System Administrators",
			Roles:       models.StringArray{"administrator"},
			Scope:       "system:all",
		},
		{
			Name:        "developers",
			Description: "Development Team",
			Roles:       models.StringArray{"developer"},
			Scope:       "system:all",
		},
		{
			Name:        "readers",
			Description: "Read-only Users",
			Roles:       models.StringArray{"reader"},
			Scope:       "system:all",
		},
	}

	for _, group := range groups {
		result := db.Where("name = ?", group.Name).FirstOrCreate(&group)
		if result.Error != nil {
			return result.Error
		}
	}

	return nil
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
		Groups:   models.StringArray{"admins"},
	}

	result := db.Where("username = ?", user.Username).FirstOrCreate(&user)
	return result.Error
}
