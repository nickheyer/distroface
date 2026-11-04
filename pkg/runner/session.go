package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitea.com/gitea/runner/act/model"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Bytes of command output kept from each end for the server
const execOutputKeep = 32 << 10

// Bound on draining a killed command's output
const execDrainWait = 10 * time.Second

// Step that holds the job open while commands arrive
const sessionStepName = "Command workspace"

// Step script that hands env over and waits
const sessionScript = `d="$DISTROFACE_SESSION_DIR"
if env -0 >/dev/null 2>&1; then env -0 > "$d/env"; else env > "$d/env"; fi
echo ready > "$d/ready"
while [ ! -e "$d/done" ]; do sleep 1; done
`

// Signals every process carrying the marker, descendants before parents
const killTreeScript = `m="$1"; s="$2"
kt() { for d in /proc/[0-9]*; do pp=; while read -r k v _; do [ "$k" = "PPid:" ] && { pp=$v; break; }; done 2>/dev/null < "$d/status"; [ "$pp" = "$1" ] && kt "${d#/proc/}" "$2"; done; kill -"$2" "$1" 2>/dev/null; }
for d in /proc/[0-9]*; do p=${d#/proc/}; [ "$p" = "$$" ] && continue; tr '\0' '\n' < "$d/environ" 2>/dev/null | grep -qxF "$m" && kt "$p" "$s"; done
exit 0`

// Commands of an agent session, run inside the live job
type session struct {
	j *job
	// Host directory the session step and the runner share
	dir string
	// Captured step environment, PATH and GITHUB_WORKSPACE included
	env  map[string]string
	host bool
	cli  client.APIClient
	// Job container once found
	containerID string
	shell       string
	// Closed when serve returns
	done chan struct{}
}

func newSession(j *job) *session {
	return &session{j: j, dir: filepath.Join(j.dir, "session"), host: j.hostJob(), done: make(chan struct{})}
}

// Where the step sees the session directory
func (s *session) stepDir() string {
	if s.host {
		return s.dir
	}
	return containerJobDir + "/session"
}

// Appends the workspace step to the job before act plans it
func (s *session) prepare(workflow *model.Workflow, jobID string) error {
	if err := os.MkdirAll(s.dir, 0o777); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o777); err != nil {
		return err
	}
	job := workflow.GetJob(jobID)
	timeout := fmt.Sprint(s.j.timeoutMinutes())
	job.Steps = append(job.Steps, &model.Step{Name: sessionStepName, Run: sessionScript, Shell: "sh", TimeoutMinutes: timeout})
	return nil
}

// Serves commands once the workspace step is up
func (s *session) serve(ctx context.Context) {
	defer close(s.done)
	if err := s.waitReady(ctx); err != nil {
		if ctx.Err() == nil {
			s.j.log.Line("##[error]Command workspace never became ready: %v", err)
		}
		return
	}
	defer s.release()
	if !s.host {
		cli, err := dockerClient(ctx, s.j.r.cfg.DockerHost)
		if err != nil {
			s.j.log.Line("##[error]Command workspace: %v", err)
			return
		}
		defer cli.Close()
		s.cli = cli
		id, err := s.findContainer(ctx)
		if err != nil {
			s.j.log.Line("##[error]Command workspace: %v", err)
			return
		}
		s.containerID = id
	}
	s.shell = s.detectShell(ctx)
	s.j.log.Line("Command workspace ready, waiting for commands")
	for ctx.Err() == nil && !s.j.cancelled.Load() {
		if s.j.r.draining() {
			s.j.log.Line("Runner is stopping, closing the command workspace")
			return
		}
		fctx, cancel := context.WithTimeout(ctx, pollWait+pollGrace)
		go func() {
			select {
			case <-s.j.r.drain:
				cancel()
			case <-fctx.Done():
			}
		}()
		resp, err := s.j.r.client.FetchExec(fctx, s.j.task.JobId, pollWait)
		cancel()
		switch {
		case err != nil && s.j.r.draining():
			s.j.log.Line("Runner is stopping, closing the command workspace")
			return
		case err != nil && ctx.Err() != nil:
			return
		case err != nil && permanent(err):
			s.j.log.Line("##[warning]Command workspace closed by the server: %v", err)
			return
		case err != nil:
			s.j.r.out.Printf("job %s: fetch exec: %v", shortID(s.j.task.JobId), err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollBackoff):
			}
		case resp.GetCancelled():
			s.j.requestCancel(serverCancelMsg)
			return
		case resp.GetDone():
			s.j.log.Line("Command workspace closed")
			return
		case resp.GetExec() != nil:
			s.runExec(ctx, resp.GetExec())
		}
	}
}

