package permissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/models"
)

type PermissionManager struct {
	db    *sql.DB
	cache PermissionCache
	mu    sync.RWMutex // OOOOF CONCURRENCY
}

func NewPermissionManager(db *sql.DB) *PermissionManager {
	pm := &PermissionManager{
		db:    db,
		cache: NewInMemoryCache(),
	}

	// START CACHE CLEANUP
	go pm.periodicallyCleanCache()

	return pm
}

func (pm *PermissionManager) getAnonymousRole() (*models.Role, error) {
	role, err := pm.getRole("anonymous")
	if err != nil {
		return nil, fmt.Errorf("failed to get anonymous role: %v", err)
	}
	return &role, nil
}

func (pm *PermissionManager) HasPermission(ctx context.Context, username string, perm models.Permission) bool {
	// HANDLE ANONYMOUS ACCESS
	if username == "" || username == "anonymous" {
		role, err := pm.getAnonymousRole()

		if err != nil {
			return false
		}
		return pm.roleHasPermission(*role, perm)
	}

	cacheKey := fmt.Sprintf("perm:%s:%s", username, perm.String())

	// CHECK CACHE
	if allowed, exists := pm.cache.Get(cacheKey); exists {
		return allowed
	}

	// CHECK PERMS FOR USER GROUPS
	groups, err := pm.getUserGroups(username)
	if err != nil {
		return false
	}

	for _, group := range groups {
		if pm.groupHasPermission(group, perm) {
			pm.cache.Set(cacheKey, true)
			return true
		}
	}

	pm.cache.Set(cacheKey, false)
	return false
}

func (pm *PermissionManager) getUserGroups(username string) ([]models.Group, error) {
	var groupsJSON string
	err := pm.db.QueryRow("SELECT groups FROM users WHERE username = ?", username).Scan(&groupsJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to get user groups: %v", err)
	}

	var groupNames []string
	if err := json.Unmarshal([]byte(groupsJSON), &groupNames); err != nil {
		return nil, fmt.Errorf("failed to unmarshal groups: %v", err)
	}

	var groups []models.Group
	for _, name := range groupNames {
		// GROUP NAME TO LOWERCASE
		group, err := pm.getGroup(strings.ToLower(name))
		if err != nil {
			log.Printf("Warning: failed to get group %s: %v", name, err)
			continue
		}
		groups = append(groups, group)
	}

	return groups, nil
}

func (pm *PermissionManager) getGroup(name string) (models.Group, error) {
	var group models.Group
	var rolesJSON string

	err := pm.db.QueryRow(
		"SELECT name, description, roles, scope FROM groups WHERE name = ?",
		name,
	).Scan(&group.Name, &group.Description, &rolesJSON, &group.Scope)

	if err != nil {
		return models.Group{}, err
	}

	if err := json.Unmarshal([]byte(rolesJSON), &group.Roles); err != nil {
		return models.Group{}, err
	}

	return group, nil
}

func (pm *PermissionManager) groupHasPermission(group models.Group, perm models.Permission) bool {
	for _, roleName := range group.Roles {
		// ROLE NAME TO LOWERCASE
		role, err := pm.getRole(strings.ToLower(roleName))
		if err != nil {
			log.Printf("Failed to get role %s: %v", roleName, err)
			continue
		}

		if pm.roleHasPermission(role, perm) {
			return true
		}
	}
	return false
}

func (pm *PermissionManager) getRole(name string) (models.Role, error) {
	var role models.Role
	var permissionsJSON string

	err := pm.db.QueryRow(
		"SELECT name, description, permissions FROM roles WHERE name = ?",
		name,
	).Scan(&role.Name, &role.Description, &permissionsJSON)

	if err != nil {
		return models.Role{}, fmt.Errorf("failed to get role: %v", err)
	}

	if err := json.Unmarshal([]byte(permissionsJSON), &role.Permissions); err != nil {
		return models.Role{}, fmt.Errorf("failed to parse permissions: %v", err)
	}

	return role, nil
}

func (pm *PermissionManager) roleHasPermission(role models.Role, perm models.Permission) bool {
	// ADMINS GOT ALL PERMS
	for _, p := range role.Permissions {
		if p.Action == models.ActionAdmin && p.Resource == models.ResourceSystem {
			return true
		}
	}

	// CHECK SPECIFIC
	for _, p := range role.Permissions {
		if p.Action == perm.Action && p.Resource == perm.Resource {
			return true
		}
	}

	return false
}

func (pm *PermissionManager) periodicallyCleanCache() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		pm.cache.Clear()
	}
}
