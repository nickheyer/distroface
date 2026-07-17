package services

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.GCServiceHandler = (*GCService)(nil)

type GCService struct {
	collector *admin.Collector
	config    *config.Config
	log       *logger.Logger
}

func NewGCService(collector *admin.Collector, cfg *config.Config, log *logger.Logger) *GCService {
	return &GCService{collector: collector, config: cfg, log: log}
}

func (s *GCService) RunGC(ctx context.Context, req *connect.Request[v1.RunGCRequest]) (*connect.Response[v1.RunGCResponse], error) {
	if err := s.collector.Start(req.Msg.DryRun, req.Msg.RemoveUntagged); err != nil {
		if errors.Is(err, admin.ErrAlreadyRunning) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.RunGCResponse{}), nil
}

func (s *GCService) GetGCStatus(ctx context.Context, req *connect.Request[v1.GetGCStatusRequest]) (*connect.Response[v1.GetGCStatusResponse], error) {
	running, last := s.collector.Status()

	resp := &v1.GetGCStatusResponse{
		Running:       running,
		Scheduled:     s.config.GC.Enabled,
		IntervalHours: int32(s.config.GC.IntervalHours),
	}
	if last != nil {
		resp.LastRun = &v1.GCRun{
			StartedAt:      timestamppb.New(last.StartedAt),
			FinishedAt:     timestamppb.New(last.FinishedAt),
			DryRun:         last.DryRun,
			RemoveUntagged: last.RemoveUntagged,
			BlobsDeleted:   last.BlobsDeleted,
			BytesFreed:     last.BytesFreed,
			Error:          last.Err,
		}
	}
	return connect.NewResponse(resp), nil
}
