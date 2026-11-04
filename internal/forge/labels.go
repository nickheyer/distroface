package forge

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
)

var colorPattern = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

// Normalizes a hex color, empty picks a default
func normalizeColor(c string) (string, error) {
	c = strings.TrimSpace(c)
	if c == "" {
		return "#6b7280", nil
	}
	if !colorPattern.MatchString(c) {
		return "", fmt.Errorf("%w: color must be a six digit hex value", ErrInvalid)
	}
	return "#" + strings.ToLower(strings.TrimPrefix(c, "#")), nil
}

// Scope prefix of an exclusive label, the part before the slash
func exclusiveScope(name string) string {
	if i := strings.Index(name, "/"); i > 0 {
		return name[:i]
	}
	return ""
}

// Fields of a label
type LabelInput struct {
	Name        string
	Color       string
	Description string
	Exclusive   bool
}

// Lists repository labels with inherited organization labels
func (m *Manager) ListLabels(ctx context.Context, repo *db.GitRepository, orgID string, q pages.Query, limit, offset int) ([]*db.GitLabel, int64, error) {
	repoID := ""
	if repo != nil {
		repoID = repo.ID
		if orgID == "" {
			orgID = m.orgIDOf(ctx, repo)
		}
	}
	return m.store.ListGitLabels(ctx, repoID, orgID, q, limit, offset)
}

// Creates a label on a repository or an organization
func (m *Manager) CreateLabel(ctx context.Context, repo *db.GitRepository, orgID string, in LabelInput) (*db.GitLabel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 {
		return nil, fmt.Errorf("%w: a label name is required", ErrInvalid)
	}
	color, err := normalizeColor(in.Color)
	if err != nil {
		return nil, err
	}
	l := &db.GitLabel{ID: uuid.New().String(), Name: name, Color: color, Description: strings.TrimSpace(in.Description), CreatedAt: time.Now()}
	if in.Exclusive {
		l.ExclusiveScope = exclusiveScope(name)
	}
	if repo != nil {
		l.RepoID = &repo.ID
	} else if orgID != "" {
		l.OrgID = &orgID
	} else {
		return nil, fmt.Errorf("%w: a repository or organization is required", ErrInvalid)
	}
	existing, _, err := m.store.ListGitLabels(ctx, derefOr(l.RepoID), derefOr(l.OrgID), pages.Query{}, 0, 0)
	if err != nil {
		return nil, err
	}
	for _, e := range existing {
		if strings.EqualFold(e.Name, name) && sameOwner(e, l) {
			return nil, fmt.Errorf("%w: label %q already exists", ErrConflict, name)
		}
	}
	if err := m.store.CreateGitLabel(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func sameOwner(a, b *db.GitLabel) bool {
	return derefOr(a.RepoID) == derefOr(b.RepoID) && derefOr(a.OrgID) == derefOr(b.OrgID)
}

// Loads a label owned by the repository or organization
func (m *Manager) Label(ctx context.Context, repo *db.GitRepository, orgID, id string) (*db.GitLabel, error) {
	l, err := m.store.GetGitLabel(ctx, id)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("%w: label", ErrNotFound)
	}
	if repo != nil && l.RepoID != nil && *l.RepoID == repo.ID {
		return l, nil
	}
	if orgID == "" && repo != nil {
		orgID = m.orgIDOf(ctx, repo)
	}
	if l.OrgID != nil && orgID != "" && *l.OrgID == orgID {
		return l, nil
	}
	return nil, fmt.Errorf("%w: label", ErrNotFound)
}

// Edits set fields of a label
func (m *Manager) UpdateLabel(ctx context.Context, l *db.GitLabel, name, color, description *string, exclusive *bool) error {
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || len(n) > 100 {
			return fmt.Errorf("%w: a label name is required", ErrInvalid)
		}
		l.Name = n
	}
	if color != nil {
		c, err := normalizeColor(*color)
		if err != nil {
			return err
		}
		l.Color = c
	}
	if description != nil {
		l.Description = strings.TrimSpace(*description)
	}
	if exclusive != nil {
		if *exclusive {
			l.ExclusiveScope = exclusiveScope(l.Name)
		} else {
			l.ExclusiveScope = ""
		}
	} else if l.ExclusiveScope != "" {
		l.ExclusiveScope = exclusiveScope(l.Name)
	}
	return m.store.UpdateGitLabel(ctx, l)
}

// Deletes a label and its assignments
func (m *Manager) DeleteLabel(ctx context.Context, l *db.GitLabel) error {
	return m.store.DeleteGitLabel(ctx, l.ID)
}

// Open and closed counts per label
func (m *Manager) LabelCounts(ctx context.Context, labels []*db.GitLabel) map[string][2]int64 {
	counts, _ := m.store.GitLabelCounts(ctx, labelIDs(labels))
	return counts
}
