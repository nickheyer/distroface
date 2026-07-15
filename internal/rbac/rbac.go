package rbac

import (
	"fmt"
	"strings"

	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

// Permission represents a single resource/action/object permission tuple.
type Permission struct {
	Resource string
	Action   string
	ObjectID string
}

// Enforcer wraps a Casbin enforcer with convenience methods for RBAC.
type Enforcer struct {
	enforcer *casbin.Enforcer
}

// NewEnforcer creates a new Casbin RBAC enforcer backed by the given GORM database.
func NewEnforcer(db *gorm.DB) (*Enforcer, error) {
	adapter, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		return nil, fmt.Errorf("failed to create casbin adapter: %w", err)
	}

	// RBAC model with resource/action/object_id
	m, err := model.NewModelFromString(`
[request_definition]
r = sub, res, act, obj

[policy_definition]
p = sub, res, act, obj

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && (p.res == "*" || r.res == p.res) && (p.act == "*" || r.act == p.act) && (p.obj == "*" || r.obj == p.obj)
`)
	if err != nil {
		return nil, fmt.Errorf("failed to create casbin model: %w", err)
	}

	e, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		return nil, fmt.Errorf("failed to create casbin enforcer: %w", err)
	}

	if err := e.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("failed to load casbin policy: %w", err)
	}

	return &Enforcer{enforcer: e}, nil
}

// SeedDefaultPolicies ensures default roles have their base permissions.
func (e *Enforcer) SeedDefaultPolicies(anonymousEnabled bool) error {
	policies := map[string][][]string{
		"admin": {
			{"admin", "*", "*", "*"},
		},
		"user": {
			{"user", ResourceRepositories, ActionRead, "*"},
			{"user", ResourceRepositories, ActionPull, "*"},
			{"user", ResourceRepositories, ActionUpdate, "*"},
			{"user", ResourceRepositories, ActionDelete, "*"},
			{"user", ResourceTokens, ActionRead, "*"},
			{"user", ResourceTokens, ActionCreate, "*"},
			{"user", ResourceTokens, ActionDelete, "*"},
			{"user", ResourceOrganizations, ActionRead, "*"},
			{"user", ResourceOrganizations, ActionCreate, "*"},
			{"user", ResourceWebhooks, ActionRead, "*"},
			{"user", ResourceWebhooks, ActionCreate, "*"},
			{"user", ResourceWebhooks, ActionUpdate, "*"},
			{"user", ResourceWebhooks, ActionDelete, "*"},
			{"user", ResourceArtifacts, ActionRead, "*"},
			{"user", ResourceArtifacts, ActionPull, "*"},
			{"user", ResourceArtifacts, ActionPush, "*"},
			{"user", ResourceArtifacts, ActionCreate, "*"},
			{"user", ResourceArtifacts, ActionUpdate, "*"},
			{"user", ResourceArtifacts, ActionDelete, "*"},
		},
		"anonymous": {
			{"anonymous", ResourceRepositories, ActionRead, "*"},
			{"anonymous", ResourceRepositories, ActionPull, "*"},
			{"anonymous", ResourceArtifacts, ActionRead, "*"},
			{"anonymous", ResourceArtifacts, ActionPull, "*"},
		},
	}

	for role, rolePolicies := range policies {
		existing, _ := e.enforcer.GetFilteredPolicy(0, role)
		if len(existing) == 0 {
			for _, p := range rolePolicies {
				if _, err := e.enforcer.AddPolicy(p[0], p[1], p[2], p[3]); err != nil {
					return err
				}
			}
			continue
		}

		// Backfill grants for resources added after role seeding
		seen := make(map[string]bool)
		for _, p := range existing {
			if len(p) >= 2 {
				seen[p[1]] = true
			}
		}
		for _, p := range rolePolicies {
			if seen[p[1]] {
				continue
			}
			if _, err := e.enforcer.AddPolicy(p[0], p[1], p[2], p[3]); err != nil {
				return err
			}
		}
	}

	return e.enforcer.SavePolicy()
}

