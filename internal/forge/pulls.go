package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/webhook"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Merge method names stored on rows
const (
	MergeMethodMerge       = "merge"
	MergeMethodSquash      = "squash"
	MergeMethodRebase      = "rebase"
	MergeMethodRebaseMerge = "rebase_merge"
	MergeMethodFastForward = "fast_forward"
)

// Mergeable states stored on rows
const (
	MergeableUnknown  = "unknown"
	MergeableClean    = "clean"
	MergeableConflict = "conflict"
	MergeableBlocked  = "blocked"
	MergeableChecking = "checking"
)

// Fields of a new pull request
type NewPull struct {
	Base                 string
	Head                 string
	Title                string
	Body                 string
	Draft                bool
	LabelIDs             []string
	MilestoneID          string
	AssigneeIDs          []string
	ReviewerIDs          []string
	TeamReviewerIDs      []string
	AllowMaintainerEdits bool
}

// Pull request with everything the detail view shows
type PullView struct {
	*IssueView
	PR          *db.GitPullRequest
	Requests    []*db.GitReviewRequest
	Reviews     []*db.GitReview
	ReviewCount map[string]int32
	Statuses    []*db.GitCommitStatus
	Merge       *Mergeability
	Allowed     []string
	Default     string
}

// Reference name reviewers fetch
func PullRef(number int64) plumbing.ReferenceName {
	return plumbing.ReferenceName(fmt.Sprintf("refs/pull/%d/head", number))
}

// Loads the pull request half of an issue
func (m *Manager) Pull(ctx context.Context, issue *db.GitIssue) (*db.GitPullRequest, error) {
	if !issue.IsPull {
		return nil, fmt.Errorf("%w: #%d is not a pull request", ErrNotFound, issue.Number)
	}
	pr, err := m.store.GetGitPullRequest(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	if pr == nil {
		return nil, fmt.Errorf("%w: pull request #%d", ErrNotFound, issue.Number)
	}
	return pr, nil
}

// Resolves a head reference to a repository and branch
func (m *Manager) resolveHead(ctx context.Context, repo *db.GitRepository, actor Actor, head string) (*db.GitRepository, string, error) {
	headRepo, branch := repo, head
	if ns, rest, ok := strings.Cut(head, ":"); ok && strings.Contains(ns, "/") {
		owner, name, _ := strings.Cut(ns, "/")
		other, err := m.store.GetGitRepository(ctx, owner, name)
		if err != nil {
			return nil, "", err
		}
		if other == nil || !m.git.Access().CanSee(ctx, actor.User, other) {
			return nil, "", fmt.Errorf("%w: head repository %s", ErrNotFound, ns)
		}
		if other.ID != repo.ID && (other.ForkOfID == nil || *other.ForkOfID != repo.ID) && (repo.ForkOfID == nil || *repo.ForkOfID != other.ID) {
			return nil, "", fmt.Errorf("%w: head repository must be a fork of the base repository", ErrInvalid)
		}
		headRepo, branch = other, rest
	}
	if branch == "" {
		return nil, "", fmt.Errorf("%w: a head branch is required", ErrInvalid)
	}
	return headRepo, branch, nil
}

// Tip of a branch in a repository, zero when missing
func (m *Manager) branchTip(repo *db.GitRepository, branch string) plumbing.Hash {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return plumbing.ZeroHash
	}
	defer r.Close()
	return r.BranchHash(branch)
}

// Writes refs/pull/n/head to the head commit, fetching across forks
func (m *Manager) refreshPullRef(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest, number int64, headRepo *db.GitRepository) (plumbing.Hash, error) {
	if headRepo == nil {
		var err error
		if headRepo, err = m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID); err != nil || headRepo == nil {
			return plumbing.ZeroHash, fmt.Errorf("%w: head repository", ErrNotFound)
		}
	}
	tip := m.branchTip(headRepo, pr.HeadBranch)
	if tip.IsZero() {
		return plumbing.ZeroHash, nil
	}
	ref := PullRef(number)
	if headRepo.ID != repo.ID {
		if err := m.git.FetchLocal(ctx, repo, headRepo, false, "+refs/heads/"+pr.HeadBranch+":"+ref.String()); err != nil {
			return plumbing.ZeroHash, err
		}
		return tip, nil
	}
	unlock := m.git.Engine().LockRepo(repo.ID)
	defer unlock()
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer r.Close()
	current, _ := r.Resolve(ref.String())
	if current == tip {
		return tip, nil
	}
	return tip, r.UpdateRef(ref, current, tip)
}

