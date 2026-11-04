package forge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
)

// Fields of a milestone
type MilestoneInput struct {
	Title       *string
	Description *string
	DueDate     *time.Time
	ClearDue    bool
	State       *string
}

func (m *Manager) ListMilestones(ctx context.Context, repo *db.GitRepository, state string, q pages.Query, limit, offset int) ([]*db.GitMilestone, int64, error) {
	return m.store.ListGitMilestones(ctx, repo.ID, state, q, limit, offset)
}

func (m *Manager) Milestone(ctx context.Context, repo *db.GitRepository, id string) (*db.GitMilestone, error) {
	ms, err := m.store.GetGitMilestone(ctx, repo.ID, id)
	if err != nil {
		return nil, err
	}
	if ms == nil {
		return nil, fmt.Errorf("%w: milestone", ErrNotFound)
	}
	return ms, nil
}

func (m *Manager) CreateMilestone(ctx context.Context, repo *db.GitRepository, actor Actor, in MilestoneInput) (*db.GitMilestone, error) {
	if !actor.CanWrite {
		return nil, ErrForbidden
	}
	if in.Title == nil || !validTitle(*in.Title) {
		return nil, fmt.Errorf("%w: a title is required", ErrInvalid)
	}
	ms := &db.GitMilestone{ID: uuid.New().String(), RepoID: repo.ID, Title: strings.TrimSpace(*in.Title), State: "open", DueDate: in.DueDate}
	if in.Description != nil {
		ms.Description = *in.Description
	}
	if err := m.store.CreateGitMilestone(ctx, ms); err != nil {
		return nil, err
	}
	return ms, nil
}

func (m *Manager) UpdateMilestone(ctx context.Context, repo *db.GitRepository, actor Actor, ms *db.GitMilestone, in MilestoneInput) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if in.Title != nil {
		if !validTitle(*in.Title) {
			return fmt.Errorf("%w: a title is required", ErrInvalid)
		}
		ms.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		ms.Description = *in.Description
	}
	if in.DueDate != nil || in.ClearDue {
		ms.DueDate = in.DueDate
	}
	if in.State != nil && *in.State != ms.State {
		switch *in.State {
		case "closed":
			now := time.Now()
			ms.ClosedAt = &now
		case "open":
			ms.ClosedAt = nil
		default:
			return fmt.Errorf("%w: state must be open or closed", ErrInvalid)
		}
		ms.State = *in.State
	}
	return m.store.UpdateGitMilestone(ctx, ms)
}

func (m *Manager) DeleteMilestone(ctx context.Context, repo *db.GitRepository, actor Actor, ms *db.GitMilestone) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	return m.store.DeleteGitMilestone(ctx, ms.ID)
}

// Open and closed issue counts per milestone
func (m *Manager) MilestoneCounts(ctx context.Context, milestones []*db.GitMilestone) map[string][2]int64 {
	ids := make([]string, 0, len(milestones))
	for _, ms := range milestones {
		ids = append(ids, ms.ID)
	}
	counts, _ := m.store.GitMilestoneCounts(ctx, ids)
	return counts
}
