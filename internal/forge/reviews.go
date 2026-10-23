package forge

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/utils/diff"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// Review states stored on rows
const (
	ReviewPending          = "pending"
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewCommented        = "commented"
)

var suggestionPattern = regexp.MustCompile("(?s)```suggestion[^\n]*\n(.*?)\n?```")

// One inline comment in a review batch
type ReviewCommentInput struct {
	Path      string
	Line      int
	Side      string
	StartLine int
	Body      string
	ThreadID  string
}

// Reviewers excluding the author and users without read access
func (m *Manager) validReviewers(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range m.validAssignees(ctx, repo, ids) {
		if id == issue.AuthorID || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Teams of the owning organization
func (m *Manager) validTeams(ctx context.Context, repo *db.GitRepository, ids []string) []*db.OrgTeam {
	if len(ids) == 0 || !repo.IsOrgNamespace {
		return nil
	}
	orgID := m.orgIDOf(ctx, repo)
	teams, _ := m.store.GetOrgTeamsByIDs(ctx, ids)
	out := teams[:0]
	for _, t := range teams {
		if t.OrgID == orgID {
			out = append(out, t)
		}
	}
	return out
}

// Requests reviews from users and teams
func (m *Manager) RequestReviewers(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, userIDs, teamIDs []string, silentEmpty bool) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	users := m.validReviewers(ctx, repo, issue, userIDs)
	teams := m.validTeams(ctx, repo, teamIDs)
	if len(users) == 0 && len(teams) == 0 {
		if silentEmpty {
			return nil
		}
		return fmt.Errorf("%w: no valid reviewers", ErrInvalid)
	}
	var notify []string
	for _, id := range users {
		if err := m.store.AddGitReviewRequest(ctx, &db.GitReviewRequest{IssueID: issue.ID, UserID: id, RequestedBy: actor.User.ID}); err != nil {
			return err
		}
		notify = append(notify, id)
		_, _ = m.AddEvent(ctx, issue, actor.User, EventReviewRequested, map[string]string{"user_id": id})
	}
	for _, t := range teams {
		if err := m.store.AddGitReviewRequest(ctx, &db.GitReviewRequest{IssueID: issue.ID, TeamID: t.ID, RequestedBy: actor.User.ID}); err != nil {
			return err
		}
		members, _ := m.store.TeamUserIDs(ctx, t)
		notify = append(notify, members...)
		_, _ = m.AddEvent(ctx, issue, actor.User, EventReviewRequested, map[string]string{"team_id": t.ID, "team": t.Name})
	}
	pr, _ := m.store.GetGitPullRequest(ctx, issue.ID)
	requested := append([]string{}, users...)
	m.Notify(ctx, Notification{
		Repo: repo, Issue: issue, Kind: NotifyReview, Actor: actor.User, Reason: "review_requested",
		Subject: fmt.Sprintf("[%s/%s] %s (#%d)", repo.Namespace, repo.Name, issue.Title, issue.Number),
		Text:    fmt.Sprintf("%s requested your review on #%d %s.", actor.username(), issue.Number, issue.Title),
		URL:     m.IssueURL(ctx, repo, issue), Reviewers: notify, ExplicitOnly: true,
	})
	m.dispatchIssue(ctx, repo, issue, actor, "review_requested", nil)
	if pr != nil {
		payload := m.pullPayloadMap(ctx, repo, issue, pr)
		payload["requested_reviewer_ids"] = requested
		m.Trigger(ctx, repo, "pull_request", "review_requested", PullRef(issue.Number).String(), pr.HeadSHA, actor.User, payload)
	}
	return nil
}

// Withdraws review requests
func (m *Manager) RemoveReviewers(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, userIDs, teamIDs []string) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	for _, id := range userIDs {
		if err := m.store.RemoveGitReviewRequest(ctx, issue.ID, id, ""); err != nil {
			return err
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, EventReviewRemoved, map[string]string{"user_id": id})
	}
	for _, id := range teamIDs {
		if err := m.store.RemoveGitReviewRequest(ctx, issue.ID, "", id); err != nil {
			return err
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, EventReviewRemoved, map[string]string{"team_id": id})
	}
	return nil
}

