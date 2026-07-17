package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.WebhookServiceHandler = (*WebhookService)(nil)

type WebhookService struct {
	store      *stores.Store
	enforcer   *rbac.Enforcer
	dispatcher *webhook.Dispatcher
	log        *logger.Logger
}

func NewWebhookService(store *stores.Store, enforcer *rbac.Enforcer, dispatcher *webhook.Dispatcher, log *logger.Logger) *WebhookService {
	return &WebhookService{store: store, enforcer: enforcer, dispatcher: dispatcher, log: log}
}

func (s *WebhookService) CreateWebhook(ctx context.Context, req *connect.Request[v1.CreateWebhookRequest]) (*connect.Response[v1.CreateWebhookResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	if msg.Url == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("url is required"))
	}
	if !isValidWebhookURL(msg.Url) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("url must be a valid HTTP or HTTPS URL"))
	}
	if len(msg.Events) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one event is required"))
	}

	scope := storage.WebhookScopeRepository
	if msg.Scope == v1.WebhookScope_WEBHOOK_SCOPE_ORGANIZATION {
		scope = storage.WebhookScopeOrganization
	}

	// Verify permission on the target repo/org
	if scope == storage.WebhookScopeRepository {
		if msg.RepoId == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("repo_id is required for repository webhooks"))
		}
		if err := s.checkRepoPermission(ctx, user, msg.RepoId, rbac.ActionUpdate); err != nil {
			return nil, err
		}
	} else {
		if msg.OrgId == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("org_id is required for organization webhooks"))
		}
		if err := s.checkOrgPermission(ctx, user, msg.OrgId, rbac.ActionUpdate); err != nil {
			return nil, err
		}
	}

	events := eventsToStrings(msg.Events)
	eventsJSON, _ := json.Marshal(events)

	contentType := msg.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	var repoID, orgID *string
	if msg.RepoId != "" {
		repoID = &msg.RepoId
	}
	if msg.OrgId != "" {
		orgID = &msg.OrgId
	}

	if msg.PayloadTemplate != "" {
		if err := webhook.ValidateTemplate(msg.PayloadTemplate); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("payload_template: %v", err))
		}
	}

	wh := &storage.Webhook{
		Scope:           scope,
		RepoID:          repoID,
		OrgID:           orgID,
		URL:             msg.Url,
		Events:          string(eventsJSON),
		Active:          msg.Active,
		ContentType:     contentType,
		PayloadTemplate: msg.PayloadTemplate,
		CreatedBy:       user.ID,
	}

	if msg.Secret != "" {
		wh.Secret = msg.Secret
	}

	if err := s.store.CreateWebhook(ctx, wh); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.CreateWebhookResponse{
		Webhook: s.webhookToProto(ctx, wh),
	}), nil
}

func (s *WebhookService) ListWebhooks(ctx context.Context, req *connect.Request[v1.ListWebhooksRequest]) (*connect.Response[v1.ListWebhooksResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	limit, offset := parsePagination(msg.PageSize, msg.PageToken)

	var webhooks []*storage.Webhook
	var total int64
	var err error

	if msg.RepoId != "" {
		if err := s.checkRepoPermission(ctx, user, msg.RepoId, rbac.ActionRead); err != nil {
			return nil, err
		}
		webhooks, total, err = s.store.ListWebhooksByRepo(ctx, msg.RepoId, limit, offset)
	} else if msg.OrgId != "" {
		if err := s.checkOrgPermission(ctx, user, msg.OrgId, rbac.ActionRead); err != nil {
			return nil, err
		}
		webhooks, total, err = s.store.ListWebhooksByOrg(ctx, msg.OrgId, limit, offset)
	} else {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("repo_id or org_id is required"))
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoWebhooks := make([]*v1.Webhook, len(webhooks))
	for i, wh := range webhooks {
		protoWebhooks[i] = s.webhookToProto(ctx, wh)
	}

	return connect.NewResponse(&v1.ListWebhooksResponse{
		Webhooks:      protoWebhooks,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    total,
	}), nil
}

func (s *WebhookService) GetWebhook(ctx context.Context, req *connect.Request[v1.GetWebhookRequest]) (*connect.Response[v1.GetWebhookResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	wh, err := s.store.GetWebhook(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if wh == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("webhook not found"))
	}

	if err := s.checkWebhookPermission(ctx, user, wh, rbac.ActionRead); err != nil {
		return nil, err
	}

	return connect.NewResponse(&v1.GetWebhookResponse{
		Webhook: s.webhookToProto(ctx, wh),
	}), nil
}

func (s *WebhookService) UpdateWebhook(ctx context.Context, req *connect.Request[v1.UpdateWebhookRequest]) (*connect.Response[v1.UpdateWebhookResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	wh, err := s.store.GetWebhook(ctx, msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if wh == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("webhook not found"))
	}

	if err := s.checkWebhookPermission(ctx, user, wh, rbac.ActionUpdate); err != nil {
		return nil, err
	}

	if msg.Url != "" {
		if !isValidWebhookURL(msg.Url) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("url must be a valid HTTP or HTTPS URL"))
		}
		wh.URL = msg.Url
	}
	if msg.Secret != "" {
		wh.Secret = msg.Secret
	}
	if len(msg.Events) > 0 {
		events := eventsToStrings(msg.Events)
		eventsJSON, _ := json.Marshal(events)
		wh.Events = string(eventsJSON)
	}
	if msg.ContentType != "" {
		wh.ContentType = msg.ContentType
	}
	if msg.PayloadTemplate != nil {
		if *msg.PayloadTemplate != "" {
			if err := webhook.ValidateTemplate(*msg.PayloadTemplate); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("payload_template: %v", err))
			}
		}
		wh.PayloadTemplate = *msg.PayloadTemplate
	}
	if msg.Active != nil {
		wh.Active = *msg.Active
	}

	if err := s.store.UpdateWebhook(ctx, wh); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.UpdateWebhookResponse{
		Webhook: s.webhookToProto(ctx, wh),
	}), nil
}

