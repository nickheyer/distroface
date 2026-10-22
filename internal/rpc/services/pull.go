package services

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.PullRequestServiceHandler = (*PullRequestService)(nil)

type PullRequestService struct {
	store *stores.Store
	forge *forge.Manager
	log   *logger.Logger
}

func NewPullRequestService(store *stores.Store, fm *forge.Manager, log *logger.Logger) *PullRequestService {
	return &PullRequestService{store: store, forge: fm, log: log}
}

// Loads a visible repository with the pulls unit readable and the caller's actor
func (s *PullRequestService) repo(ctx context.Context, namespace, name string) (*auth.AuthenticatedUser, *storage.GitRepository, forge.Actor, error) {
	user := auth.UserFromContext(ctx)
	repo, err := namedGitRepo(ctx, s.store, user, namespace, name)
	if err != nil {
		return nil, nil, forge.Actor{}, err
	}
	// One identity pass answers visibility and the unit level
	levels := s.forge.Git().Access().Levels(ctx, user, repo)
	if !levelsCanSee(repo, levels) {
		return nil, nil, forge.Actor{}, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	if !gitrepo.UnitEnabled(repo, storage.GitUnitPulls) && !levels.IsOwner && !levels.CanAdmin {
		return nil, nil, forge.Actor{}, connect.Errorf(connect.CodeFailedPrecondition, "pull requests are disabled on this repository")
	}
	level := levels.Unit(storage.GitUnitPulls)
	if level < gitrepo.LevelRead {
		return nil, nil, forge.Actor{}, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	return user, repo, forge.Actor{User: user, CanWrite: level >= gitrepo.LevelWrite, CanAdmin: levels.CanAdmin || levels.IsOwner}, nil
}

// Loads a pull request with its issue and the caller's actor
func (s *PullRequestService) pull(ctx context.Context, namespace, name string, number int64) (*auth.AuthenticatedUser, *storage.GitRepository, *storage.GitIssue, *storage.GitPullRequest, forge.Actor, error) {
	user, repo, actor, err := s.repo(ctx, namespace, name)
	if err != nil {
		return nil, nil, nil, nil, forge.Actor{}, err
	}
	issue, err := s.forge.Issue(ctx, repo, number)
	if err != nil {
		return nil, nil, nil, nil, forge.Actor{}, mapForgeErr(err)
	}
	pr, err := s.forge.Pull(ctx, issue)
	if err != nil {
		return nil, nil, nil, nil, forge.Actor{}, mapForgeErr(err)
	}
	return user, repo, issue, pr, actor, nil
}

func prMergeableProto(state string) v1.MergeableState {
	switch state {
	case forge.MergeableClean:
		return v1.MergeableState_MERGEABLE_STATE_CLEAN
	case forge.MergeableConflict:
		return v1.MergeableState_MERGEABLE_STATE_CONFLICT
	case forge.MergeableBlocked:
		return v1.MergeableState_MERGEABLE_STATE_BLOCKED
	case forge.MergeableChecking:
		return v1.MergeableState_MERGEABLE_STATE_CHECKING
	}
	return v1.MergeableState_MERGEABLE_STATE_UNKNOWN
}

func prReviewProto(ctx context.Context, users *gitUserCache, rv *storage.GitReview, counts map[string]int32) *v1.Review {
	out := &v1.Review{
		Id: rv.ID, Reviewer: issueUserProto(users.user(ctx, rv.ReviewerID)), State: forge.ReviewStateProto(rv.State), Body: rv.Body,
		CommitSha: rv.CommitSHA, Dismissed: rv.Dismissed, DismissedBy: users.username(ctx, rv.DismissedBy), CommentCount: counts[rv.ID],
		CreatedAt: timestamppb.New(rv.CreatedAt),
	}
	if rv.SubmittedAt != nil {
		out.SubmittedAt = timestamppb.New(*rv.SubmittedAt)
	}
	return out
}

func prReviewCommentProto(ctx context.Context, users *gitUserCache, c *storage.GitReviewComment, reactions []*storage.GitReaction, viewerID string) *v1.ReviewComment {
	out := &v1.ReviewComment{
		Id: c.ID, Author: issueUserProto(users.user(ctx, c.AuthorID)), Path: c.Path, Line: int32(c.Line), Side: c.Side, StartLine: int32(c.StartLine),
		DiffHunk: c.DiffHunk, Body: c.Body, Resolved: c.Resolved, ResolvedBy: users.username(ctx, c.ResolvedBy), Outdated: c.OutdatedSHA != "",
		CommitSha: c.CommitSHA, ThreadId: c.ThreadID, Reactions: issueReactionGroups(ctx, users, reactions, viewerID), Pending: c.Pending,
		CreatedAt: timestamppb.New(c.CreatedAt), UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
	if c.ReviewID != nil {
		out.ReviewId = *c.ReviewID
	}
	return out
}

// User ids a pull view names beyond its issue rows
func pullUserIDs(view *forge.PullView) []string {
	ids := []string{view.PR.AutoMergeBy, view.PR.MergedBy, view.Issue.AuthorID, view.Issue.ClosedBy}
	for _, rq := range view.Requests {
		ids = append(ids, rq.UserID)
	}
	for _, rv := range view.Reviews {
		ids = append(ids, rv.ReviewerID, rv.DismissedBy)
	}
	for _, r := range view.Reactions {
		ids = append(ids, r.UserID)
	}
	return ids
}

// Names of every team asked to review, one query
func (s *PullRequestService) teamNames(ctx context.Context, views []*forge.PullView) map[string]string {
	seen := map[string]bool{}
	var ids []string
	for _, v := range views {
		for _, rq := range v.Requests {
			if rq.TeamID != "" && !seen[rq.TeamID] {
				seen[rq.TeamID] = true
				ids = append(ids, rq.TeamID)
			}
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	teams, _ := s.store.GetOrgTeamsByIDs(ctx, ids)
	for _, t := range teams {
		out[t.ID] = t.Name
	}
	return out
}

// Converts a pull request view with shared lookups
func (s *PullRequestService) toProto(ctx context.Context, users *gitUserCache, teams map[string]string, view *forge.PullView, viewerID string) *v1.PullRequest {
	pr := view.PR
	out := &v1.PullRequest{
		Issue:                issueViewProto(ctx, s.forge, users, viewerID, view.IssueView),
		HeadBranch:           pr.HeadBranch,
		HeadSha:              pr.HeadSHA,
		BaseBranch:           pr.BaseBranch,
		MergeBaseSha:         pr.MergeBaseSHA,
		Draft:                pr.Draft,
		MergeableState:       prMergeableProto(pr.MergeableState),
		ChangedFiles:         int32(pr.ChangedFiles),
		Additions:            int32(pr.Additions),
		Deletions:            int32(pr.Deletions),
		CommitCount:          int32(pr.CommitCount),
		AllowMaintainerEdits: pr.AllowMaintainerEdits,
		AutoMergeMethod:      forge.MergeMethodProto(pr.AutoMergeMethod),
		AutoMergeBy:          users.username(ctx, pr.AutoMergeBy),
		MergeCommitSha:       pr.MergeCommitSHA,
		MergeMethod:          forge.MergeMethodProto(pr.MergeMethod),
		HeadDeleted:          pr.HeadDeleted,
		Status:               gitCombinedState(view.Statuses),
		DefaultMethod:        forge.MergeMethodProto(view.Default),
		HeadRef:              forge.PullRef(view.Issue.Number).String(),
	}
	out.ConflictPaths = decodeStringList(pr.ConflictPaths)
	if view.HeadRepo != nil {
		out.HeadRepoFullName = view.HeadRepo.Namespace + "/" + view.HeadRepo.Name
	}
	if pr.MergedAt != nil {
		out.MergedAt = timestamppb.New(*pr.MergedAt)
		out.MergedBy = issueUserProto(users.user(ctx, pr.MergedBy))
	}
	for _, m := range view.Allowed {
		out.AllowedMethods = append(out.AllowedMethods, forge.MergeMethodProto(m))
	}
	for _, rq := range view.Requests {
		p := &v1.ReviewRequest{Fulfilled: rq.Fulfilled, TeamId: rq.TeamID}
		if rq.UserID != "" {
			p.User = issueUserProto(users.user(ctx, rq.UserID))
		}
		if rq.TeamID != "" {
			p.TeamName = teams[rq.TeamID]
		}
		out.ReviewRequests = append(out.ReviewRequests, p)
	}
	for _, rv := range view.Reviews {
		out.Reviews = append(out.Reviews, prReviewProto(ctx, users, rv, view.ReviewCount))
	}
	if view.Merge != nil {
		out.MergeableState = prMergeableProto(view.Merge.State)
		out.ConflictPaths = view.Merge.Conflicts
		out.CanMerge = view.Merge.CanMerge
		for _, b := range view.Merge.Blockers {
			out.Blockers = append(out.Blockers, &v1.MergeBlocker{Rule: b.Rule, Message: b.Message})
		}
	}
	return out
}

func (s *PullRequestService) view(ctx context.Context, repo *storage.GitRepository, actor forge.Actor, issue *storage.GitIssue, pr *storage.GitPullRequest, withMerge bool) (*v1.PullRequest, error) {
	view, err := s.forge.PullView(ctx, actor, repo, issue, pr, withMerge)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	users := newGitUserCache(s.store)
	users.prime(ctx, pullUserIDs(view))
	return s.toProto(ctx, users, s.teamNames(ctx, []*forge.PullView{view}), view, viewerID(actor.User)), nil
}

// ── Lifecycle ────────────────────────────────────────────────────────────

func (s *PullRequestService) CreatePullRequest(ctx context.Context, req *v1.CreatePullRequestRequest) (*v1.CreatePullRequestResponse, error) {
	user, repo, actor, err := s.repo(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	issue, pr, err := s.forge.CreatePull(ctx, repo, actor, forge.NewPull{
		Base: req.Base, Head: req.Head, Title: req.Title, Body: req.Body, Draft: req.Draft, LabelIDs: req.LabelIds, MilestoneID: req.MilestoneId,
		AssigneeIDs: req.AssigneeIds, ReviewerIDs: req.ReviewerIds, TeamReviewerIDs: req.TeamReviewerIds, AllowMaintainerEdits: req.AllowMaintainerEdits,
	})
	if err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, true)
	if err != nil {
		return nil, err
	}
	return &v1.CreatePullRequestResponse{PullRequest: out}, nil
}

func (s *PullRequestService) GetPullRequest(ctx context.Context, req *v1.GetPullRequestRequest) (*v1.GetPullRequestResponse, error) {
	_, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	out, err := s.view(ctx, repo, actor, issue, pr, true)
	if err != nil {
		return nil, err
	}
	return &v1.GetPullRequestResponse{PullRequest: out}, nil
}

func (s *PullRequestService) ListPullRequests(ctx context.Context, req *v1.ListPullRequestsRequest) (*v1.ListPullRequestsResponse, error) {
	user, repo, actor, err := s.repo(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	state := issueStateString(req.State)
	if req.State == v1.IssueState_ISSUE_STATE_UNSPECIFIED {
		state = "open"
	}
	isPull := true
	opts := stores.IssueListOptions{
		RepoID: repo.ID, State: state, Labels: req.Labels, Milestone: req.Milestone, Assignee: req.Assignee, Author: req.Author,
		Reviewer: req.Reviewer, Base: req.Base, Head: req.Head, Draft: req.Draft, IsPull: &isPull, Query: pages.ParseQuery(req.Page),
		OrderBy: issueOrder(req.Page), Limit: limit, Offset: offset,
	}
	issues, total, err := s.forge.ListIssues(ctx, opts)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	countOpts := opts
	countOpts.State = ""
	open, closed, _ := s.store.CountGitIssuesByState(ctx, countOpts)
	out := &v1.ListPullRequestsResponse{Page: pages.Info(offset, limit, total), OpenCount: open, ClosedCount: closed}
	views, err := s.forge.PullViews(ctx, actor, repo, issues)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	users := newGitUserCache(s.store)
	var ids []string
	for _, view := range views {
		ids = append(ids, pullUserIDs(view)...)
	}
	users.prime(ctx, ids)
	teams := s.teamNames(ctx, views)
	for _, view := range views {
		out.PullRequests = append(out.PullRequests, s.toProto(ctx, users, teams, view, viewerID(user)))
	}
	return out, nil
}

func (s *PullRequestService) UpdatePullRequest(ctx context.Context, req *v1.UpdatePullRequestRequest) (*v1.UpdatePullRequestResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if req.Title != nil || req.Body != nil {
		if err := s.forge.UpdateIssue(ctx, repo, actor, issue, forge.IssueUpdate{Title: req.Title, Body: req.Body}); err != nil {
			return nil, mapForgeErr(err)
		}
	}
	if err := s.forge.UpdatePull(ctx, repo, actor, issue, pr, forge.PullUpdate{Base: req.Base, Draft: req.Draft, AllowMaintainerEdits: req.AllowMaintainerEdits}); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, true)
	if err != nil {
		return nil, err
	}
	return &v1.UpdatePullRequestResponse{PullRequest: out}, nil
}

// ── Diff and commits ─────────────────────────────────────────────────────

// Commit range a diff request covers, the whole pull request unless a commit range is given
func prDiffRange(pr *storage.GitPullRequest, startSha, endSha string) (plumbing.Hash, plumbing.Hash) {
	base := plumbing.ZeroHash
	if pr.MergeBaseSHA != "" {
		base = plumbing.NewHash(pr.MergeBaseSHA)
	}
	head := plumbing.NewHash(pr.HeadSHA)
	if startSha != "" {
		base = plumbing.NewHash(startSha)
	}
	if endSha != "" {
		head = plumbing.NewHash(endSha)
	}
	return base, head
}

func (s *PullRequestService) GetPullRequestDiff(ctx context.Context, req *v1.GetPullRequestDiffRequest) (*v1.GetPullRequestDiffResponse, error) {
	_, repo, _, pr, _, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	r, err := s.forge.Git().Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, mapGitErr(err)
	}
	defer r.Close()
	base, head := prDiffRange(pr, req.StartSha, req.EndSha)
	limit, offset := pages.Parse(req.Page)
	diff, err := r.Diff(ctx, base, head, gitrepo.DiffOptions{Limit: limit, Offset: offset, PathQuery: pages.ParseQuery(req.Page).Text})
	if err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.GetPullRequestDiffResponse{Diff: diff, Page: pages.Info(offset, limit, int64(diff.TotalFiles)), BaseSha: base.String(), HeadSha: head.String()}, nil
}

func (s *PullRequestService) GetPullRequestFile(ctx context.Context, req *v1.GetPullRequestFileRequest) (*v1.GetPullRequestFileResponse, error) {
	_, repo, _, pr, _, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	r, err := s.forge.Git().Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, mapGitErr(err)
	}
	defer r.Close()
	base, head := prDiffRange(pr, req.StartSha, req.EndSha)
	diff, err := r.Diff(ctx, base, head, gitrepo.DiffOptions{OnlyPath: gitCleanPath(req.Path), Limit: 1})
	if err != nil {
		return nil, mapGitErr(err)
	}
	if len(diff.Files) == 0 {
		return nil, connect.Errorf(connect.CodeNotFound, "path %q did not change", req.Path)
	}
	return &v1.GetPullRequestFileResponse{File: diff.Files[0]}, nil
}

func (s *PullRequestService) ListPullRequestCommits(ctx context.Context, req *v1.ListPullRequestCommitsRequest) (*v1.ListPullRequestCommitsResponse, error) {
	_, repo, _, pr, _, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	r, err := s.forge.Git().Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, mapGitErr(err)
	}
	defer r.Close()
	base, head := prDiffRange(pr, "", "")
	hashes, err := r.CommitRange(ctx, base, head)
	if err != nil {
		return nil, mapGitErr(err)
	}
	commits, info, err := gitPageCommits(r, hashes, req.Page)
	if err != nil {
		return nil, mapGitErr(err)
	}
	enrich := newGitCommitEnricher(ctx, s.store, s.forge.Git(), repo, r)
	enrich.loadStatuses(commits)
	out := &v1.ListPullRequestCommitsResponse{Page: info}
	for _, c := range commits {
		out.Commits = append(out.Commits, enrich.commit(c, false, true))
	}
	return out, nil
}

// ── Merging ──────────────────────────────────────────────────────────────

func (s *PullRequestService) GetMergeability(ctx context.Context, req *v1.GetMergeabilityRequest) (*v1.GetMergeabilityResponse, error) {
	_, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	mg, err := s.forge.Mergeability(ctx, repo, actor, issue, pr)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	out := &v1.GetMergeabilityResponse{State: prMergeableProto(mg.State), ConflictPaths: mg.Conflicts, CanMerge: mg.CanMerge}
	for _, b := range mg.Blockers {
		out.Blockers = append(out.Blockers, &v1.MergeBlocker{Rule: b.Rule, Message: b.Message})
	}
	return out, nil
}

func (s *PullRequestService) MergePullRequest(ctx context.Context, req *v1.MergePullRequestRequest) (*v1.MergePullRequestResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	sha, err := s.forge.Merge(ctx, repo, actor, issue, pr, forge.MergeRequest{
		Method: forge.MergeMethodFromProto(req.Method), Title: req.Title, Message: req.Message, DeleteBranch: req.DeleteBranch, ExpectedHead: req.ExpectedHeadSha,
	})
	if err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, false)
	if err != nil {
		return nil, err
	}
	return &v1.MergePullRequestResponse{PullRequest: out, MergeCommitSha: sha.String()}, nil
}

func (s *PullRequestService) ScheduleAutoMerge(ctx context.Context, req *v1.ScheduleAutoMergeRequest) (*v1.ScheduleAutoMergeResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.ScheduleAutoMerge(ctx, repo, actor, issue, pr, forge.MergeMethodFromProto(req.Method), req.Title, req.Message, req.DeleteBranch); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, true)
	if err != nil {
		return nil, err
	}
	return &v1.ScheduleAutoMergeResponse{PullRequest: out}, nil
}

