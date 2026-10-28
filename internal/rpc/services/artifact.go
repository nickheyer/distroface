package services

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/activity"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.ArtifactServiceHandler = (*ArtifactService)(nil)

var artifactRepoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type ArtifactService struct {
	store    *stores.Store
	manager  *artifacts.Manager
	access   *artifacts.Access
	mirrors  *mirror.Monitor
	git      *gitrepo.Manager
	activity *activity.Recorder
	log      *logger.Logger
}

// Installs the activity recorder, nil keeps the feed quiet
func (s *ArtifactService) SetActivity(rec *activity.Recorder) { s.activity = rec }

func NewArtifactService(store *stores.Store, manager *artifacts.Manager, enforcer *rbac.Enforcer, mirrors *mirror.Monitor, git *gitrepo.Manager, log *logger.Logger) *ArtifactService {
	access := artifacts.NewAccess(store, enforcer)
	if git != nil {
		access.SetLinkedAccess(git.Access())
	}
	return &ArtifactService{store: store, manager: manager, access: access, mirrors: mirrors, git: git, log: log}
}

// ── Repositories ─────────────────────────────────────────────────────────

func (s *ArtifactService) CreateArtifactRepository(ctx context.Context, req *v1.CreateArtifactRepositoryRequest) (*v1.CreateArtifactRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}

	msg := req
	ns, name := repoRef(ctx, user, msg.Namespace, msg.Name)
	if !artifactRepoNamePattern.MatchString(name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if !s.access.CanCreateInNamespace(ctx, user, ns) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot create repository in namespace %q", ns)
	}

	existing, err := s.store.GetArtifactRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "repository %q already exists", ns+"/"+name)
	}

	repoType := msg.Type
	if repoType == v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_UNSPECIFIED {
		repoType = v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_FILE
	}
	mirrorCfg := ""
	if repoType != v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_FILE {
		if err := s.mirrors.ValidateArtifactMirror(ctx, repoType, msg.Mirror); err != nil {
			return nil, mapMirrorErr(err)
		}
		if mirrorCfg, err = s.mirrors.EncodeConfig(msg.Mirror); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	} else if msg.Mirror != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "file repositories do not take mirror settings")
	}

	isPrivate := msg.IsPrivate
	if !isPrivate && ns != user.Username {
		isPrivate = s.manager.EffectivePrivateByDefault(ctx, ns)
	}
	if src, _ := s.store.GetGitRepository(ctx, ns, name); src != nil && src.IsPrivate {
		isPrivate = true
	}
	repo := &storage.ArtifactRepository{
		Namespace:    ns,
		Name:         name,
		Description:  msg.Description,
		OwnerID:      user.ID,
		IsPrivate:    isPrivate,
		Type:         repoType,
		MirrorConfig: mirrorCfg,
	}
	if err := s.store.CreateArtifactRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if s.git != nil {
		s.git.LinkArtifactByName(ctx, repo)
	}
	if s.activity != nil {
		s.activity.Artifact(repo, user, activity.KindCreated, "", "")
	}

	return &v1.CreateArtifactRepositoryResponse{
		Repository: s.repoToProto(ctx, repo, nil),
	}, nil
}

func (s *ArtifactService) GetArtifactRepository(ctx context.Context, req *v1.GetArtifactRepositoryRequest) (*v1.GetArtifactRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.visibleRepo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}

	stats, err := s.store.GetArtifactRepoStats(ctx, []int64{repo.ID})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	out := s.repoToProto(ctx, repo, stats)
	s.attachStars(ctx, []*v1.ArtifactRepository{out})
	return &v1.GetArtifactRepositoryResponse{Repository: out}, nil
}

