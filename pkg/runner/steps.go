package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// One step as the runner executes it, from the workflow or a composite action
type stepSpec struct {
	ID               string
	Name             string
	Uses             string
	Run              string
	Shell            string
	WorkingDirectory string
	If               string
	Env              map[string]string
	With             map[string]string
	ContinueOnError  bool
	TimeoutMinutes   int32
	// Expression forms evaluated per step when set
	ContinueOnErrorExpr string
	TimeoutMinutesExpr  string
}

func taskStepSpec(st *v1.TaskStep) stepSpec {
	return stepSpec{
		ID: st.Id, Name: st.Name, Uses: st.Uses, Run: st.Run, Shell: st.Shell, WorkingDirectory: st.WorkingDirectory,
		If: st.Condition, Env: st.Env, With: st.With, ContinueOnError: st.ContinueOnError, TimeoutMinutes: st.TimeoutMinutes,
		ContinueOnErrorExpr: st.GetContinueOnErrorExpr(), TimeoutMinutesExpr: st.GetTimeoutMinutesExpr(),
	}
}

// Literal or evaluated continue-on-error
func (st stepSpec) continueOnError(ectx *exprContext) (bool, error) {
	if strings.TrimSpace(st.ContinueOnErrorExpr) == "" {
		return st.ContinueOnError, nil
	}
	v, err := evalExpr(st.ContinueOnErrorExpr, ectx)
	if err != nil {
		return false, fmt.Errorf("continue-on-error: %w", err)
	}
	return truthy(v), nil
}

// Literal or evaluated timeout-minutes, zero means the job limit
func (st stepSpec) timeoutMinutes(ectx *exprContext) (int32, error) {
	if strings.TrimSpace(st.TimeoutMinutesExpr) == "" {
		return st.TimeoutMinutes, nil
	}
	v, err := evalExpr(st.TimeoutMinutesExpr, ectx)
	if err != nil {
		return 0, fmt.Errorf("timeout-minutes: %w", err)
	}
	n := toNumber(v)
	if n != n || n < 0 {
		return 0, fmt.Errorf("timeout-minutes: %q is not a number", stringify(v))
	}
	return int32(n), nil
}

// Display name the hosted runner gives a step without one
func defaultStepName(st stepSpec) string {
	if st.Uses != "" {
		return "Run " + strings.TrimSpace(st.Uses)
	}
	line := strings.TrimSpace(st.Run)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return "Run " + line
}

// Where steps run, the job itself or one composite action
type stepScope struct {
	j *job
	// Step table row annotations and names attach to
	row int
	// Results of earlier steps of a composite as steps.<id>
	steps map[string]any
	// Status success() and failure() answer inside a composite
	status string
	// Action the scope belongs to, nil at the job level
	call *actionCall
	// Key nested actions keep their state under
	stateKey string
	out      io.Writer
	depth    int
}

func (j *job) jobScope(row int) *stepScope {
	return &stepScope{j: j, row: row, out: j.log}
}

// Contexts as the next step of the scope sees them
func (sc *stepScope) exprContext() *exprContext {
	ectx := sc.j.exprContext()
	if sc.call == nil {
		return ectx
	}
	ectx.steps = sc.steps
	ectx.status = sc.status
	ectx.inputs = sc.call.inputs
	ectx.github = sc.call.github
	return ectx
}

// Environment under the step env, the action env inside a composite
func (sc *stepScope) baseEnv() map[string]string {
	if sc.call == nil {
		return sc.j.baseEnv()
	}
	env := make(map[string]string, len(sc.call.env)+8)
	maps.Copy(env, sc.call.env)
	sc.j.mu.Lock()
	maps.Copy(env, sc.j.env)
	sc.j.mu.Unlock()
	return env
}

// Directory relative uses references resolve against
func (sc *stepScope) actionBase() string {
	if sc.call != nil && sc.call.act.root != "" {
		return sc.call.act.root
	}
	return sc.j.workspace
}

// Records a composite step result as steps.<id>
func (sc *stepScope) record(id, outcome, conclusion string, outputs map[string]string) {
	if id == "" {
		return
	}
	outs := map[string]any{}
	for k, v := range outputs {
		outs[k] = v
	}
	sc.steps[id] = map[string]any{"outcome": outcome, "conclusion": conclusion, "outputs": outs}
}

// One step resolved against its scope and ready to run
type stepRun struct {
	spec stepSpec
	ectx *exprContext
	// Display name after interpolation
	name string
	// GITHUB_ACTION
	action string
	// State key of the action the step runs
	key string
}

