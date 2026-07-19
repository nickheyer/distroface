package stores

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── Session operations ───────────────────────────────────────────────────

func (s *Store) CreateSession(ctx context.Context, session *db.Session) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(session).Error
}

func (s *Store) GetSession(ctx context.Context, token string) (*db.Session, error) {
	var session db.Session
	err := s.db.WithContext(ctx).Preload("User").Where("token = ? AND expires_at > ?", token, time.Now()).First(&session).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return s.db.WithContext(ctx).Where("token = ?", token).Delete(&db.Session{}).Error
}

func (s *Store) CleanAllSessions(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("1 = 1").Delete(&db.Session{}).Error
}
