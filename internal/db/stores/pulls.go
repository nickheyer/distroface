package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── Pull requests ────────────────────────────────────────────────────────

func (s *Store) CreateGitPullRequest(ctx context.Context, pr *db.GitPullRequest) error {
	return s.db.WithContext(ctx).Create(pr).Error
}

func (s *Store) GetGitPullRequest(ctx context.Context, issueID string) (*db.GitPullRequest, error) {
	var pr db.GitPullRequest
	err := s.db.WithContext(ctx).First(&pr, "issue_id = ?", issueID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &pr, err
}

func (s *Store) GetGitPullRequests(ctx context.Context, issueIDs []string) (map[string]*db.GitPullRequest, error) {
	out := map[string]*db.GitPullRequest{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []*db.GitPullRequest
	if err := s.db.WithContext(ctx).Where("issue_id IN ?", issueIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, pr := range rows {
		out[pr.IssueID] = pr
	}
	return out, nil
}

func (s *Store) UpdateGitPullRequest(ctx context.Context, pr *db.GitPullRequest) error {
	return s.db.WithContext(ctx).Save(pr).Error
}

func (s *Store) UpdateGitPullRequestColumns(ctx context.Context, issueID string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.GitPullRequest{}).Where("issue_id = ?", issueID).Updates(cols).Error
}

// Open pull requests whose head branch lives in the given repo
func (s *Store) ListOpenPullsByHead(ctx context.Context, headRepoID, branch string) ([]*db.GitPullRequest, error) {
	var out []*db.GitPullRequest
	err := s.db.WithContext(ctx).Joins("JOIN git_issues ON git_issues.id = git_pull_requests.issue_id").
		Where("git_pull_requests.head_repo_id = ? AND git_pull_requests.head_branch = ? AND git_issues.state = ?", headRepoID, branch, db.IssueStateOpen).
		Find(&out).Error
	return out, err
}

// Open pull requests targeting a base branch of the given repo
func (s *Store) ListOpenPullsByBase(ctx context.Context, repoID, branch string) ([]*db.GitPullRequest, error) {
	var out []*db.GitPullRequest
	err := s.db.WithContext(ctx).Joins("JOIN git_issues ON git_issues.id = git_pull_requests.issue_id").
		Where("git_issues.repo_id = ? AND git_pull_requests.base_branch = ? AND git_issues.state = ?", repoID, branch, db.IssueStateOpen).
		Find(&out).Error
	return out, err
}

// Pull requests with auto merge armed in a repo
func (s *Store) ListAutoMergePulls(ctx context.Context, repoID string) ([]*db.GitPullRequest, error) {
	var out []*db.GitPullRequest
	err := s.db.WithContext(ctx).Joins("JOIN git_issues ON git_issues.id = git_pull_requests.issue_id").
		Where("git_issues.repo_id = ? AND git_pull_requests.auto_merge_method <> '' AND git_issues.state = ?", repoID, db.IssueStateOpen).
		Find(&out).Error
	return out, err
}

// Existing open pull request between the same branches
func (s *Store) FindOpenPull(ctx context.Context, repoID, headRepoID, headBranch, baseBranch string) (*db.GitIssue, error) {
	var issue db.GitIssue
	err := s.db.WithContext(ctx).Joins("JOIN git_pull_requests pr ON pr.issue_id = git_issues.id").
		Where("git_issues.repo_id = ? AND git_issues.state = ? AND pr.head_repo_id = ? AND pr.head_branch = ? AND pr.base_branch = ?",
			repoID, db.IssueStateOpen, headRepoID, headBranch, baseBranch).First(&issue).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &issue, err
}

// Merged pull requests whose merge commit landed between two dates, for release notes
func (s *Store) ListMergedPullsBetween(ctx context.Context, repoID string, since, until time.Time) ([]*db.GitIssue, error) {
	var out []*db.GitIssue
	err := s.db.WithContext(ctx).Joins("JOIN git_pull_requests pr ON pr.issue_id = git_issues.id").
		Where("git_issues.repo_id = ? AND pr.merged_at IS NOT NULL AND pr.merged_at > ? AND pr.merged_at <= ?", repoID, since, until).
		Order("pr.merged_at ASC").Find(&out).Error
	return out, err
}

// ── Review requests ──────────────────────────────────────────────────────

func (s *Store) AddGitReviewRequest(ctx context.Context, r *db.GitReviewRequest) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	var existing db.GitReviewRequest
	err := s.db.WithContext(ctx).First(&existing, "issue_id = ? AND user_id = ? AND team_id = ?", r.IssueID, r.UserID, r.TeamID).Error
	if err == nil {
		return s.db.WithContext(ctx).Model(&existing).Update("fulfilled", false).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *Store) RemoveGitReviewRequest(ctx context.Context, issueID, userID, teamID string) error {
	return s.db.WithContext(ctx).Where("issue_id = ? AND user_id = ? AND team_id = ?", issueID, userID, teamID).Delete(&db.GitReviewRequest{}).Error
}

func (s *Store) ListGitReviewRequests(ctx context.Context, issueID string) ([]*db.GitReviewRequest, error) {
	var out []*db.GitReviewRequest
	err := s.db.WithContext(ctx).Where("issue_id = ?", issueID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// Review requests of many issues keyed by issue id
func (s *Store) ListGitReviewRequestsByIssues(ctx context.Context, issueIDs []string) (map[string][]*db.GitReviewRequest, error) {
	out := map[string][]*db.GitReviewRequest{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []*db.GitReviewRequest
	if err := s.db.WithContext(ctx).Where("issue_id IN ?", issueIDs).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = append(out[r.IssueID], r)
	}
	return out, nil
}

// Submitted reviews of many issues keyed by issue id
func (s *Store) ListGitReviewsByIssues(ctx context.Context, issueIDs []string) (map[string][]*db.GitReview, error) {
	out := map[string][]*db.GitReview{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []*db.GitReview
	if err := s.db.WithContext(ctx).Where("issue_id IN ? AND state <> ?", issueIDs, "pending").Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = append(out[r.IssueID], r)
	}
	return out, nil
}

func (s *Store) FulfillGitReviewRequest(ctx context.Context, issueID, userID string) error {
	return s.db.WithContext(ctx).Model(&db.GitReviewRequest{}).Where("issue_id = ? AND user_id = ?", issueID, userID).Update("fulfilled", true).Error
}

// ── Reviews ──────────────────────────────────────────────────────────────

func (s *Store) CreateGitReview(ctx context.Context, r *db.GitReview) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *Store) GetGitReview(ctx context.Context, issueID, id string) (*db.GitReview, error) {
	var r db.GitReview
	err := s.db.WithContext(ctx).First(&r, "issue_id = ? AND id = ?", issueID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) GetPendingGitReview(ctx context.Context, issueID, reviewerID string) (*db.GitReview, error) {
	var r db.GitReview
	err := s.db.WithContext(ctx).First(&r, "issue_id = ? AND reviewer_id = ? AND state = ?", issueID, reviewerID, "pending").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) ListGitReviews(ctx context.Context, issueID string, includePending bool, limit, offset int) ([]*db.GitReview, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitReview{}).Where("issue_id = ?", issueID)
	if !includePending {
		q = q.Where("state <> ?", "pending")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitReview
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at ASC").Find(&out).Error
	return out, total, err
}

func (s *Store) UpdateGitReview(ctx context.Context, r *db.GitReview) error {
	return s.db.WithContext(ctx).Save(r).Error
}

// Dismisses approvals made on an older head
func (s *Store) DismissStaleGitApprovals(ctx context.Context, issueID, headSHA string) (int64, error) {
	res := s.db.WithContext(ctx).Model(&db.GitReview{}).
		Where("issue_id = ? AND state = ? AND dismissed = ? AND commit_sha <> ?", issueID, "approved", false, headSHA).
		Updates(map[string]any{"dismissed": true, "dismissed_by": "system"})
	return res.RowsAffected, res.Error
}

func (s *Store) CountGitReviewComments(ctx context.Context, reviewIDs []string) (map[string]int32, error) {
	out := map[string]int32{}
	if len(reviewIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ReviewID string
		Count    int32
	}
	err := s.db.WithContext(ctx).Model(&db.GitReviewComment{}).Select("review_id, COUNT(*) as count").
		Where("review_id IN ?", reviewIDs).Group("review_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ReviewID] = r.Count
	}
	return out, nil
}

// ── Review comments ──────────────────────────────────────────────────────

func (s *Store) CreateGitReviewComment(ctx context.Context, c *db.GitReviewComment) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.ThreadID == "" {
		c.ThreadID = c.ID
	}
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *Store) GetGitReviewComment(ctx context.Context, issueID, id string) (*db.GitReviewComment, error) {
	var c db.GitReviewComment
	err := s.db.WithContext(ctx).First(&c, "issue_id = ? AND id = ?", issueID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// Pending comments stay private to their author
func (s *Store) ListGitReviewComments(ctx context.Context, issueID, path, viewerID string) ([]*db.GitReviewComment, error) {
	q := s.db.WithContext(ctx).Where("issue_id = ?", issueID)
	if path != "" {
		q = q.Where("path = ?", path)
	}
	q = q.Where("pending = ? OR author_id = ?", false, viewerID)
	var out []*db.GitReviewComment
	err := q.Order("thread_id ASC, created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) ListGitReviewCommentsByReview(ctx context.Context, reviewID string) ([]*db.GitReviewComment, error) {
	var out []*db.GitReviewComment
	err := s.db.WithContext(ctx).Where("review_id = ?", reviewID).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) UpdateGitReviewComment(ctx context.Context, c *db.GitReviewComment) error {
	return s.db.WithContext(ctx).Save(c).Error
}

func (s *Store) PublishGitReviewComments(ctx context.Context, reviewID string) error {
	return s.db.WithContext(ctx).Model(&db.GitReviewComment{}).Where("review_id = ?", reviewID).Update("pending", false).Error
}

func (s *Store) ResolveGitReviewThread(ctx context.Context, issueID, threadID, resolvedBy string, resolved bool) error {
	cols := map[string]any{"resolved": resolved, "resolved_by": ""}
	if resolved {
		cols["resolved_by"] = resolvedBy
	}
	return s.db.WithContext(ctx).Model(&db.GitReviewComment{}).Where("issue_id = ? AND thread_id = ?", issueID, threadID).Updates(cols).Error
}

func (s *Store) DeleteGitReviewComment(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitReviewComment{}, "id = ?", id).Error
}

// Marks comments outdated when their anchor vanished from the head
func (s *Store) MarkGitReviewCommentsOutdated(ctx context.Context, ids []string, headSHA string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&db.GitReviewComment{}).Where("id IN ?", ids).Update("outdated_sha", headSHA).Error
}