// Opens a pull request
func (m *Manager) CreatePull(ctx context.Context, repo *db.GitRepository, actor Actor, in NewPull) (*db.GitIssue, *db.GitPullRequest, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return nil, nil, fmt.Errorf("%w: sign in to open pull requests", ErrForbidden)
	}
	if repo.IsArchived {
		return nil, nil, gitrepo.ErrArchived
	}
	if !gitrepo.UnitEnabled(repo, db.GitUnitPulls) {
		return nil, nil, fmt.Errorf("%w: pull requests are disabled on this repository", ErrForbidden)
	}
	if !validTitle(in.Title) {
		return nil, nil, fmt.Errorf("%w: a title is required", ErrInvalid)
	}
	base := in.Base
	if base == "" {
		base = repo.DefaultBranch
	}
	baseTip := m.branchTip(repo, base)
	if baseTip.IsZero() {
		return nil, nil, fmt.Errorf("%w: base branch %q does not exist", ErrInvalid, base)
	}
	headRepo, headBranch, err := m.resolveHead(ctx, repo, actor, in.Head)
	if err != nil {
		return nil, nil, err
	}
	headTip := m.branchTip(headRepo, headBranch)
	if headTip.IsZero() {
		return nil, nil, fmt.Errorf("%w: head branch %q does not exist", ErrInvalid, headBranch)
	}
	if headRepo.ID == repo.ID && headBranch == base {
		return nil, nil, fmt.Errorf("%w: head and base are the same branch", ErrInvalid)
	}
	if existing, _ := m.store.FindOpenPull(ctx, repo.ID, headRepo.ID, headBranch, base); existing != nil {
		return nil, nil, fmt.Errorf("%w: pull request #%d already proposes these changes", ErrConflict, existing.Number)
	}
	if !actor.CanWrite {
		in.LabelIDs, in.AssigneeIDs, in.MilestoneID = nil, nil, ""
	}
	number, err := m.store.NextGitCounter(ctx, repo.ID, "issue_counter")
	if err != nil {
		return nil, nil, err
	}
	issue := &db.GitIssue{ID: uuid.New().String(), RepoID: repo.ID, Number: number, IsPull: true, Title: strings.TrimSpace(in.Title), Body: in.Body, State: "open", AuthorID: actor.User.ID}
	if in.MilestoneID != "" {
		if ms, _ := m.store.GetGitMilestone(ctx, repo.ID, in.MilestoneID); ms != nil {
			issue.MilestoneID = &ms.ID
		}
	}
	if err := m.store.CreateGitIssue(ctx, issue); err != nil {
		return nil, nil, err
	}
	pr := &db.GitPullRequest{
		IssueID: issue.ID, HeadRepoID: headRepo.ID, HeadBranch: headBranch, HeadSHA: headTip.String(), BaseBranch: base,
		Draft: in.Draft, MergeableState: MergeableUnknown, ConflictPaths: "[]", AllowMaintainerEdits: in.AllowMaintainerEdits || headRepo.ID == repo.ID,
	}
	if err := m.store.CreateGitPullRequest(ctx, pr); err != nil {
		_ = m.store.DeleteGitIssue(ctx, issue.ID)
		return nil, nil, err
	}
	if _, err := m.refreshPullRef(ctx, repo, pr, number, headRepo); err != nil {
		m.log.Error("pull ref for #%d: %v", number, err)
	}
	m.refreshPullStats(ctx, repo, pr)
	if len(in.LabelIDs) > 0 {
		labels, _ := m.validLabels(ctx, repo, in.LabelIDs)
		_ = m.store.SetGitIssueLabels(ctx, issue.ID, labelIDs(labels))
	}
	if len(in.AssigneeIDs) > 0 {
		_ = m.store.SetGitIssueAssignees(ctx, issue.ID, m.validAssignees(ctx, repo, in.AssigneeIDs))
	}
	_ = m.store.SetGitIssueSubscription(ctx, issue.ID, actor.User.ID, true)
	m.recordReferences(ctx, repo, issue, actor, issue.Body)
	users, teams := m.codeownerReviewers(ctx, repo, pr)
	_ = m.RequestReviewers(ctx, repo, actor, issue, append(in.ReviewerIDs, users...), append(in.TeamReviewerIDs, teams...), true)
	m.notifyIssue(ctx, repo, issue, actor, "opened", issue.Title, mentions(issue.Body))
	m.dispatchIssue(ctx, repo, issue, actor, "opened", nil)
	m.Trigger(ctx, repo, "pull_request", "opened", "refs/pull/"+fmt.Sprint(number)+"/head", pr.HeadSHA, actor.User, m.pullPayloadMap(ctx, repo, issue, pr))
	return issue, pr, nil
}

