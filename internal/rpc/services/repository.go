package services

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/pagination"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.RepositoryServiceHandler = (*RepositoryService)(nil)

type RepositoryService struct {
	store    *stores.Store
	registry *registry.RegistryAccess
	enforcer *rbac.Enforcer
	log      *logger.Logger
}

func NewRepositoryService(store *stores.Store, reg *registry.RegistryAccess, enforcer *rbac.Enforcer, log *logger.Logger) *RepositoryService {
	return &RepositoryService{store: store, registry: reg, enforcer: enforcer, log: log}
}

// Checks if the requesting user can read the given repo via RBAC
func (s *RepositoryService) canReadRepo(ctx context.Context, repo *storage.Repository) bool {
	if !repo.IsPrivate {
		return true
	}
	user := auth.UserFromContext(ctx)
	if user == nil {
		return false
	}
	objectID := repo.Namespace + "/" + repo.Name
	allowed, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionRead, objectID)
	return allowed
}

func (s *RepositoryService) GetRepository(ctx context.Context, req *connect.Request[v1.GetRepositoryRequest]) (*connect.Response[v1.GetRepositoryResponse], error) {
	if req.Msg.Namespace == "" || req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	repo, err := s.store.GetRepository(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	proto := repoToProto(repo)
	s.attachStars(ctx, []*v1.Repository{proto})

	return connect.NewResponse(&v1.GetRepositoryResponse{
		Repository: proto,
	}), nil
}

func (s *RepositoryService) ListRepositories(ctx context.Context, req *connect.Request[v1.ListRepositoriesRequest]) (*connect.Response[v1.ListRepositoriesResponse], error) {
	pageSize, offset := pagination.Parse(req.Msg.Page)

	// Resolve visibility: admin sees all, authenticated users see public +
	// owned + org membership + RBAC grants, anonymous sees public only.
	user := auth.UserFromContext(ctx)
	var userID string
	var canManage bool
	var grantedRepos []string

	if user != nil {
		userID = user.ID
		canManage, _ = s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, "*")
		if !canManage {
			grantedRepos = s.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceRepositories, rbac.ActionRead)
		}
	}

	namespace := portal.ScopeNamespace(ctx, req.Msg.Namespace)
	q := pagination.ParseQuery(req.Msg.Page)
	if err := stores.ReposQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	repos, total, err := s.store.ListRepositories(ctx, namespace, q, userID, canManage, grantedRepos, pageSize, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoRepos := make([]*v1.Repository, len(repos))
	for i, r := range repos {
		protoRepos[i] = repoToProto(r)
	}
	s.attachStars(ctx, protoRepos)

	return connect.NewResponse(&v1.ListRepositoriesResponse{
		Repositories: protoRepos,
		Page:         pagination.Info(offset, pageSize, total),
	}), nil
}

func (s *RepositoryService) DeleteRepository(ctx context.Context, req *connect.Request[v1.DeleteRepositoryRequest]) (*connect.Response[v1.DeleteRepositoryResponse], error) {
	if req.Msg.Namespace == "" || req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	repo, err := s.store.GetRepository(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	objectID := repo.Namespace + "/" + repo.Name
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID)
	if !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.NewError(connect.CodePermissionDenied, nil)
			}
		}
	}

	if err := s.store.DeleteRepository(ctx, req.Msg.Namespace, req.Msg.Name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.DeleteRepositoryResponse{}), nil
}

// Tags are derived from distribution, so no sql to sort for us
var tagSortColumns = map[string]func(a, b *v1.Tag) int{
	"name":      func(a, b *v1.Tag) int { return strings.Compare(a.Name, b.Name) },
	"pushed_at": func(a, b *v1.Tag) int { return a.GetPushedAt().AsTime().Compare(b.GetPushedAt().AsTime()) },
}

