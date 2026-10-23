package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/client"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

const (
	cancelPollEvery   = 5 * time.Second
	defaultJobMinutes = 360
	// Minutes each cleanup step gets after cancel or timeout
	postStepMinutes  = 10
	setupStepName    = "Set up job"
	completeStepName = "Complete job"
)

// Where a job's commands run
type executor interface {
	Describe() string
	Path(hostPath string) string
	Start(ctx context.Context) error
	Exec(ctx context.Context, spec execSpec) (int, error)
	Has(ctx context.Context, bin string) bool
	// Absolute path of a binary, empty when missing
	Which(ctx context.Context, bin string) string
	Info() executorInfo
	Kill()
	Close(ctx context.Context) error
}

// One claimed task from claim to completion
type job struct {
	r    *Runner
	task *v1.Task
	log  *logStream

	dir        string
	workspace  string
	tempDir    string
	actionsDir string
	// Runner wide tool cache that outlives the job
	toolCache string
	// Job dir handle that refuses symlink escapes
	root    *os.Root
	exec    executor
	docker  *client.Client
	network string
	// Host entries that let containers reach a loopback bound server
	hosts    []string
	confined bool
	hasBash  atomic.Int32

	github   map[string]any
	inputs   map[string]any
	matrix   map[string]any
	needs    map[string]any
	jobInfo  map[string]any
	strategy map[string]any
	outputs  map[string]string

	nodeCmds    map[int]*nodeCmd
	dockerAuths map[string]dockerAuth
	// Pull only logins from container and service credentials
	imageAuths map[string]dockerAuth
	prepared   map[string]*actionDef
	// Step registered problem matchers, kept job wide
	matchers *matcherSet
	// GITHUB_ACTION of each task step, numbered up front
	stepActions []string
	// Task steps whose action declares a pre phase, in step order
	preSteps []preStep

	mu          sync.Mutex
	env         map[string]string
	path        []string
	steps       []*v1.JobStep
	stepsCtx    map[string]any
	stepState   map[string]map[string]string
	actionNames map[string]int
	// Queued post phases by state key, main refreshes what pre captured
	actionPosts map[string]*actionCall
	annotations []Annotation
	status      string
	summary     strings.Builder
	post        []postStep
	cancelled   atomic.Bool
	timedOut    atomic.Bool
	// Runner shutdown, nothing more runs in the environment
	hardCancel atomic.Bool
	cancelStep context.CancelFunc
	stepCount  int32
	// Fingerprint of the last step table the server accepted
	reportMu   sync.Mutex
	lastReport string
}

// Action cleanup, run in reverse order after the steps
type postStep struct {
	name string
	run  func(ctx context.Context, out *commandWriter) error
}

// Post step result when its post-if is false
var errSkipped = errors.New("skipped")

func newJob(r *Runner, task *v1.Task) *job {
	j := &job{
		r:           r,
		task:        task,
		env:         map[string]string{},
		stepsCtx:    map[string]any{},
		stepState:   map[string]map[string]string{},
		actionNames: map[string]int{},
		actionPosts: map[string]*actionCall{},
		outputs:     map[string]string{},
		nodeCmds:    map[int]*nodeCmd{},
		dockerAuths: map[string]dockerAuth{},
		imageAuths:  map[string]dockerAuth{},
		prepared:    map[string]*actionDef{},
		matchers:    newMatcherSet(),
	}
	var ctxJSON struct {
		GitHub   map[string]any    `json:"github"`
		Inputs   map[string]any    `json:"inputs"`
		Outputs  map[string]string `json:"outputs"`
		Job      map[string]any    `json:"job"`
		Strategy map[string]any    `json:"strategy"`
	}
	_ = json.Unmarshal([]byte(task.Context), &ctxJSON)
	j.github, j.inputs, j.jobInfo = orMap(ctxJSON.GitHub), orMap(ctxJSON.Inputs), orMap(ctxJSON.Job)
	// Older servers send no strategy, a single job is the hosted default
	j.strategy = ctxJSON.Strategy
	if len(j.strategy) == 0 {
		j.strategy = map[string]any{"fail-fast": true, "job-index": float64(0), "job-total": float64(1), "max-parallel": float64(1)}
	}
	if ctxJSON.Outputs != nil {
		j.outputs = ctxJSON.Outputs
	}
	if _, ok := j.github["token"]; !ok && task.Token != "" {
		j.github["token"] = task.Token
	}
	_ = json.Unmarshal([]byte(task.Matrix), &j.matrix)
	_ = json.Unmarshal([]byte(task.Needs), &j.needs)
	if host := registryHost(task.ServerUrl); host != "" {
		if task.Env == nil {
			task.Env = map[string]string{}
		}
		if _, ok := task.Env["DISTROFACE_REGISTRY"]; !ok {
			task.Env["DISTROFACE_REGISTRY"] = host
		}
	}
	j.addStep(setupStepName)
	for _, st := range task.Steps {
		j.addStep(st.Name)
		j.stepActions = append(j.stepActions, j.nextActionName(taskStepSpec(st)))
	}
	return j
}

