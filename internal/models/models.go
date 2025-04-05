package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type Action string
type Resource string
type Scope string

// PERMISSIONS - SINGLE SOURCE OF TRUTH
const (
	// ACTIONS
	ActionView     Action = "VIEW"
	ActionCreate   Action = "CREATE"
	ActionUpdate   Action = "UPDATE"
	ActionDelete   Action = "DELETE"
	ActionPush     Action = "PUSH"
	ActionPull     Action = "PULL"
	ActionAdmin    Action = "ADMIN"
	ActionLogin    Action = "LOGIN"
	ActionLogout   Action = "LOGOUT"
	ActionMigrate  Action = "MIGRATE"
	ActionUpload   Action = "UPLOAD"
	ActionDownload Action = "DOWNLOAD"

	// RESOURCES
	ResourceTask     Resource = "TASK"
	ResourceWebUI    Resource = "WEBUI"
	ResourceImage    Resource = "IMAGE"
	ResourceTag      Resource = "TAG"
	ResourceUser     Resource = "USER"
	ResourceGroup    Resource = "GROUP"
	ResourceSystem   Resource = "SYSTEM"
	ResourceArtifact Resource = "ARTIFACT"
	ResourceRepo     Resource = "REPO"

	// SCOPES
	ScopeGlobal     Scope = "global"
	ScopeRepository Scope = "repository"
	ScopeProject    Scope = "project"
)

// SINGLE CAPABILITY TO DO SOMETHING
type Permission struct {
	Action   Action   `json:"action"`
	Resource Resource `json:"resource"`
	Scope    Scope    `json:"scope,omitempty"`
	Target   string   `json:"target,omitempty"` // OPT TARGET W/ SCOPE
}

func (p Permission) String() string {
	return string(p.Resource) + ":" + string(p.Action)
}

type StringArray []string

func (sa *StringArray) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to unmarshal StringArray value: %v", value)
	}

	return json.Unmarshal(bytes, sa)
}

func (sa StringArray) Value() (driver.Value, error) {
	return json.Marshal(sa)
}

type PermissionArray []Permission

func (pa *PermissionArray) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to unmarshal PermissionArray value: %v", value)
	}

	return json.Unmarshal(bytes, pa)
}

func (pa PermissionArray) Value() (driver.Value, error) {
	return json.Marshal(pa)
}

type StringMap map[string]string

func (sm *StringMap) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to unmarshal StringMap value: %v", value)
	}

	return json.Unmarshal(bytes, sm)
}

func (sm StringMap) Value() (driver.Value, error) {
	return json.Marshal(sm)
}

// NAMED SET OF PERMISSIONS
type Role struct {
	ID          int             `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string          `json:"name" gorm:"uniqueIndex;not null"`
	Description string          `json:"description" gorm:"not null"`
	Permissions PermissionArray `json:"permissions" gorm:"type:text;not null"`
	CreatedAt   time.Time       `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time       `json:"updated_at" gorm:"autoUpdateTime"`
}

// ROLES AND USERS
type Group struct {
	ID          int         `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string      `json:"name" gorm:"uniqueIndex;not null"`
	Description string      `json:"description" gorm:"not null"`
	Roles       StringArray `json:"roles" gorm:"type:text;not null"`
	Scope       string      `json:"scope" gorm:"index;not null;default:'system:default'"`
	Target      string      `json:"target,omitempty" gorm:"default:''"`
	CreatedAt   time.Time   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time   `json:"updated_at" gorm:"autoUpdateTime"`
}

// SYSTEM USER WITH GROUPS
type User struct {
	ID        int         `json:"id" gorm:"primaryKey;autoIncrement"`
	Username  string      `json:"username" gorm:"uniqueIndex;not null"`
	Password  []byte      `json:"-,omitempty" gorm:"not null"`
	Groups    StringArray `json:"groups" gorm:"type:text;not null"`
	CreatedAt time.Time   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time   `json:"updated_at" gorm:"autoUpdateTime"`
}

// DEFAULT SYSTEM ROLES - TODO: USE THIS INSTEAD OF MANUAL MIGRATIONS
type GlobalView struct {
	TotalImages int64                `json:"total_images"`
	TotalSize   int64                `json:"total_size"`
	Images      []*ImageMetadataView `json:"images"`
}

type ImageMetadataView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	FullName    string    `json:"full_name"`
	Tags        []string  `json:"tags"`
	Size        int64     `json:"size"`
	Owner       string    `json:"owner"`
	Labels      StringMap `json:"labels"`
	Private     bool      `json:"private"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	IsOwnedByMe bool      `json:"is_owned_by_me"`
}

