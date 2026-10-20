package services

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/nickheyer/distroface/pkg/utils"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.GitServiceHandler = (*GitService)(nil)

var gitTopicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,34}$`)

const gitMaxTopics = 25

// Pending transfers to users expire after a week
const gitTransferTTL = 7 * 24 * time.Hour

type GitService struct {
	store    *stores.Store
	manager  *gitrepo.Manager
	forge    *forge.Manager
	mirrors  *mirror.Monitor
	enforcer *rbac.Enforcer
	log      *logger.Logger

	insights  *gitrepo.Memo[repoInsights]
	languages *gitrepo.Memo[map[string]int64]
}

// Contributors and weekly activity of one commit, computed together
type repoInsights struct {
	contributors []gitrepo.Contributor
	weeks        []gitrepo.WeekActivity
}

// Keyed by repo and head, so a push simply misses
const (
	gitInsightsTTL  = time.Hour
	gitInsightsSize = 512
)

func NewGitService(store *stores.Store, manager *gitrepo.Manager, fm *forge.Manager, mirrors *mirror.Monitor, enforcer *rbac.Enforcer, log *logger.Logger) *GitService {
	return &GitService{
		store: store, manager: manager, forge: fm, mirrors: mirrors, enforcer: enforcer, log: log,
		insights:  gitrepo.NewMemo[repoInsights](gitInsightsTTL, gitInsightsSize),
		languages: gitrepo.NewMemo[map[string]int64](gitInsightsTTL, gitInsightsSize),
	}
}

func (s *GitService) access() *gitrepo.Access { return s.manager.Access() }

func (s *GitService) requireEnabled(ctx context.Context) error {
	if !s.manager.Enabled(ctx) {
		return connect.Errorf(connect.CodeFailedPrecondition, "git hosting is disabled")
	}
	return nil
}

// Loads a visible repository
func (s *GitService) repo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
	return visibleGitRepo(ctx, s.store, s.access(), user, namespace, name)
}

// Loads a repository for an admin or owner level mutation
func (s *GitService) repoForWrite(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string, need gitrepo.Level) (*storage.GitRepository, error) {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, err
	}
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	repo, err := s.repo(ctx, user, namespace, name)
	if err != nil {
		return nil, err
	}
	lv := s.access().Levels(ctx, user, repo)
	switch {
	case need >= gitrepo.LevelOwner && !lv.IsOwner:
		return nil, connect.Errorf(connect.CodePermissionDenied, "owner access required")
	case need == gitrepo.LevelAdmin && !lv.IsOwner && !lv.CanAdmin:
		return nil, connect.Errorf(connect.CodePermissionDenied, "admin access required")
	case need < gitrepo.LevelAdmin && lv.Unit(storage.GitUnitCode) < need:
		return nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	return repo, nil
}

func (s *GitService) signedIn(ctx context.Context) (*auth.AuthenticatedUser, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	return user, nil
}

// ── Repositories ─────────────────────────────────────────────────────────

func (s *GitService) CreateGitRepository(ctx context.Context, req *v1.CreateGitRepositoryRequest) (*v1.CreateGitRepositoryResponse, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, err
	}
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	ns, name := gitRepoRef(ctx, user, req.Namespace, req.Name)
	if !gitrepo.ValidName(name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "repository name must match %s and not end in .git or .wiki", gitrepo.NamePattern.String())
	}
	if !s.access().CanCreateInNamespace(ctx, user, ns) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot create repository in namespace %q", ns)
	}
	repoType := req.Type
	if repoType == v1.GitRepoType_GIT_REPO_TYPE_UNSPECIFIED {
		repoType = v1.GitRepoType_GIT_REPO_TYPE_STANDARD
	}
	if repoType == v1.GitRepoType_GIT_REPO_TYPE_FORK {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "use ForkGitRepository to create a fork")
	}
	mirrorCfg := ""
	if repoType == v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
		if req.Mirror == nil || req.Mirror.Upstream == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "mirror repositories need an upstream")
		}
		if s.mirrors == nil {
			return nil, connect.Errorf(connect.CodeUnavailable, "mirroring is unavailable")
		}
		if err := s.mirrors.ValidateGitMirror(ctx, req.Mirror); err != nil {
			return nil, mapMirrorErr(err)
		}
		if s.mirrors == nil {
			return nil, connect.Errorf(connect.CodeUnavailable, "mirroring is not available")
		}
		if mirrorCfg, err = s.mirrors.EncodeConfig(req.Mirror); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	} else if req.Mirror != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "only mirror repositories take mirror settings")
	}
	for _, t := range req.Topics {
		if !gitTopicPattern.MatchString(t) {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid topic %q", t)
		}
	}
	private := req.Visibility == v1.Visibility_VISIBILITY_PRIVATE
	if req.Visibility == v1.Visibility_VISIBILITY_UNSPECIFIED {
		private = s.manager.NamespaceGitSettings(ctx, ns).GetPrivateByDefault()
	}
	if req.Website != "" && !utils.IsWebURL(req.Website) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "website must be an http or https url")
	}
	params := gitrepo.CreateParams{
		Namespace:     ns,
		Name:          name,
		Description:   req.Description,
		Website:       req.Website,
		Private:       private,
		DefaultBranch: req.DefaultBranch,
		ObjectFormat:  req.ObjectFormat,
		Type:          repoType,
		MirrorConfig:  mirrorCfg,
		AutoInit:      req.AutoInit && repoType == v1.GitRepoType_GIT_REPO_TYPE_STANDARD,
		Gitignore:     req.GitignoreTemplate,
		License:       req.License,
		Topics:        req.Topics,
	}
	repo, err := s.manager.Create(ctx, user, params)
	if err != nil {
		return nil, mapGitErr(err)
	}
	if repoType == v1.GitRepoType_GIT_REPO_TYPE_TEMPLATE {
		repo.Type = v1.GitRepoType_GIT_REPO_TYPE_TEMPLATE
		_ = s.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"type": repo.Type})
	}
	if repoType == v1.GitRepoType_GIT_REPO_TYPE_MIRROR && s.mirrors != nil {
		if req.MirrorReleases || len(req.MirrorImages) > 0 {
			if err := s.setupCompositeMirror(ctx, repo, req.MirrorReleases, req.MirrorImages); err != nil {
				s.log.Error("composite mirror setup for %s/%s: %v", repo.Namespace, repo.Name, err)
			}
		}
		if req.ImportMetadata {
			if err := s.mirrors.StartGitImport(repo); err != nil {
				s.log.Error("import start for %s/%s: %v", repo.Namespace, repo.Name, err)
			}
		} else if err := s.mirrors.SyncGitRepoNow(repo); err != nil {
			s.log.Debug("initial mirror sync for %s/%s: %v", repo.Namespace, repo.Name, err)
		}
	}
	return &v1.CreateGitRepositoryResponse{Repository: s.gitRepoToProto(ctx, user, repo, nil, true)}, nil
}

func (s *GitService) GetGitRepository(ctx context.Context, req *v1.GetGitRepositoryRequest) (*v1.GetGitRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	return &v1.GetGitRepositoryResponse{Repository: s.gitRepoToProto(ctx, user, repo, nil, true)}, nil
}

func (s *GitService) listOptions(ctx context.Context, user *auth.AuthenticatedUser, page *v1.PageRequest, namespace string) (stores.GitRepoListOptions, error) {
	q := pages.ParseQuery(page)
	if err := stores.GitReposQuery.Validate(q); err != nil {
		return stores.GitRepoListOptions{}, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	limit, offset := pages.Parse(page)
	opts := s.access().ListOptions(user, portal.ScopeNamespace(ctx, namespace))
	opts.Query = q
	opts.Limit = limit
	opts.Offset = offset
	opts.OrderBy = pages.OrderBy(page, stores.GitRepoSortColumns, "")
	return opts, nil
}

func (s *GitService) ListGitRepositories(ctx context.Context, req *v1.ListGitRepositoriesRequest) (*v1.ListGitRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	opts, err := s.listOptions(ctx, user, req.Page, req.Namespace)
	if err != nil {
		return nil, err
	}
	opts.Type = req.Type
	opts.Topic = req.Topic
	opts.IncludeArchived = req.IncludeArchived
	opts.Forks = req.Forks
	repos, total, err := s.store.ListGitRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.ListGitRepositoriesResponse{
		Repositories: s.gitReposToProto(ctx, user, repos),
		Page:         pages.Info(opts.Offset, opts.Limit, total),
	}, nil
}

func (s *GitService) SearchGitRepositories(ctx context.Context, req *v1.SearchGitRepositoriesRequest) (*v1.SearchGitRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	opts, err := s.listOptions(ctx, user, req.Page, "")
	if err != nil {
		return nil, err
	}
	opts.IncludeArchived = true
	repos, total, err := s.store.ListGitRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.SearchGitRepositoriesResponse{
		Repositories: s.gitReposToProto(ctx, user, repos),
		Page:         pages.Info(opts.Offset, opts.Limit, total),
	}, nil
}

func (s *GitService) UpdateGitRepository(ctx context.Context, req *v1.UpdateGitRepositoryRequest) (*v1.UpdateGitRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	if repo.IsArchived {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is archived")
	}
	cols := map[string]any{}
	if req.Description != nil {
		cols["description"] = *req.Description
	}
	if req.Website != nil {
		if *req.Website != "" && !utils.IsWebURL(*req.Website) {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "website must be an http or https url")
		}
		cols["website"] = *req.Website
	}
	if req.Visibility != nil && *req.Visibility != v1.Visibility_VISIBILITY_UNSPECIFIED {
		cols["is_private"] = *req.Visibility == v1.Visibility_VISIBILITY_PRIVATE
	}
	if req.LfsEnabled != nil {
		cols["lfs_enabled"] = *req.LfsEnabled
	}
	if req.IsTemplate != nil {
		if *req.IsTemplate {
			if repo.Type != v1.GitRepoType_GIT_REPO_TYPE_STANDARD && repo.Type != v1.GitRepoType_GIT_REPO_TYPE_TEMPLATE {
				return nil, connect.Errorf(connect.CodeFailedPrecondition, "only standard repositories can become templates")
			}
			cols["type"] = v1.GitRepoType_GIT_REPO_TYPE_TEMPLATE
		} else if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_TEMPLATE {
			cols["type"] = v1.GitRepoType_GIT_REPO_TYPE_STANDARD
		}
	}
	if req.Units != nil {
		cols["units"] = gitrepo.EncodeUnits(req.Units)
	}
	if req.Merge != nil {
		cols["merge_settings"] = gitrepo.EncodeMergeSettings(req.Merge)
	}
	if req.DefaultBranch != nil && *req.DefaultBranch != repo.DefaultBranch {
		r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
		if err != nil {
			return nil, mapGitErr(err)
		}
		exists := !r.BranchHash(*req.DefaultBranch).IsZero()
		empty := r.IsEmpty()
		if exists {
			_ = r.SetHead(*req.DefaultBranch)
		}
		r.Close()
		if !exists && !empty {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "branch %q does not exist", *req.DefaultBranch)
		}
		cols["default_branch"] = *req.DefaultBranch
	}
	if req.Mirror != nil {
		if repo.Type != v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "only mirror repositories take mirror settings")
		}
		if s.mirrors == nil {
			return nil, connect.Errorf(connect.CodeUnavailable, "mirroring is not available")
		}
		merged, err := s.mirrors.ApplyUpdate(repo.MirrorConfig, req.Mirror)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		full, err := s.mirrors.Config(merged)
		if err != nil {
			return nil, mapMirrorErr(err)
		}
		{
			if err := s.mirrors.ValidateGitMirror(ctx, full); err != nil {
				return nil, mapMirrorErr(err)
			}
		}
		cols["mirror_config"] = merged
	}
	if len(cols) > 0 {
		cols["updated_at"] = time.Now().UTC()
		if err := s.store.UpdateGitRepositoryColumns(ctx, repo.ID, cols); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}
	repo, err = s.store.GetGitRepositoryByID(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UpdateGitRepositoryResponse{Repository: s.gitRepoToProto(ctx, user, repo, nil, true)}, nil
}

func (s *GitService) DeleteGitRepository(ctx context.Context, req *v1.DeleteGitRepositoryRequest) (*v1.DeleteGitRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelOwner)
	if err != nil {
		return nil, err
	}
	if err := s.manager.Delete(ctx, user, repo, req.DeleteLinked); err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.DeleteGitRepositoryResponse{}, nil
}

func (s *GitService) MoveGitRepository(ctx context.Context, req *v1.MoveGitRepositoryRequest) (*v1.MoveGitRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelOwner)
	if err != nil {
		return nil, err
	}
	newNS, newName := req.NewNamespace, req.NewName
	if newNS == "" {
		newNS = repo.Namespace
	}
	if newName == "" {
		newName = repo.Name
	}
	if !gitrepo.ValidName(newName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if newNS == repo.Namespace && newName == repo.Name {
		return &v1.MoveGitRepositoryResponse{Repository: s.gitRepoToProto(ctx, user, repo, nil, true)}, nil
	}
	if existing, _ := s.store.GetGitRepository(ctx, newNS, newName); existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "%s/%s already exists", newNS, newName)
	}
	if newNS != repo.Namespace && !s.access().CanCreateInNamespace(ctx, user, newNS) {
		// Transfers to another user wait for that user's acceptance
		target, _ := s.store.GetUserByUsername(ctx, newNS)
		if target == nil {
			return nil, connect.Errorf(connect.CodePermissionDenied, "cannot move repository into namespace %q", newNS)
		}
		t := &storage.GitTransfer{
			RepoID:      repo.ID,
			ToNamespace: newNS,
			NewName:     newName,
			RequestedBy: user.ID,
			ExpiresAt:   time.Now().Add(gitTransferTTL),
			CreatedAt:   time.Now(),
		}
		if err := s.store.CreateGitTransfer(ctx, t); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if s.forge != nil {
			s.forge.Notify(ctx, forge.Notification{
				Repo: repo, Kind: forge.NotifyTransfer, Actor: user, Recipients: []string{target.ID},
				Subject: "Repository transfer: " + repo.Namespace + "/" + repo.Name,
				Text:    user.Username + " wants to transfer " + repo.Namespace + "/" + repo.Name + " to you. Accept it from your notifications within seven days.",
				URL:     s.manager.BaseURL(ctx) + "/notifications",
			})
		}
		return &v1.MoveGitRepositoryResponse{Transfer: gitTransferToProto(t, repo)}, nil
	}
	moved, err := s.manager.Move(ctx, user, repo, newNS, newName)
	if err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.MoveGitRepositoryResponse{Repository: s.gitRepoToProto(ctx, user, moved, nil, true)}, nil
}

func (s *GitService) transferForUser(ctx context.Context, user *auth.AuthenticatedUser, id string) (*storage.GitTransfer, *storage.GitRepository, error) {
	if id == "" {
		return nil, nil, connect.Errorf(connect.CodeInvalidArgument, "transfer id is required")
	}
	t, err := s.store.GetGitTransfer(ctx, id)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if t == nil || t.ExpiresAt.Before(time.Now()) {
		return nil, nil, connect.Errorf(connect.CodeNotFound, "transfer not found")
	}
	if t.ToNamespace != user.Username && !s.enforcer.HasPermission(user.Roles, rbac.ResourceGit, rbac.ActionManage) {
		return nil, nil, connect.Errorf(connect.CodePermissionDenied, "transfer is addressed to another user")
	}
	repo, err := s.store.GetGitRepositoryByID(ctx, t.RepoID)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		_ = s.store.DeleteGitTransfer(ctx, t.ID)
		return nil, nil, connect.Errorf(connect.CodeNotFound, "repository no longer exists")
	}
	return t, repo, nil
}

func (s *GitService) AcceptTransfer(ctx context.Context, req *v1.AcceptTransferRequest) (*v1.AcceptTransferResponse, error) {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, err
	}
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	t, repo, err := s.transferForUser(ctx, user, req.Id)
	if err != nil {
		return nil, err
	}
	name := t.NewName
	if name == "" {
		name = repo.Name
	}
	moved, err := s.manager.Move(ctx, user, repo, t.ToNamespace, name)
	if err != nil {
		return nil, mapGitErr(err)
	}
	_ = s.store.DeleteGitTransfer(ctx, t.ID)
	return &v1.AcceptTransferResponse{Repository: s.gitRepoToProto(ctx, user, moved, nil, true)}, nil
}

func (s *GitService) RejectTransfer(ctx context.Context, req *v1.RejectTransferRequest) (*v1.RejectTransferResponse, error) {
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	t, _, err := s.transferForUser(ctx, user, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteGitTransfer(ctx, t.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.RejectTransferResponse{}, nil
}

func (s *GitService) ListTransfers(ctx context.Context, req *v1.ListTransfersRequest) (*v1.ListTransfersResponse, error) {
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	transfers, total, err := s.store.ListGitTransfersTo(ctx, []string{user.Username}, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.GitTransfer, 0, len(transfers))
	for _, t := range transfers {
		repo, _ := s.store.GetGitRepositoryByID(ctx, t.RepoID)
		out = append(out, gitTransferToProto(t, repo))
	}
	return &v1.ListTransfersResponse{Transfers: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *GitService) setArchived(ctx context.Context, namespace, name string, archived bool) (*v1.GitRepository, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, namespace, name, gitrepo.LevelOwner)
	if err != nil {
		return nil, err
	}
	if repo.IsArchived != archived {
		if err := s.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"is_archived": archived, "updated_at": time.Now().UTC()}); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		repo.IsArchived = archived
		action := "archived"
		if !archived {
			action = "unarchived"
		}
		s.manager.DispatchGit(ctx, "git_repository", repo, gitRepoEventPayload(user, action))
	}
	return s.gitRepoToProto(ctx, user, repo, nil, true), nil
}

func (s *GitService) ArchiveGitRepository(ctx context.Context, req *v1.ArchiveGitRepositoryRequest) (*v1.ArchiveGitRepositoryResponse, error) {
	repo, err := s.setArchived(ctx, req.Namespace, req.Name, true)
	if err != nil {
		return nil, err
	}
	return &v1.ArchiveGitRepositoryResponse{Repository: repo}, nil
}

func (s *GitService) UnarchiveGitRepository(ctx context.Context, req *v1.UnarchiveGitRepositoryRequest) (*v1.UnarchiveGitRepositoryResponse, error) {
	repo, err := s.setArchived(ctx, req.Namespace, req.Name, false)
	if err != nil {
		return nil, err
	}
	return &v1.UnarchiveGitRepositoryResponse{Repository: repo}, nil
}

// ── Forks and templates ──────────────────────────────────────────────────

func (s *GitService) ForkGitRepository(ctx context.Context, req *v1.ForkGitRepositoryRequest) (*v1.ForkGitRepositoryResponse, error) {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, err
	}
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	parent, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if !s.manager.GitSettings(ctx, parent).GetAllowForks() || !gitrepo.MergeSettingsOf(parent).AllowForks {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "forking is disabled for this repository")
	}
	target := req.TargetNamespace
	if target == "" {
		target = user.Username
	}
	if !s.access().CanCreateInNamespace(ctx, user, target) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot create repository in namespace %q", target)
	}
	name := req.TargetName
	if name == "" {
		name = parent.Name
	}
	if !gitrepo.ValidName(name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if target == parent.Namespace && name == parent.Name {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a fork needs a different namespace or name")
	}
	fork, err := s.manager.Fork(ctx, user, parent, target, name)
	if err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.ForkGitRepositoryResponse{Repository: s.gitRepoToProto(ctx, user, fork, nil, true)}, nil
}

func (s *GitService) SyncFork(ctx context.Context, req *v1.SyncForkRequest) (*v1.SyncForkResponse, error) {
	user := auth.UserFromContext(ctx)
	fork, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if fork.ForkOfID == nil {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is not a fork")
	}
	parent, err := s.store.GetGitRepositoryByID(ctx, *fork.ForkOfID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if parent == nil || !s.access().CanSee(ctx, user, parent) {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "the parent repository is no longer reachable")
	}
	branch := fork.DefaultBranch
	staging := plumbing.ReferenceName("refs/distroface/sync/" + branch)
	if err := s.manager.FetchLocal(ctx, fork, parent, false, "+refs/heads/"+branch+":"+staging.String()); err != nil {
		return nil, mapGitErr(err)
	}
	r, err := s.manager.Engine().OpenRepo(fork.Namespace, fork.Name, false)
	if err != nil {
		return nil, mapGitErr(err)
	}
	upstream, err := r.Resolve(staging.String())
	if err != nil {
		r.Close()
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "branch %q does not exist upstream", branch)
	}
	local := r.BranchHash(branch)
	defer s.manager.DropStagingRef(fork, staging)
	if local == upstream {
		r.Close()
		return &v1.SyncForkResponse{}, nil
	}
	ff := local.IsZero()
	if !ff {
		ff, _ = r.IsAncestor(local, upstream)
	}
	// A protected default branch takes upstream through its own rules, by pull request
	if ff {
		protections, _ := s.store.ListGitBranchProtections(ctx, fork.ID)
		ff = gitrepo.MatchProtection(protections, branch) == nil
	}
	if !ff {
		r.Close()
		// Diverged and protected forks get a pull request from the parent instead
		if s.forge == nil {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "fork has diverged from upstream, open a pull request to merge upstream changes")
		}
		levels := s.access().Levels(ctx, user, fork)
		actor := forge.Actor{User: user, CanWrite: levels.Unit(storage.GitUnitPulls) >= gitrepo.LevelWrite, CanAdmin: levels.CanAdmin || levels.IsOwner}
		if existing, _ := s.store.FindOpenPull(ctx, fork.ID, parent.ID, branch, branch); existing != nil {
			return &v1.SyncForkResponse{PullRequestNumber: existing.Number}, nil
		}
		issue, _, err := s.forge.CreatePull(ctx, fork, actor, forge.NewPull{
			Base: branch, Head: parent.Namespace + "/" + parent.Name + ":" + branch,
			Title: fmt.Sprintf("Sync %s from %s/%s", branch, parent.Namespace, parent.Name),
			Body:  "Upstream changes that could not be applied to this fork directly.",
		})
		if err != nil {
			return nil, mapForgeErr(err)
		}
		return &v1.SyncForkResponse{PullRequestNumber: issue.Number}, nil
	}
	commits, _, _ := r.CommitsBetween(ctx, local, upstream, 10000)
	r.Close()
	if err := s.manager.UpdateRef(ctx, fork, user, plumbing.NewBranchReferenceName(branch), local, upstream, gitrepo.HookOptions{}); err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.SyncForkResponse{Commits: int32(len(commits))}, nil
}

func (s *GitService) ListForks(ctx context.Context, req *v1.ListForksRequest) (*v1.ListForksResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	forks, _, err := s.store.ListGitForks(ctx, repo.ID, 0, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	visible := forks[:0]
	for _, f := range forks {
		if s.access().CanSee(ctx, user, f) {
			visible = append(visible, f)
		}
	}
	total := int64(len(visible))
	end := min(offset+limit, len(visible))
	if offset > len(visible) {
		offset = len(visible)
	}
	return &v1.ListForksResponse{
		Repositories: s.gitReposToProto(ctx, user, visible[offset:end]),
		Page:         pages.Info(offset, limit, total),
	}, nil
}

func (s *GitService) UseTemplate(ctx context.Context, req *v1.UseTemplateRequest) (*v1.UseTemplateResponse, error) {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, err
	}
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	tpl, err := s.repo(ctx, user, req.TemplateNamespace, req.TemplateName)
	if err != nil {
		return nil, err
	}
	if tpl.Type != v1.GitRepoType_GIT_REPO_TYPE_TEMPLATE {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is not a template")
	}
	ns, name := gitRepoRef(ctx, user, req.Namespace, req.Name)
	if !gitrepo.ValidName(name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid repository name")
	}
	if !s.access().CanCreateInNamespace(ctx, user, ns) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "cannot create repository in namespace %q", ns)
	}
	private := req.Visibility == v1.Visibility_VISIBILITY_PRIVATE
	if req.Visibility == v1.Visibility_VISIBILITY_UNSPECIFIED {
		private = tpl.IsPrivate || s.manager.NamespaceGitSettings(ctx, ns).GetPrivateByDefault()
	}
	repo, err := s.manager.UseTemplate(ctx, user, tpl, gitrepo.CreateParams{
		Namespace: ns, Name: name, Description: req.Description, Private: private,
	})
	if err != nil {
		return nil, mapGitErr(err)
	}
	if req.IncludeLabels {
		labels, _, err := s.store.ListGitLabels(ctx, tpl.ID, "", pages.Query{}, 0, 0)
		if err == nil {
			for _, l := range labels {
				clone := *l
				clone.ID = ""
				clone.RepoID = &repo.ID
				clone.OrgID = nil
				_ = s.store.CreateGitLabel(ctx, &clone)
			}
		}
	}
	return &v1.UseTemplateResponse{Repository: s.gitRepoToProto(ctx, user, repo, nil, true)}, nil
}

// ── Topics, stars, watching ──────────────────────────────────────────────

func (s *GitService) SetTopics(ctx context.Context, req *v1.SetTopicsRequest) (*v1.SetTopicsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	topics := make([]string, 0, len(req.Topics))
	for _, t := range req.Topics {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if !gitTopicPattern.MatchString(t) {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid topic %q", t)
		}
		seen[t] = true
		topics = append(topics, t)
	}
	if len(topics) > gitMaxTopics {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "at most %d topics are allowed", gitMaxTopics)
	}
	if err := s.store.SetGitTopics(ctx, repo.ID, topics); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.SetTopicsResponse{Topics: topics}, nil
}

func (s *GitService) star(ctx context.Context, namespace, name string, star bool) (int64, error) {
	user, err := s.signedIn(ctx)
	if err != nil {
		return 0, err
	}
	repo, err := s.repo(ctx, user, namespace, name)
	if err != nil {
		return 0, err
	}
	if star {
		err = s.store.StarGitRepository(ctx, user.ID, repo.ID)
	} else {
		err = s.store.UnstarGitRepository(ctx, user.ID, repo.ID)
	}
	if err != nil {
		return 0, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	counts, _ := s.store.GetGitStarCounts(ctx, []string{repo.ID})
	return counts[repo.ID], nil
}

func (s *GitService) StarGitRepository(ctx context.Context, req *v1.StarGitRepositoryRequest) (*v1.StarGitRepositoryResponse, error) {
	n, err := s.star(ctx, req.Namespace, req.Name, true)
	if err != nil {
		return nil, err
	}
	return &v1.StarGitRepositoryResponse{StarCount: n}, nil
}

func (s *GitService) UnstarGitRepository(ctx context.Context, req *v1.UnstarGitRepositoryRequest) (*v1.UnstarGitRepositoryResponse, error) {
	n, err := s.star(ctx, req.Namespace, req.Name, false)
	if err != nil {
		return nil, err
	}
	return &v1.UnstarGitRepositoryResponse{StarCount: n}, nil
}

func (s *GitService) ListStarredGitRepositories(ctx context.Context, req *v1.ListStarredGitRepositoriesRequest) (*v1.ListStarredGitRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	target := user
	if req.Username != "" && (user == nil || req.Username != user.Username) {
		u, err := s.store.GetUserByUsername(ctx, req.Username)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if u == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "user not found")
		}
		target = &auth.AuthenticatedUser{ID: u.ID, Username: u.Username}
	}
	if target == nil || target.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	opts, err := s.listOptions(ctx, user, req.Page, "")
	if err != nil {
		return nil, err
	}
	opts.StarredBy = target.ID
	opts.IncludeArchived = true
	repos, total, err := s.store.ListGitRepositories(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.ListStarredGitRepositoriesResponse{
		Repositories: s.gitReposToProto(ctx, user, repos),
		Page:         pages.Info(opts.Offset, opts.Limit, total),
	}, nil
}

func (s *GitService) WatchGitRepository(ctx context.Context, req *v1.WatchGitRepositoryRequest) (*v1.WatchGitRepositoryResponse, error) {
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	mode := gitWatchModeString(req.Mode)
	if err := s.store.SetGitWatch(ctx, user.ID, repo.ID, mode); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.WatchGitRepositoryResponse{Mode: gitWatchModeProto(mode)}, nil
}

// ── Mirrors ──────────────────────────────────────────────────────────────

func (s *GitService) mirrorRepo(ctx context.Context, namespace, name string) (*storage.GitRepository, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, namespace, name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	if s.mirrors == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "mirroring is unavailable")
	}
	return repo, nil
}

func (s *GitService) SyncGitRepository(ctx context.Context, req *v1.SyncGitRepositoryRequest) (*v1.SyncGitRepositoryResponse, error) {
	repo, err := s.mirrorRepo(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if repo.Type != v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is not a mirror")
	}
	if err := s.mirrors.SyncGitRepoNow(repo); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.SyncGitRepositoryResponse{}, nil
}

func (s *GitService) StopGitRepositorySync(ctx context.Context, req *v1.StopGitRepositorySyncRequest) (*v1.StopGitRepositorySyncResponse, error) {
	repo, err := s.mirrorRepo(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.mirrors.StopGitSync(repo); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.StopGitRepositorySyncResponse{}, nil
}

func (s *GitService) GenerateMirrorKey(ctx context.Context, req *v1.GenerateMirrorKeyRequest) (*v1.GenerateMirrorKeyResponse, error) {
	repo, err := s.mirrorRepo(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	pub, err := s.mirrors.GenerateGitMirrorKey(ctx, repo)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.GenerateMirrorKeyResponse{PublicKey: pub}, nil
}

func (s *GitService) ListPushMirrors(ctx context.Context, req *v1.ListPushMirrorsRequest) (*v1.ListPushMirrorsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListGitPushMirrors(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.GitPushMirror, 0, len(rows))
	for _, pm := range rows {
		out = append(out, s.gitPushMirrorToProto(pm))
	}
	return &v1.ListPushMirrorsResponse{Mirrors: out}, nil
}

func (s *GitService) CreatePushMirror(ctx context.Context, req *v1.CreatePushMirrorRequest) (*v1.CreatePushMirrorResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	if _, err := gitrepo.ValidateUpstreamURL(req.RemoteUrl); err != nil {
		return nil, mapGitErr(err)
	}
	if req.UseSshKey && s.mirrors != nil && s.mirrors.GitMirrorPublicKey(repo) == "" {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "generate the repository ssh key first")
	}
	pm := &storage.GitPushMirror{
		RepoID:          repo.ID,
		RemoteURL:       strings.TrimSpace(req.RemoteUrl),
		AuthToken:       s.mirrors.SealToken(req.AuthToken),
		Username:        req.Username,
		UseSSHKey:       req.UseSshKey,
		IntervalMinutes: max(req.IntervalMinutes, 0),
		SyncOnPush:      req.SyncOnPush,
	}
	if err := s.store.CreateGitPushMirror(ctx, pm); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.CreatePushMirrorResponse{Mirror: s.gitPushMirrorToProto(pm)}, nil
}

func (s *GitService) pushMirror(ctx context.Context, namespace, name, id string) (*storage.GitRepository, *storage.GitPushMirror, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, namespace, name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, connect.Errorf(connect.CodeInvalidArgument, "push mirror id is required")
	}
	pm, err := s.store.GetGitPushMirror(ctx, repo.ID, id)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if pm == nil {
		return nil, nil, connect.Errorf(connect.CodeNotFound, "push mirror not found")
	}
	return repo, pm, nil
}

func (s *GitService) UpdatePushMirror(ctx context.Context, req *v1.UpdatePushMirrorRequest) (*v1.UpdatePushMirrorResponse, error) {
	repo, pm, err := s.pushMirror(ctx, req.Namespace, req.Name, req.Id)
	if err != nil {
		return nil, err
	}
	if req.RemoteUrl != nil {
		next, err := gitrepo.ValidateUpstreamURL(*req.RemoteUrl)
		if err != nil {
			return nil, mapGitErr(err)
		}
		// A stored credential never follows the mirror to another host
		if prev, err := gitrepo.ValidateUpstreamURL(pm.RemoteURL); err == nil && !strings.EqualFold(prev.Hostname(), next.Hostname()) && req.AuthToken == nil {
			pm.AuthToken = ""
		}
		pm.RemoteURL = strings.TrimSpace(*req.RemoteUrl)
	}
	if req.AuthToken != nil {
		pm.AuthToken = s.mirrors.SealToken(*req.AuthToken)
	}
	if req.Username != nil {
		pm.Username = *req.Username
	}
	if req.UseSshKey != nil {
		if *req.UseSshKey && s.mirrors != nil && s.mirrors.GitMirrorPublicKey(repo) == "" {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "generate the repository ssh key first")
		}
		pm.UseSSHKey = *req.UseSshKey
	}
	if req.IntervalMinutes != nil {
		pm.IntervalMinutes = max(*req.IntervalMinutes, 0)
	}
	if req.SyncOnPush != nil {
		pm.SyncOnPush = *req.SyncOnPush
	}
	if err := s.store.UpdateGitPushMirror(ctx, pm); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UpdatePushMirrorResponse{Mirror: s.gitPushMirrorToProto(pm)}, nil
}

func (s *GitService) DeletePushMirror(ctx context.Context, req *v1.DeletePushMirrorRequest) (*v1.DeletePushMirrorResponse, error) {
	repo, pm, err := s.pushMirror(ctx, req.Namespace, req.Name, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteGitPushMirror(ctx, repo.ID, pm.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeletePushMirrorResponse{}, nil
}

func (s *GitService) SyncPushMirror(ctx context.Context, req *v1.SyncPushMirrorRequest) (*v1.SyncPushMirrorResponse, error) {
	repo, pm, err := s.pushMirror(ctx, req.Namespace, req.Name, req.Id)
	if err != nil {
		return nil, err
	}
	if s.mirrors == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "mirroring is unavailable")
	}
	if err := s.mirrors.SyncPushMirrorNow(repo, pm); err != nil {
		return nil, mapSyncErr(err)
	}
	return &v1.SyncPushMirrorResponse{}, nil
}

// ── Linked packages ──────────────────────────────────────────────────────

func gitLinkedByName(repo *storage.GitRepository, ns, name string) bool {
	return ns == repo.Namespace && (name == repo.Name || strings.HasPrefix(name, repo.Name+"/"))
}

func (s *GitService) linkedImage(repo *storage.GitRepository, img *storage.Repository) *v1.LinkedRepository {
	out := &v1.LinkedRepository{
		Kind:         v1.LinkedRepositoryKind_LINKED_REPOSITORY_KIND_IMAGE,
		Namespace:    img.Namespace,
		Name:         img.Name,
		FullName:     img.Namespace + "/" + img.Name,
		PullCount:    img.PullCount,
		IsPrivate:    img.IsPrivate,
		LinkedByName: gitLinkedByName(repo, img.Namespace, img.Name),
		UpdatedAt:    timestamppb.New(img.UpdatedAt),
	}
	return out
}

func (s *GitService) linkedArtifact(repo *storage.GitRepository, art *storage.ArtifactRepository, latest map[int64]string) *v1.LinkedRepository {
	return &v1.LinkedRepository{
		Kind:         v1.LinkedRepositoryKind_LINKED_REPOSITORY_KIND_ARTIFACT,
		Namespace:    art.Namespace,
		Name:         art.Name,
		FullName:     art.Namespace + "/" + art.Name,
		Latest:       latest[art.ID],
		IsPrivate:    art.IsPrivate,
		LinkedByName: art.Namespace == repo.Namespace && art.Name == repo.Name,
		UpdatedAt:    timestamppb.New(art.UpdatedAt),
	}
}

func (s *GitService) ListLinkedRepositories(ctx context.Context, req *v1.ListLinkedRepositoriesRequest) (*v1.ListLinkedRepositoriesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	images, err := s.store.ListRepositoriesByGitRepo(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	arts, err := s.store.ListArtifactRepositoriesByGitRepo(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	ids := make([]int64, 0, len(arts))
	for _, a := range arts {
		ids = append(ids, a.ID)
	}
	latest, _ := s.store.LatestArtifactVersions(ctx, ids)
	out := make([]*v1.LinkedRepository, 0, len(images)+len(arts))
	for _, img := range images {
		out = append(out, s.linkedImage(repo, img))
	}
	for _, a := range arts {
		out = append(out, s.linkedArtifact(repo, a, latest))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return &v1.ListLinkedRepositoriesResponse{Repositories: out}, nil
}

func (s *GitService) LinkRepository(ctx context.Context, req *v1.LinkRepositoryRequest) (*v1.LinkRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	if req.PackageName == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "package name is required")
	}
	switch req.Kind {
	case v1.LinkedRepositoryKind_LINKED_REPOSITORY_KIND_IMAGE:
		img, err := s.store.GetRepository(ctx, repo.Namespace, req.PackageName)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if img == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "image repository %s/%s not found", repo.Namespace, req.PackageName)
		}
		if img.GitRepoID != nil && *img.GitRepoID != repo.ID {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "package is linked to another repository")
		}
		if !s.manager.CanManageImage(ctx, user, img) {
			return nil, connect.Errorf(connect.CodePermissionDenied, "linking shares the package with this repository's collaborators, which needs manage rights on the package")
		}
		if err := s.store.SetRepositoryGitRepo(ctx, img.ID, &repo.ID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		img.GitRepoID = &repo.ID
		return &v1.LinkRepositoryResponse{Repository: s.linkedImage(repo, img)}, nil
	case v1.LinkedRepositoryKind_LINKED_REPOSITORY_KIND_ARTIFACT:
		art, err := s.store.GetArtifactRepository(ctx, repo.Namespace, req.PackageName)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if art == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "artifact repository %s/%s not found", repo.Namespace, req.PackageName)
		}
		if art.GitRepoID != nil && *art.GitRepoID != repo.ID {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "package is linked to another repository")
		}
		if !s.manager.CanManageArtifact(ctx, user, art) {
			return nil, connect.Errorf(connect.CodePermissionDenied, "linking shares the package with this repository's collaborators, which needs manage rights on the package")
		}
		if err := s.store.SetArtifactRepositoryGitRepo(ctx, art.ID, &repo.ID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		art.GitRepoID = &repo.ID
		latest, _ := s.store.LatestArtifactVersions(ctx, []int64{art.ID})
		return &v1.LinkRepositoryResponse{Repository: s.linkedArtifact(repo, art, latest)}, nil
	}
	return nil, connect.Errorf(connect.CodeInvalidArgument, "kind is required")
}

func (s *GitService) UnlinkRepository(ctx context.Context, req *v1.UnlinkRepositoryRequest) (*v1.UnlinkRepositoryResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	switch req.Kind {
	case v1.LinkedRepositoryKind_LINKED_REPOSITORY_KIND_IMAGE:
		img, err := s.store.GetRepository(ctx, repo.Namespace, req.PackageName)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if img == nil || img.GitRepoID == nil || *img.GitRepoID != repo.ID {
			return nil, connect.Errorf(connect.CodeNotFound, "package is not linked to this repository")
		}
		if err := s.store.SetRepositoryGitRepo(ctx, img.ID, nil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	case v1.LinkedRepositoryKind_LINKED_REPOSITORY_KIND_ARTIFACT:
		art, err := s.store.GetArtifactRepository(ctx, repo.Namespace, req.PackageName)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if art == nil || art.GitRepoID == nil || *art.GitRepoID != repo.ID {
			return nil, connect.Errorf(connect.CodeNotFound, "package is not linked to this repository")
		}
		if err := s.store.SetArtifactRepositoryGitRepo(ctx, art.ID, nil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	default:
		return nil, connect.Errorf(connect.CodeInvalidArgument, "kind is required")
	}
	return &v1.UnlinkRepositoryResponse{}, nil
}

// ── Collaborators ────────────────────────────────────────────────────────

func (s *GitService) ListCollaborators(ctx context.Context, req *v1.ListCollaboratorsRequest) (*v1.ListCollaboratorsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListGitCollaborators(ctx, repo.ID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	users := newGitUserCache(s.store)
	out := make([]*v1.GitCollaborator, 0, len(rows))
	for _, c := range rows {
		out = append(out, gitCollaboratorToProto(ctx, users, c))
	}
	return &v1.ListCollaboratorsResponse{Collaborators: out, Page: pages.Info(offset, limit, total)}, nil
}

func gitCollaboratorLevel(l v1.GitAccessLevel) (string, error) {
	switch l {
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_READ:
		return storage.GitLevelRead, nil
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_WRITE, v1.GitAccessLevel_GIT_ACCESS_LEVEL_UNSPECIFIED:
		return storage.GitLevelWrite, nil
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_ADMIN:
		return storage.GitLevelAdmin, nil
	}
	return "", connect.Errorf(connect.CodeInvalidArgument, "permission must be read, write, or admin")
}

func (s *GitService) collaboratorTarget(ctx context.Context, repo *storage.GitRepository, username string) (*storage.User, error) {
	if username == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "username is required")
	}
	u, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if u == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user not found")
	}
	if u.ID == repo.OwnerID || u.Username == repo.Namespace {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the owner is already a collaborator")
	}
	return u, nil
}

func (s *GitService) AddCollaborator(ctx context.Context, req *v1.AddCollaboratorRequest) (*v1.AddCollaboratorResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	target, err := s.collaboratorTarget(ctx, repo, req.Username)
	if err != nil {
		return nil, err
	}
	level, err := gitCollaboratorLevel(req.Permission)
	if err != nil {
		return nil, err
	}
	existing, _ := s.store.GetGitCollaborator(ctx, repo.ID, target.ID)
	c := &storage.GitCollaborator{RepoID: repo.ID, UserID: target.ID, Permission: level, Accepted: target.ID == user.ID}
	if existing != nil {
		c.Accepted = existing.Accepted
		c.CreatedAt = existing.CreatedAt
	}
	if err := s.store.UpsertGitCollaborator(ctx, c); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if existing == nil && s.forge != nil && !c.Accepted {
		full := repo.Namespace + "/" + repo.Name
		s.forge.Notify(ctx, forge.Notification{
			Repo: repo, Kind: forge.NotifyInvite, Actor: user, Recipients: []string{target.ID},
			Subject: "Invitation to collaborate on " + full,
			Text:    user.Username + " invited you to collaborate on " + full + " with " + level + " access. Accept it from your notifications.",
			URL:     s.manager.HTMLURL(ctx, repo),
		})
	}
	return &v1.AddCollaboratorResponse{Collaborator: gitCollaboratorToProto(ctx, newGitUserCache(s.store), c)}, nil
}

func (s *GitService) UpdateCollaborator(ctx context.Context, req *v1.UpdateCollaboratorRequest) (*v1.UpdateCollaboratorResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	target, err := s.collaboratorTarget(ctx, repo, req.Username)
	if err != nil {
		return nil, err
	}
	c, err := s.store.GetGitCollaborator(ctx, repo.ID, target.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if c == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "collaborator not found")
	}
	level, err := gitCollaboratorLevel(req.Permission)
	if err != nil {
		return nil, err
	}
	c.Permission = level
	if err := s.store.UpsertGitCollaborator(ctx, c); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UpdateCollaboratorResponse{Collaborator: gitCollaboratorToProto(ctx, newGitUserCache(s.store), c)}, nil
}

func (s *GitService) RemoveCollaborator(ctx context.Context, req *v1.RemoveCollaboratorRequest) (*v1.RemoveCollaboratorResponse, error) {
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	target, err := s.store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if target == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "user not found")
	}
	// Collaborators may leave on their own, removing others needs admin
	if target.ID != user.ID {
		if _, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin); err != nil {
			return nil, err
		}
	}
	if err := s.store.DeleteGitCollaborator(ctx, repo.ID, target.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.RemoveCollaboratorResponse{}, nil
}

func (s *GitService) AcceptCollaboration(ctx context.Context, req *v1.AcceptCollaborationRequest) (*v1.AcceptCollaborationResponse, error) {
	user, err := s.signedIn(ctx)
	if err != nil {
		return nil, err
	}
	ns, name := gitRepoRef(ctx, user, req.Namespace, req.Name)
	repo, err := s.store.GetGitRepository(ctx, ns, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	c, err := s.store.GetGitCollaborator(ctx, repo.ID, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if c == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "no pending invitation")
	}
	if !c.Accepted {
		c.Accepted = true
		if err := s.store.UpsertGitCollaborator(ctx, c); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}
	return &v1.AcceptCollaborationResponse{}, nil
}

// ── Insights ─────────────────────────────────────────────────────────────

const gitActivityBudget = 5000

func (s *GitService) activity(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) ([]gitrepo.Contributor, []gitrepo.WeekActivity, error) {
	repo, err := s.repo(ctx, user, namespace, name)
	if err != nil {
		return nil, nil, err
	}
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, nil, mapGitErr(err)
	}
	defer r.Close()
	head := r.BranchHash(repo.DefaultBranch)
	if head.IsZero() {
		return nil, nil, nil
	}
	res, err := s.insights.Get(ctx, repo.ID+"@"+head.String(), func() (repoInsights, error) {
		wctx, cancel := insightContext(ctx)
		defer cancel()
		contributors, weeks, err := r.Activity(wctx, head, time.Now().AddDate(-1, 0, 0), gitActivityBudget)
		return repoInsights{contributors: contributors, weeks: weeks}, err
	})
	if err != nil {
		return nil, nil, mapGitErr(err)
	}
	return res.contributors, res.weeks, nil
}

// Walks outlive the request that started them so waiters still get a result
func insightContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
}

func (s *GitService) GetRepositoryActivity(ctx context.Context, req *v1.GetRepositoryActivityRequest) (*v1.GetRepositoryActivityResponse, error) {
	_, weeks, err := s.activity(ctx, auth.UserFromContext(ctx), req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.GitActivityWeek, 0, len(weeks))
	for _, w := range weeks {
		out = append(out, &v1.GitActivityWeek{Week: timestamppb.New(w.Week), Commits: w.Commits, Additions: w.Additions, Deletions: w.Deletions})
	}
	return &v1.GetRepositoryActivityResponse{Weeks: out}, nil
}

func (s *GitService) ListContributors(ctx context.Context, req *v1.ListContributorsRequest) (*v1.ListContributorsResponse, error) {
	contributors, _, err := s.activity(ctx, auth.UserFromContext(ctx), req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	total := int64(len(contributors))
	if offset > len(contributors) {
		offset = len(contributors)
	}
	end := min(offset+limit, len(contributors))
	users := newGitUserCache(s.store)
	out := make([]*v1.GitContributor, 0, end-offset)
	for _, c := range contributors[offset:end] {
		p := &v1.GitContributor{Username: users.usernameByEmail(ctx, c.Email), DisplayName: c.Name, Email: c.Email, Commits: c.Commits, Additions: c.Additions, Deletions: c.Deletions}
		out = append(out, p)
	}
	return &v1.ListContributorsResponse{Contributors: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *GitService) GetRepositoryLanguages(ctx context.Context, req *v1.GetRepositoryLanguagesRequest) (*v1.GetRepositoryLanguagesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, mapGitErr(err)
	}
	defer r.Close()
	head := r.BranchHash(repo.DefaultBranch)
	if head.IsZero() {
		return &v1.GetRepositoryLanguagesResponse{}, nil
	}
	langs, err := s.languages.Get(ctx, repo.ID+"@"+head.String(), func() (map[string]int64, error) {
		wctx, cancel := insightContext(ctx)
		defer cancel()
		return r.Languages(wctx, head)
	})
	if err != nil {
		return nil, mapGitErr(err)
	}
	var total int64
	for _, b := range langs {
		total += b
	}
	out := make([]*v1.GitLanguage, 0, len(langs))
	for name, b := range langs {
		l := &v1.GitLanguage{Name: name, Bytes: b, Color: gitLanguageColor(name)}
		if total > 0 {
			l.Percent = float32(float64(b) * 100 / float64(total))
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return &v1.GetRepositoryLanguagesResponse{Languages: out}, nil
}

func (s *GitService) RunMaintenance(ctx context.Context, req *v1.RunMaintenanceRequest) (*v1.RunMaintenanceResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repoForWrite(ctx, user, req.Namespace, req.Name, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	size, err := s.manager.Maintain(ctx, repo)
	if err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.RunMaintenanceResponse{SizeBytes: size}, nil
}

// Creates the linked release package and image mirrors for a mirror repository
func (s *GitService) setupCompositeMirror(ctx context.Context, repo *storage.GitRepository, releases bool, images []string) error {
	return s.mirrors.SetupCompositeMirror(ctx, repo, releases, images)
}
