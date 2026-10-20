package services

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.ProjectServiceHandler = (*ProjectService)(nil)

type ProjectService struct {
	store *stores.Store
	forge *forge.Manager
	log   *logger.Logger
}

func NewProjectService(store *stores.Store, fm *forge.Manager, log *logger.Logger) *ProjectService {
	return &ProjectService{store: store, forge: fm, log: log}
}

// Board scope resolved to a repository or organization with the caller's rights
type projectScope struct {
	repo     *storage.GitRepository
	orgID    string
	canWrite bool
	// Collaborators with write or org members see private boards
	seesPrivate bool
}

// Resolves the scope a board lives in and checks read access
func (s *ProjectService) scopeFor(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, orgID string) (*projectScope, error) {
	if repo != nil {
		levels := s.forge.Git().Access().Levels(ctx, user, repo)
		if !gitrepo.UnitEnabled(repo, storage.GitUnitProjects) && !levels.IsOwner && !levels.CanAdmin {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "projects are disabled on this repository")
		}
		level := levels.Unit(storage.GitUnitProjects)
		if level < gitrepo.LevelRead {
			return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		return &projectScope{repo: repo, canWrite: level >= gitrepo.LevelWrite && !repo.IsArchived, seesPrivate: level >= gitrepo.LevelWrite}, nil
	}
	canRead, canWrite, member := s.forge.OrgProjectAccess(ctx, user, orgID)
	if !canRead {
		return nil, connect.Errorf(connect.CodeNotFound, "organization not found")
	}
	return &projectScope{orgID: orgID, canWrite: canWrite, seesPrivate: member}, nil
}

// Scope from a request naming a repository or an organization
func (s *ProjectService) scopeFromRequest(ctx context.Context, namespace, name, orgID string) (*auth.AuthenticatedUser, *projectScope, error) {
	user := auth.UserFromContext(ctx)
	if name != "" || orgID == "" {
		repo, err := visibleGitRepo(ctx, s.store, s.forge.Git().Access(), user, namespace, name)
		if err != nil {
			return nil, nil, err
		}
		sc, err := s.scopeFor(ctx, user, repo, "")
		return user, sc, err
	}
	sc, err := s.scopeFor(ctx, user, nil, orgID)
	return user, sc, err
}

// Loads a board and resolves its scope
func (s *ProjectService) project(ctx context.Context, id string) (*auth.AuthenticatedUser, *storage.GitProject, *projectScope, error) {
	user := auth.UserFromContext(ctx)
	p, err := s.forge.Project(ctx, id)
	if err != nil {
		return nil, nil, nil, mapForgeErr(err)
	}
	repo, err := s.forge.ProjectRepo(ctx, p)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if p.RepoID != nil {
		if repo == nil || !s.forge.Git().Access().CanSee(ctx, user, repo) {
			return nil, nil, nil, connect.Errorf(connect.CodeNotFound, "project not found")
		}
	}
	orgID := ""
	if p.OrgID != nil {
		orgID = *p.OrgID
	}
	sc, err := s.scopeFor(ctx, user, repo, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	if !p.Public && !sc.seesPrivate {
		return nil, nil, nil, connect.Errorf(connect.CodeNotFound, "project not found")
	}
	return user, p, sc, nil
}

func (s *ProjectService) requireWrite(ctx context.Context, user *auth.AuthenticatedUser, sc *projectScope) error {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return err
	}
	if user == nil || user.IsAnonymous() {
		return connect.NewError(connect.CodeUnauthenticated, "")
	}
	if !sc.canWrite {
		return connect.Errorf(connect.CodePermissionDenied, "write access required")
	}
	return nil
}

