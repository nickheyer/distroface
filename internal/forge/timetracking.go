package forge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Records a manual time entry on a repository that tracks time
func (m *Manager) AddTrackedTime(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, seconds int64, note string) (*db.GitTrackedTime, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return nil, ErrForbidden
	}
	if !gitrepo.UnitsOf(repo).TimeTracking {
		return nil, fmt.Errorf("%w: time tracking is disabled on this repository", ErrDisabled)
	}
	if seconds <= 0 || seconds > 365*24*3600 {
		return nil, fmt.Errorf("%w: seconds must be positive and under a year", ErrInvalid)
	}
	t := &db.GitTrackedTime{ID: uuid.New().String(), IssueID: issue.ID, UserID: actor.User.ID, Seconds: seconds, Note: strings.TrimSpace(note)}
	if err := m.store.CreateGitTrackedTime(ctx, t); err != nil {
		return nil, err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventTimeTracked, map[string]any{"seconds": seconds, "note": t.Note, "id": t.ID})
	return t, nil
}

// Deletes an entry by its owner or a writer
func (m *Manager) DeleteTrackedTime(ctx context.Context, actor Actor, issue *db.GitIssue, id string) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	t, err := m.store.GetGitTrackedTime(ctx, issue.ID, id)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("%w: time entry", ErrNotFound)
	}
	if t.UserID != actor.User.ID && !actor.CanWrite {
		return ErrForbidden
	}
	return m.store.DeleteGitTrackedTime(ctx, t.ID)
}

// Seconds spent open, summed between the closed and reopened events
func OpenSeconds(issue *db.GitIssue, events []stores.IssueStateEvent, now time.Time) int64 {
	open, since := true, issue.CreatedAt
	var total time.Duration
	for _, e := range events {
		switch e.Type {
		case EventClosed:
			if open {
				total += e.CreatedAt.Sub(since)
				open = false
			}
		case EventReopened:
			if !open {
				since = e.CreatedAt
				open = true
			}
		}
	}
	if open {
		end := now
		if issue.State == db.IssueStateClosed && issue.ClosedAt != nil {
			end = *issue.ClosedAt
		}
		total += end.Sub(since)
	}
	if total < 0 {
		return 0
	}
	return int64(total.Seconds())
}
