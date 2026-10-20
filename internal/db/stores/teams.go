package stores

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	"gorm.io/gorm"
)

// Name of the implicit team every member belongs to
const DefaultTeamName = "Members"

// Every unit written at the write level
func DefaultTeamUnits() map[string]string {
	units := map[string]string{}
	for _, u := range []string{db.GitUnitCode, db.GitUnitIssues, db.GitUnitPulls, db.GitUnitReleases, db.GitUnitWiki, db.GitUnitProjects, db.GitUnitPackages, db.GitUnitActions} {
		units[u] = db.GitLevelWrite
	}
	return units
}

// Encodes a unit map for storage
func EncodeTeamUnits(units map[string]string) string {
	b, _ := json.Marshal(units)
	return string(b)
}

// Decodes a stored unit map, missing units are none
func DecodeTeamUnits(raw string) map[string]string {
	out := map[string]string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Store) CreateOrgTeam(ctx context.Context, t *db.OrgTeam) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(t).Error
}

// Creates the default team once per org
func (s *Store) EnsureDefaultTeam(ctx context.Context, orgID string) (*db.OrgTeam, error) {
	var t db.OrgTeam
	err := s.db.WithContext(ctx).First(&t, "org_id = ? AND is_default = ?", orgID, true).Error
	if err == nil {
		return &t, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	t = db.OrgTeam{
		ID:               uuid.New().String(),
		OrgID:            orgID,
		Name:             DefaultTeamName,
		Description:      "Every organization member",
		IsDefault:        true,
		CanCreateRepos:   true,
		IncludesAllRepos: true,
		Units:            EncodeTeamUnits(DefaultTeamUnits()),
	}
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetOrgTeam(ctx context.Context, orgID, id string) (*db.OrgTeam, error) {
	var t db.OrgTeam
	err := s.db.WithContext(ctx).First(&t, "org_id = ? AND id = ?", orgID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

func (s *Store) GetOrgTeamByName(ctx context.Context, orgID, name string) (*db.OrgTeam, error) {
	var t db.OrgTeam
	err := s.db.WithContext(ctx).First(&t, "org_id = ? AND name = ?", orgID, name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

// TeamsQuery allowlists team list filters
var TeamsQuery = pages.Spec{
	Fields: map[string]string{"name": "name", "description": "description"},
	Text:   []string{"name", "description"},
}

func (s *Store) ListOrgTeams(ctx context.Context, orgID string, q pages.Query, limit, offset int) ([]*db.OrgTeam, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.OrgTeam{}).Where("org_id = ?", orgID).Scopes(TeamsQuery.Scope(q))
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.OrgTeam
	if limit > 0 {
		tx = tx.Limit(limit).Offset(offset)
	}
	err := tx.Order("is_default DESC, name ASC").Find(&out).Error
	return out, total, err
}

func (s *Store) UpdateOrgTeam(ctx context.Context, t *db.OrgTeam) error {
	return s.db.WithContext(ctx).Save(t).Error
}

func (s *Store) DeleteOrgTeam(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.OrgTeam{}, "id = ?", id).Error
}

func (s *Store) CountOrgTeams(ctx context.Context, orgID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.OrgTeam{}).Where("org_id = ?", orgID).Count(&n).Error
	return n, err
}

func (s *Store) AddOrgTeamMember(ctx context.Context, teamID, userID string) error {
	var existing db.OrgTeamMember
	if err := s.db.WithContext(ctx).First(&existing, "team_id = ? AND user_id = ?", teamID, userID).Error; err == nil {
		return nil
	}
	return s.db.WithContext(ctx).Create(&db.OrgTeamMember{TeamID: teamID, UserID: userID}).Error
}

func (s *Store) RemoveOrgTeamMember(ctx context.Context, teamID, userID string) error {
	return s.db.WithContext(ctx).Where("team_id = ? AND user_id = ?", teamID, userID).Delete(&db.OrgTeamMember{}).Error
}

func (s *Store) ListOrgTeamMembers(ctx context.Context, teamID string, limit, offset int) ([]*db.OrgMember, int64, error) {
	base := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&db.OrgMember{}).
			Joins("JOIN org_teams t ON t.org_id = org_members.org_id").
			Joins("JOIN org_team_members tm ON tm.team_id = t.id AND tm.user_id = org_members.user_id").
			Where("t.id = ?", teamID)
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.OrgMember
	err := base().Preload("User").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (s *Store) CountOrgTeamMembers(ctx context.Context, teamID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.OrgTeamMember{}).Where("team_id = ?", teamID).Count(&n).Error
	return n, err
}

func (s *Store) AddOrgTeamRepo(ctx context.Context, teamID, repoID string) error {
	var existing db.OrgTeamRepo
	if err := s.db.WithContext(ctx).First(&existing, "team_id = ? AND repo_id = ?", teamID, repoID).Error; err == nil {
		return nil
	}
	return s.db.WithContext(ctx).Create(&db.OrgTeamRepo{TeamID: teamID, RepoID: repoID}).Error
}

func (s *Store) RemoveOrgTeamRepo(ctx context.Context, teamID, repoID string) error {
	return s.db.WithContext(ctx).Where("team_id = ? AND repo_id = ?", teamID, repoID).Delete(&db.OrgTeamRepo{}).Error
}

func (s *Store) ListOrgTeamRepos(ctx context.Context, teamID string, limit, offset int) ([]*db.GitRepository, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitRepository{}).
		Joins("JOIN org_team_repos tr ON tr.repo_id = git_repositories.id").Where("tr.team_id = ?", teamID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitRepository
	err := q.Order("git_repositories.name ASC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (s *Store) CountOrgTeamRepos(ctx context.Context, teamID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.OrgTeamRepo{}).Where("team_id = ?", teamID).Count(&n).Error
	return n, err
}

// Teams of one org that grant the user access to a repo, the default team included
func (s *Store) TeamsForUserRepo(ctx context.Context, orgID, userID, repoID string) ([]*db.OrgTeam, error) {
	var out []*db.OrgTeam
	err := s.db.WithContext(ctx).
		Where("org_id = ? AND (is_default = ? OR id IN (SELECT team_id FROM org_team_members WHERE user_id = ?))", orgID, true, userID).
		Where("includes_all_repos = ? OR id IN (SELECT team_id FROM org_team_repos WHERE repo_id = ?)", true, repoID).
		Find(&out).Error
	return out, err
}

// Teams the user belongs to in one org, the default team included
func (s *Store) TeamsForUser(ctx context.Context, orgID, userID string) ([]*db.OrgTeam, error) {
	var out []*db.OrgTeam
	err := s.db.WithContext(ctx).
		Where("org_id = ? AND (is_default = ? OR id IN (SELECT team_id FROM org_team_members WHERE user_id = ?))", orgID, true, userID).
		Find(&out).Error
	return out, err
}

// User ids that belong to a team, default teams expand to every member
func (s *Store) TeamUserIDs(ctx context.Context, team *db.OrgTeam) ([]string, error) {
	var ids []string
	if team.IsDefault {
		err := s.db.WithContext(ctx).Model(&db.OrgMember{}).Where("org_id = ?", team.OrgID).Pluck("user_id", &ids).Error
		return ids, err
	}
	err := s.db.WithContext(ctx).Model(&db.OrgTeamMember{}).Where("team_id = ?", team.ID).Pluck("user_id", &ids).Error
	return ids, err
}

func (s *Store) GetOrgTeamsByIDs(ctx context.Context, ids []string) ([]*db.OrgTeam, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*db.OrgTeam
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error
	return out, err
}