func (s *ProjectService) toProto(ctx context.Context, p *storage.GitProject, withCards bool) (*v1.Project, error) {
	view, err := s.forge.ProjectView(ctx, p, withCards)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.Project{
		Id: p.ID, Title: p.Title, Description: p.Description, State: p.State, Public: p.Public,
		OpenCount: int32(view.Open), ClosedCount: int32(view.Closed),
		CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
	if u, _ := s.store.GetUserByID(ctx, p.CreatorID); u != nil {
		out.Creator = u.Username
	}
	if p.OrgID != nil {
		out.OrgId = *p.OrgID
	}
	if repo, _ := s.forge.ProjectRepo(ctx, p); repo != nil {
		out.RepoFullName = repo.Namespace + "/" + repo.Name
	}
	cardsByColumn := map[string][]*v1.ProjectCard{}
	if withCards {
		ids := make([]string, 0, len(view.Issues))
		for id := range view.Issues {
			ids = append(ids, id)
		}
		labels, _ := s.store.GetGitIssueLabels(ctx, ids)
		assignees, _ := s.store.GetGitIssueAssignees(ctx, ids)
		viewer := auth.UserFromContext(ctx)
		access := s.forge.Git().Access()
		repos := map[string]*storage.GitRepository{}
		for _, c := range view.Cards {
			is := view.Issues[c.IssueID]
			if is == nil {
				continue
			}
			if repos[is.RepoID] == nil {
				repos[is.RepoID], _ = s.store.GetGitRepositoryByID(ctx, is.RepoID)
			}
			// A board shows a card only when its issue is readable on its own repository
			r := repos[is.RepoID]
			unit := storage.GitUnitIssues
			if is.IsPull {
				unit = storage.GitUnitPulls
			}
			if r == nil || access.Level(ctx, viewer, r, unit) < gitrepo.LevelRead {
				continue
			}
			card := &v1.ProjectCard{IssueId: is.ID, Number: is.Number, Title: is.Title, State: is.State, IsPull: is.IsPull, Position: int32(c.Position), RepoFullName: r.Namespace + "/" + r.Name}
			for _, l := range labels[is.ID] {
				card.Labels = append(card.Labels, l.Name)
			}
			for _, u := range assignees[is.ID] {
				card.Assignees = append(card.Assignees, u.Username)
			}
			cardsByColumn[c.ColumnID] = append(cardsByColumn[c.ColumnID], card)
		}
	}
	for _, c := range view.Columns {
		out.Columns = append(out.Columns, projectColumnProto(c, cardsByColumn[c.ID]))
	}
	return out, nil
}

func projectColumnProto(c *storage.GitProjectColumn, cards []*v1.ProjectCard) *v1.ProjectColumn {
	return &v1.ProjectColumn{Id: c.ID, Title: c.Title, Color: c.Color, Position: int32(c.Position), IsDefault: c.IsDefault, Cards: cards}
}

func projectTemplateName(t v1.ProjectTemplate) string {
	switch t {
	case v1.ProjectTemplate_PROJECT_TEMPLATE_BUG_TRIAGE:
		return "bug_triage"
	case v1.ProjectTemplate_PROJECT_TEMPLATE_NONE:
		return "none"
	}
	return "basic_kanban"
}

func (s *ProjectService) CreateProject(ctx context.Context, req *v1.CreateProjectRequest) (*v1.CreateProjectResponse, error) {
	user, sc, err := s.scopeFromRequest(ctx, req.Namespace, req.Name, req.OrgId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	p, err := s.forge.CreateProject(ctx, sc.repo, sc.orgID, user, req.Title, req.Description, projectTemplateName(req.Template), req.Public)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.toProto(ctx, p, true)
	if err != nil {
		return nil, err
	}
	return &v1.CreateProjectResponse{Project: out}, nil
}

func (s *ProjectService) GetProject(ctx context.Context, req *v1.GetProjectRequest) (*v1.GetProjectResponse, error) {
	_, p, _, err := s.project(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	out, err := s.toProto(ctx, p, true)
	if err != nil {
		return nil, err
	}
	return &v1.GetProjectResponse{Project: out}, nil
}

func (s *ProjectService) ListProjects(ctx context.Context, req *v1.ListProjectsRequest) (*v1.ListProjectsResponse, error) {
	_, sc, err := s.scopeFromRequest(ctx, req.Namespace, req.Name, req.OrgId)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.forge.ListProjects(ctx, sc.repo, sc.orgID, req.State, !sc.seesPrivate, pages.ParseQuery(req.Page), limit, offset)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	out := &v1.ListProjectsResponse{Page: pages.Info(offset, limit, total)}
	for _, p := range rows {
		proto, err := s.toProto(ctx, p, false)
		if err != nil {
			return nil, err
		}
		out.Projects = append(out.Projects, proto)
	}
	return out, nil
}

func (s *ProjectService) UpdateProject(ctx context.Context, req *v1.UpdateProjectRequest) (*v1.UpdateProjectResponse, error) {
	user, p, sc, err := s.project(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	if err := s.forge.UpdateProject(ctx, p, req.Title, req.Description, req.Public); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.toProto(ctx, p, false)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateProjectResponse{Project: out}, nil
}

func (s *ProjectService) DeleteProject(ctx context.Context, req *v1.DeleteProjectRequest) (*v1.DeleteProjectResponse, error) {
	user, p, sc, err := s.project(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	if err := s.forge.DeleteProject(ctx, p); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteProjectResponse{}, nil
}

func (s *ProjectService) CloseProject(ctx context.Context, req *v1.CloseProjectRequest) (*v1.CloseProjectResponse, error) {
	user, p, sc, err := s.project(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	if err := s.forge.CloseProject(ctx, p, req.Closed); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.toProto(ctx, p, false)
	if err != nil {
		return nil, err
	}
	return &v1.CloseProjectResponse{Project: out}, nil
}

func (s *ProjectService) CreateColumn(ctx context.Context, req *v1.CreateColumnRequest) (*v1.CreateColumnResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	c, err := s.forge.CreateColumn(ctx, p, req.Title, req.Color)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.CreateColumnResponse{Column: projectColumnProto(c, nil)}, nil
}

func (s *ProjectService) UpdateColumn(ctx context.Context, req *v1.UpdateColumnRequest) (*v1.UpdateColumnResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	c, err := s.forge.Column(ctx, p, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.UpdateColumn(ctx, c, req.Title, req.Color, req.IsDefault); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.UpdateColumnResponse{Column: projectColumnProto(c, nil)}, nil
}

func (s *ProjectService) DeleteColumn(ctx context.Context, req *v1.DeleteColumnRequest) (*v1.DeleteColumnResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	c, err := s.forge.Column(ctx, p, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.DeleteColumn(ctx, p, c); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteColumnResponse{}, nil
}

func (s *ProjectService) MoveColumn(ctx context.Context, req *v1.MoveColumnRequest) (*v1.MoveColumnResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	c, err := s.forge.Column(ctx, p, req.Id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	if err := s.forge.MoveColumn(ctx, p, c, int(req.Position)); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.toProto(ctx, p, true)
	if err != nil {
		return nil, err
	}
	return &v1.MoveColumnResponse{Project: out}, nil
}

// Issue the caller may see for board placement
func (s *ProjectService) issueFor(ctx context.Context, user *auth.AuthenticatedUser, id string) (*storage.GitIssue, error) {
	issue, err := s.forge.IssueByID(ctx, id)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	repo, err := s.store.GetGitRepositoryByID(ctx, issue.RepoID)
	if err != nil || repo == nil || !s.forge.Git().Access().CanSee(ctx, user, repo) {
		return nil, connect.Errorf(connect.CodeNotFound, "issue not found")
	}
	return issue, nil
}

func (s *ProjectService) AddIssueToProject(ctx context.Context, req *v1.AddIssueToProjectRequest) (*v1.AddIssueToProjectResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	issue, err := s.issueFor(ctx, user, req.IssueId)
	if err != nil {
		return nil, err
	}
	if err := s.forge.AddIssueToProject(ctx, p, forge.Actor{User: user, CanWrite: true}, issue, req.ColumnId); err != nil {
		return nil, mapForgeErr(err)
	}
	placement, _ := s.store.GetGitProjectIssue(ctx, issue.ID)
	card := &v1.ProjectCard{IssueId: issue.ID, Number: issue.Number, Title: issue.Title, State: issue.State, IsPull: issue.IsPull}
	if placement != nil {
		card.Position = int32(placement.Position)
	}
	return &v1.AddIssueToProjectResponse{Card: card}, nil
}

func (s *ProjectService) MoveIssue(ctx context.Context, req *v1.MoveIssueRequest) (*v1.MoveIssueResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	issue, err := s.issueFor(ctx, user, req.IssueId)
	if err != nil {
		return nil, err
	}
	if err := s.forge.MoveIssue(ctx, p, forge.Actor{User: user, CanWrite: true}, issue, req.ColumnId, int(req.Position)); err != nil {
		return nil, mapForgeErr(err)
	}
	out, err := s.toProto(ctx, p, true)
	if err != nil {
		return nil, err
	}
	return &v1.MoveIssueResponse{Project: out}, nil
}

func (s *ProjectService) RemoveIssueFromProject(ctx context.Context, req *v1.RemoveIssueFromProjectRequest) (*v1.RemoveIssueFromProjectResponse, error) {
	user, p, sc, err := s.project(ctx, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, user, sc); err != nil {
		return nil, err
	}
	issue, err := s.issueFor(ctx, user, req.IssueId)
	if err != nil {
		return nil, err
	}
	if err := s.forge.RemoveIssueFromProject(ctx, p, forge.Actor{User: user, CanWrite: true}, issue); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.RemoveIssueFromProjectResponse{}, nil
}
