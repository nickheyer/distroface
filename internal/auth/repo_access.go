package auth

import (
	"context"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
)

// Answers whether the linked source repository grants an action on a package
type LinkedAccess interface {
	LinkedRepoAllows(ctx context.Context, user *AuthenticatedUser, gitRepoID string, action string) bool
}

// Image repo access rules for token endpoint and rpc
type RepoAccess struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
	linked   LinkedAccess
}

func NewRepoAccess(store *stores.Store, enforcer *rbac.Enforcer) *RepoAccess {
	return &RepoAccess{store: store, enforcer: enforcer}
}

// Installs the source repository access hook, set once at startup
func (a *RepoAccess) SetLinkedAccess(l LinkedAccess) {
	a.linked = l
}

// Full name of the linked source repo, empty when standalone
func (a *RepoAccess) sourceName(ctx context.Context, repo *storage.Repository) string {
	if repo.GitRepoID == nil {
		return ""
	}
	g, err := a.store.GetGitRepositoryByID(ctx, *repo.GitRepoID)
	if err != nil || g == nil {
		return ""
	}
	return g.Namespace + "/" + g.Name
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
	if user.IsScoped() {
		if !ScopeAllows(user, action, repo.Namespace+"/"+repo.Name, repo.Namespace, a.sourceName(ctx, repo)) {
			return false
		}
		if user.IsDeployKey() {
			return repo.GitRepoID != nil && a.linked != nil && a.linked.LinkedRepoAllows(ctx, user, *repo.GitRepoID, action)
		}
	}
	if !user.IsAnonymous() && repo.OwnerID != "" && repo.OwnerID == user.ID {
		return true
	}
	if repo.GitRepoID != nil && a.linked != nil && a.linked.LinkedRepoAllows(ctx, user, *repo.GitRepoID, action) {
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
	if user.IsScoped() && !ScopeAllows(user, action, repo.Namespace+"/"+repo.Name, repo.Namespace, a.sourceName(ctx, repo)) {
		return false
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
