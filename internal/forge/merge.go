package forge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// One unmet merge rule
type Blocker struct {
	Rule    string
	Message string
}

// Merge state of a pull request with the rules it fails
type Mergeability struct {
	State     string
	Conflicts []string
	Blockers  []Blocker
	CanMerge  bool
}

// Computes the merge state, caching conflict results on the row
func (m *Manager) Mergeability(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest) (*Mergeability, error) {
	out := &Mergeability{State: pr.MergeableState}
	_ = json_unmarshal(pr.ConflictPaths, &out.Conflicts)
	switch {
	case repo.IsArchived:
		out.Blockers = append(out.Blockers, Blocker{"archived", "the repository is archived"})
	case pr.MergedAt != nil:
		out.Blockers = append(out.Blockers, Blocker{"closed", "the pull request is already merged"})
	case issue.State != "open":
		out.Blockers = append(out.Blockers, Blocker{"closed", "the pull request is closed"})
	}
	if pr.Draft {
		out.Blockers = append(out.Blockers, Blocker{"draft", "the pull request is a draft"})
	}
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	head := plumbing.NewHash(pr.HeadSHA)
	baseTip := r.BranchHash(pr.BaseBranch)
	if baseTip.IsZero() {
		out.Blockers = append(out.Blockers, Blocker{"conflict", "the base branch no longer exists"})
		out.State = MergeableConflict
		return out, nil
	}
	if _, err := r.Commit(head); err != nil {
		out.Blockers = append(out.Blockers, Blocker{"conflict", "the head commit is missing"})
		out.State = MergeableConflict
		return out, nil
	}
	mergeBase, _ := r.MergeBase(baseTip, head)
	if pr.MergeableState == MergeableUnknown || pr.MergeableState == MergeableChecking || pr.MergeableState == MergeableBlocked {
		res, err := r.MergeCommits(ctx, baseTip, head, false)
		if err != nil {
			return nil, err
		}
		out.Conflicts = out.Conflicts[:0]
		for _, c := range res.Conflicts {
			out.Conflicts = append(out.Conflicts, c.Path)
		}
		state := MergeableClean
		if len(out.Conflicts) > 0 {
			state = MergeableConflict
		}
		raw, _ := json_marshal(out.Conflicts)
		pr.MergeableState, pr.ConflictPaths = state, string(raw)
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": state, "conflict_paths": string(raw)})
	}
	if pr.MergeableState == MergeableConflict {
		out.Blockers = append(out.Blockers, Blocker{"conflict", "the branches have conflicts"})
	}
	protections, _ := m.store.ListGitBranchProtections(ctx, repo.ID)
	rule := gitrepo.MatchProtection(protections, pr.BaseBranch)
	mergerAllowed := true
	if rule != nil {
		out.Blockers = append(out.Blockers, m.ruleBlockers(ctx, r, repo, issue, pr, rule, mergeBase, baseTip, head)...)
		if ids := gitrepo.DecodeIDs(rule.AllowedMergers); len(ids) > 0 && actor.User != nil && !m.git.Access().InAllowList(ctx, actor.User, repo, ids) && !m.git.Access().InAllowList(ctx, actor.User, repo, gitrepo.DecodeIDs(rule.BypassIDs)) {
			mergerAllowed = false
		}
	}
	state := MergeableClean
	switch {
	case pr.MergeableState == MergeableConflict:
		state = MergeableConflict
	case len(out.Blockers) > 0:
		state = MergeableBlocked
	}
	out.State = state
	if state != pr.MergeableState && pr.MergeableState != MergeableConflict {
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": state})
		pr.MergeableState = state
	}
	if !mergerAllowed {
		out.Blockers = append(out.Blockers, Blocker{"merger", "you are not in the list of allowed mergers"})
	}
	out.CanMerge = actor.CanWrite && state == MergeableClean && mergerAllowed
	return out, nil
}

