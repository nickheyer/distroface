package stores

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

// ── Issues ───────────────────────────────────────────────────────────────

func (s *Store) CreateGitIssue(ctx context.Context, issue *db.GitIssue) error {
	if issue.ID == "" {
		issue.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(issue).Error
}

func (s *Store) GetGitIssue(ctx context.Context, repoID string, number int64) (*db.GitIssue, error) {
	var issue db.GitIssue
	err := s.db.WithContext(ctx).First(&issue, "repo_id = ? AND number = ?", repoID, number).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &issue, err
}

func (s *Store) GetGitIssueByID(ctx context.Context, id string) (*db.GitIssue, error) {
	var issue db.GitIssue
	err := s.db.WithContext(ctx).First(&issue, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &issue, err
}

func (s *Store) GetGitIssuesByIDs(ctx context.Context, ids []string) ([]*db.GitIssue, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*db.GitIssue
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error
	return out, err
}

type IssueListOptions struct {
	RepoID string
	// Repos whose issues the viewer may read
	IssueRepoIDs []string
	// Repos whose pull requests the viewer may read
	PullRepoIDs []string
	State       string
	Labels      []string
	Milestone   string
	Assignee    string
	Author      string
	Mentioned   string
	Involved    string
	IsPull      *bool
	ProjectID   string
	Reviewer    string
	Base        string
	Head        string
	Draft       *bool
	Merged      *bool
	Query       pages.Query
	OrderBy     string
	Limit       int
	Offset      int
}

// IssuesQuery allowlists issue list filters
var IssuesQuery = pages.Spec{
	Fields: map[string]string{"title": "git_issues.title", "body": "git_issues.body"},
	Text:   []string{"git_issues.title", "git_issues.body"},
}

// IssueSortColumns allowlists order_by for issue lists
var IssueSortColumns = map[string]bool{"created_at": true, "updated_at": true, "comments": true, "due_date": true, "number": true}

// Qualified order clause, accepts bare or already prefixed columns
func issueOrderClause(order string) string {
	order = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(order), "git_issues."))
	if order == "" {
		return "git_issues.created_at DESC"
	}
	if strings.HasPrefix(order, "comments ") {
		order = "comment_count " + strings.TrimPrefix(order, "comments ")
	}
	return "git_issues." + order
}

