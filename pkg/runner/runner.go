package runner

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

const (
	heartbeatEvery = 30 * time.Second
	pollWait       = 20 * time.Second
	pollBackoff    = 5 * time.Second
)

// Polls for tasks and runs them up to concurrency
type Runner struct {
	cfg     *Config
	client  *Client
	version string
	http    *http.Client
	out     *log.Logger

	mu      sync.Mutex
	running map[string]*job
}

// The http client carries the instance trust settings
func New(cfg *Config, version string, httpClient *http.Client) (*Runner, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if version == "" {
		version = "dev"
	}
	return &Runner{
		cfg: cfg, version: version, http: httpClient,
		client:  NewClient(cfg.Server, cfg.Token, httpClient),
		out:     log.New(os.Stderr, "", log.LstdFlags),
		running: map[string]*job{},
	}, nil
}

// Polls until the context ends or a signal drains
func (r *Runner) Run(ctx context.Context) error {
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	jobCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			r.out.Printf("shutdown requested, finishing running jobs (signal again to cancel them)")
			stopPolling()
		case <-pollCtx.Done():
			return
		}
		select {
		case <-sigs:
			r.out.Printf("cancelling running jobs")
			cancelJobs()
		case <-jobCtx.Done():
		}
	}()

	hbCtx, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	go r.heartbeat(hbCtx)

	r.out.Printf("runner %s online at %s, labels %v, concurrency %d", r.cfg.Name, r.cfg.Server, r.cfg.Labels, r.cfg.Concurrency)
	slots := make(chan struct{}, r.cfg.Concurrency)
	var wg sync.WaitGroup
	for {
		select {
		case <-pollCtx.Done():
			wg.Wait()
			return nil
		case slots <- struct{}{}:
		}
		// Hard cancel only, so a claimed task is never dropped
		task, err := r.client.FetchTask(jobCtx, pollWait)
		if err != nil {
			<-slots
			if pollCtx.Err() != nil {
				wg.Wait()
				return nil
			}
			r.out.Printf("fetch task: %v", err)
			select {
			case <-pollCtx.Done():
			case <-time.After(pollBackoff):
			}
			continue
		}
		if task == nil {
			<-slots
			continue
		}
		wg.Add(1)
		go func(task *v1.Task) {
			defer wg.Done()
			defer func() { <-slots }()
			r.runTask(jobCtx, task)
		}(task)
	}
}

func (r *Runner) runTask(ctx context.Context, task *v1.Task) {
	j := newJob(r, task)
	r.mu.Lock()
	r.running[task.JobId] = j
	r.mu.Unlock()
	r.out.Printf("job %s: starting %s / %s", shortID(task.JobId), task.RepoFullName, task.JobName)
	started := time.Now()
	j.run(ctx)
	r.mu.Lock()
	delete(r.running, task.JobId)
	r.mu.Unlock()
	r.out.Printf("job %s: done in %s", shortID(task.JobId), time.Since(started).Round(time.Second))
}

func (r *Runner) runningIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.running))
	for id := range r.running {
		ids = append(ids, id)
	}
	return ids
}

func (r *Runner) heartbeat(ctx context.Context) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := r.client.Heartbeat(hctx, r.version, r.runningIDs()); err != nil && ctx.Err() == nil {
			r.out.Printf("heartbeat: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// One heartbeat for status checks
func (r *Runner) Heartbeat(ctx context.Context) error {
	return r.client.Heartbeat(ctx, r.version, nil)
}

// Whether the configured daemon answers
func (r *Runner) DockerAvailable(ctx context.Context) error {
	cli, err := newDockerClient(r.cfg.DockerHost)
	if err != nil {
		return err
	}
	defer cli.Close()
	_, err = cli.Ping(ctx)
	return err
}
