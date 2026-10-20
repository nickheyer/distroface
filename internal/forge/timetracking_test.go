package forge

import (
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

func TestOpenSecondsSumsOpenIntervals(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issue := &db.GitIssue{CreatedAt: t0, State: db.IssueStateOpen}
	events := []stores.IssueStateEvent{
		{Type: EventClosed, CreatedAt: t0.Add(2 * time.Hour)},
		{Type: EventReopened, CreatedAt: t0.Add(10 * time.Hour)},
	}
	got := OpenSeconds(issue, events, t0.Add(11*time.Hour))
	if want := int64(3 * 3600); got != want {
		t.Fatalf("open seconds = %d, want %d", got, want)
	}
}

func TestOpenSecondsStopsAtClose(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	closed := t0.Add(90 * time.Minute)
	issue := &db.GitIssue{CreatedAt: t0, State: db.IssueStateClosed, ClosedAt: &closed}
	events := []stores.IssueStateEvent{{Type: EventClosed, CreatedAt: closed}}
	got := OpenSeconds(issue, events, t0.Add(48*time.Hour))
	if want := int64(5400); got != want {
		t.Fatalf("open seconds = %d, want %d", got, want)
	}
}

func TestOpenSecondsClosedWithoutEventsUsesClosedAt(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	closed := t0.Add(time.Hour)
	issue := &db.GitIssue{CreatedAt: t0, State: db.IssueStateClosed, ClosedAt: &closed}
	if got := OpenSeconds(issue, nil, t0.Add(72*time.Hour)); got != 3600 {
		t.Fatalf("open seconds = %d, want 3600", got)
	}
}

func TestOpenSecondsIgnoresDuplicateFlips(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issue := &db.GitIssue{CreatedAt: t0, State: db.IssueStateOpen}
	events := []stores.IssueStateEvent{
		{Type: EventReopened, CreatedAt: t0.Add(time.Hour)},
		{Type: EventClosed, CreatedAt: t0.Add(2 * time.Hour)},
		{Type: EventClosed, CreatedAt: t0.Add(3 * time.Hour)},
		{Type: EventReopened, CreatedAt: t0.Add(4 * time.Hour)},
	}
	if got := OpenSeconds(issue, events, t0.Add(5*time.Hour)); got != 3*3600 {
		t.Fatalf("open seconds = %d, want %d", got, 3*3600)
	}
}