func (s *Store) issueQuery(ctx context.Context, opts IssueListOptions) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&db.GitIssue{})
	if opts.RepoID != "" {
		q = q.Where("git_issues.repo_id = ?", opts.RepoID)
	}
	switch {
	case len(opts.IssueRepoIDs) > 0 && len(opts.PullRepoIDs) > 0:
		q = q.Where("((git_issues.repo_id IN ? AND git_issues.is_pull = ?) OR (git_issues.repo_id IN ? AND git_issues.is_pull = ?))", opts.IssueRepoIDs, false, opts.PullRepoIDs, true)
	case len(opts.IssueRepoIDs) > 0:
		q = q.Where("git_issues.repo_id IN ? AND git_issues.is_pull = ?", opts.IssueRepoIDs, false)
	case len(opts.PullRepoIDs) > 0:
		q = q.Where("git_issues.repo_id IN ? AND git_issues.is_pull = ?", opts.PullRepoIDs, true)
	}
	if opts.State != "" {
		q = q.Where("git_issues.state = ?", opts.State)
	}
	if opts.IsPull != nil {
		q = q.Where("git_issues.is_pull = ?", *opts.IsPull)
	}
	for _, label := range opts.Labels {
		q = q.Where("git_issues.id IN (SELECT il.issue_id FROM git_issue_labels il JOIN git_labels l ON l.id = il.label_id WHERE l.name = ?)", label)
	}
	if opts.Milestone != "" {
		if opts.Milestone == "none" {
			q = q.Where("git_issues.milestone_id IS NULL")
		} else {
			q = q.Where("git_issues.milestone_id IN (SELECT id FROM git_milestones WHERE title = ? OR id = ?)", opts.Milestone, opts.Milestone)
		}
	}
	if opts.Assignee != "" {
		if opts.Assignee == "none" {
			q = q.Where("git_issues.id NOT IN (SELECT issue_id FROM git_issue_assignees)")
		} else {
			q = q.Where("git_issues.id IN (SELECT a.issue_id FROM git_issue_assignees a JOIN users u ON u.id = a.user_id WHERE u.username = ? OR u.id = ?)", opts.Assignee, opts.Assignee)
		}
	}
	if opts.Author != "" {
		q = q.Where("git_issues.author_id IN (SELECT id FROM users WHERE username = ? OR id = ?)", opts.Author, opts.Author)
	}
	if opts.Mentioned != "" {
		pattern := pages.LikeContains("@" + opts.Mentioned)
		q = q.Where("(git_issues.body LIKE ? ESCAPE '\\' OR git_issues.id IN (SELECT issue_id FROM git_comments WHERE body LIKE ? ESCAPE '\\'))", pattern, pattern)
	}
	if opts.Involved != "" {
		pattern := pages.LikeContains("@" + opts.Involved)
		q = q.Where("(git_issues.author_id = ? OR git_issues.id IN (SELECT issue_id FROM git_issue_assignees WHERE user_id = ?) "+
			"OR git_issues.id IN (SELECT issue_id FROM git_comments WHERE author_id = ?) "+
			"OR git_issues.id IN (SELECT issue_id FROM git_review_requests WHERE user_id = ?) "+
			"OR git_issues.body LIKE ? ESCAPE '\\')", opts.Involved, opts.Involved, opts.Involved, opts.Involved, pattern)
	}
	if opts.ProjectID != "" {
		q = q.Where("git_issues.id IN (SELECT issue_id FROM git_project_issues WHERE project_id = ?)", opts.ProjectID)
	}
	if opts.Reviewer != "" {
		q = q.Where("(git_issues.id IN (SELECT rr.issue_id FROM git_review_requests rr JOIN users u ON u.id = rr.user_id WHERE u.username = ? OR u.id = ?) "+
			"OR git_issues.id IN (SELECT rv.issue_id FROM git_reviews rv JOIN users u ON u.id = rv.reviewer_id WHERE u.username = ? OR u.id = ?))",
			opts.Reviewer, opts.Reviewer, opts.Reviewer, opts.Reviewer)
	}
	if opts.Base != "" {
		q = q.Where("git_issues.id IN (SELECT issue_id FROM git_pull_requests WHERE base_branch = ?)", opts.Base)
	}
	if opts.Head != "" {
		// Fork heads prefix the branch with their repository, bare branches belong to the base repository
		if ns, branch, ok := strings.Cut(opts.Head, ":"); ok && strings.Contains(ns, "/") {
			owner, name, _ := strings.Cut(ns, "/")
			q = q.Where("git_issues.id IN (SELECT issue_id FROM git_pull_requests WHERE head_branch = ? AND head_repo_id IN (SELECT id FROM git_repositories WHERE namespace = ? AND name = ?))", branch, owner, name)
		} else {
			q = q.Where("git_issues.id IN (SELECT issue_id FROM git_pull_requests WHERE head_branch = ? AND head_repo_id = git_issues.repo_id)", opts.Head)
		}
	}
	if opts.Draft != nil {
		q = q.Where("git_issues.id IN (SELECT issue_id FROM git_pull_requests WHERE draft = ?)", *opts.Draft)
	}
	if opts.Merged != nil {
		if *opts.Merged {
			q = q.Where("git_issues.id IN (SELECT issue_id FROM git_pull_requests WHERE merged_at IS NOT NULL)")
		} else {
			q = q.Where("git_issues.id NOT IN (SELECT issue_id FROM git_pull_requests WHERE merged_at IS NOT NULL)")
		}
	}
	return q.Scopes(IssuesQuery.Scope(opts.Query))
}

