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
	"strconv"
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
	NotifyIssue        = "issue"
	NotifyPull         = "pull"
	NotifyCommit       = "commit"
	NotifyRelease      = "release"
	NotifyMention      = "mention"
	NotifyReview       = "review"
	NotifyAssignment   = "assignment"
	NotifyInvite       = "invite"
	NotifyTransfer     = "transfer"
	NotifyRepository   = "repository"
	NotifyWorkflowRun  = "workflow_run"
	NotifyAgentSession = "agent_session"
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

// Reports whether any stream is open for the user
func (h *notifyHub) listening(userID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[userID]) > 0
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

// Fan outs give up after this long
const notifyTimeout = 2 * time.Minute

// Kinds that offer access, their explicit recipients hold none yet
func offersAccess(kind string) bool {
	return kind == NotifyInvite || kind == NotifyTransfer
}

// Creates or bumps notification rows for watchers and participants and queues mail
func (m *Manager) Notify(ctx context.Context, n Notification) {
	if n.Repo == nil {
		return
	}
	repo := *n.Repo
	n.Repo = &repo
	if n.Issue != nil {
		issue := *n.Issue
		n.Issue = &issue
	}
	if n.Actor != nil {
		actor := *n.Actor
		n.Actor = &actor
	}
	m.pending.Add(1)
	go func() {
		defer m.pending.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
		defer cancel()
		m.fanOut(ctx, n)
	}()
}

// Blocks until in flight fan outs finish or the context ends
func (m *Manager) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		m.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Resolves recipients, writes their rows, and queues their mail
func (m *Manager) fanOut(ctx context.Context, n Notification) {
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
	var participants []string
	if n.Issue != nil {
		participants, _ = m.store.GitIssueParticipants(ctx, n.Issue)
	}
	if !n.ExplicitOnly {
		add(participants, n.Reason)
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
		subs, _ := m.store.GetGitIssueSubscriptions(ctx, n.Issue.ID, recipientIDs(recipients))
		for id, on := range subs {
			if !on && !contains(n.Mentioned, id) {
				delete(recipients, id)
			}
		}
	}
	users := m.allowedRecipients(ctx, n, recipients)
	if len(recipients) == 0 {
		return
	}
	prefs, err := m.store.GetUserNotificationPrefsByIDs(ctx, recipientIDs(recipients))
	if err != nil {
		m.log.Error("notification prefs: %v", err)
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
	mc := m.mailContext(ctx, n.Repo)
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
		p := prefs[id]
		if p.WebMode != "none" {
			if err := m.store.UpsertGitNotification(ctx, row); err != nil {
				m.log.Error("notification for %s: %v", id, err)
			} else if hub.listening(id) {
				m.PublishUnread(ctx, id)
			}
		}
		if mc != nil && wantsMail(p, reason, participants, id, n) {
			m.sendNotificationMail(ctx, mc, n, users[id], reason)
		}
	}
}

func recipientIDs(recipients map[string]string) []string {
	out := make([]string, 0, len(recipients))
	for id := range recipients {
		out = append(out, id)
	}
	return out
}

