package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nickheyer/distroface/pkg/logger"
)

type fakeGate struct {
	freezes, thaws int
	fail           error
}

func (g *fakeGate) Freeze(context.Context) error {
	if g.fail != nil {
		return g.fail
	}
	g.freezes++
	return nil
}

func (g *fakeGate) Thaw() { g.thaws++ }

func waitForRun(t *testing.T, c *Collector) *Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if running, last := c.Status(); !running && last != nil {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gc never finished")
	return nil
}

func TestCollectorPausesWritesAndResetsCache(t *testing.T) {
	c, err := NewCollector(t.TempDir(), logger.New())
	if err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{}
	resets := 0
	c.SetWriteGate(gate)
	c.SetCacheReset(func(context.Context) error { resets++; return nil })

	if err := c.Start(false, false); err != nil {
		t.Fatal(err)
	}
	run := waitForRun(t, c)
	if run.Err != "" {
		t.Fatalf("run error %q", run.Err)
	}
	if gate.freezes != 1 || gate.thaws != 1 || resets != 1 {
		t.Errorf("freezes=%d thaws=%d resets=%d, want one each", gate.freezes, gate.thaws, resets)
	}
}

func TestCollectorDryRunLeavesWritesAlone(t *testing.T) {
	c, err := NewCollector(t.TempDir(), logger.New())
	if err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{}
	resets := 0
	c.SetWriteGate(gate)
	c.SetCacheReset(func(context.Context) error { resets++; return nil })

	if err := c.Start(true, true); err != nil {
		t.Fatal(err)
	}
	run := waitForRun(t, c)
	if run.Err != "" || !run.DryRun {
		t.Fatalf("run = %+v", run)
	}
	if gate.freezes != 0 || resets != 0 {
		t.Errorf("dry run froze %d times and reset %d times", gate.freezes, resets)
	}
}

func TestCollectorAbortsWhenWritesWontDrain(t *testing.T) {
	c, err := NewCollector(t.TempDir(), logger.New())
	if err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{fail: errors.New("uploads still running")}
	resets := 0
	c.SetWriteGate(gate)
	c.SetCacheReset(func(context.Context) error { resets++; return nil })

	if err := c.Start(false, false); err != nil {
		t.Fatal(err)
	}
	run := waitForRun(t, c)
	if !strings.Contains(run.Err, "uploads still running") {
		t.Fatalf("run error = %q", run.Err)
	}
	if gate.thaws != 0 || resets != 0 {
		t.Errorf("aborted run thawed %d times and reset %d times", gate.thaws, resets)
	}
	if running, _ := c.Status(); running {
		t.Error("collector still marked running")
	}
}
