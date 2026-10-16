package services

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.NotificationServiceHandler = (*NotificationService)(nil)

type NotificationService struct {
	store *stores.Store
	forge *forge.Manager
	log   *logger.Logger
}

func NewNotificationService(store *stores.Store, fm *forge.Manager, log *logger.Logger) *NotificationService {
	return &NotificationService{store: store, forge: fm, log: log}
}

func (s *NotificationService) user(ctx context.Context) (*auth.AuthenticatedUser, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	return user, nil
}

func notifStatusProto(status string) v1.NotificationStatus {
	switch status {
	case "unread":
		return v1.NotificationStatus_NOTIFICATION_STATUS_UNREAD
	case "read":
		return v1.NotificationStatus_NOTIFICATION_STATUS_READ
	case "pinned":
		return v1.NotificationStatus_NOTIFICATION_STATUS_PINNED
	}
	return v1.NotificationStatus_NOTIFICATION_STATUS_UNSPECIFIED
}

func notifStatusString(status v1.NotificationStatus) string {
	switch status {
	case v1.NotificationStatus_NOTIFICATION_STATUS_UNREAD:
		return "unread"
	case v1.NotificationStatus_NOTIFICATION_STATUS_READ:
		return "read"
	case v1.NotificationStatus_NOTIFICATION_STATUS_PINNED:
		return "pinned"
	}
	return ""
}

func notifModeProto(mode string) v1.NotificationMode {
	switch mode {
	case "all":
		return v1.NotificationMode_NOTIFICATION_MODE_ALL
	case "none":
		return v1.NotificationMode_NOTIFICATION_MODE_NONE
	}
	return v1.NotificationMode_NOTIFICATION_MODE_PARTICIPATING
}

func notifModeString(mode v1.NotificationMode) string {
	switch mode {
	case v1.NotificationMode_NOTIFICATION_MODE_ALL:
		return "all"
	case v1.NotificationMode_NOTIFICATION_MODE_NONE:
		return "none"
	}
	return "participating"
}

// Converts rows with their repositories and issues loaded once
func (s *NotificationService) toProtos(ctx context.Context, rows []*storage.GitNotification) []*v1.Notification {
	repoIDs := map[string]bool{}
	var issueIDs []string
	for _, n := range rows {
		repoIDs[n.RepoID] = true
		if n.IssueID != nil {
			issueIDs = append(issueIDs, *n.IssueID)
		}
	}
	repos, _ := s.store.GetGitRepositoriesByIDs(ctx, sortedStrings(repoIDs))
	issues := map[string]*storage.GitIssue{}
	if list, err := s.store.GetGitIssuesByIDs(ctx, issueIDs); err == nil {
		for _, is := range list {
			issues[is.ID] = is
		}
	}
	out := make([]*v1.Notification, 0, len(rows))
	for _, n := range rows {
		p := &v1.Notification{
			Id: n.ID, Kind: n.Kind, Title: n.Title, Reason: n.Reason, Sha: n.SHA,
			Status: notifStatusProto(n.Status), UpdatedBy: n.UpdatedBy, UpdatedAt: timestamppb.New(n.UpdatedAt),
		}
		repo := repos[n.RepoID]
		if repo != nil {
			p.RepoFullName = repo.Namespace + "/" + repo.Name
			p.HtmlUrl = s.forge.Git().HTMLURL(ctx, repo)
			switch n.Kind {
			case forge.NotifyCommit:
				p.HtmlUrl += "/commit/" + n.SHA
			case forge.NotifyRelease:
				p.HtmlUrl += "/releases"
			case forge.NotifyWorkflowRun:
				p.HtmlUrl += "/actions"
			case forge.NotifyInvite, forge.NotifyTransfer:
				p.HtmlUrl = s.forge.Git().BaseURL(ctx) + "/notifications"
			}
		}
		if n.IssueID != nil {
			if is := issues[*n.IssueID]; is != nil {
				p.Number = is.Number
				if repo != nil {
					p.HtmlUrl = s.forge.IssueURL(ctx, repo, is)
				}
			}
		}
		out = append(out, p)
	}
	return out
}