// Enforce checks if any of the given roles allows the specified action on a
// resource with the given object ID. Returns true on the first matching role.
func (e *Enforcer) Enforce(roles []string, resource, action, objectID string) (bool, error) {
	for _, role := range roles {
		allowed, err := e.enforcer.Enforce(role, resource, action, objectID)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

// GetAllowedObjects checks the given roles for a resource+action and returns
// Whether access is unrestricted (wildcard) or limited to specific object IDs.
// If allowAll is true, the caller should not filter. Otherwise, only the
// Returned objectIDs are permitted.
func (e *Enforcer) GetAllowedObjects(roles []string, resource, action string) (allowAll bool, objectIDs []string) {
	seen := make(map[string]bool)
	for _, role := range roles {
		policies, _ := e.enforcer.GetFilteredPolicy(0, role)
		for _, p := range policies {
			if len(p) < 4 {
				continue
			}
			pRes, pAct, pObj := p[1], p[2], p[3]
			// Wildcard resource or wildcard action with wildcard object = full access
			if (pRes == "*" || pRes == resource) && (pAct == "*" || pAct == action) {
				if pObj == "*" {
					return true, nil
				}
				if !seen[pObj] {
					seen[pObj] = true
					objectIDs = append(objectIDs, pObj)
				}
			}
		}
	}
	return false, objectIDs
}

// GetGrantedObjects returns only the non-wildcard object IDs granted to any of
// the given roles for a resource+action. Wildcard grants are ignored — this is
// used for visibility filtering where "has permission" is separate from "can
// see private resources."
func (e *Enforcer) GetGrantedObjects(roles []string, resource, action string) []string {
	seen := make(map[string]bool)
	var objectIDs []string
	for _, role := range roles {
		policies, _ := e.enforcer.GetFilteredPolicy(0, role)
		for _, p := range policies {
			if len(p) < 4 {
				continue
			}
			pRes, pAct, pObj := p[1], p[2], p[3]
			if pObj == "*" {
				continue // Skip wildcards — they don't grant visibility
			}
			if (pRes == "*" || pRes == resource) && (pAct == "*" || pAct == action) {
				if !seen[pObj] {
					seen[pObj] = true
					objectIDs = append(objectIDs, pObj)
				}
			}
		}
	}
	return objectIDs
}

// HasPermission checks if any of the given roles has the specified resource+action
// permission (wildcard or specific). This is a capability check, not a visibility check.
func (e *Enforcer) HasPermission(roles []string, resource, action string) bool {
	for _, role := range roles {
		policies, _ := e.enforcer.GetFilteredPolicy(0, role)
		for _, p := range policies {
			if len(p) < 4 {
				continue
			}
			pRes, pAct := p[1], p[2]
			if (pRes == "*" || pRes == resource) && (pAct == "*" || pAct == action) {
				return true
			}
		}
	}
	return false
}

// GetPermissionsForRole returns all permissions currently assigned to the role.
func (e *Enforcer) GetPermissionsForRole(role string) []Permission {
	policies, _ := e.enforcer.GetFilteredPolicy(0, role)
	perms := make([]Permission, 0, len(policies))
	for _, p := range policies {
		if len(p) >= 4 {
			perms = append(perms, Permission{
				Resource: p[1],
				Action:   p[2],
				ObjectID: p[3],
			})
		}
	}
	return perms
}

// SetPermissionsForRole replaces all permissions for a role atomically.
// The admin role cannot be modified.
func (e *Enforcer) SetPermissionsForRole(role string, perms []Permission) error {
	if strings.ToLower(role) == "admin" {
		return fmt.Errorf("cannot modify admin role permissions")
	}

	e.enforcer.RemoveFilteredPolicy(0, role)

	for _, p := range perms {
		objectID := p.ObjectID
		if objectID == "" {
			objectID = "*"
		}
		_, err := e.enforcer.AddPolicy(role, p.Resource, p.Action, objectID)
		if err != nil {
			return err
		}
	}

	return e.enforcer.SavePolicy()
}

// Move casbin policies to new role name
func (e *Enforcer) RenameRole(oldName, newName string) error {
	if strings.ToLower(oldName) == "admin" || strings.ToLower(newName) == "admin" {
		return fmt.Errorf("cannot rename the admin role")
	}

	policies, err := e.enforcer.GetFilteredPolicy(0, oldName)
	if err != nil {
		return err
	}
	for _, p := range policies {
		if len(p) < 4 {
			continue
		}
		if _, err := e.enforcer.AddPolicy(newName, p[1], p[2], p[3]); err != nil {
			return err
		}
	}
	if _, err := e.enforcer.RemoveFilteredPolicy(0, oldName); err != nil {
		return err
	}

	return e.enforcer.SavePolicy()
}

// GetPermissionMatrix returns a map of role names to their permission slices.
func (e *Enforcer) GetPermissionMatrix() map[string][]Permission {
	policies, _ := e.enforcer.GetPolicy()
	matrix := make(map[string][]Permission)
	for _, p := range policies {
		if len(p) >= 4 {
			role := p[0]
			matrix[role] = append(matrix[role], Permission{
				Resource: p[1],
				Action:   p[2],
				ObjectID: p[3],
			})
		}
	}
	return matrix
}
