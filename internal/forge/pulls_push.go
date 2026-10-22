package forge

import (
	"context"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Marks pull requests whose head or base moved, stats refresh later
func (m *Manager) onPushPulls(ctx context.Context, ev gitrepo.RefEvent) {
	repo := ev.Repo
	actor := Actor{User: ev.Actor, CanWrite: true}
	// Forks host heads of pull requests opened on their parent
	var parent *db.GitRepository
	if repo.ForkOfID != nil {
		parent, _ = m.store.GetGitRepositoryByID(ctx, *repo.ForkOfID)
	}
	stale := map[*db.GitRepository][]string{}
	retry := false
	for _, u := range ev.Updates {
		if !u.Name.IsBranch() {
			continue
		}
		branch := u.Name.Short()
		heads, _ := m.store.ListOpenPullsByHead(ctx, repo.ID, branch)
		if len(heads) > 0 {
			ids := make([]string, 0, len(heads))
			for _, pr := range heads {
				ids = append(ids, pr.IssueID)
			}
			issues := map[string]*db.GitIssue{}
			if rows, _ := m.store.GetGitIssuesByIDs(ctx, ids); rows != nil {
				for _, is := range rows {
					issues[is.ID] = is
				}
			}
			for _, pr := range heads {
				issue := issues[pr.IssueID]
				if issue == nil {
					continue
				}
				base := repo
				if issue.RepoID != repo.ID {
					if parent == nil || issue.RepoID != parent.ID {
						continue
					}
					base = parent
				}
				m.headMoved(ctx, base, actor, issue, pr, u)
				if !u.New.IsZero() {
					stale[base] = append(stale[base], pr.IssueID)
				}
			}
		}
		if u.New.IsZero() {
			continue
		}
		bases, _ := m.store.ListOpenPullsByBase(ctx, repo.ID, branch)
		for _, pr := range bases {
			m.markPullStale(ctx, pr)
			stale[repo] = append(stale[repo], pr.IssueID)
		}
		retry = true
	}
	for base, ids := range stale {
		m.queuePullStats(base, ids)
	}
	if retry {
		go m.RetryAutoMerge(context.WithoutCancel(ctx), repo, "")
	}
}

// Records a head push on one pull request
func (m *Manager) headMoved(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, u gitrepo.RefUpdate) {
	if u.New.IsZero() {
		pr.HeadDeleted = true
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_deleted": true})
		_, _ = m.AddEvent(ctx, issue, actor.User, EventBranchDeleted, map[string]string{"branch": pr.HeadBranch})
		return
	}
	oldSHA := pr.HeadSHA
	pr.HeadSHA = u.New.String()
	cols := staleMergeColumns(pr)
	cols["head_sha"], cols["head_deleted"] = pr.HeadSHA, false
	// New commits from anyone but the person who armed it disarm auto merge
	if pr.AutoMergeMethod != "" && (actor.User == nil || actor.User.ID != pr.AutoMergeBy) {
		pr.AutoMergeMethod, pr.AutoMergeBy = "", ""
		cols["auto_merge_method"], cols["auto_merge_by"] = "", ""
		_, _ = m.AddEvent(ctx, issue, actor.User, EventAutoMergeCancelled, map[string]string{"reason": "head_pushed"})
	}
	_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, cols)
	if _, err := m.refreshPullRef(ctx, repo, pr, issue.Number, nil); err != nil {
		m.log.Debug("pull ref refresh #%d: %v", issue.Number, err)
	}
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
	// Diff only when a comment could still go stale
	comments, err := m.store.ListGitReviewComments(ctx, issue.ID, "", "")
	if err != nil {
		return
	}
	var live []*db.GitReviewComment
	for _, c := range comments {
		if c.OutdatedSHA == "" {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
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
	var ids []string
	for _, c := range live {
		if changed[c.Path] {
			ids = append(ids, c.ID)
		}
	}
	_ = m.store.MarkGitReviewCommentsOutdated(ctx, ids, newSHA)
}
