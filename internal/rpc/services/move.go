package services

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// ── Renames and transfers of standalone packages ─────────────────────────

// Audit event for a team or package operation by the caller
func teamAudit(ctx context.Context, action, detail string) audit.Event {
	ev := audit.Event{Action: action, Resource: "organizations", Outcome: audit.OutcomeSuccess, Detail: detail}
	if user := auth.UserFromContext(ctx); user != nil {
		ev.Actor, ev.ActorID = user.Username, user.ID
	}
	return ev
}

// Owner id and org flag of a namespace that must exist
func namespaceOwner(ctx context.Context, s interface {
	GetOrganization(ctx context.Context, name string) (*storage.Organization, error)
	GetUserByUsername(ctx context.Context, name string) (*storage.User, error)
}, namespace string) (string, bool, error) {
	if org, _ := s.GetOrganization(ctx, namespace); org != nil {
		return org.ID, true, nil
	}
	if u, _ := s.GetUserByUsername(ctx, namespace); u != nil {
		return u.ID, false, nil
	}
	return "", false, connect.Errorf(connect.CodeNotFound, "namespace %q does not exist", namespace)
}

func (s *RepositoryService) MoveRepository(ctx context.Context, req *v1.MoveRepositoryRequest) (*v1.MoveRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if p := portal.FromContext(ctx); p != nil && !p.AllowPush {
		return nil, connect.Errorf(connect.CodePermissionDenied, "portal is read-only")
	}
	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil || !s.canReadRepo(ctx, repo) {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	if repo.GitRepoID != nil {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "this image repository follows its source repository, move that instead")
	}
	objectID := repo.Namespace + "/" + repo.Name
	if canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID); !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.Errorf(connect.CodePermissionDenied, "owner access required")
			}
		}
	}
	newNS, newName := req.NewNamespace, req.NewName
	if newNS == "" {
		newNS = repo.Namespace
	}
	if newName == "" {
		newName = repo.Name
	}
	if !imageRepoNamePattern.MatchString(newName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if newNS == repo.Namespace && newName == repo.Name {
		return &v1.MoveRepositoryResponse{Repository: s.repoToProto(repo)}, nil
	}
	if newNS != repo.Namespace && !s.canCreateInNamespace(ctx, user, newNS) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot move repository into namespace %q", newNS)
	}
	if existing, _ := s.store.GetRepository(ctx, newNS, newName); existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "%s/%s already exists", newNS, newName)
	}
	ownerID, isOrg, err := namespaceOwner(ctx, s.store, newNS)
	if err != nil {
		return nil, err
	}
	if s.registry != nil {
		if err := s.registry.MoveRepository(repo.Namespace, repo.Name, newNS, newName); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
		}
	}
	if err := s.store.MoveRepository(ctx, repo.ID, newNS, newName, ownerID, isOrg); err != nil {
		if s.registry != nil {
			_ = s.registry.MoveRepository(newNS, newName, repo.Namespace, repo.Name)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	_ = s.enforcer.RenameObject(rbac.ResourceRepositories, objectID, newNS+"/"+newName)
	moved, err := s.store.GetRepositoryByID(ctx, repo.ID)
	if err != nil || moved == nil {
		return nil, connect.NewError(connect.CodeInternal, "repository vanished during move")
	}
	return &v1.MoveRepositoryResponse{Repository: s.repoToProto(moved)}, nil
}

func (s *ArtifactService) MoveArtifactRepository(ctx context.Context, req *v1.MoveArtifactRepositoryRequest) (*v1.MoveArtifactRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if p := portal.FromContext(ctx); p != nil && !p.AllowPush {
		return nil, connect.Errorf(connect.CodePermissionDenied, "portal is read-only")
	}
	repo, err := s.mutableRepo(ctx, user, req.Namespace, req.Name, rbac.ActionManage)
	if err != nil {
		return nil, err
	}
	if repo.GitRepoID != nil {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "this artifact repository follows its source repository, move that instead")
	}
	newNS, newName := req.NewNamespace, req.NewName
	if newNS == "" {
		newNS = repo.Namespace
	}
	if newName == "" {
		newName = repo.Name
	}
	if !artifactRepoNamePattern.MatchString(newName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if newNS == repo.Namespace && newName == repo.Name {
		return &v1.MoveArtifactRepositoryResponse{Repository: s.repoToProto(ctx, repo, nil)}, nil
	}
	if newNS != repo.Namespace && !s.access.CanCreateInNamespace(ctx, user, newNS) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot move repository into namespace %q", newNS)
	}
	if existing, _ := s.store.GetArtifactRepository(ctx, newNS, newName); existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "%s/%s already exists", newNS, newName)
	}
	ownerID, _, err := namespaceOwner(ctx, s.store, newNS)
	if err != nil {
		return nil, err
	}
	objectID := repo.Namespace + "/" + repo.Name
	if err := s.store.MoveArtifactRepository(ctx, repo.ID, newNS, newName, ownerID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	_ = s.access.RenameObject(rbac.ResourceArtifacts, objectID, newNS+"/"+newName)
	moved, err := s.store.GetArtifactRepositoryByID(ctx, repo.ID)
	if err != nil || moved == nil {
		return nil, connect.NewError(connect.CodeInternal, "repository vanished during move")
	}
	return &v1.MoveArtifactRepositoryResponse{Repository: s.repoToProto(ctx, moved, nil)}, nil
}