func (s *WebhookService) DeleteWebhook(ctx context.Context, req *connect.Request[v1.DeleteWebhookRequest]) (*connect.Response[v1.DeleteWebhookResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	wh, err := s.store.GetWebhook(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if wh == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("webhook not found"))
	}

	if err := s.checkWebhookPermission(ctx, user, wh, rbac.ActionDelete); err != nil {
		return nil, err
	}

	if err := s.store.DeleteWebhook(ctx, wh.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.DeleteWebhookResponse{}), nil
}

func (s *WebhookService) ListWebhookDeliveries(ctx context.Context, req *connect.Request[v1.ListWebhookDeliveriesRequest]) (*connect.Response[v1.ListWebhookDeliveriesResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	if msg.WebhookId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("webhook_id is required"))
	}

	wh, err := s.store.GetWebhook(ctx, msg.WebhookId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if wh == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("webhook not found"))
	}

	if err := s.checkWebhookPermission(ctx, user, wh, rbac.ActionRead); err != nil {
		return nil, err
	}

	limit, offset := parsePagination(msg.PageSize, msg.PageToken)
	deliveries, total, err := s.store.ListWebhookDeliveries(ctx, msg.WebhookId, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoDeliveries := make([]*v1.WebhookDelivery, len(deliveries))
	for i, d := range deliveries {
		protoDeliveries[i] = deliveryToProto(d)
	}

	return connect.NewResponse(&v1.ListWebhookDeliveriesResponse{
		Deliveries:    protoDeliveries,
		NextPageToken: nextPageToken(offset, limit, total),
		TotalCount:    total,
	}), nil
}

func (s *WebhookService) RedeliverWebhook(ctx context.Context, req *connect.Request[v1.RedeliverWebhookRequest]) (*connect.Response[v1.RedeliverWebhookResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	msg := req.Msg
	if msg.DeliveryId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("delivery_id is required"))
	}

	// Look up delivery to find webhook for permission check
	delivery, err := s.store.GetWebhookDelivery(ctx, msg.DeliveryId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if delivery == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("delivery not found"))
	}

	wh, err := s.store.GetWebhook(ctx, delivery.WebhookID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if wh == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("webhook not found"))
	}

	if err := s.checkWebhookPermission(ctx, user, wh, rbac.ActionUpdate); err != nil {
		return nil, err
	}

	newDelivery, err := s.dispatcher.Redeliver(ctx, msg.DeliveryId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&v1.RedeliverWebhookResponse{
		Delivery: deliveryToProto(newDelivery),
	}), nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// Allow only absolute http or https urls
func isValidWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *WebhookService) checkRepoPermission(ctx context.Context, user *auth.AuthenticatedUser, repoID, action string) error {
	repo := s.getRepoByID(ctx, repoID)
	if repo == nil {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("repository not found"))
	}
	allowed, err := s.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, action, repo.Namespace+"/"+repo.Name)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if !allowed {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("permission denied"))
	}
	return nil
}

