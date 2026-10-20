package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Evaluates the condition, runs the step, records the outcome
func (j *job) runStep(ctx context.Context, i int, st *v1.TaskStep) {
	if j.stopped() {
		j.finishStep(i, st, "skipped", "skipped", nil)
		return
	}
	ectx := j.exprContext()
	ok, err := evalCondition(st.Condition, ectx)
	if err != nil {
		j.fail("step %q: bad if expression: %v", st.Name, err)
		j.finishStep(i, st, "failure", "failure", nil)
		j.sync(ctx)
		return
	}
	if !ok {
		j.finishStep(i, st, "skipped", "skipped", nil)
		return
	}
	j.setStep(i, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
	j.sync(ctx)
	if j.stopped() {
		j.finishStep(i, st, "cancelled", "cancelled", nil)
		return
	}
	name := interpolate(st.Name, ectx)
	j.log.Line("##[group]%s", name)
	started := time.Now()
	sctx, cancel := j.stepContext(ctx, st.TimeoutMinutes)
	outputs, err := j.execStep(sctx, st, ectx)
	timedOut := errors.Is(sctx.Err(), context.DeadlineExceeded) && !j.stopped()
	cancel()
	j.log.Line("##[endgroup]")

	outcome := "success"
	switch {
	case j.stopped():
		outcome = "cancelled"
	case timedOut:
		outcome = "failure"
		j.log.Line("##[error]The step timed out after %d minutes", st.TimeoutMinutes)
		j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("%s timed out after %d minutes", name, st.TimeoutMinutes), Step: st.Number})
	case err != nil:
		outcome = "failure"
		j.log.Line("##[error]%v", err)
		j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("%s: %v", name, err), Step: st.Number})
	}
	conclusion := outcome
	if outcome == "failure" && st.ContinueOnError {
		conclusion = "success"
	}
	if conclusion == "failure" {
		j.mu.Lock()
		if j.status == "" {
			j.status = "failure"
		}
		j.mu.Unlock()
	}
	j.finishStep(i, st, outcome, conclusion, outputs)
	j.r.out.Printf("job %s: step %d %q %s in %s", shortID(j.task.JobId), st.Number, name, outcome, time.Since(started).Round(time.Millisecond))
	j.sync(ctx)
}

func (j *job) finishStep(i int, st *v1.TaskStep, outcome, conclusion string, outputs map[string]string) {
	j.setStep(i, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, protoConclusion(conclusion))
	if st.Id == "" {
		return
	}
	outs := map[string]any{}
	for k, v := range outputs {
		outs[k] = v
	}
	j.mu.Lock()
	j.stepsCtx[st.Id] = map[string]any{"outcome": outcome, "conclusion": conclusion, "outputs": outs}
	j.mu.Unlock()
}

func protoConclusion(s string) v1.ActionsConclusion {
	switch s {
	case "success":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS
	case "failure":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	case "cancelled":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	case "skipped":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED
	}
	return v1.ActionsConclusion_ACTIONS_CONCLUSION_NEUTRAL
}

// Runs one step and returns its outputs
func (j *job) execStep(ctx context.Context, st *v1.TaskStep, ectx *exprContext) (map[string]string, error) {
	files, err := j.newCommandFiles()
	if err != nil {
		return nil, err
	}
	defer files.cleanup()
	env := j.baseEnv()
	for k, v := range interpolateMap(st.Env, ectx) {
		env[k] = v
	}
	files.apply(j, env)
	outputs := map[string]string{}
	cw := j.newCommandWriter(st.Number, outputs)
	if st.Uses != "" {
		err = j.runUses(ctx, st, env, ectx, outputs, cw)
	} else {
		shell := st.Shell
		if shell == "" {
			shell = j.task.DefaultShell
		}
		code, e := j.runScript(ctx, interpolate(st.Run, ectx), shell, j.workDir(st.WorkingDirectory), env, cw)
		err = exitErr(code, e)
	}
	cw.Flush()
	j.applyCommandFiles(files, outputs)
	return outputs, firstErr(err, cw.failure())
}

func exitErr(code int, err error) error {
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("process completed with exit code %d", code)
	}
	return nil
}

func (j *job) newCommandWriter(step int32, outputs map[string]string) *commandWriter {
	return &commandWriter{
		out: j.log,
		onAnnotation: func(a Annotation) {
			a.Step = step
			j.annotate(a)
		},
		onMask:   j.log.AddMask,
		onOutput: func(k, v string) { outputs[k] = v },
	}
}

// Step working directory in the executor view
func (j *job) workDir(wd string) string {
	base := j.pathOf(j.workspace)
	wd = strings.TrimSpace(wd)
	if wd == "" {
		return base
	}
	if path.IsAbs(wd) || filepath.IsAbs(wd) {
		return wd
	}
	return path.Join(base, filepath.ToSlash(wd))
}

