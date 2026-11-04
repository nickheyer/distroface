package forge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/pages"
)

// Starting column layouts
var projectTemplates = map[string][]string{
	"basic_kanban": {"To do", "In progress", "Done"},
	"bug_triage":   {"Needs triage", "High priority", "Low priority", "Closed"},
}

// Project with its columns and placements
type ProjectView struct {
	Project *db.GitProject
	Columns []*db.GitProjectColumn
	Cards   []*db.GitProjectIssue
	Issues  map[string]*db.GitIssue
	Open    int64
	Closed  int64
}

// Loads a board
func (m *Manager) Project(ctx context.Context, id string) (*db.GitProject, error) {
	p, err := m.store.GetGitProject(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("%w: project", ErrNotFound)
	}
	return p, nil
}

// Repository a board belongs to, nil for organization boards
func (m *Manager) ProjectRepo(ctx context.Context, p *db.GitProject) (*db.GitRepository, error) {
	if p.RepoID == nil {
		return nil, nil
	}
	return m.store.GetGitRepositoryByID(ctx, *p.RepoID)
}

// Reports whether issues of a repository may sit on a board
func (m *Manager) projectCoversRepo(ctx context.Context, p *db.GitProject, repo *db.GitRepository) bool {
	if p.RepoID != nil {
		return *p.RepoID == repo.ID
	}
	if p.OrgID != nil {
		return *p.OrgID == m.orgIDOf(ctx, repo)
	}
	return false
}

// Reports whether a user may read or manage an organization board
func (m *Manager) OrgProjectLevel(ctx context.Context, user *auth.AuthenticatedUser, orgID string) (canRead, canWrite bool) {
	canRead, canWrite, _ = m.OrgProjectAccess(ctx, user, orgID)
	return canRead, canWrite
}

// Read, manage, and membership of a user on an organization's boards
func (m *Manager) OrgProjectAccess(ctx context.Context, user *auth.AuthenticatedUser, orgID string) (canRead, canWrite, member bool) {
	org, _ := m.store.GetOrganizationByID(ctx, orgID)
	if org == nil {
		return false, false, false
	}
	if user == nil || user.IsAnonymous() {
		return true, false, false
	}
	row, _ := m.store.GetOrgMember(ctx, org.ID, user.ID)
	if row == nil {
		return true, false, false
	}
	return true, row.Role == db.OrgRoleOwner || row.Role == db.OrgRoleAdmin, true
}

// Reports whether a viewer may see a board, private boards need write or membership
func (m *Manager) ProjectVisible(ctx context.Context, user *auth.AuthenticatedUser, p *db.GitProject, repo *db.GitRepository) bool {
	if p == nil {
		return false
	}
	if p.Public {
		return true
	}
	if p.RepoID != nil {
		if repo == nil || repo.ID != *p.RepoID {
			repo, _ = m.store.GetGitRepositoryByID(ctx, *p.RepoID)
		}
		if repo == nil {
			return false
		}
		return m.git.Access().Levels(ctx, user, repo).Unit(db.GitUnitProjects) >= gitrepo.LevelWrite
	}
	if p.OrgID != nil {
		_, _, member := m.OrgProjectAccess(ctx, user, *p.OrgID)
		return member
	}
	return false
}

// Creates a board with template columns, private unless asked otherwise
func (m *Manager) CreateProject(ctx context.Context, repo *db.GitRepository, orgID string, creator *auth.AuthenticatedUser, title, description, template string, public bool) (*db.GitProject, error) {
	if !validTitle(title) {
		return nil, fmt.Errorf("%w: a title is required", ErrInvalid)
	}
	p := &db.GitProject{ID: uuid.New().String(), Title: strings.TrimSpace(title), Description: description, State: "open", Public: public, CreatorID: creator.ID}
	if repo != nil {
		p.RepoID = &repo.ID
	} else if orgID != "" {
		p.OrgID = &orgID
	} else {
		return nil, fmt.Errorf("%w: a repository or organization is required", ErrInvalid)
	}
	var columns []*db.GitProjectColumn
	for i, name := range projectTemplates[template] {
		columns = append(columns, &db.GitProjectColumn{ID: uuid.New().String(), ProjectID: p.ID, Title: name, Position: i, IsDefault: i == 0})
	}
	if err := m.store.CreateGitProject(ctx, p, columns); err != nil {
		return nil, err
	}
	return p, nil
}

