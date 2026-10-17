package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.ActionsServiceHandler = (*ActionsService)(nil)

const actionsLogPoll = time.Second

type ActionsService struct {
	store   *stores.Store
	actions *actions.Manager
	manager *gitrepo.Manager
	log     *logger.Logger
}

func NewActionsService(store *stores.Store, am *actions.Manager, manager *gitrepo.Manager, log *logger.Logger) *ActionsService {
	return &ActionsService{store: store, actions: am, manager: manager, log: log}
}

// ── Workflows ────────────────────────────────────────────────────────────

func (s *ActionsService) ListWorkflows(ctx context.Context, req *v1.ListWorkflowsRequest) (*v1.ListWorkflowsResponse, error) {
	repo, _, err := s.actionsRepo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	files, err := s.actionsWorkflowFiles(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]*v1.Workflow, 0, len(files.files))
	for _, f := range files.files {
		out = append(out, files.proto(f))
	}
	return &v1.ListWorkflowsResponse{Workflows: out}, nil
}

func (s *ActionsService) GetWorkflow(ctx context.Context, req *v1.GetWorkflowRequest) (*v1.GetWorkflowResponse, error) {
	repo, _, err := s.actionsRepo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	files, err := s.actionsWorkflowFiles(ctx, repo)
	if err != nil {
		return nil, err
	}
	f := files.find(req.Path)
	if f == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "workflow not found")
	}
	return &v1.GetWorkflowResponse{Workflow: files.proto(f), Source: f.Source}, nil
}

func (s *ActionsService) EnableWorkflow(ctx context.Context, req *v1.EnableWorkflowRequest) (*v1.EnableWorkflowResponse, error) {
	wf, err := s.actionsSetDisabled(ctx, req.Namespace, req.Name, req.Path, false)
	if err != nil {
		return nil, err
	}
	return &v1.EnableWorkflowResponse{Workflow: wf}, nil
}

func (s *ActionsService) DisableWorkflow(ctx context.Context, req *v1.DisableWorkflowRequest) (*v1.DisableWorkflowResponse, error) {
	wf, err := s.actionsSetDisabled(ctx, req.Namespace, req.Name, req.Path, true)
	if err != nil {
		return nil, err
	}
	return &v1.DisableWorkflowResponse{Workflow: wf}, nil
}

func (s *ActionsService) actionsSetDisabled(ctx context.Context, ns, name, path string, disabled bool) (*v1.Workflow, error) {
	repo, _, err := s.actionsRepo(ctx, ns, name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	files, err := s.actionsWorkflowFiles(ctx, repo)
	if err != nil {
		return nil, err
	}
	f := files.find(path)
	if f == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "workflow not found")
	}
	if err := s.store.SetWorkflowDisabled(ctx, repo.ID, path, disabled); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	files.disabled[path] = disabled
	return files.proto(f), nil
}

func (s *ActionsService) DispatchWorkflow(ctx context.Context, req *v1.DispatchWorkflowRequest) (*v1.DispatchWorkflowResponse, error) {
	repo, user, err := s.actionsRepo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if req.Path == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "workflow path is required")
	}
	run, err := s.actions.Dispatch(ctx, repo, user, req.Path, req.Ref, req.Inputs)
	if err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.DispatchWorkflowResponse{Run: s.actionsRunProto(ctx, repo, run, true)}, nil
}

// ── Runs ─────────────────────────────────────────────────────────────────