// Writes the script to temp, runs it with the shell
func (j *job) runScript(ctx context.Context, script, shell, dir string, env map[string]string, out io.Writer) (int, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		shell = "bash"
		if !j.bashAvailable(ctx) {
			shell = "sh"
		}
	}
	ext := ".sh"
	var cmd []string
	switch shell {
	case "bash":
		cmd = []string{"bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "{0}"}
	case "sh":
		cmd = []string{"sh", "-e", "{0}"}
	case "python":
		ext = ".py"
		py := "python3"
		if !j.exec.Has(ctx, py) {
			py = "python"
		}
		cmd = []string{py, "{0}"}
	case "pwsh", "powershell":
		ext = ".ps1"
		cmd = []string{shell, "-command", ". '{0}'"}
	case "node":
		ext = ".js"
		cmd = []string{"node", "{0}"}
	default:
		cmd = splitShellWords(shell)
		if !strings.Contains(shell, "{0}") {
			cmd = append(cmd, "{0}")
		}
	}
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	file, err := j.writeTemp("script-"+uuid.NewString()[:8]+ext, script)
	if err != nil {
		return -1, err
	}
	target := j.pathOf(file)
	for i := range cmd {
		cmd[i] = strings.ReplaceAll(cmd[i], "{0}", target)
	}
	return j.exec.Exec(ctx, execSpec{Cmd: j.withPath(cmd), Env: envSlice(env), Dir: dir, Out: out})
}

func (j *job) bashAvailable(ctx context.Context) bool {
	switch j.hasBash.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	ok := j.exec.Has(ctx, "bash")
	if ok {
		j.hasBash.Store(1)
	} else {
		j.hasBash.Store(2)
	}
	return ok
}

// GITHUB_PATH additions are prepended through a tiny sh wrapper
func (j *job) withPath(cmd []string) []string {
	j.mu.Lock()
	adds := append([]string(nil), j.path...)
	j.mu.Unlock()
	if len(adds) == 0 {
		return cmd
	}
	return append([]string{"/bin/sh", "-c", `export PATH="$0:$PATH"; exec "$@"`, strings.Join(adds, ":")}, cmd...)
}

func (j *job) writeTemp(name, content string) (string, error) {
	p := filepath.Join(j.tempDir, name)
	if err := os.WriteFile(p, []byte(content), 0o666); err != nil {
		return "", err
	}
	_ = os.Chmod(p, 0o666)
	return p, nil
}

// Per step GITHUB_OUTPUT style files
type commandFiles struct {
	output, env, path, summary, state string
}

func (j *job) newCommandFiles() (*commandFiles, error) {
	dir := filepath.Join(j.tempDir, "_runner_file_commands")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o777)
	id := uuid.NewString()[:8]
	f := &commandFiles{
		output: filepath.Join(dir, "output_"+id), env: filepath.Join(dir, "env_"+id), path: filepath.Join(dir, "path_"+id),
		summary: filepath.Join(dir, "summary_"+id), state: filepath.Join(dir, "state_"+id),
	}
	for _, p := range []string{f.output, f.env, f.path, f.summary, f.state} {
		if err := os.WriteFile(p, nil, 0o666); err != nil {
			return nil, err
		}
		_ = os.Chmod(p, 0o666)
	}
	return f, nil
}

func (f *commandFiles) apply(j *job, env map[string]string) {
	env["GITHUB_OUTPUT"] = j.pathOf(f.output)
	env["GITHUB_ENV"] = j.pathOf(f.env)
	env["GITHUB_PATH"] = j.pathOf(f.path)
	env["GITHUB_STEP_SUMMARY"] = j.pathOf(f.summary)
	env["GITHUB_STATE"] = j.pathOf(f.state)
}

func (f *commandFiles) cleanup() {
	for _, p := range []string{f.output, f.env, f.path, f.summary, f.state} {
		os.Remove(p)
	}
}

func (f *commandFiles) stateValues() map[string]string {
	out := map[string]string{}
	data, _ := os.ReadFile(f.state)
	for _, kv := range parseKeyValueFile(data) {
		out[kv[0]] = kv[1]
	}
	return out
}

// Folds outputs, env, path, and summary back into the job
func (j *job) applyCommandFiles(f *commandFiles, outputs map[string]string) {
	if data, err := os.ReadFile(f.output); err == nil {
		for _, kv := range parseKeyValueFile(data) {
			outputs[kv[0]] = kv[1]
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if data, err := os.ReadFile(f.env); err == nil {
		for _, kv := range parseKeyValueFile(data) {
			j.env[kv[0]] = kv[1]
		}
	}
	if data, err := os.ReadFile(f.path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				j.path = append([]string{line}, j.path...)
			}
		}
	}
	if data, err := os.ReadFile(f.summary); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		j.summary.Write(data)
		if data[len(data)-1] != '\n' {
			j.summary.WriteByte('\n')
		}
	}
}
