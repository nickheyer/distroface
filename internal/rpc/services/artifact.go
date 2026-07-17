package services

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.ArtifactServiceHandler = (*ArtifactService)(nil)

var artifactRepoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type ArtifactService struct {
	store   *storage.Store
	manager *artifacts.Manager
	access  *artifacts.Access
	log     *logger.Logger
}

func NewArtifactService(store *storage.Store, manager *artifacts.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *ArtifactService {
	return &ArtifactService{store: store, manager: manager, access: artifacts.NewAccess(store, enforcer), log: log}
}

// ── Repositories ─────────────────────────────────────────────────────────

func (s *ArtifactService) CreateArtifactRepository(ctx context.Context, req *connect.Request[v1.CreateArtifactRepositoryRequest]) (*connect.Response[v1.CreateArtifactRepositoryResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	if !artifactRepoNamePattern.MatchString(msg.Name) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid repository name"))
	}

	ns, name := repoRef(user, msg.Namespace, msg.Name)
	if !s.access.CanCreateInNamespace(ctx, user, ns) {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot create repository in namespace %q", ns))
	}

	existing, err := s.store.GetArtifactRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("repository %q already exists", ns+"/"+name))
	}

	isPrivate := msg.IsPrivate
	if !isPrivate && ns != user.Username {
		isPrivate = s.manager.EffectivePrivateByDefault(ctx, ns)
	}
	repo := &storage.ArtifactRepository{
		Namespace:   ns,
		Name:        name,
		Description: msg.Description,
		OwnerID:     user.ID,
		IsPrivate:   isPrivate,
	}
	if err := s.store.CreateArtifactRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.CreateArtifactRepositoryResponse{
		Repository: s.repoToProto(ctx, repo, nil),
	}), nil
}

func (s *ArtifactService) GetArtifactRepository(ctx context.Context, req *connect.Request[v1.GetArtifactRepositoryRequest]) (*connect.Response[v1.GetArtifactRepositoryResponse], error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.visibleRepo(ctx, user, req.Msg.Namespace, req.Msg.Name)
	if err != nil {
		return nil, err
	}

	stats, err := s.store.GetArtifactRepoStats(ctx, []int64{repo.ID})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.GetArtifactRepositoryResponse{
		Repository: s.repoToProto(ctx, repo, stats),
	}), nil
}

func (s *ArtifactService) ListArtifactRepositories(ctx context.Context, req *connect.Request[v1.ListArtifactRepositoriesRequest]) (*connect.Response[v1.ListArtifactRepositoriesResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
	limit, offset := parsePagination(msg.PageSize, msg.PageToken)

	opts := s.access.ListOptions(user, portal.ScopeNamespace(ctx, msg.Namespace))
	opts.Search = msg.Search
	opts.Limit = limit
	opts.Offset = offset

	repos, total, err := s.store.ListArtifactRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	ids := make([]int64, len(repos))
	for i, r := range repos {
		ids[i] = r.ID
	}
	stats, err := s.store.GetArtifactRepoStats(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoRepos := make([]*v1.ArtifactRepository, len(repos))
	for i, r := range repos {
		protoRepos[i] = s.repoToProto(ctx, r, stats)
	}

	return connect.NewResponse(&v1.ListArtifactRepositoriesResponse{
		Repositories:  protoRepos,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    total,
	}), nil
}

func (s *ArtifactService) UpdateArtifactRepository(ctx context.Context, req *connect.Request[v1.UpdateArtifactRepositoryRequest]) (*connect.Response[v1.UpdateArtifactRepositoryResponse], error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.mutableRepo(ctx, user, req.Msg.Namespace, req.Msg.Name, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}

	if req.Msg.Description != nil {
		repo.Description = *req.Msg.Description
	}
	if req.Msg.IsPrivate != nil {
		repo.IsPrivate = *req.Msg.IsPrivate
	}
	if err := s.store.UpdateArtifactRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UpdateArtifactRepositoryResponse{
		Repository: s.repoToProto(ctx, repo, nil),
	}), nil
}

func (s *ArtifactService) DeleteArtifactRepository(ctx context.Context, req *connect.Request[v1.DeleteArtifactRepositoryRequest]) (*connect.Response[v1.DeleteArtifactRepositoryResponse], error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.mutableRepo(ctx, user, req.Msg.Namespace, req.Msg.Name, rbac.ActionDelete)
	if err != nil {
		return nil, err
	}

	if err := s.manager.DeleteRepository(ctx, repo); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.DeleteArtifactRepositoryResponse{}), nil
}

// ── Uploads ──────────────────────────────────────────────────────────────

