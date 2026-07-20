package services

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.MirrorServiceHandler = (*MirrorService)(nil)

// Keepalives hold proxies open between rare events
const watchKeepalive = 45 * time.Second

type MirrorService struct {
	monitor  *mirror.Monitor
	enforcer *rbac.Enforcer
	access   *artifacts.Access
	log      *logger.Logger
}

func NewMirrorService(monitor *mirror.Monitor, enforcer *rbac.Enforcer, access *artifacts.Access, log *logger.Logger) *MirrorService {
	return &MirrorService{monitor: monitor, enforcer: enforcer, access: access, log: log}
}

func (s *MirrorService) WatchSyncs(ctx context.Context, req *connect.Request[v1.WatchSyncsRequest], stream *connect.ServerStream[v1.SyncEvent]) error {
	if s.monitor == nil {
		return connect.NewError(connect.CodeUnavailable, nil)
	}
	ch, cancel := s.monitor.Subscribe()
	defer cancel()

	// In flight syncs replay so fresh pages see current state
	for _, ev := range s.monitor.Active() {
		if err := s.sendVisible(ctx, stream, ev); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(watchKeepalive)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-ch:
			if err := s.sendVisible(ctx, stream, ev); err != nil {
				return err
			}
		case <-ticker.C:
			if err := stream.Send(&v1.SyncEvent{}); err != nil {
				return err
			}
		}
	}
}

func (s *MirrorService) sendVisible(ctx context.Context, stream *connect.ServerStream[v1.SyncEvent], ev mirror.Event) error {
	if !s.visible(ctx, ev) {
		return nil
	}
	return stream.Send(eventToProto(ev))
}

// Mirrors the read rules of the repo detail endpoints
func (s *MirrorService) visible(ctx context.Context, ev mirror.Event) bool {
	if portal.ForeignRef(ctx, ev.Namespace) {
		return false
	}
	if !ev.Private {
		return true
	}
	user := auth.UserFromContext(ctx)
	if user == nil {
		return false
	}
	switch ev.Kind {
	case "image":
		allowed, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionRead, ev.Namespace+"/"+ev.Name)
		return allowed
	case "artifact":
		repo := &db.ArtifactRepository{Namespace: ev.Namespace, Name: ev.Name, OwnerID: ev.OwnerID, IsPrivate: true}
		return s.access.HasRepoAccess(ctx, user, repo, rbac.ActionRead)
	}
	return false
}

func eventToProto(ev mirror.Event) *v1.SyncEvent {
	phase := v1.SyncPhase_SYNC_PHASE_UNSPECIFIED
	switch ev.Phase {
	case mirror.PhaseStarted:
		phase = v1.SyncPhase_SYNC_PHASE_STARTED
	case mirror.PhaseCompleted:
		phase = v1.SyncPhase_SYNC_PHASE_COMPLETED
	case mirror.PhaseFailed:
		phase = v1.SyncPhase_SYNC_PHASE_FAILED
	}
	return &v1.SyncEvent{
		Kind:      ev.Kind,
		RepoId:    ev.RepoID,
		Namespace: ev.Namespace,
		Name:      ev.Name,
		Phase:     phase,
		Error:     ev.Err,
		At:        timestamppb.New(ev.At),
	}
}
