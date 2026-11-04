package admin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/nickheyer/distroface/internal/cache/cachetest"
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

// Two collectors on one storage never sweep at the same time
func TestCollectorsShareOneLease(t *testing.T) {
	client, srv := cachetest.New(t)
	peer := cachetest.Attach(t, srv)
	dir := t.TempDir()
	a, err := NewCollector(dir, logger.New())
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewCollector(dir, logger.New())
	if err != nil {
		t.Fatal(err)
	}
	a.SetShared(client)
	b.SetShared(peer)

	release := make(chan struct{})
	entered := make(chan struct{})
	a.SetCacheReset(func(context.Context) error {
		close(entered)
		<-release
		return nil
	})
	if err := a.Start(false, false); err != nil {
		t.Fatal(err)
	}
	<-entered

	// The peer sees the run and refuses to overlap it
	st := b.State()
	if !st.Running || st.RunningOn != client.ID() {
		t.Errorf("peer state = %+v", st)
	}
	err = b.Start(false, false)
	if !errors.Is(err, ErrAlreadyRunning) || !strings.Contains(err.Error(), client.ID()) {
		t.Errorf("peer Start error = %v", err)
	}
	close(release)

	run := waitForRun(t, a)
	if run.RanOn != client.ID() {
		t.Errorf("RanOn = %q", run.RanOn)
	}
	// The result reaches the peer
	st = b.State()
	if st.Running || st.Last == nil || st.Last.RanOn != client.ID() {
		t.Errorf("peer state after run = %+v", st)
	}
	if err := b.Start(true, false); err != nil {
		t.Errorf("peer cannot run once the lease is free: %v", err)
	}
	waitForRun(t, b)
}

func TestCollectorFinishHooksRun(t *testing.T) {
	c, err := NewCollector(t.TempDir(), logger.New())
	if err != nil {
		t.Fatal(err)
	}
	var runs, dry atomic.Int32
	c.OnFinish(func(r *Run) {
		runs.Add(1)
		if r.DryRun {
			dry.Add(1)
		}
	})
	if err := c.Start(true, false); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, c)
	if runs.Load() != 1 || dry.Load() != 1 {
		t.Errorf("hooks ran %d times, %d saw a dry run", runs.Load(), dry.Load())
	}
}

// Running stays true and Last stays unset until every finish hook returns
func TestCollectorRunningCoversFinishHooks(t *testing.T) {
	c, err := NewCollector(t.TempDir(), logger.New())
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	c.OnFinish(func(*Run) {
		close(entered)
		<-release
	})
	if err := c.Start(true, false); err != nil {
		t.Fatal(err)
	}
	<-entered
	if running, last := c.Status(); !running || last != nil {
		t.Errorf("during hook running=%v last=%v", running, last)
	}
	if err := c.Start(true, false); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("Start during hook = %v", err)
	}
	close(release)
	waitForRun(t, c)
}

// The shared schedule clock keeps peers from piling on and survives restarts
func TestCollectorDueIsSharedAndClaimed(t *testing.T) {
	client, srv := cachetest.New(t)
	peer := cachetest.Attach(t, srv)
	a, _ := NewCollector(t.TempDir(), logger.New())
	b, _ := NewCollector(t.TempDir(), logger.New())
	a.SetShared(client)
	b.SetShared(peer)
	ctx := context.Background()
	if !a.due(ctx, time.Hour) {
		t.Fatal("first check not due")
	}
	if b.due(ctx, time.Hour) {
		t.Error("peer also claimed the slot")
	}
	fresh, _ := NewCollector(t.TempDir(), logger.New())
	fresh.SetShared(cachetest.Attach(t, srv))
	if fresh.due(ctx, time.Hour) {
		t.Error("a restarted instance ran early")
	}
	srv.FastForward(2 * time.Minute)
	if fresh.due(ctx, time.Minute) {
		// The claim expired but the stamp says a run just happened
		t.Error("stamp ignored after the claim lapsed")
	}
}

// An unreferenced blob is measured by a dry run and removed by a real one
func TestCollectorCountsSweptBlobs(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCollector(dir, logger.New())
	if err != nil {
		t.Fatal(err)
	}
	c.SetWriteGate(&fakeGate{})
	payload := bytes.Repeat([]byte("x"), 1234)
	dgst := digest.FromBytes(payload)
	blobDir := filepath.Join(dir, "docker", "registry", "v2", "blobs", "sha256", dgst.Encoded()[:2], dgst.Encoded())
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, "data"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.Start(true, false); err != nil {
		t.Fatal(err)
	}
	run := waitForRun(t, c)
	if run.Err != "" || run.BlobsDeleted != 1 || run.BytesFreed != 1234 {
		t.Fatalf("dry run = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(blobDir, "data")); err != nil {
		t.Fatalf("dry run removed the blob: %v", err)
	}

	if err := c.Start(false, false); err != nil {
		t.Fatal(err)
	}
	run = waitForRun(t, c)
	if run.Err != "" {
		t.Fatalf("run error %q", run.Err)
	}
	if run.BlobsDeleted != 1 || run.BytesFreed != 1234 {
		t.Errorf("counted blobs=%d bytes=%d, want 1 and 1234", run.BlobsDeleted, run.BytesFreed)
	}
	if _, err := os.Stat(blobDir); !os.IsNotExist(err) {
		t.Errorf("blob directory survived the sweep: %v", err)
	}
}

func TestIsBlobDir(t *testing.T) {
	cases := map[string]bool{
		"/docker/registry/v2/blobs/sha256/ab/abcdef":        true,
		"/docker/registry/v2/blobs/sha256/ab/abcdef/":       false,
		"/docker/registry/v2/blobs/sha256/ab/abcdef/data":   false,
		"/docker/registry/v2/blobs/sha256/ab":               false,
		"/docker/registry/v2/repositories/ns/app/_layers/x": false,
	}
	for p, want := range cases {
		if got := isBlobDir(p); got != want {
			t.Errorf("isBlobDir(%q) = %v", p, got)
		}
	}
}