// Lists boards of a scope, private ones only when the viewer may see them
func (m *Manager) ListProjects(ctx context.Context, repo *db.GitRepository, orgID, state string, publicOnly bool, q pages.Query, limit, offset int) ([]*db.GitProject, int64, error) {
	repoID := ""
	if repo != nil {
		repoID = repo.ID
	}
	return m.store.ListGitProjects(ctx, repoID, orgID, state, publicOnly, q, limit, offset)
}

func (m *Manager) UpdateProject(ctx context.Context, p *db.GitProject, title, description *string, public *bool) error {
	if title != nil {
		if !validTitle(*title) {
			return fmt.Errorf("%w: a title is required", ErrInvalid)
		}
		p.Title = strings.TrimSpace(*title)
	}
	if description != nil {
		p.Description = *description
	}
	if public != nil {
		p.Public = *public
	}
	return m.store.UpdateGitProject(ctx, p)
}

func (m *Manager) CloseProject(ctx context.Context, p *db.GitProject, closed bool) error {
	p.State = "open"
	if closed {
		p.State = "closed"
	}
	return m.store.UpdateGitProject(ctx, p)
}

func (m *Manager) DeleteProject(ctx context.Context, p *db.GitProject) error {
	return m.store.DeleteGitProject(ctx, p.ID)
}

