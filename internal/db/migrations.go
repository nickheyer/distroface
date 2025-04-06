package db

import (
	"github.com/nickheyer/distroface/internal/models"
	"gorm.io/gorm"
)

func RunMigrations(db *gorm.DB) error {

	return db.Debug().AutoMigrate(
		&models.Group{},
		&models.Setting{},
		&models.Role{},
		&models.User{},
		&models.ImageMetadata{},
		&models.UserImage{},
		&models.ArtifactRepository{},
		&models.Artifact{},
		&models.ArtifactProperty{})
}
