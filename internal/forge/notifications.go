package forge

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/mailer"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Notification kinds stored on git_notifications rows
const (
	NotifyIssue       = "issue"
	NotifyPull        = "pull"
	NotifyCommit      = "commit"
	NotifyRelease     = "release"
	NotifyMention     = "mention"
	NotifyReview      = "review"
	NotifyAssignment  = "assignment"
	NotifyInvite      = "invite"
	NotifyTransfer    = "transfer"
	NotifyRepository  = "repository"
	NotifyWorkflowRun = "workflow_run"
)

// Reply by mail tokens live this long
const inboundTokenTTL = 30 * 24 * time.Hour

// Details of one notification fan out
type Notification struct {
	Repo    *db.GitRepository
	Issue   *db.GitIssue // nil for repository level events
	SHA     string       // commit level events
	Kind    string
	Actor   *auth.AuthenticatedUser
	Reason  string // commented, closed, merged, mentioned, assigned, review_requested
	Subject string
	Text    string
	URL     string
	// Users notified regardless of watch mode, actor is always excluded
	Recipients []string
	// Per kind email toggle on the recipient's preferences
	Mentioned []string
	Reviewers []string
	Assigned  []string
	// Comment the mail threads under, when any
	CommentID string
	// Skip the participants and watchers, only explicit recipients
	ExplicitOnly bool
}

// Per user unread count stream
type notifyHub struct {
	mu   sync.Mutex
	subs map[string]map[chan int64]struct{}
}

func (h *notifyHub) subscribe(userID string) (<-chan int64, func()) {
	ch := make(chan int64, 8)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[string]map[chan int64]struct{}{}
	}
	if h.subs[userID] == nil {
		h.subs[userID] = map[chan int64]struct{}{}
	}
	h.subs[userID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[userID], ch)
		if len(h.subs[userID]) == 0 {
			delete(h.subs, userID)
		}
		h.mu.Unlock()
	}
}

func (h *notifyHub) publish(userID string, count int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[userID] {
		select {
		case ch <- count:
		default:
		}
	}
}

var hub notifyHub

// Subscribes to unread count changes for a user
func (m *Manager) WatchUnread(userID string) (<-chan int64, func()) {
	return hub.subscribe(userID)
}

// Publishes the current unread count to a user's streams
func (m *Manager) PublishUnread(ctx context.Context, userID string) int64 {
	n, _ := m.store.CountUnreadGitNotifications(ctx, userID)
	hub.publish(userID, n)
	return n
}

