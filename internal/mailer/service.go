package mailer

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Delivery states of a queued message
const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusFailed = "failed"
)

// Scope a message is sent on behalf of, resolves the effective smtp tier
type Scope struct {
	Type v1.SettingsScopeType
	ID   string
}

// One outbound message
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
	Headers map[string]string
}

// Posts a reply received by mail as a comment, installed by the forge
type InboundHandler func(ctx context.Context, token *db.MailInboundToken, from, body string) error

// Service queues and delivers mail per the effective settings of a scope
type Service struct {
	store   *stores.Store
	res     *settings.Resolver
	log     *logger.Logger
	inbound InboundHandler
	rate    rateWindow
}

// Installs the reply by mail handler, set once at startup
func (s *Service) SetInboundHandler(fn InboundHandler) { s.inbound = fn }

func NewService(store *stores.Store, res *settings.Resolver, log *logger.Logger) *Service {
	return &Service{store: store, res: res, log: log}
}

// Effective mail settings for a scope, system when the scope is empty
func (s *Service) Settings(ctx context.Context, scope Scope) *v1.MailSettings {
	switch scope.Type {
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG:
		return s.res.Org(ctx, scope.ID).GetMail()
	case v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO:
		return s.res.Repo(ctx, scope.ID).GetMail()
	}
	return s.res.System(ctx).GetMail()
}

// Reports whether a scope can send mail at all
func (s *Service) Enabled(ctx context.Context, scope Scope) bool {
	ms := s.Settings(ctx, scope)
	return ms.GetEnabled() && ms.GetSmtpHost() != ""
}

// Scope whose mail settings apply to a repository event, falling back to the org and system tiers
func (s *Service) ScopeFor(ctx context.Context, repoID, orgID string) Scope {
	if repoID != "" {
		sc := Scope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO, ID: repoID}
		if s.Enabled(ctx, sc) {
			return sc
		}
	}
	if orgID != "" {
		sc := Scope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, ID: orgID}
		if s.Enabled(ctx, sc) {
			return sc
		}
	}
	return Scope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM}
}

// Instance name shown in mail layouts
func (s *Service) InstanceName(ctx context.Context) string {
	if host := s.res.System(ctx).GetServer().GetPublicHostname(); host != "" {
		return "Distroface at " + host
	}
	return "Distroface"
}

// Queues one message per recipient and returns the rows, dropped silently when mail is off
func (s *Service) EnqueueRows(ctx context.Context, scope Scope, msg Message) ([]*db.MailMessage, error) {
	if !s.Enabled(ctx, scope) {
		return nil, nil
	}
	headers := "{}"
	if len(msg.Headers) > 0 {
		if b, err := json.Marshal(msg.Headers); err == nil {
			headers = string(b)
		}
	}
	var rows []*db.MailMessage
	for _, to := range msg.To {
		if to == "" {
			continue
		}
		row := &db.MailMessage{
			ID:        uuid.New().String(),
			ScopeType: int32(scope.Type),
			ScopeID:   scope.ID,
			To:        to,
			Subject:   msg.Subject,
			Text:      msg.Text,
			HTML:      msg.HTML,
			Headers:   headers,
			Status:    StatusQueued,
			CreatedAt: time.Now(),
		}
		if err := s.store.CreateMailMessage(ctx, row); err != nil {
			return rows, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Queues one message per recipient, dropped silently when mail is off
func (s *Service) Enqueue(ctx context.Context, scope Scope, msg Message) error {
	if !s.Enabled(ctx, scope) {
		return nil
	}
	headers := "{}"
	if len(msg.Headers) > 0 {
		if b, err := json.Marshal(msg.Headers); err == nil {
			headers = string(b)
		}
	}
	for _, to := range msg.To {
		if to == "" {
			continue
		}
		row := &db.MailMessage{
			ID:        uuid.New().String(),
			ScopeType: int32(scope.Type),
			ScopeID:   scope.ID,
			To:        to,
			Subject:   msg.Subject,
			Text:      msg.Text,
			HTML:      msg.HTML,
			Headers:   headers,
			Status:    StatusQueued,
			CreatedAt: time.Now(),
		}
		if err := s.store.CreateMailMessage(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