// Total under the filters, state included
func (s *Store) CountGitIssues(ctx context.Context, opts IssueListOptions) (int64, error) {
	var total int64
	err := s.issueQuery(ctx, opts).Count(&total).Error
	return total, err
}

func (s *Store) ListGitIssues(ctx context.Context, opts IssueListOptions) ([]*db.GitIssue, int64, error) {
	q := s.issueQuery(ctx, opts)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q = q.Order("git_issues.pinned DESC, " + issueOrderClause(opts.OrderBy))
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit).Offset(opts.Offset)
	}
	var out []*db.GitIssue
	err := q.Find(&out).Error
	return out, total, err
}

// Open and closed totals under the same filters
func (s *Store) CountGitIssuesByState(ctx context.Context, opts IssueListOptions) (int64, int64, error) {
	opts.State = ""
	var rows []struct {
		State string
		Count int64
	}
	if err := s.issueQuery(ctx, opts).Select("git_issues.state AS state, COUNT(*) AS count").Group("git_issues.state").Scan(&rows).Error; err != nil {
		return 0, 0, err
	}
	var open, closed int64
	for _, r := range rows {
		switch r.State {
		case db.IssueStateOpen:
			open = r.Count
		case db.IssueStateClosed:
			closed = r.Count
		}
	}
	return open, closed, nil
}

func (s *Store) CountOpenGitIssues(ctx context.Context, repoIDs []string) (map[string]int64, map[string]int64, error) {
	issues, pulls := map[string]int64{}, map[string]int64{}
	if len(repoIDs) == 0 {
		return issues, pulls, nil
	}
	var rows []struct {
		RepoID string
		IsPull bool
		Count  int64
	}
	err := s.db.WithContext(ctx).Model(&db.GitIssue{}).Select("repo_id, is_pull, COUNT(*) as count").
		Where("repo_id IN ? AND state = ?", repoIDs, db.IssueStateOpen).Group("repo_id, is_pull").Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		if r.IsPull {
			pulls[r.RepoID] = r.Count
		} else {
			issues[r.RepoID] = r.Count
		}
	}
	return issues, pulls, nil
}

func (s *Store) UpdateGitIssue(ctx context.Context, issue *db.GitIssue) error {
	return s.db.WithContext(ctx).Save(issue).Error
}

func (s *Store) UpdateGitIssueColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.GitIssue{}).Where("id = ?", id).Updates(cols).Error
}

func (s *Store) DeleteGitIssue(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitIssue{}, "id = ?", id).Error
}

func (s *Store) TouchGitIssue(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.GitIssue{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error
}

// Highest pin order plus one, capped by the caller
func (s *Store) NextGitPinOrder(ctx context.Context, repoID string) (int, error) {
	var last int
	err := s.db.WithContext(ctx).Model(&db.GitIssue{}).Where("repo_id = ?", repoID).Select("COALESCE(MAX(pinned),0)").Scan(&last).Error
	return last + 1, err
}

func (s *Store) CountPinnedGitIssues(ctx context.Context, repoID string, isPull bool) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.GitIssue{}).Where("repo_id = ? AND is_pull = ? AND pinned > 0", repoID, isPull).Count(&n).Error
	return n, err
}

// ── Labels ───────────────────────────────────────────────────────────────

func (s *Store) CreateGitLabel(ctx context.Context, l *db.GitLabel) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(l).Error
}

func (s *Store) GetGitLabel(ctx context.Context, id string) (*db.GitLabel, error) {
	var l db.GitLabel
	err := s.db.WithContext(ctx).First(&l, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &l, err
}

// Repo labels plus the owning org's labels
func (s *Store) ListGitLabels(ctx context.Context, repoID string, orgID string, q pages.Query, limit, offset int) ([]*db.GitLabel, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.GitLabel{})
	switch {
	case repoID != "" && orgID != "":
		tx = tx.Where("repo_id = ? OR org_id = ?", repoID, orgID)
	case repoID != "":
		tx = tx.Where("repo_id = ?", repoID)
	default:
		tx = tx.Where("org_id = ?", orgID)
	}
	tx = tx.Scopes(pages.Spec{Fields: map[string]string{"name": "name"}, Text: []string{"name", "description"}}.Scope(q))
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitLabel
	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	err := tx.Order("org_id IS NULL DESC, name ASC").Find(&out).Error
	return out, total, err
}