// Runs one step and returns its outputs
func (sc *stepScope) exec(ctx context.Context, run stepRun) (map[string]string, error) {
	j := sc.j
	st := run.spec
	files, err := j.newCommandFiles()
	if err != nil {
		return nil, err
	}
	defer files.cleanup()
	stepEnv, err := interpolateMap(st.Env, run.ectx)
	if err != nil {
		return nil, fmt.Errorf("env: %w", err)
	}
	env := sc.baseEnv()
	maps.Copy(env, stepEnv)
	// Step env is visible to with, run, and working-directory
	ectx := run.ectx.withEnv(stepEnv)
	env["GITHUB_ACTION"] = run.action
	files.apply(j, env)
	outputs := map[string]string{}
	state := map[string]string{}
	cw := j.newCommandWriter(sc.out, j.rowNumber(sc.row), outputs, state)
	if st.Uses != "" {
		err = sc.runUses(ctx, run, env, ectx, outputs, cw)
	} else {
		err = sc.runScriptStep(ctx, st, env, ectx, cw)
	}
	cw.Flush()
	j.applyCommandFiles(files, outputs)
	maps.Copy(state, files.stateValues())
	j.mergeState(run.key, state)
	return outputs, firstErr(err, cw.failure())
}

func (sc *stepScope) runScriptStep(ctx context.Context, st stepSpec, env map[string]string, ectx *exprContext, out io.Writer) error {
	j := sc.j
	shell := strings.TrimSpace(st.Shell)
	if shell == "" {
		if sc.call != nil {
			return errors.New("required property is missing: shell")
		}
		shell = j.task.DefaultShell
	}
	script, err := interpolate(st.Run, ectx)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	wd, err := interpolate(st.WorkingDirectory, ectx)
	if err != nil {
		return fmt.Errorf("working-directory: %w", err)
	}
	code, err := j.runScript(ctx, script, shell, j.workDir(wd), env, out)
	return exitErr(code, err)
}

// Native handlers first, anything else is a resolved action
func (sc *stepScope) runUses(ctx context.Context, run stepRun, env map[string]string, ectx *exprContext, outputs map[string]string, out io.Writer) error {
	j := sc.j
	ref := strings.TrimSpace(run.spec.Uses)
	with, err := interpolateMap(run.spec.With, ectx)
	if err != nil {
		return fmt.Errorf("with: %w", err)
	}
	name, _, _ := strings.Cut(ref, "@")
	if handled, err := j.runNative(ctx, name, with, env, outputs, out, run.name); handled {
		return err
	}
	act, err := j.resolveAction(ctx, ref, sc.actionBase(), out)
	if err != nil && sc.call != nil && localAction(ref) {
		act, err = j.resolveAction(ctx, ref, j.workspace, out)
	}
	if err != nil {
		return err
	}
	call, err := j.prepareCall(act, with, env, ectx, run.key, run.name, out)
	if err != nil {
		return err
	}
	return j.runAction(ctx, sc, call, outputs, out)
}

