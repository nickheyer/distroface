package services

import (
	"context"
	"sort"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

var _ distrofacev1connect.GitRefServiceHandler = (*GitRefService)(nil)

// Ahead and behind walks stop counting past this many commits
const gitAheadBehindLimit = 1000

type GitRefService struct {
	store   *stores.Store
	manager *gitrepo.Manager
	log     *logger.Logger
}

func NewGitRefService(store *stores.Store, manager *gitrepo.Manager, log *logger.Logger) *GitRefService {
	return &GitRefService{store: store, manager: manager, log: log}
}

func (s *GitRefService) repo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
	return visibleGitRepo(ctx, s.store, s.manager.Access(), user, namespace, name)
}

// Loads a repository for a branch or tag write
func (s *GitRefService) writable(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, err
	}
	return gitRepoWithLevel(ctx, s.store, s.manager.Access(), user, namespace, name, storage.GitUnitCode, gitrepo.LevelWrite)
}

// Loads a repository for a protection write
func (s *GitRefService) admin(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
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
	lv := s.manager.Access().Levels(ctx, user, repo)
	if !lv.IsOwner && !lv.CanAdmin {
		return nil, connect.Errorf(connect.CodePermissionDenied, "admin access required")
	}
	return repo, nil
}

func (s *GitRefService) open(repo *storage.GitRepository) (*gitrepo.Repo, error) {
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, mapGitErr(err)
	}
	return r, nil
}

// Validates a branch or tag short name
func gitValidRefName(name string) bool {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") || strings.Contains(name, "..") || strings.Contains(name, "//") ||
		strings.Contains(name, "@{") || name == "@" {
		return false
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(" ~^:?*[\\", c) {
			return false
		}
	}
	return plumbing.ReferenceName("refs/heads/"+name).Validate() == nil
}

// ── Branches ─────────────────────────────────────────────────────────────

func (s *GitRefService) branchProto(ctx context.Context, repo *storage.GitRepository, r *gitrepo.Repo, enrich *gitCommitEnricher, protections []*storage.GitBranchProtection, name string, sha, defaultSHA plumbing.Hash) *v1.GitBranch {
	out := &v1.GitBranch{
		Name:        name,
		Sha:         sha.String(),
		IsDefault:   name == repo.DefaultBranch,
		IsProtected: gitrepo.MatchProtection(protections, name) != nil,
	}
	if c, err := r.Commit(sha); err == nil {
		out.Commit = enrich.commit(c, false, false)
	}
	if !defaultSHA.IsZero() && sha != defaultSHA {
		out.Ahead, out.Behind, _ = r.AheadBehind(ctx, sha, defaultSHA, gitAheadBehindLimit)
	}
	return out
}

func (s *GitRefService) ListBranches(ctx context.Context, req *v1.ListBranchesRequest) (*v1.ListBranchesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	branches, err := r.Branches()
	if err != nil {
		return nil, mapGitErr(err)
	}
	names := gitSortedBranchNames(branches)
	if q := strings.ToLower(pages.ParseQuery(req.Page).Text); q != "" {
		filtered := names[:0]
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), q) {
				filtered = append(filtered, n)
			}
		}
		names = filtered
	}
	// Default branch sorts first, then by recency when asked
	defaultSHA := branches[repo.DefaultBranch]
	when := map[string]int64{}
	if strings.HasPrefix(pages.OrderBy(req.Page, map[string]bool{"name": true, "updated": true}, "name ASC"), "updated") {
		for _, n := range names {
			if c, err := r.Commit(branches[n]); err == nil {
				when[n] = c.Committer.When.Unix()
			}
		}
		sort.SliceStable(names, func(i, j int) bool { return when[names[i]] > when[names[j]] })
	}
	sort.SliceStable(names, func(i, j int) bool { return names[i] == repo.DefaultBranch && names[j] != repo.DefaultBranch })
	limit, offset := pages.Parse(req.Page)
	total := int64(len(names))
	if offset > len(names) {
		offset = len(names)
	}
	end := min(offset+limit, len(names))
	protections, _ := s.store.ListGitBranchProtections(ctx, repo.ID)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	out := make([]*v1.GitBranch, 0, end-offset)
	for _, n := range names[offset:end] {
		out = append(out, s.branchProto(ctx, repo, r, enrich, protections, n, branches[n], defaultSHA))
	}
	return &v1.ListBranchesResponse{Branches: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *GitRefService) GetBranch(ctx context.Context, req *v1.GetBranchRequest) (*v1.GetBranchResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	sha := r.BranchHash(req.Branch)
	if sha.IsZero() {
		return nil, connect.Errorf(connect.CodeNotFound, "branch %q not found", req.Branch)
	}
	protections, _ := s.store.ListGitBranchProtections(ctx, repo.ID)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	enrich.loadStatuses([]*object.Commit{{Hash: sha}})
	return &v1.GetBranchResponse{Branch: s.branchProto(ctx, repo, r, enrich, protections, req.Branch, sha, r.BranchHash(repo.DefaultBranch))}, nil
}