func (s *Store) GetGitLabelsByIDs(ctx context.Context, ids []string) ([]*db.GitLabel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*db.GitLabel
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error
	return out, err
}

func (s *Store) UpdateGitLabel(ctx context.Context, l *db.GitLabel) error {
	return s.db.WithContext(ctx).Save(l).Error
}

func (s *Store) DeleteGitLabel(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitLabel{}, "id = ?", id).Error
}

// Open and closed counts per label id
func (s *Store) GitLabelCounts(ctx context.Context, labelIDs []string) (map[string][2]int64, error) {
	out := map[string][2]int64{}
	if len(labelIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		LabelID string
		State   string
		Count   int64
	}
	err := s.db.WithContext(ctx).Table("git_issue_labels").
		Select("git_issue_labels.label_id, git_issues.state, COUNT(*) as count").
		Joins("JOIN git_issues ON git_issues.id = git_issue_labels.issue_id").
		Where("git_issue_labels.label_id IN ?", labelIDs).Group("git_issue_labels.label_id, git_issues.state").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		c := out[r.LabelID]
		if r.State == db.IssueStateOpen {
			c[0] = r.Count
		} else {
			c[1] = r.Count
		}
		out[r.LabelID] = c
	}
	return out, nil
}

func (s *Store) SetGitIssueLabels(ctx context.Context, issueID string, labelIDs []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("issue_id = ?", issueID).Delete(&db.GitIssueLabel{}).Error; err != nil {
			return err
		}
		for _, id := range labelIDs {
			if err := tx.Create(&db.GitIssueLabel{IssueID: issueID, LabelID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetGitIssueLabels(ctx context.Context, issueIDs []string) (map[string][]*db.GitLabel, error) {
	out := map[string][]*db.GitLabel{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		IssueID string
		db.GitLabel
	}
	err := s.db.WithContext(ctx).Table("git_issue_labels").
		Select("git_issue_labels.issue_id, git_labels.*").
		Joins("JOIN git_labels ON git_labels.id = git_issue_labels.label_id").
		Where("git_issue_labels.issue_id IN ?", issueIDs).Order("git_labels.name ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		l := rows[i].GitLabel
		out[rows[i].IssueID] = append(out[rows[i].IssueID], &l)
	}
	return out, nil
}

// ── Milestones ───────────────────────────────────────────────────────────

func (s *Store) CreateGitMilestone(ctx context.Context, m *db.GitMilestone) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(m).Error
}

func (s *Store) GetGitMilestone(ctx context.Context, repoID, id string) (*db.GitMilestone, error) {
	var m db.GitMilestone
	err := s.db.WithContext(ctx).First(&m, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

func (s *Store) ListGitMilestones(ctx context.Context, repoID, state string, q pages.Query, limit, offset int) ([]*db.GitMilestone, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.GitMilestone{}).Where("repo_id = ?", repoID)
	if state != "" {
		tx = tx.Where("state = ?", state)
	}
	tx = tx.Scopes(pages.Spec{Fields: map[string]string{"title": "title"}, Text: []string{"title", "description"}}.Scope(q))
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitMilestone
	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	err := tx.Order("due_date IS NULL, due_date ASC, created_at DESC").Find(&out).Error
	return out, total, err
}

func (s *Store) GetGitMilestonesByIDs(ctx context.Context, ids []string) (map[string]*db.GitMilestone, error) {
	out := map[string]*db.GitMilestone{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []*db.GitMilestone
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, m := range rows {
		out[m.ID] = m
	}
	return out, nil
}

func (s *Store) GitMilestoneCounts(ctx context.Context, ids []string) (map[string][2]int64, error) {
	out := map[string][2]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		MilestoneID string
		State       string
		Count       int64
	}
	err := s.db.WithContext(ctx).Model(&db.GitIssue{}).Select("milestone_id, state, COUNT(*) as count").
		Where("milestone_id IN ?", ids).Group("milestone_id, state").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		c := out[r.MilestoneID]
		if r.State == db.IssueStateOpen {
			c[0] = r.Count
		} else {
			c[1] = r.Count
		}
		out[r.MilestoneID] = c
	}
	return out, nil
}

func (s *Store) UpdateGitMilestone(ctx context.Context, m *db.GitMilestone) error {
	return s.db.WithContext(ctx).Save(m).Error
}

func (s *Store) DeleteGitMilestone(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.GitIssue{}).Where("milestone_id = ?", id).Update("milestone_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&db.GitMilestone{}, "id = ?", id).Error
	})
}

// ── Assignees ────────────────────────────────────────────────────────────

func (s *Store) SetGitIssueAssignees(ctx context.Context, issueID string, userIDs []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("issue_id = ?", issueID).Delete(&db.GitIssueAssignee{}).Error; err != nil {
			return err
		}
		for _, id := range userIDs {
			if err := tx.Create(&db.GitIssueAssignee{IssueID: issueID, UserID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetGitIssueAssignees(ctx context.Context, issueIDs []string) (map[string][]*db.User, error) {
	out := map[string][]*db.User{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		IssueID string
		db.User
	}
	err := s.db.WithContext(ctx).Table("git_issue_assignees").Select("git_issue_assignees.issue_id, users.*").
		Joins("JOIN users ON users.id = git_issue_assignees.user_id").
		Where("git_issue_assignees.issue_id IN ?", issueIDs).Order("users.username ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		u := rows[i].User
		out[rows[i].IssueID] = append(out[rows[i].IssueID], &u)
	}
	return out, nil
}

// ── Comments ─────────────────────────────────────────────────────────────

func (s *Store) CreateGitComment(ctx context.Context, c *db.GitComment) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		updates := map[string]any{"updated_at": time.Now().UTC()}
		if c.Type == "comment" {
			updates["comment_count"] = gorm.Expr("comment_count + 1")
		}
		return tx.Model(&db.GitIssue{}).Where("id = ?", c.IssueID).Updates(updates).Error
	})
}

func (s *Store) GetGitComment(ctx context.Context, id string) (*db.GitComment, error) {
	var c db.GitComment
	err := s.db.WithContext(ctx).First(&c, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

func (s *Store) ListGitComments(ctx context.Context, issueID string, commentsOnly bool, limit, offset int) ([]*db.GitComment, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitComment{}).Where("issue_id = ?", issueID)
	if commentsOnly {
		q = q.Where("type = ?", "comment")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitComment
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at ASC").Find(&out).Error
	return out, total, err
}

func (s *Store) UpdateGitComment(ctx context.Context, c *db.GitComment) error {
	return s.db.WithContext(ctx).Save(c).Error
}

func (s *Store) DeleteGitComment(ctx context.Context, c *db.GitComment) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&db.GitComment{}, "id = ?", c.ID).Error; err != nil {
			return err
		}
		if c.Type == "comment" {
			return tx.Model(&db.GitIssue{}).Where("id = ? AND comment_count > 0", c.IssueID).UpdateColumn("comment_count", gorm.Expr("comment_count - 1")).Error
		}
		return nil
	})
}

