package services

import (
	"context"
	"regexp"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── Teams ────────────────────────────────────────────────────────────────

var teamNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// Loads an org and checks the caller is an owner or admin, or holds manage
func (s *OrganizationService) teamOrgAdmin(ctx context.Context, orgID string) (*storage.Organization, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	org, err := s.teamOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID); canManage {
		return org, nil
	}
	member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
	if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
		return nil, connect.Errorf(connect.CodePermissionDenied, "organization admin required")
	}
	return org, nil
}

// Loads an org the caller may read, by id or name
func (s *OrganizationService) teamOrg(ctx context.Context, orgID string) (*storage.Organization, error) {
	if orgID == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "org_id is required")
	}
	org, err := s.store.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if org == nil {
		if org, err = s.store.GetOrganization(ctx, orgID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}
	if org == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "organization not found")
	}
	// Teams, their members, and their repositories are the organization's own business
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID); member == nil && !s.enforcer.HasPermission(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage) {
		return nil, connect.Errorf(connect.CodeNotFound, "organization not found")
	}
	return org, nil
}

func (s *OrganizationService) team(ctx context.Context, org *storage.Organization, id string) (*storage.OrgTeam, error) {
	if id == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "team id is required")
	}
	t, err := s.store.GetOrgTeam(ctx, org.ID, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if t == nil {
		if t, err = s.store.GetOrgTeamByName(ctx, org.ID, id); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}
	if t == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "team not found")
	}
	return t, nil
}

func teamLevelProto(level string) v1.TeamUnitLevel {
	switch level {
	case storage.GitLevelRead:
		return v1.TeamUnitLevel_TEAM_UNIT_LEVEL_READ
	case storage.GitLevelWrite, storage.GitLevelAdmin:
		return v1.TeamUnitLevel_TEAM_UNIT_LEVEL_WRITE
	}
	return v1.TeamUnitLevel_TEAM_UNIT_LEVEL_NONE
}

func teamLevelString(level v1.TeamUnitLevel) string {
	switch level {
	case v1.TeamUnitLevel_TEAM_UNIT_LEVEL_READ:
		return storage.GitLevelRead
	case v1.TeamUnitLevel_TEAM_UNIT_LEVEL_WRITE:
		return storage.GitLevelWrite
	}
	return storage.GitLevelNone
}

// Validates and encodes unit permissions, unknown units are refused
func teamUnitsFromProto(in []*v1.TeamUnitPermission) (string, error) {
	units := map[string]string{}
	for _, u := range gitrepo.AllUnits {
		units[u] = storage.GitLevelNone
	}
	for _, p := range in {
		if _, ok := units[p.Unit]; !ok {
			return "", connect.Errorf(connect.CodeInvalidArgument, "unknown unit %q", p.Unit)
		}
		units[p.Unit] = teamLevelString(p.Level)
	}
	return stores.EncodeTeamUnits(units), nil
}

