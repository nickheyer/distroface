package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

func (s *Store) CreateMailMessage(ctx context.Context, m *db.MailMessage) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.NextTry.IsZero() {
		m.NextTry = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Create(m).Error
}

func (s *Store) GetMailMessage(ctx context.Context, id string) (*db.MailMessage, error) {
	var m db.MailMessage
	err := s.db.WithContext(ctx).First(&m, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &m, err
}

// Queued messages whose retry time has come
func (s *Store) ListDueMailMessages(ctx context.Context, limit int) ([]*db.MailMessage, error) {
	var out []*db.MailMessage
	err := s.db.WithContext(ctx).Where("status = ? AND next_try <= ?", "queued", time.Now().UTC()).Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *Store) ListMailMessages(ctx context.Context, scopeType int32, scopeID, status string, limit, offset int) ([]*db.MailMessage, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.MailMessage{}).Where("scope_type = ? AND scope_id = ?", scopeType, scopeID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.MailMessage
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (s *Store) UpdateMailMessage(ctx context.Context, m *db.MailMessage) error {
	return s.db.WithContext(ctx).Save(m).Error
}

func (s *Store) DeleteMailMessagesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("status <> ? AND created_at < ?", "queued", cutoff).Delete(&db.MailMessage{})
	return res.RowsAffected, res.Error
}

func (s *Store) CreateMailInboundToken(ctx context.Context, t *db.MailInboundToken) error {
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *Store) GetMailInboundToken(ctx context.Context, hash string) (*db.MailInboundToken, error) {
	var t db.MailInboundToken
	err := s.db.WithContext(ctx).First(&t, "token_hash = ? AND expires_at > ?", hash, time.Now().UTC()).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &t, err
}

func (s *Store) DeleteExpiredMailInboundTokens(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("expires_at <= ?", time.Now().UTC()).Delete(&db.MailInboundToken{}).Error
}

func (s *Store) CreatePasswordReset(ctx context.Context, r *db.PasswordReset) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *Store) GetPasswordReset(ctx context.Context, hash string) (*db.PasswordReset, error) {
	var r db.PasswordReset
	err := s.db.WithContext(ctx).First(&r, "token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, time.Now().UTC()).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &r, err
}

func (s *Store) UsePasswordReset(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.PasswordReset{}).Where("id = ?", id).Update("used_at", time.Now().UTC()).Error
}

func (s *Store) CreateEmailVerification(ctx context.Context, v *db.EmailVerification) error {
	if v.ID == "" {
		v.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(v).Error
}

func (s *Store) GetEmailVerification(ctx context.Context, hash string) (*db.EmailVerification, error) {
	var v db.EmailVerification
	err := s.db.WithContext(ctx).First(&v, "token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, time.Now().UTC()).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &v, err
}

func (s *Store) UseEmailVerification(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.EmailVerification{}).Where("id = ?", id).Update("used_at", time.Now().UTC()).Error
}

func (s *Store) SetUserEmailVerified(ctx context.Context, userID string, at time.Time) error {
	return s.db.WithContext(ctx).Model(&db.User{}).Where("id = ?", userID).Update("email_verified_at", at).Error
}