// Blockers from one protection rule
func (m *Manager) ruleBlockers(ctx context.Context, r *gitrepo.Repo, repo *db.GitRepository, issue *db.GitIssue, pr *db.GitPullRequest, rule *db.GitBranchProtection, mergeBase, baseTip, head plumbing.Hash) []Blocker {
	var out []Blocker
	reviews, _, _ := m.store.ListGitReviews(ctx, issue.ID, false, 0, 0)
	// The newest review per reviewer decides, a dismissed newest leaves no standing
	latest := map[string]*db.GitReview{}
	for _, rv := range reviews {
		if rv.Imported || rv.State == "commented" || rv.ReviewerID == issue.AuthorID {
			continue
		}
		latest[rv.ReviewerID] = rv
	}
	approvals := map[string]bool{}
	changes := 0
	for id, rv := range latest {
		if rv.Dismissed || !m.reviewerHasWeight(ctx, repo, id) {
			continue
		}
		switch rv.State {
		case "approved":
			approvals[id] = true
		case "changes_requested":
			changes++
		}
	}
	if rule.RequiredApprovals > 0 && len(approvals) < rule.RequiredApprovals {
		out = append(out, Blocker{"approvals", fmt.Sprintf("%d of %d required approvals", len(approvals), rule.RequiredApprovals)})
	}
	if changes > 0 {
		out = append(out, Blocker{"changes_requested", "a reviewer requested changes"})
	}
	if rule.RequireCodeOwnerReview {
		owners := m.ownersForPull(ctx, repo, pr)
		for _, group := range owners {
			// An owner entry that names nobody cannot be satisfied, it never passes by accident
			if len(group.UserIDs) == 0 {
				out = append(out, Blocker{"code_owners", "code owners for " + group.Pattern + " resolve to no user who can approve"})
				break
			}
			satisfied := false
			for _, id := range group.UserIDs {
				if approvals[id] {
					satisfied = true
				}
			}
			if !satisfied {
				out = append(out, Blocker{"code_owners", "code owner approval missing for " + group.Pattern})
				break
			}
		}
	}
	if rule.RequireStatusChecks {
		statuses, _ := m.store.LatestGitCommitStatuses(ctx, repo.ID, pr.HeadSHA)
		byContext := map[string]string{}
		for _, st := range statuses {
			byContext[st.Context] = st.State
		}
		required := gitrepo.DecodeIDs(rule.StatusCheckContexts)
		if len(required) == 0 {
			// No named checks means every reported check, and something must have reported
			if len(byContext) == 0 {
				out = append(out, Blocker{"status_checks", "no status check has reported on the head commit yet"})
			}
			for c := range byContext {
				required = append(required, c)
			}
		}
		for _, c := range required {
			if byContext[c] != "success" {
				out = append(out, Blocker{"status_checks", "status check " + c + " has not passed"})
				break
			}
		}
	}
	if rule.RequireSignedCommits || rule.RequireLinearHistory {
		commits, truncated, _ := r.CommitsBetween(ctx, mergeBase, head, 1000)
		if truncated {
			out = append(out, Blocker{"history", "more than 1000 commits cannot be verified, split the pull request"})
		}
		for _, c := range commits {
			if rule.RequireLinearHistory && c.NumParents() > 1 {
				out = append(out, Blocker{"linear_history", "commit " + c.Hash.String()[:8] + " is a merge commit"})
				break
			}
			if rule.RequireSignedCommits {
				v := m.git.Signing().VerifyCommit(ctx, c)
				if v.State != 2 && v.State != 4 {
					out = append(out, Blocker{"signed_commits", "commit " + c.Hash.String()[:8] + " is not signed by a verified key"})
					break
				}
			}
		}
	}
	if rule.RequireUpToDate && mergeBase != baseTip {
		out = append(out, Blocker{"up_to_date", "the head branch is behind the base branch"})
	}
	return out
}

// Reviewers whose approvals count hold write on the pulls unit
func (m *Manager) reviewerHasWeight(ctx context.Context, repo *db.GitRepository, userID string) bool {
	u, _ := m.store.GetUserByID(ctx, userID)
	if u == nil {
		return false
	}
	return m.git.Access().Level(ctx, m.principal(u), repo, db.GitUnitPulls) >= gitrepo.LevelWrite
}

// Request to merge a pull request
type MergeRequest struct {
	Method       string
	Title        string
	Message      string
	DeleteBranch bool
	ExpectedHead string
}