func (s *OrganizationService) teamToProto(ctx context.Context, t *storage.OrgTeam) *v1.Team {
	out := &v1.Team{
		Id: t.ID, OrgId: t.OrgID, Name: t.Name, Description: t.Description, IsDefault: t.IsDefault,
		CanCreateRepos: t.CanCreateRepos, IncludesAllRepos: t.IncludesAllRepos, AdminAccess: t.AdminAccess,
		CreatedAt: timestamppb.New(t.CreatedAt), UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
	units := stores.DecodeTeamUnits(t.Units)
	for _, u := range gitrepo.AllUnits {
		out.Units = append(out.Units, &v1.TeamUnitPermission{Unit: u, Level: teamLevelProto(units[u])})
	}
	if t.IsDefault {
		n, _ := s.store.GetOrgMemberCount(ctx, t.OrgID)
		out.MemberCount = int32(n)
	} else if n, err := s.store.CountOrgTeamMembers(ctx, t.ID); err == nil {
		out.MemberCount = int32(n)
	}
	if t.IncludesAllRepos {
		repos, _ := s.store.ListGitRepositoriesByNamespace(ctx, s.orgNameOf(ctx, t.OrgID))
		out.RepoCount = int32(len(repos))
	} else if n, err := s.store.CountOrgTeamRepos(ctx, t.ID); err == nil {
		out.RepoCount = int32(n)
	}
	return out
}

func (s *OrganizationService) orgNameOf(ctx context.Context, orgID string) string {
	org, _ := s.store.GetOrganizationByID(ctx, orgID)
	if org == nil {
		return ""
	}
	return org.Name
}

func (s *OrganizationService) ListTeams(ctx context.Context, req *v1.ListTeamsRequest) (*v1.ListTeamsResponse, error) {
	org, err := s.teamOrg(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.EnsureDefaultTeam(ctx, org.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	limit, offset := pages.Parse(req.Page)
	q := pages.ParseQuery(req.Page)
	if err := stores.TeamsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	teams, total, err := s.store.ListOrgTeams(ctx, org.ID, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListTeamsResponse{Page: pages.Info(offset, limit, total)}
	for _, t := range teams {
		out.Teams = append(out.Teams, s.teamToProto(ctx, t))
	}
	return out, nil
}

func (s *OrganizationService) GetTeam(ctx context.Context, req *v1.GetTeamRequest) (*v1.GetTeamResponse, error) {
	org, err := s.teamOrg(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.Id)
	if err != nil {
		return nil, err
	}
	return &v1.GetTeamResponse{Team: s.teamToProto(ctx, t)}, nil
}

func (s *OrganizationService) CreateTeam(ctx context.Context, req *v1.CreateTeamRequest) (*v1.CreateTeamResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if !teamNamePattern.MatchString(name) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid team name")
	}
	if existing, _ := s.store.GetOrgTeamByName(ctx, org.ID, name); existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "team %q already exists", name)
	}
	units, err := teamUnitsFromProto(req.Units)
	if err != nil {
		return nil, err
	}
	t := &storage.OrgTeam{
		OrgID: org.ID, Name: name, Description: strings.TrimSpace(req.Description),
		CanCreateRepos: req.CanCreateRepos, IncludesAllRepos: req.IncludesAllRepos, AdminAccess: req.AdminAccess, Units: units,
	}
	if err := s.store.CreateOrgTeam(ctx, t); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.CreateTeamResponse{Team: s.teamToProto(ctx, t)}, nil
}

func (s *OrganizationService) UpdateTeam(ctx context.Context, req *v1.UpdateTeamRequest) (*v1.UpdateTeamResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.Id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil && !t.IsDefault {
		name := strings.TrimSpace(*req.Name)
		if !teamNamePattern.MatchString(name) {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid team name")
		}
		if existing, _ := s.store.GetOrgTeamByName(ctx, org.ID, name); existing != nil && existing.ID != t.ID {
			return nil, connect.Errorf(connect.CodeAlreadyExists, "team %q already exists", name)
		}
		t.Name = name
	}
	if req.Description != nil {
		t.Description = strings.TrimSpace(*req.Description)
	}
	if req.CanCreateRepos != nil {
		t.CanCreateRepos = *req.CanCreateRepos
	}
	if req.IncludesAllRepos != nil && !t.IsDefault {
		t.IncludesAllRepos = *req.IncludesAllRepos
	}
	if req.AdminAccess != nil {
		t.AdminAccess = *req.AdminAccess
	}
	if req.SetUnits {
		units, err := teamUnitsFromProto(req.Units)
		if err != nil {
			return nil, err
		}
		t.Units = units
	}
	if err := s.store.UpdateOrgTeam(ctx, t); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UpdateTeamResponse{Team: s.teamToProto(ctx, t)}, nil
}

func (s *OrganizationService) DeleteTeam(ctx context.Context, req *v1.DeleteTeamRequest) (*v1.DeleteTeamResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.Id)
	if err != nil {
		return nil, err
	}
	if t.IsDefault {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "the default team cannot be deleted")
	}
	if err := s.store.DeleteOrgTeam(ctx, t.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteTeamResponse{}, nil
}

