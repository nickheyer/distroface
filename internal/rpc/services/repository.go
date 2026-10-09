package services

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/natsort"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.RepositoryServiceHandler = (*RepositoryService)(nil)

type RepositoryService struct {
	store    *stores.Store
	registry *registry.RegistryAccess
	enforcer *rbac.Enforcer
	access   *auth.RepoAccess
	mirrors  *mirror.Monitor
	log      *logger.Logger
}

func NewRepositoryService(store *stores.Store, reg *registry.RegistryAccess, enforcer *rbac.Enforcer, mirrors *mirror.Monitor, log *logger.Logger) *RepositoryService {
	return &RepositoryService{
		store:    store,
		registry: reg,
		enforcer: enforcer,
		access:   auth.NewRepoAccess(store, enforcer),
		mirrors:  mirrors,
		log:      log,
	}
}

var imageRepoNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// Namespace owners create at will, others need the manage grant
func (s *RepositoryService) canCreateInNamespace(ctx context.Context, user *auth.AuthenticatedUser, namespace string) bool {
	if namespace == user.Username {
		return true
	}
	if isMember, role, _ := s.store.IsOrgMember(ctx, namespace, user.ID); isMember {
		return role == storage.OrgRoleOwner || role == storage.OrgRoleAdmin
	}
	return s.enforcer.HasPermission(user.Roles, rbac.ResourceRepositories, rbac.ActionManage)
}

func (s *RepositoryService) CreateRepository(ctx context.Context, req *v1.CreateRepositoryRequest) (*v1.CreateRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	msg := req
	ns := msg.Namespace
	if ns == "" {
		ns = user.Username
	}
	if portal.ForeignRef(ctx, ns) {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}
	if !imageRepoNamePattern.MatchString(msg.Name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if !s.canCreateInNamespace(ctx, user, ns) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot create repository in namespace %q", ns)
	}

	existing, err := s.store.GetRepository(ctx, ns, msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "repository %q already exists", ns+"/"+msg.Name)
	}

	repoType := msg.Type
	if repoType == v1.RepositoryType_REPOSITORY_TYPE_UNSPECIFIED {
		repoType = v1.RepositoryType_REPOSITORY_TYPE_STANDARD
	}
	mirrorCfg := ""
	if repoType == v1.RepositoryType_REPOSITORY_TYPE_MIRROR {
		if err := s.mirrors.ValidateRegistryMirror(ctx, msg.Mirror); err != nil {
			return nil, mapMirrorErr(err)
		}
		if mirrorCfg, err = mirror.EncodeConfig(msg.Mirror); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	} else if msg.Mirror != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "standard repositories do not take mirror settings")
	}

	ownerID := user.ID
	isOrgNamespace := false
	if ns != user.Username {
		if org, _ := s.store.GetOrganization(ctx, ns); org != nil {
			ownerID = org.ID
			isOrgNamespace = true
		}
	}

	repo := &storage.Repository{
		ID:             uuid.New().String(),
		Namespace:      ns,
		Name:           msg.Name,
		Description:    msg.Description,
		OwnerID:        ownerID,
		IsPrivate:      msg.Visibility == v1.Visibility_VISIBILITY_PRIVATE,
		IsOrgNamespace: isOrgNamespace,
		Type:           repoType,
		MirrorConfig:   mirrorCfg,
	}
	if err := s.store.CreateRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.CreateRepositoryResponse{
		Repository: s.repoToProto(repo),
	}, nil
}

// Public repos show, private ones need an explicit relationship
func (s *RepositoryService) canReadRepo(ctx context.Context, repo *storage.Repository) bool {
	if portal.ForeignRef(ctx, repo.Namespace) {
		return false
	}
	return s.access.CanSee(ctx, auth.UserFromContext(ctx), repo)
}

func (s *RepositoryService) GetRepository(ctx context.Context, req *v1.GetRepositoryRequest) (*v1.GetRepositoryResponse, error) {
	if req.Namespace == "" || req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	if !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	proto := s.repoToProto(repo)
	s.attachStars(ctx, []*v1.Repository{proto})

	return &v1.GetRepositoryResponse{
		Repository: proto,
	}, nil
}