func (s *ActionsService) ListWorkflowRuns(ctx context.Context, req *v1.ListWorkflowRunsRequest) (*v1.ListWorkflowRunsResponse, error) {
	repo, _, err := s.actionsRepo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	opts := stores.ActionsRunListOptions{
		RepoID: repo.ID, WorkflowPath: req.WorkflowPath, Event: req.Event, Actor: req.Actor,
		Status: actions.StatusFromProto(req.Status), Conclusion: actions.ConclusionFromProto(req.Conclusion),
		Limit: limit, Offset: offset,
	}
	if ref := strings.TrimSpace(req.Ref); ref != "" {
		if !strings.HasPrefix(ref, "refs/") {
			ref = "refs/heads/" + ref
		}
		opts.Ref = ref
	}
	runs, total, err := s.store.ListActionsRuns(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.WorkflowRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, s.actionsRunProto(ctx, repo, r, false))
	}
	return &v1.ListWorkflowRunsResponse{Runs: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *ActionsService) GetWorkflowRun(ctx context.Context, req *v1.GetWorkflowRunRequest) (*v1.GetWorkflowRunResponse, error) {
	repo, run, _, err := s.actionsRun(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	return &v1.GetWorkflowRunResponse{Run: s.actionsRunProto(ctx, repo, run, true)}, nil
}

func (s *ActionsService) CancelRun(ctx context.Context, req *v1.CancelRunRequest) (*v1.CancelRunResponse, error) {
	repo, run, _, err := s.actionsRun(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.actions.CancelRun(ctx, repo, run); err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.CancelRunResponse{Run: s.actionsReloadRun(ctx, repo, run)}, nil
}

func (s *ActionsService) RerunRun(ctx context.Context, req *v1.RerunRunRequest) (*v1.RerunRunResponse, error) {
	repo, run, _, err := s.actionsRun(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.actions.RerunRun(ctx, repo, run, req.FailedOnly); err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.RerunRunResponse{Run: s.actionsReloadRun(ctx, repo, run)}, nil
}

func (s *ActionsService) ApproveRun(ctx context.Context, req *v1.ApproveRunRequest) (*v1.ApproveRunResponse, error) {
	repo, run, _, err := s.actionsRun(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.actions.ApproveRun(ctx, repo, run); err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.ApproveRunResponse{Run: s.actionsReloadRun(ctx, repo, run)}, nil
}

func (s *ActionsService) GetJob(ctx context.Context, req *v1.GetJobRequest) (*v1.GetJobResponse, error) {
	_, _, job, err := s.actionsJob(ctx, req.Namespace, req.Name, req.Id)
	if err != nil {
		return nil, err
	}
	return &v1.GetJobResponse{Job: s.actionsJobProto(ctx, job, map[string]string{})}, nil
}

func (s *ActionsService) StreamJobLog(ctx context.Context, req *v1.StreamJobLogRequest, stream distrofacev1connect.ActionsServiceStreamJobLogServerStream) error {
	_, _, job, err := s.actionsJob(ctx, req.Namespace, req.Name, req.JobId)
	if err != nil {
		return err
	}
	from := int(req.FromIndex)
	if from < 0 {
		from = 0
	}
	ticker := time.NewTicker(actionsLogPoll)
	defer ticker.Stop()
	for {
		chunks, err := s.actions.LogChunks(ctx, job.ID, from)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		for _, c := range chunks {
			if err := stream.Send(&v1.JobLogChunk{JobId: job.ID, Index: int32(c.Index), Data: c.Data}); err != nil {
				return err
			}
			from = c.Index + 1
		}
		if job.Status == actions.StatusCompleted {
			if job.LogDigest != "" && from == 0 {
				data, err := s.actions.ReadCompactedLog(job)
				if err == nil && len(data) > 0 {
					if err := stream.Send(&v1.JobLogChunk{JobId: job.ID, Index: 0, Data: data}); err != nil {
						return err
					}
					from = 1
				}
			}
			return stream.Send(&v1.JobLogChunk{JobId: job.ID, Index: int32(from), Done: true})
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		fresh, err := s.store.GetActionsJob(ctx, job.ID)
		if err != nil || fresh == nil {
			return stream.Send(&v1.JobLogChunk{JobId: job.ID, Index: int32(from), Done: true})
		}
		job = fresh
	}
}

func (s *ActionsService) ListRunArtifacts(ctx context.Context, req *v1.ListRunArtifactsRequest) (*v1.ListRunArtifactsResponse, error) {
	_, run, _, err := s.actionsRun(ctx, req.Namespace, req.Name, req.RunId, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListActionsArtifacts(ctx, run.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	pkgs := map[int64]*storage.ArtifactRepository{}
	out := make([]*v1.RunArtifact, 0, len(rows))
	for _, row := range rows {
		art, _ := s.store.GetArtifact(ctx, row.ArtifactID)
		var pkg *storage.ArtifactRepository
		if art != nil {
			pkg = pkgs[art.RepoID]
			if pkg == nil {
				pkg, _ = s.store.GetArtifactRepositoryByID(ctx, art.RepoID)
				pkgs[art.RepoID] = pkg
			}
		}
		out = append(out, actionsArtifactProto(row, art, pkg))
	}
	return &v1.ListRunArtifactsResponse{Artifacts: out}, nil
}

// ── Secrets and variables ────────────────────────────────────────────────

func (s *ActionsService) ListSecrets(ctx context.Context, req *v1.ListSecretsRequest) (*v1.ListSecretsResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListActionsSecrets(ctx, sc.kind, sc.id, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.Secret, 0, len(rows))
	for _, r := range rows {
		out = append(out, &v1.Secret{Name: r.Name, Scope: sc.proto(), UpdatedAt: timestamppb.New(r.UpdatedAt)})
	}
	return &v1.ListSecretsResponse{Secrets: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *ActionsService) SetSecret(ctx context.Context, req *v1.SetSecretRequest) (*v1.SetSecretResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	if !actions.ValidSecretName(req.SecretName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "secret name must be an identifier")
	}
	if req.Value == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "secret value is required")
	}
	sealed, err := s.actions.Seal(req.Value)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	row := &storage.ActionsSecret{ScopeKind: sc.kind, ScopeID: sc.id, Name: req.SecretName, Sealed: sealed}
	if err := s.store.UpsertActionsSecret(ctx, row); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.SetSecretResponse{Secret: &v1.Secret{Name: row.Name, Scope: sc.proto(), UpdatedAt: timestamppb.New(time.Now().UTC())}}, nil
}

func (s *ActionsService) DeleteSecret(ctx context.Context, req *v1.DeleteSecretRequest) (*v1.DeleteSecretResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteActionsSecret(ctx, sc.kind, sc.id, req.SecretName); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteSecretResponse{}, nil
}

func (s *ActionsService) ListVariables(ctx context.Context, req *v1.ListVariablesRequest) (*v1.ListVariablesResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListActionsVariables(ctx, sc.kind, sc.id, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.Variable, 0, len(rows))
	for _, r := range rows {
		out = append(out, &v1.Variable{Name: r.Name, Value: r.Value, Scope: sc.proto(), UpdatedAt: timestamppb.New(r.UpdatedAt)})
	}
	return &v1.ListVariablesResponse{Variables: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *ActionsService) SetVariable(ctx context.Context, req *v1.SetVariableRequest) (*v1.SetVariableResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	if !actions.ValidSecretName(req.VariableName) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "variable name must be an identifier")
	}
	row := &storage.ActionsVariable{ScopeKind: sc.kind, ScopeID: sc.id, Name: req.VariableName, Value: req.Value}
	if err := s.store.UpsertActionsVariable(ctx, row); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.SetVariableResponse{Variable: &v1.Variable{Name: row.Name, Value: row.Value, Scope: sc.proto(), UpdatedAt: timestamppb.New(time.Now().UTC())}}, nil
}

func (s *ActionsService) DeleteVariable(ctx context.Context, req *v1.DeleteVariableRequest) (*v1.DeleteVariableResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteActionsVariable(ctx, sc.kind, sc.id, req.VariableName); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.DeleteVariableResponse{}, nil
}

// ── Runners ──────────────────────────────────────────────────────────────

func (s *ActionsService) ListRunners(ctx context.Context, req *v1.ListRunnersRequest) (*v1.ListRunnersResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListActionsRunners(ctx, sc.kind, sc.id, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := make([]*v1.Runner, 0, len(rows))
	for _, r := range rows {
		out = append(out, actionsRunnerProto(r, sc.proto()))
	}
	return &v1.ListRunnersResponse{Runners: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *ActionsService) CreateRunnerToken(ctx context.Context, req *v1.CreateRunnerTokenRequest) (*v1.CreateRunnerTokenResponse, error) {
	sc, err := s.actionsScope(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	user := auth.UserFromContext(ctx)
	token, expires, err := s.actions.CreateRegistrationToken(ctx, sc.kind, sc.id, user.ID)
	if err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.CreateRunnerTokenResponse{Token: token, ExpiresAt: timestamppb.New(expires)}, nil
}

func (s *ActionsService) DeleteRunner(ctx context.Context, req *v1.DeleteRunnerRequest) (*v1.DeleteRunnerResponse, error) {
	sc, runner, err := s.actionsScopedRunner(ctx, req.Scope, req.Id)
	if err != nil {
		return nil, err
	}
	_ = sc
	if err := s.actions.DeleteRunner(ctx, runner); err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.DeleteRunnerResponse{}, nil
}

func (s *ActionsService) UpdateRunner(ctx context.Context, req *v1.UpdateRunnerRequest) (*v1.UpdateRunnerResponse, error) {
	sc, runner, err := s.actionsScopedRunner(ctx, req.Scope, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.actions.UpdateRunner(ctx, runner, req.RunnerName, req.Labels, req.SetLabels); err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.UpdateRunnerResponse{Runner: actionsRunnerProto(runner, sc.proto())}, nil
}

// ── Access helpers ───────────────────────────────────────────────────────

// Loads a repository and requires a level on the actions unit
func (s *ActionsService) actionsRepo(ctx context.Context, ns, name string, need gitrepo.Level) (*storage.GitRepository, *auth.AuthenticatedUser, error) {
	user := auth.UserFromContext(ctx)
	repo, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, ns, name)
	if err != nil {
		return nil, nil, err
	}
	if need > gitrepo.LevelRead {
		if user == nil || user.IsAnonymous() {
			return nil, nil, connect.NewError(connect.CodeUnauthenticated, "")
		}
		if err := portalAllowsGitWrite(ctx); err != nil {
			return nil, nil, err
		}
	}
	if s.manager.Access().Level(ctx, user, repo, storage.GitUnitActions) < need {
		if !gitrepo.UnitEnabled(repo, storage.GitUnitActions) {
			return nil, nil, connect.Errorf(connect.CodeFailedPrecondition, "actions are disabled on this repository")
		}
		return nil, nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	return repo, user, nil
}

func (s *ActionsService) actionsRun(ctx context.Context, ns, name, id string, need gitrepo.Level) (*storage.GitRepository, *storage.ActionsRun, *auth.AuthenticatedUser, error) {
	repo, user, err := s.actionsRepo(ctx, ns, name, need)
	if err != nil {
		return nil, nil, nil, err
	}
	if id == "" {
		return nil, nil, nil, connect.Errorf(connect.CodeInvalidArgument, "run id is required")
	}
	run, err := s.store.GetActionsRun(ctx, repo.ID, id)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if run == nil {
		return nil, nil, nil, connect.Errorf(connect.CodeNotFound, "run not found")
	}
	return repo, run, user, nil
}

func (s *ActionsService) actionsJob(ctx context.Context, ns, name, id string) (*storage.GitRepository, *storage.ActionsRun, *storage.ActionsJob, error) {
	repo, _, err := s.actionsRepo(ctx, ns, name, gitrepo.LevelRead)
	if err != nil {
		return nil, nil, nil, err
	}
	if id == "" {
		return nil, nil, nil, connect.Errorf(connect.CodeInvalidArgument, "job id is required")
	}
	job, err := s.store.GetActionsJob(ctx, id)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if job == nil {
		return nil, nil, nil, connect.Errorf(connect.CodeNotFound, "job not found")
	}
	run, err := s.store.GetActionsRun(ctx, repo.ID, job.RunID)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if run == nil {
		return nil, nil, nil, connect.Errorf(connect.CodeNotFound, "job not found")
	}
	return repo, run, job, nil
}

func (s *ActionsService) actionsReloadRun(ctx context.Context, repo *storage.GitRepository, run *storage.ActionsRun) *v1.WorkflowRun {
	if fresh, _ := s.store.GetActionsRun(ctx, repo.ID, run.ID); fresh != nil {
		run = fresh
	}
	return s.actionsRunProto(ctx, repo, run, true)
}

// Git manage permission stands in for system administration
func (s *ActionsService) actionsSystemAdmin(user *auth.AuthenticatedUser) bool {
	if user == nil || user.IsAnonymous() || user.IsScoped() {
		return false
	}
	return s.manager.Access().ListOptions(user, "").IncludePrivate
}

// Resolved secret, variable, or runner scope with the caller admitted
type actionsScopeRef struct {
	kind    string
	id      string
	display string
}

func (r *actionsScopeRef) proto() *v1.ActionsScope {
	return &v1.ActionsScope{Kind: r.kind, Id: r.display}
}

// Requires administration of the scope the request names
func (s *ActionsService) actionsScope(ctx context.Context, scope *v1.ActionsScope) (*actionsScopeRef, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	kind := strings.ToLower(strings.TrimSpace(scope.GetKind()))
	id := strings.TrimSpace(scope.GetId())
	if kind == "" {
		kind = storage.ActionsScopeSystem
	}
	switch kind {
	case storage.ActionsScopeSystem:
		if !s.actionsSystemAdmin(user) {
			return nil, connect.Errorf(connect.CodePermissionDenied, "system administration required")
		}
		return &actionsScopeRef{kind: kind}, nil
	case storage.ActionsScopeOrg:
		if id == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "organization is required")
		}
		org, err := s.store.GetOrganization(ctx, id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if org == nil {
			org, err = s.store.GetOrganizationByID(ctx, id)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
			}
		}
		if org == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "organization not found")
		}
		if !s.actionsSystemAdmin(user) {
			member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
			if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
				return nil, connect.Errorf(connect.CodePermissionDenied, "organization administration required")
			}
		}
		return &actionsScopeRef{kind: kind, id: org.ID, display: org.ID}, nil
	case storage.ActionsScopeRepo:
		ns, name, ok := strings.Cut(id, "/")
		if !ok || ns == "" || name == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "repository scope id must be namespace/name")
		}
		repo, _, err := s.actionsRepo(ctx, ns, name, gitrepo.LevelAdmin)
		if err != nil {
			return nil, err
		}
		return &actionsScopeRef{kind: kind, id: repo.ID, display: repo.Namespace + "/" + repo.Name}, nil
	}
	return nil, connect.Errorf(connect.CodeInvalidArgument, "unknown scope kind %q", kind)
}

func (s *ActionsService) actionsScopedRunner(ctx context.Context, scope *v1.ActionsScope, id string) (*actionsScopeRef, *storage.ActionsRunner, error) {
	sc, err := s.actionsScope(ctx, scope)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, connect.Errorf(connect.CodeInvalidArgument, "runner id is required")
	}
	runner, err := s.store.GetActionsRunner(ctx, id)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if runner == nil || runner.ScopeKind != sc.kind || runner.ScopeID != sc.id {
		return nil, nil, connect.Errorf(connect.CodeNotFound, "runner not found")
	}
	return sc, runner, nil
}