// Full board view with columns, cards, and their issues
func (m *Manager) ProjectView(ctx context.Context, p *db.GitProject, withCards bool) (*ProjectView, error) {
	columns, err := m.store.ListGitProjectColumns(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	view := &ProjectView{Project: p, Columns: columns, Issues: map[string]*db.GitIssue{}}
	counts, _ := m.store.GitProjectCounts(ctx, []string{p.ID})
	view.Open, view.Closed = counts[p.ID][0], counts[p.ID][1]
	if !withCards {
		return view, nil
	}
	cards, err := m.store.ListGitProjectIssues(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	view.Cards = cards
	ids := make([]string, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, c.IssueID)
	}
	issues, _ := m.store.GetGitIssuesByIDs(ctx, ids)
	for _, is := range issues {
		view.Issues[is.ID] = is
	}
	return view, nil
}

func (m *Manager) CreateColumn(ctx context.Context, p *db.GitProject, title, color string) (*db.GitProjectColumn, error) {
	if !validTitle(title) {
		return nil, fmt.Errorf("%w: a title is required", ErrInvalid)
	}
	existing, _ := m.store.ListGitProjectColumns(ctx, p.ID)
	c := &db.GitProjectColumn{ID: uuid.New().String(), ProjectID: p.ID, Title: strings.TrimSpace(title), Color: color, Position: len(existing), IsDefault: len(existing) == 0}
	if err := m.store.CreateGitProjectColumn(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (m *Manager) Column(ctx context.Context, p *db.GitProject, id string) (*db.GitProjectColumn, error) {
	c, err := m.store.GetGitProjectColumn(ctx, p.ID, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("%w: column", ErrNotFound)
	}
	return c, nil
}

func (m *Manager) UpdateColumn(ctx context.Context, c *db.GitProjectColumn, title, color *string, isDefault *bool) error {
	if title != nil {
		if !validTitle(*title) {
			return fmt.Errorf("%w: a title is required", ErrInvalid)
		}
		c.Title = strings.TrimSpace(*title)
	}
	if color != nil {
		c.Color = *color
	}
	if isDefault != nil {
		c.IsDefault = *isDefault
	}
	return m.store.UpdateGitProjectColumn(ctx, c)
}

func (m *Manager) DeleteColumn(ctx context.Context, p *db.GitProject, c *db.GitProjectColumn) error {
	columns, _ := m.store.ListGitProjectColumns(ctx, p.ID)
	if len(columns) <= 1 {
		return fmt.Errorf("%w: a board keeps at least one column", ErrInvalid)
	}
	return m.store.DeleteGitProjectColumn(ctx, p.ID, c.ID)
}

func (m *Manager) MoveColumn(ctx context.Context, p *db.GitProject, c *db.GitProjectColumn, position int) error {
	return m.store.MoveGitProjectColumn(ctx, p.ID, c.ID, position)
}

// Default column of a board, the first when none is marked
func (m *Manager) defaultColumn(ctx context.Context, p *db.GitProject) (*db.GitProjectColumn, error) {
	columns, err := m.store.ListGitProjectColumns(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: the board has no columns", ErrInvalid)
	}
	for _, c := range columns {
		if c.IsDefault {
			return c, nil
		}
	}
	return columns[0], nil
}

// Places or moves an issue on a board
func (m *Manager) placeIssue(ctx context.Context, p *db.GitProject, issue *db.GitIssue, columnID string, position int) error {
	if columnID == "" {
		c, err := m.defaultColumn(ctx, p)
		if err != nil {
			return err
		}
		columnID = c.ID
	} else if _, err := m.Column(ctx, p, columnID); err != nil {
		return err
	}
	return m.store.PlaceGitProjectIssue(ctx, p.ID, issue.ID, columnID, position)
}

// Adds an issue to a board, the issue must belong to the board's scope
func (m *Manager) AddIssueToProject(ctx context.Context, p *db.GitProject, actor Actor, issue *db.GitIssue, columnID string) error {
	repo, err := m.store.GetGitRepositoryByID(ctx, issue.RepoID)
	if err != nil || repo == nil {
		return fmt.Errorf("%w: issue", ErrNotFound)
	}
	if !m.projectCoversRepo(ctx, p, repo) {
		return fmt.Errorf("%w: issue belongs to another scope", ErrInvalid)
	}
	if err := m.placeIssue(ctx, p, issue, columnID, 1<<30); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventProjectAdded, map[string]string{"project_id": p.ID, "title": p.Title})
	return nil
}

// Moves a card within a board
func (m *Manager) MoveIssue(ctx context.Context, p *db.GitProject, actor Actor, issue *db.GitIssue, columnID string, position int) error {
	placement, _ := m.store.GetGitProjectIssue(ctx, issue.ID)
	if placement == nil || placement.ProjectID != p.ID {
		return fmt.Errorf("%w: issue is not on this board", ErrNotFound)
	}
	if err := m.placeIssue(ctx, p, issue, columnID, position); err != nil {
		return err
	}
	if placement.ColumnID != columnID {
		col, _ := m.Column(ctx, p, columnID)
		title := ""
		if col != nil {
			title = col.Title
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, EventProjectMoved, map[string]string{"project_id": p.ID, "column_id": columnID, "column": title})
	}
	return nil
}

// Removes a card from a board
func (m *Manager) RemoveIssueFromProject(ctx context.Context, p *db.GitProject, actor Actor, issue *db.GitIssue) error {
	if err := m.store.RemoveGitProjectIssue(ctx, p.ID, issue.ID); err != nil {
		return err
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, EventProjectRemoved, map[string]string{"project_id": p.ID, "title": p.Title})
	return nil
}

// Issue by id for board operations
func (m *Manager) IssueByID(ctx context.Context, id string) (*db.GitIssue, error) {
	issue, err := m.store.GetGitIssueByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, fmt.Errorf("%w: issue", ErrNotFound)
	}
	return issue, nil
}

var _ = time.Now