// Evaluates the condition, runs the step, records the outcome
func (j *job) runStep(ctx context.Context, i int, ts *v1.TaskStep) {
	st := taskStepSpec(ts)
	row := j.taskRow(i)
	if j.hardCancel.Load() {
		j.finishStep(row, st.ID, "skipped", "skipped", nil)
		return
	}
	// After cancel or timeout only always() and cancelled() steps run
	wasStopped := j.stopped()
	sc := j.jobScope(row)
	ectx := sc.exprContext()
	ok, err := evalCondition(st.If, ectx)
	if err != nil {
		j.log.Line("##[group]%s", ts.Name)
		j.fail(row, "step %q: bad if expression: %v", ts.Name, err)
		j.log.Line("##[endgroup]")
		j.finishStep(row, st.ID, "failure", "failure", nil)
		j.sync(ctx)
		return
	}
	if !ok {
		j.finishStep(row, st.ID, "skipped", "skipped", nil)
		return
	}
	name, err := interpolate(st.Name, ectx)
	if err != nil {
		j.log.Line("##[group]%s", ts.Name)
		j.fail(row, "step %q: name: %v", ts.Name, err)
		j.log.Line("##[endgroup]")
		j.finishStep(row, st.ID, "failure", "failure", nil)
		j.sync(ctx)
		return
	}
	if name == "" {
		name = defaultStepName(st)
	}
	j.nameStep(row, name)
	continueOnError, err := st.continueOnError(ectx)
	var limit int32
	if err == nil {
		limit, err = st.timeoutMinutes(ectx)
	}
	if err != nil {
		j.log.Line("##[group]%s", name)
		j.fail(row, "step %q: %v", name, err)
		j.log.Line("##[endgroup]")
		j.finishStep(row, st.ID, "failure", "failure", nil)
		j.sync(ctx)
		return
	}
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
	j.sync(ctx)
	if j.hardCancel.Load() || (!wasStopped && j.stopped()) {
		j.finishStep(row, st.ID, "cancelled", "cancelled", nil)
		return
	}
	j.log.Line("##[group]%s", name)
	started := time.Now()
	if wasStopped && (limit <= 0 || limit > postStepMinutes) {
		limit = postStepMinutes
	}
	sctx, cancel := j.stepContext(ctx, limit, wasStopped)
	outputs, err := sc.exec(sctx, stepRun{spec: st, ectx: ectx, name: name, action: j.stepActions[i], key: strconv.Itoa(i)})
	interrupted := !wasStopped && j.stopped()
	timedOut := errors.Is(sctx.Err(), context.DeadlineExceeded) && !interrupted
	cancel()

	number := j.rowNumber(row)
	outcome := "success"
	switch {
	case interrupted:
		outcome = "cancelled"
	case timedOut:
		outcome = "failure"
		j.log.Line("##[error]The action '%s' has timed out after %d minutes.", name, limit)
		j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("%s timed out after %d minutes", name, limit), Step: number})
	case err != nil:
		outcome = "failure"
		j.log.Line("##[error]%v", err)
		j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("%s: %v", name, err), Step: number})
	}
	j.log.Line("##[endgroup]")
	conclusion := outcome
	if outcome == "failure" && continueOnError {
		conclusion = "success"
	}
	if conclusion == "failure" {
		j.markFailed()
	}
	j.finishStep(row, st.ID, outcome, conclusion, outputs)
	j.r.out.Printf("job %s: step %d %q %s in %s", shortID(j.task.JobId), number, name, outcome, time.Since(started).Round(time.Millisecond))
	j.sync(ctx)
}

func (j *job) finishStep(row int, id, outcome, conclusion string, outputs map[string]string) {
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, protoConclusion(conclusion))
	if id == "" {
		return
	}
	outs := map[string]any{}
	for k, v := range outputs {
		outs[k] = v
	}
	j.mu.Lock()
	j.stepsCtx[id] = map[string]any{"outcome": outcome, "conclusion": conclusion, "outputs": outs}
	j.mu.Unlock()
}

