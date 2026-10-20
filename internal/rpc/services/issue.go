package services

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.IssueServiceHandler = (*IssueService)(nil)

type IssueService struct {
	store *stores.Store
	forge *forge.Manager
	log   *logger.Logger
}

func NewIssueService(store *stores.Store, fm *forge.Manager, log *logger.Logger) *IssueService {
	return &IssueService{store: store, forge: fm, log: log}
}

func pagesOrderBy(page *v1.PageRequest, allowed map[string]bool, def string) string {
	return pages.OrderBy(page, allowed, def)
}

// Loads a visible repository with the issues unit readable
func (s *IssueService) repo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, error) {
	repo, err := visibleGitRepo(ctx, s.store, s.forge.Git().Access(), user, namespace, name)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// Actor over the unit an issue lives in
func (s *IssueService) actor(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, isPull bool) (forge.Actor, error) {
	unit := storage.GitUnitIssues
	if isPull {
		unit = storage.GitUnitPulls
	}
	levels := s.forge.Git().Access().Levels(ctx, user, repo)
	level := levels.Unit(unit)
	if !gitrepo.UnitEnabled(repo, unit) && !levels.IsOwner && !levels.CanAdmin {
		return forge.Actor{}, connect.Errorf(connect.CodeFailedPrecondition, "the %s feature is disabled on this repository", unit)
	}
	if level < gitrepo.LevelRead {
		return forge.Actor{}, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	return forge.Actor{User: user, CanWrite: level >= gitrepo.LevelWrite, CanAdmin: levels.CanAdmin || levels.IsOwner}, nil
}

// Loads a repository, an issue, and the caller's actor
func (s *IssueService) issue(ctx context.Context, namespace, name string, number int64) (*auth.AuthenticatedUser, *storage.GitRepository, *storage.GitIssue, forge.Actor, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, namespace, name)
	if err != nil {
		return nil, nil, nil, forge.Actor{}, err
	}
	issue, err := s.forge.Issue(ctx, repo, number)
	if err != nil {
		return nil, nil, nil, forge.Actor{}, mapForgeErr(err)
	}
	actor, err := s.actor(ctx, user, repo, issue.IsPull)
	if err != nil {
		return nil, nil, nil, forge.Actor{}, err
	}
	return user, repo, issue, actor, nil
}

// Requires a signed in caller and a writable portal
func issueWriteAllowed(ctx context.Context, user *auth.AuthenticatedUser) error {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return err
	}
	if user == nil || user.IsAnonymous() {
		return connect.NewError(connect.CodeUnauthenticated, "")
	}
	return nil
}

func viewerID(user *auth.AuthenticatedUser) string {
	if user == nil || user.IsAnonymous() {
		return ""
	}
	return user.ID
}

func (s *IssueService) view(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, issue *storage.GitIssue) (*v1.Issue, error) {
	v, err := s.forge.IssueView(ctx, user, repo, issue)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return s.issueToProto(ctx, newGitUserCache(s.store), viewerID(user), v), nil
}

func optionalTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil || (ts.Seconds == 0 && ts.Nanos == 0) {
		return nil
	}
	t := ts.AsTime()
	return &t
}

// ── Issues ───────────────────────────────────────────────────────────────

func (s *IssueService) CreateIssue(ctx context.Context, req *v1.CreateIssueRequest) (*v1.CreateIssueResponse, error) {
	user := auth.UserFromContext(ctx)
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	actor, err := s.actor(ctx, user, repo, false)
	if err != nil {
		return nil, err
	}
	issue, err := s.forge.CreateIssue(ctx, repo, actor, forge.NewIssue{
		Title: req.Title, Body: req.Body, LabelIDs: req.LabelIds, MilestoneID: req.MilestoneId, AssigneeIDs: req.AssigneeIds,
		DueDate: optionalTime(req.DueDate), ProjectID: req.ProjectId, Template: req.Template,
	})
	if err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.CreateIssueResponse{Issue: out}, nil
}

func (s *IssueService) GetIssue(ctx context.Context, req *v1.GetIssueRequest) (*v1.GetIssueResponse, error) {
	user, repo, issue, _, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.GetIssueResponse{Issue: out}, nil
}