// Row of a task step, pre rows sit between setup and the steps
func (j *job) taskRow(i int) int {
	return 1 + len(j.preSteps) + i
}

func (j *job) repoName() string {
	_, name, _ := strings.Cut(j.task.RepoFullName, "/")
	if name == "" {
		name = "repo"
	}
	return name
}

// Drives the whole job and always reports a conclusion
func (j *job) run(parent context.Context) {
	bg := context.Background()
	j.log = newLogStream(j.r.client, j.task.JobId)
	j.log.Start(bg)
	for _, v := range j.task.Secrets {
		j.log.AddMask(v)
	}
	if j.task.Token != "" {
		j.log.AddMask(j.task.Token)
	}

	minutes := int(j.task.TimeoutMinutes)
	if minutes <= 0 {
		minutes = defaultJobMinutes
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	timer := time.AfterFunc(time.Duration(minutes)*time.Minute, func() {
		j.timedOut.Store(true)
		j.log.Line("##[error]The job has exceeded the maximum execution time of %d minutes", minutes)
		j.cancelRunning()
	})
	stopWatch := make(chan struct{})
	go j.watchCancel(ctx, stopWatch)

	conclusion := j.execute(ctx)
	timer.Stop()
	close(stopWatch)

	j.log.Line("Job finished with conclusion %s", strings.ToLower(strings.TrimPrefix(conclusion.String(), "ACTIONS_CONCLUSION_")))
	if err := j.log.Close(bg); err != nil {
		j.r.out.Printf("job %s: log upload: %v", shortID(j.task.JobId), err)
	}
	j.complete(bg, conclusion)
}

// Stops the running step, the environment stays for cleanup
func (j *job) cancelRunning() {
	j.mu.Lock()
	cancel := j.cancelStep
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Polls the server for a cancel request between step updates
func (j *job) watchCancel(ctx context.Context, stop <-chan struct{}) {
	t := time.NewTicker(cancelPollEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			j.cancelled.Store(true)
			j.hardCancel.Store(true)
			j.cancelRunning()
			if ex := j.executor(); ex != nil {
				ex.Kill()
			}
			return
		case <-t.C:
			j.sync(ctx)
		}
	}
}

func (j *job) executor() executor {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.exec
}

// Reports progress and acts on a server side cancel
func (j *job) sync(ctx context.Context) {
	if j.report(ctx) && !j.cancelled.Swap(true) {
		j.log.Line("##[error]The job was cancelled")
		j.cancelRunning()
	}
}

// Sends the step table when it changed, answers the cancel flag
func (j *job) report(ctx context.Context) bool {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	steps, ann := j.snapshot()
	key := reportKey(steps, ann)
	j.reportMu.Lock()
	unchanged := key == j.lastReport
	j.reportMu.Unlock()
	if unchanged {
		steps, ann = nil, ""
	}
	cancelled, err := j.r.client.UpdateTask(rctx, j.task.JobId, steps, "", ann)
	if err != nil {
		j.r.out.Printf("job %s: update: %v", shortID(j.task.JobId), err)
		return false
	}
	if !unchanged {
		j.reportMu.Lock()
		j.lastReport = key
		j.reportMu.Unlock()
	}
	return cancelled
}

// Stable encoding of a snapshot, equal keys mean nothing to resend
func reportKey(steps []*v1.JobStep, ann string) string {
	var sb strings.Builder
	opts := proto.MarshalOptions{Deterministic: true}
	for _, s := range steps {
		b, _ := opts.Marshal(s)
		sb.WriteByte(byte(len(b) >> 8))
		sb.WriteByte(byte(len(b)))
		sb.Write(b)
	}
	sb.WriteByte(0)
	sb.WriteString(ann)
	return sb.String()
}

func (j *job) snapshot() ([]*v1.JobStep, string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	steps := make([]*v1.JobStep, 0, len(j.steps))
	for _, s := range j.steps {
		steps = append(steps, proto.Clone(s).(*v1.JobStep))
	}
	ann := make([]Annotation, len(j.annotations))
	copy(ann, j.annotations)
	raw, _ := json.Marshal(ann)
	return steps, string(raw)
}

func (j *job) annotate(a Annotation) {
	j.mu.Lock()
	j.annotations = append(j.annotations, a)
	j.mu.Unlock()
}

// Records a job level failure against a step row
func (j *job) fail(row int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	j.log.Line("##[error]%s", msg)
	j.annotate(Annotation{Level: "error", Message: msg, Step: j.rowNumber(row)})
	j.markFailed()
}

// Whether a step already failed the job
func (j *job) failed() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status == "failure"
}