func (s *WebhookService) checkOrgPermission(ctx context.Context, user *auth.AuthenticatedUser, orgID, action string) error {
	org, err := s.store.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("organization not found"))
	}
	allowed, err := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, action, org.Name)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if !allowed {
		// Fall back to org membership check
		member, err := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if err != nil || member == nil {
			return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("permission denied"))
		}
		if action != rbac.ActionRead && member.Role == storage.OrgRoleMember {
			return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("permission denied"))
		}
	}
	return nil
}

func (s *WebhookService) checkWebhookPermission(ctx context.Context, user *auth.AuthenticatedUser, wh *storage.Webhook, action string) error {
	if wh.Scope == storage.WebhookScopeRepository {
		return s.checkRepoPermission(ctx, user, derefStr(wh.RepoID), action)
	}
	return s.checkOrgPermission(ctx, user, derefStr(wh.OrgID), action)
}

func (s *WebhookService) getRepoByID(ctx context.Context, id string) *storage.Repository {
	var repo storage.Repository
	err := s.store.DB().WithContext(ctx).First(&repo, "id = ?", id).Error
	if err != nil {
		return nil
	}
	return &repo
}

func (s *WebhookService) webhookToProto(ctx context.Context, wh *storage.Webhook) *v1.Webhook {
	scope := v1.WebhookScope_WEBHOOK_SCOPE_REPOSITORY
	if wh.Scope == storage.WebhookScopeOrganization {
		scope = v1.WebhookScope_WEBHOOK_SCOPE_ORGANIZATION
	}

	var events []string
	json.Unmarshal([]byte(wh.Events), &events)

	protoEvents := stringsToEvents(events)

	scopeName := ""
	if wh.Scope == storage.WebhookScopeRepository {
		repo := s.getRepoByID(ctx, derefStr(wh.RepoID))
		if repo != nil {
			scopeName = repo.Namespace + "/" + repo.Name
		}
	} else {
		org, _ := s.store.GetOrganizationByID(ctx, derefStr(wh.OrgID))
		if org != nil {
			scopeName = org.Name
		}
	}

	return &v1.Webhook{
		Id:              wh.ID,
		Scope:           scope,
		RepoId:          derefStr(wh.RepoID),
		OrgId:           derefStr(wh.OrgID),
		Url:             wh.URL,
		HasSecret:       wh.Secret != "",
		Events:          protoEvents,
		Active:          wh.Active,
		ContentType:     wh.ContentType,
		CreatedAt:       timestamppb.New(wh.CreatedAt),
		UpdatedAt:       timestamppb.New(wh.UpdatedAt),
		ScopeName:       scopeName,
		PayloadTemplate: wh.PayloadTemplate,
	}
}

func deliveryToProto(d *storage.WebhookDelivery) *v1.WebhookDelivery {
	return &v1.WebhookDelivery{
		Id:           d.ID,
		WebhookId:    d.WebhookID,
		Event:        stringToEvent(d.Event),
		StatusCode:   int32(d.StatusCode),
		Success:      d.Success,
		RequestBody:  d.RequestBody,
		ResponseBody: d.ResponseBody,
		DurationMs:   d.DurationMs,
		Attempt:      int32(d.Attempt),
		DeliveredAt:  timestamppb.New(d.DeliveredAt),
	}
}

func eventsToStrings(events []v1.WebhookEvent) []string {
	result := make([]string, 0, len(events))
	for _, e := range events {
		switch e {
		case v1.WebhookEvent_WEBHOOK_EVENT_PUSH:
			result = append(result, "push")
		case v1.WebhookEvent_WEBHOOK_EVENT_PULL:
			result = append(result, "pull")
		case v1.WebhookEvent_WEBHOOK_EVENT_DELETE:
			result = append(result, "delete")
		}
	}
	return result
}

func stringsToEvents(events []string) []v1.WebhookEvent {
	result := make([]v1.WebhookEvent, 0, len(events))
	for _, e := range events {
		result = append(result, stringToEvent(e))
	}
	return result
}

func stringToEvent(s string) v1.WebhookEvent {
	switch s {
	case "push":
		return v1.WebhookEvent_WEBHOOK_EVENT_PUSH
	case "pull":
		return v1.WebhookEvent_WEBHOOK_EVENT_PULL
	case "delete":
		return v1.WebhookEvent_WEBHOOK_EVENT_DELETE
	default:
		return v1.WebhookEvent_WEBHOOK_EVENT_UNSPECIFIED
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