// Workflow files of the default branch with their state and stats
type actionsWorkflowSet struct {
	files    []*actions.WorkflowFile
	disabled map[string]bool
	counts   map[string]int32
	last     map[string]time.Time
}

func (w *actionsWorkflowSet) find(path string) *actions.WorkflowFile {
	for _, f := range w.files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

func (w *actionsWorkflowSet) proto(f *actions.WorkflowFile) *v1.Workflow {
	out := &v1.Workflow{Path: f.Path, Enabled: !w.disabled[f.Path], RunCount: w.counts[f.Path]}
	if t, ok := w.last[f.Path]; ok && !t.IsZero() {
		out.LastRunAt = timestamppb.New(t)
	}
	if f.Err != nil {
		out.Error = f.Err.Error()
		out.Name = f.Path
		return out
	}
	wf := f.Workflow
	out.Name = wf.Name
	if out.Name == "" {
		out.Name = f.Path
	}
	out.Triggers = wf.Events()
	if inputs, order, ok := wf.DispatchInputs(); ok {
		list := make([]map[string]any, 0, len(order))
		for _, name := range order {
			in := inputs[name]
			list = append(list, map[string]any{
				"name": name, "type": in.Type, "description": in.Description,
				"default": in.Default, "required": in.Required, "options": in.Options,
			})
		}
		if b, err := json.Marshal(list); err == nil {
			out.DispatchInputs = string(b)
		}
	}
	return out
}

func (s *ActionsService) actionsWorkflowFiles(ctx context.Context, repo *storage.GitRepository) (*actionsWorkflowSet, error) {
	files, _, err := s.actions.LoadWorkflows(ctx, repo, repo.DefaultBranch)
	if err != nil {
		return nil, mapGitErr(err)
	}
	disabled, err := s.store.GetDisabledWorkflows(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	counts, last, err := s.store.ActionsWorkflowStats(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &actionsWorkflowSet{files: files, disabled: disabled, counts: counts, last: last}, nil
}

// ── Proto mapping ────────────────────────────────────────────────────────

func (s *ActionsService) actionsRunProto(ctx context.Context, repo *storage.GitRepository, run *storage.ActionsRun, withJobs bool) *v1.WorkflowRun {
	out := &v1.WorkflowRun{
		Id: run.ID, RepoFullName: repo.Namespace + "/" + repo.Name, Number: run.Number,
		WorkflowPath: run.WorkflowPath, WorkflowName: run.WorkflowName, Event: run.Event, Ref: run.Ref, Sha: run.SHA,
		Status: actions.ProtoStatus(run.Status), Conclusion: actions.ProtoConclusion(run.Conclusion),
		NeedsApproval: run.NeedsApproval, PullRequestNumber: run.PullNumber,
		HtmlUrl:   s.manager.HTMLURL(ctx, repo) + "/actions/runs/" + run.ID,
		CreatedAt: timestamppb.New(run.CreatedAt),
		Notes:     actions.StringList(run.Notes),
	}
	if run.TriggerUserID != "" {
		if u, _ := s.store.GetUserByID(ctx, run.TriggerUserID); u != nil {
			out.TriggerUser = u.Username
		}
	}
	if run.StartedAt != nil {
		out.StartedAt = timestamppb.New(*run.StartedAt)
	}
	if run.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(*run.FinishedAt)
	}
	if withJobs {
		jobs, _ := s.store.ListActionsJobs(ctx, run.ID)
		names := map[string]string{}
		for _, j := range jobs {
			out.Jobs = append(out.Jobs, s.actionsJobProto(ctx, j, names))
		}
	}
	return out
}

func (s *ActionsService) actionsJobProto(ctx context.Context, j *storage.ActionsJob, runnerNames map[string]string) *v1.Job {
	out := &v1.Job{
		Id: j.ID, RunId: j.RunID, Name: j.Name,
		RunsOn: actionsDecodeList(j.RunsOn), Needs: actionsDecodeList(j.Needs),
		Status: actions.ProtoStatus(j.Status), Conclusion: actions.ProtoConclusion(j.Conclusion),
		Attempt: int32(j.Attempt), Steps: actions.DecodeSteps(j.Steps), Outputs: j.Outputs, Annotations: j.Annotations,
	}
	if j.RunnerID != "" {
		name, ok := runnerNames[j.RunnerID]
		if !ok {
			if r, _ := s.store.GetActionsRunner(ctx, j.RunnerID); r != nil {
				name = r.Name
			}
			runnerNames[j.RunnerID] = name
		}
		out.RunnerName = name
	}
	if j.StartedAt != nil {
		out.StartedAt = timestamppb.New(*j.StartedAt)
	}
	if j.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(*j.FinishedAt)
	}
	return out
}

func actionsDecodeList(raw string) []string {
	var out []string
	if raw == "" || json.Unmarshal([]byte(raw), &out) != nil {
		return []string{}
	}
	return out
}

func actionsRunnerProto(r *storage.ActionsRunner, scope *v1.ActionsScope) *v1.Runner {
	out := &v1.Runner{
		Id: r.ID, Name: r.Name, Scope: scope, Labels: actions.RunnerLabels(r), Version: r.Version,
		Status: actions.RunnerStatus(r), CreatedAt: timestamppb.New(r.CreatedAt),
	}
	if scope == nil {
		out.Scope = &v1.ActionsScope{Kind: r.ScopeKind, Id: r.ScopeID}
	}
	if r.LastSeen != nil {
		out.LastSeen = timestamppb.New(*r.LastSeen)
	}
	return out
}

func actionsArtifactProto(row *storage.ActionsArtifact, art *storage.Artifact, pkg *storage.ArtifactRepository) *v1.RunArtifact {
	out := &v1.RunArtifact{Id: row.ID, Name: row.Name, CreatedAt: timestamppb.New(row.CreatedAt)}
	if row.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*row.ExpiresAt)
	}
	if art != nil {
		out.Size = art.Size
		if pkg != nil {
			out.DownloadUrl = actions.ArtifactDownloadPath(pkg, art)
		}
	}
	return out
}

// Maps actions domain errors onto connect codes
func mapActionsErr(err error) error {
	if err == nil {
		return nil
	}
	var cerr *connect.Error
	switch {
	case errors.As(err, &cerr):
		return err
	case errors.Is(err, actions.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, actions.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, actions.ErrDenied):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, actions.ErrConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, actions.ErrTooLarge):
		return connect.NewError(connect.CodeResourceExhausted, err.Error()).WithCause(err)
	case errors.Is(err, actions.ErrDisabled):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	}
	return mapGitErr(err)
}
