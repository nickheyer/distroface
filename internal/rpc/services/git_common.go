package services

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/portal"
)

// Maps engine and manager errors onto connect codes
func mapGitErr(err error) error {
	if err == nil {
		return nil
	}
	var cerr *connect.Error
	switch {
	case errors.As(err, &cerr):
		return err
	case errors.Is(err, gitrepo.ErrInvalid), errors.Is(err, gitrepo.ErrInvalidName), errors.Is(err, gitrepo.ErrPathConflict):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, gitrepo.ErrArchived), errors.Is(err, gitrepo.ErrMirror), errors.Is(err, gitrepo.ErrImporting), errors.Is(err, gitrepo.ErrNotFastForward):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, gitrepo.ErrRefChanged):
		return connect.NewError(connect.CodeAborted, err.Error()).WithCause(err)
	case errors.Is(err, gitrepo.ErrMergeConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, gitrepo.ErrRepoNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, gitrepo.ErrTooLarge):
		return connect.NewError(connect.CodeResourceExhausted, err.Error()).WithCause(err)
	}
	return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
}

// Resolves a git repository reference, mapping portal names and the caller's own namespace
func gitRepoRef(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (string, string) {
	return repoRef(ctx, user, namespace, name)
}

// Loads a git repository the caller may see, 404 hides private repos and foreign portals
func visibleGitRepo(ctx context.Context, store *stores.Store, access *gitrepo.Access, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
	repo, err := namedGitRepo(ctx, store, user, namespace, name)
	if err != nil {
		return nil, err
	}
	if !access.CanSee(ctx, user, repo) {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	return repo, nil
}

// Resolves the row without an access check, callers decide visibility
func namedGitRepo(ctx context.Context, store *stores.Store, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
	if name == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "repository name is required")
	}
	ns, name := gitRepoRef(ctx, user, namespace, name)
	if ns == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "repository namespace is required")
	}
	if portal.ForeignRef(ctx, ns) {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	repo, moved, err := store.ResolveGitRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	_ = moved
	return repo, nil
}

// What CanSee answers, from levels already computed
func levelsCanSee(repo *storage.GitRepository, levels gitrepo.Levels) bool {
	if !gitrepo.UnitEnabled(repo, storage.GitUnitCode) && !levels.IsOwner && !levels.CanAdmin {
		return false
	}
	return levels.Unit(storage.GitUnitCode) >= gitrepo.LevelRead
}

// Loads a repository and requires a level on one unit, read failures stay 404
func gitRepoWithLevel(ctx context.Context, store *stores.Store, access *gitrepo.Access, user *auth.AuthenticatedUser, namespace, name, unit string, need gitrepo.Level) (*storage.GitRepository, error) {
	repo, err := namedGitRepo(ctx, store, user, namespace, name)
	if err != nil {
		return nil, err
	}
	// One identity pass answers visibility and the unit level
	levels := access.Levels(ctx, user, repo)
	if !levelsCanSee(repo, levels) {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	if need > gitrepo.LevelRead && (user == nil || user.IsAnonymous()) {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if !gitrepo.UnitEnabled(repo, unit) && !levels.IsOwner && !levels.CanAdmin {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "the %s feature is disabled on this repository", unit)
	}
	if levels.Unit(unit) < need {
		if need <= gitrepo.LevelRead {
			return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		return nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	if need > gitrepo.LevelRead && repo.IsArchived && unit != storage.GitUnitCode {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is archived")
	}
	return repo, nil
}

// Reports whether a repository write is allowed on a read only portal
func portalAllowsGitWrite(ctx context.Context) error {
	if p := portal.FromContext(ctx); p != nil && !p.AllowPush {
		return connect.Errorf(connect.CodePermissionDenied, "portal is read-only")
	}
	return nil
}

// Decodes a json string list stored on a row
func decodeStringList(raw string) []string {
	var out []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}