// Merges a pull request through the chosen method
func (m *Manager) Merge(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, req MergeRequest) (plumbing.Hash, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return plumbing.ZeroHash, ErrForbidden
	}
	if req.ExpectedHead != "" && req.ExpectedHead != pr.HeadSHA {
		return plumbing.ZeroHash, fmt.Errorf("%w: the head moved to %s", gitrepo.ErrRefChanged, pr.HeadSHA[:8])
	}
	allowed, def := m.allowedMethods(repo)
	method := req.Method
	if method == "" {
		method = def
	}
	if !contains(allowed, method) {
		return plumbing.ZeroHash, fmt.Errorf("%w: merge method %s is not allowed on this repository", ErrInvalid, method)
	}
	mg, err := m.Mergeability(ctx, repo, actor, issue, pr)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if !mg.CanMerge {
		if !actor.CanWrite {
			return plumbing.ZeroHash, ErrForbidden
		}
		msgs := make([]string, 0, len(mg.Blockers))
		for _, b := range mg.Blockers {
			msgs = append(msgs, b.Message)
		}
		if len(msgs) == 0 {
			msgs = append(msgs, "the pull request cannot be merged")
		}
		return plumbing.ZeroHash, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(msgs, ", "))
	}
	protections, _ := m.store.ListGitBranchProtections(ctx, repo.ID)
	if rule := gitrepo.MatchProtection(protections, pr.BaseBranch); rule != nil && rule.RequireLinearHistory && (method == MergeMethodMerge || method == MergeMethodRebaseMerge) {
		return plumbing.ZeroHash, fmt.Errorf("%w: the base branch requires a linear history, use squash, rebase, or fast forward", ErrInvalid)
	}
	headRepo, _ := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	head := plumbing.NewHash(pr.HeadSHA)
	mergeBase := plumbing.ZeroHash
	if pr.MergeBaseSHA != "" {
		mergeBase = plumbing.NewHash(pr.MergeBaseSHA)
	}
	committer := m.git.Signature(ctx, actor.User)
	headRef := pr.HeadBranch
	if headRepo != nil && headRepo.ID != repo.ID {
		headRef = headRepo.Namespace + "/" + headRef
	}
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Message)
	message := ""
	switch method {
	case MergeMethodMerge, MergeMethodRebaseMerge:
		if title == "" {
			title = fmt.Sprintf("Merge pull request #%d from %s", issue.Number, headRef)
		}
		if body == "" {
			body = issue.Title
		}
		message = title + "\n\n" + body + "\n"
	case MergeMethodSquash:
		if title == "" {
			title = fmt.Sprintf("%s (#%d)", issue.Title, issue.Number)
		}
		if body == "" {
			body = m.squashBody(ctx, repo, pr)
		}
		message = title + "\n\n" + body + "\n"
	case MergeMethodRebase, MergeMethodFastForward:
		message = fmt.Sprintf("Merge pull request #%d", issue.Number)
	}
	author := committer
	if u, _ := m.store.GetUserByID(ctx, issue.AuthorID); u != nil {
		author = m.git.Signature(ctx, m.principal(u))
	}
	build := func(r *gitrepo.Repo, baseTip plumbing.Hash) (plumbing.Hash, error) {
		switch method {
		case MergeMethodFastForward:
			if ok, _ := r.IsAncestor(baseTip, head); !ok {
				return plumbing.ZeroHash, gitrepo.ErrNotFastForward
			}
			return head, nil
		case MergeMethodMerge:
			res, err := r.MergeCommits(ctx, baseTip, head, true)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			if !res.Clean() {
				return plumbing.ZeroHash, conflictError(res)
			}
			return r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: res.Tree, Parents: []plumbing.Hash{baseTip, head}, Message: message, Author: committer, Committer: committer, Signer: m.git.Signer(ctx)})
		case MergeMethodSquash:
			res, err := r.MergeCommits(ctx, baseTip, head, true)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			if !res.Clean() {
				return plumbing.ZeroHash, conflictError(res)
			}
			return r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: res.Tree, Parents: []plumbing.Hash{baseTip}, Message: message, Author: author, Committer: committer, Signer: m.git.Signer(ctx)})
		case MergeMethodRebase, MergeMethodRebaseMerge:
			rebased, err := m.replayCommits(ctx, r, committer, mergeBase, head, baseTip)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			if method == MergeMethodRebase {
				return rebased, nil
			}
			tree, err := r.TreeOf(rebased)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			return r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: tree, Parents: []plumbing.Hash{baseTip, rebased}, Message: message, Author: committer, Committer: committer, Signer: m.git.Signer(ctx)})
		}
		return plumbing.ZeroHash, fmt.Errorf("%w: unknown merge method", ErrInvalid)
	}
	sha, err := m.git.CommitToBranch(ctx, repo, actor.User, gitrepo.CommitRequest{
		Branch: pr.BaseBranch, Message: message, Build: build,
		Options: gitrepo.HookOptions{BypassPullRequest: true, BypassSignedCommits: true},
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	now := time.Now().UTC()
	pr.MergedAt, pr.MergedBy, pr.MergeCommitSHA, pr.MergeMethod = &now, actor.User.ID, sha.String(), method
	pr.AutoMergeMethod, pr.AutoMergeBy = "", ""
	if err := m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{
		"merged_at": now, "merged_by": actor.User.ID, "merge_commit_sha": sha.String(), "merge_method": method,
		"auto_merge_method": "", "auto_merge_by": "", "mergeable_state": MergeableClean,
	}); err != nil {
		return sha, err
	}
	issue.State, issue.ClosedAt, issue.ClosedBy = "closed", &now, actor.User.ID
	_ = m.store.UpdateGitIssueColumns(ctx, issue.ID, map[string]any{"state": "closed", "closed_at": now, "closed_by": actor.User.ID, "updated_at": now})
	_, _ = m.AddEvent(ctx, issue, actor.User, EventMerged, map[string]string{"sha": sha.String(), "method": method, "base": pr.BaseBranch})
	m.notifyIssue(ctx, repo, issue, actor, "merged", issue.Title, nil)
	m.dispatchIssue(ctx, repo, issue, actor, "merged", nil)
	m.Trigger(ctx, repo, "pull_request", "closed", "refs/heads/"+pr.BaseBranch, sha.String(), actor.User, m.pullPayloadMap(ctx, repo, issue, pr))
	m.git.Audit(ctx, auditEvent(actor, "Git/merge", fmt.Sprintf("%s/%s#%d %s %s", repo.Namespace, repo.Name, issue.Number, method, sha.String())))
	m.closeReferencedIssues(ctx, repo, actor, issue, sha.String())
	if req.DeleteBranch || gitrepo.MergeSettingsOf(repo).DeleteBranchOnMerge {
		if err := m.DeleteHeadBranch(ctx, repo, actor, issue, pr); err != nil {
			m.log.Debug("delete head branch after merge #%d: %v", issue.Number, err)
		}
	}
	return sha, nil
}