func (j *job) markFailed() {
	j.mu.Lock()
	if j.status == "" {
		j.status = "failure"
	}
	j.mu.Unlock()
}

func (j *job) rowNumber(row int) int32 {
	j.mu.Lock()
	defer j.mu.Unlock()
	if row < 0 || row >= len(j.steps) {
		return 0
	}
	return j.steps[row].Number
}

func (j *job) complete(ctx context.Context, conclusion v1.ActionsConclusion) {
	steps, ann := j.snapshot()
	outputs := j.evalOutputs()
	for attempt := range 5 {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := j.r.client.CompleteTask(cctx, j.task.JobId, conclusion, steps, outputs, ann)
		cancel()
		if err == nil {
			return
		}
		j.r.out.Printf("job %s: complete: %v", shortID(j.task.JobId), err)
		if permanent(err) {
			return
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
}

// Job outputs are expressions over the final steps context
func (j *job) evalOutputs() string {
	ectx := j.exprContext()
	out := map[string]string{}
	for k, v := range j.outputs {
		s, err := interpolate(v, ectx)
		if err != nil {
			j.r.out.Printf("job %s: output %s: %v", shortID(j.task.JobId), k, err)
		}
		out[k] = s
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

// Prepares the environment, runs steps, picks the conclusion
func (j *job) execute(ctx context.Context) v1.ActionsConclusion {
	ready := j.setup(ctx)
	for _, p := range j.preSteps {
		if !ready {
			j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED)
			continue
		}
		j.runPreStep(ctx, p)
	}
	for i, st := range j.task.Steps {
		if !ready {
			j.finishStep(j.taskRow(i), st.Id, "skipped", "skipped", nil)
			continue
		}
		j.runStep(ctx, i, st)
	}
	// Session jobs stay open for commands after checkout
	if j.task.GetInteractive() && ready && !j.stopped() && !j.failed() {
		j.serveExecs(ctx)
	}
	j.runPost(ctx)
	j.teardown(ctx)
	return j.conclusion()
}

// Set up job, the environment, action downloads, and pre steps
func (j *job) setup(ctx context.Context) bool {
	const row = 0
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
	j.log.Line("##[group]%s", setupStepName)
	j.log.Line("Runner %s (dfcli %s) picked up job %s of %s", j.r.cfg.Name, j.r.version, j.task.JobName, j.task.RepoFullName)
	j.sync(ctx)
	sctx, cancel := j.stepContext(ctx, 0, false)
	err := j.setupEnvironment(sctx)
	if err == nil {
		err = j.prepareActions(sctx)
	}
	if err == nil {
		j.planPreSteps()
	}
	cancel()
	switch {
	case j.stopped():
		j.log.Line("##[endgroup]")
		j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED)
		return false
	case err != nil:
		j.fail(row, "%v", err)
		j.log.Line("##[endgroup]")
		j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)
		j.sync(ctx)
		return false
	}
	j.log.Line("##[endgroup]")
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	j.sync(ctx)
	return true
}

func (j *job) setupEnvironment(ctx context.Context) error {
	if err := j.prepare(); err != nil {
		return err
	}
	if err := j.exec.Start(ctx); err != nil {
		return fmt.Errorf("could not start the job environment: %w", err)
	}
	return nil
}

// Complete job, cleanup of the environment and the summary
func (j *job) teardown(ctx context.Context) {
	row := j.addStep(completeStepName)
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
	j.log.Line("##[group]%s", completeStepName)
	if j.exec != nil {
		j.log.Line("Cleaning up the job environment")
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_ = j.exec.Close(cctx)
		ccancel()
	}
	if j.root != nil {
		_ = j.root.Close()
		j.root = nil
	}
	if j.dir != "" {
		if err := os.RemoveAll(j.dir); err != nil {
			j.log.Line("##[warning]Job directory cleanup: %v", err)
		}
	}
	j.mu.Lock()
	summary := strings.TrimRight(j.summary.String(), "\n")
	j.mu.Unlock()
	if summary != "" {
		j.log.Line("##[group]Job summary")
		_, _ = j.log.Write([]byte(summary + "\n"))
		j.log.Line("##[endgroup]")
	}
	j.log.Line("##[endgroup]")
	j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	j.report(ctx)
}

func (j *job) conclusion() v1.ActionsConclusion {
	j.mu.Lock()
	status := j.status
	j.mu.Unlock()
	switch {
	case j.timedOut.Load():
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT
	case j.cancelled.Load():
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	case status == "failure":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	}
	return v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS
}

// Lays out the job directory and picks the executor
func (j *job) prepare() error {
	root := j.r.cfg.JobRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("job root %s: %w", root, err)
	}
	dir, err := os.MkdirTemp(root, j.r.jobDirPrefix()+shortID(j.task.JobId)+"-")
	if err != nil {
		return err
	}
	j.dir = dir
	_ = os.Chmod(dir, 0o777)
	if j.root, err = os.OpenRoot(dir); err != nil {
		return err
	}
	name := j.repoName()
	j.workspace = filepath.Join(dir, name, name)
	j.tempDir = filepath.Join(dir, "_temp")
	j.actionsDir = filepath.Join(dir, "_actions")
	for _, d := range []string{j.workspace, j.tempDir, j.actionsDir, j.homeDir(), j.workflowDir(), j.fileCommandsDir()} {
		if err := j.mkdirAll(d); err != nil {
			return err
		}
	}
	// Shared across jobs like a hosted runner's tool cache
	j.toolCache = filepath.Join(root, "_tool")
	if err := os.MkdirAll(j.toolCache, 0o777); err != nil {
		return fmt.Errorf("tool cache %s: %w", j.toolCache, err)
	}
	_ = os.Chmod(j.toolCache, 0o777)
	if ev, ok := j.github["event"]; ok {
		raw, _ := json.MarshalIndent(ev, "", "  ")
		if err := j.writeFile(j.eventPath(), raw, 0o666); err != nil {
			return err
		}
	}
	if err := j.seedDockerAuth(); err != nil {
		return fmt.Errorf("docker config: %w", err)
	}

	labels := j.runsOn()
	image, host, ok := j.r.cfg.ImageFor(labels)
	switch {
	case j.task.ContainerImage != "":
		image, host = j.task.ContainerImage, false
		j.addImageAuth(image, j.task.ContainerCredentials)
	case !ok:
		image = j.r.cfg.FallbackImage()
		if len(labels) > 0 {
			j.log.Line("##[warning]No image mapped for runs-on %s, using %s", strings.Join(labels, ","), image)
		}
	}
	for _, svc := range j.task.Services {
		j.addImageAuth(svc.Image, svc.Credentials)
	}
	if host {
		if len(j.task.Services) > 0 {
			return errHostServices
		}
		j.setExecutor(&hostExecutor{jobDir: dir, log: j.log})
		return nil
	}
	cli, err := newDockerClient(j.r.cfg.DockerHost)
	if err != nil {
		return err
	}
	j.docker = cli
	j.network = "dfcli-" + shortID(j.task.JobId)
	j.confined = true
	var unreachable []string
	j.hosts, unreachable = gatewayHosts(j.task.ServerUrl, j.task.CloneUrl)
	for _, h := range unreachable {
		j.log.Line("##[warning]Server address %s is loopback, job containers cannot reach it, register the runner with a hostname instead", h)
	}
	j.setExecutor(&dockerExecutor{
		cli: cli, jobID: j.task.JobId, jobDir: dir, image: image, options: j.task.ContainerOptions,
		services: j.task.Services, log: j.log, auth: j.pullAuth,
		externals:  j.r.ext.root,
		toolCache:  j.toolCache,
		privileged: j.r.cfg.PrivilegedJobs,
		hosts:      j.hosts,
		labels:     j.r.labels(j.task.JobId),
	})
	return nil
}

