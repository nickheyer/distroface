package container

import (
	"context"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/registry"
)

// Deletes and moves image and artifact packages on behalf of their source repository
type packageManager struct {
	store     *stores.Store
	registry  *registry.RegistryAccess
	artifacts *artifacts.Manager
}

var _ gitrepo.PackageManager = (*packageManager)(nil)

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