// Closes issues the pull request body says it fixes
func (m *Manager) closeReferencedIssues(ctx context.Context, repo *db.GitRepository, actor Actor, pull *db.GitIssue, sha string) {
	if pr, _ := m.store.GetGitPullRequest(ctx, pull.ID); pr != nil && pr.BaseBranch != repo.DefaultBranch {
		return
	}
	for _, ref := range ParseReferences(pull.Body) {
		if !ref.Closes {
			continue
		}
		target, issue := m.resolveReference(ctx, repo, actor, ref)
		if issue == nil || issue.IsPull || issue.State != "open" || target.ID != repo.ID {
			continue
		}
		if err := m.setState(ctx, target, actor, issue, nil, "closed", sha); err != nil {
			m.log.Debug("closing #%d from merge: %v", issue.Number, err)
		}
	}
}

// Squash commit body listing the squashed commits
func (m *Manager) squashBody(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest) string {
	commits, err := m.PullCommits(ctx, repo, pr, 200)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range commits {
		first := strings.SplitN(strings.TrimSpace(c.Message), "\n", 2)[0]
		fmt.Fprintf(&b, "* %s\n", first)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Replays commits between base and head onto a new parent, oldest first
func (m *Manager) replayCommits(ctx context.Context, r *gitrepo.Repo, committer object.Signature, mergeBase, head, onto plumbing.Hash) (plumbing.Hash, error) {
	commits, truncated, err := r.CommitsBetween(ctx, mergeBase, head, 1000)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if truncated {
		return plumbing.ZeroHash, fmt.Errorf("%w: the pull request has more than 1000 commits, rebase it locally or merge instead", ErrInvalid)
	}
	cur := onto
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		if c.NumParents() > 1 {
			return plumbing.ZeroHash, fmt.Errorf("%w: rebase cannot replay merge commit %s", ErrInvalid, c.Hash.String()[:8])
		}
		parent := plumbing.ZeroHash
		if c.NumParents() == 1 {
			parent = c.ParentHashes[0]
		}
		res, err := r.Merge3(ctx, parent, cur, c.Hash, true)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if !res.Clean() {
			return plumbing.ZeroHash, conflictError(res)
		}
		curTree, err := r.TreeOf(cur)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if res.Tree == curTree {
			continue
		}
		cur, err = r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: res.Tree, Parents: []plumbing.Hash{cur}, Message: c.Message, Author: c.Author, Committer: committer, Signer: m.git.Signer(ctx)})
		if err != nil {
			return plumbing.ZeroHash, err
		}
	}
	return cur, nil
}

// Rebases a head onto a new base inside one repository
func (m *Manager) rebaseOnto(ctx context.Context, repo *db.GitRepository, actor Actor, mergeBase, head, onto plumbing.Hash) (plumbing.Hash, error) {
	lock := m.git.Engine().LockRepo(repo.ID)
	defer lock.Unlock()
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer r.Close()
	return m.replayCommits(ctx, r, m.git.Signature(ctx, actor.User), mergeBase, head, onto)
}