// Recomputes merge base, counts, and resets the merge state
func (m *Manager) refreshPullStats(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	defer r.Close()
	head := plumbing.NewHash(pr.HeadSHA)
	baseTip := r.BranchHash(pr.BaseBranch)
	cols := map[string]any{"mergeable_state": MergeableUnknown, "conflict_paths": "[]"}
	pr.MergeableState, pr.ConflictPaths = MergeableUnknown, "[]"
	if _, err := r.Commit(head); err != nil || baseTip.IsZero() {
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, cols)
		return
	}
	mergeBase, err := r.MergeBase(baseTip, head)
	if err != nil {
		mergeBase = plumbing.ZeroHash
	}
	pr.MergeBaseSHA = mergeBase.String()
	if mergeBase.IsZero() {
		pr.MergeBaseSHA = ""
	}
	cols["merge_base_sha"] = pr.MergeBaseSHA
	commits, _ := r.CommitRange(ctx, mergeBase, head)
	pr.CommitCount = len(commits)
	cols["commit_count"] = pr.CommitCount
	if diff, err := r.Diff(ctx, mergeBase, head, gitrepo.DiffOptions{}); err == nil {
		pr.ChangedFiles, pr.Additions, pr.Deletions = int(diff.TotalFiles), int(diff.Additions), int(diff.Deletions)
		cols["changed_files"], cols["additions"], cols["deletions"] = pr.ChangedFiles, pr.Additions, pr.Deletions
	}
	_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, cols)
}

// Loads the view rows of a pull request, computing mergeability when asked
func (m *Manager) PullView(ctx context.Context, actor Actor, repo *db.GitRepository, issue *db.GitIssue, pr *db.GitPullRequest, withMerge bool) (*PullView, error) {
	iv, err := m.IssueView(ctx, actor.User, repo, issue)
	if err != nil {
		return nil, err
	}
	view := &PullView{IssueView: iv, PR: pr}
	view.Requests, _ = m.store.ListGitReviewRequests(ctx, issue.ID)
	view.Reviews, _, _ = m.store.ListGitReviews(ctx, issue.ID, false, 0, 0)
	ids := make([]string, 0, len(view.Reviews))
	for _, rv := range view.Reviews {
		ids = append(ids, rv.ID)
	}
	view.ReviewCount, _ = m.store.CountGitReviewComments(ctx, ids)
	view.Statuses, _ = m.store.LatestGitCommitStatuses(ctx, repo.ID, pr.HeadSHA)
	view.Allowed, view.Default = m.allowedMethods(repo)
	if withMerge {
		view.Merge, _ = m.Mergeability(ctx, repo, actor, issue, pr)
	}
	return view, nil
}

// Methods the repository settings allow
func (m *Manager) allowedMethods(repo *db.GitRepository) ([]string, string) {
	ms := gitrepo.MergeSettingsOf(repo)
	var out []string
	if ms.AllowMerge {
		out = append(out, MergeMethodMerge)
	}
	if ms.AllowSquash {
		out = append(out, MergeMethodSquash)
	}
	if ms.AllowRebase {
		out = append(out, MergeMethodRebase)
	}
	if ms.AllowRebaseMerge {
		out = append(out, MergeMethodRebaseMerge)
	}
	if ms.AllowFastForward {
		out = append(out, MergeMethodFastForward)
	}
	def := MergeMethodFromProto(ms.DefaultMethod)
	if def == "" || !contains(out, def) {
		if len(out) > 0 {
			def = out[0]
		}
	}
	return out, def
}

// Stored name of a proto merge method
func MergeMethodFromProto(p v1.GitMergeMethod) string {
	switch p {
	case v1.GitMergeMethod_GIT_MERGE_METHOD_MERGE:
		return MergeMethodMerge
	case v1.GitMergeMethod_GIT_MERGE_METHOD_SQUASH:
		return MergeMethodSquash
	case v1.GitMergeMethod_GIT_MERGE_METHOD_REBASE:
		return MergeMethodRebase
	case v1.GitMergeMethod_GIT_MERGE_METHOD_REBASE_MERGE:
		return MergeMethodRebaseMerge
	case v1.GitMergeMethod_GIT_MERGE_METHOD_FAST_FORWARD:
		return MergeMethodFastForward
	}
	return ""
}

