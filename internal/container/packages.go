package container

import (
	"context"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
)

// Deletes, moves, and vets image and artifact packages for their source repository
type packageManager struct {
	store     *stores.Store
	registry  *registry.RegistryAccess
	artifacts *artifacts.Manager
	images    *auth.RepoAccess
	artAccess *artifacts.Access
}

var _ gitrepo.PackageManager = (*packageManager)(nil)

func (p *packageManager) CanManageImageRepo(ctx context.Context, user *auth.AuthenticatedUser, repo *db.Repository) bool {
	return user != nil && !user.IsAnonymous() && p.images.HasRepoAccess(ctx, user, repo, rbac.ActionManage)
}

func (p *packageManager) CanManageArtifactRepo(ctx context.Context, user *auth.AuthenticatedUser, repo *db.ArtifactRepository) bool {
	return user != nil && !user.IsAnonymous() && p.artAccess.HasRepoAccess(ctx, user, repo, rbac.ActionManage)
}

func (p *packageManager) DeleteImageRepo(ctx context.Context, repo *db.Repository) error {
	if err := p.store.DeleteRepository(ctx, repo.Namespace, repo.Name); err != nil {
		return err
	}
	return p.registry.DeleteRepository(repo.Namespace, repo.Name)
}

func (p *packageManager) DeleteArtifactRepo(ctx context.Context, repo *db.ArtifactRepository) error {
	return p.artifacts.DeleteRepository(ctx, repo)
}

func (p *packageManager) MoveImageRepo(ctx context.Context, repo *db.Repository, newNamespace, newName, ownerID string, isOrg bool) error {
	if err := p.registry.MoveRepository(repo.Namespace, repo.Name, newNamespace, newName); err != nil {
		return err
	}
	return p.store.MoveRepository(ctx, repo.ID, newNamespace, newName, ownerID, isOrg)
}

func (p *packageManager) MoveArtifactRepo(ctx context.Context, repo *db.ArtifactRepository, newNamespace, newName, ownerID string) error {
	return p.store.MoveArtifactRepository(ctx, repo.ID, newNamespace, newName, ownerID)
}