func (s *NotificationService) repoID(ctx context.Context, fullName string) (string, error) {
	if fullName == "" {
		return "", nil
	}
	ns, name, ok := strings.Cut(fullName, "/")
	if !ok {
		return "", connect.Errorf(connect.CodeInvalidArgument, "repository must be namespace/name")
	}
	repo, err := s.store.GetGitRepository(ctx, ns, name)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if repo == nil {
		return "", connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	return repo.ID, nil
}

func (s *NotificationService) ListNotifications(ctx context.Context, req *v1.ListNotificationsRequest) (*v1.ListNotificationsResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	repoID, err := s.repoID(ctx, req.RepoFullName)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListGitNotifications(ctx, user.ID, notifStatusString(req.Status), repoID, req.All, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	unread, _ := s.store.CountUnreadGitNotifications(ctx, user.ID)
	return &v1.ListNotificationsResponse{Notifications: s.toProtos(ctx, rows), Page: pages.Info(offset, limit, total), UnreadCount: unread}, nil
}

func (s *NotificationService) GetUnreadCount(ctx context.Context, req *v1.GetUnreadCountRequest) (*v1.GetUnreadCountResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.store.CountUnreadGitNotifications(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.GetUnreadCountResponse{UnreadCount: n}, nil
}

func (s *NotificationService) MarkRead(ctx context.Context, req *v1.MarkReadRequest) (*v1.MarkReadResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	status := "read"
	if req.Unread {
		status = "unread"
	}
	if err := s.store.SetGitNotificationStatus(ctx, user.ID, req.Ids, status); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.MarkReadResponse{UnreadCount: s.forge.PublishUnread(ctx, user.ID)}, nil
}

func (s *NotificationService) MarkAllRead(ctx context.Context, req *v1.MarkAllReadRequest) (*v1.MarkAllReadResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	repoID, err := s.repoID(ctx, req.RepoFullName)
	if err != nil {
		return nil, err
	}
	if err := s.store.MarkAllGitNotificationsRead(ctx, user.ID, repoID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.MarkAllReadResponse{UnreadCount: s.forge.PublishUnread(ctx, user.ID)}, nil
}

func (s *NotificationService) PinNotification(ctx context.Context, req *v1.PinNotificationRequest) (*v1.PinNotificationResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.store.GetGitNotification(ctx, user.ID, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if n == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "notification not found")
	}
	status := "read"
	if req.Pinned {
		status = "pinned"
	}
	if err := s.store.SetGitNotificationStatus(ctx, user.ID, []string{n.ID}, status); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	n.Status = status
	s.forge.PublishUnread(ctx, user.ID)
	return &v1.PinNotificationResponse{Notification: s.toProtos(ctx, []*storage.GitNotification{n})[0]}, nil
}

func (s *NotificationService) WatchNotifications(ctx context.Context, req *v1.WatchNotificationsRequest, stream distrofacev1connect.NotificationServiceWatchNotificationsServerStream) error {
	user, err := s.user(ctx)
	if err != nil {
		return err
	}
	ch, cancel := s.forge.WatchUnread(user.ID)
	defer cancel()
	count, _ := s.store.CountUnreadGitNotifications(ctx, user.ID)
	if err := stream.Send(&v1.NotificationEvent{UnreadCount: count}); err != nil {
		return err
	}
	ticker := time.NewTicker(watchKeepalive)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case n := <-ch:
			if err := stream.Send(&v1.NotificationEvent{UnreadCount: n}); err != nil {
				return err
			}
		case <-ticker.C:
			if err := stream.Send(&v1.NotificationEvent{UnreadCount: count}); err != nil {
				return err
			}
		}
	}
}

func notifPrefsProto(p *storage.UserNotificationPrefs) *v1.NotificationPrefs {
	return &v1.NotificationPrefs{
		WebMode: notifModeProto(p.WebMode), EmailMode: notifModeProto(p.EmailMode),
		EmailMentions: p.EmailMentions, EmailReviewRequests: p.EmailReviewRequests,
		EmailAssignments: p.EmailAssignments, EmailOwnActivity: p.EmailOwnActivity,
	}
}

func (s *NotificationService) GetNotificationPrefs(ctx context.Context, req *v1.GetNotificationPrefsRequest) (*v1.GetNotificationPrefsResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.store.GetUserNotificationPrefs(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.GetNotificationPrefsResponse{Prefs: notifPrefsProto(p)}, nil
}

func (s *NotificationService) UpdateNotificationPrefs(ctx context.Context, req *v1.UpdateNotificationPrefsRequest) (*v1.UpdateNotificationPrefsResponse, error) {
	userID := ""
	if user := auth.UserFromContext(ctx); user != nil && !user.IsAnonymous() {
		userID = user.ID
	}
	if req.UnsubscribeToken != "" {
		tokenUser := s.forge.VerifyUnsubscribeToken(req.UnsubscribeToken)
		if tokenUser == "" {
			return nil, connect.Errorf(connect.CodePermissionDenied, "invalid unsubscribe token")
		}
		userID = tokenUser
	}
	if userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if req.Prefs == nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "prefs are required")
	}
	p, err := s.store.GetUserNotificationPrefs(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	p.UserID = userID
	p.WebMode = notifModeString(req.Prefs.WebMode)
	p.EmailMode = notifModeString(req.Prefs.EmailMode)
	p.EmailMentions = req.Prefs.EmailMentions
	p.EmailReviewRequests = req.Prefs.EmailReviewRequests
	p.EmailAssignments = req.Prefs.EmailAssignments
	p.EmailOwnActivity = req.Prefs.EmailOwnActivity
	if err := s.store.SaveUserNotificationPrefs(ctx, p); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &v1.UpdateNotificationPrefsResponse{Prefs: notifPrefsProto(p)}, nil
}
