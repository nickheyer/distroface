package mailer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/wneessen/go-mail"
)

// Delivery attempts before a message is marked failed
const maxAttempts = 5

// Messages older than this leave the delivery log
const retention = 30 * 24 * time.Hour

// Per scope sliding window for max_per_minute
type rateWindow struct {
	mu    sync.Mutex
	sends map[string][]time.Time
}

func (w *rateWindow) allow(key string, limit int32) bool {
	if limit <= 0 {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sends == nil {
		w.sends = map[string][]time.Time{}
	}
	cutoff := time.Now().Add(-time.Minute)
	kept := w.sends[key][:0]
	for _, t := range w.sends[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= int(limit) {
		w.sends[key] = kept
		return false
	}
	w.sends[key] = append(kept, time.Now())
	return true
}

// Starts the delivery worker and the incoming mail poller
func (s *Service) Start(ctx context.Context) {
	go s.runWorker(ctx)
	go s.runInbound(ctx)
}

func (s *Service) runWorker(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	cleanup := time.NewTicker(6 * time.Hour)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.drain(ctx)
		case <-cleanup.C:
			if _, err := s.store.DeleteMailMessagesBefore(ctx, time.Now().Add(-retention)); err != nil {
				s.log.Error("mail: pruning deliveries: %v", err)
			}
			_ = s.store.DeleteExpiredMailInboundTokens(ctx)
		}
	}
}

// Sends every due message once
func (s *Service) drain(ctx context.Context) {
	due, err := s.store.ListDueMailMessages(ctx, 50)
	if err != nil {
		s.log.Error("mail: listing due messages: %v", err)
		return
	}
	for _, m := range due {
		if ctx.Err() != nil {
			return
		}
		scope := Scope{Type: v1.SettingsScopeType(m.ScopeType), ID: m.ScopeID}
		ms := s.Settings(ctx, scope)
		if !s.rate.allow(scopeKey(scope), ms.GetMaxPerMinute()) {
			continue
		}
		err := s.deliver(ctx, ms, m)
		m.Attempts++
		now := time.Now().UTC()
		if err == nil {
			m.Status = StatusSent
			m.SentAt = &now
			m.LastError = ""
		} else {
			m.LastError = truncate(err.Error(), 1000)
			if m.Attempts >= maxAttempts {
				m.Status = StatusFailed
				s.log.Error("mail: giving up on %s to %s: %v", m.ID, m.To, err)
				if s.audit != nil {
					s.audit.Record(ctx, audit.Event{Action: "Mail/send", Resource: "mail", Outcome: audit.OutcomeError,
						Detail: fmt.Sprintf("%q to %s failed after %d attempts: %s", m.Subject, m.To, m.Attempts, m.LastError)})
				}
			} else {
				m.NextTry = now.Add(time.Duration(1<<uint(m.Attempts)) * time.Minute)
			}
		}
		if err := s.store.UpdateMailMessage(ctx, m); err != nil {
			s.log.Error("mail: updating %s: %v", m.ID, err)
		}
	}
}

func scopeKey(scope Scope) string {
	return fmt.Sprintf("%d:%s", scope.Type, scope.ID)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Requeues a failed message for an immediate attempt
func (s *Service) Retry(ctx context.Context, m *db.MailMessage) error {
	m.Status = StatusQueued
	m.Attempts = 0
	m.LastError = ""
	m.NextTry = time.Now().UTC()
	return s.store.UpdateMailMessage(ctx, m)
}

// Connects to the relay of a scope and sends one message
func (s *Service) deliver(ctx context.Context, ms *v1.MailSettings, m *db.MailMessage) error {
	if !ms.GetEnabled() || ms.GetSmtpHost() == "" {
		return errors.New("mail is disabled for this scope")
	}
	from := ms.GetFromAddress()
	if from == "" {
		return errors.New("no from address configured")
	}
	msg := mail.NewMsg()
	if name := ms.GetFromName(); name != "" {
		if err := msg.FromFormat(name, from); err != nil {
			return err
		}
	} else if err := msg.From(from); err != nil {
		return err
	}
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("invalid recipient %q: %w", m.To, err)
	}
	msg.Subject(m.Subject)
	msg.SetDate()
	// Every message is machine generated, auto responders must stay quiet
	msg.SetGenHeader(mail.Header("Auto-Submitted"), "auto-generated")
	msg.SetGenHeader(mail.Header("X-Auto-Response-Suppress"), "All")
	msg.SetBodyString(mail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(mail.TypeTextHTML, m.HTML)
	}
	if rt := ms.GetReplyTo(); rt != "" {
		_ = msg.ReplyTo(rt)
	}
	var headers map[string]string
	_ = json.Unmarshal([]byte(m.Headers), &headers)
	messageIDSet := false
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "message-id":
			msg.SetMessageIDWithValue(strings.Trim(v, "<>"))
			messageIDSet = true
		case "reply-to":
			_ = msg.ReplyTo(v)
		default:
			msg.SetGenHeader(mail.Header(k), v)
		}
	}
	if !messageIDSet {
		msg.SetMessageID()
	}
	client, err := s.client(ms)
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return client.DialAndSendWithContext(sendCtx, msg)
}

// Builds a go-mail client from the effective settings
func (s *Service) client(ms *v1.MailSettings) (*mail.Client, error) {
	host := ms.GetSmtpHost()
	port := int(ms.GetSmtpPort())
	if port <= 0 {
		port = 587
	}
	opts := []mail.Option{mail.WithPort(port), mail.WithTimeout(30 * time.Second)}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if ms.GetSkipTlsVerify() {
		tlsCfg.InsecureSkipVerify = true
	}
	opts = append(opts, mail.WithTLSConfig(tlsCfg))
	switch ms.GetSecurity() {
	case v1.MailSecurity_MAIL_SECURITY_NONE:
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	case v1.MailSecurity_MAIL_SECURITY_TLS:
		opts = append(opts, mail.WithSSL(), mail.WithTLSPolicy(mail.TLSMandatory))
	default:
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if ms.GetUsername() != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover), mail.WithUsername(ms.GetUsername()), mail.WithPassword(ms.GetPassword()))
	}
	if !ms.GetAllowPrivateNetworks() {
		opts = append(opts, mail.WithDialContextFunc(publicOnlyDialer()))
	}
	return mail.NewClient(host, opts...)
}

// Dialer refusing relays that resolve to private or loopback addresses
func publicOnlyDialer() mail.DialContextFunc {
	d := &net.Dialer{Timeout: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsUnspecified() {
				return nil, fmt.Errorf("mail relay %s resolves to a private address, enable allow_private_networks to use it", host)
			}
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}