func (s *RepositoryService) ListRepositories(ctx context.Context, req *v1.ListRepositoriesRequest) (*v1.ListRepositoriesResponse, error) {
	pageSize, offset := pages.Parse(req.Page)

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

	namespace := portal.ScopeNamespace(ctx, req.Namespace)
	q := pages.ParseQuery(req.Page)
	if err := stores.ReposQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	repos, total, err := s.store.ListRepositories(ctx, namespace, q, userID, canManage, grantedRepos, pageSize, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoRepos := make([]*v1.Repository, len(repos))
	for i, r := range repos {
		protoRepos[i] = s.repoToProto(r)
	}
	s.attachStars(ctx, protoRepos)

	return &v1.ListRepositoriesResponse{
		Repositories: protoRepos,
		Page:         pages.Info(offset, pageSize, total),
	}, nil
}

func (s *RepositoryService) DeleteRepository(ctx context.Context, req *v1.DeleteRepositoryRequest) (*v1.DeleteRepositoryResponse, error) {
	if req.Namespace == "" || req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}
	if portal.ForeignRef(ctx, req.Namespace) {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	objectID := repo.Namespace + "/" + repo.Name
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID)
	if !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.NewError(connect.CodePermissionDenied, "")
			}
		}
	}

	if err := s.store.DeleteRepository(ctx, req.Namespace, req.Name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.DeleteRepositoryResponse{}, nil
}

func (s *RepositoryService) ListTags(ctx context.Context, req *v1.ListTagsRequest) (*v1.ListTagsResponse, error) {
	if req.Namespace == "" || req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	if !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	tags, err := s.registry.ListTags(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	page := req.Page
	if page == nil {
		page = &v1.PageRequest{}
	}
	if page.OrderBy == "" {
		page.OrderBy = "version desc"
	}

	byVersion := natsort.TagVersionComparator(tags)
	pages.Sort(page, tags, map[string]func(a, b *v1.Tag) int{
		"name":      byVersion,
		"version":   byVersion,
		"size":      func(a, b *v1.Tag) int { return cmp.Compare(a.SizeBytes, b.SizeBytes) },
		"pushed_at": func(a, b *v1.Tag) int { return a.GetPushedAt().AsTime().Compare(b.GetPushedAt().AsTime()) },
	})

	pageSize, offset := pages.Parse(page)

	total := len(tags)
	start := min(offset, total)
	end := min(start+pageSize, total)

	return &v1.ListTagsResponse{
		Tags: tags[start:end],
		Page: pages.Info(start, pageSize, int64(total)),
	}, nil
}

func (s *RepositoryService) ResolveTag(ctx context.Context, req *v1.ResolveTagRequest) (*v1.ResolveTagResponse, error) {
	if req.Namespace == "" || req.Name == "" || req.Tag == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}

	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	if !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	desc, err := s.registry.ResolveTag(ctx, req.Namespace, req.Name, req.Tag)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("tag %q: %w", req.Tag, err).Error())
	}

	return &v1.ResolveTagResponse{
		Descriptor_: desc,
	}, nil
}

func (s *RepositoryService) UpdateRepository(ctx context.Context, req *v1.UpdateRepositoryRequest) (*v1.UpdateRepositoryResponse, error) {
	if req.Namespace == "" || req.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}
	if portal.ForeignRef(ctx, req.Namespace) {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	objectID := repo.Namespace + "/" + repo.Name
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID)
	if !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.NewError(connect.CodePermissionDenied, "")
			}
		}
	}

	if req.Description != nil {
		repo.Description = *req.Description
	}
	if req.Visibility != nil {
		repo.IsPrivate = *req.Visibility == v1.Visibility_VISIBILITY_PRIVATE
	}
	if req.Mirror != nil {
		if repo.Type != v1.RepositoryType_REPOSITORY_TYPE_MIRROR {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "standard repositories do not take mirror settings")
		}
		merged, err := mirror.ApplyUpdate(repo.MirrorConfig, req.Mirror)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		full, err := mirror.ParseConfig(merged)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		if err := s.mirrors.ValidateRegistryMirror(ctx, full); err != nil {
			return nil, mapMirrorErr(err)
		}
		repo.MirrorConfig = merged
		// Fresh config invalidates the conditional request cursor
		repo.MirrorState = ""
	}

	if err := s.store.UpdateRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.UpdateRepositoryResponse{
		Repository: s.repoToProto(repo),
	}, nil
}

func (s *RepositoryService) SyncRepository(ctx context.Context, req *v1.SyncRepositoryRequest) (*v1.SyncRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	objectID := repo.Namespace + "/" + repo.Name
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID)
	if !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.NewError(connect.CodePermissionDenied, "")
			}
		}
	}

	if err := s.mirrors.SyncImageRepoNow(repo); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.SyncRepositoryResponse{}, nil
}