// Participants of an issue, author plus commenters plus assignees plus reviewers
func (s *Store) GitIssueParticipants(ctx context.Context, issue *db.GitIssue) ([]string, error) {
	ids := map[string]bool{issue.AuthorID: true}
	var authors []string
	if err := s.db.WithContext(ctx).Model(&db.GitComment{}).Where("issue_id = ?", issue.ID).Distinct().Pluck("author_id", &authors).Error; err != nil {
		return nil, err
	}
	for _, a := range authors {
		ids[a] = true
	}
	var assignees []string
	if err := s.db.WithContext(ctx).Model(&db.GitIssueAssignee{}).Where("issue_id = ?", issue.ID).Pluck("user_id", &assignees).Error; err != nil {
		return nil, err
	}
	for _, a := range assignees {
		ids[a] = true
	}
	var reviewers []string
	if err := s.db.WithContext(ctx).Model(&db.GitReviewRequest{}).Where("issue_id = ? AND user_id <> ''", issue.ID).Pluck("user_id", &reviewers).Error; err != nil {
		return nil, err
	}
	for _, r := range reviewers {
		ids[r] = true
	}
	var subs []db.GitIssueSubscription
	if err := s.db.WithContext(ctx).Where("issue_id = ?", issue.ID).Find(&subs).Error; err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sub.Subscribed {
			ids[sub.UserID] = true
		} else {
			delete(ids, sub.UserID)
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out, nil
}

// ── Reactions ────────────────────────────────────────────────────────────

func (s *Store) AddGitReaction(ctx context.Context, r *db.GitReaction) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	var existing db.GitReaction
	if err := s.db.WithContext(ctx).First(&existing, "target_type = ? AND target_id = ? AND user_id = ? AND emoji = ?", r.TargetType, r.TargetID, r.UserID, r.Emoji).Error; err == nil {
		return nil
	}
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *Store) RemoveGitReaction(ctx context.Context, targetType, targetID, userID, emoji string) error {
	return s.db.WithContext(ctx).Where("target_type = ? AND target_id = ? AND user_id = ? AND emoji = ?", targetType, targetID, userID, emoji).Delete(&db.GitReaction{}).Error
}

