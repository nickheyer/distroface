package runner

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

const (
	heartbeatEvery = 30 * time.Second
	pollWait       = 20 * time.Second
	pollBackoff    = 5 * time.Second
	// Slack past the long poll before a fetch is abandoned
	pollGrace = 15 * time.Second
)

// Labels naming the runner and job on docker objects
const (
	labelJob    = "dfcli.runner.job"
	labelRunner = "dfcli.runner.name"
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

	ext *externals
	// Buildkit state volumes, one per concurrency slot
	bkSlots chan string
	pullMu  sync.Mutex
	pulled  map[string]bool

	dnsMu sync.Mutex
	dns   *dnsConfig
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
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	r := &Runner{
		cfg: cfg, version: version, http: httpClient,
		client:  NewClient(cfg.Server, cfg.Token, httpClient),
		out:     log.New(os.Stderr, "", log.LstdFlags),
		running: map[string]*job{},
		ext:     newExternals(filepath.Join(cfg.JobRoot(), "_externals"), httpClient),
		bkSlots: make(chan string, cfg.Concurrency),
		pulled:  map[string]bool{},
	}
	for i := range cfg.Concurrency {
		r.bkSlots <- buildKitVolumeName(cfg.Name, i)
	}
	return r, nil
}

// Blocks until a buildkit state volume is free
func (r *Runner) acquireBuildKitVolume(ctx context.Context) (string, func(), error) {
	select {
	case name := <-r.bkSlots:
		return name, func() { r.bkSlots <- name }, nil
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
}

// Pulls an image once per runner process
func (r *Runner) pullOnce(ctx context.Context, cli *client.Client, ref string, out io.Writer) error {
	r.pullMu.Lock()
	defer r.pullMu.Unlock()
	if r.pulled[ref] {
		return nil
	}
	if err := pullImage(ctx, cli, ref, "", out); err != nil {
		return err
	}
	r.pulled[ref] = true
	return nil
}

// Nameservers builds resolve with, probed from the daemon once
func (r *Runner) buildDNS(ctx context.Context, cli *client.Client, image string, out io.Writer) *dnsConfig {
	r.dnsMu.Lock()
	defer r.dnsMu.Unlock()
	if r.dns != nil {
		return r.dns
	}
	if len(r.cfg.BuildKitDNS) > 0 {
		r.dns = &dnsConfig{nameservers: append([]string(nil), r.cfg.BuildKitDNS...)}
		return r.dns
	}
	cfg, err := probeDaemonDNS(ctx, cli, image)
	if err != nil {
		fmt.Fprintf(out, "##[warning]Resolver probe on the daemon's default network failed: %v\n", err)
		return nil
	}
	r.dns = cfg
	return cfg
}

// Labels shared by every container and network of a job
func (r *Runner) labels(jobID string) map[string]string {
	return map[string]string{labelJob: jobID, labelRunner: r.cfg.Name}
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

	if other := r.claimJobRoot(); other > 0 {
		r.out.Printf("runner %s is still running as pid %d, skipping leftover cleanup", r.cfg.Name, other)
	} else {
		r.cleanupLeftovers(ctx)
	}
	defer os.Remove(r.pidPath())

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
		fctx, cancelFetch := context.WithTimeout(jobCtx, pollWait+pollGrace)
		task, err := r.client.FetchTask(fctx, pollWait)
		cancelFetch()
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

// Job directory prefix of this runner inside the job root
func (r *Runner) jobDirPrefix() string {
	return "job-" + safeName(r.cfg.Name) + "-"
}

// Removes containers, networks, and directories a crashed run left behind
func (r *Runner) cleanupLeftovers(ctx context.Context) {
	root := r.cfg.JobRoot()
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), r.jobDirPrefix()) {
				r.out.Printf("removing leftover job directory %s", e.Name())
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	cli, err := newDockerClient(r.cfg.DockerHost)
	if err != nil {
		return
	}
	defer cli.Close()
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := cli.Ping(cctx); err != nil {
		return
	}
	args := filters.NewArgs(filters.Arg("label", labelRunner+"="+r.cfg.Name))
	if list, err := cli.ContainerList(cctx, container.ListOptions{All: true, Filters: args}); err == nil {
		for _, c := range list {
			r.out.Printf("removing leftover container %s", shortID(c.ID))
			_ = cli.ContainerRemove(cctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
		}
	}
	if list, err := cli.NetworkList(cctx, network.ListOptions{Filters: args}); err == nil {
		for _, n := range list {
			r.out.Printf("removing leftover network %s", n.Name)
			_ = cli.NetworkRemove(cctx, n.ID)
		}
	}
}

func (r *Runner) pidPath() string {
	return filepath.Join(r.cfg.JobRoot(), "runner-"+safeName(r.cfg.Name)+".pid")
}

// Records this process, answers a live earlier same named process
func (r *Runner) claimJobRoot() int {
	_ = os.MkdirAll(r.cfg.JobRoot(), 0o755)
	if raw, err := os.ReadFile(r.pidPath()); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid != os.Getpid() && processAlive(pid) {
			return pid
		}
	}
	_ = os.WriteFile(r.pidPath(), []byte(strconv.Itoa(os.Getpid())), 0o644)
	return 0
}

// Lowercase letters, digits, dashes, and dots only
func safeName(s string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, s)
	clean = strings.Trim(clean, "-.")
	if clean == "" {
		return "runner"
	}
	if len(clean) > 32 {
		clean = clean[:32]
	}
	return clean
}
