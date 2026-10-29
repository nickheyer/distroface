package db

import (
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Organization role constants
const (
	OrgRoleOwner  = "owner"
	OrgRoleAdmin  = "admin"
	OrgRoleMember = "member"
)

type User struct {
	ID                 string     `json:"id" gorm:"primaryKey"`
	Username           string     `json:"username" gorm:"not null;uniqueIndex:idx_user_provider"`
	Email              *string    `json:"email" gorm:"uniqueIndex:idx_user_email,where:email IS NOT NULL AND email <> ''"`
	PasswordHash       string     `json:"-" gorm:"column:password_hash"`
	DisplayName        string     `json:"display_name"`
	AuthProvider       string     `json:"auth_provider" gorm:"not null;default:'local';uniqueIndex:idx_user_provider"`
	OIDCSubject        string     `json:"oidc_subject" gorm:"column:oidc_subject;uniqueIndex:idx_oidc_identity,where:oidc_subject != ''"`
	OIDCIssuer         string     `json:"oidc_issuer" gorm:"column:oidc_issuer;uniqueIndex:idx_oidc_identity,where:oidc_subject != ''"`
	IsActive           bool       `json:"is_active" gorm:"not null;default:true"`
	MustChangePassword bool       `json:"must_change_password" gorm:"not null;default:false;column:must_change_password"`
	LastLogin          *time.Time `json:"last_login" gorm:"column:last_login"`
	EmailVerifiedAt    *time.Time `json:"email_verified_at" gorm:"column:email_verified_at"`
	AvatarURL          string     `json:"avatar_url" gorm:"not null;default:'';column:avatar_url"`
	CreatedAt          time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
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

type SystemSetting struct { // Internal state like the jwt secret, never user config
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"not null"`
}

type SettingsRow struct { // One protojson settings document per scope tier
	ScopeType int32     `gorm:"primaryKey;column:scope_type"`
	ScopeID   string    `gorm:"primaryKey;default:'';column:scope_id"`
	Value     string    `gorm:"type:text;not null"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
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
	ScopeType  int32      `json:"scope_type" gorm:"not null;default:0;column:scope_type"` // Zero means the full identity
	ScopeID    string     `json:"scope_id" gorm:"not null;default:'';index;column:scope_id"`
	ReadOnly   bool       `json:"read_only" gorm:"not null;default:false;column:read_only"`
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
	Source    string        `json:"source" gorm:"not null;default:'local'"`
	CreatedAt time.Time     `json:"created_at" gorm:"autoCreateTime"`
	Org       *Organization `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
	User      *User         `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type Repository struct {
	ID              string            `json:"id" gorm:"primaryKey"`
	Namespace       string            `json:"namespace" gorm:"uniqueIndex:idx_namespace_name;not null"`
	Name            string            `json:"name" gorm:"uniqueIndex:idx_namespace_name;not null"`
	Description     string            `json:"description"`
	OwnerID         string            `json:"owner_id" gorm:"index"`
	IsPrivate       bool              `json:"is_private" gorm:"default:false"`
	IsOrgNamespace  bool              `json:"is_org_namespace" gorm:"default:false"`
	PullCount       int64             `json:"pull_count" gorm:"default:0"`
	PushCount       int64             `json:"push_count" gorm:"default:0"`
	LastPush        *time.Time        `json:"last_push"`
	Type            v1.RepositoryType `json:"type" gorm:"not null;default:1"`
	MirrorConfig    string            `json:"-" gorm:"type:text;not null;default:'';column:mirror_config"` // Protojson, token sealed for upstream auth
	MirrorState     string            `json:"-" gorm:"type:text;not null;default:'';column:mirror_state"`  // Sync cursor and cooldown bookkeeping
	MirrorLastSync  *time.Time        `json:"mirror_last_sync" gorm:"column:mirror_last_sync"`
	MirrorLastError string            `json:"mirror_last_error" gorm:"column:mirror_last_error"`
	GitRepoID       *string           `json:"git_repo_id" gorm:"index;column:git_repo_id"` // Source repository link
	CreatedAt       time.Time         `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time         `json:"updated_at" gorm:"autoUpdateTime"`
}

// Star target kinds
const (
	StarKindImage    = "image"
	StarKindGit      = "git"
	StarKindArtifact = "artifact"
)

// One user's star on an image, git, or artifact repository
type Star struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	UserID    string    `json:"user_id" gorm:"not null;uniqueIndex:idx_star_user_target;column:user_id"`
	Kind      string    `json:"kind" gorm:"not null;default:'image';uniqueIndex:idx_star_user_target;index:idx_star_target,priority:1"`
	TargetID  string    `json:"target_id" gorm:"not null;default:'';uniqueIndex:idx_star_user_target;index:idx_star_target,priority:2;column:target_id"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	User      *User     `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// Uploaded profile image of a local account
type UserAvatar struct {
	UserID      string    `gorm:"primaryKey;column:user_id"`
	ContentType string    `gorm:"not null;column:content_type"`
	Data        []byte    `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
	User        *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// Activity target kinds
const (
	ActivityTargetGit      = "git"
	ActivityTargetImage    = "image"
	ActivityTargetArtifact = "artifact"
	ActivityTargetOrg      = "org"
)

// One thing someone did, the feed reads these newest first
type ActivityEvent struct {
	ID         string    `gorm:"primaryKey"`
	ActorID    string    `gorm:"not null;default:'';index:idx_activity_actor,priority:1;column:actor_id"`
	Actor      string    `gorm:"not null;default:''"`
	Kind       string    `gorm:"not null;index"`
	TargetKind string    `gorm:"not null;default:'';index:idx_activity_target,priority:1;column:target_kind"`
	TargetID   string    `gorm:"not null;default:'';index:idx_activity_target,priority:2;column:target_id"`
	Namespace  string    `gorm:"not null;default:'';index:idx_activity_namespace,priority:1"`
	Name       string    `gorm:"not null;default:''"`
	Subject    string    `gorm:"not null;default:''"`
	Summary    string    `gorm:"type:text;not null;default:''"`
	Detail     string    `gorm:"type:text;not null;default:'{}'"`
	IsPublic   bool      `gorm:"not null;default:true;index:idx_activity_public,priority:1;column:is_public"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index:idx_activity_actor,priority:2;index:idx_activity_namespace,priority:2;index:idx_activity_public,priority:2"`
}

// Webhook scope constants
const (
	WebhookScopeRepository    = "repository"
	WebhookScopeOrganization  = "organization"
	WebhookScopeGitRepository = "git_repository"
)

type Webhook struct {
	ID              string         `json:"id" gorm:"primaryKey"`
	Scope           string         `json:"scope" gorm:"not null"`
	RepoID          *string        `json:"repo_id" gorm:"index;column:repo_id"`
	OrgID           *string        `json:"org_id" gorm:"index;column:org_id"`
	GitRepoID       *string        `json:"git_repo_id" gorm:"index;column:git_repo_id"`
	URL             string         `json:"url" gorm:"not null"`
	Secret          string         `json:"-" gorm:"column:secret"` // Plaintext secret needed for hmac signing
	Events          string         `json:"events" gorm:"not null"` // JSON array: ["push","pull","delete"]
	Active          bool           `json:"active" gorm:"not null;default:true"`
	ContentType     string         `json:"content_type" gorm:"not null;default:'application/json'"`
	PayloadTemplate string         `json:"payload_template" gorm:"type:text"`
	CreatedBy       string         `json:"created_by" gorm:"not null;column:created_by"`
	CreatedAt       time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
	Repo            *Repository    `json:"-" gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
	Org             *Organization  `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
	GitRepo         *GitRepository `json:"-" gorm:"foreignKey:GitRepoID;constraint:OnDelete:CASCADE"`
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

type RegistryPortal struct { // Alternate org-owned registry host and/or proxy port
	ID                 string        `json:"id" gorm:"primaryKey"`
	OrgID              string        `json:"org_id" gorm:"not null;index;column:org_id"`
	Name               string        `json:"name" gorm:"not null"`
	Hostname           string        `json:"hostname" gorm:"not null;index"` // Empty matches any host on Port
	Port               int           `json:"port" gorm:"not null;index"`     // 0 serves on the main port only, ports may be shared
	MapUnqualified     bool          `json:"map_unqualified" gorm:"not null"`
	Rules              string        `json:"rules" gorm:"not null;default:'[]'"` // JSON array of {pattern, replace}
	AllowPush          bool          `json:"allow_push" gorm:"not null"`
	RequireAuth        bool          `json:"require_auth" gorm:"not null"`
	TLS                bool          `json:"tls" gorm:"not null;default:false;column:tls"`                                   // Https enforced for this portal, cleartext requests redirect
	CertSource         v1.CertSource `json:"cert_source" gorm:"not null;default:1;column:cert_source"`                       // How the serving certificate materializes
	HidePrimaryLink    bool          `json:"hide_primary_link" gorm:"not null;default:false;column:hide_primary_link"`       // Portal UI drops the exit link
	BackendURL         string        `json:"backend_url" gorm:"not null;default:'';column:backend_url"`                      // Set proxies all portal traffic to this local service
	BackendRewriteHost bool          `json:"backend_rewrite_host" gorm:"not null;default:false;column:backend_rewrite_host"` // Send the backend's host instead of the client's
	BackendInsecure    bool          `json:"backend_insecure" gorm:"not null;default:false;column:backend_insecure"`         // Skip tls verification for an https backend
	SSHPort            int           `json:"ssh_port" gorm:"not null;default:0;column:ssh_port"`                             // Dedicated git ssh listener, 0 none
	PublicHost         string        `json:"public_host" gorm:"not null;default:'';column:public_host"`                      // Host[:port] used in links when a proxy exposes the portal elsewhere
	Enabled            bool          `json:"enabled" gorm:"not null"`
	CreatedAt          time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
	Org                *Organization `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type TLSCertificate struct { // Uploaded pem material, keys never leave the db
	ID        string      `json:"id" gorm:"primaryKey"`
	Scope     v1.TLSScope `json:"scope" gorm:"not null;uniqueIndex:idx_tls_cert_target"`
	OrgID     string      `json:"org_id" gorm:"not null;default:'';uniqueIndex:idx_tls_cert_target;column:org_id"`
	PortalID  string      `json:"portal_id" gorm:"not null;default:'';uniqueIndex:idx_tls_cert_target;column:portal_id"`
	CertPEM   string      `json:"-" gorm:"type:text;not null;column:cert_pem"`
	KeyPEM    string      `json:"-" gorm:"type:text;not null;column:key_pem"`
	CreatedBy string      `json:"created_by" gorm:"column:created_by"`
	CreatedAt time.Time   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time   `json:"updated_at" gorm:"autoUpdateTime"`
}

type ArtifactRepository struct { // Generic artifact repo, integer PK kept for v1 parity
	ID              int64               `json:"id" gorm:"primaryKey;autoIncrement"`
	Namespace       string              `json:"namespace" gorm:"not null;default:'';uniqueIndex:idx_artifact_repo_namespace_name;column:namespace"` // Org name or owner username
	Name            string              `json:"name" gorm:"not null;uniqueIndex:idx_artifact_repo_namespace_name"`
	Description     string              `json:"description"`
	OwnerID         string              `json:"owner_id" gorm:"index;column:owner_id"`
	IsPrivate       bool                `json:"private" gorm:"not null;default:false"`
	Type            v1.ArtifactRepoType `json:"type" gorm:"not null;default:1"`
	MirrorConfig    string              `json:"-" gorm:"type:text;not null;default:'';column:mirror_config"` // Protojson, token sealed for upstream auth
	MirrorState     string              `json:"-" gorm:"type:text;not null;default:'';column:mirror_state"`  // Sync cursor and cooldown bookkeeping
	MirrorLastSync  *time.Time          `json:"mirror_last_sync" gorm:"column:mirror_last_sync"`
	MirrorLastError string              `json:"mirror_last_error" gorm:"column:mirror_last_error"`
	GitRepoID       *string             `json:"git_repo_id" gorm:"index;column:git_repo_id"` // Source repository link
	CreatedAt       time.Time           `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time           `json:"updated_at" gorm:"autoUpdateTime"`
}

type Artifact struct {
	ID         string              `json:"id" gorm:"primaryKey"`
	RepoID     int64               `json:"repo_id" gorm:"not null;index;uniqueIndex:idx_artifact_identity;column:repo_id"`
	Name       string              `json:"name" gorm:"not null"`
	Path       string              `json:"path" gorm:"not null;uniqueIndex:idx_artifact_identity"`
	UploadID   string              `json:"upload_id" gorm:"not null;column:upload_id"`
	Version    string              `json:"version" gorm:"not null;uniqueIndex:idx_artifact_identity"`
	PropsHash  string              `json:"-" gorm:"not null;default:'';uniqueIndex:idx_artifact_identity;column:props_hash"` // Property set fingerprint, fourth identity component
	Digest     string              `json:"digest" gorm:"not null;index"`                                                     // Full sha256 content address
	Size       int64               `json:"size" gorm:"not null"`
	MimeType   string              `json:"mime_type" gorm:"column:mime_type"`
	Metadata   string              `json:"metadata" gorm:"type:text;not null;default:'{}'"` // Arbitrary JSON object
	CreatedAt  time.Time           `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time           `json:"updated_at" gorm:"autoUpdateTime"`
	Properties map[string]string   `json:"properties" gorm:"-"` // Loaded from artifact_properties
	Repo       *ArtifactRepository `json:"-" gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type ArtifactProperty struct {
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	ArtifactID string    `json:"artifact_id" gorm:"not null;uniqueIndex:idx_prop_artifact_key;column:artifact_id"`
	Key        string    `json:"key" gorm:"not null;uniqueIndex:idx_prop_artifact_key;index:idx_prop_key_value"`
	Value      string    `json:"value" gorm:"not null;index:idx_prop_key_value"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
	Artifact   *Artifact `json:"-" gorm:"foreignKey:ArtifactID;constraint:OnDelete:CASCADE"`
}

type CertificateDomain struct { // Allowlist and approval entry for a portal hostname
	ID         string                    `json:"id" gorm:"primaryKey"`
	Domain     string                    `json:"domain" gorm:"not null;uniqueIndex"`
	Scope      v1.CertificateDomainScope `json:"scope" gorm:"not null;default:1"`
	OrgID      *string                   `json:"org_id" gorm:"index;column:org_id"`
	Approved   bool                      `json:"approved" gorm:"not null;default:false"`
	ApprovedBy string                    `json:"approved_by" gorm:"column:approved_by"`
	CreatedBy  string                    `json:"created_by" gorm:"not null;column:created_by"`
	CreatedAt  time.Time                 `json:"created_at" gorm:"autoCreateTime"`
	Org        *Organization             `json:"-" gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type ACMECacheEntry struct { // Autocert cache row holding certs and account state
	Key       string    `gorm:"primaryKey"`
	Data      []byte    `gorm:"not null"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

type ACMEAccount struct { // Built in acme server account keyed by jwk thumbprint
	ID            string    `gorm:"primaryKey"` // RFC 7638 thumbprint
	PublicKeyJSON string    `gorm:"type:text;not null;column:public_key_json"`
	Contacts      string    `gorm:"type:text;not null;default:'[]'"` // JSON array
	Status        string    `gorm:"not null;default:'valid'"`
	CreatedAt     time.Time `gorm:"autoCreateTime"`
}

type AuditEvent struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	Actor     string    `json:"actor" gorm:"index"`
	ActorID   string    `json:"actor_id" gorm:"column:actor_id"`
	SourceIP  string    `json:"source_ip" gorm:"column:source_ip"`
	Action    string    `json:"action" gorm:"not null;index"`
	Resource  string    `json:"resource" gorm:"index"`
	Outcome   string    `json:"outcome" gorm:"not null"`
	Detail    string    `json:"detail" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;index"`
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

// ── Git repositories ─────────────────────────────────────────────────────

// Repository unit names shared by teams, collaborators, and access checks
const (
	GitUnitCode     = "code"
	GitUnitIssues   = "issues"
	GitUnitPulls    = "pulls"
	GitUnitReleases = "releases"
	GitUnitWiki     = "wiki"
	GitUnitProjects = "projects"
	GitUnitPackages = "packages"
	GitUnitActions  = "actions"
	GitUnitAgents   = "agents"
)

// Collaborator and team level names
const (
	GitLevelNone  = "none"
	GitLevelRead  = "read"
	GitLevelWrite = "write"
	GitLevelAdmin = "admin"
)

type GitRepository struct {
	ID              string         `json:"id" gorm:"primaryKey"`
	Namespace       string         `json:"namespace" gorm:"uniqueIndex:idx_git_namespace_name;not null"`
	Name            string         `json:"name" gorm:"uniqueIndex:idx_git_namespace_name;not null"`
	Description     string         `json:"description"`
	Website         string         `json:"website"`
	OwnerID         string         `json:"owner_id" gorm:"index;column:owner_id"`
	IsPrivate       bool           `json:"is_private" gorm:"not null;default:false"`
	IsOrgNamespace  bool           `json:"is_org_namespace" gorm:"not null;default:false"`
	DefaultBranch   string         `json:"default_branch" gorm:"not null;default:'main'"`
	ObjectFormat    string         `json:"object_format" gorm:"not null;default:'sha1'"`
	Type            v1.GitRepoType `json:"type" gorm:"not null;default:1"`
	ForkOfID        *string        `json:"fork_of_id" gorm:"index;column:fork_of_id"`
	TemplateOfID    *string        `json:"template_of_id" gorm:"column:template_of_id"`
	IsArchived      bool           `json:"is_archived" gorm:"not null;default:false"`
	Units           string         `json:"units" gorm:"type:text;not null;default:'{}'"` // Protojson GitRepoUnits
	LFSEnabled      bool           `json:"lfs_enabled" gorm:"not null;default:true;column:lfs_enabled"`
	MergeSettings   string         `json:"merge_settings" gorm:"type:text;not null;default:'{}'"` // Protojson GitMergeSettings
	IssueCounter    int64          `json:"issue_counter" gorm:"not null;default:0"`
	RunCounter      int64          `json:"run_counter" gorm:"not null;default:0"`
	ArtifactCounter int64          `json:"artifact_counter" gorm:"not null;default:0"` // Numbers run artifacts per repository
	AgentCounter    int64          `json:"agent_counter" gorm:"not null;default:0;column:agent_counter"`
	SizeBytes       int64          `json:"size_bytes" gorm:"not null;default:0"`
	LFSSizeBytes    int64          `json:"lfs_size_bytes" gorm:"not null;default:0;column:lfs_size_bytes"`
	BranchCount     int32          `json:"branch_count" gorm:"not null;default:0"`
	TagCount        int32          `json:"tag_count" gorm:"not null;default:0"`
	PullCount       int64          `json:"pull_count" gorm:"not null;default:0"`
	PushCount       int64          `json:"push_count" gorm:"not null;default:0"`
	LastPush        *time.Time     `json:"last_push"`
	MirrorConfig    string         `json:"-" gorm:"type:text;not null;default:'';column:mirror_config"`
	MirrorState     string         `json:"-" gorm:"type:text;not null;default:'';column:mirror_state"`
	MirrorLastSync  *time.Time     `json:"mirror_last_sync" gorm:"column:mirror_last_sync"`
	MirrorLastError string         `json:"mirror_last_error" gorm:"column:mirror_last_error"`
	CreatedAt       time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
}

type GitRedirect struct {
	ID        string         `gorm:"primaryKey"`
	Namespace string         `gorm:"not null;uniqueIndex:idx_git_redirect"`
	Name      string         `gorm:"not null;uniqueIndex:idx_git_redirect"`
	RepoID    string         `gorm:"not null;index"`
	CreatedAt time.Time      `gorm:"autoCreateTime"`
	Repo      *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitTransfer struct {
	ID          string         `gorm:"primaryKey"`
	RepoID      string         `gorm:"not null;uniqueIndex"`
	ToNamespace string         `gorm:"not null;index"`
	NewName     string         `gorm:"not null"`
	RequestedBy string         `gorm:"not null"`
	ExpiresAt   time.Time      `gorm:"not null"`
	CreatedAt   time.Time      `gorm:"autoCreateTime"`
	Repo        *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitTopic struct {
	RepoID string         `gorm:"primaryKey"`
	Topic  string         `gorm:"primaryKey;index"`
	Repo   *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitWatch struct {
	ID        string         `gorm:"primaryKey"`
	UserID    string         `gorm:"not null;uniqueIndex:idx_git_watch_user_repo"`
	RepoID    string         `gorm:"not null;uniqueIndex:idx_git_watch_user_repo;index"`
	Mode      string         `gorm:"not null;default:'participating'"` // all, participating, ignore
	CreatedAt time.Time      `gorm:"autoCreateTime"`
	User      *User          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Repo      *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitCollaborator struct {
	ID         string         `gorm:"primaryKey"`
	RepoID     string         `gorm:"not null;uniqueIndex:idx_git_collab_repo_user"`
	UserID     string         `gorm:"not null;uniqueIndex:idx_git_collab_repo_user;index"`
	Permission string         `gorm:"not null;default:'read'"`
	Accepted   bool           `gorm:"not null;default:false"`
	CreatedAt  time.Time      `gorm:"autoCreateTime"`
	Repo       *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
	User       *User          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type OrgTeam struct {
	ID               string `gorm:"primaryKey"`
	OrgID            string `gorm:"not null;uniqueIndex:idx_team_org_name"`
	Name             string `gorm:"not null;uniqueIndex:idx_team_org_name"`
	Description      string
	IsDefault        bool          `gorm:"not null;default:false"`
	CanCreateRepos   bool          `gorm:"not null;default:false"`
	IncludesAllRepos bool          `gorm:"not null;default:false"`
	AdminAccess      bool          `gorm:"not null;default:false"`
	Units            string        `gorm:"type:text;not null;default:'{}'"` // JSON unit name to level
	CreatedAt        time.Time     `gorm:"autoCreateTime"`
	UpdatedAt        time.Time     `gorm:"autoUpdateTime"`
	Org              *Organization `gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type OrgTeamMember struct {
	TeamID string   `gorm:"primaryKey"`
	UserID string   `gorm:"primaryKey;index"`
	Team   *OrgTeam `gorm:"foreignKey:TeamID;constraint:OnDelete:CASCADE"`
	User   *User    `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type OrgTeamRepo struct {
	TeamID string         `gorm:"primaryKey"`
	RepoID string         `gorm:"primaryKey;index"`
	Team   *OrgTeam       `gorm:"foreignKey:TeamID;constraint:OnDelete:CASCADE"`
	Repo   *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

// SSH key owner kinds
const (
	SSHKeyOwnerUser    = "user"
	SSHKeyOwnerGitRepo = "git_repo"
)

type SSHKey struct {
	ID          string `gorm:"primaryKey"`
	OwnerType   string `gorm:"not null;index:idx_sshkey_owner"`
	OwnerID     string `gorm:"not null;index:idx_sshkey_owner"`
	Title       string `gorm:"not null"`
	PublicKey   string `gorm:"type:text;not null"`
	Fingerprint string `gorm:"not null;uniqueIndex"`
	KeyType     string `gorm:"not null"`
	Usage       string `gorm:"not null;default:'auth'"` // auth, signing, both
	ReadOnly    bool   `gorm:"not null;default:true"`
	CreatedBy   string `gorm:"not null"`
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

type GPGKey struct {
	ID          string `gorm:"primaryKey"`
	UserID      string `gorm:"not null;index"`
	KeyID       string `gorm:"not null;index"`
	Fingerprint string `gorm:"not null;uniqueIndex"`
	Armored     string `gorm:"type:text;not null"`
	Emails      string `gorm:"type:text;not null;default:'[]'"` // JSON array
	Verified    bool   `gorm:"not null;default:false"`
	Challenge   string `gorm:"not null;default:''"`
	ExpiresAt   *time.Time
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	User        *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type GitBranchProtection struct {
	ID                     string         `gorm:"primaryKey"`
	RepoID                 string         `gorm:"not null;index"`
	Pattern                string         `gorm:"not null"`
	Priority               int            `gorm:"not null;default:0"`
	BlockForcePush         bool           `gorm:"not null;default:false"`
	BlockDeletion          bool           `gorm:"not null;default:false"`
	RequirePullRequest     bool           `gorm:"not null;default:false"`
	RequiredApprovals      int            `gorm:"not null;default:0"`
	DismissStaleApprovals  bool           `gorm:"not null;default:false"`
	RequireCodeOwnerReview bool           `gorm:"not null;default:false"`
	RequireStatusChecks    bool           `gorm:"not null;default:false"`
	StatusCheckContexts    string         `gorm:"type:text;not null;default:'[]'"`
	RequireSignedCommits   bool           `gorm:"not null;default:false"`
	RequireLinearHistory   bool           `gorm:"not null;default:false"`
	RequireUpToDate        bool           `gorm:"not null;default:false"`
	AllowedPushers         string         `gorm:"type:text;not null;default:'[]'"`
	AllowedMergers         string         `gorm:"type:text;not null;default:'[]'"`
	BypassIDs              string         `gorm:"type:text;not null;default:'[]'"`
	CreatedAt              time.Time      `gorm:"autoCreateTime"`
	UpdatedAt              time.Time      `gorm:"autoUpdateTime"`
	Repo                   *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitTagProtection struct {
	ID             string         `gorm:"primaryKey"`
	RepoID         string         `gorm:"not null;index"`
	Pattern        string         `gorm:"not null"`
	BlockDeletion  bool           `gorm:"not null;default:false"`
	AllowedPushers string         `gorm:"type:text;not null;default:'[]'"`
	CreatedAt      time.Time      `gorm:"autoCreateTime"`
	Repo           *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitCommitStatus struct {
	ID          string `gorm:"primaryKey"`
	RepoID      string `gorm:"not null;index:idx_status_repo_sha"`
	SHA         string `gorm:"not null;index:idx_status_repo_sha;column:sha"`
	Context     string `gorm:"not null"`
	State       string `gorm:"not null"` // pending, success, failure, error
	TargetURL   string `gorm:"column:target_url"`
	Description string
	CreatorID   string         `gorm:"not null"`
	CreatedAt   time.Time      `gorm:"autoCreateTime"`
	Repo        *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type LFSObject struct {
	OID       string    `gorm:"primaryKey;column:oid"`
	Size      int64     `gorm:"not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

type GitLFSLink struct {
	RepoID string         `gorm:"primaryKey"`
	OID    string         `gorm:"primaryKey;index;column:oid"`
	Repo   *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type LFSLock struct {
	ID        string         `gorm:"primaryKey"`
	RepoID    string         `gorm:"not null;uniqueIndex:idx_lfs_lock_repo_path"`
	Path      string         `gorm:"not null;uniqueIndex:idx_lfs_lock_repo_path"`
	OwnerID   string         `gorm:"not null"`
	CreatedAt time.Time      `gorm:"autoCreateTime"`
	Repo      *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitRelease struct {
	ID          string `gorm:"primaryKey"`
	RepoID      string `gorm:"not null;uniqueIndex:idx_release_repo_tag"`
	Tag         string `gorm:"not null;uniqueIndex:idx_release_repo_tag"`
	Name        string
	Body        string `gorm:"type:text"`
	Draft       bool   `gorm:"not null;default:false"`
	Prerelease  bool   `gorm:"not null;default:false"`
	AuthorID    string `gorm:"not null"`
	TargetSHA   string `gorm:"column:target_sha"`
	PublishedAt *time.Time
	CreatedAt   time.Time      `gorm:"autoCreateTime"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime"`
	Repo        *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitReleaseImage struct {
	ID          string      `gorm:"primaryKey"`
	ReleaseID   string      `gorm:"not null;uniqueIndex:idx_release_image"`
	ImageRepoID string      `gorm:"not null;uniqueIndex:idx_release_image"`
	Tag         string      `gorm:"not null;uniqueIndex:idx_release_image"`
	Digest      string      `gorm:"not null"`
	CreatedAt   time.Time   `gorm:"autoCreateTime"`
	Release     *GitRelease `gorm:"foreignKey:ReleaseID;constraint:OnDelete:CASCADE"`
	ImageRepo   *Repository `gorm:"foreignKey:ImageRepoID;constraint:OnDelete:CASCADE"`
}

type GitReleaseAsset struct {
	ID            string      `gorm:"primaryKey"`
	ReleaseID     string      `gorm:"not null;uniqueIndex:idx_release_asset_name;index"`
	Name          string      `gorm:"not null;uniqueIndex:idx_release_asset_name"`
	Label         string      `gorm:"not null;default:''"`
	Size          int64       `gorm:"not null;default:0"`
	Digest        string      `gorm:"not null;index"`
	ContentType   string      `gorm:"not null;default:'application/octet-stream'"`
	UploaderID    string      `gorm:"not null;default:''"`
	DownloadCount int64       `gorm:"not null;default:0"`
	CreatedAt     time.Time   `gorm:"autoCreateTime"`
	UpdatedAt     time.Time   `gorm:"autoUpdateTime"`
	Release       *GitRelease `gorm:"foreignKey:ReleaseID;constraint:OnDelete:CASCADE"`
}

type GitPushMirror struct {
	ID              string `gorm:"primaryKey"`
	RepoID          string `gorm:"not null;index"`
	RemoteURL       string `gorm:"not null;column:remote_url"`
	AuthToken       string `gorm:"not null;default:''"`
	Username        string `gorm:"not null;default:''"`
	UseSSHKey       bool   `gorm:"not null;default:false;column:use_ssh_key"`
	IntervalMinutes int32  `gorm:"not null;default:0"`
	SyncOnPush      bool   `gorm:"not null;default:true"`
	LastSync        *time.Time
	LastError       string
	State           string         `gorm:"type:text;not null;default:''"`
	CreatedAt       time.Time      `gorm:"autoCreateTime"`
	Repo            *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitImport struct {
	RepoID    string `gorm:"primaryKey"`
	Source    string `gorm:"not null"` // github, gitlab, gitea
	Upstream  string `gorm:"not null"`
	Phase     string `gorm:"not null;default:''"`
	State     string `gorm:"type:text;not null;default:'{}'"`
	LastError string
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	Repo      *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

// ── Issues and pull requests ─────────────────────────────────────────────

// Issue state constants
const (
	IssueStateOpen   = "open"
	IssueStateClosed = "closed"
)

type GitIssue struct {
	ID           string  `gorm:"primaryKey"`
	RepoID       string  `gorm:"not null;uniqueIndex:idx_issue_repo_number;index:idx_issue_repo_state"`
	Number       int64   `gorm:"not null;uniqueIndex:idx_issue_repo_number"`
	IsPull       bool    `gorm:"not null;index"`
	Title        string  `gorm:"not null"`
	Body         string  `gorm:"type:text"`
	State        string  `gorm:"not null;default:'open';index:idx_issue_repo_state"`
	AuthorID     string  `gorm:"not null;index"`
	MilestoneID  *string `gorm:"index"`
	Locked       bool    `gorm:"not null;default:false"`
	LockReason   string
	Pinned       int `gorm:"not null;default:0"`
	DueDate      *time.Time
	ClosedAt     *time.Time
	ClosedBy     string
	CommentCount int            `gorm:"not null;default:0"`
	CreatedAt    time.Time      `gorm:"autoCreateTime;index"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime;index"`
	Repo         *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitPullRequest struct {
	IssueID              string `gorm:"primaryKey"`
	HeadRepoID           string `gorm:"not null;index"`
	HeadBranch           string `gorm:"not null;index"`
	HeadSHA              string `gorm:"not null;column:head_sha"`
	BaseBranch           string `gorm:"not null;index"`
	MergeBaseSHA         string `gorm:"column:merge_base_sha"`
	Draft                bool   `gorm:"not null;default:false"`
	MergeableState       string `gorm:"not null;default:'unknown'"`
	ConflictState        string `gorm:"not null;default:'unknown'"` // Merge simulation result alone, unknown until run
	ConflictPaths        string `gorm:"type:text;not null;default:'[]'"`
	ChangedFiles         int    `gorm:"not null;default:0"`
	Additions            int    `gorm:"not null;default:0"`
	Deletions            int    `gorm:"not null;default:0"`
	CommitCount          int    `gorm:"not null;default:0"`
	AllowMaintainerEdits bool   `gorm:"not null;default:true"`
	AutoMergeMethod      string `gorm:"not null;default:''"`
	AutoMergeBy          string `gorm:"not null;default:''"`
	AutoMergeTitle       string `gorm:"not null;default:''"`
	AutoMergeMessage     string `gorm:"type:text;not null;default:''"`
	AutoMergeDelete      bool   `gorm:"not null;default:false"`
	MergedAt             *time.Time
	MergedBy             string    `gorm:"not null;default:''"`
	MergeCommitSHA       string    `gorm:"not null;default:'';column:merge_commit_sha"`
	MergeMethod          string    `gorm:"not null;default:''"`
	HeadDeleted          bool      `gorm:"not null;default:false"`
	Issue                *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

type GitLabel struct {
	ID             string  `gorm:"primaryKey"`
	RepoID         *string `gorm:"index"`
	OrgID          *string `gorm:"index"`
	Name           string  `gorm:"not null"`
	Color          string  `gorm:"not null"`
	Description    string
	ExclusiveScope string         `gorm:"not null;default:''"`
	CreatedAt      time.Time      `gorm:"autoCreateTime"`
	Repo           *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
	Org            *Organization  `gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type GitIssueLabel struct {
	IssueID string    `gorm:"primaryKey"`
	LabelID string    `gorm:"primaryKey;index"`
	Issue   *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	Label   *GitLabel `gorm:"foreignKey:LabelID;constraint:OnDelete:CASCADE"`
}

type GitMilestone struct {
	ID          string `gorm:"primaryKey"`
	RepoID      string `gorm:"not null;index"`
	Title       string `gorm:"not null"`
	Description string `gorm:"type:text"`
	DueDate     *time.Time
	State       string `gorm:"not null;default:'open'"`
	ClosedAt    *time.Time
	CreatedAt   time.Time      `gorm:"autoCreateTime"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime"`
	Repo        *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type GitIssueAssignee struct {
	IssueID string    `gorm:"primaryKey"`
	UserID  string    `gorm:"primaryKey;index"`
	Issue   *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	User    *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type GitComment struct {
	ID        string  `gorm:"primaryKey"`
	IssueID   string  `gorm:"not null;index"`
	AuthorID  string  `gorm:"not null"`
	Type      string  `gorm:"not null;default:'comment'"`
	Body      string  `gorm:"type:text"`
	Payload   string  `gorm:"type:text;not null;default:'{}'"`
	ReviewID  *string `gorm:"index"`
	ViaEmail  bool    `gorm:"not null;default:false"`
	EditedAt  *time.Time
	CreatedAt time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
	Issue     *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

type GitReaction struct {
	ID         string    `gorm:"primaryKey"`
	TargetType string    `gorm:"not null;uniqueIndex:idx_reaction"` // issue, comment, review_comment
	TargetID   string    `gorm:"not null;uniqueIndex:idx_reaction;index"`
	UserID     string    `gorm:"not null;uniqueIndex:idx_reaction"`
	Emoji      string    `gorm:"not null;uniqueIndex:idx_reaction"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
}

type GitIssueDependency struct {
	IssueID     string    `gorm:"primaryKey"`
	DependsOnID string    `gorm:"primaryKey;index"`
	Issue       *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	DependsOn   *GitIssue `gorm:"foreignKey:DependsOnID;constraint:OnDelete:CASCADE"`
}

type GitTrackedTime struct {
	ID        string `gorm:"primaryKey"`
	IssueID   string `gorm:"not null;index"`
	UserID    string `gorm:"not null;index"`
	Seconds   int64  `gorm:"not null"`
	Note      string
	CreatedAt time.Time `gorm:"autoCreateTime"`
	Issue     *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

type GitIssueSubscription struct {
	IssueID    string    `gorm:"primaryKey"`
	UserID     string    `gorm:"primaryKey;index"`
	Subscribed bool      `gorm:"not null;default:true"` // False silences a participant
	Issue      *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

type GitReviewRequest struct {
	ID          string    `gorm:"primaryKey"`
	IssueID     string    `gorm:"not null;index"`
	UserID      string    `gorm:"not null;default:''"`
	TeamID      string    `gorm:"not null;default:''"`
	RequestedBy string    `gorm:"not null"`
	Fulfilled   bool      `gorm:"not null;default:false"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	Issue       *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

type GitReview struct {
	ID          string `gorm:"primaryKey"`
	IssueID     string `gorm:"not null;index"`
	ReviewerID  string `gorm:"not null;index"`
	State       string `gorm:"not null;default:'pending'"`
	Body        string `gorm:"type:text"`
	CommitSHA   string `gorm:"not null;default:'';column:commit_sha"`
	Dismissed   bool   `gorm:"not null;default:false"`
	DismissedBy string `gorm:"not null;default:''"`
	Imported    bool   `gorm:"not null;default:false"` // Copied from another forge, never counts toward required reviews
	SubmittedAt *time.Time
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	Issue       *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

type GitReviewComment struct {
	ID          string    `gorm:"primaryKey"`
	ReviewID    *string   `gorm:"index"`
	IssueID     string    `gorm:"not null;index"`
	AuthorID    string    `gorm:"not null"`
	Path        string    `gorm:"not null"`
	Line        int       `gorm:"not null"`
	Side        string    `gorm:"not null;default:'new'"`
	StartLine   int       `gorm:"not null;default:0"`
	DiffHunk    string    `gorm:"type:text"`
	Body        string    `gorm:"type:text"`
	ThreadID    string    `gorm:"not null;index"`
	Resolved    bool      `gorm:"not null;default:false"`
	ResolvedBy  string    `gorm:"not null;default:''"`
	CommitSHA   string    `gorm:"not null;default:'';column:commit_sha"`
	OutdatedSHA string    `gorm:"not null;default:'';column:outdated_sha"`
	Pending     bool      `gorm:"not null;default:false"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
	Issue       *GitIssue `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
}

// ── Projects ─────────────────────────────────────────────────────────────

type GitProject struct {
	ID          string         `gorm:"primaryKey"`
	RepoID      *string        `gorm:"index"`
	OrgID       *string        `gorm:"index"`
	Title       string         `gorm:"not null"`
	Description string         `gorm:"type:text"`
	State       string         `gorm:"not null;default:'open'"`
	Public      bool           `gorm:"not null;default:false"`
	CreatorID   string         `gorm:"not null"`
	CreatedAt   time.Time      `gorm:"autoCreateTime"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime"`
	Repo        *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
	Org         *Organization  `gorm:"foreignKey:OrgID;constraint:OnDelete:CASCADE"`
}

type GitProjectColumn struct {
	ID        string      `gorm:"primaryKey"`
	ProjectID string      `gorm:"not null;index"`
	Title     string      `gorm:"not null"`
	Color     string      `gorm:"not null;default:''"`
	Position  int         `gorm:"not null;default:0"`
	IsDefault bool        `gorm:"not null;default:false"`
	Project   *GitProject `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
}

type GitProjectIssue struct {
	ProjectID string            `gorm:"primaryKey"`
	IssueID   string            `gorm:"primaryKey;index"`
	ColumnID  string            `gorm:"not null;index"`
	Position  int               `gorm:"not null;default:0"`
	Project   *GitProject       `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	Issue     *GitIssue         `gorm:"foreignKey:IssueID;constraint:OnDelete:CASCADE"`
	Column    *GitProjectColumn `gorm:"foreignKey:ColumnID;constraint:OnDelete:CASCADE"`
}

// ── Notifications and mail ───────────────────────────────────────────────

type GitNotification struct {
	ID        string         `gorm:"primaryKey"`
	UserID    string         `gorm:"not null;index:idx_notif_user_status"`
	RepoID    string         `gorm:"not null;index"`
	IssueID   *string        `gorm:"index"`
	SHA       string         `gorm:"not null;default:'';column:sha"`
	Kind      string         `gorm:"not null"`
	Title     string         `gorm:"not null;default:''"`
	Reason    string         `gorm:"not null;default:''"`
	Status    string         `gorm:"not null;default:'unread';index:idx_notif_user_status"`
	UpdatedBy string         `gorm:"not null;default:''"`
	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime;index"`
	User      *User          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Repo      *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type UserNotificationPrefs struct {
	UserID              string    `gorm:"primaryKey"`
	WebMode             string    `gorm:"not null;default:'participating'"`
	EmailMode           string    `gorm:"not null;default:'participating'"`
	EmailMentions       bool      `gorm:"not null;default:true"`
	EmailReviewRequests bool      `gorm:"not null;default:true"`
	EmailAssignments    bool      `gorm:"not null;default:true"`
	EmailOwnActivity    bool      `gorm:"not null;default:false"`
	UpdatedAt           time.Time `gorm:"autoUpdateTime"`
	User                *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type MailMessage struct {
	ID        string `gorm:"primaryKey"`
	ScopeType int32  `gorm:"not null;default:1"`
	ScopeID   string `gorm:"not null;default:''"`
	To        string `gorm:"not null"`
	Subject   string `gorm:"not null"`
	Text      string `gorm:"type:text"`
	HTML      string `gorm:"type:text;column:html"`
	Headers   string `gorm:"type:text;not null;default:'{}'"`
	Status    string `gorm:"not null;default:'queued';index"`
	Attempts  int    `gorm:"not null;default:0"`
	LastError string
	NextTry   time.Time `gorm:"index"`
	SentAt    *time.Time
	CreatedAt time.Time `gorm:"autoCreateTime;index"`
}

type MailInboundToken struct {
	TokenHash string    `gorm:"primaryKey"`
	UserID    string    `gorm:"not null;index"`
	IssueID   string    `gorm:"not null;index"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

type PasswordReset struct {
	ID        string    `gorm:"primaryKey"`
	UserID    string    `gorm:"not null;index"`
	TokenHash string    `gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time `gorm:"autoCreateTime"`
	User      *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type EmailVerification struct {
	ID        string    `gorm:"primaryKey"`
	UserID    string    `gorm:"not null;index"`
	Email     string    `gorm:"not null"`
	TokenHash string    `gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time `gorm:"autoCreateTime"`
	User      *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// ── Actions ──────────────────────────────────────────────────────────────

// Actions scope kinds
const (
	ActionsScopeSystem = "system"
	ActionsScopeOrg    = "org"
	ActionsScopeRepo   = "repo"
)

type ActionsRunner struct {
	ID           string `gorm:"primaryKey"`
	ScopeKind    string `gorm:"not null;index:idx_runner_scope"`
	ScopeID      string `gorm:"not null;default:'';index:idx_runner_scope"`
	Name         string `gorm:"not null"`
	Labels       string `gorm:"type:text;not null;default:'[]'"`
	TokenHash    string `gorm:"not null;uniqueIndex"`
	Version      string `gorm:"not null;default:''"`
	Busy         int    `gorm:"not null;default:0"`
	LastSeen     *time.Time
	Builtin      bool      `gorm:"not null;default:false"` // Runs inside the server process
	Capabilities string    `gorm:"type:text;not null;default:'[]'"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
}

type ActionsRunnerToken struct {
	ID        string    `gorm:"primaryKey"`
	ScopeKind string    `gorm:"not null"`
	ScopeID   string    `gorm:"not null;default:''"`
	TokenHash string    `gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedBy string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

type ActionsWorkflowState struct {
	RepoID   string         `gorm:"primaryKey"`
	Path     string         `gorm:"primaryKey"`
	Disabled bool           `gorm:"not null;default:false"`
	Repo     *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type ActionsRun struct {
	ID             string `gorm:"primaryKey"`
	RepoID         string `gorm:"not null;uniqueIndex:idx_run_repo_number;index:idx_run_repo_created"`
	Number         int64  `gorm:"not null;uniqueIndex:idx_run_repo_number"`
	WorkflowPath   string `gorm:"not null;index"`
	WorkflowName   string `gorm:"not null;default:''"`
	Event          string `gorm:"not null"`
	Ref            string `gorm:"not null"`
	SHA            string `gorm:"not null;column:sha;index"`
	TriggerUserID  string `gorm:"not null;default:''"`
	Status         string `gorm:"not null;default:'queued';index"`
	Conclusion     string `gorm:"not null;default:''"`
	NeedsApproval  bool   `gorm:"not null;default:false"`
	FromFork       bool   `gorm:"not null;default:false"` // Head code from a fork, no secrets and a read only token
	SkipCI         bool   `gorm:"not null;default:false;column:skip_ci"`
	PullNumber     int64  `gorm:"not null;default:0"`
	Payload        string `gorm:"type:text;not null;default:'{}'"` // Event payload as json
	Workflow       string `gorm:"type:text;not null;default:''"`   // Workflow source at trigger time
	Concurrency    string `gorm:"not null;default:''"`
	Notes          string `gorm:"type:text;not null;default:'[]'"` // Parser notes shown on the run
	AgentSessionID string `gorm:"not null;default:'';index"`       // Agent session that owns the run
	AgentSlug      string `gorm:"not null;default:''"`
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time      `gorm:"autoCreateTime;index:idx_run_repo_created"`
	Repo           *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type ActionsJob struct {
	ID                   string `gorm:"primaryKey"`
	RunID                string `gorm:"not null;index"`
	Name                 string `gorm:"not null"`
	JobKey               string `gorm:"not null"`                      // Job id plus its matrix suffix
	WorkflowJobID        string `gorm:"not null;default:''"`           // Job id in the workflow file
	WorkflowPayload      string `gorm:"type:text;not null;default:''"` // Single job workflow the runner executes
	RunsOn               string `gorm:"type:text;not null;default:'[]'"`
	Needs                string `gorm:"type:text;not null;default:'[]'"`
	Matrix               string `gorm:"type:text;not null;default:'{}'"`
	MatrixGroup          string `gorm:"not null;default:''"` // Job id shared by every matrix leg
	MaxParallel          int    `gorm:"not null;default:0"`
	FailFast             bool   `gorm:"not null"` // No default, gorm would swap a stored false for it
	ContinueOnError      bool   `gorm:"not null;default:false"`
	TimeoutMinutes       int    `gorm:"not null;default:0"`
	Concurrency          string `gorm:"not null;default:'';index"` // Job level concurrency group
	ConcurrencyCancel    bool   `gorm:"not null;default:false"`
	ConcurrencyEvaluated bool   `gorm:"not null;default:false"`
	ReadOnly             bool   `gorm:"not null;default:false"`        // Permissions grant neither contents nor packages write
	Deferred             bool   `gorm:"not null;default:false"`        // Expands once needed jobs finish
	Error                string `gorm:"type:text;not null;default:''"` // Planning failure, fails the job on advance
	ParentJobID          string `gorm:"not null;default:'';index"`     // Caller this job was expanded from
	IsReusableCaller     bool   `gorm:"not null;default:false"`
	IsExpanded           bool   `gorm:"not null;default:false"`
	CallUses             string `gorm:"not null;default:''"`
	CallSecrets          string `gorm:"type:text;not null;default:''"`   // inherit or json alias to name map
	CallInputs           string `gorm:"type:text;not null;default:'{}'"` // Evaluated with values as json
	CallContent          string `gorm:"type:text;not null;default:''"`   // Called workflow source
	CallSourceRepoID     string `gorm:"not null;default:''"`             // Local repository the called workflow was read from
	CallSourceRepo       string `gorm:"not null;default:''"`             // owner/repo the called workflow came from
	CallSourceRef        string `gorm:"not null;default:''"`             // Commit or ref the called workflow was read at
	Status               string `gorm:"not null;default:'waiting';index"`
	Conclusion           string `gorm:"not null;default:''"`
	RunnerID             string `gorm:"not null;default:'';index"`
	Attempt              int    `gorm:"not null;default:1"`
	Steps                string `gorm:"type:text;not null;default:'[]'"`
	Outputs              string `gorm:"type:text;not null;default:'{}'"`
	Annotations          string `gorm:"type:text;not null;default:'[]'"`
	TokenID              string `gorm:"not null;default:'';index"`
	LogChunks            int    `gorm:"not null;default:0"`
	LogBytes             int64  `gorm:"not null;default:0"`
	LogDigest            string `gorm:"not null;default:''"`    // Compacted log blob after completion
	Interactive          bool   `gorm:"not null;default:false"` // Serves commands over FetchExec after its steps
	CloseRequested       bool   `gorm:"not null;default:false"` // Owner asked the job to finish after the current command
	StartedAt            *time.Time
	FinishedAt           *time.Time
	CreatedAt            time.Time   `gorm:"autoCreateTime"`
	Run                  *ActionsRun `gorm:"foreignKey:RunID;constraint:OnDelete:CASCADE"`
}

type ActionsJobLog struct {
	JobID string      `gorm:"primaryKey"`
	Index int         `gorm:"primaryKey"`
	Data  []byte      `gorm:"not null"`
	Job   *ActionsJob `gorm:"foreignKey:JobID;constraint:OnDelete:CASCADE"`
}

type ActionsSecret struct {
	ID        string    `gorm:"primaryKey"`
	ScopeKind string    `gorm:"not null;uniqueIndex:idx_secret_scope_name"`
	ScopeID   string    `gorm:"not null;default:'';uniqueIndex:idx_secret_scope_name"`
	Name      string    `gorm:"not null;uniqueIndex:idx_secret_scope_name"`
	Sealed    []byte    `gorm:"not null"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

type ActionsVariable struct {
	ID        string    `gorm:"primaryKey"`
	ScopeKind string    `gorm:"not null;uniqueIndex:idx_variable_scope_name"`
	ScopeID   string    `gorm:"not null;default:'';uniqueIndex:idx_variable_scope_name"`
	Name      string    `gorm:"not null;uniqueIndex:idx_variable_scope_name"`
	Value     string    `gorm:"type:text;not null"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

type ActionsArtifact struct {
	ID          string      `gorm:"primaryKey"`
	RunID       string      `gorm:"not null;index;uniqueIndex:idx_actions_artifact_run_name"`
	JobID       string      `gorm:"not null;default:''"`
	Number      int64       `gorm:"not null;default:0;index"` // Per repository id GitHub style clients use
	Name        string      `gorm:"not null;uniqueIndex:idx_actions_artifact_run_name"`
	Digest      string      `gorm:"not null;default:'';index"`
	Size        int64       `gorm:"not null;default:0"`
	ContentType string      `gorm:"not null;default:'application/zip'"`
	Status      string      `gorm:"not null;default:'pending';index"` // pending, confirmed
	ExpiresAt   *time.Time  `gorm:"index"`
	CreatedAt   time.Time   `gorm:"autoCreateTime"`
	Run         *ActionsRun `gorm:"foreignKey:RunID;constraint:OnDelete:CASCADE"`
}

type ActionsRunImage struct {
	ID          string      `gorm:"primaryKey"`
	RunID       string      `gorm:"not null;uniqueIndex:idx_run_image;index"`
	JobID       string      `gorm:"not null;default:''"`
	ImageRepoID string      `gorm:"not null;uniqueIndex:idx_run_image;index"`
	Tag         string      `gorm:"not null;uniqueIndex:idx_run_image"`
	Digest      string      `gorm:"not null"`
	CreatedAt   time.Time   `gorm:"autoCreateTime"`
	Run         *ActionsRun `gorm:"foreignKey:RunID;constraint:OnDelete:CASCADE"`
	ImageRepo   *Repository `gorm:"foreignKey:ImageRepoID;constraint:OnDelete:CASCADE"`
}

type ActionsCache struct {
	ID         int64          `gorm:"primaryKey;autoIncrement"`
	RepoID     string         `gorm:"not null;index:idx_cache_repo_key"`
	Key        string         `gorm:"not null;index:idx_cache_repo_key;column:cache_key"`
	Version    string         `gorm:"not null;default:''"`
	Ref        string         `gorm:"not null;default:''"` // Ref of the run that saved it
	Digest     string         `gorm:"not null;default:'';index"`
	Size       int64          `gorm:"not null;default:0"`
	State      string         `gorm:"not null;default:'reserved';index:idx_cache_state_used,priority:1"` // reserved, complete
	CreatedAt  time.Time      `gorm:"autoCreateTime"`
	LastUsedAt time.Time      `gorm:"not null;index:idx_cache_state_used,priority:2"`
	Repo       *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

// ── Agents ───────────────────────────────────────────────────────────────

// Auth provider of agent bot accounts, they never sign in
const AuthProviderAgent = "agent"

type AgentProvider struct {
	ID                string `gorm:"primaryKey"`
	ScopeKind         string `gorm:"not null;index:idx_agent_provider_scope"`
	ScopeID           string `gorm:"not null;default:'';index:idx_agent_provider_scope"`
	Name              string `gorm:"not null"`
	Kind              string `gorm:"not null"`
	BaseURL           string `gorm:"not null;default:'';column:base_url"`
	APIKey            []byte `gorm:"column:api_key"` // Sealed
	Headers           []byte // Sealed json object
	APIVersion        string `gorm:"not null;default:'';column:api_version"`
	DefaultModel      string `gorm:"not null;default:''"`
	Models            string `gorm:"type:text;not null;default:'[]'"`
	MaxTokens         int    `gorm:"not null;default:0"`
	ContextWindow     int    `gorm:"not null;default:0"`
	TimeoutSeconds    int    `gorm:"not null;default:0"`
	SkipTLSVerify     bool   `gorm:"not null;default:false;column:skip_tls_verify"`
	ToolsSupported    bool   `gorm:"not null;default:true"`
	RequestsPerMinute int    `gorm:"not null;default:0"`
	TokensPerDay      int64  `gorm:"not null;default:0"`
	Enabled           bool   `gorm:"not null;default:true"`
	IsDefault         bool   `gorm:"not null;default:false"`
	LastTestedAt      *time.Time
	LastTestError     string    `gorm:"type:text;not null;default:''"`
	CreatedBy         string    `gorm:"not null;default:''"`
	CreatedAt         time.Time `gorm:"autoCreateTime"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime"`
}

type Agent struct {
	ID               string    `gorm:"primaryKey"`
	ScopeKind        string    `gorm:"not null;uniqueIndex:idx_agent_scope_slug"`
	ScopeID          string    `gorm:"not null;default:'';uniqueIndex:idx_agent_scope_slug"`
	Slug             string    `gorm:"not null;uniqueIndex:idx_agent_scope_slug"`
	Name             string    `gorm:"not null"`
	Description      string    `gorm:"type:text;not null;default:''"`
	Kind             string    `gorm:"not null"`
	ProviderID       string    `gorm:"not null;default:'';index"`
	Model            string    `gorm:"not null;default:''"`
	Instructions     string    `gorm:"type:text;not null;default:''"`
	Tools            string    `gorm:"type:text;not null;default:'[]'"`
	Triggers         string    `gorm:"type:text;not null;default:'{}'"` // Protojson AgentTriggers
	MaxTurns         int       `gorm:"not null;default:0"`
	MaxTokens        int64     `gorm:"not null;default:0"`
	Temperature      float64   `gorm:"not null;default:-1"` // Negative means unset
	Approval         string    `gorm:"not null;default:'none'"`
	Enabled          bool      `gorm:"not null;default:true"`
	Concurrency      int       `gorm:"not null;default:0"`
	AllowedCallerIDs string    `gorm:"type:text;not null;default:'[]'"`
	CreatedBy        string    `gorm:"not null;default:''"`
	CreatedAt        time.Time `gorm:"autoCreateTime"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime"`
}

// Bot account behind an agent handle, shared by every scope using the slug
type AgentIdentity struct {
	Slug      string    `gorm:"primaryKey"`
	UserID    string    `gorm:"not null;uniqueIndex"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	User      *User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

type AgentSession struct {
	ID               string  `gorm:"primaryKey"`
	RepoID           string  `gorm:"not null;uniqueIndex:idx_agent_session_repo_number;index:idx_agent_session_repo_created"`
	Number           int64   `gorm:"not null;uniqueIndex:idx_agent_session_repo_number"`
	AgentID          string  `gorm:"not null;default:'';index"`
	AgentSlug        string  `gorm:"not null;default:'';index"`
	AgentName        string  `gorm:"not null;default:''"`
	AgentKind        string  `gorm:"not null;default:''"`
	AgentSource      string  `gorm:"not null;default:'database'"`
	BotUserID        string  `gorm:"not null;default:''"`
	ProviderID       string  `gorm:"not null;default:'';index"`
	ProviderName     string  `gorm:"not null;default:''"`
	Model            string  `gorm:"not null;default:''"`
	Trigger          string  `gorm:"not null;default:'manual';index"`
	TriggerUserID    string  `gorm:"not null;default:'';index"`
	Prompt           string  `gorm:"type:text;not null;default:''"`
	Title            string  `gorm:"not null;default:''"`
	IssueID          *string `gorm:"index"`
	IssueNumber      int64   `gorm:"not null;default:0"`
	IsPull           bool    `gorm:"not null;default:false"`
	Ref              string  `gorm:"not null;default:''"`
	SHA              string  `gorm:"not null;default:'';column:sha"`
	Branch           string  `gorm:"not null;default:''"`
	Status           string  `gorm:"not null;default:'queued';index"`
	Conclusion       string  `gorm:"not null;default:''"`
	Summary          string  `gorm:"type:text;not null;default:''"`
	Error            string  `gorm:"type:text;not null;default:''"`
	Turns            int     `gorm:"not null;default:0"`
	ToolCalls        int     `gorm:"not null;default:0"`
	InputTokens      int64   `gorm:"not null;default:0"` // Tokens read for the first time
	OutputTokens     int64   `gorm:"not null;default:0"`
	PromptTokens     int64   `gorm:"not null;default:0"` // Every prompt sent in full, the billed input
	ContextTokens    int64   `gorm:"not null;default:0"` // Prompt plus output of the latest call
	CacheReadTokens  int64   `gorm:"not null;default:0"` // Prompt tokens the provider served from cache
	CacheWriteTokens int64   `gorm:"not null;default:0"`
	MaxTurns         int     `gorm:"not null;default:0"`
	MaxTokens        int64   `gorm:"not null;default:0"`
	ResultPullNumber int64   `gorm:"not null;default:0"`
	ResultReviewID   string  `gorm:"not null;default:''"`
	ResultCommits    string  `gorm:"type:text;not null;default:'[]'"`
	Definition       string  `gorm:"type:text;not null;default:'{}'"` // Agent snapshot as json at start
	EventCount       int64   `gorm:"not null;default:0"`
	StartedAt        *time.Time
	FinishedAt       *time.Time
	CreatedAt        time.Time      `gorm:"autoCreateTime;index:idx_agent_session_repo_created"`
	UpdatedAt        time.Time      `gorm:"autoUpdateTime"`
	Repo             *GitRepository `gorm:"foreignKey:RepoID;constraint:OnDelete:CASCADE"`
}

type AgentSessionEvent struct {
	ID         int64         `gorm:"primaryKey;autoIncrement"`
	SessionID  string        `gorm:"not null;uniqueIndex:idx_agent_event_seq"`
	Seq        int64         `gorm:"not null;uniqueIndex:idx_agent_event_seq"`
	Kind       string        `gorm:"not null"`
	Text       string        `gorm:"type:text;not null;default:''"`
	Tool       string        `gorm:"not null;default:''"`
	ToolCallID string        `gorm:"not null;default:'';column:tool_call_id"`
	Data       string        `gorm:"type:text;not null;default:'{}'"`
	CreatedAt  time.Time     `gorm:"autoCreateTime"`
	Session    *AgentSession `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE"`
}

type AgentApproval struct {
	ID        string `gorm:"primaryKey"`
	SessionID string `gorm:"not null;index"`
	EventSeq  int64  `gorm:"not null;default:0"`
	Tool      string `gorm:"not null"`
	Title     string `gorm:"not null;default:''"`
	Arguments string `gorm:"type:text;not null;default:'{}'"`
	Status    string `gorm:"not null;default:'pending';index"`
	DecidedBy string `gorm:"not null;default:''"`
	DecidedAt *time.Time
	Note      string        `gorm:"type:text;not null;default:''"`
	CreatedAt time.Time     `gorm:"autoCreateTime"`
	Session   *AgentSession `gorm:"foreignKey:SessionID;constraint:OnDelete:CASCADE"`
}

// One bash script queued on an interactive job
type ActionsJobExec struct {
	ID             string    `gorm:"primaryKey"`
	JobID          string    `gorm:"not null;index:idx_job_exec_job_number,unique"`
	Number         int       `gorm:"not null;index:idx_job_exec_job_number,unique"`
	Script         string    `gorm:"type:text;not null;default:''"`
	TimeoutMinutes int       `gorm:"not null;default:0"`
	Status         string    `gorm:"not null;default:'queued';index"`
	Conclusion     string    `gorm:"not null;default:''"`
	ExitCode       int       `gorm:"not null;default:0"`
	Output         string    `gorm:"type:text;not null;default:''"` // Head and tail of what it printed
	Error          string    `gorm:"type:text;not null;default:''"` // Why it never ran
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	StartedAt      *time.Time
	FinishedAt     *time.Time
	Job            *ActionsJob `gorm:"foreignKey:JobID;constraint:OnDelete:CASCADE"`
}