func (s *GitRefService) CreateBranch(ctx context.Context, req *v1.CreateBranchRequest) (*v1.CreateBranchResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.writable(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if !gitValidRefName(req.Branch) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid branch name")
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	from := req.From
	if from == "" {
		from = repo.DefaultBranch
	}
	target, err := r.Resolve(from)
	if err != nil {
		r.Close()
		return nil, connect.Errorf(connect.CodeNotFound, "ref %q not found", from)
	}
	if !r.BranchHash(req.Branch).IsZero() {
		r.Close()
		return nil, connect.Errorf(connect.CodeAlreadyExists, "branch %q already exists", req.Branch)
	}
	r.Close()
	if err := s.manager.UpdateRef(ctx, repo, user, plumbing.NewBranchReferenceName(req.Branch), plumbing.ZeroHash, target, gitrepo.HookOptions{}); err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.CreateBranchResponse{Branch: s.branchAfterWrite(ctx, repo, req.Branch)}, nil
}

// Rebuilds a branch view after a ref write
func (s *GitRefService) branchAfterWrite(ctx context.Context, repo *storage.GitRepository, name string) *v1.GitBranch {
	r, err := s.open(repo)
	if err != nil {
		return &v1.GitBranch{Name: name}
	}
	defer r.Close()
	protections, _ := s.store.ListGitBranchProtections(ctx, repo.ID)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	return s.branchProto(ctx, repo, r, enrich, protections, name, r.BranchHash(name), r.BranchHash(repo.DefaultBranch))
}

func (s *GitRefService) DeleteBranch(ctx context.Context, req *v1.DeleteBranchRequest) (*v1.DeleteBranchResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.writable(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if req.Branch == repo.DefaultBranch {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "the default branch cannot be deleted")
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	old := r.BranchHash(req.Branch)
	r.Close()
	if old.IsZero() {
		return nil, connect.Errorf(connect.CodeNotFound, "branch %q not found", req.Branch)
	}
	if err := s.manager.UpdateRef(ctx, repo, user, plumbing.NewBranchReferenceName(req.Branch), old, plumbing.ZeroHash, gitrepo.HookOptions{}); err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.DeleteBranchResponse{}, nil
}

func (s *GitRefService) RenameBranch(ctx context.Context, req *v1.RenameBranchRequest) (*v1.RenameBranchResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.writable(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if !gitValidRefName(req.NewName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid branch name")
	}
	if req.NewName == req.Branch {
		return &v1.RenameBranchResponse{Branch: s.branchAfterWrite(ctx, repo, req.Branch)}, nil
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	old := r.BranchHash(req.Branch)
	exists := !r.BranchHash(req.NewName).IsZero()
	r.Close()
	if old.IsZero() {
		return nil, connect.Errorf(connect.CodeNotFound, "branch %q not found", req.Branch)
	}
	if exists {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "branch %q already exists", req.NewName)
	}
	protections, err := s.store.ListGitBranchProtections(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	// The default branch and protected names move only under an admin, rules travel along
	isDefault := req.Branch == repo.DefaultBranch
	guarded := isDefault || gitrepo.MatchProtection(protections, req.Branch) != nil || gitrepo.MatchProtection(protections, req.NewName) != nil
	opts := gitrepo.HookOptions{}
	if guarded {
		if _, err := s.admin(ctx, user, req.Namespace, req.Name); err != nil {
			return nil, err
		}
		opts = gitrepo.HookOptions{BypassPullRequest: true, BypassSignedCommits: true, BypassAllowedPushers: true}
	}
	if err := s.manager.UpdateRef(ctx, repo, user, plumbing.NewBranchReferenceName(req.NewName), plumbing.ZeroHash, old, opts); err != nil {
		return nil, mapGitErr(err)
	}
	if isDefault {
		if err := s.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"default_branch": req.NewName}); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		repo.DefaultBranch = req.NewName
		if r, err := s.open(repo); err == nil {
			_ = r.SetHead(req.NewName)
			r.Close()
		}
	}
	for _, rule := range protections {
		if rule.Pattern == req.Branch {
			rule.Pattern = req.NewName
			_ = s.store.SaveGitBranchProtection(ctx, rule)
		}
	}
	// Pull requests follow before the old name goes, else they read as head deleted
	for _, pr := range s.openPullsOnBranch(ctx, repo, req.Branch) {
		_ = s.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_branch": req.NewName})
	}
	for _, pr := range s.openPullsOnBase(ctx, repo, req.Branch) {
		_ = s.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"base_branch": req.NewName})
	}
	opts.Rename = true
	if err := s.manager.UpdateRef(ctx, repo, user, plumbing.NewBranchReferenceName(req.Branch), old, plumbing.ZeroHash, opts); err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.RenameBranchResponse{Branch: s.branchAfterWrite(ctx, repo, req.NewName)}, nil
}

