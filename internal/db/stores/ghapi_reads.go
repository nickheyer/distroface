package stores

import "context"

// Ids of every comment in a repository, newest first
func (s *Store) ListGitCommentIDsByRepo(ctx context.Context, repoID string) ([]string, error) {
	var ids []string
	err := s.db.WithContext(ctx).Table("git_comments").
		Joins("JOIN git_issues ON git_issues.id = git_comments.issue_id").
		Where("git_issues.repo_id = ?", repoID).
		Order("git_comments.created_at DESC").
		Pluck("git_comments.id", &ids).Error
	return ids, err
}
