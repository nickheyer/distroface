package forge

import (
	"context"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/webhook"
)

// Pull request section of issue and pull webhooks
func (m *Manager) pullPayload(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, pr *db.GitPullRequest) *webhook.PullRequestPayload {
	p := &webhook.PullRequestPayload{
		Number: issue.Number, Title: issue.Title, Base: pr.BaseBranch, Head: pr.HeadBranch, HeadSHA: pr.HeadSHA,
		Draft: pr.Draft, Merged: pr.MergedAt != nil, MergeCommit: pr.MergeCommitSHA, URL: m.IssueURL(ctx, repo, issue),
	}
	if head, _ := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID); head != nil {
		p.HeadRepo = head.Namespace + "/" + head.Name
	}
	if u, _ := m.store.GetUserByID(ctx, issue.AuthorID); u != nil {
		p.Author = u.Username
	}
	return p
}