type ImageRepository struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	FullName    string     `json:"full_name"`
	Tags        []ImageTag `json:"tags"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Owner       string     `json:"owner"`
	Private     bool       `json:"private"`
	Size        int64      `json:"size"`
	IsOwnedByMe bool       `json:"is_owned_by_me"`
}

type ImageTag struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Digest  string    `json:"digest"`
	Created time.Time `json:"created"`
}

type ImageMetadata struct {
	ID        string    `json:"id" gorm:"primaryKey;type:text"`
	Size      int64     `json:"size" gorm:"not null"`
	Labels    StringMap `json:"labels" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
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

type DockerManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
	} `json:"layers"`
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

type ArtifactProperty struct {
	ID         int       `json:"id" gorm:"primaryKey;autoIncrement"`
	ArtifactID string    `json:"artifact_id" gorm:"index;not null"`
	Key        string    `json:"key" gorm:"not null;index"`
	Value      string    `json:"value" gorm:"not null;index"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (a *Artifact) PropertiesMap() map[string]string {
	result := make(map[string]string)
	for _, prop := range a.Properties {
		result[prop.Key] = prop.Value
	}
	return result
}

type ArtifactSearchCriteria struct {
	RepoID     *int              `json:"repo_id,omitempty"`
	Username   string            `json:"username,omitempty"`
	Name       *string           `json:"name,omitempty"`
	Version    *string           `json:"version,omitempty"`
	Path       *string           `json:"path,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Sort       string            `json:"sort,omitempty"`
	Order      string            `json:"order,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	Offset     int               `json:"offset,omitempty"`
}

type SearchResponse struct {
	Results []Artifact `json:"results"`
	Total   int        `json:"total"`
	Limit   int        `json:"limit"`
	Offset  int        `json:"offset"`
	Sort    string     `json:"sort"`
	Order   string     `json:"order"`
}

type VisibilityUpdateRequest struct {
	ID      string `json:"id"`   // CAN BE ID OR REPO NAME OR HASH
	Name    string `json:"name"` // CAN ONLY BE REPO NAME
	Private bool   `json:"private"`
}

type VisibilityUpdateResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type RegistryStats struct {
	TotalImages int64            `json:"total_images"`
	TotalSize   int64            `json:"total_size"`
	Images      []*ImageMetadata `json:"images"`
}

// FOR UNDERLYING REGISTRY AUTH
type ResourceActions struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type CustomClaims struct {
	Subject string             `json:"sub,omitempty"`
	Access  []*ResourceActions `json:"access,omitempty"`
	Groups  []string           `json:"groups,omitempty"`
	jwt.RegisteredClaims
}

type AuthConfig struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Username  string    `json:"username"`
	Server    string    `json:"server"`
}

func (c CustomClaims) Valid() error {
	return c.RegisteredClaims.Valid()
}

// METRIC TYPES
type BlobMetrics struct {
	Total          int64   `json:"total"`
	Failed         int64   `json:"failed"`
	InProgress     int32   `json:"inProgress"`
	BytesProcessed int64   `json:"bytesProcessed"`
	AvgDuration    float64 `json:"avgDuration"`
}

type PerformanceMetrics struct {
	AvgUploadSpeed   float64 `json:"avgUploadSpeed"`   // MB/S
	AvgDownloadSpeed float64 `json:"avgDownloadSpeed"` // MB/S
	DiskUsage        int64   `json:"diskUsage"`        // GB
	DiskTotal        int64   `json:"diskTotal"`        // GB
	MemoryUsage      int64   `json:"memoryUsage"`      // MB
	MemoryTotal      int64   `json:"memoryTotal"`      // MB
	CpuUsage         float64 `json:"cpuUsage"`
}

type TimeSeriesPoint struct {
	Timestamp     time.Time `json:"timestamp"`
	UploadSpeed   float64   `json:"uploadSpeed"`
	DownloadSpeed float64   `json:"downloadSpeed"`
	ActiveUploads int32     `json:"activeUploads"`
}

type MetricsData struct {
	BlobUploads    BlobMetrics        `json:"blobUploads"`
	BlobDownloads  BlobMetrics        `json:"blobDownloads"`
	Performance    PerformanceMetrics `json:"performance"`
	TimeseriesData []TimeSeriesPoint  `json:"timeseriesData"`
	AccessLogs     []AccessLogEntry   `json:"access_logs"`
}

type DiskInfo struct {
	DiskTotal     int64
	DiskAvailable int64
}

type AccessLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	Path      string    `json:"path"`
	Method    string    `json:"method"`
	Status    int       `json:"status"`
}

type Setting struct {
	ID        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Key       string    `json:"key" gorm:"uniqueIndex;not null"`
	Value     string    `json:"value" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

type SchemaMigration struct {
	ID        string    `json:"id" gorm:"primaryKey;type:text"`
	AppliedAt time.Time `json:"applied_at" gorm:"autoCreateTime"`
}
