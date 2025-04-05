package permissions

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/utils"
	"gorm.io/gorm"
)

type PermissionManager struct {
	db    *gorm.DB
	cache PermissionCache
	repo  repository.Repository
}

func NewPermissionManager(repo repository.Repository, db *gorm.DB) *PermissionManager {
	pm := &PermissionManager{
		db:    db,
		cache: NewInMemoryCache(),
		repo:  repo,
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
	var user models.User
	if err := pm.db.Where("username = ?", username).Select("groups").First(&user).Error; err != nil {
		return nil, fmt.Errorf("failed to get user groups: %v", err)
	}

	var groups []models.Group
	for _, name := range user.Groups {
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
	if err := pm.db.Where("name = ?", name).Select("name, description, roles, scope").First(&group).Error; err != nil {
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
	if err := pm.db.Where("name = ?", name).Select("name, description, permissions").First(&role).Error; err != nil {
		return models.Role{}, fmt.Errorf("failed to get role: %v", err)
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
	authSettings, _ := utils.GetSettings[*models.AuthSettings](pm.repo, "auth")
	interval := time.Duration(authSettings.SessionTimeout) * time.Minute
	ticker := time.NewTicker(interval)
	for range ticker.C {
		fmt.Printf("Cleaning cache...\n")
		pm.cache.Clear()
	}
}
