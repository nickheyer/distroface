package services

import (
	"context"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type HealthService struct {
	log *logger.Logger
}

func NewHealthService(log *logger.Logger) *HealthService {
	return &HealthService{log: log}
}

func (s *HealthService) Check(ctx context.Context, req *connect.Request[v1.HealthServiceCheckRequest]) (*connect.Response[v1.HealthServiceCheckResponse], error) {
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}

	resp := &v1.HealthServiceCheckResponse{
		Status:    "ok",
		Timestamp: timestamppb.New(time.Now()),
		Version:   version,
	}
	return connect.NewResponse(resp), nil
}