func (s *ArtifactService) ListArtifactRepositories(ctx context.Context, req *v1.ListArtifactRepositoriesRequest) (*v1.ListArtifactRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	limit, offset := pages.Parse(msg.Page)

	q := pages.ParseQuery(msg.Page)
	if err := stores.ArtifactReposQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	opts := s.access.ListOptions(user, portal.ScopeNamespace(ctx, msg.Namespace))
	opts.Query = q
	opts.Limit = limit
	opts.Offset = offset

	repos, total, err := s.store.ListArtifactRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	ids := make([]int64, len(repos))
	for i, r := range repos {
		ids[i] = r.ID
	}
	stats, err := s.store.GetArtifactRepoStats(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	protoRepos := make([]*v1.ArtifactRepository, len(repos))
	for i, r := range repos {
		protoRepos[i] = s.repoToProto(ctx, r, stats)
	}
	s.attachStars(ctx, protoRepos)

	return &v1.ListArtifactRepositoriesResponse{
		Repositories: protoRepos,
		Page:         pages.Info(offset, limit, total),
	}, nil
}

// ── Stars ────────────────────────────────────────────────────────────────

// Fills star counts and the caller's starred flags
func (s *ArtifactService) attachStars(ctx context.Context, repos []*v1.ArtifactRepository) {
	if len(repos) == 0 {
		return
	}
	ids := make([]string, len(repos))
	for i, r := range repos {
		ids[i] = strconv.FormatInt(r.Id, 10)
	}
	counts, err := s.store.StarCounts(ctx, storage.StarKindArtifact, ids)
	if err != nil {
		s.log.Error("loading artifact star counts: %v", err)
		return
	}
	var starred map[string]bool
	if user := auth.UserFromContext(ctx); user != nil && !user.IsAnonymous() {
		if starred, err = s.store.StarredSet(ctx, user.ID, storage.StarKindArtifact, ids); err != nil {
			s.log.Error("loading artifact starred set: %v", err)
		}
	}
	for i, r := range repos {
		r.StarCount = counts[ids[i]]
		r.IsStarred = starred[ids[i]]
	}
}

func (s *ArtifactService) star(ctx context.Context, namespace, name string, star bool) (int64, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return 0, connect.NewError(connect.CodeUnauthenticated, "")
	}
	repo, err := s.visibleRepo(ctx, user, namespace, name)
	if err != nil {
		return 0, err
	}
	id := strconv.FormatInt(repo.ID, 10)
	if star {
		created, err := s.store.Star(ctx, user.ID, storage.StarKindArtifact, id)
		if err != nil {
			return 0, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if created && s.activity != nil {
			s.activity.Artifact(repo, user, activity.KindStar, "", "")
		}
	} else if err := s.store.Unstar(ctx, user.ID, storage.StarKindArtifact, id); err != nil {
		return 0, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	n, err := s.store.CountStars(ctx, storage.StarKindArtifact, id)
	if err != nil {
		return 0, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return n, nil
}

func (s *ArtifactService) StarArtifactRepository(ctx context.Context, req *v1.StarArtifactRepositoryRequest) (*v1.StarArtifactRepositoryResponse, error) {
	n, err := s.star(ctx, req.Namespace, req.Name, true)
	if err != nil {
		return nil, err
	}
	return &v1.StarArtifactRepositoryResponse{StarCount: n}, nil
}

func (s *ArtifactService) UnstarArtifactRepository(ctx context.Context, req *v1.UnstarArtifactRepositoryRequest) (*v1.UnstarArtifactRepositoryResponse, error) {
	n, err := s.star(ctx, req.Namespace, req.Name, false)
	if err != nil {
		return nil, err
	}
	return &v1.UnstarArtifactRepositoryResponse{StarCount: n}, nil
}

func (s *ArtifactService) ListStarredArtifactRepositories(ctx context.Context, req *v1.ListStarredArtifactRepositoriesRequest) (*v1.ListStarredArtifactRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	targetID, err := starredTarget(ctx, s.store, user, req.Username)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	opts := s.access.ListOptions(user, "")
	opts.StarredBy = targetID
	opts.Limit = limit
	opts.Offset = offset
	repos, total, err := s.store.ListArtifactRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	ids := make([]int64, len(repos))
	for i, r := range repos {
		ids[i] = r.ID
	}
	stats, err := s.store.GetArtifactRepoStats(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.ArtifactRepository, len(repos))
	for i, r := range repos {
		out[i] = s.repoToProto(ctx, r, stats)
	}
	s.attachStars(ctx, out)
	return &v1.ListStarredArtifactRepositoriesResponse{Repositories: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *ArtifactService) UpdateArtifactRepository(ctx context.Context, req *v1.UpdateArtifactRepositoryRequest) (*v1.UpdateArtifactRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.mutableRepo(ctx, user, req.Namespace, req.Name, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}

	if req.Description != nil {
		repo.Description = *req.Description
	}
	if req.IsPrivate != nil {
		repo.IsPrivate = *req.IsPrivate
	}
	if req.Mirror != nil {
		if repo.Type == v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_FILE {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "file repositories do not take mirror settings")
		}
		merged, err := s.mirrors.ApplyUpdate(repo.MirrorConfig, req.Mirror)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		full, err := s.mirrors.Config(merged)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		if err := s.mirrors.ValidateArtifactMirror(ctx, repo.Type, full); err != nil {
			return nil, mapMirrorErr(err)
		}
		repo.MirrorConfig = merged
		// Fresh config invalidates the conditional request cursor
		repo.MirrorState = ""
	}
	if err := s.store.UpdateArtifactRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.UpdateArtifactRepositoryResponse{
		Repository: s.repoToProto(ctx, repo, nil),
	}, nil
}

func (s *ArtifactService) DeleteArtifactRepository(ctx context.Context, req *v1.DeleteArtifactRepositoryRequest) (*v1.DeleteArtifactRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.mutableRepo(ctx, user, req.Namespace, req.Name, rbac.ActionDelete)
	if err != nil {
		return nil, err
	}

	if err := s.manager.DeleteRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteArtifactRepositoryResponse{}, nil
}

func (s *ArtifactService) SyncArtifactRepository(ctx context.Context, req *v1.SyncArtifactRepositoryRequest) (*v1.SyncArtifactRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.mutableRepo(ctx, user, req.Namespace, req.Name, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}
	if err := s.mirrors.SyncArtifactRepoNow(repo); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.SyncArtifactRepositoryResponse{}, nil
}

func (s *ArtifactService) StopArtifactRepositorySync(ctx context.Context, req *v1.StopArtifactRepositorySyncRequest) (*v1.StopArtifactRepositorySyncResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.mutableRepo(ctx, user, req.Namespace, req.Name, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}
	if err := s.mirrors.StopArtifactSync(repo); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.StopArtifactRepositorySyncResponse{}, nil
}

// ── Uploads ──────────────────────────────────────────────────────────────

func (s *ArtifactService) InitiateArtifactUpload(ctx context.Context, req *v1.InitiateArtifactUploadRequest) (*v1.InitiateArtifactUploadResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.pushableRepo(ctx, user, req.Namespace, req.RepoName)
	if err != nil {
		return nil, err
	}

	uploadID, err := s.manager.Blobs().InitiateOwnedUpload(auth.UploadOwner(user))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.InitiateArtifactUploadResponse{
		UploadId:  uploadID,
		UploadUrl: artifactUploadURL(user, repo.Namespace, repo.Name, uploadID),
	}, nil
}

func (s *ArtifactService) CompleteArtifactUpload(ctx context.Context, req *v1.CompleteArtifactUploadRequest) (*v1.CompleteArtifactUploadResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	repo, err := s.pushableRepo(ctx, user, msg.Namespace, msg.RepoName)
	if err != nil {
		return nil, err
	}

	if err := s.manager.Blobs().VerifyUploadOwner(msg.UploadId, auth.UploadOwners(user)...); err != nil {
		return nil, mapArtifactErr(err)
	}
	artifact, err := s.manager.CompleteUpload(ctx, repo, msg.UploadId, msg.Version, msg.Path, msg.Metadata, msg.Properties)
	if err != nil {
		return nil, mapArtifactErr(err)
	}
	if s.activity != nil {
		s.activity.Artifact(repo, user, activity.KindArtifactUpload, artifact.Version, artifact.Path)
	}

	return &v1.CompleteArtifactUploadResponse{
		Artifact: artifactToProto(artifact),
	}, nil
}

// ── Reads ────────────────────────────────────────────────────────────────

func (s *ArtifactService) GetArtifact(ctx context.Context, req *v1.GetArtifactRequest) (*v1.GetArtifactResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.visibleRepo(ctx, user, req.Namespace, req.RepoName)
	if err != nil {
		return nil, err
	}

	artifact, err := s.repoArtifact(ctx, repo, req.Id)
	if err != nil {
		return nil, err
	}

	return &v1.GetArtifactResponse{
		Artifact: artifactToProto(artifact),
	}, nil
}

func (s *ArtifactService) ListArtifacts(ctx context.Context, req *v1.ListArtifactsRequest) (*v1.ListArtifactsResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	repo, err := s.visibleRepo(ctx, user, msg.Namespace, msg.RepoName)
	if err != nil {
		return nil, err
	}

	limit, offset := pages.Parse(msg.Page)
	list, total, err := s.store.ListArtifacts(ctx, repo.ID, msg.Version, kindOf(msg.Workflow), limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.ListArtifactsResponse{
		Artifacts: artifactsToProto(list),
		Page:      pages.Info(offset, limit, total),
	}, nil
}

func (s *ArtifactService) ListArtifactVersions(ctx context.Context, req *v1.ListArtifactVersionsRequest) (*v1.ListArtifactVersionsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.visibleRepo(ctx, user, req.Namespace, req.RepoName)
	if err != nil {
		return nil, err
	}

	limit, offset := pages.Parse(req.Page)
	byVersion, desc := versionOrder(req.Page)
	kind := kindOf(req.Workflow)
	order, total, err := s.store.ListArtifactVersionPage(ctx, repo.ID, kind, byVersion, desc, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	list, err := s.store.ListArtifactsByVersions(ctx, repo.ID, kind, order)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	grouped := make(map[string][]*storage.Artifact)
	for _, a := range list { // Already newest first
		grouped[a.Version] = append(grouped[a.Version], a)
	}
	var releases map[string]bool
	if repo.GitRepoID != nil && !req.Workflow {
		releases, _ = s.store.GitReleaseTags(ctx, *repo.GitRepoID)
	}

	versions := make([]*v1.ArtifactVersionGroup, len(order))
	for i, ver := range order {
		group := &v1.ArtifactVersionGroup{
			Version:    ver,
			Artifacts:  artifactsToProto(grouped[ver]),
			HasRelease: releases[ver],
		}
		if rows := grouped[ver]; req.Workflow && len(rows) > 0 {
			group.RunId = rows[0].Properties["actions.run"]
		}
		versions[i] = group
	}

	return &v1.ListArtifactVersionsResponse{
		Versions: versions,
		Page:     pages.Info(offset, limit, total),
	}, nil
}

// Listings show package contents unless the caller asks for run uploads
func kindOf(workflow bool) stores.KindFilter {
	if workflow {
		return stores.KindWorkflow
	}
	return stores.KindRegular
}

// Version groups default to natural version order newest first
func versionOrder(p *v1.PageRequest) (byVersion, desc bool) {
	fields := strings.Fields(strings.ToLower(p.GetOrderBy()))
	if len(fields) == 0 {
		return true, true
	}
	desc = len(fields) > 1 && fields[1] == "desc"
	switch fields[0] {
	case "activity", "created_at", "latest":
		return false, desc
	default:
		return true, desc
	}
}

func (s *ArtifactService) SearchArtifacts(ctx context.Context, req *v1.SearchArtifactsRequest) (*v1.SearchArtifactsResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	limit, offset := pages.Parse(msg.Page)

	q := pages.ParseQuery(msg.Page)
	if err := stores.ArtifactsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	criteria := stores.ArtifactSearchCriteria{
		Kind:       kindOf(msg.Workflow),
		Query:      q,
		Properties: msg.Properties,
		OrderBy:    pages.OrderBy(msg.Page, stores.ArtifactSortColumns, "created_at DESC"),
		Limit:      limit,
		Offset:     offset,
	}

	if msg.RepoName != "" {
		repo, err := s.visibleRepo(ctx, user, msg.Namespace, msg.RepoName)
		if err != nil {
			return nil, err
		}
		criteria.RepoID = &repo.ID
	} else {
		repoIDs, err := s.visibleRepoIDs(ctx, user, portal.ScopeNamespace(ctx, msg.Namespace))
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if len(repoIDs) == 0 {
			return &v1.SearchArtifactsResponse{Artifacts: []*v1.Artifact{}, Page: &v1.PageInfo{}}, nil
		}
		criteria.RepoIDs = repoIDs
	}

	list, total, err := s.store.SearchArtifacts(ctx, criteria)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.SearchArtifactsResponse{
		Artifacts: artifactsToProto(list),
		Page:      pages.Info(offset, limit, total),
	}, nil
}

// ── Mutations ────────────────────────────────────────────────────────────

func (s *ArtifactService) UpdateArtifact(ctx context.Context, req *v1.UpdateArtifactRequest) (*v1.UpdateArtifactResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	repo, err := s.mutableRepo(ctx, user, msg.Namespace, msg.RepoName, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}

	artifact, err := s.repoArtifact(ctx, repo, msg.Id)
	if err != nil {
		return nil, err
	}

	if msg.Path != nil {
		if err := artifacts.ValidatePath(*msg.Path); err != nil {
			return nil, mapArtifactErr(err)
		}
		artifact.Path = *msg.Path
		artifact.Name = path.Base(*msg.Path)
	}
	if msg.Name != nil && *msg.Name != "" {
		artifact.Name = *msg.Name
		dir := path.Dir(artifact.Path)
		if dir == "." {
			artifact.Path = *msg.Name
		} else {
			artifact.Path = dir + "/" + *msg.Name
		}
	}
	if msg.Version != nil {
		if err := artifacts.ValidateVersion(*msg.Version); err != nil {
			return nil, mapArtifactErr(err)
		}
		artifact.Version = *msg.Version
	}
	if msg.Metadata != nil {
		metadata := *msg.Metadata
		if metadata == "" {
			metadata = "{}"
		}
		artifact.Metadata = metadata
	}

	if err := s.store.UpdateArtifact(ctx, artifact); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, connect.Errorf(connect.CodeAlreadyExists, "an artifact with that version and path already exists")
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	return &v1.UpdateArtifactResponse{
		Artifact: artifactToProto(artifact),
	}, nil
}

func (s *ArtifactService) SetArtifactProperties(ctx context.Context, req *v1.SetArtifactPropertiesRequest) (*v1.SetArtifactPropertiesResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	repo, err := s.mutableRepo(ctx, user, msg.Namespace, msg.RepoName, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}

	artifact, err := s.repoArtifact(ctx, repo, msg.Id)
	if err != nil {
		return nil, err
	}

	if err := s.store.SetArtifactProperties(ctx, artifact.ID, msg.Properties); err != nil {
		if errors.Is(err, stores.ErrDuplicateIdentity) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	artifact.Properties = msg.Properties

	return &v1.SetArtifactPropertiesResponse{
		Artifact: artifactToProto(artifact),
	}, nil
}

func (s *ArtifactService) DeleteArtifact(ctx context.Context, req *v1.DeleteArtifactRequest) (*v1.DeleteArtifactResponse, error) {
	user := auth.UserFromContext(ctx)
	msg := req
	repo, err := s.mutableRepo(ctx, user, msg.Namespace, msg.RepoName, rbac.ActionDelete)
	if err != nil {
		return nil, err
	}

	var artifact *storage.Artifact
	if msg.Id != "" {
		artifact, err = s.repoArtifact(ctx, repo, msg.Id)
		if err != nil {
			return nil, err
		}
	} else {
		if msg.Version == "" || msg.Path == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "id or version+path is required")
		}
		artifact, err = s.store.GetArtifactByPathVersion(ctx, repo.ID, msg.Version, msg.Path)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if artifact == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "artifact not found")
		}
	}

	if err := s.manager.DeleteArtifact(ctx, artifact); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteArtifactResponse{}, nil
}

// ── Access helpers ───────────────────────────────────────────────────────

// Portal mapping first, then empty namespace defaults to caller username
func repoRef(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (string, string) {
	namespace, name = portal.ScopeRepoRef(ctx, namespace, name)
	if namespace == "" && user != nil {
		namespace = user.Username
	}
	return namespace, name
}

// Fetches repo and enforces read visibility
func (s *ArtifactService) visibleRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.ArtifactRepository, error) {
	if name == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "repository name is required")
	}
	bare := namespace == ""
	ns, name := repoRef(ctx, user, namespace, name)
	if portal.ForeignRef(ctx, ns) {
		return nil, connect.Errorf(connect.CodeNotFound, "artifact repository not found")
	}
	repo, err := s.store.GetArtifactRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	// Portal traffic resolves by mapping alone, never fuzzy
	if repo == nil && bare && portal.FromContext(ctx) == nil {
		if repo, err = s.visibleRepoByName(ctx, user, name); err != nil {
			return nil, err
		}
	}
	if repo == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "artifact repository not found")
	}
	if repo.IsPrivate && !s.access.HasRepoAccess(ctx, user, repo, rbac.ActionRead) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	return repo, nil
}