func (s *RepositoryService) ListTags(ctx context.Context, req *connect.Request[v1.ListTagsRequest]) (*connect.Response[v1.ListTagsResponse], error) {
	if req.Msg.Namespace == "" || req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	repo, err := s.store.GetRepository(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	tags, err := s.registry.ListTags(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pagination.Sort(req.Msg.Page, tags, tagSortColumns)

	pageSize, offset := pagination.Parse(req.Msg.Page)

	total := len(tags)
	start := min(offset, total)
	end := min(start+pageSize, total)

	return connect.NewResponse(&v1.ListTagsResponse{
		Tags: tags[start:end],
		Page: pagination.Info(start, pageSize, int64(total)),
	}), nil
}

func (s *RepositoryService) ResolveTag(ctx context.Context, req *connect.Request[v1.ResolveTagRequest]) (*connect.Response[v1.ResolveTagResponse], error) {
	if req.Msg.Namespace == "" || req.Msg.Name == "" || req.Msg.Tag == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	repo, err := s.store.GetRepository(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	desc, err := s.registry.ResolveTag(ctx, req.Msg.Namespace, req.Msg.Name, req.Msg.Tag)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("tag %q: %w", req.Msg.Tag, err))
	}

	return connect.NewResponse(&v1.ResolveTagResponse{
		Descriptor_: desc,
	}), nil
}

func (s *RepositoryService) UpdateRepository(ctx context.Context, req *connect.Request[v1.UpdateRepositoryRequest]) (*connect.Response[v1.UpdateRepositoryResponse], error) {
	if req.Msg.Namespace == "" || req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	repo, err := s.store.GetRepository(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	objectID := repo.Namespace + "/" + repo.Name
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID)
	if !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.NewError(connect.CodePermissionDenied, nil)
			}
		}
	}

	if req.Msg.Description != nil {
		repo.Description = *req.Msg.Description
	}
	if req.Msg.Visibility != nil {
		repo.IsPrivate = *req.Msg.Visibility == v1.Visibility_VISIBILITY_PRIVATE
	}

	if err := s.store.UpdateRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UpdateRepositoryResponse{
		Repository: repoToProto(repo),
	}), nil
}

func (s *RepositoryService) StarRepository(ctx context.Context, req *connect.Request[v1.StarRepositoryRequest]) (*connect.Response[v1.StarRepositoryResponse], error) {
	repo, err := s.starTarget(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, err
	}

	user := auth.UserFromContext(ctx)
	if err := s.store.StarRepository(ctx, user.ID, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	count, err := s.store.CountStars(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.StarRepositoryResponse{StarCount: count}), nil
}

func (s *RepositoryService) UnstarRepository(ctx context.Context, req *connect.Request[v1.UnstarRepositoryRequest]) (*connect.Response[v1.UnstarRepositoryResponse], error) {
	repo, err := s.starTarget(ctx, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, err
	}

	user := auth.UserFromContext(ctx)
	if err := s.store.UnstarRepository(ctx, user.ID, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	count, err := s.store.CountStars(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.UnstarRepositoryResponse{StarCount: count}), nil
}

func (s *RepositoryService) ListStarredRepositories(ctx context.Context, req *connect.Request[v1.ListStarredRepositoriesRequest]) (*connect.Response[v1.ListStarredRepositoriesResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	limit, offset := pagination.Parse(req.Msg.Page)

	repos, total, err := s.store.ListStarredRepositories(ctx, user.ID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoRepos := make([]*v1.Repository, len(repos))
	for i, r := range repos {
		protoRepos[i] = repoToProto(r)
	}
	s.attachStars(ctx, protoRepos)

	return connect.NewResponse(&v1.ListStarredRepositoriesResponse{
		Repositories: protoRepos,
		Page:         pagination.Info(offset, limit, total),
	}), nil
}

// Validates auth and read access for star mutations
func (s *RepositoryService) starTarget(ctx context.Context, namespace, name string) (*storage.Repository, error) {
	if namespace == "" || name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	repo, err := s.store.GetRepository(ctx, namespace, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil || !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return repo, nil
}

// Fills star counts and the caller's starred flags
func (s *RepositoryService) attachStars(ctx context.Context, repos []*v1.Repository) {
	if len(repos) == 0 {
		return
	}
	ids := make([]string, len(repos))
	for i, r := range repos {
		ids[i] = r.Id
	}

	counts, err := s.store.GetStarCounts(ctx, ids)
	if err != nil {
		s.log.Error("loading star counts: %v", err)
		return
	}

	var starred map[string]bool
	if user := auth.UserFromContext(ctx); user != nil {
		if starred, err = s.store.GetStarredSet(ctx, user.ID, ids); err != nil {
			s.log.Error("loading starred set: %v", err)
		}
	}

	for _, r := range repos {
		r.StarCount = counts[r.Id]
		r.IsStarred = starred[r.Id]
	}
}

func repoToProto(r *storage.Repository) *v1.Repository {
	vis := v1.Visibility_VISIBILITY_PUBLIC
	if r.IsPrivate {
		vis = v1.Visibility_VISIBILITY_PRIVATE
	}

	repo := &v1.Repository{
		Id:             r.ID,
		Namespace:      r.Namespace,
		Name:           r.Name,
		FullName:       r.Namespace + "/" + r.Name,
		Description:    r.Description,
		Visibility:     vis,
		OwnerId:        r.OwnerID,
		PullCount:      r.PullCount,
		PushCount:      r.PushCount,
		CreatedAt:      timestamppb.New(r.CreatedAt),
		UpdatedAt:      timestamppb.New(r.UpdatedAt),
		IsOrgNamespace: r.IsOrgNamespace,
	}

	if r.LastPush != nil {
		repo.LastPushedAt = timestamppb.New(*r.LastPush)
	}

	return repo
}
