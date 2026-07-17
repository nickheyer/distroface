package artifacts

import (
	"context"
	"slices"

	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
)

// Shared artifact repo access rules for the v1 facade and RPC service
type Access struct {
	store    *storage.Store
	enforcer *rbac.Enforcer
}

func NewAccess(store *storage.Store, enforcer *rbac.Enforcer) *Access {
	return &Access{store: store, enforcer: enforcer}
}

// Owner, manage permission, org membership, or scoped grant
func (a *Access) HasRepoAccess(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.ArtifactRepository, action string) bool {
	if user == nil {
		return false
	}
	if repo.OwnerID != "" && repo.OwnerID == user.ID {
		return true
	}
	if a.enforcer.HasPermission(user.Roles, rbac.ResourceArtifacts, rbac.ActionManage) {
		return true
	}
	if repo.Namespace == user.Username {
		return true
	}
	if isMember, role, _ := a.store.IsOrgMember(ctx, repo.Namespace, user.ID); isMember {
		switch action {
		case rbac.ActionRead, rbac.ActionPull:
			return true
		default:
			return role == storage.OrgRoleOwner || role == storage.OrgRoleAdmin
		}
	}
	return slices.Contains(a.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceArtifacts, action), repo.Namespace+"/"+repo.Name)
}

// Public repos or any read grant
func (a *Access) CanSee(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.ArtifactRepository) bool {
	return !repo.IsPrivate || a.HasRepoAccess(ctx, user, repo, rbac.ActionRead)
}

// Owner username, org owner/admin, or manage into an existing namespace
func (a *Access) CanCreateInNamespace(ctx context.Context, user *auth.AuthenticatedUser, namespace string) bool {
	if user == nil {
		return false
	}
	if namespace == user.Username {
		return true
	}
	if isMember, role, _ := a.store.IsOrgMember(ctx, namespace, user.ID); isMember {
		return role == storage.OrgRoleOwner || role == storage.OrgRoleAdmin
	}
	if !a.enforcer.HasPermission(user.Roles, rbac.ResourceArtifacts, rbac.ActionManage) {
		return false
	}
	if u, _ := a.store.GetUserByUsername(ctx, namespace); u != nil {
		return true
	}
	org, _ := a.store.GetOrganization(ctx, namespace)
	return org != nil
}

// Repo list options honoring viewer visibility
func (a *Access) ListOptions(user *auth.AuthenticatedUser, namespace string) storage.ArtifactRepoListOptions {
	opts := storage.ArtifactRepoListOptions{Namespace: namespace}
	if user != nil {
		opts.ViewerID = user.ID
		opts.IncludePrivate = a.enforcer.HasPermission(user.Roles, rbac.ResourceArtifacts, rbac.ActionManage)
		if !opts.IncludePrivate {
			opts.GrantedRepos = a.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceArtifacts, rbac.ActionRead)
		}
	}
	return opts
}