// Bare refs fall back to the unique visible match anywhere
func (s *ArtifactService) visibleRepoByName(ctx context.Context, user *auth.AuthenticatedUser, name string) (*storage.ArtifactRepository, error) {
	repos, _, err := s.store.ListArtifactRepositories(ctx, s.access.ListOptions(user, ""))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	var matches []*storage.ArtifactRepository
	for _, r := range repos {
		if r.Name == name {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		var full []string
		for _, m := range matches {
			full = append(full, m.Namespace+"/"+m.Name)
		}
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"repository name %q is ambiguous, qualify it as one of: %s", name, strings.Join(full, ", "))
	}
}

// Upload access, private repos need owner or grant
func (s *ArtifactService) pushableRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.ArtifactRepository, error) {
	repo, cerr := s.visibleRepo(ctx, user, namespace, name)
	if cerr != nil {
		return nil, cerr
	}
	if repo.IsPrivate && !s.access.HasRepoAccess(ctx, user, repo, rbac.ActionPush) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	return repo, nil
}

// Owner level access for destructive operations like v1
func (s *ArtifactService) mutableRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name, action string) (*storage.ArtifactRepository, error) {
	if name == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "repository name is required")
	}
	ns, name := repoRef(ctx, user, namespace, name)
	if portal.ForeignRef(ctx, ns) {
		return nil, connect.Errorf(connect.CodeNotFound, "artifact repository not found")
	}
	repo, err := s.store.GetArtifactRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "artifact repository not found")
	}
	if !s.access.HasRepoAccess(ctx, user, repo, action) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	return repo, nil
}

