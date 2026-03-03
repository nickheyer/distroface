package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.RepositoryServiceHandler = (*RepositoryService)(nil)

type RepositoryService struct {
	store    *storage.Store
	registry *registry.RegistryAccess
	enforcer *rbac.Enforcer
	log      *logger.Logger
}

func NewRepositoryService(store *storage.Store, reg *registry.RegistryAccess, enforcer *rbac.Enforcer, log *logger.Logger) *RepositoryService {
	return &RepositoryService{store: store, registry: reg, enforcer: enforcer, log: log}
}

// canReadRepo checks if the requesting user can read the given repo via RBAC.
// Public repos are readable by anyone. Private repos require the user's roles
// to have repositories.read on the specific object (namespace/name) or wildcard.
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

	return connect.NewResponse(&v1.GetRepositoryResponse{
		Repository: repoToProto(repo),
	}), nil
}

func (s *RepositoryService) ListRepositories(ctx context.Context, req *connect.Request[v1.ListRepositoriesRequest]) (*connect.Response[v1.ListRepositoriesResponse], error) {
	pageSize := int(req.Msg.PageSize)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	offset := 0
	if req.Msg.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Msg.PageToken)
		if err == nil {
			offset, _ = strconv.Atoi(string(decoded))
		}
	}

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

	repos, total, err := s.store.ListRepositories(ctx, req.Msg.Namespace, req.Msg.Query, userID, canManage, grantedRepos, pageSize, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var nextPageToken string
	if offset+pageSize < int(total) {
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset + pageSize)))
	}

	protoRepos := make([]*v1.Repository, len(repos))
	for i, r := range repos {
		protoRepos[i] = repoToProto(r)
	}

	return connect.NewResponse(&v1.ListRepositoriesResponse{
		Repositories:  protoRepos,
		NextPageToken: nextPageToken,
		TotalCount:    int32(total),
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

	pageSize := int(req.Msg.PageSize)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	offset := 0
	if req.Msg.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Msg.PageToken)
		if err == nil {
			offset, _ = strconv.Atoi(string(decoded))
		}
	}

	total := len(tags)
	start := min(offset, total)
	end := min(start+pageSize, total)

	var nextPageToken string
	if end < total {
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}

	return connect.NewResponse(&v1.ListTagsResponse{
		Tags:          tags[start:end],
		NextPageToken: nextPageToken,
		TotalCount:    int32(total),
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

func repoToProto(r *storage.Repository) *v1.Repository {
	vis := v1.Visibility_VISIBILITY_PUBLIC
	if r.IsPrivate {
		vis = v1.Visibility_VISIBILITY_PRIVATE
	}

	repo := &v1.Repository{
		Id:          r.ID,
		Namespace:   r.Namespace,
		Name:        r.Name,
		FullName:    r.Namespace + "/" + r.Name,
		Description: r.Description,
		Visibility:  vis,
		OwnerId:     r.OwnerID,
		PullCount:   r.PullCount,
		PushCount:   r.PushCount,
		CreatedAt:   timestamppb.New(r.CreatedAt),
		UpdatedAt:   timestamppb.New(r.UpdatedAt),
	}

	if r.LastPush != nil {
		repo.LastPushedAt = timestamppb.New(*r.LastPush)
	}

	return repo
}
