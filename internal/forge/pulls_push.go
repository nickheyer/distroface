package forge

import (
	"context"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Updates open pull requests whose head or base moved
func (m *Manager) onPushPulls(ctx context.Context, ev gitrepo.RefEvent) {
	repo := ev.Repo
	actor := Actor{User: ev.Actor, CanWrite: true}
	for _, u := range ev.Updates {
		if !u.Name.IsBranch() {
			continue
		}
		branch := u.Name.Short()
		heads, _ := m.store.ListOpenPullsByHead(ctx, repo.ID, branch)
		for _, pr := range heads {
			m.headMoved(ctx, repo, actor, pr, u)
		}
		bases, _ := m.store.ListOpenPullsByBase(ctx, repo.ID, branch)
		for _, pr := range bases {
			if u.New.IsZero() {
				continue
			}
			_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": MergeableUnknown})
			pr.MergeableState = MergeableUnknown
			m.refreshPullStats(ctx, repo, pr)
		}
		if !u.New.IsZero() {
			go m.RetryAutoMerge(context.WithoutCancel(ctx), repo, "")
		}
	}
	// Forks hosting heads of pull requests on their parent
	if repo.ForkOfID != nil {
		parent, _ := m.store.GetGitRepositoryByID(ctx, *repo.ForkOfID)
		if parent != nil {
			for _, u := range ev.Updates {
				if !u.Name.IsBranch() {
					continue
				}
				heads, _ := m.store.ListOpenPullsByHead(ctx, repo.ID, u.Name.Short())
				for _, pr := range heads {
					issue, _ := m.store.GetGitIssueByID(ctx, pr.IssueID)
					if issue != nil && issue.RepoID == parent.ID {
						m.headMoved(ctx, parent, actor, pr, u)
					}
				}
			}
		}
	}
}

// Records a head push on one pull request
func (m *Manager) headMoved(ctx context.Context, repo *db.GitRepository, actor Actor, pr *db.GitPullRequest, u gitrepo.RefUpdate) {
	issue, err := m.store.GetGitIssueByID(ctx, pr.IssueID)
	if err != nil || issue == nil || issue.RepoID != repo.ID {
		return
	}
	if u.New.IsZero() {
		pr.HeadDeleted = true
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_deleted": true})
		_, _ = m.AddEvent(ctx, issue, actor.User, EventBranchDeleted, map[string]string{"branch": pr.HeadBranch})
		return
	}
	oldSHA := pr.HeadSHA
	pr.HeadSHA = u.New.String()
	cols := map[string]any{"head_sha": pr.HeadSHA, "head_deleted": false, "mergeable_state": MergeableUnknown}
	// New commits from anyone but the person who armed it disarm auto merge
	if pr.AutoMergeMethod != "" && (actor.User == nil || actor.User.ID != pr.AutoMergeBy) {
		pr.AutoMergeMethod, pr.AutoMergeBy = "", ""
		cols["auto_merge_method"], cols["auto_merge_by"] = "", ""
		_, _ = m.AddEvent(ctx, issue, actor.User, EventAutoMergeCancelled, map[string]string{"reason": "head_pushed"})
	}
	_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, cols)
	pr.MergeableState = MergeableUnknown
	if _, err := m.refreshPullRef(ctx, repo, pr, issue.Number, nil); err != nil {
		m.log.Debug("pull ref refresh #%d: %v", issue.Number, err)
	}
	m.refreshPullStats(ctx, repo, pr)
	kind := EventHeadPushed
	if u.Forced {
		kind = EventForcePushed
	}
	count := 0
	if r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false); err == nil {
		old := plumbing.NewHash(oldSHA)
		if u.Forced {
			old = plumbing.ZeroHash
			if pr.MergeBaseSHA != "" {
				old = plumbing.NewHash(pr.MergeBaseSHA)
			}
		}
		if commits, _, err := r.CommitsBetween(ctx, old, u.New, 1000); err == nil {
			count = len(commits)
		}
		m.markOutdatedComments(ctx, r, issue, oldSHA, pr.HeadSHA)
		r.Close()
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, kind, map[string]any{"before": oldSHA, "after": pr.HeadSHA, "commits": count})
	protections, _ := m.store.ListGitBranchProtections(ctx, repo.ID)
	if rule := gitrepo.MatchProtection(protections, pr.BaseBranch); rule != nil && rule.DismissStaleApprovals {
		if n, _ := m.store.DismissStaleGitApprovals(ctx, issue.ID, pr.HeadSHA); n > 0 {
			_, _ = m.AddEvent(ctx, issue, actor.User, "review_dismissed", map[string]any{"stale": true, "count": n})
		}
	}
	m.dispatchIssue(ctx, repo, issue, actor, "synchronize", nil)
	m.Trigger(ctx, repo, "pull_request", "synchronize", PullRef(issue.Number).String(), pr.HeadSHA, actor.User, m.pullPayloadMap(ctx, repo, issue, pr))
}

// Marks inline comments outdated when their file changed between heads
func (m *Manager) markOutdatedComments(ctx context.Context, r *gitrepo.Repo, issue *db.GitIssue, oldSHA, newSHA string) {
	if oldSHA == "" || oldSHA == newSHA {
		return
	}
	diff, err := r.Diff(ctx, plumbing.NewHash(oldSHA), plumbing.NewHash(newSHA), gitrepo.DiffOptions{})
	if err != nil {
		return
	}
	changed := map[string]bool{}
	for _, f := range diff.Files {
		changed[f.OldPath] = true
		changed[f.NewPath] = true
	}
	comments, err := m.store.ListGitReviewComments(ctx, issue.ID, "", "")
	if err != nil {
		return
	}
	var ids []string
	for _, c := range comments {
		if c.OutdatedSHA == "" && changed[c.Path] {
			ids = append(ids, c.ID)
		}
	}
	_ = m.store.MarkGitReviewCommentsOutdated(ctx, ids, newSHA)
}