func (s *OrganizationService) ListTeamMembers(ctx context.Context, req *v1.ListTeamMembersRequest) (*v1.ListTeamMembersResponse, error) {
	org, err := s.teamOrg(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.TeamId)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	var members []*storage.OrgMember
	var total int64
	if t.IsDefault {
		members, total, err = s.store.ListOrgMembers(ctx, org.ID, pages.ParseQuery(req.Page), limit, offset)
	} else {
		members, total, err = s.store.ListOrgTeamMembers(ctx, t.ID, limit, offset)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListTeamMembersResponse{Page: pages.Info(offset, limit, total)}
	for _, m := range members {
		out.Members = append(out.Members, orgMemberToProto(m))
	}
	return out, nil
}

func (s *OrganizationService) AddTeamMember(ctx context.Context, req *v1.AddTeamMemberRequest) (*v1.AddTeamMemberResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.TeamId)
	if err != nil {
		return nil, err
	}
	if t.IsDefault {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "every member already belongs to the default team")
	}
	if req.UserId == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "user_id is required")
	}
	member, _ := s.store.GetOrgMember(ctx, org.ID, req.UserId)
	if member == nil {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "user is not a member of the organization")
	}
	if err := s.store.AddOrgTeamMember(ctx, t.ID, req.UserId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if s.git != nil {
		s.git.Audit(ctx, teamAudit(ctx, "Team/add-member", org.Name+"/"+t.Name+" "+req.UserId))
	}
	return &v1.AddTeamMemberResponse{}, nil
}

func (s *OrganizationService) RemoveTeamMember(ctx context.Context, req *v1.RemoveTeamMemberRequest) (*v1.RemoveTeamMemberResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.TeamId)
	if err != nil {
		return nil, err
	}
	if t.IsDefault {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "remove the user from the organization instead")
	}
	if err := s.store.RemoveOrgTeamMember(ctx, t.ID, req.UserId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.RemoveTeamMemberResponse{}, nil
}

func (s *OrganizationService) ListTeamRepos(ctx context.Context, req *v1.ListTeamReposRequest) (*v1.ListTeamReposResponse, error) {
	org, err := s.teamOrg(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.TeamId)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	var repos []*storage.GitRepository
	var total int64
	if t.IncludesAllRepos {
		all, err := s.store.ListGitRepositoriesByNamespace(ctx, org.Name)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		total = int64(len(all))
		if offset > len(all) {
			offset = len(all)
		}
		repos = all[offset:min(offset+limit, len(all))]
	} else if repos, total, err = s.store.ListOrgTeamRepos(ctx, t.ID, limit, offset); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListTeamReposResponse{Page: pages.Info(offset, limit, total)}
	for _, r := range repos {
		out.Repos = append(out.Repos, &v1.TeamRepo{RepoId: r.ID, Namespace: r.Namespace, Name: r.Name})
	}
	return out, nil
}

// Git repository of the org named in a team request
func (s *OrganizationService) teamRepo(ctx context.Context, org *storage.Organization, name string) (*storage.GitRepository, error) {
	name = strings.TrimPrefix(name, org.Name+"/")
	if name == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "repo_name is required")
	}
	repo, err := s.store.GetGitRepository(ctx, org.Name, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "repository %s/%s not found", org.Name, name)
	}
	return repo, nil
}

func (s *OrganizationService) AddTeamRepo(ctx context.Context, req *v1.AddTeamRepoRequest) (*v1.AddTeamRepoResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.TeamId)
	if err != nil {
		return nil, err
	}
	if t.IncludesAllRepos {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "the team already includes every repository")
	}
	repo, err := s.teamRepo(ctx, org, req.RepoName)
	if err != nil {
		return nil, err
	}
	if err := s.store.AddOrgTeamRepo(ctx, t.ID, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.AddTeamRepoResponse{}, nil
}

func (s *OrganizationService) RemoveTeamRepo(ctx context.Context, req *v1.RemoveTeamRepoRequest) (*v1.RemoveTeamRepoResponse, error) {
	org, err := s.teamOrgAdmin(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	t, err := s.team(ctx, org, req.TeamId)
	if err != nil {
		return nil, err
	}
	repo, err := s.teamRepo(ctx, org, req.RepoName)
	if err != nil {
		return nil, err
	}
	if err := s.store.RemoveOrgTeamRepo(ctx, t.ID, repo.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.RemoveTeamRepoResponse{}, nil
}