func (s *ArtifactService) InitiateArtifactUpload(ctx context.Context, req *connect.Request[v1.InitiateArtifactUploadRequest]) (*connect.Response[v1.InitiateArtifactUploadResponse], error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.pushableRepo(ctx, user, req.Msg.Namespace, req.Msg.RepoName)
	if err != nil {
		return nil, err
	}

	uploadID, err := s.manager.Blobs().InitiateUpload()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.InitiateArtifactUploadResponse{
		UploadId:  uploadID,
		UploadUrl: artifactUploadURL(user, repo.Namespace, repo.Name, uploadID),
	}), nil
}

func (s *ArtifactService) CompleteArtifactUpload(ctx context.Context, req *connect.Request[v1.CompleteArtifactUploadRequest]) (*connect.Response[v1.CompleteArtifactUploadResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
	repo, err := s.pushableRepo(ctx, user, msg.Namespace, msg.RepoName)
	if err != nil {
		return nil, err
	}

	artifact, err := s.manager.CompleteUpload(ctx, repo, msg.UploadId, msg.Version, msg.Path, msg.Metadata, msg.Properties)
	if err != nil {
		return nil, mapArtifactErr(err)
	}

	return connect.NewResponse(&v1.CompleteArtifactUploadResponse{
		Artifact: artifactToProto(artifact),
	}), nil
}

// ── Reads ────────────────────────────────────────────────────────────────

func (s *ArtifactService) GetArtifact(ctx context.Context, req *connect.Request[v1.GetArtifactRequest]) (*connect.Response[v1.GetArtifactResponse], error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.visibleRepo(ctx, user, req.Msg.Namespace, req.Msg.RepoName)
	if err != nil {
		return nil, err
	}

	artifact, err := s.repoArtifact(ctx, repo, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&v1.GetArtifactResponse{
		Artifact: artifactToProto(artifact),
	}), nil
}

func (s *ArtifactService) ListArtifacts(ctx context.Context, req *connect.Request[v1.ListArtifactsRequest]) (*connect.Response[v1.ListArtifactsResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
	repo, err := s.visibleRepo(ctx, user, msg.Namespace, msg.RepoName)
	if err != nil {
		return nil, err
	}

	limit, offset := parsePagination(msg.PageSize, msg.PageToken)
	list, total, err := s.store.ListArtifacts(ctx, repo.ID, msg.Version, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.ListArtifactsResponse{
		Artifacts:     artifactsToProto(list),
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    total,
	}), nil
}

func (s *ArtifactService) ListArtifactVersions(ctx context.Context, req *connect.Request[v1.ListArtifactVersionsRequest]) (*connect.Response[v1.ListArtifactVersionsResponse], error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.visibleRepo(ctx, user, req.Msg.Namespace, req.Msg.RepoName)
	if err != nil {
		return nil, err
	}

	list, _, err := s.store.ListArtifacts(ctx, repo.ID, "", 0, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	grouped := make(map[string][]*storage.Artifact)
	order := []string{}
	for _, a := range list { // Already newest first
		if _, ok := grouped[a.Version]; !ok {
			order = append(order, a.Version)
		}
		grouped[a.Version] = append(grouped[a.Version], a)
	}

	versions := make([]*v1.ArtifactVersionGroup, len(order))
	for i, ver := range order {
		versions[i] = &v1.ArtifactVersionGroup{
			Version:   ver,
			Artifacts: artifactsToProto(grouped[ver]),
		}
	}

	return connect.NewResponse(&v1.ListArtifactVersionsResponse{Versions: versions}), nil
}

func (s *ArtifactService) SearchArtifacts(ctx context.Context, req *connect.Request[v1.SearchArtifactsRequest]) (*connect.Response[v1.SearchArtifactsResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
	limit, offset := parsePagination(msg.PageSize, msg.PageToken)

	criteria := storage.ArtifactSearchCriteria{
		Name:       msg.Name,
		Version:    msg.Version,
		Path:       msg.Path,
		Properties: msg.Properties,
		Sort:       msg.Sort,
		Order:      strings.ToUpper(msg.Order),
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
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if len(repoIDs) == 0 {
			return connect.NewResponse(&v1.SearchArtifactsResponse{Artifacts: []*v1.Artifact{}}), nil
		}
		criteria.RepoIDs = repoIDs
	}

	list, total, err := s.store.SearchArtifacts(ctx, criteria)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.SearchArtifactsResponse{
		Artifacts:     artifactsToProto(list),
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    total,
	}), nil
}

// ── Mutations ────────────────────────────────────────────────────────────

func (s *ArtifactService) UpdateArtifact(ctx context.Context, req *connect.Request[v1.UpdateArtifactRequest]) (*connect.Response[v1.UpdateArtifactResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
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
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("an artifact with that version and path already exists"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UpdateArtifactResponse{
		Artifact: artifactToProto(artifact),
	}), nil
}

func (s *ArtifactService) SetArtifactProperties(ctx context.Context, req *connect.Request[v1.SetArtifactPropertiesRequest]) (*connect.Response[v1.SetArtifactPropertiesResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
	repo, err := s.mutableRepo(ctx, user, msg.Namespace, msg.RepoName, rbac.ActionUpdate)
	if err != nil {
		return nil, err
	}

	artifact, err := s.repoArtifact(ctx, repo, msg.Id)
	if err != nil {
		return nil, err
	}

	if err := s.store.SetArtifactProperties(ctx, artifact.ID, msg.Properties); err != nil {
		if errors.Is(err, storage.ErrDuplicateIdentity) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	artifact.Properties = msg.Properties

	return connect.NewResponse(&v1.SetArtifactPropertiesResponse{
		Artifact: artifactToProto(artifact),
	}), nil
}

func (s *ArtifactService) DeleteArtifact(ctx context.Context, req *connect.Request[v1.DeleteArtifactRequest]) (*connect.Response[v1.DeleteArtifactResponse], error) {
	user := auth.UserFromContext(ctx)
	msg := req.Msg
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
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id or version+path is required"))
		}
		artifact, err = s.store.GetArtifactByPathVersion(ctx, repo.ID, msg.Version, msg.Path)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if artifact == nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("artifact not found"))
		}
	}

	if err := s.manager.DeleteArtifact(ctx, artifact); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.DeleteArtifactResponse{}), nil
}

// ── Access helpers ───────────────────────────────────────────────────────

// Empty namespace defaults to the caller username
func repoRef(user *auth.AuthenticatedUser, namespace, name string) (string, string) {
	if namespace == "" && user != nil {
		namespace = user.Username
	}
	return namespace, name
}

// Fetches repo and enforces read visibility
func (s *ArtifactService) visibleRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.ArtifactRepository, error) {
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("repository name is required"))
	}
	ns, name := repoRef(user, namespace, name)
	repo, err := s.store.GetArtifactRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("artifact repository not found"))
	}
	if repo.IsPrivate && !s.access.HasRepoAccess(ctx, user, repo, rbac.ActionRead) {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("access denied"))
	}
	return repo, nil
}

