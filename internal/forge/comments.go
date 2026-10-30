package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Reactions accepted on issues and comments
var Reactions = []string{"+1", "-1", "laugh", "hooray", "confused", "heart", "rocket", "eyes"}

func validReaction(e string) bool {
	return slices.Contains(Reactions, e)
}

// Reports whether the actor may comment on an issue
func (m *Manager) canComment(repo *db.GitRepository, actor Actor, issue *db.GitIssue) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return fmt.Errorf("%w: sign in to comment", ErrForbidden)
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if issue.Locked && !actor.CanWrite {
		return ErrLocked
	}
	return nil
}

// Posts a comment with references, mentions, notifications, webhooks, and actions
func (m *Manager) CreateComment(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, body string, viaEmail bool) (*db.GitComment, error) {
	if err := m.canComment(repo, actor, issue); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: a comment body is required", ErrInvalid)
	}
	if len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("%w: comment is too large", ErrInvalid)
	}
	c := &db.GitComment{
		ID:       uuid.New().String(),
		IssueID:  issue.ID,
		AuthorID: actor.User.ID,
		Type:     EventComment,
		Body:     body,
		Payload:  "{}",
		ViaEmail: viaEmail,
	}
	if err := m.store.CreateGitComment(ctx, c); err != nil {
		return nil, err
	}
	issue.CommentCount++
	_ = m.store.SetGitIssueSubscription(ctx, issue.ID, actor.User.ID, true)
	m.recordReferences(ctx, repo, issue, actor, body)
	m.Notify(ctx, Notification{
		Repo: repo, Issue: issue, Kind: m.notifyKind(issue), Actor: actor.User, Reason: "commented",
		Subject:   fmt.Sprintf("[%s/%s] %s (%s)", repo.Namespace, repo.Name, issue.Title, issue.Ref()),
		Text:      fmt.Sprintf("%s commented on %s:\n\n%s", actor.username(), issue.Ref(), body),
		URL:       m.IssueURL(ctx, repo, issue) + "#comment-" + c.ID,
		Mentioned: m.resolveMentions(ctx, repo, mentions(body)),
		CommentID: c.ID,
	})
	m.dispatchIssue(ctx, repo, issue, actor, "created", c)
	m.Trigger(ctx, repo, "issue_comment", "created", "", "", actor.User, m.commentPayloadMap(ctx, repo, issue, c))
	return c, nil
}

// Edits a comment body keeping the previous one in the payload history
func (m *Manager) UpdateComment(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, c *db.GitComment, body string) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if c.Type != EventComment {
		return fmt.Errorf("%w: timeline events cannot be edited", ErrInvalid)
	}
	if c.AuthorID != actor.User.ID && !actor.CanWrite {
		return ErrForbidden
	}
	if strings.TrimSpace(body) == "" || len(body) > MaxBodyBytes {
		return fmt.Errorf("%w: a comment body is required", ErrInvalid)
	}
	if body == c.Body {
		return nil
	}
	var payload struct {
		History []struct {
			Body     string `json:"body"`
			EditedAt string `json:"edited_at"`
			EditedBy string `json:"edited_by"`
		} `json:"history"`
	}
	_ = json.Unmarshal([]byte(c.Payload), &payload)
	payload.History = append(payload.History, struct {
		Body     string `json:"body"`
		EditedAt string `json:"edited_at"`
		EditedBy string `json:"edited_by"`
	}{Body: c.Body, EditedAt: time.Now().UTC().Format(time.RFC3339), EditedBy: actor.User.ID})
	if len(payload.History) > 20 {
		payload.History = payload.History[len(payload.History)-20:]
	}
	raw, _ := json.Marshal(payload)
	now := time.Now()
	c.Body = body
	c.Payload = string(raw)
	c.EditedAt = &now
	if err := m.store.UpdateGitComment(ctx, c); err != nil {
		return err
	}
	m.recordReferences(ctx, repo, issue, actor, body)
	m.dispatchIssue(ctx, repo, issue, actor, "edited", c)
	m.Trigger(ctx, repo, "issue_comment", "edited", "", "", actor.User, m.commentPayloadMap(ctx, repo, issue, c))
	return nil
}

// Deletes a comment by its author or a writer
func (m *Manager) DeleteComment(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, c *db.GitComment) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if c.Type != EventComment && !actor.CanAdmin {
		return fmt.Errorf("%w: timeline events cannot be deleted", ErrInvalid)
	}
	if c.AuthorID != actor.User.ID && !actor.CanWrite {
		return ErrForbidden
	}
	if err := m.store.DeleteGitComment(ctx, c); err != nil {
		return err
	}
	m.dispatchIssue(ctx, repo, issue, actor, "deleted", c)
	m.Trigger(ctx, repo, "issue_comment", "deleted", "", "", actor.User, m.commentPayloadMap(ctx, repo, issue, c))
	return nil
}

// Loads a comment and checks it belongs to the repository
func (m *Manager) Comment(ctx context.Context, repo *db.GitRepository, id string) (*db.GitIssue, *db.GitComment, error) {
	c, err := m.store.GetGitComment(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if c == nil {
		return nil, nil, fmt.Errorf("%w: comment", ErrNotFound)
	}
	issue, err := m.store.GetGitIssueByID(ctx, c.IssueID)
	if err != nil {
		return nil, nil, err
	}
	if issue == nil || issue.RepoID != repo.ID {
		return nil, nil, fmt.Errorf("%w: comment", ErrNotFound)
	}
	return issue, c, nil
}

func (m *Manager) commentPayloadMap(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, c *db.GitComment) map[string]any {
	out := m.issuePayloadMap(ctx, repo, issue)
	out["comment"] = map[string]any{"id": c.ID, "body": c.Body, "author_id": c.AuthorID, "created_at": c.CreatedAt.UTC().Format(time.RFC3339)}
	return out
}

// Adds or removes a reaction on an issue body or a comment
func (m *Manager) React(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, commentID, emoji string, add bool) ([]*db.GitReaction, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return nil, fmt.Errorf("%w: sign in to react", ErrForbidden)
	}
	if !validReaction(emoji) {
		return nil, fmt.Errorf("%w: unknown reaction %q", ErrInvalid, emoji)
	}
	targetType, targetID := "issue", issue.ID
	if commentID != "" {
		owner, c, err := m.Comment(ctx, repo, commentID)
		if err != nil {
			return nil, err
		}
		if owner.ID != issue.ID {
			return nil, fmt.Errorf("%w: comment", ErrNotFound)
		}
		targetType, targetID = "comment", c.ID
	}
	var err error
	if add {
		err = m.store.AddGitReaction(ctx, &db.GitReaction{ID: uuid.New().String(), TargetType: targetType, TargetID: targetID, UserID: actor.User.ID, Emoji: emoji})
	} else {
		err = m.store.RemoveGitReaction(ctx, targetType, targetID, actor.User.ID, emoji)
	}
	if err != nil {
		return nil, err
	}
	all, err := m.store.ListGitReactions(ctx, targetType, []string{targetID})
	if err != nil {
		return nil, err
	}
	return all[targetID], nil
}