func reviewStateFromProto(s v1.ReviewState) string {
	switch s {
	case v1.ReviewState_REVIEW_STATE_APPROVED:
		return ReviewApproved
	case v1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED:
		return ReviewChangesRequested
	case v1.ReviewState_REVIEW_STATE_COMMENTED:
		return ReviewCommented
	case v1.ReviewState_REVIEW_STATE_PENDING:
		return ReviewPending
	}
	return ""
}

// Stored review state for a proto value
func ReviewStateFromProto(s v1.ReviewState) string { return reviewStateFromProto(s) }

// Proto value of a stored review state
func ReviewStateProto(s string) v1.ReviewState {
	switch s {
	case ReviewApproved:
		return v1.ReviewState_REVIEW_STATE_APPROVED
	case ReviewChangesRequested:
		return v1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED
	case ReviewCommented:
		return v1.ReviewState_REVIEW_STATE_COMMENTED
	case ReviewPending:
		return v1.ReviewState_REVIEW_STATE_PENDING
	}
	return v1.ReviewState_REVIEW_STATE_UNSPECIFIED
}

// Reports whether a review may carry the state
func (m *Manager) reviewAllowed(repo *db.GitRepository, actor Actor, issue *db.GitIssue, state string) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return fmt.Errorf("%w: sign in to review", ErrForbidden)
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if issue.Locked && !actor.CanWrite {
		return ErrLocked
	}
	if (state == ReviewApproved || state == ReviewChangesRequested) && issue.AuthorID == actor.User.ID {
		return fmt.Errorf("%w: you cannot approve or request changes on your own pull request", ErrInvalid)
	}
	return nil
}

// Creates a review, pending until submitted when the state is empty
func (m *Manager) CreateReview(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, state, body, commitSHA string, comments []ReviewCommentInput) (*db.GitReview, error) {
	if state == "" {
		state = ReviewPending
	}
	if err := m.reviewAllowed(repo, actor, issue, state); err != nil {
		return nil, err
	}
	if commitSHA == "" {
		commitSHA = pr.HeadSHA
	}
	if existing, _ := m.store.GetPendingGitReview(ctx, issue.ID, actor.User.ID); existing != nil {
		if state == ReviewPending {
			for _, c := range comments {
				if _, err := m.addReviewComment(ctx, repo, actor, issue, pr, existing, c, commitSHA, true); err != nil {
					return nil, err
				}
			}
			return existing, nil
		}
		for _, c := range comments {
			if _, err := m.addReviewComment(ctx, repo, actor, issue, pr, existing, c, commitSHA, true); err != nil {
				return nil, err
			}
		}
		return m.SubmitReview(ctx, repo, actor, issue, pr, existing, state, body)
	}
	rv := &db.GitReview{ID: uuid.New().String(), IssueID: issue.ID, ReviewerID: actor.User.ID, State: ReviewPending, Body: body, CommitSHA: commitSHA}
	if err := m.store.CreateGitReview(ctx, rv); err != nil {
		return nil, err
	}
	for _, c := range comments {
		if _, err := m.addReviewComment(ctx, repo, actor, issue, pr, rv, c, commitSHA, true); err != nil {
			return nil, err
		}
	}
	if state == ReviewPending {
		return rv, nil
	}
	return m.SubmitReview(ctx, repo, actor, issue, pr, rv, state, body)
}

