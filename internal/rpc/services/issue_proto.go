package services

import (
	"context"
	"errors"
	"sort"
	"strings"

	"connectrpc.com/connect/v2"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Maps forge errors onto connect codes
func mapForgeErr(err error) error {
	if err == nil {
		return nil
	}
	var cerr *connect.Error
	switch {
	case errors.As(err, &cerr):
		return err
	case errors.Is(err, forge.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, forge.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, forge.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, forge.ErrLocked), errors.Is(err, forge.ErrBlocked), errors.Is(err, forge.ErrDisabled):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, forge.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err.Error()).WithCause(err)
	}
	return mapGitErr(err)
}

func issueStateProto(state string) v1.IssueState {
	switch state {
	case "open":
		return v1.IssueState_ISSUE_STATE_OPEN
	case "closed":
		return v1.IssueState_ISSUE_STATE_CLOSED
	}
	return v1.IssueState_ISSUE_STATE_UNSPECIFIED
}

func issueStateString(state v1.IssueState) string {
	switch state {
	case v1.IssueState_ISSUE_STATE_OPEN:
		return "open"
	case v1.IssueState_ISSUE_STATE_CLOSED:
		return "closed"
	}
	return ""
}

func issueUserProto(u *storage.User) *v1.IssueUser {
	if u == nil {
		return nil
	}
	return &v1.IssueUser{Id: u.ID, Username: u.Username, DisplayName: u.DisplayName, IsAgent: isAgentUser(u)}
}

func issueLabelProto(l *storage.GitLabel, counts map[string][2]int64) *v1.Label {
	out := &v1.Label{Id: l.ID, Name: l.Name, Color: l.Color, Description: l.Description, IsOrgLabel: l.OrgID != nil, ExclusiveScope: l.ExclusiveScope}
	if c, ok := counts[l.ID]; ok {
		out.OpenIssues, out.ClosedIssues = c[0], c[1]
	}
	return out
}

func issueMilestoneProto(ms *storage.GitMilestone, counts map[string][2]int64) *v1.Milestone {
	if ms == nil {
		return nil
	}
	out := &v1.Milestone{
		Id: ms.ID, Title: ms.Title, Description: ms.Description, State: issueStateProto(ms.State),
		CreatedAt: timestamppb.New(ms.CreatedAt), UpdatedAt: timestamppb.New(ms.UpdatedAt),
	}
	if ms.DueDate != nil {
		out.DueDate = timestamppb.New(*ms.DueDate)
	}
	if ms.ClosedAt != nil {
		out.ClosedAt = timestamppb.New(*ms.ClosedAt)
	}
	if c, ok := counts[ms.ID]; ok {
		out.OpenIssues, out.ClosedIssues = c[0], c[1]
	}
	return out
}

// Groups reactions per emoji with the viewer's own flag
func issueReactionGroups(ctx context.Context, users *gitUserCache, reactions []*storage.GitReaction, viewerID string) []*v1.ReactionGroup {
	byEmoji := map[string]*v1.ReactionGroup{}
	for _, r := range reactions {
		g := byEmoji[r.Emoji]
		if g == nil {
			g = &v1.ReactionGroup{Emoji: r.Emoji}
			byEmoji[r.Emoji] = g
		}
		g.Count++
		if r.UserID == viewerID {
			g.Reacted = true
		}
		if len(g.Usernames) < 10 {
			if name := users.username(ctx, r.UserID); name != "" {
				g.Usernames = append(g.Usernames, name)
			}
		}
	}
	out := make([]*v1.ReactionGroup, 0, len(byEmoji))
	for _, e := range forge.Reactions {
		if g := byEmoji[e]; g != nil {
			out = append(out, g)
		}
	}
	return out
}

// Reaction authors the list rows name
func issueViewUserIDs(views []*forge.IssueView) []string {
	var ids []string
	for _, v := range views {
		for _, r := range v.Reactions {
			ids = append(ids, r.UserID)
		}
	}
	return ids
}

// Converts an issue view
func (s *IssueService) issueToProto(ctx context.Context, users *gitUserCache, viewerID string, v *forge.IssueView) *v1.Issue {
	return issueViewProto(ctx, s.forge, users, viewerID, v)
}