// Proto value of a stored merge method
func MergeMethodProto(s string) v1.GitMergeMethod {
	switch s {
	case MergeMethodMerge:
		return v1.GitMergeMethod_GIT_MERGE_METHOD_MERGE
	case MergeMethodSquash:
		return v1.GitMergeMethod_GIT_MERGE_METHOD_SQUASH
	case MergeMethodRebase:
		return v1.GitMergeMethod_GIT_MERGE_METHOD_REBASE
	case MergeMethodRebaseMerge:
		return v1.GitMergeMethod_GIT_MERGE_METHOD_REBASE_MERGE
	case MergeMethodFastForward:
		return v1.GitMergeMethod_GIT_MERGE_METHOD_FAST_FORWARD
	}
	return v1.GitMergeMethod_GIT_MERGE_METHOD_UNSPECIFIED
}

// Edits to a pull request beyond its issue fields
type PullUpdate struct {
	Base                 *string
	Draft                *bool
	AllowMaintainerEdits *bool
}

// Applies pull request specific edits
func (m *Manager) UpdatePull(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, up PullUpdate) error {
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if pr.MergedAt != nil {
		return fmt.Errorf("%w: merged pull requests cannot change", ErrInvalid)
	}
	cols := map[string]any{}
	if up.Base != nil && *up.Base != pr.BaseBranch {
		if m.branchTip(repo, *up.Base).IsZero() {
			return fmt.Errorf("%w: base branch %q does not exist", ErrInvalid, *up.Base)
		}
		if pr.HeadRepoID == repo.ID && pr.HeadBranch == *up.Base {
			return fmt.Errorf("%w: head and base are the same branch", ErrInvalid)
		}
		old := pr.BaseBranch
		pr.BaseBranch = *up.Base
		cols["base_branch"] = pr.BaseBranch
		_, _ = m.AddEvent(ctx, issue, actor.User, EventBaseChanged, map[string]string{"from": old, "to": pr.BaseBranch})
	}
	if up.Draft != nil && *up.Draft != pr.Draft {
		pr.Draft = *up.Draft
		cols["draft"] = pr.Draft
		kind, action := EventReadyForReview, "ready_for_review"
		if pr.Draft {
			kind, action = EventConvertedToDraft, "converted_to_draft"
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, kind, nil)
		m.dispatchIssue(ctx, repo, issue, actor, action, nil)
		m.Trigger(ctx, repo, "pull_request", action, PullRef(issue.Number).String(), pr.HeadSHA, actor.User, m.pullPayloadMap(ctx, repo, issue, pr))
	}
	if up.AllowMaintainerEdits != nil && actor.User.ID == issue.AuthorID {
		pr.AllowMaintainerEdits = *up.AllowMaintainerEdits
		cols["allow_maintainer_edits"] = pr.AllowMaintainerEdits
	}
	if len(cols) == 0 {
		return nil
	}
	if err := m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, cols); err != nil {
		return err
	}
	if up.Base != nil {
		m.refreshPullStats(ctx, repo, pr)
	}
	return nil
}

// Closes without merging, optionally deleting the head branch
func (m *Manager) ClosePull(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, deleteBranch bool) error {
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	if pr.MergedAt != nil {
		return fmt.Errorf("%w: pull request is already merged", ErrInvalid)
	}
	if err := m.setState(ctx, repo, actor, issue, "closed", ""); err != nil {
		return err
	}
	if pr.AutoMergeMethod != "" {
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"auto_merge_method": "", "auto_merge_by": ""})
	}
	if deleteBranch {
		_ = m.DeleteHeadBranch(ctx, repo, actor, issue, pr)
	}
	return nil
}

// Reopens a closed, unmerged pull request
func (m *Manager) ReopenPull(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest) error {
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	if pr.MergedAt != nil {
		return fmt.Errorf("%w: merged pull requests cannot reopen", ErrInvalid)
	}
	headRepo, _ := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	if headRepo == nil || m.branchTip(headRepo, pr.HeadBranch).IsZero() {
		return fmt.Errorf("%w: the head branch no longer exists, restore it first", ErrInvalid)
	}
	if err := m.setState(ctx, repo, actor, issue, "open", ""); err != nil {
		return err
	}
	tip, _ := m.refreshPullRef(ctx, repo, pr, issue.Number, headRepo)
	if !tip.IsZero() {
		pr.HeadSHA = tip.String()
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_sha": pr.HeadSHA, "head_deleted": false})
	}
	m.refreshPullStats(ctx, repo, pr)
	return nil
}

