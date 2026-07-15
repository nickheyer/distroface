package db

import "time"

// Organization role constants
const (
	OrgRoleOwner  = "owner"
	OrgRoleAdmin  = "admin"
	OrgRoleMember = "member"
)

type User struct {
	ID           string     `json:"id" gorm:"primaryKey"`
	Username     string     `json:"username" gorm:"not null;uniqueIndex:idx_user_provider"`
	Email        *string    `json:"email" gorm:"index"`
	PasswordHash string     `json:"-" gorm:"column:password_hash"`
	DisplayName  string     `json:"display_name"`
	AuthProvider string     `json:"auth_provider" gorm:"not null;default:'local';uniqueIndex:idx_user_provider"`
	OIDCSubject  string     `json:"oidc_subject" gorm:"column:oidc_subject;uniqueIndex:idx_oidc_identity,where:oidc_subject != ''"`
	OIDCIssuer   string     `json:"oidc_issuer" gorm:"column:oidc_issuer;uniqueIndex:idx_oidc_identity,where:oidc_subject != ''"`
	IsActive     bool       `json:"is_active" gorm:"not null;default:true"`
	LastLogin    *time.Time `json:"last_login" gorm:"column:last_login"`
	CreatedAt    time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

type Role struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name" gorm:"not null;uniqueIndex"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"is_system" gorm:"not null;default:false"`
	IsDefault   bool      `json:"is_default" gorm:"not null;default:false"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type UserRole struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	UserID    string    `json:"user_id" gorm:"not null;index;column:user_id"`
	RoleName  string    `json:"role_name" gorm:"not null;index;column:role_name"`
	Source    string    `json:"source" gorm:"not null;default:'local'"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
}

type SystemSetting struct {
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"not null"`
}

type Session struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	UserID    string    `json:"user_id" gorm:"index;not null;column:user_id"`
	User      User      `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Token     string    `json:"-" gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `json:"expires_at" gorm:"not null;index"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
}

type APIToken struct {
	ID         string     `json:"id" gorm:"primaryKey"`
	UserID     string     `json:"user_id" gorm:"not null;index;column:user_id"`
	Name       string     `json:"name" gorm:"not null"`
	TokenHash  string     `json:"-" gorm:"not null;uniqueIndex;column:token_hash"`
	ExpiresAt  *time.Time `json:"expires_at" gorm:"column:expires_at"`
	LastUsedAt *time.Time `json:"last_used_at" gorm:"column:last_used_at"`
	CreatedAt  time.Time  `json:"created_at" gorm:"autoCreateTime"`
	User       *User      `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type Organization struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name" gorm:"not null;uniqueIndex"`
	DisplayName string    `json:"display_name" gorm:"column:display_name"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by" gorm:"not null;column:created_by"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type OrgMember struct {
	ID        string        `json:"id" gorm:"primaryKey"`
	OrgID     string        `json:"org_id" gorm:"not null;uniqueIndex:idx_org_user;column:org_id"`
	UserID    string        `json:"user_id" gorm:"not null;uniqueIndex:idx_org_user;column:user_id"`
	Role      string        `json:"role" gorm:"not null;default:'member'"`
	CreatedAt time.Time     `json:"created_at" gorm:"autoCreateTime"`
	Org       *Organization `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
	User      *User         `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type Repository struct {
	ID             string     `json:"id" gorm:"primaryKey"`
	Namespace      string     `json:"namespace" gorm:"uniqueIndex:idx_namespace_name;not null"`
	Name           string     `json:"name" gorm:"uniqueIndex:idx_namespace_name;not null"`
	Description    string     `json:"description"`
	OwnerID        string     `json:"owner_id" gorm:"index"`
	IsPrivate      bool       `json:"is_private" gorm:"default:false"`
	IsOrgNamespace bool       `json:"is_org_namespace" gorm:"default:false"`
	PullCount      int64      `json:"pull_count" gorm:"default:0"`
	PushCount      int64      `json:"push_count" gorm:"default:0"`
	LastPush       *time.Time `json:"last_push"`
	CreatedAt      time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

// Webhook scope constants
const (
	WebhookScopeRepository   = "repository"
	WebhookScopeOrganization = "organization"
)

type Webhook struct {
	ID              string        `json:"id" gorm:"primaryKey"`
	Scope           string        `json:"scope" gorm:"not null"`
	RepoID          *string       `json:"repo_id" gorm:"index;column:repo_id"`
	OrgID           *string       `json:"org_id" gorm:"index;column:org_id"`
	URL             string        `json:"url" gorm:"not null"`
	SecretHash      string        `json:"-" gorm:"column:secret_hash"`
	Events          string        `json:"events" gorm:"not null"` // JSON array: ["push","pull","delete"]
	Active          bool          `json:"active" gorm:"not null;default:true"`
	ContentType     string        `json:"content_type" gorm:"not null;default:'application/json'"`
	PayloadTemplate string        `json:"payload_template" gorm:"type:text"`
	CreatedBy       string        `json:"created_by" gorm:"not null;column:created_by"`
	CreatedAt       time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
	Repo            *Repository   `json:"-" gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
	Org             *Organization `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type WebhookDelivery struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	WebhookID    string    `json:"webhook_id" gorm:"not null;index;column:webhook_id"`
	Event        string    `json:"event" gorm:"not null"`
	StatusCode   int       `json:"status_code"`
	Success      bool      `json:"success"`
	RequestBody  string    `json:"request_body"`
	ResponseBody string    `json:"response_body" gorm:"type:text"`
	DurationMs   int64     `json:"duration_ms"`
	Attempt      int       `json:"attempt" gorm:"not null;default:1"`
	DeliveredAt  time.Time `json:"delivered_at" gorm:"autoCreateTime"`
	Webhook      *Webhook  `json:"-" gorm:"foreignKey:WebhookID;constraint:OnDelete:CASCADE"`
}

type RegistryPortal struct { // Alternate org-owned registry host
	ID             string        `json:"id" gorm:"primaryKey"`
	OrgID          string        `json:"org_id" gorm:"not null;index;column:org_id"`
	Name           string        `json:"name" gorm:"not null"`
	Hostname       string        `json:"hostname" gorm:"not null;uniqueIndex"`
	MapUnqualified bool          `json:"map_unqualified" gorm:"not null"`
	Rules          string        `json:"rules" gorm:"not null;default:'[]'"` // JSON array of {pattern, replace}
	AllowPush      bool          `json:"allow_push" gorm:"not null"`
	RequireAuth    bool          `json:"require_auth" gorm:"not null"`
	Enabled        bool          `json:"enabled" gorm:"not null"`
	CreatedAt      time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
	Org            *Organization `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type RegistrationInvite struct {
	ID          string     `json:"id" gorm:"primaryKey"`
	Code        string     `json:"code" gorm:"not null;uniqueIndex"`
	Description string     `json:"description"`
	Roles       string     `json:"roles" gorm:"not null;default:'[]'"` // JSON array of role names
	PinHash     string     `json:"-" gorm:"column:pin_hash"`
	MaxUses     int        `json:"max_uses" gorm:"not null;default:0"` // 0 = unlimited
	UseCount    int        `json:"use_count" gorm:"not null;default:0"`
	ExpiresAt   *time.Time `json:"expires_at" gorm:"column:expires_at"`
	CreatedBy   string     `json:"created_by" gorm:"not null;column:created_by"`
	CreatedAt   time.Time  `json:"created_at" gorm:"autoCreateTime"`
}