// Drops inactive recipients and any who cannot see the repository
func (m *Manager) allowedRecipients(ctx context.Context, n Notification, recipients map[string]string) map[string]*db.User {
	ids := recipientIDs(recipients)
	users, err := m.store.GetUsersByIDs(ctx, ids)
	if err != nil {
		clear(recipients)
		return nil
	}
	roles, _ := m.store.GetUserRoleNamesByIDs(ctx, ids)
	var principals []*auth.AuthenticatedUser
	for id := range recipients {
		u := users[id]
		if u == nil || !u.IsActive {
			delete(recipients, id)
			continue
		}
		if offersAccess(n.Kind) && contains(n.Recipients, id) {
			continue
		}
		p := m.principal(u)
		p.Roles = roles[id]
		principals = append(principals, p)
	}
	visible := m.git.Access().CanSeeMany(ctx, principals, n.Repo)
	for _, p := range principals {
		if !visible[p.ID] {
			delete(recipients, p.ID)
		}
	}
	return users
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Email delivery rule per the recipient's mode and per kind toggles
func wantsMail(p *db.UserNotificationPrefs, reason string, participants []string, id string, n Notification) bool {
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
	return contains(participants, id) || contains(n.Recipients, id)
}

// Repository mail facts shared by every recipient of one fan out
type mailContext struct {
	scope    mailer.Scope
	host     string
	incoming *v1.MailIncomingSettings
}

// Nil when mail is off for the repository
func (m *Manager) mailContext(ctx context.Context, repo *db.GitRepository) *mailContext {
	if m.mail == nil {
		return nil
	}
	scope := mailer.Scope{Type: v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO, ID: repo.ID}
	if !m.mail.Enabled(ctx, scope) {
		return nil
	}
	settings := m.mail.Settings(ctx, scope)
	return &mailContext{scope: scope, host: mailDomain(settings.GetFromAddress()), incoming: settings.GetIncoming()}
}

// Queues one notification mail with threading headers
func (m *Manager) sendNotificationMail(ctx context.Context, mc *mailContext, n Notification, u *db.User, reason string) {
	if u == nil || u.Email == nil || *u.Email == "" {
		return
	}
	if _, err := mail.ParseAddress(*u.Email); err != nil {
		return
	}
	full := n.Repo.Namespace + "/" + n.Repo.Name
	headers := map[string]string{
		"List-Id":                 fmt.Sprintf("%s <%s.%s>", full, strings.ReplaceAll(full, "/", "."), mc.host),
		"X-Distroface-Reason":     reason,
		"X-Distroface-Repository": full,
	}
	if n.Issue != nil {
		root := fmt.Sprintf("<%s/%s/issues/%d@%s>", n.Repo.Namespace, n.Repo.Name, n.Issue.Number, mc.host)
		if n.CommentID != "" {
			headers["Message-ID"] = fmt.Sprintf("<%s/%s/issues/%d/%s@%s>", n.Repo.Namespace, n.Repo.Name, n.Issue.Number, n.CommentID, mc.host)
			headers["In-Reply-To"] = root
			headers["References"] = root
		} else {
			headers["Message-ID"] = root
		}
		if reply := m.replyAddress(ctx, mc.incoming, u.ID, n.Issue); reply != "" {
			headers["Reply-To"] = reply
		}
	}
	footer := fmt.Sprintf("\n\n--\nYou are receiving this because you were %s.\nView it at %s\nManage notifications at %s/settings/notifications?token=%s",
		reasonText(reason), n.URL, m.git.BaseURL(ctx), m.UnsubscribeToken(u.ID))
	_ = m.mail.Enqueue(ctx, mc.scope, mailer.Message{To: []string{*u.Email}, Subject: n.Subject, Text: n.Text + footer, Headers: headers})
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
func (m *Manager) replyAddress(ctx context.Context, in *v1.MailIncomingSettings, userID string, issue *db.GitIssue) string {
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

// Mailed unsubscribe links stop working after this long
const unsubscribeTokenTTL = 30 * 24 * time.Hour

// Signed token that lets an unsubscribe link change preferences without a session
func (m *Manager) UnsubscribeToken(userID string) string {
	if len(m.tokenKey) == 0 || userID == "" {
		return ""
	}
	expires := strconv.FormatInt(time.Now().Add(unsubscribeTokenTTL).Unix(), 10)
	return userID + "." + expires + "." + m.signUnsubscribe(userID, expires)
}

// User id carried by a valid unexpired unsubscribe token, empty when invalid
func (m *Manager) VerifyUnsubscribeToken(token string) string {
	if len(m.tokenKey) == 0 {
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" {
		return ""
	}
	userID, expires, sig := parts[0], parts[1], parts[2]
	exp, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return ""
	}
	if !hmac.Equal([]byte(m.signUnsubscribe(userID, expires)), []byte(sig)) {
		return ""
	}
	return userID
}

func (m *Manager) signUnsubscribe(userID, expires string) string {
	mac := hmac.New(sha256.New, m.tokenKey)
	mac.Write([]byte("unsubscribe:" + userID + ":" + expires))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
