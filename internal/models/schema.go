package models

import (
	"time"

	"gorm.io/gorm"
)

type Setting struct {
	ID        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Key       string    `json:"key" gorm:"uniqueIndex;not null"`
	Value     string    `json:"value" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// NAMED SET OF PERMISSIONS
type Role struct {
	ID          int          `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string       `json:"name" gorm:"uniqueIndex;not null"`
	Description string       `json:"description" gorm:"default:''"`
	Permissions []Permission `json:"permissions" gorm:"type:text;serializer:json;not null"`
	CreatedAt   time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time    `json:"updated_at" gorm:"autoUpdateTime"`
}

// ROLES AND USERS
type Group struct {
	ID          int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string    `json:"name" gorm:"uniqueIndex;not null"`
	Description string    `json:"description" gorm:"default:''"`
	Roles       []string  `json:"roles" gorm:"type:text;serializer:json"`
	Scope       string    `json:"scope" gorm:"default:'system:default'"`
	Target      string    `json:"target" gorm:"default:'';null"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (g *Group) BeforeCreate(tx *gorm.DB) error {
	if g.Scope == "" {
		g.Scope = "system:default"
	}
	return nil
}

// SYSTEM USER WITH GROUPS
type User struct {
	ID        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Username  string    `json:"username" gorm:"uniqueIndex;not null"`
	Password  []byte    `json:"-,omitempty" gorm:"not null"`
	Groups    []string  `json:"groups" gorm:"type:text;serializer:json;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type ImageMetadata struct {
	ID        string            `json:"id" gorm:"primaryKey;type:text"`
	Name      string            `json:"name" gorm:"not null"`
	Tags      []string          `json:"tags" gorm:"type:text;serializer:json;not null"`
	Size      int64             `json:"size" gorm:"not null"`
	Owner     string            `json:"owner" gorm:"not null;index"`
	Labels    map[string]string `json:"labels" gorm:"type:text;serializer:json"`
	Private   bool              `json:"private" gorm:"default:false"`
	CreatedAt time.Time         `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time         `json:"updated_at" gorm:"autoUpdateTime"`
}

type UserImage struct {
	ID        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Username  string    `json:"username" gorm:"uniqueIndex:idx_user_images_unique;not null"`
	Name      string    `json:"name" gorm:"uniqueIndex:idx_user_images_unique;index;not null"`
	Tag       string    `json:"tag" gorm:"uniqueIndex:idx_user_images_unique;not null"`
	ImageID   string    `json:"image_id" gorm:"type:text;not null;index"`
	Private   bool      `json:"private" gorm:"default:false"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type ArtifactRepository struct {
	ID          int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string    `json:"name" gorm:"uniqueIndex;not null"`
	Description string    `json:"description"`
	Owner       string    `json:"owner" gorm:"not null"`
	Private     *bool     `json:"private" gorm:"default:true"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type Artifact struct {
	ID         string             `json:"id" gorm:"primaryKey;type:text"`
	RepoID     int                `json:"repo_id" gorm:"not null;index"`
	Name       string             `json:"name" gorm:"not null"`
	Path       string             `json:"path" gorm:"not null"`
	UploadID   string             `json:"upload_id" gorm:"not null"`
	Version    string             `json:"version" gorm:"not null"`
	Size       int64              `json:"size" gorm:"not null"`
	MimeType   string             `json:"mime_type"`
	Metadata   string             `json:"metadata"`
	CreatedAt  time.Time          `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time          `json:"updated_at" gorm:"autoUpdateTime"`
	Properties []ArtifactProperty `json:"properties" gorm:"foreignKey:ArtifactID;references:ID"`
}

func (a *Artifact) PropertiesMap() map[string]string {
	result := make(map[string]string)
	for _, prop := range a.Properties {
		result[prop.Key] = prop.Value
	}
	return result
}

type ArtifactProperty struct {
	ID         int       `json:"id" gorm:"primaryKey;autoIncrement"`
	ArtifactID string    `json:"artifact_id" gorm:"index;not null"`
	Key        string    `json:"key" gorm:"not null;index"`
	Value      string    `json:"value" gorm:"not null;index"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}
