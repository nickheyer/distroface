package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Runs steps straight on the runner machine
type hostExecutor struct {
	jobDir string
	log    *logStream

	mu    sync.Mutex
	procs map[*exec.Cmd]struct{}
}

func (h *hostExecutor) Describe() string {
	return "host " + runtime.GOOS + "/" + runtime.GOARCH
}

func (h *hostExecutor) Path(hostPath string) string {
	return hostPath
}

func (h *hostExecutor) Start(ctx context.Context) error {
	h.procs = map[*exec.Cmd]struct{}{}
	h.log.Line("Running on the host executor")
	return nil
}

func (h *hostExecutor) Exec(ctx context.Context, spec execSpec) (int, error) {
	if len(spec.Cmd) == 0 {
		return -1, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, spec.Cmd[0], spec.Cmd[1:]...)
	cmd.Env = mergeOSEnv(spec.Env)
	cmd.Dir = spec.Dir
	cmd.Stdout = spec.Out
	cmd.Stderr = spec.Out
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	h.mu.Lock()
	h.procs[cmd] = struct{}{}
	h.mu.Unlock()
	err := cmd.Wait()
	h.mu.Lock()
	delete(h.procs, cmd)
	h.mu.Unlock()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (h *hostExecutor) Has(ctx context.Context, bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func (h *hostExecutor) Kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for cmd := range h.procs {
		killProcessGroup(cmd)
	}
}

func (h *hostExecutor) Close(ctx context.Context) error {
	h.Kill()
	return nil
}

// Task env wins over the inherited process environment
func mergeOSEnv(extra []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(extra)+len(os.Environ()))
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		seen[k] = true
		out = append(out, kv)
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !seen[k] {
			out = append(out, kv)
		}
	}
	return out
}

var errHostServices = fmt.Errorf("services need the docker executor")
