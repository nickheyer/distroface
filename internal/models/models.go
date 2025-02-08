package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type Action string
type Resource string
type Scope string

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

// NAMED SET OF PERMISSIONS
type Role struct {
	ID          int          `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Permissions []Permission `json:"permissions"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// PERMISSIONS TO JSON
func (r *Role) MarshalPermissions() (string, error) {
	data, err := json.Marshal(r.Permissions)
	return string(data), err
}

// JSON TO PERMISSIONS
func (r *Role) UnmarshalPermissions(data string) error {
	return json.Unmarshal([]byte(data), &r.Permissions)
}

// ROLES AND USERS
type Group struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Roles       []string  `json:"roles"`
	Scope       Scope     `json:"scope"`
	Target      string    `json:"target,omitempty"` // OPT TARGET W/ SCOPE
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ROLES TO JSON
func (g *Group) MarshalRoles() (string, error) {
	data, err := json.Marshal(g.Roles)
	return string(data), err
}

// JSON TO ROLES
func (g *Group) UnmarshalRoles(data string) error {
	return json.Unmarshal([]byte(data), &g.Roles)
}

// SYSTEM USER WITH GROUPS
type User struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Password  []byte    `json:"-"`
	Groups    []string  `json:"groups"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GROUPS TO JSON
func (u *User) MarshalGroups() (string, error) {
	data, err := json.Marshal(u.Groups)
	return string(data), err
}

// JSON TO GROUPS
func (u *User) UnmarshalGroups(data string) error {
	return json.Unmarshal([]byte(data), &u.Groups)
}

// DEFAULT SYSTEM ROLES - TODO: USE THIS INSTEAD OF MANUAL MIGRATIONS
func DefaultRoles() []Role {
	return []Role{
		{
			Name:        "anonymous",
			Description: "Unauthenticated access",
			Permissions: []Permission{
				{Action: ActionPull, Resource: ResourceImage},
				{Action: ActionView, Resource: ResourceWebUI},
				{Action: ActionLogin, Resource: ResourceWebUI},
			},
		},
		{
			Name:        "reader",
			Description: "Basic read access",
			Permissions: []Permission{
				{Action: ActionPull, Resource: ResourceImage},
				{Action: ActionView, Resource: ResourceTag},
				{Action: ActionView, Resource: ResourceWebUI},
				{Action: ActionLogin, Resource: ResourceWebUI},
				{Action: ActionLogout, Resource: ResourceWebUI},
				{Action: ActionView, Resource: ResourceRepo},
				{Action: ActionView, Resource: ResourceArtifact},
			},
		},
		{
			Name:        "developer",
			Description: "Standard developer access",
			Permissions: []Permission{
				{Action: ActionPull, Resource: ResourceImage},
				{Action: ActionPush, Resource: ResourceImage},
				{Action: ActionView, Resource: ResourceTag},
				{Action: ActionCreate, Resource: ResourceTag},
				{Action: ActionView, Resource: ResourceWebUI},
				{Action: ActionLogin, Resource: ResourceWebUI},
				{Action: ActionLogout, Resource: ResourceWebUI},
				{Action: ActionMigrate, Resource: ResourceTask},
				{Action: ActionView, Resource: ResourceRepo},
				{Action: ActionView, Resource: ResourceArtifact},
				{Action: ActionDownload, Resource: ResourceArtifact},
				{Action: ActionUpload, Resource: ResourceArtifact},
			},
		},
		{
			Name:        "administrator",
			Description: "Full system access",
			Permissions: []Permission{
				{Action: ActionAdmin, Resource: ResourceSystem},
			},
		},
	}
}

type GlobalView struct {
	TotalImages int64            `json:"total_images"`
	TotalSize   int64            `json:"total_size"`
	Images      []*ImageMetadata `json:"images"`
}

type ImageMetadata struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Tags      []string          `json:"tags"`
	Size      int64             `json:"size"`
	Owner     string            `json:"owner"`
	Labels    map[string]string `json:"labels"`
	Private   bool              `json:"private"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
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
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	Private     bool      `json:"private"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Artifact struct {
	ID         string            `json:"id"`
	RepoID     int               `json:"repo_id"`
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	Size       int64             `json:"size"`
	MimeType   string            `json:"mime_type"`
	Metadata   string            `json:"metadata"`
	Properties map[string]string `json:"properties"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

type VisibilityUpdateRequest struct {
	ID      string `json:"id"`
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

func (c CustomClaims) Valid() error {
	return c.RegisteredClaims.Valid()
}

// SETTINGS STUFF
const (
	SettingKeyArtifacts = "artifacts"
	SettingKeyRegistry  = "registry"
	SettingKeyAuth      = "auth"
)

type ArtifactSettings struct {
	Retention  RetentionPolicy  `json:"retention"`
	Storage    StorageConfig    `json:"storage"`
	Properties PropertiesConfig `json:"properties"`
	Search     SearchConfig     `json:"search"`
}

// DEFINES ARTIFACT CLEANUP POLICY
type RetentionPolicy struct {
	Enabled       bool `json:"enabled"`
	MaxVersions   int  `json:"maxVersions"`
	MaxAge        int  `json:"maxAge"` // IN DAYS
	ExcludeLatest bool `json:"excludeLatest"`
}

// DEFINES STORAGE RELATED POLICIES
type StorageConfig struct {
	MaxFileSize        int64    `json:"maxFileSize"` // IN MB
	AllowedTypes       []string `json:"allowedTypes"`
	CompressionEnabled bool     `json:"compressionEnabled"`
}

// DEFINES ARTIFACT PROPERTY SETTINGS
type PropertiesConfig struct {
	Required []string `json:"required"`
	Indexed  []string `json:"indexed"`
}

// DEFINES SEARCH RELATED SETTINGS
type SearchConfig struct {
	MaxResults   int    `json:"maxResults"`
	DefaultSort  string `json:"defaultSort"`
	DefaultOrder string `json:"defaultOrder"`
}

// ARTIFACT SETTING VALIDATION
func (s *ArtifactSettings) Validate() error {
	if s.Retention.MaxVersions < 0 {
		return fmt.Errorf("maxVersions cannot be negative")
	}
	if s.Retention.MaxAge < 0 {
		return fmt.Errorf("maxAge cannot be negative")
	}
	if s.Storage.MaxFileSize < 0 {
		return fmt.Errorf("maxFileSize cannot be negative")
	}
	if len(s.Properties.Required) == 0 {
		return fmt.Errorf("at least one required property must be specified")
	}
	if s.Search.MaxResults <= 0 {
		return fmt.Errorf("maxResults must be positive")
	}
	return nil
}

// REGISTRY RELATED SETTINGS
type RegistrySettings struct {
	Cleanup CleanupPolicy `json:"cleanup"`
	Proxy   ProxyConfig   `json:"proxy"`
	Storage StorageConfig `json:"storage"`
}

type CleanupPolicy struct {
	Enabled     bool `json:"enabled"`
	MaxAge      int  `json:"maxAge"` // DAYS
	UnusedOnly  bool `json:"unusedOnly"`
	MinVersions int  `json:"minVersions"` // MIN VERSIONS TO KEEP
}

type ProxyConfig struct {
	Enabled      bool   `json:"enabled"`
	RemoteURL    string `json:"remoteUrl"`
	CacheEnabled bool   `json:"cacheEnabled"`
	CacheMaxAge  int    `json:"cacheMaxAge"` // HOURS
}

func (s *RegistrySettings) Validate() error {
	if s.Cleanup.MaxAge < 0 {
		return fmt.Errorf("maxAge cannot be negative")
	}
	if s.Cleanup.MinVersions < 0 {
		return fmt.Errorf("minVersions cannot be negative")
	}
	if s.Proxy.CacheMaxAge < 0 {
		return fmt.Errorf("cacheMaxAge cannot be negative")
	}
	if s.Proxy.Enabled && s.Proxy.RemoteURL == "" {
		return fmt.Errorf("remoteUrl is required when proxy is enabled")
	}
	return nil
}

// AUTH RELATED SETTINGS
type AuthSettings struct {
	TokenExpiry    int      `json:"tokenExpiry"`    // MINUTES
	SessionTimeout int      `json:"sessionTimeout"` // MINUTES
	PasswordPolicy PwPolicy `json:"passwordPolicy"`
	AllowAnonymous bool     `json:"allowAnonymous"`
}

type PwPolicy struct {
	MinLength      int  `json:"minLength"`
	RequireUpper   bool `json:"requireUpper"`
	RequireLower   bool `json:"requireLower"`
	RequireNumber  bool `json:"requireNumber"`
	RequireSpecial bool `json:"requireSpecial"`
}

func (s *AuthSettings) Validate() error {
	if s.TokenExpiry <= 0 {
		return fmt.Errorf("tokenExpiry must be positive")
	}
	if s.SessionTimeout <= 0 {
		return fmt.Errorf("sessionTimeout must be positive")
	}
	if s.PasswordPolicy.MinLength < 4 {
		return fmt.Errorf("minimum password length must be at least 4")
	}
	return nil
}

var DefaultSettings = map[string]interface{}{
	SettingKeyArtifacts: ArtifactSettings{
		Retention: RetentionPolicy{
			Enabled:       false,
			MaxVersions:   5,
			MaxAge:        30,
			ExcludeLatest: true,
		},
		Storage: StorageConfig{
			MaxFileSize:        1024,
			AllowedTypes:       []string{"*/*"},
			CompressionEnabled: true,
		},
		Properties: PropertiesConfig{
			Required: []string{"version", "build", "branch"},
			Indexed:  []string{"version", "build", "branch", "commit"},
		},
		Search: SearchConfig{
			MaxResults:   100,
			DefaultSort:  "created",
			DefaultOrder: "desc",
		},
	},
	SettingKeyRegistry: RegistrySettings{
		Cleanup: CleanupPolicy{
			Enabled:     false,
			MaxAge:      90,
			UnusedOnly:  true,
			MinVersions: 1,
		},
		Proxy: ProxyConfig{
			Enabled:      false,
			RemoteURL:    "",
			CacheEnabled: true,
			CacheMaxAge:  24,
		},
		Storage: StorageConfig{
			MaxFileSize:        10240,
			AllowedTypes:       []string{"application/vnd.docker.distribution.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"},
			CompressionEnabled: true,
		},
	},
	SettingKeyAuth: AuthSettings{
		TokenExpiry:    60,
		SessionTimeout: 1440,
		PasswordPolicy: PwPolicy{
			MinLength:      8,
			RequireUpper:   true,
			RequireLower:   true,
			RequireNumber:  true,
			RequireSpecial: false,
		},
		AllowAnonymous: false,
	},
}

// SETTINGS INTERFACE FOR VALIDATION
type Settings interface {
	Validate() error
}

func GetDefaultSettings(key string) (interface{}, error) {
	if val, ok := DefaultSettings[key]; ok {
		return val, nil
	}
	return nil, fmt.Errorf("unknown settings key: %s", key)
}