// Submits a pending review, publishing its comments
func (m *Manager) SubmitReview(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, rv *db.GitReview, state, body string) (*db.GitReview, error) {
	if rv.ReviewerID != actor.id() {
		return nil, ErrForbidden
	}
	if rv.State != ReviewPending {
		return nil, fmt.Errorf("%w: review is already submitted", ErrInvalid)
	}
	if state == "" || state == ReviewPending {
		state = ReviewCommented
	}
	if err := m.reviewAllowed(repo, actor, issue, state); err != nil {
		return nil, err
	}
	now := time.Now()
	rv.State, rv.SubmittedAt = state, &now
	if body != "" {
		rv.Body = body
	}
	if rv.CommitSHA == "" {
		rv.CommitSHA = pr.HeadSHA
	}
	if err := m.store.UpdateGitReview(ctx, rv); err != nil {
		return nil, err
	}
	_ = m.store.PublishGitReviewComments(ctx, rv.ID)
	_ = m.store.FulfillGitReviewRequest(ctx, issue.ID, actor.User.ID)
	_ = m.store.SetGitIssueSubscription(ctx, issue.ID, actor.User.ID, true)
	payload := map[string]any{"review_id": rv.ID, "state": state, "body": rv.Body, "commit_sha": rv.CommitSHA}
	if c, err := m.AddEvent(ctx, issue, actor.User, EventReviewed, payload); err == nil {
		c.ReviewID = &rv.ID
		_ = m.store.UpdateGitComment(ctx, c)
	}
	if pr.MergeableState == MergeableBlocked {
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": MergeableUnknown})
	}
	verb := map[string]string{ReviewApproved: "approved", ReviewChangesRequested: "requested changes on", ReviewCommented: "reviewed"}[state]
	m.Notify(ctx, Notification{
		Repo: repo, Issue: issue, Kind: NotifyReview, Actor: actor.User, Reason: "reviewed",
		Subject: fmt.Sprintf("[%s/%s] %s (#%d)", repo.Namespace, repo.Name, issue.Title, issue.Number),
		Text:    fmt.Sprintf("%s %s #%d %s.\n\n%s", actor.username(), verb, issue.Number, issue.Title, rv.Body),
		URL:     m.IssueURL(ctx, repo, issue), Mentioned: m.resolveMentions(ctx, repo, mentions(rv.Body)),
	})
	m.dispatchReview(ctx, repo, issue, pr, actor, "submitted", rv)
	reviewPayload := m.pullPayloadMap(ctx, repo, issue, pr)
	reviewPayload["review"] = map[string]any{"id": rv.ID, "state": rv.State, "body": rv.Body, "reviewer_id": rv.ReviewerID, "commit_sha": rv.CommitSHA}
	m.Trigger(ctx, repo, "pull_request_review", "submitted", PullRef(issue.Number).String(), pr.HeadSHA, actor.User, reviewPayload)
	if state == ReviewApproved {
		go m.RetryAutoMerge(context.WithoutCancel(ctx), repo, pr.HeadSHA)
	}
	return rv, nil
}

// Dismisses a review so it no longer counts
func (m *Manager) DismissReview(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, rv *db.GitReview, message string) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if rv.State == ReviewPending {
		return fmt.Errorf("%w: pending reviews cannot be dismissed", ErrInvalid)
	}
	rv.Dismissed, rv.DismissedBy = true, actor.User.ID
	if err := m.store.UpdateGitReview(ctx, rv); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, "review_dismissed", map[string]string{"review_id": rv.ID, "message": message, "reviewer_id": rv.ReviewerID})
	if pr.MergeableState == MergeableBlocked || pr.MergeableState == MergeableClean {
		_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": MergeableUnknown})
	}
	m.dispatchReview(ctx, repo, issue, pr, actor, "dismissed", rv)
	return nil
}

// Loads a review of a pull request
func (m *Manager) Review(ctx context.Context, issue *db.GitIssue, id string) (*db.GitReview, error) {
	rv, err := m.store.GetGitReview(ctx, issue.ID, id)
	if err != nil {
		return nil, err
	}
	if rv == nil {
		return nil, fmt.Errorf("%w: review", ErrNotFound)
	}
	return rv, nil
}