// Reports whether the actor may push the head branch of a pull request
func (m *Manager) canPushHead(ctx context.Context, repo, headRepo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest) bool {
	if actor.User == nil {
		return false
	}
	if m.git.Access().Level(ctx, actor.User, headRepo, db.GitUnitCode) >= gitrepo.LevelWrite {
		return true
	}
	return headRepo.ID != repo.ID && pr.AllowMaintainerEdits && actor.CanWrite
}

// Deletes the head branch after a merge or close
func (m *Manager) DeleteHeadBranch(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest) error {
	headRepo, err := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil || headRepo == nil {
		return fmt.Errorf("%w: head repository", ErrNotFound)
	}
	if headRepo.ID == repo.ID && pr.HeadBranch == repo.DefaultBranch {
		return fmt.Errorf("%w: the default branch stays", ErrInvalid)
	}
	if !m.canPushHead(ctx, repo, headRepo, actor, issue, pr) {
		return ErrForbidden
	}
	protections, _ := m.store.ListGitBranchProtections(ctx, headRepo.ID)
	if rule := gitrepo.MatchProtection(protections, pr.HeadBranch); rule != nil && rule.BlockDeletion {
		return fmt.Errorf("%w: branch %s is protected from deletion", ErrForbidden, pr.HeadBranch)
	}
	tip := m.branchTip(headRepo, pr.HeadBranch)
	if tip.IsZero() {
		return nil
	}
	if err := m.git.UpdateRef(ctx, headRepo, actor.User, plumbing.NewBranchReferenceName(pr.HeadBranch), tip, plumbing.ZeroHash, gitrepo.HookOptions{BypassPullRequest: true}); err != nil {
		return err
	}
	pr.HeadDeleted = true
	_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_deleted": true})
	_, _ = m.AddEvent(ctx, issue, actor.User, EventBranchDeleted, map[string]string{"branch": pr.HeadBranch})
	return nil
}

// Recreates the head branch from refs/pull/n/head
func (m *Manager) RestoreHeadBranch(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest) error {
	headRepo, err := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil || headRepo == nil {
		return fmt.Errorf("%w: head repository", ErrNotFound)
	}
	if !m.canPushHead(ctx, repo, headRepo, actor, issue, pr) {
		return ErrForbidden
	}
	if !m.branchTip(headRepo, pr.HeadBranch).IsZero() {
		return fmt.Errorf("%w: branch %s already exists", ErrConflict, pr.HeadBranch)
	}
	head := plumbing.NewHash(pr.HeadSHA)
	if headRepo.ID != repo.ID {
		if err := m.git.FetchLocal(ctx, headRepo, repo, false, "+"+PullRef(issue.Number).String()+":refs/distroface/restore/"+pr.HeadSHA); err != nil {
			return err
		}
	}
	if err := m.git.UpdateRef(ctx, headRepo, actor.User, plumbing.NewBranchReferenceName(pr.HeadBranch), plumbing.ZeroHash, head, gitrepo.HookOptions{BypassPullRequest: true, BypassSignedCommits: true}); err != nil {
		return err
	}
	pr.HeadDeleted = false
	_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_deleted": false})
	_, _ = m.AddEvent(ctx, issue, actor.User, EventBranchRestored, map[string]string{"branch": pr.HeadBranch})
	return nil
}

