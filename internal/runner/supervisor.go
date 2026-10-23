package runner

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	runnerapp "github.com/nickheyer/distroface/pkg/runner"
	"google.golang.org/protobuf/proto"
)

// Fake host the in process transport routes on
const Host = "builtin.distroface.internal"

// Everything the embedded runner needs from the server
type SupervisorOptions struct {
	Resolver *settings.Resolver
	Registry *Registry
	// Whole server handler, runner rpc calls go straight in
	Handler http.Handler
	Version string
	// Job work tree parent, same path on the docker host
	WorkDir string
	Log     *logger.Logger
}

// Starts one runner and blocks until the context ends
type startFunc func(ctx context.Context, cfg *v1.BuiltinRunnerSettings) error

// Keeps one runner alive matching the system settings
type Supervisor struct {
	o     SupervisorOptions
	start startFunc

	mu      sync.Mutex
	desired *v1.BuiltinRunnerSettings
	kick    chan struct{}
}

func NewSupervisor(o SupervisorOptions) *Supervisor {
	s := &Supervisor{o: o, kick: make(chan struct{}, 1)}
	s.start = s.startRunner
	return s
}

// Follows the settings until the context ends
func (s *Supervisor) Start(ctx context.Context) {
	s.o.Resolver.Subscribe(func() { s.Reconcile(ctx) })
	go s.loop(ctx)
	s.Reconcile(ctx)
}

// Records the wanted settings and wakes the worker
func (s *Supervisor) Reconcile(ctx context.Context) {
	want := proto.Clone(s.o.Resolver.System(ctx).GetGit().GetActions().GetBuiltinRunner()).(*v1.BuiltinRunnerSettings)
	s.mu.Lock()
	s.desired = want
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Supervisor) wanted() *v1.BuiltinRunnerSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desired
}

// Applies changes in order, draining the old runner first
func (s *Supervisor) loop(ctx context.Context) {
	var current *v1.BuiltinRunnerSettings
	var cancel context.CancelFunc
	var done chan struct{}
	stop := func() {
		if cancel == nil {
			return
		}
		cancel()
		<-done
		cancel, done, current = nil, nil, nil
	}
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		}
		want := s.wanted()
		if want == nil || !want.GetEnabled() {
			if current != nil {
				s.o.Log.Info("Built in runner stopping, disabled in the settings")
				stop()
				if err := s.o.Registry.DeleteBuiltin(ctx); err != nil {
					s.o.Log.Error("retiring the built in runner: %v", err)
				}
			}
			continue
		}
		if current != nil && proto.Equal(current, want) {
			continue
		}
		if current != nil {
			s.o.Log.Info("Built in runner restarting with new settings")
			stop()
		}
		rctx, c := context.WithCancel(ctx)
		d := make(chan struct{})
		cancel, done, current = c, d, want
		go func() {
			defer close(d)
			if err := s.start(rctx, want); err != nil && rctx.Err() == nil {
				s.o.Log.Error("Built in runner stopped: %v", err)
			}
		}()
	}
}

// Labels the settings ask for, the stock label when none
func labelsOf(cfg *v1.BuiltinRunnerSettings) []string {
	var labels []string
	for _, l := range strings.Split(cfg.GetLabels(), ",") {
		if l = strings.TrimSpace(l); l != "" {
			labels = append(labels, l)
		}
	}
	if len(labels) == 0 {
		labels = []string{runnerapp.DefaultLabel}
	}
	return labels
}

// Registers then polls for jobs until the context ends
func (s *Supervisor) startRunner(ctx context.Context, cfg *v1.BuiltinRunnerSettings) error {
	labels := labelsOf(cfg)
	row, token, err := s.o.Registry.RegisterBuiltin(ctx, labels, s.o.Version)
	if err != nil {
		return err
	}
	image := strings.TrimSpace(cfg.GetImage())
	if image == "" {
		image = runnerapp.DefaultImage
	}
	images := map[string]string{}
	for _, l := range labels {
		images[l] = image
	}
	rc := &runnerapp.Config{
		Server: "http://" + Host, Token: token, RunnerID: row.ID, Name: row.Name, Scope: "system",
		Labels: labels, Images: images, Concurrency: max(int(cfg.GetConcurrency()), 1), WorkDir: s.o.WorkDir,
	}
	client := &http.Client{Transport: &Transport{Host: Host, Handler: s.o.Handler}}
	r, err := runnerapp.New(rc, s.o.Version, client)
	if err != nil {
		return err
	}
	r.DisableSignals()
	if err := r.DockerAvailable(ctx); err != nil {
		s.o.Log.Warn("Built in runner: docker is unreachable (%v), jobs will fail until the daemon answers", err)
	}
	s.o.Log.Info("Built in runner online with labels %s, work dir %s", strings.Join(labels, ","), rc.JobRoot())
	return r.Run(ctx)
}
