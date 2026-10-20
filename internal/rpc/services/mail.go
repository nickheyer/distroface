package services

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.MailServiceHandler = (*MailService)(nil)

type MailService struct {
	store    *stores.Store
	mailer   *mailer.Service
	manager  *gitrepo.Manager
	enforcer *rbac.Enforcer
	log      *logger.Logger
}

func NewMailService(store *stores.Store, ms *mailer.Service, manager *gitrepo.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *MailService {
	return &MailService{store: store, mailer: ms, manager: manager, enforcer: enforcer, log: log}
}

// Checks the caller administers the settings scope a mail belongs to
func (s *MailService) requireScopeAdmin(ctx context.Context, scope *v1.SettingsScope) (mailer.Scope, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return mailer.Scope{}, connect.NewError(connect.CodeUnauthenticated, "")
	}
	out := mailer.Scope{Type: scope.GetType(), ID: scope.GetScopeId()}
	if out.Type == v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_UNSPECIFIED {
		out.Type = v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM
	}
	systemAdmin, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceSettings, rbac.ActionManage, "*")
	switch out.Type {
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM:
		if !systemAdmin {
			return out, connect.NewError(connect.CodePermissionDenied, "")
		}
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG:
		if out.ID == "" {
			return out, connect.Errorf(connect.CodeInvalidArgument, "scope_id required")
		}
		if systemAdmin {
			return out, nil
		}
		if canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, out.ID); canManage {
			return out, nil
		}
		member, _ := s.store.GetOrgMember(ctx, out.ID, user.ID)
		if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
			return out, connect.NewError(connect.CodePermissionDenied, "")
		}
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO:
		if out.ID == "" {
			return out, connect.Errorf(connect.CodeInvalidArgument, "scope_id required")
		}
		repo, err := s.store.GetGitRepositoryByID(ctx, out.ID)
		if err != nil {
			return out, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if repo == nil || s.manager == nil || !s.manager.Access().CanSee(ctx, user, repo) {
			return out, connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		if systemAdmin {
			return out, nil
		}
		lv := s.manager.Access().Levels(ctx, user, repo)
		if !lv.CanAdmin && !lv.IsOwner {
			return out, connect.NewError(connect.CodePermissionDenied, "")
		}
	default:
		return out, connect.Errorf(connect.CodeInvalidArgument, "mail is configured at the system, organization, or repository tier")
	}
	return out, nil
}

func mailStatusProto(status string) v1.MailStatus {
	switch status {
	case mailer.StatusQueued:
		return v1.MailStatus_MAIL_STATUS_QUEUED
	case mailer.StatusSent:
		return v1.MailStatus_MAIL_STATUS_SENT
	case mailer.StatusFailed:
		return v1.MailStatus_MAIL_STATUS_FAILED
	}
	return v1.MailStatus_MAIL_STATUS_UNSPECIFIED
}

func mailStatusString(status v1.MailStatus) string {
	switch status {
	case v1.MailStatus_MAIL_STATUS_QUEUED:
		return mailer.StatusQueued
	case v1.MailStatus_MAIL_STATUS_SENT:
		return mailer.StatusSent
	case v1.MailStatus_MAIL_STATUS_FAILED:
		return mailer.StatusFailed
	}
	return ""
}

func mailDeliveryProto(m *storage.MailMessage) *v1.MailDelivery {
	out := &v1.MailDelivery{
		Id: m.ID, Scope: &v1.SettingsScope{Type: v1.SettingsScopeType(m.ScopeType), ScopeId: m.ScopeID},
		To: m.To, Subject: m.Subject, Status: mailStatusProto(m.Status), Attempts: int32(m.Attempts), LastError: m.LastError,
		CreatedAt: timestamppb.New(m.CreatedAt),
	}
	if m.SentAt != nil {
		out.SentAt = timestamppb.New(*m.SentAt)
	}
	return out
}

func (s *MailService) SendTestEmail(ctx context.Context, req *v1.SendTestEmailRequest) (*v1.SendTestEmailResponse, error) {
	scope, err := s.requireScopeAdmin(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	user := auth.UserFromContext(ctx)
	to := req.To
	if to == "" {
		if u, _ := s.store.GetUserByID(ctx, user.ID); u != nil && u.Email != nil {
			to = *u.Email
		}
	}
	if to == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a recipient is required because your account has no email address")
	}
	if !s.mailer.Enabled(ctx, scope) {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "mail is not enabled or has no smtp host at this tier")
	}
	text, html := mailer.Render(mailer.Template{
		Instance: s.mailer.InstanceName(ctx), Title: "Test email",
		Intro: "This message confirms the mail settings of this tier deliver. Requested by " + user.Username + ".",
	})
	rows, err := s.mailer.EnqueueRows(ctx, scope, mailer.Message{To: []string{to}, Subject: "[Distroface] Test email", Text: text, HTML: html})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if len(rows) == 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "mail is not enabled at this tier")
	}
	return &v1.SendTestEmailResponse{Delivery: mailDeliveryProto(rows[0])}, nil
}

func (s *MailService) ListMailDeliveries(ctx context.Context, req *v1.ListMailDeliveriesRequest) (*v1.ListMailDeliveriesResponse, error) {
	scope, err := s.requireScopeAdmin(ctx, req.Scope)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListMailMessages(ctx, int32(scope.Type), scope.ID, mailStatusString(req.Status), limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListMailDeliveriesResponse{Page: pages.Info(offset, limit, total)}
	for _, m := range rows {
		out.Deliveries = append(out.Deliveries, mailDeliveryProto(m))
	}
	return out, nil
}

func (s *MailService) RetryMailDelivery(ctx context.Context, req *v1.RetryMailDeliveryRequest) (*v1.RetryMailDeliveryResponse, error) {
	m, err := s.store.GetMailMessage(ctx, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if m == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "delivery not found")
	}
	if _, err := s.requireScopeAdmin(ctx, &v1.SettingsScope{Type: v1.SettingsScopeType(m.ScopeType), ScopeId: m.ScopeID}); err != nil {
		return nil, err
	}
	if m.Status == mailer.StatusQueued {
		return &v1.RetryMailDeliveryResponse{Delivery: mailDeliveryProto(m)}, nil
	}
	if err := s.mailer.Retry(ctx, m); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.RetryMailDeliveryResponse{Delivery: mailDeliveryProto(m)}, nil
}