// Brings the head up to date by merging the base in or rebasing onto it
func (m *Manager) UpdateBranch(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, rebase bool) error {
	if issue.State != "open" || pr.MergedAt != nil {
		return fmt.Errorf("%w: pull request is not open", ErrInvalid)
	}
	headRepo, err := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil || headRepo == nil {
		return fmt.Errorf("%w: head repository", ErrNotFound)
	}
	if !m.canPushHead(ctx, repo, headRepo, actor, issue, pr) {
		return ErrForbidden
	}
	baseTip := m.branchTip(repo, pr.BaseBranch)
	headTip := m.branchTip(headRepo, pr.HeadBranch)
	if baseTip.IsZero() || headTip.IsZero() {
		return fmt.Errorf("%w: a branch no longer exists", ErrInvalid)
	}
	if headRepo.ID != repo.ID {
		if err := m.git.FetchLocal(ctx, headRepo, repo, false, "+refs/heads/"+pr.BaseBranch+":refs/distroface/base/"+baseTip.String()); err != nil {
			return err
		}
	}
	r, err := m.git.Engine().OpenRepo(headRepo.Namespace, headRepo.Name, false)
	if err != nil {
		return err
	}
	if ok, _ := r.IsAncestor(baseTip, headTip); ok {
		r.Close()
		return nil
	}
	mergeBase, err := r.MergeBase(baseTip, headTip)
	r.Close()
	if err != nil {
		return err
	}
	author := m.git.Signature(ctx, actor.User)
	if rebase {
		if !m.canPushHead(ctx, repo, headRepo, actor, issue, pr) {
			return ErrForbidden
		}
		newHead, err := m.rebaseOnto(ctx, headRepo, actor, mergeBase, headTip, baseTip)
		if err != nil {
			return err
		}
		return m.git.UpdateRef(ctx, headRepo, actor.User, plumbing.NewBranchReferenceName(pr.HeadBranch), headTip, newHead, gitrepo.HookOptions{BypassPullRequest: true, BypassSignedCommits: true})
	}
	msg := fmt.Sprintf("Merge branch '%s' into %s\n", pr.BaseBranch, pr.HeadBranch)
	_, err = m.git.CommitToBranch(ctx, headRepo, actor.User, gitrepo.CommitRequest{
		Branch: pr.HeadBranch, ExpectedHead: headTip, Message: msg, Options: gitrepo.HookOptions{BypassPullRequest: true, BypassSignedCommits: true},
		Build: func(r *gitrepo.Repo, head plumbing.Hash) (plumbing.Hash, error) {
			res, err := r.Merge3(ctx, mergeBase, head, baseTip, true)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			if !res.Clean() {
				return plumbing.ZeroHash, conflictError(res)
			}
			return r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: res.Tree, Parents: []plumbing.Hash{head, baseTip}, Message: msg, Author: author, Committer: author, Signer: m.git.Signer(ctx)})
		},
	})
	return err
}

// Conflict error naming the paths
func conflictError(res *gitrepo.MergeResult) error {
	paths := make([]string, 0, len(res.Conflicts))
	for _, c := range res.Conflicts {
		paths = append(paths, c.Path)
	}
	return fmt.Errorf("%w in %s", gitrepo.ErrMergeConflict, strings.Join(paths, ", "))
}

// Commits of a pull request oldest first
func (m *Manager) PullCommits(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest, limit int) ([]*object.Commit, error) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	base := plumbing.ZeroHash
	if pr.MergeBaseSHA != "" {
		base = plumbing.NewHash(pr.MergeBaseSHA)
	}
	hashes, err := r.CommitRange(ctx, base, plumbing.NewHash(pr.HeadSHA))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(hashes) > limit {
		hashes = hashes[:limit]
	}
	commits := make([]*object.Commit, 0, len(hashes))
	for _, h := range hashes {
		c, err := r.Commit(h)
		if err != nil {
			return nil, err
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// Pull request payload for workflow event contexts
func (m *Manager) pullPayloadMap(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, pr *db.GitPullRequest) map[string]any {
	out := m.issuePayloadMap(ctx, repo, issue)
	p := m.pullPayload(ctx, repo, issue, pr)
	b, _ := json.Marshal(p)
	var pm map[string]any
	_ = json.Unmarshal(b, &pm)
	if existing, ok := out["pull_request"].(map[string]any); ok {
		for k, v := range pm {
			existing[k] = v
		}
	} else {
		out["pull_request"] = pm
	}
	return out
}

// Sends the pull request webhook with a review section
func (m *Manager) dispatchReview(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, pr *db.GitPullRequest, actor Actor, action string, rv *db.GitReview) {
	m.git.DispatchGit(ctx, "git_pull_request_review", repo, webhook.GitPayload{
		Action: action, Sender: webhook.Sender{Username: actor.username()},
		Issue: m.issuePayload(ctx, repo, issue), PullRequest: m.pullPayload(ctx, repo, issue, pr),
		Review: &webhook.ReviewPayload{ID: rv.ID, State: rv.State, Body: rv.Body, Reviewer: m.usernameOf(ctx, rv.ReviewerID)},
	})
}

func (m *Manager) usernameOf(ctx context.Context, id string) string {
	if u, _ := m.store.GetUserByID(ctx, id); u != nil {
		return u.Username
	}
	return ""
}

// Principal for a stored user row
func (m *Manager) principal(u *db.User) *auth.AuthenticatedUser {
	p := &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: []string{"user"}, Provider: u.AuthProvider}
	if u.Email != nil {
		p.Email = *u.Email
	}
	return p
}

var _ = time.Now