func (s *PullRequestService) CancelAutoMerge(ctx context.Context, req *v1.CancelAutoMergeRequest) (*v1.CancelAutoMergeResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.CancelAutoMerge(ctx, repo, actor, issue, pr); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, false)
	if err != nil {
		return nil, err
	}
	return &v1.CancelAutoMergeResponse{PullRequest: out}, nil
}

func (s *PullRequestService) ClosePullRequest(ctx context.Context, req *v1.ClosePullRequestRequest) (*v1.ClosePullRequestResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.ClosePull(ctx, repo, actor, issue, pr, req.DeleteBranch); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, false)
	if err != nil {
		return nil, err
	}
	return &v1.ClosePullRequestResponse{PullRequest: out}, nil
}

func (s *PullRequestService) ReopenPullRequest(ctx context.Context, req *v1.ReopenPullRequestRequest) (*v1.ReopenPullRequestResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.ReopenPull(ctx, repo, actor, issue, pr); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, true)
	if err != nil {
		return nil, err
	}
	return &v1.ReopenPullRequestResponse{PullRequest: out}, nil
}

func (s *PullRequestService) UpdateBranch(ctx context.Context, req *v1.UpdateBranchRequest) (*v1.UpdateBranchResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.UpdateBranch(ctx, repo, actor, issue, pr, req.Rebase); err != nil {
		return nil, mapForgeErr(err)
	}
	pr, _ = s.forge.Pull(ctx, issue)
	out, err := s.view(ctx, repo, actor, issue, pr, true)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateBranchResponse{PullRequest: out}, nil
}