// Pull only, never written to the docker config
func (j *job) addImageAuth(image string, c *v1.TaskCredentials) {
	if c == nil || (c.Username == "" && c.Password == "") {
		return
	}
	j.log.AddMask(c.Password)
	j.mu.Lock()
	j.imageAuths[dockerAuthKey(tagHost(image))] = dockerAuth{Username: c.Username, Password: c.Password}
	j.mu.Unlock()
}

// GITHUB_PATH entries so far, newest first
func (j *job) pathAdds() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.path...)
}

func (j *job) setExecutor(ex executor) {
	j.mu.Lock()
	j.exec = ex
	j.mu.Unlock()
}

// Task labels first, workflow source for older servers
func (j *job) runsOn() []string {
	if len(j.task.GetRunsOn()) > 0 {
		return j.task.GetRunsOn()
	}
	var ctxJSON struct {
		Source string `json:"workflow_source"`
	}
	_ = json.Unmarshal([]byte(j.task.Context), &ctxJSON)
	key, _ := j.jobInfo["key"].(string)
	if ctxJSON.Source == "" || key == "" {
		return nil
	}
	var wf struct {
		Jobs map[string]struct {
			RunsOn any `yaml:"runs-on"`
		} `yaml:"jobs"`
	}
	if yaml.Unmarshal([]byte(ctxJSON.Source), &wf) != nil {
		return nil
	}
	def, ok := wf.Jobs[key]
	if !ok {
		return nil
	}
	ectx := j.exprContext()
	var labels []string
	add := func(v any) {
		if s, ok := v.(string); ok {
			if label, err := interpolate(s, ectx); err == nil {
				s = label
			}
			labels = append(labels, strings.TrimSpace(s))
		}
	}
	switch v := def.RunsOn.(type) {
	case string:
		add(v)
	case []any:
		for _, item := range v {
			add(item)
		}
	case map[string]any:
		if l, ok := v["labels"].([]any); ok {
			for _, item := range l {
				add(item)
			}
		} else {
			add(v["labels"])
		}
	}
	return labels
}

