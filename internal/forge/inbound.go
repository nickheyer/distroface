package forge

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

var quotedHistoryPattern = regexp.MustCompile(`(?m)^(On .+ wrote:|-- |-----Original Message-----|From: .+|Sent from my .+)$`)

// Strips quoted history and signatures from a mailed reply
func stripMailReply(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if loc := quotedHistoryPattern.FindStringIndex(body); loc != nil {
		body = body[:loc[0]]
	}
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Posts a mailed reply as a comment by the token's user with a via email marker
func (m *Manager) HandleInboundMail(ctx context.Context, token *db.MailInboundToken, from, body string) error {
	issue, err := m.store.GetGitIssueByID(ctx, token.IssueID)
	if err != nil {
		return err
	}
	if issue == nil {
		return fmt.Errorf("%w: issue for mailed reply", ErrNotFound)
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, issue.RepoID)
	if err != nil || repo == nil {
		return fmt.Errorf("%w: repository for mailed reply", ErrNotFound)
	}
	u, err := m.store.GetUserByID(ctx, token.UserID)
	if err != nil || u == nil {
		return fmt.Errorf("%w: user for mailed reply", ErrNotFound)
	}
	text := stripMailReply(body)
	if text == "" {
		return fmt.Errorf("%w: empty reply", ErrInvalid)
	}
	principal := &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: []string{"user"}, Provider: u.AuthProvider}
	if u.Email != nil {
		principal.Email = *u.Email
	}
	level := m.git.Access().Level(ctx, principal, repo, db.GitUnitIssues)
	if level < gitrepo.LevelRead {
		return ErrForbidden
	}
	actor := Actor{User: principal, CanWrite: level >= gitrepo.LevelWrite}
	_, err = m.CreateComment(ctx, repo, actor, issue, text, true)
	return err
}