func (s *PullRequestService) RestoreHeadBranch(ctx context.Context, req *v1.RestoreHeadBranchRequest) (*v1.RestoreHeadBranchResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.RestoreHeadBranch(ctx, repo, actor, issue, pr); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, false)
	if err != nil {
		return nil, err
	}
	return &v1.RestoreHeadBranchResponse{PullRequest: out}, nil
}

// ── Reviews ──────────────────────────────────────────────────────────────

func (s *PullRequestService) RequestReviewers(ctx context.Context, req *v1.RequestReviewersRequest) (*v1.RequestReviewersResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.RequestReviewers(ctx, repo, actor, issue, req.UserIds, req.TeamIds, false); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, false)
	if err != nil {
		return nil, err
	}
	return &v1.RequestReviewersResponse{PullRequest: out}, nil
}

func (s *PullRequestService) RemoveReviewers(ctx context.Context, req *v1.RemoveReviewersRequest) (*v1.RemoveReviewersResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.RemoveReviewers(ctx, repo, actor, issue, req.UserIds, req.TeamIds); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, repo, actor, issue, pr, false)
	if err != nil {
		return nil, err
	}
	return &v1.RemoveReviewersResponse{PullRequest: out}, nil
}

func prCommentInputs(in []*v1.ReviewCommentInput) []forge.ReviewCommentInput {
	out := make([]forge.ReviewCommentInput, 0, len(in))
	for _, c := range in {
		out = append(out, forge.ReviewCommentInput{Path: gitCleanPath(c.Path), Line: int(c.Line), Side: c.Side, StartLine: int(c.StartLine), Body: c.Body})
	}
	return out
}