func (s *Store) ListGitReactions(ctx context.Context, targetType string, targetIDs []string) (map[string][]*db.GitReaction, error) {
	out := map[string][]*db.GitReaction{}
	if len(targetIDs) == 0 {
		return out, nil
	}
	var rows []*db.GitReaction
	if err := s.db.WithContext(ctx).Where("target_type = ? AND target_id IN ?", targetType, targetIDs).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.TargetID] = append(out[r.TargetID], r)
	}
	return out, nil
}

// ── Dependencies ─────────────────────────────────────────────────────────

func (s *Store) AddGitIssueDependency(ctx context.Context, issueID, dependsOnID string) error {
	var existing db.GitIssueDependency
	if err := s.db.WithContext(ctx).First(&existing, "issue_id = ? AND depends_on_id = ?", issueID, dependsOnID).Error; err == nil {
		return nil
	}
	return s.db.WithContext(ctx).Create(&db.GitIssueDependency{IssueID: issueID, DependsOnID: dependsOnID}).Error
}

func (s *Store) RemoveGitIssueDependency(ctx context.Context, issueID, dependsOnID string) error {
	return s.db.WithContext(ctx).Where("issue_id = ? AND depends_on_id = ?", issueID, dependsOnID).Delete(&db.GitIssueDependency{}).Error
}

func (s *Store) ListGitIssueBlockers(ctx context.Context, issueID string) ([]*db.GitIssue, error) {
	var out []*db.GitIssue
	err := s.db.WithContext(ctx).Joins("JOIN git_issue_dependencies d ON d.depends_on_id = git_issues.id").Where("d.issue_id = ?", issueID).Find(&out).Error
	return out, err
}

func (s *Store) ListGitIssueBlocking(ctx context.Context, issueID string) ([]*db.GitIssue, error) {
	var out []*db.GitIssue
	err := s.db.WithContext(ctx).Joins("JOIN git_issue_dependencies d ON d.issue_id = git_issues.id").Where("d.depends_on_id = ?", issueID).Find(&out).Error
	return out, err
}

