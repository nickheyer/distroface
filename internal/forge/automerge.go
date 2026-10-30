package forge

import (
	"context"
	"fmt"

	"github.com/nickheyer/distroface/internal/db"
)

// Arms an automatic merge once every rule passes
func (m *Manager) ScheduleAutoMerge(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, method, title, message string, deleteBranch bool) error {
	if actor.User == nil || actor.User.IsAnonymous() || !actor.CanWrite {
		return ErrForbidden
	}
	if issue.State != "open" || pr.MergedAt != nil {
		return fmt.Errorf("%w: pull request is not open", ErrInvalid)
	}
	allowed, def := m.allowedMethods(repo)
	if method == "" {
		method = def
	}
	if !contains(allowed, method) {
		return fmt.Errorf("%w: merge method %s is not allowed on this repository", ErrInvalid, method)
	}
	pr.AutoMergeMethod, pr.AutoMergeBy, pr.AutoMergeTitle, pr.AutoMergeMessage, pr.AutoMergeDelete = method, actor.User.ID, title, message, deleteBranch
	if err := m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{
		"auto_merge_method": method, "auto_merge_by": actor.User.ID, "auto_merge_title": title, "auto_merge_message": message, "auto_merge_delete": deleteBranch,
	}); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventAutoMergeArmed, map[string]string{"method": method})
	go m.RetryAutoMerge(context.WithoutCancel(ctx), repo, pr.HeadSHA)
	return nil
}

// Disarms an automatic merge
func (m *Manager) CancelAutoMerge(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if pr.AutoMergeBy != actor.User.ID && !actor.CanWrite {
		return ErrForbidden
	}
	if pr.AutoMergeMethod == "" {
		return nil
	}
	pr.AutoMergeMethod, pr.AutoMergeBy = "", ""
	if err := m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"auto_merge_method": "", "auto_merge_by": ""}); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventAutoMergeCancelled, nil)
	return nil
}

// Merges armed pull requests whose head is the given sha when every rule passes
func (m *Manager) RetryAutoMerge(ctx context.Context, repo *db.GitRepository, sha string) {
	pulls, err := m.store.ListAutoMergePulls(ctx, repo.ID)
	if err != nil {
		return
	}
	for _, pr := range pulls {
		if sha != "" && pr.HeadSHA != sha {
			continue
		}
		issue, err := m.store.GetGitIssueByID(ctx, pr.IssueID)
		if err != nil || issue == nil || issue.State != "open" {
			continue
		}
		u, _ := m.store.GetUserByID(ctx, pr.AutoMergeBy)
		if u == nil {
			continue
		}
		principal := m.principal(u)
		levels := m.git.Access().Levels(ctx, principal, repo)
		actor := Actor{User: principal, CanWrite: levels.Unit(db.GitUnitPulls) >= 2, CanAdmin: levels.CanAdmin || levels.IsOwner}
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": MergeableUnknown})
		pr.MergeableState = MergeableUnknown
		mg, err := m.Mergeability(ctx, repo, actor, issue, pr)
		if err != nil || !mg.CanMerge {
			continue
		}
		if _, err := m.Merge(ctx, repo, actor, issue, pr, MergeRequest{Method: pr.AutoMergeMethod, Title: pr.AutoMergeTitle, Message: pr.AutoMergeMessage, DeleteBranch: pr.AutoMergeDelete}); err != nil {
			m.log.Debug("auto merge !%d: %v", issue.Number, err)
		}
	}
}