func (s *PullRequestService) CreateReview(ctx context.Context, req *v1.CreateReviewRequest) (*v1.CreateReviewResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	rv, err := s.forge.CreateReview(ctx, repo, actor, issue, pr, forge.ReviewStateFromProto(req.State), req.Body, req.CommitSha, prCommentInputs(req.Comments))
	if err != nil {
		return nil, mapForgeErr(err)
	}
	counts, _ := s.store.CountGitReviewComments(ctx, []string{rv.ID})
	return &v1.CreateReviewResponse{Review: prReviewProto(ctx, newGitUserCache(s.store), rv, counts)}, nil
}

func (s *PullRequestService) SubmitReview(ctx context.Context, req *v1.SubmitReviewRequest) (*v1.SubmitReviewResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	rv, err := s.forge.Review(ctx, issue, req.ReviewId)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	rv, err = s.forge.SubmitReview(ctx, repo, actor, issue, pr, rv, forge.ReviewStateFromProto(req.State), req.Body)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	counts, _ := s.store.CountGitReviewComments(ctx, []string{rv.ID})
	return &v1.SubmitReviewResponse{Review: prReviewProto(ctx, newGitUserCache(s.store), rv, counts)}, nil
}

func (s *PullRequestService) DismissReview(ctx context.Context, req *v1.DismissReviewRequest) (*v1.DismissReviewResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	rv, err := s.forge.Review(ctx, issue, req.ReviewId)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.DismissReview(ctx, repo, actor, issue, pr, rv, req.Message); err != nil {
		return nil, mapForgeErr(err)
	}
	counts, _ := s.store.CountGitReviewComments(ctx, []string{rv.ID})
	return &v1.DismissReviewResponse{Review: prReviewProto(ctx, newGitUserCache(s.store), rv, counts)}, nil
}