// Lets the workspace step finish
func (s *session) release() {
	_ = os.WriteFile(filepath.Join(s.dir, "done"), []byte("done\n"), 0o666)
}

// Blocks until the step wrote its environment
func (s *session) waitReady(ctx context.Context) error {
	for {
		if _, err := os.Stat(filepath.Join(s.dir, "ready")); err == nil {
			raw, err := os.ReadFile(filepath.Join(s.dir, "env"))
			if err != nil {
				return err
			}
			s.env = parseEnvDump(raw)
			if s.env["GITHUB_WORKSPACE"] == "" {
				return errors.New("step environment has no GITHUB_WORKSPACE")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// env output, NUL separated when the image's env supports it
func parseEnvDump(raw []byte) map[string]string {
	out := map[string]string{}
	var entries []string
	if bytes.IndexByte(raw, 0) >= 0 {
		entries = strings.Split(string(raw), "\x00")
	} else {
		// Continuation lines belong to the value before them
		for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
			if envKeyLine(line) || len(entries) == 0 {
				entries = append(entries, line)
			} else {
				entries[len(entries)-1] += "\n" + line
			}
		}
	}
	for _, e := range entries {
		k, v, ok := strings.Cut(e, "=")
		if ok && k != "" {
			out[k] = v
		}
	}
	return out
}

func envKeyLine(line string) bool {
	k, _, ok := strings.Cut(line, "=")
	if !ok || k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Job container act started for this task
func (s *session) findContainer(ctx context.Context) (string, error) {
	prefix := s.j.containerPrefix()
	var last error
	for range 20 {
		id, err := findJobContainer(ctx, s.cli, prefix)
		if err == nil {
			return id, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return "", last
}

// bash when the workspace has it, sh otherwise
func (s *session) detectShell(ctx context.Context) string {
	var buf bytes.Buffer
	code, err := s.execute(ctx, "probe", `command -v bash`, &buf)
	if err == nil && code == 0 && strings.HasPrefix(strings.TrimSpace(buf.String()), "/") {
		return "bash"
	}
	return "sh"
}

// Runs one command as a step and reports it
func (s *session) runExec(ctx context.Context, ex *v1.ExecRequest) {
	name := fmt.Sprintf("Command %d", ex.GetNumber())
	s.j.rep.mu.Lock()
	row := s.j.rep.addRowLocked("exec:"+ex.GetId(), name)
	s.j.rep.markDirty(true)
	s.j.rep.mu.Unlock()
	started := time.Now()

	sctx, cancel := context.WithCancel(ctx)
	if m := ex.GetTimeoutMinutes(); m > 0 {
		sctx, cancel = context.WithTimeout(ctx, time.Duration(m)*time.Minute)
	}
	kept := &headTail{keep: execOutputKeep}
	code, err := s.execute(sctx, ex.GetId(), ex.GetScript(), io.MultiWriter(s.j.log, kept))
	interrupted := s.j.cancelled.Load() || ctx.Err() != nil
	timedOut := errors.Is(sctx.Err(), context.DeadlineExceeded) && !interrupted
	cancel()

	res := &v1.CompleteExecRequest{
		JobId: s.j.task.JobId, ExecId: ex.GetId(), ExitCode: int32(code), Output: s.j.log.Masked(kept.String()),
		Conclusion: v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS,
	}
	switch {
	case interrupted:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	case timedOut:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT
		s.j.log.Line("##[error]The command timed out after %d minutes", ex.GetTimeoutMinutes())
	case err != nil && code < 0:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
		res.Error = err.Error()
		s.j.log.Line("##[error]%v", err)
	case code != 0:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	}
	s.j.rep.mu.Lock()
	s.j.rep.closeRowLocked(row, res.Conclusion)
	s.j.rep.mu.Unlock()
	s.completeExec(ctx, res)
	s.j.r.out.Printf("job %s: command %d exit %d in %s", shortID(s.j.task.JobId), ex.GetNumber(), code, time.Since(started).Round(time.Millisecond))
}

// Shell command line for a script under the detected shell
func (s *session) shellArgs(script string) []string {
	if s.shell == "bash" {
		return []string{"bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", script}
	}
	return []string{"sh", "-e", "-c", script}
}

// Environment of the step with the command marker, as a list
func (s *session) execEnv(marker string) []string {
	out := make([]string, 0, len(s.env)+2)
	for k, v := range s.env {
		out = append(out, k+"="+v)
	}
	out = append(out, "GITHUB_ACTION=__run", "DISTROFACE_EXEC="+marker)
	return out
}

// Runs a script in the workspace, answering the exit code
func (s *session) execute(ctx context.Context, marker, script string, out io.Writer) (int, error) {
	if s.host {
		return s.executeHost(ctx, marker, script, out)
	}
	return s.executeDocker(ctx, marker, script, out)
}

func (s *session) executeHost(ctx context.Context, marker, script string, out io.Writer) (int, error) {
	args := s.shellArgs(script)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = s.env["GITHUB_WORKSPACE"]
	cmd.Env = s.execEnv(marker)
	cmd.Stdout, cmd.Stderr = out, out
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		return -1, ctx.Err()
	case err := <-done:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		if err != nil {
			return -1, err
		}
		return 0, nil
	}
}

func (s *session) executeDocker(ctx context.Context, marker, script string, out io.Writer) (int, error) {
	created, err := s.cli.ExecCreate(ctx, s.containerID, client.ExecCreateOptions{
		Cmd: s.shellArgs(script), Env: s.execEnv(marker), WorkingDir: s.env["GITHUB_WORKSPACE"],
		AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return -1, fmt.Errorf("start command: %w", err)
	}
	resp, err := s.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, fmt.Errorf("attach command: %w", err)
	}
	copied := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(out, out, resp.Reader)
		copied <- err
	}()
	select {
	case <-ctx.Done():
		s.killMarked(marker)
		// Closing unblocks the copy when the marker was missing
		resp.Close()
		select {
		case <-copied:
		case <-time.After(execDrainWait):
		}
		return -1, ctx.Err()
	case <-copied:
		resp.Close()
	}
	info, err := s.cli.ExecInspect(context.WithoutCancel(ctx), created.ID, client.ExecInspectOptions{})
	if err != nil {
		return -1, fmt.Errorf("inspect command: %w", err)
	}
	return info.ExitCode, nil
}

// Terminates then kills every process of a timed out or cancelled command
func (s *session) killMarked(marker string) {
	bg, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, sig := range []string{"TERM", "KILL"} {
		created, err := s.cli.ExecCreate(bg, s.containerID, client.ExecCreateOptions{Cmd: []string{"/bin/sh", "-c", killTreeScript, "sh", "DISTROFACE_EXEC=" + marker, sig}, User: "0"})
		if err != nil {
			return
		}
		if _, err := s.cli.ExecStart(bg, created.ID, client.ExecStartOptions{}); err != nil {
			return
		}
		if sig == "TERM" {
			time.Sleep(2 * time.Second)
		}
	}
}

// Reports a command's outcome, retrying transient failures
func (s *session) completeExec(ctx context.Context, res *v1.CompleteExecRequest) {
	for attempt := range 5 {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err := s.j.r.client.CompleteExec(cctx, res)
		cancel()
		if err == nil {
			return
		}
		s.j.r.out.Printf("job %s: complete exec: %v", shortID(s.j.task.JobId), err)
		if permanent(err) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
}

// Keeps both ends of a stream, counts the rest
type headTail struct {
	keep    int
	head    []byte
	tail    []byte
	dropped int
}

func (b *headTail) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.keep - len(b.head); room > 0 {
		take := min(room, len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	b.tail = append(b.tail, p...)
	if len(b.tail) > 2*b.keep {
		over := len(b.tail) - b.keep
		b.dropped += over
		b.tail = append(b.tail[:0], b.tail[over:]...)
	}
	return n, nil
}

// Both ends on line boundaries around an elision note
func (b *headTail) String() string {
	head, tail, dropped := b.head, b.tail, b.dropped
	if over := len(tail) - b.keep; over > 0 {
		dropped += over
		tail = tail[over:]
	}
	if dropped == 0 {
		return string(head) + string(tail)
	}
	if i := bytes.LastIndexByte(head, '\n'); i >= 0 {
		dropped += len(head) - i - 1
		head = head[:i+1]
	}
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		dropped += i + 1
		tail = tail[i+1:]
	}
	return fmt.Sprintf("%s\n[... %d bytes of output elided, rerun with a filter such as tail or grep to see the middle ...]\n%s", head, dropped, tail)
}
