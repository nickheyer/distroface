package audit

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.AuditServiceHandler = (*Service)(nil)

type Service struct {
	store *stores.Store
	log   *logger.Logger
}

func NewService(store *stores.Store, log *logger.Logger) *Service {
	return &Service{store: store, log: log}
}

func (s *Service) ListAuditEvents(ctx context.Context, req *v1.ListAuditEventsRequest) (*v1.ListAuditEventsResponse, error) {
	limit, offset := pages.Parse(req.Page)
	q := pages.ParseQuery(req.Page)
	if err := stores.AuditQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	orderBy := pages.OrderBy(req.Page, stores.AuditSortColumns, "created_at DESC")
	events, total, more, err := s.store.ListAuditEvents(ctx, q, orderBy, limit, offset, pages.After(req.Page))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	resp := &v1.ListAuditEventsResponse{Page: &v1.PageInfo{TotalCount: total}}
	if more {
		resp.Page.NextPageToken = pages.AfterToken(events[len(events)-1].ID)
	}
	for _, ev := range events {
		resp.Events = append(resp.Events, &v1.AuditEvent{
			Id:        ev.ID,
			Actor:     ev.Actor,
			ActorId:   ev.ActorID,
			SourceIp:  ev.SourceIP,
			Action:    ev.Action,
			Resource:  ev.Resource,
			Outcome:   ev.Outcome,
			Detail:    ev.Detail,
			CreatedAt: timestamppb.New(ev.CreatedAt),
		})
	}
	return resp, nil
}