func (s *PullRequestService) ListReviews(ctx context.Context, req *v1.ListReviewsRequest) (*v1.ListReviewsResponse, error) {
	user, _, issue, _, _, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListGitReviews(ctx, issue.ID, false, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if user != nil && !user.IsAnonymous() {
		if pending, _ := s.store.GetPendingGitReview(ctx, issue.ID, user.ID); pending != nil {
			rows = append(rows, pending)
		}
	}
	ids := make([]string, 0, len(rows))
	for _, rv := range rows {
		ids = append(ids, rv.ID)
	}
	counts, _ := s.store.CountGitReviewComments(ctx, ids)
	users := newGitUserCache(s.store)
	out := &v1.ListReviewsResponse{Page: pages.Info(offset, limit, total)}
	for _, rv := range rows {
		out.Reviews = append(out.Reviews, prReviewProto(ctx, users, rv, counts))
	}
	return out, nil
}

func (s *PullRequestService) CreateReviewComment(ctx context.Context, req *v1.CreateReviewCommentRequest) (*v1.CreateReviewCommentResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	in := forge.ReviewCommentInput{Path: gitCleanPath(req.Path), Line: int(req.Line), Side: req.Side, StartLine: int(req.StartLine), Body: req.Body, ThreadID: req.ThreadId}
	c, err := s.forge.CreateReviewComment(ctx, repo, actor, issue, pr, req.ReviewId, in, req.CommitSha)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.CreateReviewCommentResponse{Comment: prReviewCommentProto(ctx, newGitUserCache(s.store), c, nil, user.ID)}, nil
}

func (s *PullRequestService) UpdateReviewComment(ctx context.Context, req *v1.UpdateReviewCommentRequest) (*v1.UpdateReviewCommentResponse, error) {
	user, repo, issue, _, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	c, err := s.forge.ReviewComment(ctx, issue, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.UpdateReviewComment(ctx, repo, actor, c, req.Body); err != nil {
		return nil, mapForgeErr(err)
	}
	reactions, _ := s.store.ListGitReactions(ctx, "review_comment", []string{c.ID})
	return &v1.UpdateReviewCommentResponse{Comment: prReviewCommentProto(ctx, newGitUserCache(s.store), c, reactions[c.ID], user.ID)}, nil
}

func (s *PullRequestService) DeleteReviewComment(ctx context.Context, req *v1.DeleteReviewCommentRequest) (*v1.DeleteReviewCommentResponse, error) {
	user, _, issue, _, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	c, err := s.forge.ReviewComment(ctx, issue, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.DeleteReviewComment(ctx, actor, c); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteReviewCommentResponse{}, nil
}

func (s *PullRequestService) ListReviewComments(ctx context.Context, req *v1.ListReviewCommentsRequest) (*v1.ListReviewCommentsResponse, error) {
	user, _, issue, _, _, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListGitReviewComments(ctx, issue.ID, gitCleanPath(req.Path), viewerID(user))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	ids := make([]string, 0, len(rows))
	for _, c := range rows {
		ids = append(ids, c.ID)
	}
	reactions, _ := s.store.ListGitReactions(ctx, "review_comment", ids)
	users := newGitUserCache(s.store)
	out := &v1.ListReviewCommentsResponse{}
	for _, c := range rows {
		out.Comments = append(out.Comments, prReviewCommentProto(ctx, users, c, reactions[c.ID], viewerID(user)))
	}
	return out, nil
}

func (s *PullRequestService) ResolveReviewThread(ctx context.Context, req *v1.ResolveReviewThreadRequest) (*v1.ResolveReviewThreadResponse, error) {
	user, repo, issue, _, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	root, err := s.forge.ResolveThread(ctx, repo, actor, issue, req.ThreadId, req.Resolved)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.ResolveReviewThreadResponse{Comment: prReviewCommentProto(ctx, newGitUserCache(s.store), root, nil, user.ID)}, nil
}

func (s *PullRequestService) ApplySuggestion(ctx context.Context, req *v1.ApplySuggestionRequest) (*v1.ApplySuggestionResponse, error) {
	user, repo, issue, pr, actor, err := s.pull(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	c, err := s.forge.ReviewComment(ctx, issue, req.CommentId)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	sha, err := s.forge.ApplySuggestion(ctx, repo, actor, issue, pr, c, req.Message)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	headRepo, _ := s.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	if headRepo == nil {
		headRepo = repo
	}
	r, err := s.forge.Git().Engine().OpenRepo(headRepo.Namespace, headRepo.Name, false)
	if err != nil {
		return &v1.ApplySuggestionResponse{Commit: &v1.GitCommit{Sha: sha.String()}}, nil
	}
	defer r.Close()
	commit, err := r.Commit(sha)
	if err != nil {
		return &v1.ApplySuggestionResponse{Commit: &v1.GitCommit{Sha: sha.String()}}, nil
	}
	return &v1.ApplySuggestionResponse{Commit: newGitCommitEnricher(ctx, s.store, s.forge.Git(), headRepo, r).commit(commit, true, false)}, nil
}
