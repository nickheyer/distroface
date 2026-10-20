package services

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/actions"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

var _ distrofacev1connect.RunnerServiceHandler = (*RunnerService)(nil)

type RunnerService struct {
	store   *stores.Store
	actions *actions.Manager
	log     *logger.Logger
}

func NewRunnerService(store *stores.Store, am *actions.Manager, log *logger.Logger) *RunnerService {
	return &RunnerService{store: store, actions: am, log: log}
}

// Authenticates the runner from the token header
func (s *RunnerService) runner(ctx context.Context) (*storage.ActionsRunner, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "runner token required")
	}
	token := info.RequestHeader().Get(actions.RunnerTokenHeader)
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "runner token required")
	}
	runner, err := s.actions.AuthenticateRunner(ctx, token)
	if err != nil {
		if errors.Is(err, actions.ErrDenied) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err.Error())
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return runner, nil
}

func (s *RunnerService) RegisterRunner(ctx context.Context, req *v1.RegisterRunnerRequest) (*v1.RegisterRunnerResponse, error) {
	runner, token, err := s.actions.Register(ctx, req.RegistrationToken, req.RunnerName, req.Labels, req.Version)
	if err != nil {
		if errors.Is(err, actions.ErrDenied) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err.Error())
		}
		return nil, mapActionsErr(err)
	}
	return &v1.RegisterRunnerResponse{Runner: actionsRunnerProto(runner, s.displayScope(ctx, runner)), RunnerToken: token}, nil
}

// Names the scope by org or repo instead of by id
func (s *RunnerService) displayScope(ctx context.Context, r *storage.ActionsRunner) *v1.ActionsScope {
	switch r.ScopeKind {
	case storage.ActionsScopeOrg:
		if org, _ := s.store.GetOrganizationByID(ctx, r.ScopeID); org != nil {
			return &v1.ActionsScope{Kind: r.ScopeKind, Id: org.Name}
		}
	case storage.ActionsScopeRepo:
		if repo, _ := s.store.GetGitRepositoryByID(ctx, r.ScopeID); repo != nil {
			return &v1.ActionsScope{Kind: r.ScopeKind, Id: repo.Namespace + "/" + repo.Name}
		}
	}
	return nil
}

func (s *RunnerService) Heartbeat(ctx context.Context, req *v1.HeartbeatRequest) (*v1.HeartbeatResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.actions.Heartbeat(ctx, runner, req.Version, req.RunningJobIds); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.HeartbeatResponse{}, nil
}

func (s *RunnerService) FetchTask(ctx context.Context, req *v1.FetchTaskRequest) (*v1.FetchTaskResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	_ = s.store.TouchActionsRunner(ctx, runner.ID, runner.Version, runner.Busy)
	task, err := s.actions.FetchTask(ctx, runner, time.Duration(req.WaitSeconds)*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return &v1.FetchTaskResponse{}, nil
		}
		return nil, mapActionsErr(err)
	}
	return &v1.FetchTaskResponse{Task: task}, nil
}

func (s *RunnerService) UpdateTask(ctx context.Context, req *v1.UpdateTaskRequest) (*v1.UpdateTaskResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	cancelled, err := s.actions.UpdateTask(ctx, runner, req.JobId, req.Steps, req.Outputs, req.Annotations)
	if err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.UpdateTaskResponse{Cancelled: cancelled}, nil
}

func (s *RunnerService) AppendTaskLog(ctx context.Context, req *v1.AppendTaskLogRequest) (*v1.AppendTaskLogResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	next, err := s.actions.AppendTaskLog(ctx, runner, req.JobId, int(req.Index), req.Data)
	if err != nil {
		if errors.Is(err, actions.ErrConflict) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error())
		}
		return nil, mapActionsErr(err)
	}
	return &v1.AppendTaskLogResponse{NextIndex: int32(next)}, nil
}

func (s *RunnerService) CompleteTask(ctx context.Context, req *v1.CompleteTaskRequest) (*v1.CompleteTaskResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	conclusion := actions.ConclusionFromProto(req.Conclusion)
	if conclusion == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "conclusion is required")
	}
	if err := s.actions.CompleteTask(ctx, runner, req.JobId, conclusion, req.Steps, req.Outputs, req.Annotations); err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.CompleteTaskResponse{}, nil
}

func (s *RunnerService) UploadTaskArtifact(ctx context.Context, req *v1.UploadTaskArtifactRequest) (*v1.UploadTaskArtifactResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	uploadID, pkg, err := s.actions.BeginArtifactUpload(ctx, runner, req.JobId, req.ArtifactName)
	if err != nil {
		return nil, mapActionsErr(err)
	}
	return &v1.UploadTaskArtifactResponse{UploadId: uploadID, UploadUrl: artifactUploadURL(nil, pkg.Namespace, pkg.Name, uploadID)}, nil
}

func (s *RunnerService) FinishTaskArtifact(ctx context.Context, req *v1.FinishTaskArtifactRequest) (*v1.FinishTaskArtifactResponse, error) {
	runner, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	row, art, pkg, err := s.actions.FinishArtifactUpload(ctx, runner, req.JobId, req.ArtifactName, req.Path, req.UploadId)
	if err != nil {
		var cerr *connect.Error
		if errors.As(err, &cerr) {
			return nil, err
		}
		if mapped := mapArtifactErr(err); connect.CodeOf(mapped) != connect.CodeInternal {
			return nil, mapped
		}
		return nil, mapActionsErr(err)
	}
	return &v1.FinishTaskArtifactResponse{Artifact: actionsArtifactProto(row, art, pkg)}, nil
}