// Repo ids readable by the user, optionally scoped to a namespace
func (s *ArtifactService) visibleRepoIDs(ctx context.Context, user *auth.AuthenticatedUser, namespace string) ([]int64, error) {
	repos, _, err := s.store.ListArtifactRepositories(ctx, s.access.ListOptions(user, namespace))
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// Upload URL, org repos carry a namespace marker the v1 facade strips
func artifactUploadURL(user *auth.AuthenticatedUser, namespace, name, uploadID string) string {
	if user != nil && namespace == user.Username {
		return fmt.Sprintf("/api/v1/artifacts/%s/upload/%s", name, uploadID)
	}
	return fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/upload/%s", namespace, name, uploadID)
}

// Fetches artifact and checks repo membership
func (s *ArtifactService) repoArtifact(ctx context.Context, repo *storage.ArtifactRepository, id string) (*storage.Artifact, error) {
	if id == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "artifact id is required")
	}
	artifact, err := s.store.GetArtifact(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if artifact == nil || artifact.RepoID != repo.ID {
		return nil, connect.Errorf(connect.CodeNotFound, "artifact not found")
	}
	return artifact, nil
}

func mapArtifactErr(err error) error {
	switch {
	case errors.Is(err, artifacts.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, artifacts.ErrUploadNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, artifacts.ErrUploadDenied):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	default:
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
}

// Config faults map to invalid, unreachable upstreams to unavailable
func mapMirrorErr(err error) error {
	if errors.Is(err, mirror.ErrInvalid) {
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	return connect.NewError(connect.CodeUnavailable, fmt.Errorf("upstream check failed: %w", err).Error())
}

// Busy and cooling down map to precondition failures the ui can show
func mapSyncErr(err error) error {
	var cool *mirror.CooldownError
	switch {
	case errors.Is(err, mirror.ErrSyncInFlight), errors.Is(err, mirror.ErrDisabled), errors.Is(err, mirror.ErrNoActiveSync), errors.As(err, &cool):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, mirror.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	default:
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
}

// ── Proto mapping ────────────────────────────────────────────────────────

func (s *ArtifactService) repoToProto(ctx context.Context, repo *storage.ArtifactRepository, stats map[int64]stores.ArtifactRepoStats) *v1.ArtifactRepository {
	owner := ""
	if repo.OwnerID != "" {
		if u, err := s.store.GetUserByID(ctx, repo.OwnerID); err == nil && u != nil {
			owner = u.Username
		}
	}
	out := &v1.ArtifactRepository{
		Id:              repo.ID,
		Name:            repo.Name,
		Namespace:       repo.Namespace,
		FullName:        repo.Namespace + "/" + repo.Name,
		Description:     repo.Description,
		Owner:           owner,
		IsPrivate:       repo.IsPrivate,
		Type:            repo.Type,
		Mirror:          mirror.Redacted(repo.MirrorConfig),
		MirrorLastError: repo.MirrorLastError,
		CreatedAt:       timestamppb.New(repo.CreatedAt),
		UpdatedAt:       timestamppb.New(repo.UpdatedAt),
	}
	if repo.MirrorLastSync != nil {
		out.MirrorLastSync = timestamppb.New(*repo.MirrorLastSync)
	}
	if st := mirror.ParseState(repo.MirrorState); st.CoolingDown(time.Now()) {
		out.MirrorNextAttempt = timestamppb.New(st.CooldownUntil)
	}
	if s.mirrors != nil {
		out.MirrorSyncing = s.mirrors.IsSyncing(fmt.Sprintf("artifact:%d", repo.ID))
	}
	if st, ok := stats[repo.ID]; ok {
		out.ArtifactCount = st.Count
		out.TotalSize = st.Size
		out.WorkflowArtifactCount = st.WorkflowCount
		out.WorkflowTotalSize = st.WorkflowSize
	}
	if repo.GitRepoID != nil {
		if g, err := s.store.GetGitRepositoryByID(ctx, *repo.GitRepoID); err == nil && g != nil {
			out.SourceRepository = g.Namespace + "/" + g.Name
		}
	}
	return out
}

func artifactToProto(a *storage.Artifact) *v1.Artifact {
	return &v1.Artifact{
		Id:         a.ID,
		RepoId:     a.RepoID,
		Name:       a.Name,
		Path:       a.Path,
		UploadId:   a.UploadID,
		Version:    a.Version,
		Kind:       a.Kind,
		Size:       a.Size,
		MimeType:   a.MimeType,
		Metadata:   a.Metadata,
		Properties: a.Properties,
		Digest:     a.Digest,
		CreatedAt:  timestamppb.New(a.CreatedAt),
		UpdatedAt:  timestamppb.New(a.UpdatedAt),
	}
}

func artifactsToProto(list []*storage.Artifact) []*v1.Artifact {
	out := make([]*v1.Artifact, len(list))
	for i, a := range list {
		out[i] = artifactToProto(a)
	}
	return out
}
