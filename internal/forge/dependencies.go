package forge

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
)

// Resolves a blocker reference written as a number or ns/name#number
func (m *Manager) resolveDependency(ctx context.Context, repo *db.GitRepository, actor Actor, ref string) (*db.GitIssue, error) {
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "#"))
	if n, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return m.Issue(ctx, repo, n)
	}
	refs := ParseReferences(ref)
	if len(refs) != 1 {
		return nil, fmt.Errorf("%w: reference must be a number or ns/name#number", ErrInvalid)
	}
	_, issue := m.resolveReference(ctx, repo, actor, refs[0])
	if issue == nil {
		return nil, fmt.Errorf("%w: issue %s", ErrNotFound, ref)
	}
	return issue, nil
}

// Marks an issue as blocked by another, refusing cycles
func (m *Manager) AddDependency(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, ref string) error {
	if !actor.CanWrite && !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	blocker, err := m.resolveDependency(ctx, repo, actor, ref)
	if err != nil {
		return err
	}
	if blocker.ID == issue.ID {
		return fmt.Errorf("%w: an issue cannot block itself", ErrInvalid)
	}
	if m.dependsOn(ctx, blocker, issue.ID, 0) {
		return fmt.Errorf("%w: that would create a dependency cycle", ErrInvalid)
	}
	if err := m.store.AddGitIssueDependency(ctx, issue.ID, blocker.ID); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventDependencyAdded, dependencyEvent(blocker, m.repoFullName(ctx, repo, blocker)))
	blocking := dependencyEvent(issue, repo.Namespace+"/"+repo.Name)
	blocking["blocking"] = true
	_, _ = m.AddEvent(ctx, blocker, actor.User, EventDependencyAdded, blocking)
	return nil
}

// Timeline payload naming an issue on either side of a dependency
func dependencyEvent(issue *db.GitIssue, repoFullName string) map[string]any {
	return map[string]any{"id": issue.ID, "number": issue.Number, "title": issue.Title, "repo_id": issue.RepoID, "repo": repoFullName, "is_pull": issue.IsPull}
}

// ns/name of an issue's repository, the current repo when it matches
func (m *Manager) repoFullName(ctx context.Context, current *db.GitRepository, issue *db.GitIssue) string {
	if current != nil && current.ID == issue.RepoID {
		return current.Namespace + "/" + current.Name
	}
	r, _ := m.store.GetGitRepositoryByID(ctx, issue.RepoID)
	if r == nil {
		return ""
	}
	return r.Namespace + "/" + r.Name
}

// Walks blockers of an issue looking for a target id
func (m *Manager) dependsOn(ctx context.Context, issue *db.GitIssue, targetID string, depth int) bool {
	if depth > 50 {
		return false
	}
	blockers, _ := m.store.ListGitIssueBlockers(ctx, issue.ID)
	for _, b := range blockers {
		if b.ID == targetID || m.dependsOn(ctx, b, targetID, depth+1) {
			return true
		}
	}
	return false
}

// Removes a blocker
func (m *Manager) RemoveDependency(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, ref string) error {
	if !actor.CanWrite && !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	blocker, err := m.resolveDependency(ctx, repo, actor, ref)
	if err != nil {
		return err
	}
	if err := m.store.RemoveGitIssueDependency(ctx, issue.ID, blocker.ID); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventDependencyRemoved, dependencyEvent(blocker, m.repoFullName(ctx, repo, blocker)))
	return nil
}

// Blockers of an issue and issues it blocks
func (m *Manager) Dependencies(ctx context.Context, issue *db.GitIssue) ([]*db.GitIssue, []*db.GitIssue, error) {
	blockedBy, err := m.store.ListGitIssueBlockers(ctx, issue.ID)
	if err != nil {
		return nil, nil, err
	}
	blocking, err := m.store.ListGitIssueBlocking(ctx, issue.ID)
	if err != nil {
		return nil, nil, err
	}
	return blockedBy, blocking, nil
}
