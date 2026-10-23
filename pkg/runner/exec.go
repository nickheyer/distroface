package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Bytes of command output kept from each end for the server
const execOutputKeep = 32 << 10

// Serves commands as steps until the server closes the job
func (j *job) serveExecs(ctx context.Context) {
	j.log.Line("Command workspace ready, waiting for commands")
	for !j.stopped() && !j.hardCancel.Load() {
		if j.r.draining() {
			j.log.Line("Runner is stopping, closing the command workspace")
			return
		}
		fctx, cancel := context.WithTimeout(ctx, pollWait+pollGrace)
		go func() {
			select {
			case <-j.r.drain:
				cancel()
			case <-fctx.Done():
			}
		}()
		resp, err := j.r.client.FetchExec(fctx, j.task.JobId, pollWait)
		cancel()
		switch {
		case err != nil && j.r.draining():
			j.log.Line("Runner is stopping, closing the command workspace")
			return
		case err != nil && ctx.Err() != nil:
			return
		case err != nil && permanent(err):
			j.log.Line("##[warning]Command workspace closed by the server: %v", err)
			return
		case err != nil:
			j.r.out.Printf("job %s: fetch exec: %v", shortID(j.task.JobId), err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollBackoff):
			}
		case resp.GetCancelled():
			if !j.cancelled.Swap(true) {
				j.log.Line("##[error]The job was cancelled")
			}
			return
		case resp.GetDone():
			j.log.Line("Command workspace closed")
			return
		case resp.GetExec() != nil:
			j.runExec(ctx, resp.GetExec())
		}
	}
}

// Runs one command as a step and reports it
func (j *job) runExec(ctx context.Context, ex *v1.ExecRequest) {
	name := fmt.Sprintf("Command %d", ex.GetNumber())
	row := j.addStep(name)
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
	j.sync(ctx)
	j.log.Line("##[group]%s", name)
	started := time.Now()

	sctx, cancel := j.stepContext(ctx, ex.GetTimeoutMinutes(), false)
	code, output, err := j.execScript(sctx, ex, j.rowNumber(row))
	interrupted := j.stopped()
	timedOut := errors.Is(sctx.Err(), context.DeadlineExceeded) && !interrupted
	cancel()

	res := &v1.CompleteExecRequest{
		JobId: j.task.JobId, ExecId: ex.GetId(), ExitCode: int32(code), Output: output,
		Conclusion: v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS,
	}
	switch {
	case interrupted:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	case timedOut:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT
		j.log.Line("##[error]The command timed out after %d minutes", ex.GetTimeoutMinutes())
		j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("%s timed out after %d minutes", name, ex.GetTimeoutMinutes()), Step: j.rowNumber(row)})
	case err != nil && code < 0:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
		res.Error = err.Error()
		j.log.Line("##[error]%v", err)
	case code != 0:
		res.Conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	}
	j.log.Line("##[endgroup]")
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, res.Conclusion)
	j.sync(ctx)
	j.completeExec(ctx, res)
	j.r.out.Printf("job %s: command %d exit %d in %s", shortID(j.task.JobId), ex.GetNumber(), code, time.Since(started).Round(time.Millisecond))
}

// Runs the script, keeping masked output for the server
func (j *job) execScript(ctx context.Context, ex *v1.ExecRequest, step int32) (int, string, error) {
	files, err := j.newCommandFiles()
	if err != nil {
		return -1, "", err
	}
	defer files.cleanup()
	env := j.baseEnv()
	env["GITHUB_ACTION"] = "__run"
	files.apply(j, env)
	shell := "bash"
	if !j.bashAvailable(ctx) {
		shell = "sh"
	}
	kept := &headTail{keep: execOutputKeep}
	outputs := map[string]string{}
	cw := j.newCommandWriter(io.MultiWriter(j.log, kept), step, outputs, map[string]string{})
	code, err := j.runScript(ctx, ex.GetScript(), shell, j.workDir(""), env, cw)
	cw.Flush()
	j.applyCommandFiles(files, outputs)
	return code, j.log.Masked(kept.String()), err
}

// Reports a command's outcome, retrying transient failures
func (j *job) completeExec(ctx context.Context, res *v1.CompleteExecRequest) {
	for attempt := range 5 {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err := j.r.client.CompleteExec(cctx, res)
		cancel()
		if err == nil {
			return
		}
		j.r.out.Printf("job %s: complete exec: %v", shortID(j.task.JobId), err)
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