// Diff hunk text around an anchored line at a commit
func (m *Manager) hunkFor(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest, path string, line int, side, sha string) string {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return ""
	}
	defer r.Close()
	base := plumbing.ZeroHash
	if pr.MergeBaseSHA != "" {
		base = plumbing.NewHash(pr.MergeBaseSHA)
	}
	diff, err := r.Diff(ctx, base, plumbing.NewHash(sha), gitrepo.DiffOptions{OnlyPath: path, Limit: 1})
	if err != nil || len(diff.Files) == 0 {
		return ""
	}
	for _, h := range diff.Files[0].Hunks {
		var b strings.Builder
		b.WriteString(h.Header)
		b.WriteByte('\n')
		hit := false
		for _, l := range h.Lines {
			switch l.Kind {
			case v1.DiffLineKind_DIFF_LINE_KIND_ADD:
				b.WriteString("+")
			case v1.DiffLineKind_DIFF_LINE_KIND_DELETE:
				b.WriteString("-")
			default:
				b.WriteString(" ")
			}
			b.WriteString(l.Text)
			b.WriteByte('\n')
			if (side == "old" && int(l.OldNumber) == line) || (side != "old" && int(l.NewNumber) == line) {
				hit = true
			}
		}
		if hit {
			return b.String()
		}
	}
	return ""
}

