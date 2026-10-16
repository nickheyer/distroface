package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Creates or bumps the notification for a user and target
func (s *Store) UpsertGitNotification(ctx context.Context, n *db.GitNotification) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Where("user_id = ? AND repo_id = ?", n.UserID, n.RepoID)
		if n.IssueID != nil {
			q = q.Where("issue_id = ?", *n.IssueID)
		} else {
			q = q.Where("issue_id IS NULL AND kind = ? AND sha = ?", n.Kind, n.SHA)
		}
		var existing db.GitNotification
		err := q.First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if n.ID == "" {
				n.ID = uuid.New().String()
			}
			n.Status = "unread"
			return tx.Create(n).Error
		}
		if err != nil {
			return err
		}
		status := "unread"
		if existing.Status == "pinned" {
			status = "pinned"
		}
		n.ID = existing.ID
		return tx.Model(&existing).Updates(map[string]any{
			"title": n.Title, "reason": n.Reason, "updated_by": n.UpdatedBy, "status": status, "updated_at": time.Now().UTC(),
		}).Error
	})
}

func (s *Store) GetGitNotification(ctx context.Context, userID, id string) (*db.GitNotification, error) {
	var n db.GitNotification
	err := s.db.WithContext(ctx).First(&n, "user_id = ? AND id = ?", userID, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &n, err
}

func (s *Store) ListGitNotifications(ctx context.Context, userID, status, repoID string, all bool, limit, offset int) ([]*db.GitNotification, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitNotification{}).Where("user_id = ?", userID)
	switch {
	case status != "":
		q = q.Where("status = ?", status)
	case !all:
		q = q.Where("status IN ?", []string{"unread", "pinned"})
	}
	if repoID != "" {
		q = q.Where("repo_id = ?", repoID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitNotification
	err := q.Order("CASE status WHEN 'pinned' THEN 0 ELSE 1 END, updated_at DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (s *Store) CountUnreadGitNotifications(ctx context.Context, userID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.GitNotification{}).Where("user_id = ? AND status = ?", userID, "unread").Count(&n).Error
	return n, err
}

func (s *Store) SetGitNotificationStatus(ctx context.Context, userID string, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&db.GitNotification{}).Where("user_id = ? AND id IN ?", userID, ids).Update("status", status).Error
}

func (s *Store) MarkAllGitNotificationsRead(ctx context.Context, userID, repoID string) error {
	q := s.db.WithContext(ctx).Model(&db.GitNotification{}).Where("user_id = ? AND status = ?", userID, "unread")
	if repoID != "" {
		q = q.Where("repo_id = ?", repoID)
	}
	return q.Update("status", "read").Error
}

func (s *Store) DeleteGitNotificationsByIssue(ctx context.Context, issueID string) error {
	return s.db.WithContext(ctx).Where("issue_id = ?", issueID).Delete(&db.GitNotification{}).Error
}

func (s *Store) GetUserNotificationPrefs(ctx context.Context, userID string) (*db.UserNotificationPrefs, error) {
	var p db.UserNotificationPrefs
	err := s.db.WithContext(ctx).First(&p, "user_id = ?", userID).Error
	if err == gorm.ErrRecordNotFound {
		return &db.UserNotificationPrefs{
			UserID: userID, WebMode: "participating", EmailMode: "participating",
			EmailMentions: true, EmailReviewRequests: true, EmailAssignments: true,
		}, nil
	}
	return &p, err
}

func (s *Store) SaveUserNotificationPrefs(ctx context.Context, p *db.UserNotificationPrefs) error {
	return s.db.WithContext(ctx).Save(p).Error
}