// Upload access, private repos need owner or grant
func (s *ArtifactService) pushableRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.ArtifactRepository, error) {
	repo, cerr := s.visibleRepo(ctx, user, namespace, name)
	if cerr != nil {
		return nil, cerr
	}
	if repo.IsPrivate && !s.access.HasRepoAccess(ctx, user, repo, rbac.ActionPush) {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("access denied"))
	}
	return repo, nil
}

// Owner level access for destructive operations like v1
func (s *ArtifactService) mutableRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name, action string) (*storage.ArtifactRepository, error) {
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("repository name is required"))
	}
	ns, name := repoRef(user, namespace, name)
	repo, err := s.store.GetArtifactRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if repo == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("artifact repository not found"))
	}
	if !s.access.HasRepoAccess(ctx, user, repo, action) {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("access denied"))
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
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("artifact id is required"))
	}
	artifact, err := s.store.GetArtifact(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if artifact == nil || artifact.RepoID != repo.ID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("artifact not found"))
	}
	return artifact, nil
}

func mapArtifactErr(err error) error {
	switch {
	case errors.Is(err, artifacts.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, artifacts.ErrUploadNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// ── Proto mapping ────────────────────────────────────────────────────────

func (s *ArtifactService) repoToProto(ctx context.Context, repo *storage.ArtifactRepository, stats map[int64]storage.ArtifactRepoStats) *v1.ArtifactRepository {
	owner := ""
	if repo.OwnerID != "" {
		if u, err := s.store.GetUserByID(ctx, repo.OwnerID); err == nil && u != nil {
			owner = u.Username
		}
	}
	out := &v1.ArtifactRepository{
		Id:          repo.ID,
		Name:        repo.Name,
		Namespace:   repo.Namespace,
		FullName:    repo.Namespace + "/" + repo.Name,
		Description: repo.Description,
		Owner:       owner,
		IsPrivate:   repo.IsPrivate,
		CreatedAt:   timestamppb.New(repo.CreatedAt),
		UpdatedAt:   timestamppb.New(repo.UpdatedAt),
	}
	if st, ok := stats[repo.ID]; ok {
		out.ArtifactCount = st.Count
		out.TotalSize = st.Size
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