// Writes one inline comment, pending when part of an unsubmitted review
func (m *Manager) addReviewComment(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, rv *db.GitReview, in ReviewCommentInput, sha string, pending bool) (*db.GitReviewComment, error) {
	if strings.TrimSpace(in.Body) == "" {
		return nil, fmt.Errorf("%w: a comment body is required", ErrInvalid)
	}
	if in.Path == "" && in.ThreadID == "" {
		return nil, fmt.Errorf("%w: a path is required", ErrInvalid)
	}
	side := in.Side
	if side != "old" {
		side = "new"
	}
	c := &db.GitReviewComment{
		ID: uuid.New().String(), IssueID: issue.ID, AuthorID: actor.User.ID, Path: in.Path, Line: in.Line, Side: side,
		StartLine: in.StartLine, Body: in.Body, ThreadID: in.ThreadID, CommitSHA: sha, Pending: pending,
	}
	if rv != nil {
		c.ReviewID = &rv.ID
	}
	if in.ThreadID != "" {
		root, _ := m.store.GetGitReviewComment(ctx, issue.ID, in.ThreadID)
		if root == nil {
			return nil, fmt.Errorf("%w: thread", ErrNotFound)
		}
		c.Path, c.Line, c.Side, c.StartLine, c.DiffHunk, c.CommitSHA = root.Path, root.Line, root.Side, root.StartLine, root.DiffHunk, root.CommitSHA
	} else {
		c.DiffHunk = m.hunkFor(ctx, repo, pr, in.Path, in.Line, side, sha)
	}
	if err := m.store.CreateGitReviewComment(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Posts an inline comment immediately or into a pending review
func (m *Manager) CreateReviewComment(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, reviewID string, in ReviewCommentInput, sha string) (*db.GitReviewComment, error) {
	if err := m.canComment(repo, actor, issue); err != nil {
		return nil, err
	}
	if sha == "" {
		sha = pr.HeadSHA
	}
	if reviewID != "" {
		rv, err := m.Review(ctx, issue, reviewID)
		if err != nil {
			return nil, err
		}
		if rv.ReviewerID != actor.User.ID || rv.State != ReviewPending {
			return nil, fmt.Errorf("%w: review is not your pending review", ErrInvalid)
		}
		return m.addReviewComment(ctx, repo, actor, issue, pr, rv, in, sha, true)
	}
	c, err := m.addReviewComment(ctx, repo, actor, issue, pr, nil, in, sha, false)
	if err != nil {
		return nil, err
	}
	_ = m.store.SetGitIssueSubscription(ctx, issue.ID, actor.User.ID, true)
	m.Notify(ctx, Notification{
		Repo: repo, Issue: issue, Kind: NotifyPull, Actor: actor.User, Reason: "commented",
		Subject: fmt.Sprintf("[%s/%s] %s (#%d)", repo.Namespace, repo.Name, issue.Title, issue.Number),
		Text:    fmt.Sprintf("%s commented on %s in #%d:\n\n%s", actor.username(), c.Path, issue.Number, c.Body),
		URL:     m.IssueURL(ctx, repo, issue) + "/files#comment-" + c.ID, Mentioned: m.resolveMentions(ctx, repo, mentions(c.Body)), CommentID: c.ID,
	})
	m.dispatchIssue(ctx, repo, issue, actor, "created", &db.GitComment{ID: c.ID, Body: c.Body, AuthorID: c.AuthorID})
	commentPayload := m.pullPayloadMap(ctx, repo, issue, pr)
	commentPayload["comment"] = map[string]any{"id": c.ID, "body": c.Body, "author_id": c.AuthorID, "path": c.Path, "line": c.Line, "side": c.Side, "thread_id": c.ThreadID}
	m.Trigger(ctx, repo, "pull_request_review_comment", "created", PullRef(issue.Number).String(), pr.HeadSHA, actor.User, commentPayload)
	return c, nil
}

// Loads an inline comment of a pull request
func (m *Manager) ReviewComment(ctx context.Context, issue *db.GitIssue, id string) (*db.GitReviewComment, error) {
	c, err := m.store.GetGitReviewComment(ctx, issue.ID, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("%w: review comment", ErrNotFound)
	}
	return c, nil
}

func (m *Manager) UpdateReviewComment(ctx context.Context, repo *db.GitRepository, actor Actor, c *db.GitReviewComment, body string) error {
	if actor.User == nil || (c.AuthorID != actor.User.ID && !actor.CanWrite) {
		return ErrForbidden
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w: a comment body is required", ErrInvalid)
	}
	c.Body = body
	return m.store.UpdateGitReviewComment(ctx, c)
}

func (m *Manager) DeleteReviewComment(ctx context.Context, actor Actor, c *db.GitReviewComment) error {
	if actor.User == nil || (c.AuthorID != actor.User.ID && !actor.CanWrite) {
		return ErrForbidden
	}
	return m.store.DeleteGitReviewComment(ctx, c.ID)
}

// Resolves or unresolves a thread
func (m *Manager) ResolveThread(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, threadID string, resolved bool) (*db.GitReviewComment, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return nil, ErrForbidden
	}
	root, err := m.ReviewComment(ctx, issue, threadID)
	if err != nil {
		return nil, err
	}
	if !actor.CanWrite && root.AuthorID != actor.User.ID && issue.AuthorID != actor.User.ID {
		return nil, ErrForbidden
	}
	if err := m.store.ResolveGitReviewThread(ctx, issue.ID, root.ThreadID, actor.User.ID, resolved); err != nil {
		return nil, err
	}
	root.Resolved = resolved
	root.ResolvedBy = ""
	if resolved {
		root.ResolvedBy = actor.User.ID
	}
	return root, nil
}

// Commits the suggestion block of a comment onto the head branch
func (m *Manager) ApplySuggestion(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, c *db.GitReviewComment, message string) (plumbing.Hash, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return plumbing.ZeroHash, ErrForbidden
	}
	if issue.State != "open" || pr.MergedAt != nil {
		return plumbing.ZeroHash, fmt.Errorf("%w: pull request is not open", ErrInvalid)
	}
	match := suggestionPattern.FindStringSubmatch(c.Body)
	if match == nil {
		return plumbing.ZeroHash, fmt.Errorf("%w: the comment has no suggestion block", ErrInvalid)
	}
	if c.Side == "old" {
		return plumbing.ZeroHash, fmt.Errorf("%w: suggestions apply to the new side only", ErrInvalid)
	}
	headRepo, err := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil || headRepo == nil {
		return plumbing.ZeroHash, fmt.Errorf("%w: head repository", ErrNotFound)
	}
	if !m.canPushHead(ctx, repo, headRepo, actor, issue, pr) {
		return plumbing.ZeroHash, ErrForbidden
	}
	headTip := m.branchTip(headRepo, pr.HeadBranch)
	if headTip.IsZero() {
		return plumbing.ZeroHash, fmt.Errorf("%w: head branch no longer exists", ErrInvalid)
	}
	if c.CommitSHA == "" {
		return plumbing.ZeroHash, fmt.Errorf("%w: the comment is not anchored to a commit", ErrInvalid)
	}
	r, err := m.git.Engine().OpenRepo(headRepo.Namespace, headRepo.Name, false)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer r.Close()
	blob, err := r.Blob(headTip, c.Path, 1<<26)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("%w: file %s", ErrNotFound, c.Path)
	}
	if blob.IsBinary {
		return plumbing.ZeroHash, fmt.Errorf("%w: cannot apply suggestions to binary files", ErrInvalid)
	}
	start, end := c.StartLine, c.Line
	if start <= 0 {
		start = end
	}
	if start < 1 || end < start {
		return plumbing.ZeroHash, fmt.Errorf("%w: the suggested lines are out of range", ErrInvalid)
	}
	if c.CommitSHA != headTip.String() {
		anchored, err := r.Blob(plumbing.NewHash(c.CommitSHA), c.Path, 1<<26)
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("%w: the commit the suggestion was made on is no longer available", ErrInvalid)
		}
		if !bytes.Equal(anchored.Content, blob.Content) {
			var ok bool
			if start, end, ok = remapLines(string(anchored.Content), string(blob.Content), start, end); !ok {
				return plumbing.ZeroHash, fmt.Errorf("%w: the suggested lines changed since the comment was made", ErrInvalid)
			}
		}
	}
	lines := strings.Split(string(blob.Content), "\n")
	if end > len(lines) {
		return plumbing.ZeroHash, fmt.Errorf("%w: the suggested lines are out of range in the current head", ErrInvalid)
	}
	replacement := strings.Split(match[1], "\n")
	if match[1] == "" {
		replacement = nil
	}
	merged := append(append(append([]string{}, lines[:start-1]...), replacement...), lines[end:]...)
	content := strings.Join(merged, "\n")
	if message == "" {
		message = fmt.Sprintf("Apply suggestion from %s\n\nCo-authored-by: %s", actor.username(), m.git.Signature(ctx, actor.User).Email)
	}
	sha, err := m.git.CommitToBranch(ctx, headRepo, actor.User, gitrepo.CommitRequest{
		Branch: pr.HeadBranch, ExpectedHead: headTip, Message: message,
		Edits:   []gitrepo.TreeEdit{{Path: c.Path, Mode: blob.Mode, Content: strings.NewReader(content)}},
		Options: gitrepo.HookOptions{},
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	_ = m.store.ResolveGitReviewThread(ctx, issue.ID, c.ThreadID, actor.User.ID, true)
	return sha, nil
}

// Maps a line range of the old text onto the new text, false when any line in it changed
func remapLines(oldText, newText string, start, end int) (int, int, bool) {
	oldLine, newLine := 1, 1
	for _, d := range mergedLineDiffs(oldText, newText) {
		n := countLines(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			if start >= oldLine && end < oldLine+n {
				shift := newLine - oldLine
				return start + shift, end + shift, true
			}
			oldLine += n
			newLine += n
		case diffmatchpatch.DiffDelete:
			oldLine += n
		case diffmatchpatch.DiffInsert:
			newLine += n
		}
	}
	return 0, 0, false
}

// Line oriented diff with adjacent chunks of one type joined
func mergedLineDiffs(oldText, newText string) []diffmatchpatch.Diff {
	var out []diffmatchpatch.Diff
	for _, d := range diff.Do(oldText, newText) {
		if d.Text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Type == d.Type {
			out[n-1].Text += d.Text
			continue
		}
		out = append(out, d)
	}
	return out
}

func countLines(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