func (s *RepositoryService) StopRepositorySync(ctx context.Context, req *v1.StopRepositorySyncRequest) (*v1.StopRepositorySyncResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	repo, err := s.store.GetRepository(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}

	objectID := repo.Namespace + "/" + repo.Name
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, objectID)
	if !canManage {
		if user.Username != repo.Namespace {
			isMember, role, _ := s.store.IsOrgMember(ctx, repo.Namespace, user.ID)
			if !isMember || (role != storage.OrgRoleOwner && role != storage.OrgRoleAdmin) {
				return nil, connect.NewError(connect.CodePermissionDenied, "")
			}
		}
	}

	if err := s.mirrors.StopImageSync(repo); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.StopRepositorySyncResponse{}, nil
}

func (s *RepositoryService) StarRepository(ctx context.Context, req *v1.StarRepositoryRequest) (*v1.StarRepositoryResponse, error) {
	repo, err := s.starTarget(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}

	user := auth.UserFromContext(ctx)
	if err := s.store.StarRepository(ctx, user.ID, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	count, err := s.store.CountStars(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.StarRepositoryResponse{StarCount: count}, nil
}

func (s *RepositoryService) UnstarRepository(ctx context.Context, req *v1.UnstarRepositoryRequest) (*v1.UnstarRepositoryResponse, error) {
	repo, err := s.starTarget(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}

	user := auth.UserFromContext(ctx)
	if err := s.store.UnstarRepository(ctx, user.ID, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	count, err := s.store.CountStars(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UnstarRepositoryResponse{StarCount: count}, nil
}

func (s *RepositoryService) ListStarredRepositories(ctx context.Context, req *v1.ListStarredRepositoriesRequest) (*v1.ListStarredRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	limit, offset := pages.Parse(req.Page)

	repos, total, err := s.store.ListStarredRepositories(ctx, user.ID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	// Isolated portals drop stars pointing outside the org
	if p := portal.FromContext(ctx); p != nil && p.Isolated {
		kept := repos[:0]
		for _, r := range repos {
			if r.Namespace == p.OrgName {
				kept = append(kept, r)
			}
		}
		total -= int64(len(repos) - len(kept))
		repos = kept
	}

	protoRepos := make([]*v1.Repository, len(repos))
	for i, r := range repos {
		protoRepos[i] = s.repoToProto(r)
	}
	s.attachStars(ctx, protoRepos)

	return &v1.ListStarredRepositoriesResponse{
		Repositories: protoRepos,
		Page:         pages.Info(offset, limit, total),
	}, nil
}

// Validates auth and read access for star mutations
func (s *RepositoryService) starTarget(ctx context.Context, namespace, name string) (*storage.Repository, error) {
	if namespace == "" || name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "")
	}
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	repo, err := s.store.GetRepository(ctx, namespace, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil || !s.canReadRepo(ctx, repo) {
		return nil, connect.NewError(connect.CodeNotFound, "")
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

func (s *RepositoryService) repoToProto(r *storage.Repository) *v1.Repository {
	vis := v1.Visibility_VISIBILITY_PUBLIC
	if r.IsPrivate {
		vis = v1.Visibility_VISIBILITY_PRIVATE
	}

	repo := &v1.Repository{
		Id:              r.ID,
		Namespace:       r.Namespace,
		Name:            r.Name,
		FullName:        r.Namespace + "/" + r.Name,
		Description:     r.Description,
		Visibility:      vis,
		OwnerId:         r.OwnerID,
		PullCount:       r.PullCount,
		PushCount:       r.PushCount,
		CreatedAt:       timestamppb.New(r.CreatedAt),
		UpdatedAt:       timestamppb.New(r.UpdatedAt),
		IsOrgNamespace:  r.IsOrgNamespace,
		Type:            r.Type,
		Mirror:          mirror.Redacted(r.MirrorConfig),
		MirrorLastError: r.MirrorLastError,
	}

	if r.LastPush != nil {
		repo.LastPushedAt = timestamppb.New(*r.LastPush)
	}
	if r.MirrorLastSync != nil {
		repo.MirrorLastSync = timestamppb.New(*r.MirrorLastSync)
	}
	if st := mirror.ParseState(r.MirrorState); st.CoolingDown(time.Now()) {
		repo.MirrorNextAttempt = timestamppb.New(st.CooldownUntil)
	}
	if s.mirrors != nil {
		repo.MirrorSyncing = s.mirrors.IsSyncing("image:" + r.ID)
	}

	return repo
}
