package auth

import (
	"context"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
)

// Image repo access rules for token endpoint and rpc
type RepoAccess struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
}

func NewRepoAccess(store *stores.Store, enforcer *rbac.Enforcer) *RepoAccess {
	return &RepoAccess{store: store, enforcer: enforcer}
}

// Namespace owner or org member
func (a *RepoAccess) related(ctx context.Context, user *AuthenticatedUser, namespace string) (bool, string) {
	if user == nil || user.IsAnonymous() {
		return false, ""
	}
	if user.Username == namespace {
		return true, storage.OrgRoleOwner
	}
	isMember, role, _ := a.store.IsOrgMember(ctx, namespace, user.ID)
	return isMember, role
}

// Owner, member, manage or scoped grant, wildcards never count
func (a *RepoAccess) HasRepoAccess(ctx context.Context, user *AuthenticatedUser, repo *storage.Repository, action string) bool {
	if user == nil || repo == nil {
		return false
	}
	if !user.IsAnonymous() && repo.OwnerID != "" && repo.OwnerID == user.ID {
		return true
	}
	if isMember, role := a.related(ctx, user, repo.Namespace); isMember {
		switch action {
		case rbac.ActionRead, rbac.ActionPull, rbac.ActionPush:
			return true
		default:
			return role == storage.OrgRoleOwner || role == storage.OrgRoleAdmin
		}
	}
	if a.enforcer.HasPermission(user.Roles, rbac.ResourceRepositories, rbac.ActionManage) {
		return true
	}
	return a.enforcer.HasScopedGrant(user.Roles, rbac.ResourceRepositories, action, repo.Namespace+"/"+repo.Name, repo.Namespace)
}

// Public repos need the capability, private ones an explicit relationship
func (a *RepoAccess) Can(ctx context.Context, user *AuthenticatedUser, repo *storage.Repository, action string) bool {
	if user == nil || repo == nil {
		return false
	}
	if repo.IsPrivate {
		return a.HasRepoAccess(ctx, user, repo, action)
	}
	if a.enforcer.HasPermission(user.Roles, rbac.ResourceRepositories, action) {
		return true
	}
	isMember, _ := a.related(ctx, user, repo.Namespace)
	return isMember
}

// Public repos or any read relationship
func (a *RepoAccess) CanSee(ctx context.Context, user *AuthenticatedUser, repo *storage.Repository) bool {
	return repo != nil && (!repo.IsPrivate || a.HasRepoAccess(ctx, user, repo, rbac.ActionRead))
}