func (s *GitRefService) openPullsOnBranch(ctx context.Context, repo *storage.GitRepository, branch string) []*storage.GitPullRequest {
	prs, _ := s.store.ListOpenPullsByHead(ctx, repo.ID, branch)
	return prs
}

func (s *GitRefService) openPullsOnBase(ctx context.Context, repo *storage.GitRepository, branch string) []*storage.GitPullRequest {
	prs, _ := s.store.ListOpenPullsByBase(ctx, repo.ID, branch)
	return prs
}

// ── Tags ─────────────────────────────────────────────────────────────────

func (s *GitRefService) tagProto(ctx context.Context, repo *storage.GitRepository, r *gitrepo.Repo, enrich *gitCommitEnricher, releases map[string]bool, name string, h plumbing.Hash) *v1.GitTag {
	out := &v1.GitTag{Name: name, HasRelease: releases[name]}
	commitHash := h
	if tag := r.TagObject(h); tag != nil {
		out.TagSha = h.String()
		out.Message = tag.Message
		out.Tagger = gitrepo.SignatureToProto(tag.Tagger)
		out.Tagger.Username = enrich.users.usernameByEmail(ctx, tag.Tagger.Email)
		out.Verification = s.manager.Signing().VerifyTag(ctx, tag).Proto()
		if c, err := tag.Commit(); err == nil {
			commitHash = c.Hash
		}
	}
	out.Sha = commitHash.String()
	if c, err := r.Commit(commitHash); err == nil {
		out.Commit = enrich.commit(c, false, false)
	}
	return out
}

func (s *GitRefService) ListGitTags(ctx context.Context, req *v1.ListGitTagsRequest) (*v1.ListGitTagsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	tags, err := r.Tags()
	if err != nil {
		return nil, mapGitErr(err)
	}
	names := gitSortedTagNames(tags)
	if q := strings.ToLower(pages.ParseQuery(req.Page).Text); q != "" {
		filtered := names[:0]
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), q) {
				filtered = append(filtered, n)
			}
		}
		names = filtered
	}
	limit, offset := pages.Parse(req.Page)
	total := int64(len(names))
	if offset > len(names) {
		offset = len(names)
	}
	end := min(offset+limit, len(names))
	releases, _ := s.store.GitReleaseTags(ctx, repo.ID)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	out := make([]*v1.GitTag, 0, end-offset)
	for _, n := range names[offset:end] {
		out = append(out, s.tagProto(ctx, repo, r, enrich, releases, n, tags[n]))
	}
	return &v1.ListGitTagsResponse{Tags: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *GitRefService) GetTag(ctx context.Context, req *v1.GetTagRequest) (*v1.GetTagResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	h := r.TagHash(req.Tag)
	if h.IsZero() {
		return nil, connect.Errorf(connect.CodeNotFound, "tag %q not found", req.Tag)
	}
	releases, _ := s.store.GitReleaseTags(ctx, repo.ID)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	return &v1.GetTagResponse{Tag: s.tagProto(ctx, repo, r, enrich, releases, req.Tag, h)}, nil
}