func (s *IssueService) ListIssues(ctx context.Context, req *v1.ListIssuesRequest) (*v1.ListIssuesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	state := issueStateString(req.State)
	if req.State == v1.IssueState_ISSUE_STATE_UNSPECIFIED {
		state = "open"
	}
	opts := stores.IssueListOptions{
		RepoID: repo.ID, State: state, Labels: req.Labels, Milestone: req.Milestone, Assignee: req.Assignee, Author: req.Author,
		Mentioned: req.Mentioned, IsPull: req.IsPull, ProjectID: req.ProjectId, Query: pages.ParseQuery(req.Page),
		OrderBy: issueOrder(req.Page), Limit: limit, Offset: offset,
	}
	if opts.IsPull == nil {
		f := false
		opts.IsPull = &f
	}
	unit := storage.GitUnitIssues
	if *opts.IsPull {
		unit = storage.GitUnitPulls
	}
	if !gitrepo.UnitEnabled(repo, unit) {
		return &v1.ListIssuesResponse{Page: pages.Info(offset, limit, 0)}, nil
	}
	if s.forge.Git().Access().Levels(ctx, user, repo).Unit(unit) < gitrepo.LevelRead {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	issues, total, err := s.forge.ListIssues(ctx, opts)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	countOpts := opts
	countOpts.State = ""
	open, closed, _ := s.store.CountGitIssuesByState(ctx, countOpts)
	views, err := s.forge.IssueViews(ctx, user, map[string]*storage.GitRepository{repo.ID: repo}, issues)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	users := newGitUserCache(s.store)
	out := &v1.ListIssuesResponse{Page: pages.Info(offset, limit, total), OpenCount: open, ClosedCount: closed}
	for _, v := range views {
		out.Issues = append(out.Issues, s.issueToProto(ctx, users, viewerID(user), v))
	}
	return out, nil
}

func (s *IssueService) SearchIssues(ctx context.Context, req *v1.SearchIssuesRequest) (*v1.SearchIssuesResponse, error) {
	user := auth.UserFromContext(ctx)
	issueIDs, pullIDs, repos, err := s.forge.VisibleRepoIDs(ctx, user, portal.ScopeNamespace(ctx, req.Namespace))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	limit, offset := pages.Parse(req.Page)
	if len(issueIDs) == 0 && len(pullIDs) == 0 {
		return &v1.SearchIssuesResponse{Page: pages.Info(offset, limit, 0)}, nil
	}
	opts := stores.IssueListOptions{
		IssueRepoIDs: issueIDs, PullRepoIDs: pullIDs, State: issueStateString(req.State), IsPull: req.IsPull,
		Query: pages.ParseQuery(req.Page), OrderBy: issueOrder(req.Page), Limit: limit, Offset: offset,
	}
	if req.Involved && user != nil && !user.IsAnonymous() {
		opts.Involved = user.ID
	}
	issues, total, err := s.forge.ListIssues(ctx, opts)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	views, err := s.forge.IssueViews(ctx, user, repos, issues)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	users := newGitUserCache(s.store)
	out := &v1.SearchIssuesResponse{Page: pages.Info(offset, limit, total)}
	for _, v := range views {
		out.Issues = append(out.Issues, s.issueToProto(ctx, users, viewerID(user), v))
	}
	return out, nil
}

func (s *IssueService) UpdateIssue(ctx context.Context, req *v1.UpdateIssueRequest) (*v1.UpdateIssueResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	up := forge.IssueUpdate{Title: req.Title, Body: req.Body, MilestoneID: req.MilestoneId, SetLabels: req.SetLabels, LabelIDs: req.LabelIds, SetAssign: req.SetAssignees, AssigneeIDs: req.AssigneeIds}
	if req.State != nil {
		st := issueStateString(*req.State)
		up.State = &st
	}
	if req.DueDate != nil {
		if t := optionalTime(req.DueDate); t != nil {
			up.DueDate = t
		} else {
			up.ClearDue = true
		}
	}
	if err := s.forge.UpdateIssue(ctx, repo, actor, issue, up); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateIssueResponse{Issue: out}, nil
}

func (s *IssueService) BulkUpdateIssues(ctx context.Context, req *v1.BulkUpdateIssuesRequest) (*v1.BulkUpdateIssuesResponse, error) {
	user := auth.UserFromContext(ctx)
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	actor, err := s.actor(ctx, user, repo, false)
	if err != nil {
		return nil, err
	}
	up := forge.BulkUpdate{MilestoneID: req.MilestoneId, AddLabelIDs: req.AddLabelIds, RemoveLabelIDs: req.RemoveLabelIds, AddAssigneeIDs: req.AddAssigneeIds, RemoveAssigneeIDs: req.RemoveAssigneeIds}
	if req.State != nil {
		st := issueStateString(*req.State)
		up.State = &st
	}
	n, err := s.forge.BulkUpdate(ctx, repo, actor, req.Numbers, up)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.BulkUpdateIssuesResponse{UpdatedCount: int32(n)}, nil
}

func (s *IssueService) LockIssue(ctx context.Context, req *v1.LockIssueRequest) (*v1.LockIssueResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.SetLocked(ctx, repo, actor, issue, true, req.Reason); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.LockIssueResponse{Issue: out}, nil
}

func (s *IssueService) UnlockIssue(ctx context.Context, req *v1.UnlockIssueRequest) (*v1.UnlockIssueResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.SetLocked(ctx, repo, actor, issue, false, ""); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.UnlockIssueResponse{Issue: out}, nil
}

func (s *IssueService) PinIssue(ctx context.Context, req *v1.PinIssueRequest) (*v1.PinIssueResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.SetPinned(ctx, repo, actor, issue, true); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.PinIssueResponse{Issue: out}, nil
}

func (s *IssueService) UnpinIssue(ctx context.Context, req *v1.UnpinIssueRequest) (*v1.UnpinIssueResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.SetPinned(ctx, repo, actor, issue, false); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.view(ctx, user, repo, issue)
	if err != nil {
		return nil, err
	}
	return &v1.UnpinIssueResponse{Issue: out}, nil
}

func (s *IssueService) DeleteIssue(ctx context.Context, req *v1.DeleteIssueRequest) (*v1.DeleteIssueResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.DeleteIssue(ctx, repo, actor, issue); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteIssueResponse{}, nil
}

// ── Comments and reactions ───────────────────────────────────────────────

func (s *IssueService) CreateComment(ctx context.Context, req *v1.CreateCommentRequest) (*v1.CreateCommentResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	c, err := s.forge.CreateComment(ctx, repo, actor, issue, req.Body, false)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.CreateCommentResponse{Comment: issueCommentProto(ctx, newGitUserCache(s.store), c, nil, user.ID)}, nil
}

// Loads a comment of the repository with the caller's actor
func (s *IssueService) comment(ctx context.Context, namespace, name, id string) (*auth.AuthenticatedUser, *storage.GitRepository, *storage.GitIssue, *storage.GitComment, forge.Actor, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, namespace, name)
	if err != nil {
		return nil, nil, nil, nil, forge.Actor{}, err
	}
	issue, c, err := s.forge.Comment(ctx, repo, id)
	if err != nil {
		return nil, nil, nil, nil, forge.Actor{}, mapForgeErr(err)
	}
	actor, err := s.actor(ctx, user, repo, issue.IsPull)
	if err != nil {
		return nil, nil, nil, nil, forge.Actor{}, err
	}
	return user, repo, issue, c, actor, nil
}