// Expression contexts as the next step sees them
func (j *job) exprContext() *exprContext {
	j.mu.Lock()
	defer j.mu.Unlock()
	env := make(map[string]string, len(j.task.Env)+len(j.env))
	for k, v := range j.task.Env {
		env[k] = v
	}
	for k, v := range j.env {
		env[k] = v
	}
	status := j.status
	if j.cancelled.Load() || j.timedOut.Load() {
		status = "cancelled"
	}
	stepsCopy := make(map[string]any, len(j.stepsCtx))
	for k, v := range j.stepsCtx {
		stepsCopy[k] = v
	}
	var info executorInfo
	if j.exec != nil {
		info = j.exec.Info()
	}
	services := map[string]any{}
	for name, svc := range info.services {
		ports := map[string]any{}
		for k, v := range svc.ports {
			ports[k] = v
		}
		services[name] = map[string]any{"id": svc.id, "network": info.network, "ports": ports}
	}
	jobCtx := map[string]any{"status": orStatus(status), "container": map[string]any{"id": info.containerID, "network": info.network}, "services": services}
	for k, v := range j.jobInfo {
		jobCtx[k] = v
	}
	ws := ""
	if j.exec != nil {
		ws = j.exec.Path(j.workspace)
	}
	var wsFS fs.FS
	if j.root != nil {
		if rel, err := j.rel(j.workspace); err == nil {
			wsFS, _ = fs.Sub(j.root.FS(), filepath.ToSlash(rel))
		}
	}
	return &exprContext{
		github: j.github, env: env, vars: j.task.Variables, secrets: j.task.Secrets, needs: j.needs, matrix: j.matrix,
		steps: stepsCopy, inputs: j.inputs, job: jobCtx, strategy: j.strategy, status: status, workspace: j.workspace, workspaceFS: wsFS,
		runner: map[string]any{"os": runnerOS(), "arch": runnerArch(), "name": j.r.cfg.Name, "temp": j.pathOf(j.tempDir), "tool_cache": j.pathOf(j.toolCache), "workspace": ws, "debug": runnerDebug(j.debugEnabled())},
	}
}