func (s *Store) CountOpenGitBlockers(ctx context.Context, issueIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		IssueID string
		Count   int64
	}
	err := s.db.WithContext(ctx).Table("git_issue_dependencies").Select("git_issue_dependencies.issue_id, COUNT(*) as count").
		Joins("JOIN git_issues ON git_issues.id = git_issue_dependencies.depends_on_id").
		Where("git_issue_dependencies.issue_id IN ? AND git_issues.state = ?", issueIDs, db.IssueStateOpen).Group("git_issue_dependencies.issue_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = r.Count
	}
	return out, nil
}

// ── Time tracking ────────────────────────────────────────────────────────

func (s *Store) CreateGitTrackedTime(ctx context.Context, t *db.GitTrackedTime) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *Store) GetGitTrackedTime(ctx context.Context, issueID, id string) (*db.GitTrackedTime, error) {
	var t db.GitTrackedTime
	err := s.db.WithContext(ctx).First(&t, "issue_id = ? AND id = ?", issueID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

func (s *Store) ListGitTrackedTime(ctx context.Context, issueID string, limit, offset int) ([]*db.GitTrackedTime, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitTrackedTime{}).Where("issue_id = ?", issueID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitTrackedTime
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

func (s *Store) SumGitTrackedTime(ctx context.Context, issueIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		IssueID string
		Total   int64
	}
	err := s.db.WithContext(ctx).Model(&db.GitTrackedTime{}).Select("issue_id, COALESCE(SUM(seconds),0) as total").
		Where("issue_id IN ?", issueIDs).Group("issue_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = r.Total
	}
	return out, nil
}

func (s *Store) DeleteGitTrackedTime(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitTrackedTime{}, "id = ?", id).Error
}

// ── Subscriptions ────────────────────────────────────────────────────────

func (s *Store) SetGitIssueSubscription(ctx context.Context, issueID, userID string, subscribed bool) error {
	var existing db.GitIssueSubscription
	err := s.db.WithContext(ctx).First(&existing, "issue_id = ? AND user_id = ?", issueID, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.WithContext(ctx).Create(&db.GitIssueSubscription{IssueID: issueID, UserID: userID, Subscribed: subscribed}).Error
	}
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&existing).Update("subscribed", subscribed).Error
}

// Explicit subscriptions of many users on one issue
func (s *Store) GetGitIssueSubscriptions(ctx context.Context, issueID string, userIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []db.GitIssueSubscription
	if err := s.db.WithContext(ctx).Where("issue_id = ? AND user_id IN ?", issueID, userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserID] = r.Subscribed
	}
	return out, nil
}

// Explicit subscriptions of one user across many issues
func (s *Store) GetGitIssueSubscriptionsForUser(ctx context.Context, issueIDs []string, userID string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(issueIDs) == 0 || userID == "" {
		return out, nil
	}
	var rows []db.GitIssueSubscription
	if err := s.db.WithContext(ctx).Where("issue_id IN ? AND user_id = ?", issueIDs, userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = r.Subscribed
	}
	return out, nil
}

func (s *Store) GetGitIssueSubscription(ctx context.Context, issueID, userID string) (*bool, error) {
	var existing db.GitIssueSubscription
	err := s.db.WithContext(ctx).First(&existing, "issue_id = ? AND user_id = ?", issueID, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing.Subscribed, nil
}

// Closed and reopened events of many issues, oldest first
type IssueStateEvent struct {
	IssueID   string
	Type      string
	CreatedAt time.Time
}

func (s *Store) ListGitIssueStateEvents(ctx context.Context, issueIDs []string) (map[string][]IssueStateEvent, error) {
	out := map[string][]IssueStateEvent{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []IssueStateEvent
	err := s.db.WithContext(ctx).Model(&db.GitComment{}).Select("issue_id, type, created_at").
		Where("issue_id IN ? AND type IN ?", issueIDs, []string{"closed", "reopened"}).Order("created_at ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = append(out[r.IssueID], r)
	}
	return out, nil
}