// Outcome and conclusion of a finished step
func stepOutcome(err error, continueOnError bool) (outcome, conclusion string) {
	if err == nil {
		return "success", "success"
	}
	if continueOnError {
		return "failure", "success"
	}
	return "failure", "failure"
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

var actionNameReplacer = strings.NewReplacer("/", "_", ".", "_", ":", "_")

// GITHUB_ACTION base the way the hosted runner names steps
func actionEnvName(st stepSpec) string {
	if st.ID != "" {
		return st.ID
	}
	if st.Uses == "" {
		return "__run"
	}
	spec, _, _ := strings.Cut(st.Uses, "@")
	return "__" + actionNameReplacer.Replace(strings.Trim(spec, "./"))
}

// Numbers repeated names, __run then __run_2
func (j *job) nextActionName(st stepSpec) string {
	base := actionEnvName(st)
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.actionNames[base] + 1
	j.actionNames[base] = n
	if n == 1 {
		return base
	}
	return base + "_" + strconv.Itoa(n)
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

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (j *job) newCommandWriter(out io.Writer, step int32, outputs, state map[string]string) *commandWriter {
	return &commandWriter{
		out: out,
		onAnnotation: func(a Annotation) {
			a.Step = step
			j.annotate(a)
		},
		onMask:   j.log.AddMask,
		onOutput: func(k, v string) { outputs[k] = v },
		onState:  func(k, v string) { state[k] = v },
		matchers: j.matchers,
		readFile: j.readExecFile,
		fileBase: j.pathOf(j.workspace),
		debug:    j.debugEnabled(),
		echo:     j.debugEnabled(),
	}
}

// Reads a file a step named in the executor view
func (j *job) readExecFile(p string) ([]byte, error) {
	host, err := j.hostPattern(p)
	if err != nil {
		return nil, err
	}
	if _, err := j.rel(host); err == nil && j.root != nil {
		return j.readFile(host)
	}
	if j.confined {
		return nil, fmt.Errorf("path %q is outside the job directory", p)
	}
	return os.ReadFile(host)
}

// ACTIONS_STEP_DEBUG as a secret or variable turns debug output on
func (j *job) debugEnabled() bool {
	return strings.EqualFold(j.task.Secrets["ACTIONS_STEP_DEBUG"], "true") || strings.EqualFold(j.task.Variables["ACTIONS_STEP_DEBUG"], "true")
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

// Shell command line for a script, {0} is the file
func shellCommand(shell string, hasBash bool, hasPython3 bool) (cmd []string, ext string) {
	ext = ".sh"
	switch strings.TrimSpace(shell) {
	case "":
		// Unnamed shells fail fast without pipefail, as on GitHub
		if hasBash {
			return []string{"bash", "-e", "{0}"}, ext
		}
		return []string{"sh", "-e", "{0}"}, ext
	case "bash":
		return []string{"bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "{0}"}, ext
	case "sh":
		return []string{"sh", "-e", "{0}"}, ext
	case "python":
		py := "python3"
		if !hasPython3 {
			py = "python"
		}
		return []string{py, "{0}"}, ".py"
	case "pwsh":
		return []string{"pwsh", "-command", ". '{0}'"}, ".ps1"
	case "powershell":
		return []string{"powershell", "-command", ". '{0}'"}, ".ps1"
	case "node":
		return []string{"node", "{0}"}, ".js"
	case "cmd":
		return []string{"cmd", "/D", "/E:ON", "/V:OFF", "/S", "/C", `CALL "{0}"`}, ".cmd"
	}
	cmd = splitShellWords(shell)
	if !strings.Contains(shell, "{0}") {
		cmd = append(cmd, "{0}")
	}
	return cmd, ext
}

// Writes the script to temp, runs it with the shell
func (j *job) runScript(ctx context.Context, script, shell, dir string, env map[string]string, out io.Writer) (int, error) {
	shell = strings.TrimSpace(shell)
	hasBash := false
	if shell == "" {
		hasBash = j.bashAvailable(ctx)
	}
	hasPy3 := true
	if shell == "python" {
		hasPy3 = j.exec.Has(ctx, "python3")
	}
	cmd, ext := shellCommand(shell, hasBash, hasPy3)
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	file, err := j.writeTemp("script-"+uuid.NewString()[:8]+ext, script)
	if err != nil {
		return -1, err
	}
	defer func() { _ = j.remove(file) }()
	target := j.pathOf(file)
	for i := range cmd {
		cmd[i] = strings.ReplaceAll(cmd[i], "{0}", target)
	}
	return j.exec.Exec(ctx, execSpec{Cmd: cmd, Env: envSlice(env), Dir: dir, Out: out, Path: j.pathAdds()})
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

func (j *job) writeTemp(name, content string) (string, error) {
	p := filepath.Join(j.tempDir, name)
	if err := j.writeFile(p, []byte(content), 0o666); err != nil {
		return "", err
	}
	return p, nil
}

// Per step GITHUB_OUTPUT style files
type commandFiles struct {
	j                                 *job
	output, env, path, summary, state string
}

func (j *job) newCommandFiles() (*commandFiles, error) {
	dir := j.fileCommandsDir()
	if err := j.mkdirAll(dir); err != nil {
		return nil, err
	}
	id := uuid.NewString()[:8]
	f := &commandFiles{
		j:      j,
		output: filepath.Join(dir, "set_output_"+id), env: filepath.Join(dir, "set_env_"+id), path: filepath.Join(dir, "add_path_"+id),
		summary: filepath.Join(dir, "step_summary_"+id), state: filepath.Join(dir, "save_state_"+id),
	}
	for _, p := range []string{f.output, f.env, f.path, f.summary, f.state} {
		if err := j.writeFile(p, nil, 0o666); err != nil {
			return nil, err
		}
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
		_ = f.j.remove(p)
	}
}

func (f *commandFiles) stateValues() map[string]string {
	out := map[string]string{}
	data, _ := f.j.readFile(f.state)
	for _, kv := range parseKeyValueFile(data) {
		out[kv[0]] = kv[1]
	}
	return out
}

// Folds outputs, env, path, and summary back into the job
func (j *job) applyCommandFiles(f *commandFiles, outputs map[string]string) {
	if data, err := j.readFile(f.output); err == nil {
		for _, kv := range parseKeyValueFile(data) {
			outputs[kv[0]] = kv[1]
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if data, err := j.readFile(f.env); err == nil {
		for _, kv := range parseKeyValueFile(data) {
			j.env[kv[0]] = kv[1]
		}
	}
	if data, err := j.readFile(f.path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				j.path = append([]string{line}, j.path...)
			}
		}
	}
	if data, err := j.readFile(f.summary); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		j.summary.Write(data)
		if data[len(data)-1] != '\n' {
			j.summary.WriteByte('\n')
		}
	}
}
