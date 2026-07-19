package services

import (
	"context"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.HealthServiceHandler = (*HealthService)(nil)

type HealthService struct {
	log *logger.Logger
}

func NewHealthService(log *logger.Logger) *HealthService {
	return &HealthService{log: log}
}

func (s *HealthService) HealthCheck(ctx context.Context, req *connect.Request[v1.HealthCheckRequest]) (*connect.Response[v1.HealthCheckResponse], error) {
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}

	resp := &v1.HealthCheckResponse{
		Status:    "ok",
		Timestamp: timestamppb.New(time.Now()),
		Version:   version,
	}
	return connect.NewResponse(resp), nil
}