func orStatus(s string) string {
	if s == "" {
		return "success"
	}
	return s
}

// Maps a host path into the executor view
func (j *job) pathOf(hostPath string) string {
	if j.exec == nil {
		return hostPath
	}
	return j.exec.Path(hostPath)
}

// Temp subdirectories laid out the way the hosted runner does
func (j *job) homeDir() string         { return filepath.Join(j.tempDir, "_github_home") }
func (j *job) workflowDir() string     { return filepath.Join(j.tempDir, "_github_workflow") }
func (j *job) fileCommandsDir() string { return filepath.Join(j.tempDir, "_runner_file_commands") }
func (j *job) eventPath() string       { return filepath.Join(j.workflowDir(), "event.json") }

func runnerOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	}
	return "Linux"
}

func runnerArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "X64"
	case "arm64":
		return "ARM64"
	case "386":
		return "X86"
	case "arm":
		return "ARM"
	}
	return runtime.GOARCH
}

// Base environment every step and action receives
func (j *job) baseEnv() map[string]string {
	g := j.github
	str := func(k string) string { return stringify(g[k]) }
	ws := j.pathOf(j.workspace)
	env := map[string]string{
		"CI": "true", "GITHUB_ACTIONS": "true", "DISTROFACE_ACTIONS": "true",
		"GITHUB_WORKSPACE": ws, "GITHUB_REPOSITORY": j.task.RepoFullName, "GITHUB_REPOSITORY_OWNER": str("repository_owner"),
		"GITHUB_REPOSITORY_ID": str("repository_id"), "GITHUB_REPOSITORY_OWNER_ID": str("repository_owner_id"),
		"GITHUB_SHA": str("sha"), "GITHUB_REF": str("ref"), "GITHUB_REF_NAME": str("ref_name"), "GITHUB_REF_TYPE": str("ref_type"), "GITHUB_REF_PROTECTED": str("ref_protected"),
		"GITHUB_HEAD_REF": str("head_ref"), "GITHUB_BASE_REF": str("base_ref"), "GITHUB_ACTOR": str("actor"), "GITHUB_ACTOR_ID": str("actor_id"), "GITHUB_TRIGGERING_ACTOR": str("triggering_actor"),
		"GITHUB_EVENT_NAME": str("event_name"), "GITHUB_EVENT_PATH": j.pathOf(j.eventPath()),
		"GITHUB_RUN_ID": j.task.RunId, "GITHUB_RUN_NUMBER": str("run_number"), "GITHUB_RUN_ATTEMPT": str("run_attempt"),
		"GITHUB_JOB": stringify(j.jobInfo["key"]), "GITHUB_WORKFLOW": str("workflow"), "GITHUB_WORKFLOW_REF": str("workflow_ref"), "GITHUB_WORKFLOW_SHA": str("workflow_sha"),
		"GITHUB_SERVER_URL": j.task.ServerUrl, "GITHUB_API_URL": str("api_url"), "GITHUB_GRAPHQL_URL": str("graphql_url"),
		"GITHUB_ACTION": "", "GITHUB_ACTION_PATH": "", "GITHUB_ACTION_REPOSITORY": "", "GITHUB_ACTION_REF": "",
		"GITHUB_RETENTION_DAYS": "", "GITHUB_ACTIONS_RUNNER": "dfcli",
		"RUNNER_OS": runnerOS(), "RUNNER_ARCH": runnerArch(), "RUNNER_NAME": j.r.cfg.Name, "RUNNER_TEMP": j.pathOf(j.tempDir),
		"RUNNER_TOOL_CACHE": j.pathOf(j.toolCache), "RUNNER_WORKSPACE": j.pathOf(filepath.Dir(j.workspace)),
		"RUNNER_ENVIRONMENT":    "self-hosted",
		"DISTROFACE_SERVER_URL": j.task.ServerUrl, "DISTROFACE_REPOSITORY": j.task.RepoFullName,
	}
	if j.task.ServerUrl == "" {
		env["GITHUB_SERVER_URL"] = str("server_url")
		env["DISTROFACE_SERVER_URL"] = str("server_url")
	}
	if j.task.Token != "" {
		env["GITHUB_TOKEN"] = j.task.Token
		env["DISTROFACE_TOKEN"] = j.task.Token
	}
	if j.tempDir != "" {
		env["DOCKER_CONFIG"] = j.pathOf(j.dockerConfigDir())
	}
	maps.Copy(env, j.task.Env)
	j.mu.Lock()
	maps.Copy(env, j.env)
	j.mu.Unlock()
	return env
}