func (s *GitRefService) CreateTag(ctx context.Context, req *v1.CreateTagRequest) (*v1.CreateTagResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.writable(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if !gitValidRefName(req.Tag) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid tag name")
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	target := req.Target
	if target == "" {
		target = repo.DefaultBranch
	}
	commit, err := r.Resolve(target)
	if err != nil {
		r.Close()
		return nil, connect.Errorf(connect.CodeNotFound, "ref %q not found", target)
	}
	if !r.TagHash(req.Tag).IsZero() {
		r.Close()
		return nil, connect.Errorf(connect.CodeAlreadyExists, "tag %q already exists", req.Tag)
	}
	ref := commit
	if strings.TrimSpace(req.Message) != "" {
		ref, err = r.WriteTag(ctx, req.Tag, commit, plumbing.CommitObject, s.manager.Signature(ctx, user), req.Message, s.manager.Signer(ctx))
		if err != nil {
			r.Close()
			return nil, mapGitErr(err)
		}
	}
	r.Close()
	if err := s.manager.UpdateRef(ctx, repo, user, plumbing.NewTagReferenceName(req.Tag), plumbing.ZeroHash, ref, gitrepo.HookOptions{}); err != nil {
		return nil, mapGitErr(err)
	}
	r, err = s.open(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	releases, _ := s.store.GitReleaseTags(ctx, repo.ID)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	return &v1.CreateTagResponse{Tag: s.tagProto(ctx, repo, r, enrich, releases, req.Tag, ref)}, nil
}

func (s *GitRefService) DeleteTag(ctx context.Context, req *v1.DeleteTagRequest) (*v1.DeleteTagResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.writable(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	old := r.TagHash(req.Tag)
	r.Close()
	if old.IsZero() {
		return nil, connect.Errorf(connect.CodeNotFound, "tag %q not found", req.Tag)
	}
	if err := s.manager.UpdateRef(ctx, repo, user, plumbing.NewTagReferenceName(req.Tag), old, plumbing.ZeroHash, gitrepo.HookOptions{}); err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.DeleteTagResponse{}, nil
}

// ── Protections ──────────────────────────────────────────────────────────

func gitValidProtectionPattern(p string) bool {
	return p != "" && len(p) <= 255 && !strings.ContainsAny(p, " \t\n")
}

func (s *GitRefService) SetBranchProtection(ctx context.Context, req *v1.SetBranchProtectionRequest) (*v1.SetBranchProtectionResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.admin(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	in := req.Protection
	if in == nil || !gitValidProtectionPattern(in.Pattern) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a branch pattern is required")
	}
	p := &storage.GitBranchProtection{ID: in.Id, RepoID: repo.ID}
	if p.ID != "" {
		existing, err := s.store.GetGitBranchProtection(ctx, repo.ID, p.ID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if existing == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "protection not found")
		}
		p.CreatedAt = existing.CreatedAt
	} else {
		p.ID = uuid.New().String()
	}
	p.Pattern = in.Pattern
	p.Priority = int(in.Priority)
	p.BlockForcePush = in.BlockForcePush
	p.BlockDeletion = in.BlockDeletion
	p.RequirePullRequest = in.RequirePullRequest
	p.RequiredApprovals = int(max(in.RequiredApprovals, 0))
	p.DismissStaleApprovals = in.DismissStaleApprovals
	p.RequireCodeOwnerReview = in.RequireCodeOwnerReview
	p.RequireStatusChecks = in.RequireStatusChecks
	p.StatusCheckContexts = gitrepo.EncodeIDs(in.StatusCheckContexts)
	p.RequireSignedCommits = in.RequireSignedCommits
	p.RequireLinearHistory = in.RequireLinearHistory
	p.RequireUpToDate = in.RequireUpToDate
	p.AllowedPushers = gitrepo.EncodeIDs(in.AllowedPushers)
	p.AllowedMergers = gitrepo.EncodeIDs(in.AllowedMergers)
	p.BypassIDs = gitrepo.EncodeIDs(in.BypassIds)
	if err := s.store.SaveGitBranchProtection(ctx, p); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.SetBranchProtectionResponse{Protection: gitBranchProtectionToProto(p)}, nil
}

func (s *GitRefService) ListBranchProtections(ctx context.Context, req *v1.ListBranchProtectionsRequest) (*v1.ListBranchProtectionsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListGitBranchProtections(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.GitBranchProtection, 0, len(rows))
	for _, p := range rows {
		out = append(out, gitBranchProtectionToProto(p))
	}
	return &v1.ListBranchProtectionsResponse{Protections: out}, nil
}

func (s *GitRefService) DeleteBranchProtection(ctx context.Context, req *v1.DeleteBranchProtectionRequest) (*v1.DeleteBranchProtectionResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.admin(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteGitBranchProtection(ctx, repo.ID, req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteBranchProtectionResponse{}, nil
}

func (s *GitRefService) SetTagProtection(ctx context.Context, req *v1.SetTagProtectionRequest) (*v1.SetTagProtectionResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.admin(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	in := req.Protection
	if in == nil || !gitValidProtectionPattern(in.Pattern) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a tag pattern is required")
	}
	p := &storage.GitTagProtection{ID: in.Id, RepoID: repo.ID, Pattern: in.Pattern, BlockDeletion: in.BlockDeletion, AllowedPushers: gitrepo.EncodeIDs(in.AllowedPushers)}
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if err := s.store.SaveGitTagProtection(ctx, p); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.SetTagProtectionResponse{Protection: gitTagProtectionToProto(p)}, nil
}

func (s *GitRefService) ListTagProtections(ctx context.Context, req *v1.ListTagProtectionsRequest) (*v1.ListTagProtectionsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListGitTagProtections(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.GitTagProtection, 0, len(rows))
	for _, p := range rows {
		out = append(out, gitTagProtectionToProto(p))
	}
	return &v1.ListTagProtectionsResponse{Protections: out}, nil
}

func (s *GitRefService) DeleteTagProtection(ctx context.Context, req *v1.DeleteTagProtectionRequest) (*v1.DeleteTagProtectionResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.admin(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteGitTagProtection(ctx, repo.ID, req.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteTagProtectionResponse{}, nil
}
