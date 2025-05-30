package models

import (
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

// DEFAULT SYSTEM ROLES - TODO: USE THIS INSTEAD OF MANUAL MIGRATIONS
type GlobalView struct {
	TotalImages int64                `json:"total_images"`
	TotalSize   int64                `json:"total_size"`
	Images      []*ImageMetadataView `json:"images"`
}

type ImageMetadataView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	FullName    string            `json:"full_name"`
	Tags        []string          `json:"tags"`
	Size        int64             `json:"size"`
	Owner       string            `json:"owner"`
	Labels      map[string]string `json:"labels"`
	Private     bool              `json:"private"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	IsOwnedByMe bool              `json:"is_owned_by_me"`
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
