package services

import (
	"context"
	"os"
	"time"

	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.HealthServiceHandler = (*HealthService)(nil)

type HealthService struct {
	log    *logger.Logger
	checks func(context.Context) map[string]string
}

func NewHealthService(log *logger.Logger) *HealthService {
	return &HealthService{log: log}
}

// Failing subsystem checks turn the status degraded
func (s *HealthService) SetChecks(fn func(context.Context) map[string]string) {
	s.checks = fn
}

func (s *HealthService) HealthCheck(ctx context.Context, req *v1.HealthCheckRequest) (*v1.HealthCheckResponse, error) {
	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}

	status := "ok"
	if s.checks != nil {
		if failing := s.checks(ctx); len(failing) > 0 {
			status = "degraded"
			for name, msg := range failing {
				s.log.Warn("Health check %s failing: %s", name, msg)
			}
		}
	}

	resp := &v1.HealthCheckResponse{
		Status:    status,
		Timestamp: timestamppb.New(time.Now()),
		Version:   version,
	}
	return resp, nil
}
