package models

import (
	"encoding/json"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type Action string
type Resource string
type Scope string

const (
	// ACTIONS
	ActionView    Action = "VIEW"
	ActionCreate  Action = "CREATE"
	ActionUpdate  Action = "UPDATE"
	ActionDelete  Action = "DELETE"
	ActionPush    Action = "PUSH"
	ActionPull    Action = "PULL"
	ActionAdmin   Action = "ADMIN"
	ActionLogin   Action = "LOGIN"
	ActionLogout  Action = "LOGOUT"
	ActionMigrate Action = "MIGRATE"

	// RESOURCES
	ResourceTask   Resource = "TASK"
	ResourceWebUI  Resource = "WEBUI"
	ResourceImage  Resource = "IMAGE"
	ResourceTag    Resource = "TAG"
	ResourceUser   Resource = "USER"
	ResourceGroup  Resource = "GROUP"
	ResourceSystem Resource = "SYSTEM"

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
