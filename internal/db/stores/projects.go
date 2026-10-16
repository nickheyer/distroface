package stores

import (
	"context"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

func (s *Store) CreateGitProject(ctx context.Context, p *db.GitProject, columns []*db.GitProjectColumn) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		for i, c := range columns {
			c.ID = uuid.New().String()
			c.ProjectID = p.ID
			c.Position = i
			if err := tx.Create(c).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetGitProject(ctx context.Context, id string) (*db.GitProject, error) {
	var p db.GitProject
	err := s.db.WithContext(ctx).First(&p, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &p, err
}

// ProjectsQuery allowlists project list filters
var ProjectsQuery = pages.Spec{
	Fields: map[string]string{"title": "title"},
	Text:   []string{"title", "description"},
}

func (s *Store) GetGitProjectsByIDs(ctx context.Context, ids []string) (map[string]*db.GitProject, error) {
	out := make(map[string]*db.GitProject, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []*db.GitProject
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, p := range rows {
		out[p.ID] = p
	}
	return out, nil
}

func (s *Store) ListGitProjects(ctx context.Context, repoID, orgID, state string, publicOnly bool, q pages.Query, limit, offset int) ([]*db.GitProject, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.GitProject{})
	if repoID != "" {
		tx = tx.Where("repo_id = ?", repoID)
	} else {
		tx = tx.Where("org_id = ?", orgID)
	}
	if state != "" {
		tx = tx.Where("state = ?", state)
	}
	if publicOnly {
		tx = tx.Where("public = ?", true)
	}
	tx = tx.Scopes(ProjectsQuery.Scope(q))
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitProject
	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	err := tx.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

func (s *Store) UpdateGitProject(ctx context.Context, p *db.GitProject) error {
	return s.db.WithContext(ctx).Save(p).Error
}

func (s *Store) DeleteGitProject(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitProject{}, "id = ?", id).Error
}

func (s *Store) ListGitProjectColumns(ctx context.Context, projectID string) ([]*db.GitProjectColumn, error) {
	var out []*db.GitProjectColumn
	err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("position ASC").Find(&out).Error
	return out, err
}

func (s *Store) GetGitProjectColumn(ctx context.Context, projectID, id string) (*db.GitProjectColumn, error) {
	var c db.GitProjectColumn
	err := s.db.WithContext(ctx).First(&c, "project_id = ? AND id = ?", projectID, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &c, err
}

func (s *Store) CreateGitProjectColumn(ctx context.Context, c *db.GitProjectColumn) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	var max int
	if err := s.db.WithContext(ctx).Model(&db.GitProjectColumn{}).Where("project_id = ?", c.ProjectID).Select("COALESCE(MAX(position),-1)").Scan(&max).Error; err != nil {
		return err
	}
	c.Position = max + 1
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *Store) UpdateGitProjectColumn(ctx context.Context, c *db.GitProjectColumn) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if c.IsDefault {
			if err := tx.Model(&db.GitProjectColumn{}).Where("project_id = ? AND id <> ?", c.ProjectID, c.ID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Save(c).Error
	})
}

// Moves the column and renumbers the rest
func (s *Store) MoveGitProjectColumn(ctx context.Context, projectID, id string, position int) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cols []*db.GitProjectColumn
		if err := tx.Where("project_id = ?", projectID).Order("position ASC").Find(&cols).Error; err != nil {
			return err
		}
		var moving *db.GitProjectColumn
		rest := make([]*db.GitProjectColumn, 0, len(cols))
		for _, c := range cols {
			if c.ID == id {
				moving = c
				continue
			}
			rest = append(rest, c)
		}
		if moving == nil {
			return gorm.ErrRecordNotFound
		}
		if position < 0 {
			position = 0
		}
		if position > len(rest) {
			position = len(rest)
		}
		ordered := append(rest[:position:position], append([]*db.GitProjectColumn{moving}, rest[position:]...)...)
		for i, c := range ordered {
			if err := tx.Model(&db.GitProjectColumn{}).Where("id = ?", c.ID).Update("position", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Deletes a column after moving its cards to the default column
func (s *Store) DeleteGitProjectColumn(ctx context.Context, projectID, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var fallback db.GitProjectColumn
		err := tx.Where("project_id = ? AND id <> ?", projectID, id).Order("is_default DESC, position ASC").First(&fallback).Error
		if err == gorm.ErrRecordNotFound {
			if err := tx.Where("project_id = ? AND column_id = ?", projectID, id).Delete(&db.GitProjectIssue{}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := tx.Model(&db.GitProjectIssue{}).Where("project_id = ? AND column_id = ?", projectID, id).Update("column_id", fallback.ID).Error; err != nil {
			return err
		}
		return tx.Delete(&db.GitProjectColumn{}, "project_id = ? AND id = ?", projectID, id).Error
	})
}

func (s *Store) ListGitProjectIssues(ctx context.Context, projectID string) ([]*db.GitProjectIssue, error) {
	var out []*db.GitProjectIssue
	err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("column_id ASC, position ASC").Find(&out).Error
	return out, err
}

func (s *Store) GetGitProjectIssue(ctx context.Context, issueID string) (*db.GitProjectIssue, error) {
	var pi db.GitProjectIssue
	err := s.db.WithContext(ctx).First(&pi, "issue_id = ?", issueID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &pi, err
}

func (s *Store) GetGitProjectIssues(ctx context.Context, issueIDs []string) (map[string]*db.GitProjectIssue, error) {
	out := map[string]*db.GitProjectIssue{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []*db.GitProjectIssue
	if err := s.db.WithContext(ctx).Where("issue_id IN ?", issueIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.IssueID] = r
	}
	return out, nil
}

// Places or moves an issue, one board per issue
func (s *Store) PlaceGitProjectIssue(ctx context.Context, projectID, issueID, columnID string, position int) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("issue_id = ?", issueID).Delete(&db.GitProjectIssue{}).Error; err != nil {
			return err
		}
		var siblings []*db.GitProjectIssue
		if err := tx.Where("project_id = ? AND column_id = ?", projectID, columnID).Order("position ASC").Find(&siblings).Error; err != nil {
			return err
		}
		if position < 0 || position > len(siblings) {
			position = len(siblings)
		}
		row := &db.GitProjectIssue{ProjectID: projectID, IssueID: issueID, ColumnID: columnID, Position: position}
		ordered := append(siblings[:position:position], append([]*db.GitProjectIssue{row}, siblings[position:]...)...)
		for i, pi := range ordered {
			if pi == row {
				continue
			}
			if err := tx.Model(&db.GitProjectIssue{}).Where("project_id = ? AND issue_id = ?", pi.ProjectID, pi.IssueID).Update("position", i).Error; err != nil {
				return err
			}
		}
		row.Position = position
		return tx.Create(row).Error
	})
}

func (s *Store) RemoveGitProjectIssue(ctx context.Context, projectID, issueID string) error {
	return s.db.WithContext(ctx).Where("project_id = ? AND issue_id = ?", projectID, issueID).Delete(&db.GitProjectIssue{}).Error
}

func (s *Store) GitProjectCounts(ctx context.Context, projectIDs []string) (map[string][2]int64, error) {
	out := map[string][2]int64{}
	if len(projectIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ProjectID string
		State     string
		Count     int64
	}
	err := s.db.WithContext(ctx).Table("git_project_issues").Select("git_project_issues.project_id, git_issues.state, COUNT(*) as count").
		Joins("JOIN git_issues ON git_issues.id = git_project_issues.issue_id").
		Where("git_project_issues.project_id IN ?", projectIDs).Group("git_project_issues.project_id, git_issues.state").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		c := out[r.ProjectID]
		if r.State == db.IssueStateOpen {
			c[0] = r.Count
		} else {
			c[1] = r.Count
		}
		out[r.ProjectID] = c
	}
	return out, nil
}
