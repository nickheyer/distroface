package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Exec lifecycle states
const (
	ExecQueued    = "queued"
	ExecRunning   = "running"
	ExecCompleted = "completed"
)

// Appends a command numbered after the last one
func (s *Store) CreateActionsJobExec(ctx context.Context, e *db.ActionsJobExec) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.Status == "" {
		e.Status = ExecQueued
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var last int
		if err := tx.Model(&db.ActionsJobExec{}).Where("job_id = ?", e.JobID).Select("COALESCE(MAX(number), 0)").Scan(&last).Error; err != nil {
			return err
		}
		e.Number = last + 1
		return tx.Create(e).Error
	})
}

func (s *Store) GetActionsJobExec(ctx context.Context, id string) (*db.ActionsJobExec, error) {
	var e db.ActionsJobExec
	err := s.db.WithContext(ctx).First(&e, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &e, err
}

// Oldest command still waiting on a job, nil when none
func (s *Store) NextQueuedActionsJobExec(ctx context.Context, jobID string) (*db.ActionsJobExec, error) {
	var e db.ActionsJobExec
	err := s.db.WithContext(ctx).Where("job_id = ? AND status = ?", jobID, ExecQueued).Order("number ASC").First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &e, err
}

// Claims a queued command, false when another poller won
func (s *Store) StartActionsJobExec(ctx context.Context, id string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&db.ActionsJobExec{}).Where("id = ? AND status = ?", id, ExecQueued).
		Updates(map[string]any{"status": ExecRunning, "started_at": time.Now().UTC()})
	return res.RowsAffected > 0, res.Error
}

func (s *Store) UpdateActionsJobExecColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.ActionsJobExec{}).Where("id = ?", id).Updates(cols).Error
}

func (s *Store) ListActionsJobExecs(ctx context.Context, jobID string) ([]*db.ActionsJobExec, error) {
	var out []*db.ActionsJobExec
	err := s.db.WithContext(ctx).Where("job_id = ?", jobID).Order("number ASC").Find(&out).Error
	return out, err
}

// Closes every unfinished command with one conclusion
func (s *Store) FinishOpenActionsJobExecs(ctx context.Context, jobID, conclusion, errText string) ([]string, error) {
	var ids []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.ActionsJobExec{}).Where("job_id = ? AND status <> ?", jobID, ExecCompleted).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		return tx.Model(&db.ActionsJobExec{}).Where("id IN ?", ids).
			Updates(map[string]any{"status": ExecCompleted, "conclusion": conclusion, "error": errText, "finished_at": time.Now().UTC()}).Error
	})
	return ids, err
}

// Live interactive jobs of an agent session, newest first
func (s *Store) ListLiveInteractiveJobsForAgentSession(ctx context.Context, sessionID string) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Model(&db.ActionsJob{}).
		Joins("JOIN actions_runs ON actions_runs.id = actions_jobs.run_id").
		Where("actions_runs.agent_session_id = ? AND actions_jobs.interactive = ? AND actions_jobs.status IN ?", sessionID, true, []string{"waiting", "queued", "running"}).
		Order("actions_jobs.created_at DESC").Find(&out).Error
	return out, err
}
