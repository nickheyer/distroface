package db

import "time"

type User struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Username     string    `json:"username" gorm:"uniqueIndex;not null"`
	Email        string    `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash string    `json:"-" gorm:"not null"`
	DisplayName  string    `json:"display_name"`
	IsAdmin      bool      `json:"is_admin" gorm:"default:false"`
	CreatedAt    time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type Session struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	UserID    string    `json:"user_id" gorm:"index;not null"`
	User      User      `json:"-" gorm:"foreignKey:UserID"`
	TokenHash string    `json:"token_hash" gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `json:"expires_at" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type Repository struct {
	ID        string     `json:"id" gorm:"primaryKey"`
	Namespace string     `json:"namespace" gorm:"uniqueIndex:idx_namespace_name;not null"`
	Name      string     `json:"name" gorm:"uniqueIndex:idx_namespace_name;not null"`
	OwnerID   string     `json:"owner_id" gorm:"index"`
	Owner     User       `json:"-" gorm:"foreignKey:OwnerID"`
	IsPrivate bool       `json:"is_private" gorm:"default:false"`
	PullCount int64      `json:"pull_count" gorm:"default:0"`
	PushCount int64      `json:"push_count" gorm:"default:0"`
	LastPush  *time.Time `json:"last_push"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}