// Converts an issue view for any service
func issueViewProto(ctx context.Context, fm *forge.Manager, users *gitUserCache, viewerID string, v *forge.IssueView) *v1.Issue {
	is := v.Issue
	out := &v1.Issue{
		Id:             is.ID,
		Number:         is.Number,
		IsPull:         is.IsPull,
		Title:          is.Title,
		Body:           is.Body,
		State:          issueStateProto(is.State),
		Author:         issueUserProto(v.Author),
		Milestone:      issueMilestoneProto(v.Milestone, nil),
		Locked:         is.Locked,
		LockReason:     is.LockReason,
		Pinned:         is.Pinned > 0,
		CommentCount:   int32(is.CommentCount),
		Reactions:      issueReactionGroups(ctx, users, v.Reactions, viewerID),
		Blocked:        v.Blocked,
		TrackedSeconds: v.Tracked,
		OpenSeconds:    v.OpenSeconds,
		Subscribed:     v.Subscribed,
		CreatedAt:      timestamppb.New(is.CreatedAt),
		UpdatedAt:      timestamppb.New(is.UpdatedAt),
	}
	if out.Author == nil {
		out.Author = &v1.IssueUser{Username: "ghost"}
	}
	if v.Repo != nil {
		out.RepoFullName = v.Repo.Namespace + "/" + v.Repo.Name
		out.HtmlUrl = fm.IssueURL(ctx, v.Repo, is)
	}
	for _, l := range v.Labels {
		out.Labels = append(out.Labels, issueLabelProto(l, nil))
	}
	for _, u := range v.Assignees {
		out.Assignees = append(out.Assignees, issueUserProto(u))
	}
	if is.DueDate != nil {
		out.DueDate = timestamppb.New(*is.DueDate)
	}
	if is.ClosedAt != nil {
		out.ClosedAt = timestamppb.New(*is.ClosedAt)
	}
	if v.ClosedBy != nil {
		out.ClosedBy = issueUserProto(v.ClosedBy)
	}
	if v.Pull != nil {
		out.Pull = &v1.IssuePullSummary{HeadBranch: v.Pull.HeadBranch, BaseBranch: v.Pull.BaseBranch, Draft: v.Pull.Draft, Merged: v.Pull.MergedAt != nil, MergeableState: v.Pull.MergeableState}
		if v.HeadRepo != nil {
			out.Pull.HeadRepoFullName = v.HeadRepo.Namespace + "/" + v.HeadRepo.Name
		}
	}
	if v.Project != nil {
		out.ProjectId, out.ProjectColumnId = v.Project.ProjectID, v.Project.ColumnID
	}
	return out
}

func commentTypeProto(kind string) v1.CommentType {
	name := "COMMENT_TYPE_" + strings.ToUpper(kind)
	if val, ok := v1.CommentType_value[name]; ok {
		return v1.CommentType(val)
	}
	switch kind {
	case forge.EventProjectAdded, forge.EventProjectMoved, forge.EventProjectRemoved:
		return v1.CommentType_COMMENT_TYPE_PROJECT_CHANGED
	case forge.EventReviewed:
		return v1.CommentType_COMMENT_TYPE_REVIEW
	case forge.EventReviewRemoved:
		return v1.CommentType_COMMENT_TYPE_REVIEW_REQUEST_REMOVED
	case forge.EventBranchDeleted, forge.EventBranchRestored:
		return v1.CommentType_COMMENT_TYPE_REFERENCED
	}
	return v1.CommentType_COMMENT_TYPE_UNSPECIFIED
}

func issueCommentProto(ctx context.Context, users *gitUserCache, c *storage.GitComment, reactions []*storage.GitReaction, viewerID string) *v1.Comment {
	out := &v1.Comment{
		Id:        c.ID,
		Type:      commentTypeProto(c.Type),
		Author:    issueUserProto(users.user(ctx, c.AuthorID)),
		Body:      c.Body,
		Payload:   c.Payload,
		Reactions: issueReactionGroups(ctx, users, reactions, viewerID),
		Edited:    c.EditedAt != nil,
		ViaEmail:  c.ViaEmail,
		CreatedAt: timestamppb.New(c.CreatedAt),
		UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
	if c.ReviewID != nil {
		out.ReviewId = *c.ReviewID
	}
	if out.Author == nil {
		out.Author = &v1.IssueUser{Username: "system"}
	}
	return out
}

func issueTrackedTimeProto(ctx context.Context, users *gitUserCache, t *storage.GitTrackedTime) *v1.TrackedTime {
	return &v1.TrackedTime{Id: t.ID, User: issueUserProto(users.user(ctx, t.UserID)), Seconds: t.Seconds, Note: t.Note, CreatedAt: timestamppb.New(t.CreatedAt)}
}

func issueRefProto(repos map[string]*storage.GitRepository, is *storage.GitIssue) *v1.IssueRef {
	out := &v1.IssueRef{Number: is.Number, Title: is.Title, State: issueStateProto(is.State), IsPull: is.IsPull}
	if r := repos[is.RepoID]; r != nil {
		out.RepoFullName = r.Namespace + "/" + r.Name
	}
	return out
}

// Repositories of a set of issues by id
func (s *IssueService) reposOf(ctx context.Context, issues []*storage.GitIssue) map[string]*storage.GitRepository {
	ids := map[string]bool{}
	for _, is := range issues {
		ids[is.RepoID] = true
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	repos, _ := s.store.GetGitRepositoriesByIDs(ctx, list)
	return repos
}

// Sort column for issue lists
func issueOrder(page *v1.PageRequest) string {
	order := strings.Fields(pageOrderBy(page, stores.IssueSortColumns, "created_at DESC"))
	if len(order) == 0 {
		return "git_issues.created_at DESC"
	}
	col := order[0]
	if col == "comments" {
		col = "comment_count"
	}
	dir := "DESC"
	if len(order) > 1 {
		dir = order[1]
	}
	return "git_issues." + col + " " + dir
}

func pageOrderBy(page *v1.PageRequest, allowed map[string]bool, def string) string {
	return pagesOrderBy(page, allowed, def)
}

// Sorted copy of a string set
func sortedStrings(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