func (j *job) setStep(row int, status v1.ActionsStatus, conclusion v1.ActionsConclusion) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if row < 0 || row >= len(j.steps) {
		return
	}
	s := j.steps[row]
	s.Status = status
	now := timestamppb.Now()
	switch status {
	case v1.ActionsStatus_ACTIONS_STATUS_RUNNING:
		s.StartedAt = now
	case v1.ActionsStatus_ACTIONS_STATUS_COMPLETED:
		if s.StartedAt == nil {
			s.StartedAt = now
		}
		s.FinishedAt = now
		s.Conclusion = conclusion
	}
}

// Sets a row's display name after expression evaluation
func (j *job) nameStep(row int, name string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if row >= 0 && row < len(j.steps) && name != "" {
		j.steps[row].Name = name
	}
}

// Appends a step table row and returns its index
func (j *job) addStep(name string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.stepCount++
	j.steps = append(j.steps, &v1.JobStep{Number: j.stepCount, Name: name, Status: v1.ActionsStatus_ACTIONS_STATUS_QUEUED})
	return len(j.steps) - 1
}

// Inserts queued rows at an index and renumbers the table
func (j *job) insertRows(at int, names []string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	rows := make([]*v1.JobStep, 0, len(names))
	for _, name := range names {
		rows = append(rows, &v1.JobStep{Name: name, Status: v1.ActionsStatus_ACTIONS_STATUS_QUEUED})
	}
	j.steps = slices.Insert(j.steps, at, rows...)
	for i, s := range j.steps {
		s.Number = int32(i + 1)
	}
	j.stepCount = int32(len(j.steps))
}

// Cleanup runs after cancel and timeout, shutdown skips it
func (j *job) runPost(ctx context.Context) {
	j.mu.Lock()
	post := j.post
	j.post = nil
	j.mu.Unlock()
	for i := len(post) - 1; i >= 0; i-- {
		p := post[i]
		if j.hardCancel.Load() {
			return
		}
		row := j.addStep("Post " + p.name)
		j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
		j.log.Line("##[group]Post %s", p.name)
		cw := j.newCommandWriter(j.log, j.rowNumber(row), map[string]string{}, map[string]string{})
		sctx, cancel := j.stepContext(ctx, postStepMinutes, true)
		err := p.run(sctx, cw)
		cancel()
		cw.Flush()
		conclusion := v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS
		switch {
		case errors.Is(err, errSkipped):
			conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED
		case err != nil:
			conclusion = v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
			j.log.Line("##[error]Post %s: %v", p.name, err)
			j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("Post %s: %v", p.name, err), Step: j.rowNumber(row)})
			j.markFailed()
		}
		j.log.Line("##[endgroup]")
		j.setStep(row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, conclusion)
	}
	j.report(ctx)
}

