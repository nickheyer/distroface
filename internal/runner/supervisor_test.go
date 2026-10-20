package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Records every start and stop the supervisor asks for
type fakeRunner struct {
	mu     sync.Mutex
	starts []string
	stops  int
}

func (f *fakeRunner) start(ctx context.Context, cfg *v1.BuiltinRunnerSettings) error {
	f.mu.Lock()
	f.starts = append(f.starts, cfg.GetLabels())
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	return ctx.Err()
}

func (f *fakeRunner) wait(t *testing.T, starts int, stops int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		ok := len(f.starts) == starts && f.stops == stops
		f.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("starts=%v stops=%d, want %d starts and %d stops", f.starts, f.stops, starts, stops)
}

func settingsOf(enabled bool, labels string) *v1.BuiltinRunnerSettings {
	return &v1.BuiltinRunnerSettings{Enabled: proto.Bool(enabled), Labels: proto.String(labels), Concurrency: proto.Int32(1)}
}

func TestSupervisorFollowsTheSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeRunner{}
	s := &Supervisor{o: SupervisorOptions{Log: logger.New()}, kick: make(chan struct{}, 1)}
	s.start = f.start
	go s.loop(ctx)

	apply := func(cfg *v1.BuiltinRunnerSettings) {
		s.mu.Lock()
		s.desired = cfg
		s.mu.Unlock()
		s.kick <- struct{}{}
	}
	apply(settingsOf(false, "ubuntu-latest"))
	time.Sleep(50 * time.Millisecond)
	f.wait(t, 0, 0)

	apply(settingsOf(true, "ubuntu-latest"))
	f.wait(t, 1, 0)
	// Same settings again change nothing
	apply(settingsOf(true, "ubuntu-latest"))
	time.Sleep(50 * time.Millisecond)
	f.wait(t, 1, 0)
	// A label change drains the old runner before the new one starts
	apply(settingsOf(true, "ubuntu-latest,big"))
	f.wait(t, 2, 1)
	if f.starts[1] != "ubuntu-latest,big" {
		t.Fatalf("second start labels = %q", f.starts[1])
	}
	cancel()
	f.wait(t, 2, 2)
}

func TestLabelsOfFallsBack(t *testing.T) {
	if got := labelsOf(&v1.BuiltinRunnerSettings{Labels: proto.String(" , ")}); len(got) != 1 || got[0] != "ubuntu-latest" {
		t.Fatalf("labels = %v", got)
	}
	if got := labelsOf(&v1.BuiltinRunnerSettings{Labels: proto.String("a, b ,,c")}); len(got) != 3 || got[2] != "c" {
		t.Fatalf("labels = %v", got)
	}
}