func (s *IssueService) UpdateComment(ctx context.Context, req *v1.UpdateCommentRequest) (*v1.UpdateCommentResponse, error) {
	user, repo, issue, c, actor, err := s.comment(ctx, req.Namespace, req.Name, req.Id)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.UpdateComment(ctx, repo, actor, issue, c, req.Body); err != nil {
		return nil, mapForgeErr(err)
	}
	reactions, _ := s.store.ListGitReactions(ctx, "comment", []string{c.ID})
	return &v1.UpdateCommentResponse{Comment: issueCommentProto(ctx, newGitUserCache(s.store), c, reactions[c.ID], user.ID)}, nil
}

func (s *IssueService) DeleteComment(ctx context.Context, req *v1.DeleteCommentRequest) (*v1.DeleteCommentResponse, error) {
	user, repo, issue, c, actor, err := s.comment(ctx, req.Namespace, req.Name, req.Id)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.DeleteComment(ctx, repo, actor, issue, c); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteCommentResponse{}, nil
}

func (s *IssueService) ListComments(ctx context.Context, req *v1.ListCommentsRequest) (*v1.ListCommentsResponse, error) {
	user, repo, issue, _, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListGitComments(ctx, issue.ID, req.CommentsOnly, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	rows = s.visibleComments(ctx, user, repo, rows)
	ids := make([]string, 0, len(rows))
	for _, c := range rows {
		ids = append(ids, c.ID)
	}
	reactions, _ := s.store.ListGitReactions(ctx, "comment", ids)
	users := newGitUserCache(s.store)
	out := &v1.ListCommentsResponse{Page: pages.Info(offset, limit, total)}
	for _, c := range rows {
		out.Comments = append(out.Comments, issueCommentProto(ctx, users, c, reactions[c.ID], viewerID(user)))
	}
	return out, nil
}

// Drops board events and foreign references the viewer cannot see
func (s *IssueService) visibleComments(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, rows []*storage.GitComment) []*storage.GitComment {
	out := rows[:0]
	boards := map[string]bool{}
	repos := map[string]bool{}
	access := s.forge.Git().Access()
	for _, c := range rows {
		switch c.Type {
		case forge.EventProjectAdded, forge.EventProjectMoved, forge.EventProjectRemoved:
			id := payloadString(c.Payload, "project_id")
			ok, seen := boards[id]
			if !seen {
				p, _ := s.store.GetGitProject(ctx, id)
				if p != nil {
					ok = s.forge.ProjectVisible(ctx, user, p, repo)
				} else {
					ok = access.Levels(ctx, user, repo).Unit(storage.GitUnitProjects) >= gitrepo.LevelWrite
				}
				boards[id] = ok
			}
			if !ok {
				continue
			}
		case forge.EventReferenced, forge.EventDependencyAdded, forge.EventDependencyRemoved, forge.EventBlockerClosed, forge.EventBlockerReopened:
			full := payloadString(c.Payload, "repo")
			if full != "" && full != repo.Namespace+"/"+repo.Name {
				ok, seen := repos[full]
				if !seen {
					ns, nm, _ := strings.Cut(full, "/")
					other, _ := s.store.GetGitRepository(ctx, ns, nm)
					ok = other != nil && access.CanSee(ctx, user, other)
					repos[full] = ok
				}
				if !ok {
					continue
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// String field of an event payload, empty when absent
func payloadString(raw, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func (s *IssueService) react(ctx context.Context, namespace, name string, number int64, commentID, emoji string, add bool) ([]*v1.ReactionGroup, error) {
	user, repo, issue, actor, err := s.issue(ctx, namespace, name, number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	reactions, err := s.forge.React(ctx, repo, actor, issue, commentID, emoji, add)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return issueReactionGroups(ctx, newGitUserCache(s.store), reactions, user.ID), nil
}

func (s *IssueService) AddReaction(ctx context.Context, req *v1.AddReactionRequest) (*v1.AddReactionResponse, error) {
	groups, err := s.react(ctx, req.Namespace, req.Name, req.Number, req.CommentId, req.Emoji, true)
	if err != nil {
		return nil, err
	}
	return &v1.AddReactionResponse{Reactions: groups}, nil
}

func (s *IssueService) RemoveReaction(ctx context.Context, req *v1.RemoveReactionRequest) (*v1.RemoveReactionResponse, error) {
	groups, err := s.react(ctx, req.Namespace, req.Name, req.Number, req.CommentId, req.Emoji, false)
	if err != nil {
		return nil, err
	}
	return &v1.RemoveReactionResponse{Reactions: groups}, nil
}

// ── Labels ───────────────────────────────────────────────────────────────

// Resolves the label owner, a repository or an organization, with write rights
func (s *IssueService) labelScope(ctx context.Context, namespace, name, orgID string, write bool) (*storage.GitRepository, string, error) {
	user := auth.UserFromContext(ctx)
	if name != "" || orgID == "" {
		repo, err := s.repo(ctx, user, namespace, name)
		if err != nil {
			return nil, "", err
		}
		if write {
			if err := issueWriteAllowed(ctx, user); err != nil {
				return nil, "", err
			}
			if s.forge.Git().Access().Level(ctx, user, repo, storage.GitUnitIssues) < gitrepo.LevelWrite {
				return nil, "", connect.Errorf(connect.CodePermissionDenied, "write access to issues required")
			}
		}
		return repo, "", nil
	}
	org, err := s.store.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		return nil, "", connect.Errorf(connect.CodeNotFound, "organization not found")
	}
	if write {
		if err := issueWriteAllowed(ctx, user); err != nil {
			return nil, "", err
		}
		if _, canWrite := s.forge.OrgProjectLevel(ctx, user, org.ID); !canWrite {
			return nil, "", connect.Errorf(connect.CodePermissionDenied, "organization admin required")
		}
	}
	return nil, org.ID, nil
}

func (s *IssueService) ListLabels(ctx context.Context, req *v1.ListLabelsRequest) (*v1.ListLabelsResponse, error) {
	repo, orgID, err := s.labelScope(ctx, req.Namespace, req.Name, req.OrgId, false)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	labels, total, err := s.forge.ListLabels(ctx, repo, orgID, pages.ParseQuery(req.Page), limit, offset)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	counts := s.forge.LabelCounts(ctx, labels)
	out := &v1.ListLabelsResponse{Page: pages.Info(offset, limit, total)}
	for _, l := range labels {
		out.Labels = append(out.Labels, issueLabelProto(l, counts))
	}
	return out, nil
}

func (s *IssueService) CreateLabel(ctx context.Context, req *v1.CreateLabelRequest) (*v1.CreateLabelResponse, error) {
	repo, orgID, err := s.labelScope(ctx, req.Namespace, req.Name, req.OrgId, true)
	if err != nil {
		return nil, err
	}
	l, err := s.forge.CreateLabel(ctx, repo, orgID, forge.LabelInput{Name: req.LabelName, Color: req.Color, Description: req.Description, Exclusive: req.Exclusive})
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.CreateLabelResponse{Label: issueLabelProto(l, nil)}, nil
}

func (s *IssueService) UpdateLabel(ctx context.Context, req *v1.UpdateLabelRequest) (*v1.UpdateLabelResponse, error) {
	repo, orgID, err := s.labelScope(ctx, req.Namespace, req.Name, req.OrgId, true)
	if err != nil {
		return nil, err
	}
	l, err := s.forge.Label(ctx, repo, orgID, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if repo != nil && l.OrgID != nil {
		return nil, connect.Errorf(connect.CodePermissionDenied, "organization labels are edited from the organization")
	}
	if err := s.forge.UpdateLabel(ctx, l, req.LabelName, req.Color, req.Description, req.Exclusive); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.UpdateLabelResponse{Label: issueLabelProto(l, s.forge.LabelCounts(ctx, []*storage.GitLabel{l}))}, nil
}

func (s *IssueService) DeleteLabel(ctx context.Context, req *v1.DeleteLabelRequest) (*v1.DeleteLabelResponse, error) {
	repo, orgID, err := s.labelScope(ctx, req.Namespace, req.Name, req.OrgId, true)
	if err != nil {
		return nil, err
	}
	l, err := s.forge.Label(ctx, repo, orgID, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if repo != nil && l.OrgID != nil {
		return nil, connect.Errorf(connect.CodePermissionDenied, "organization labels are deleted from the organization")
	}
	if err := s.forge.DeleteLabel(ctx, l); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteLabelResponse{}, nil
}

// ── Milestones ───────────────────────────────────────────────────────────

func (s *IssueService) ListMilestones(ctx context.Context, req *v1.ListMilestonesRequest) (*v1.ListMilestonesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := s.repo(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.forge.ListMilestones(ctx, repo, issueStateString(req.State), pages.ParseQuery(req.Page), limit, offset)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	counts := s.forge.MilestoneCounts(ctx, rows)
	out := &v1.ListMilestonesResponse{Page: pages.Info(offset, limit, total)}
	for _, ms := range rows {
		out.Milestones = append(out.Milestones, issueMilestoneProto(ms, counts))
	}
	return out, nil
}

func (s *IssueService) milestoneActor(ctx context.Context, namespace, name string) (*storage.GitRepository, forge.Actor, error) {
	user := auth.UserFromContext(ctx)
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, forge.Actor{}, err
	}
	repo, err := s.repo(ctx, user, namespace, name)
	if err != nil {
		return nil, forge.Actor{}, err
	}
	actor, err := s.actor(ctx, user, repo, false)
	if err != nil {
		return nil, forge.Actor{}, err
	}
	return repo, actor, nil
}

func (s *IssueService) CreateMilestone(ctx context.Context, req *v1.CreateMilestoneRequest) (*v1.CreateMilestoneResponse, error) {
	repo, actor, err := s.milestoneActor(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	ms, err := s.forge.CreateMilestone(ctx, repo, actor, forge.MilestoneInput{Title: &req.Title, Description: &req.Description, DueDate: optionalTime(req.DueDate)})
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.CreateMilestoneResponse{Milestone: issueMilestoneProto(ms, nil)}, nil
}

func (s *IssueService) UpdateMilestone(ctx context.Context, req *v1.UpdateMilestoneRequest) (*v1.UpdateMilestoneResponse, error) {
	repo, actor, err := s.milestoneActor(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	ms, err := s.forge.Milestone(ctx, repo, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	in := forge.MilestoneInput{Title: req.Title, Description: req.Description}
	if req.DueDate != nil {
		if t := optionalTime(req.DueDate); t != nil {
			in.DueDate = t
		} else {
			in.ClearDue = true
		}
	}
	if req.State != nil {
		st := issueStateString(*req.State)
		in.State = &st
	}
	if err := s.forge.UpdateMilestone(ctx, repo, actor, ms, in); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.UpdateMilestoneResponse{Milestone: issueMilestoneProto(ms, s.forge.MilestoneCounts(ctx, []*storage.GitMilestone{ms}))}, nil
}

func (s *IssueService) DeleteMilestone(ctx context.Context, req *v1.DeleteMilestoneRequest) (*v1.DeleteMilestoneResponse, error) {
	repo, actor, err := s.milestoneActor(ctx, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	ms, err := s.forge.Milestone(ctx, repo, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.DeleteMilestone(ctx, repo, actor, ms); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteMilestoneResponse{}, nil
}

// ── Dependencies ─────────────────────────────────────────────────────────

func (s *IssueService) dependencyRefs(ctx context.Context, user *auth.AuthenticatedUser, issue *storage.GitIssue) ([]*v1.IssueRef, []*v1.IssueRef, error) {
	blockedBy, blocking, err := s.forge.Dependencies(ctx, issue)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	repos := s.reposOf(ctx, append(append([]*storage.GitIssue{}, blockedBy...), blocking...))
	access := s.forge.Git().Access()
	visible := map[string]bool{issue.RepoID: true}
	canSee := func(is *storage.GitIssue) bool {
		ok, seen := visible[is.RepoID]
		if !seen {
			r := repos[is.RepoID]
			ok = r != nil && access.CanSee(ctx, user, r)
			visible[is.RepoID] = ok
		}
		return ok
	}
	var a, b []*v1.IssueRef
	for _, is := range blockedBy {
		if canSee(is) {
			a = append(a, issueRefProto(repos, is))
		}
	}
	for _, is := range blocking {
		if canSee(is) {
			b = append(b, issueRefProto(repos, is))
		}
	}
	return a, b, nil
}

func (s *IssueService) AddDependency(ctx context.Context, req *v1.AddDependencyRequest) (*v1.AddDependencyResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.AddDependency(ctx, repo, actor, issue, req.DependsOn); err != nil {
		return nil, mapForgeErr(err)
	}
	blockedBy, _, err := s.dependencyRefs(ctx, user, issue)
	if err != nil {
		return nil, err
	}
	return &v1.AddDependencyResponse{BlockedBy: blockedBy}, nil
}

func (s *IssueService) RemoveDependency(ctx context.Context, req *v1.RemoveDependencyRequest) (*v1.RemoveDependencyResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.RemoveDependency(ctx, repo, actor, issue, req.DependsOn); err != nil {
		return nil, mapForgeErr(err)
	}
	blockedBy, _, err := s.dependencyRefs(ctx, user, issue)
	if err != nil {
		return nil, err
	}
	return &v1.RemoveDependencyResponse{BlockedBy: blockedBy}, nil
}

func (s *IssueService) ListDependencies(ctx context.Context, req *v1.ListDependenciesRequest) (*v1.ListDependenciesResponse, error) {
	user, _, issue, _, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	blockedBy, blocking, err := s.dependencyRefs(ctx, user, issue)
	if err != nil {
		return nil, err
	}
	return &v1.ListDependenciesResponse{BlockedBy: blockedBy, Blocking: blocking}, nil
}

// ── Time tracking ────────────────────────────────────────────────────────

func (s *IssueService) AddTrackedTime(ctx context.Context, req *v1.AddTrackedTimeRequest) (*v1.AddTrackedTimeResponse, error) {
	user, repo, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	t, err := s.forge.AddTrackedTime(ctx, repo, actor, issue, req.Seconds, req.Note)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.AddTrackedTimeResponse{Time: issueTrackedTimeProto(ctx, newGitUserCache(s.store), t)}, nil
}

func (s *IssueService) DeleteTrackedTime(ctx context.Context, req *v1.DeleteTrackedTimeRequest) (*v1.DeleteTrackedTimeResponse, error) {
	user, _, issue, actor, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	if err := issueWriteAllowed(ctx, user); err != nil {
		return nil, err
	}
	if err := s.forge.DeleteTrackedTime(ctx, actor, issue, req.Id); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteTrackedTimeResponse{}, nil
}

func (s *IssueService) ListTrackedTime(ctx context.Context, req *v1.ListTrackedTimeRequest) (*v1.ListTrackedTimeResponse, error) {
	_, repo, issue, _, err := s.issue(ctx, req.Namespace, req.Name, req.Number)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	if !gitrepo.UnitsOf(repo).TimeTracking {
		return &v1.ListTrackedTimeResponse{Page: pages.Info(offset, limit, 0)}, nil
	}
	rows, total, err := s.store.ListGitTrackedTime(ctx, issue.ID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	sums, _ := s.store.SumGitTrackedTime(ctx, []string{issue.ID})
	users := newGitUserCache(s.store)
	out := &v1.ListTrackedTimeResponse{Page: pages.Info(offset, limit, total), TotalSeconds: sums[issue.ID]}
	for _, t := range rows {
		out.Times = append(out.Times, issueTrackedTimeProto(ctx, users, t))
	}
	return out, nil
}

// ── Subscriptions ────────────────────────────────────────────────────────

func (s *IssueService) subscribe(ctx context.Context, namespace, name string, number int64, on bool) error {
	user, _, issue, _, err := s.issue(ctx, namespace, name, number)
	if err != nil {
		return err
	}
	if user == nil || user.IsAnonymous() {
		return connect.NewError(connect.CodeUnauthenticated, "")
	}
	if err := s.store.SetGitIssueSubscription(ctx, issue.ID, user.ID, on); err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return nil
}

func (s *IssueService) SubscribeIssue(ctx context.Context, req *v1.SubscribeIssueRequest) (*v1.SubscribeIssueResponse, error) {
	if err := s.subscribe(ctx, req.Namespace, req.Name, req.Number, true); err != nil {
		return nil, err
	}
	return &v1.SubscribeIssueResponse{}, nil
}

func (s *IssueService) UnsubscribeIssue(ctx context.Context, req *v1.UnsubscribeIssueRequest) (*v1.UnsubscribeIssueResponse, error) {
	if err := s.subscribe(ctx, req.Namespace, req.Name, req.Number, false); err != nil {
		return nil, err
	}
	return &v1.UnsubscribeIssueResponse{}, nil
}
