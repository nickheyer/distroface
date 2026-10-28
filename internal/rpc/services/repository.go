package services

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/activity"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
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
	git      *gitrepo.Manager
	activity *activity.Recorder
	log      *logger.Logger
}

// Installs the activity recorder, nil keeps the feed quiet
func (s *RepositoryService) SetActivity(rec *activity.Recorder) { s.activity = rec }

func NewRepositoryService(store *stores.Store, reg *registry.RegistryAccess, enforcer *rbac.Enforcer, mirrors *mirror.Monitor, git *gitrepo.Manager, log *logger.Logger) *RepositoryService {
	return &RepositoryService{
		store:    store,
		registry: reg,
		enforcer: enforcer,
		access:   auth.NewRepoAccess(store, enforcer),
		mirrors:  mirrors,
		log:      log, git: git}
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
		if mirrorCfg, err = s.mirrors.EncodeConfig(msg.Mirror); err != nil {
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

	isPrivate := msg.Visibility == v1.Visibility_VISIBILITY_PRIVATE
	if msg.Visibility == v1.Visibility_VISIBILITY_UNSPECIFIED {
		// New packages follow their source repository's visibility
		first := msg.Name
		if i := strings.IndexByte(first, '/'); i > 0 {
			first = first[:i]
		}
		if src, _ := s.store.GetGitRepository(ctx, ns, first); src != nil {
			isPrivate = src.IsPrivate
		}
	}
	repo := &storage.Repository{
		ID:             uuid.New().String(),
		Namespace:      ns,
		Name:           msg.Name,
		Description:    msg.Description,
		OwnerID:        ownerID,
		IsPrivate:      isPrivate,
		IsOrgNamespace: isOrgNamespace,
		Type:           repoType,
		MirrorConfig:   mirrorCfg,
	}
	if err := s.store.CreateRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if s.git != nil {
		s.git.LinkImageByName(ctx, repo)
	}
	if s.activity != nil {
		s.activity.Image(repo, user, activity.KindCreated, "", nil)
	}

	proto := s.repoToProto(repo)
	s.fillSources(ctx, []*storage.Repository{repo}, []*v1.Repository{proto})

	return &v1.CreateRepositoryResponse{
		Repository: proto,
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
	s.fillSources(ctx, []*storage.Repository{repo}, []*v1.Repository{proto})

	return &v1.GetRepositoryResponse{
		Repository: proto,
	}, nil
}

func (s *RepositoryService) ListRepositories(ctx context.Context, req *v1.ListRepositoriesRequest) (*v1.ListRepositoriesResponse, error) {
	pageSize, offset := pages.Parse(req.Page)

	// Resolve visibility: admin sees all, authenticated users see public +
	// owned + org membership + RBAC grants, anonymous sees public only.
	user := auth.UserFromContext(ctx)
	opts := stores.RepoListOptions{Namespace: portal.ScopeNamespace(ctx, req.Namespace), Limit: pageSize, Offset: offset}
	if user != nil {
		opts.ViewerID = user.ID
		opts.IncludePrivate, _ = s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, "*")
		if !opts.IncludePrivate {
			opts.GrantedRepos = s.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceRepositories, rbac.ActionRead)
		}
		opts.OnlyRepo, opts.OnlyNamespace = user.ListingScope()
	}

	opts.Query = pages.ParseQuery(req.Page)
	if err := stores.ReposQuery.Validate(opts.Query); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	repos, total, err := s.store.ListRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoRepos := make([]*v1.Repository, len(repos))
	for i, r := range repos {
		protoRepos[i] = s.repoToProto(r)
	}
	s.attachStars(ctx, protoRepos)
	s.fillSources(ctx, repos, protoRepos)

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
	s.markReleases(ctx, repo, tags)

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
		merged, err := s.mirrors.ApplyUpdate(repo.MirrorConfig, req.Mirror)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		full, err := s.mirrors.Config(merged)
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

	proto := s.repoToProto(repo)
	s.fillSources(ctx, []*storage.Repository{repo}, []*v1.Repository{proto})

	return &v1.UpdateRepositoryResponse{
		Repository: proto,
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
	created, err := s.store.Star(ctx, user.ID, storage.StarKindImage, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if created && s.activity != nil {
		s.activity.Image(repo, user, activity.KindStar, "", nil)
	}

	count, err := s.store.CountStars(ctx, storage.StarKindImage, repo.ID)
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
	if err := s.store.Unstar(ctx, user.ID, storage.StarKindImage, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	count, err := s.store.CountStars(ctx, storage.StarKindImage, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UnstarRepositoryResponse{StarCount: count}, nil
}

func (s *RepositoryService) ListStarredRepositories(ctx context.Context, req *v1.ListStarredRepositoriesRequest) (*v1.ListStarredRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	targetID, err := starredTarget(ctx, s.store, user, req.Username)
	if err != nil {
		return nil, err
	}

	limit, offset := pages.Parse(req.Page)
	opts := stores.RepoListOptions{Limit: limit, Offset: offset}
	if user != nil {
		opts.ViewerID = user.ID
		opts.IncludePrivate, _ = s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, "*")
		if !opts.IncludePrivate {
			opts.GrantedRepos = s.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceRepositories, rbac.ActionRead)
		}
		opts.OnlyRepo, opts.OnlyNamespace = user.ListingScope()
	}

	repos, total, err := s.store.ListStarredRepositories(ctx, targetID, opts)
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
	s.fillSources(ctx, repos, protoRepos)

	return &v1.ListStarredRepositoriesResponse{
		Repositories: protoRepos,
		Page:         pages.Info(offset, limit, total),
	}, nil
}

// Resolves whose stars a listing shows, the caller's when no username is given
func starredTarget(ctx context.Context, store *stores.Store, user *auth.AuthenticatedUser, username string) (string, error) {
	if username == "" || (user != nil && username == user.Username) {
		if user == nil || user.IsAnonymous() {
			return "", connect.NewError(connect.CodeUnauthenticated, "")
		}
		return user.ID, nil
	}
	u, err := store.GetUserByUsername(ctx, username)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if u == nil {
		return "", connect.Errorf(connect.CodeNotFound, "user not found")
	}
	return u.ID, nil
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

	counts, err := s.store.StarCounts(ctx, storage.StarKindImage, ids)
	if err != nil {
		s.log.Error("loading star counts: %v", err)
		return
	}

	var starred map[string]bool
	if user := auth.UserFromContext(ctx); user != nil {
		if starred, err = s.store.StarredSet(ctx, user.ID, storage.StarKindImage, ids); err != nil {
			s.log.Error("loading starred set: %v", err)
		}
	}

	for _, r := range repos {
		r.StarCount = counts[r.Id]
		r.IsStarred = starred[r.Id]
	}
}

// Fills source_repository from the linked git repositories
func (s *RepositoryService) fillSources(ctx context.Context, rows []*storage.Repository, protos []*v1.Repository) {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.GitRepoID != nil {
			ids = append(ids, *r.GitRepoID)
		}
	}
	if len(ids) == 0 {
		return
	}
	gits, err := s.store.GetGitRepositoriesByIDs(ctx, ids)
	if err != nil {
		return
	}
	for i, r := range rows {
		if r.GitRepoID == nil || i >= len(protos) {
			continue
		}
		if g := gits[*r.GitRepoID]; g != nil {
			protos[i].SourceRepository = g.Namespace + "/" + g.Name
		}
	}
}

// Flags tags matching or pinned by a release of the source repository
func (s *RepositoryService) markReleases(ctx context.Context, repo *storage.Repository, tags []*v1.Tag) {
	if repo.GitRepoID == nil {
		return
	}
	byTag, _ := s.store.GitReleaseTags(ctx, *repo.GitRepoID)
	pinned, _ := s.store.GitReleaseImageTags(ctx, repo.ID)
	for _, t := range tags {
		t.HasRelease = byTag[t.Name] || pinned[t.Name] != ""
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