// Step context the cancel watcher can reach
func (j *job) stepContext(ctx context.Context, minutes int32, wasStopped bool) (context.Context, context.CancelFunc) {
	var sctx context.Context
	var cancel context.CancelFunc
	if minutes > 0 {
		sctx, cancel = context.WithTimeout(ctx, time.Duration(minutes)*time.Minute)
	} else {
		sctx, cancel = context.WithCancel(ctx)
	}
	j.mu.Lock()
	j.cancelStep = cancel
	j.mu.Unlock()
	// A cancel that landed between steps must not be lost
	if !wasStopped && j.stopped() {
		cancel()
	}
	return sctx, func() {
		j.mu.Lock()
		j.cancelStep = nil
		j.mu.Unlock()
		cancel()
	}
}

// True once the job must stop scheduling steps
func (j *job) stopped() bool {
	return j.cancelled.Load() || j.timedOut.Load()
}

// State an action's pre, main, and post scripts share
func (j *job) stateFor(key string) map[string]string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return maps.Clone(j.stepState[key])
}

func (j *job) mergeState(key string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.stepState[key]
	if st == nil {
		st = map[string]string{}
		j.stepState[key] = st
	}
	maps.Copy(st, values)
}

func runnerDebug(on bool) string {
	if on {
		return "1"
	}
	return ""
}

// Path of a host location relative to the job dir
func (j *job) rel(hostPath string) (string, error) {
	rel, err := filepath.Rel(j.dir, hostPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the job directory", hostPath)
	}
	return rel, nil
}

func (j *job) fsRoot() (*os.Root, error) {
	if j.root == nil {
		return nil, fmt.Errorf("job directory is not open")
	}
	return j.root, nil
}

// Job dir file operations go through the root

func (j *job) writeFile(hostPath string, data []byte, perm os.FileMode) error {
	root, err := j.fsRoot()
	if err != nil {
		return err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return err
	}
	if err := root.WriteFile(rel, data, perm); err != nil {
		return err
	}
	return root.Chmod(rel, perm)
}

func (j *job) readFile(hostPath string) ([]byte, error) {
	root, err := j.fsRoot()
	if err != nil {
		return nil, err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return nil, err
	}
	return root.ReadFile(rel)
}

func (j *job) mkdirAll(hostPath string) error {
	root, err := j.fsRoot()
	if err != nil {
		return err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(rel, 0o777); err != nil {
		return err
	}
	return root.Chmod(rel, 0o777)
}

func (j *job) removeAll(hostPath string) error {
	root, err := j.fsRoot()
	if err != nil {
		return err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return err
	}
	return root.RemoveAll(rel)
}

func (j *job) remove(hostPath string) error {
	root, err := j.fsRoot()
	if err != nil {
		return err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return err
	}
	return root.Remove(rel)
}

func (j *job) openFile(hostPath string, flag int, perm os.FileMode) (*os.File, error) {
	root, err := j.fsRoot()
	if err != nil {
		return nil, err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return nil, err
	}
	return root.OpenFile(rel, flag, perm)
}

func (j *job) open(hostPath string) (*os.File, error) {
	return j.openFile(hostPath, os.O_RDONLY, 0)
}

func (j *job) stat(hostPath string) (os.FileInfo, error) {
	root, err := j.fsRoot()
	if err != nil {
		return nil, err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return nil, err
	}
	return root.Stat(rel)
}

func (j *job) lstat(hostPath string) (os.FileInfo, error) {
	root, err := j.fsRoot()
	if err != nil {
		return nil, err
	}
	rel, err := j.rel(hostPath)
	if err != nil {
		return nil, err
	}
	return root.Lstat(rel)
}

func (j *job) readDir(hostPath string) ([]os.DirEntry, error) {
	f, err := j.open(hostPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}
