package audit

import (
	"context"
	"time"

	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Outcome constants
const (
	OutcomeSuccess = "success"
	OutcomeDenied  = "denied"
	OutcomeError   = "error"
)

type Event struct {
	Action   string
	Resource string
	Outcome  string
	Detail   string
	SourceIP string
}

// Writes security events to the db, nil recorder drops everything
type Recorder struct {
	store *stores.Store
	log   *logger.Logger
}

func NewRecorder(store *stores.Store, log *logger.Logger) *Recorder {
	return &Recorder{store: store, log: log}
}

// Actor comes from the auth context, failures only log
func (r *Recorder) Record(ctx context.Context, ev Event) {
	if r == nil {
		return
	}
	record := &storage.AuditEvent{
		Action:   ev.Action,
		Resource: ev.Resource,
		Outcome:  ev.Outcome,
		Detail:   ev.Detail,
		SourceIP: ev.SourceIP,
	}
	if user := auth.UserFromContext(ctx); user != nil {
		record.Actor = user.Username
		record.ActorID = user.ID
	}
	// Context may already be canceled, the write should still land
	if err := r.store.CreateAuditEvent(context.WithoutCancel(ctx), record); err != nil {
		r.log.Error("audit write failed for %s: %v", ev.Action, err)
	}
}

// Prunes old events now and then daily until ctx ends
func (r *Recorder) ScheduleRetention(ctx context.Context, retentionDays int) {
	if r == nil || retentionDays <= 0 {
		return
	}
	prune := func() {
		// Store timestamps are utc, keep comparisons in the same zone
		cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
		if n, err := r.store.DeleteAuditEventsBefore(ctx, cutoff); err != nil {
			r.log.Error("audit retention prune failed: %v", err)
		} else if n > 0 {
			r.log.Info("audit retention pruned %d events", n)
		}
	}
	go func() {
		prune()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				prune()
			}
		}
	}()
}