// Creates or bumps notification rows for watchers and participants and queues mail
func (m *Manager) Notify(ctx context.Context, n Notification) {
	if n.Repo == nil {
		return
	}
	actorID := ""
	if n.Actor != nil {
		actorID = n.Actor.ID
	}
	recipients := map[string]string{}
	add := func(ids []string, reason string) {
		for _, id := range ids {
			if id == "" || id == actorID {
				continue
			}
			if _, ok := recipients[id]; !ok {
				recipients[id] = reason
			}
		}
	}
	add(n.Mentioned, "mentioned")
	add(n.Reviewers, "review_requested")
	add(n.Assigned, "assigned")
	add(n.Recipients, n.Reason)
	if !n.ExplicitOnly {
		if n.Issue != nil {
			participants, _ := m.store.GitIssueParticipants(ctx, n.Issue)
			add(participants, n.Reason)
		}
		watchers, _ := m.store.ListGitWatchers(ctx, n.Repo.ID, "all")
		add(watchers, n.Reason)
	}
	ignoring, _ := m.store.ListGitWatchers(ctx, n.Repo.ID, "ignore")
	for _, id := range ignoring {
		if _, explicit := recipients[id]; explicit && (contains(n.Mentioned, id) || contains(n.Recipients, id)) {
			continue
		}
		delete(recipients, id)
	}
	if n.Issue != nil {
		for id := range recipients {
			if sub, _ := m.store.GetGitIssueSubscription(ctx, n.Issue.ID, id); sub != nil && !*sub && !contains(n.Mentioned, id) {
				delete(recipients, id)
			}
		}
	}
	if len(recipients) == 0 {
		return
	}
	title := n.Subject
	if n.Issue != nil {
		title = n.Issue.Title
	}
	actorName := "system"
	if n.Actor != nil {
		actorName = n.Actor.Username
	}
	for id, reason := range recipients {
		row := &db.GitNotification{
			ID:        uuid.New().String(),
			UserID:    id,
			RepoID:    n.Repo.ID,
			SHA:       n.SHA,
			Kind:      n.Kind,
			Title:     title,
			Reason:    reason,
			UpdatedBy: actorName,
		}
		if n.Issue != nil {
			row.IssueID = &n.Issue.ID
		}
		prefs := m.prefs(ctx, id)
		if prefs.WebMode != "none" {
			if err := m.store.UpsertGitNotification(ctx, row); err != nil {
				m.log.Error("notification for %s: %v", id, err)
			} else {
				m.PublishUnread(ctx, id)
			}
		}
		if m.wantsMail(prefs, reason, recipients, id, n) {
			m.sendNotificationMail(ctx, n, id, reason)
		}
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Preferences with defaults applied
func (m *Manager) prefs(ctx context.Context, userID string) *db.UserNotificationPrefs {
	p, _ := m.store.GetUserNotificationPrefs(ctx, userID)
	if p == nil {
		p = &db.UserNotificationPrefs{UserID: userID, WebMode: "participating", EmailMode: "participating", EmailMentions: true, EmailReviewRequests: true, EmailAssignments: true}
	}
	return p
}

// Email delivery rule per the recipient's mode and per kind toggles
func (m *Manager) wantsMail(p *db.UserNotificationPrefs, reason string, all map[string]string, id string, n Notification) bool {
	switch reason {
	case "mentioned":
		return p.EmailMentions
	case "review_requested":
		return p.EmailReviewRequests
	case "assigned":
		return p.EmailAssignments
	}
	switch p.EmailMode {
	case "none":
		return false
	case "all":
		return true
	}
	if n.Issue == nil {
		return contains(n.Recipients, id)
	}
	participants, _ := m.store.GitIssueParticipants(context.Background(), n.Issue)
	return contains(participants, id) || contains(n.Recipients, id)
}

// Queues one notification mail with threading headers
func (m *Manager) sendNotificationMail(ctx context.Context, n Notification, userID, reason string) {
	if m.mail == nil {
		return
	}
	scope := mailer.Scope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO, ID: n.Repo.ID}
	if !m.mail.Enabled(ctx, scope) {
		return
	}
	u, _ := m.store.GetUserByID(ctx, userID)
	if u == nil || u.Email == nil || *u.Email == "" {
		return
	}
	if _, err := mail.ParseAddress(*u.Email); err != nil {
		return
	}
	full := n.Repo.Namespace + "/" + n.Repo.Name
	host := mailDomain(m.mail.Settings(ctx, scope).GetFromAddress())
	headers := map[string]string{
		"List-Id":                 fmt.Sprintf("%s <%s.%s>", full, strings.ReplaceAll(full, "/", "."), host),
		"X-Distroface-Reason":     reason,
		"X-Distroface-Repository": full,
	}
	if n.Issue != nil {
		root := fmt.Sprintf("<%s/%s/issues/%d@%s>", n.Repo.Namespace, n.Repo.Name, n.Issue.Number, host)
		if n.CommentID != "" {
			headers["Message-ID"] = fmt.Sprintf("<%s/%s/issues/%d/%s@%s>", n.Repo.Namespace, n.Repo.Name, n.Issue.Number, n.CommentID, host)
			headers["In-Reply-To"] = root
			headers["References"] = root
		} else {
			headers["Message-ID"] = root
		}
		if reply := m.replyAddress(ctx, scope, userID, n.Issue); reply != "" {
			headers["Reply-To"] = reply
		}
	}
	footer := fmt.Sprintf("\n\n--\nYou are receiving this because you were %s.\nView it at %s\nManage notifications at %s/settings/notifications?token=%s",
		reasonText(reason), n.URL, m.git.BaseURL(ctx), m.UnsubscribeToken(userID))
	_ = m.mail.Enqueue(ctx, scope, mailer.Message{To: []string{*u.Email}, Subject: n.Subject, Text: n.Text + footer, Headers: headers})
}

func reasonText(reason string) string {
	switch reason {
	case "mentioned":
		return "mentioned"
	case "review_requested":
		return "asked to review"
	case "assigned":
		return "assigned"
	case "":
		return "watching this repository"
	}
	return "participating or watching"
}

func mailDomain(from string) string {
	if addr, err := mail.ParseAddress(from); err == nil {
		from = addr.Address
	}
	if i := strings.LastIndex(from, "@"); i >= 0 {
		return from[i+1:]
	}
	return "distroface.local"
}

// Reply address carrying a token bound to the recipient and issue
func (m *Manager) replyAddress(ctx context.Context, scope mailer.Scope, userID string, issue *db.GitIssue) string {
	in := m.mail.Settings(ctx, scope).GetIncoming()
	if !in.GetEnabled() || in.GetReplyAddress() == "" {
		return ""
	}
	local, domain, ok := strings.Cut(in.GetReplyAddress(), "@")
	if !ok {
		return ""
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	row := &db.MailInboundToken{TokenHash: hex.EncodeToString(sum[:]), UserID: userID, IssueID: issue.ID, ExpiresAt: time.Now().Add(inboundTokenTTL)}
	if err := m.store.CreateMailInboundToken(ctx, row); err != nil {
		return ""
	}
	return fmt.Sprintf("%s+%s@%s", local, token, domain)
}

// Signed token that lets an unsubscribe link change preferences without a session
func (m *Manager) UnsubscribeToken(userID string) string {
	mac := hmac.New(sha256.New, m.tokenKey())
	mac.Write([]byte("unsubscribe:" + userID))
	return userID + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// User id carried by a valid unsubscribe token, empty when invalid
func (m *Manager) VerifyUnsubscribeToken(token string) string {
	userID, sig, ok := strings.Cut(token, ".")
	if !ok || userID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, m.tokenKey())
	mac.Write([]byte("unsubscribe:" + userID))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ""
	}
	return userID
}

// Key for signed notification tokens, derived from the stored jwt secret
func (m *Manager) tokenKey() []byte {
	secret, _ := m.store.GetSystemSetting(context.Background(), "jwt_secret")
	sum := sha256.Sum256([]byte("notifications:" + secret))
	return sum[:]
}
