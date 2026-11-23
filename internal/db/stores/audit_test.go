package stores

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
)

// Events written between pages must not repeat or drop rows
func TestAuditPagingStableUnderInserts(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	record := func(action string, at time.Time) {
		t.Helper()
		if err := store.CreateAuditEvent(ctx, &db.AuditEvent{Action: action, Outcome: "success", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	// Shared timestamps exercise the id tie break
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	const seeded = 12
	inserted := 0
	for i := range seeded {
		record(fmt.Sprintf("seed%d", i), base.Add(time.Duration(i/3)*time.Second))
	}

	for _, orderBy := range []string{"created_at DESC", "created_at ASC"} {
		t.Run(orderBy, func(t *testing.T) {
			seen := map[string]bool{}
			after := ""
			walked := 0
			for page := 0; ; page++ {
				events, total, more, err := store.ListAuditEvents(ctx, pages.Query{Filters: []pages.Filter{{Field: "outcome", Match: pages.MatchEquals, Value: "success"}}}, orderBy, 5, 0, after)
				if err != nil {
					t.Fatal(err)
				}
				if want := int64(seeded + inserted); total != want {
					t.Fatalf("total = %d, want %d", total, want)
				}
				for _, ev := range events {
					if seen[ev.ID] {
						t.Errorf("page %d repeats %s", page, ev.Action)
					}
					seen[ev.ID] = true
					if strings.HasPrefix(ev.Action, "seed") {
						walked++
					}
				}
				if !more {
					break
				}
				after = events[len(events)-1].ID
				record("late", time.Now())
				inserted++
			}
			if walked != seeded {
				t.Errorf("walked %d seeded events, want %d", walked, seeded)
			}
		})
	}
}

func TestAuditPagingOffsetOrderIsTotal(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	at := time.Now().Truncate(time.Second)
	for i := range 7 {
		if err := store.CreateAuditEvent(ctx, &db.AuditEvent{Action: fmt.Sprintf("a%d", i), Outcome: "success", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for offset := 0; offset < 7; offset += 3 {
		events, _, more, err := store.ListAuditEvents(ctx, pages.Query{}, "created_at DESC", 3, offset, "")
		if err != nil {
			t.Fatal(err)
		}
		if want := offset+3 < 7; more != want {
			t.Errorf("offset %d more = %v, want %v", offset, more, want)
		}
		for _, ev := range events {
			ids = append(ids, ev.ID)
		}
	}
	if len(ids) != 7 {
		t.Fatalf("walked %d events, want 7", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] <= ids[i] {
			t.Errorf("ids not strictly descending at %d", i)
		}
	}
}
