package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
)

// Where a job's commands run
type executor interface {
	Describe() string
	Path(hostPath string) string
	Start(ctx context.Context) error
	Exec(ctx context.Context, spec execSpec) (int, error)
	Has(ctx context.Context, bin string) bool
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
	exec       executor
	docker     *client.Client
	network    string
	confined   bool
	hasBash    atomic.Int32

	github  map[string]any
	inputs  map[string]any
	matrix  map[string]any
	needs   map[string]any
	jobInfo map[string]any
	outputs map[string]string

	mu          sync.Mutex
	env         map[string]string
	path        []string
	steps       []*v1.JobStep
	stepsCtx    map[string]any
	annotations []Annotation
	status      string
	summary     strings.Builder
	post        []postStep
	cancelled   atomic.Bool
	timedOut    atomic.Bool
	cancelStep  context.CancelFunc
	stepCount   int32
}

type postStep struct {
	name string
	run  func(ctx context.Context) error
}

func newJob(r *Runner, task *v1.Task) *job {
	j := &job{r: r, task: task, env: map[string]string{}, stepsCtx: map[string]any{}, outputs: map[string]string{}}
	var ctxJSON struct {
		GitHub  map[string]any    `json:"github"`
		Inputs  map[string]any    `json:"inputs"`
		Outputs map[string]string `json:"outputs"`
		Job     map[string]any    `json:"job"`
	}
	_ = json.Unmarshal([]byte(task.Context), &ctxJSON)
	j.github, j.inputs, j.jobInfo = orMap(ctxJSON.GitHub), orMap(ctxJSON.Inputs), orMap(ctxJSON.Job)
	if ctxJSON.Outputs != nil {
		j.outputs = ctxJSON.Outputs
	}
	_ = json.Unmarshal([]byte(task.Matrix), &j.matrix)
	_ = json.Unmarshal([]byte(task.Needs), &j.needs)
	for i, st := range task.Steps {
		j.steps = append(j.steps, &v1.JobStep{Number: int32(i + 1), Name: st.Name, Status: v1.ActionsStatus_ACTIONS_STATUS_QUEUED})
	}
	j.stepCount = int32(len(task.Steps))
	return j
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
		j.cancelRunning()
	})
	defer timer.Stop()
	stopWatch := make(chan struct{})
	go j.watchCancel(ctx, stopWatch)

	j.log.Line("Runner %s (dfcli %s) picked up job %s of %s", j.r.cfg.Name, j.r.version, j.task.JobName, j.task.RepoFullName)
	conclusion := j.execute(ctx)
	close(stopWatch)

	if j.exec != nil {
		cctx, ccancel := context.WithTimeout(bg, 2*time.Minute)
		_ = j.exec.Close(cctx)
		ccancel()
	}
	if j.dir != "" {
		os.RemoveAll(j.dir)
	}
	j.log.Line("Job finished with conclusion %s", strings.ToLower(strings.TrimPrefix(conclusion.String(), "ACTIONS_CONCLUSION_")))
	if err := j.log.Close(bg); err != nil {
		j.r.out.Printf("job %s: log upload: %v", shortID(j.task.JobId), err)
	}
	j.complete(bg, conclusion)
}

// Stops the running step and kills the job environment
func (j *job) cancelRunning() {
	j.mu.Lock()
	cancel := j.cancelStep
	ex := j.exec
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ex != nil {
		ex.Kill()
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
			j.cancelRunning()
			return
		case <-t.C:
			j.sync(ctx)
		}
	}
}

// Reports progress and acts on a server side cancel
func (j *job) sync(ctx context.Context) {
	if j.cancelled.Load() {
		return
	}
	if j.report(ctx) && !j.cancelled.Swap(true) {
		j.log.Line("##[error]The job was cancelled")
		j.cancelRunning()
	}
}

// Sends the step table, answers the cancel flag
func (j *job) report(ctx context.Context) bool {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	steps, ann := j.snapshot()
	cancelled, err := j.r.client.UpdateTask(rctx, j.task.JobId, steps, "", ann)
	if err != nil {
		j.r.out.Printf("job %s: update: %v", shortID(j.task.JobId), err)
		return false
	}
	return cancelled
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

func (j *job) fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	j.log.Line("##[error]%s", msg)
	j.annotate(Annotation{Level: "error", Message: msg})
	j.mu.Lock()
	if j.status == "" {
		j.status = "failure"
	}
	j.mu.Unlock()
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
		out[k] = interpolate(v, ectx)
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

// Prepares the environment, runs steps, picks the conclusion
func (j *job) execute(ctx context.Context) v1.ActionsConclusion {
	if err := j.prepare(); err != nil {
		j.fail("%v", err)
		return j.conclusion()
	}
	if err := j.exec.Start(ctx); err != nil {
		j.fail("could not start the job environment: %v", err)
		return j.conclusion()
	}
	for i, st := range j.task.Steps {
		j.runStep(ctx, i, st)
	}
	j.runPost(ctx)
	j.mu.Lock()
	hasSummary := j.summary.Len() > 0
	j.mu.Unlock()
	if hasSummary {
		j.log.Line("##[group]Job summary")
		_, _ = j.log.Write([]byte(strings.TrimRight(j.summary.String(), "\n") + "\n"))
		j.log.Line("##[endgroup]")
	}
	return j.conclusion()
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
	dir, err := os.MkdirTemp(root, "job-"+shortID(j.task.JobId)+"-")
	if err != nil {
		return err
	}
	j.dir = dir
	_ = os.Chmod(dir, 0o777)
	name := j.repoName()
	j.workspace = filepath.Join(dir, name, name)
	j.tempDir = filepath.Join(dir, "_temp")
	j.actionsDir = filepath.Join(dir, "_actions")
	for _, d := range []string{j.workspace, j.tempDir, j.actionsDir, filepath.Join(dir, "_tool")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			return err
		}
		_ = os.Chmod(d, 0o777)
	}
	if ev, ok := j.github["event"]; ok {
		raw, _ := json.MarshalIndent(ev, "", "  ")
		if err := os.WriteFile(filepath.Join(j.tempDir, "event.json"), raw, 0o666); err != nil {
			return err
		}
	}

	labels := j.runsOn()
	image, host, ok := j.r.cfg.ImageFor(labels)
	switch {
	case j.task.ContainerImage != "":
		image, host = j.task.ContainerImage, false
	case !ok:
		image = j.r.cfg.FallbackImage()
		if len(labels) > 0 {
			j.log.Line("##[warning]No image mapped for runs-on %s, using %s", strings.Join(labels, ","), image)
		}
	}
	if host {
		if len(j.task.Services) > 0 {
			return errHostServices
		}
		j.exec = &hostExecutor{jobDir: dir, log: j.log}
		return nil
	}
	cli, err := newDockerClient(j.r.cfg.DockerHost)
	if err != nil {
		return err
	}
	j.docker = cli
	j.network = "dfcli-" + shortID(j.task.JobId)
	j.confined = true
	j.exec = &dockerExecutor{
		cli: cli, jobID: j.task.JobId, jobDir: dir, image: image, options: j.task.ContainerOptions,
		services: j.task.Services, log: j.log, auth: registryAuth{host: registryHost(j.task.ServerUrl), token: j.task.Token},
		privileged: j.r.cfg.PrivilegedJobs,
	}
	return nil
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
			labels = append(labels, strings.TrimSpace(interpolate(s, ectx)))
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
	if j.cancelled.Load() {
		status = "cancelled"
	}
	stepsCopy := make(map[string]any, len(j.stepsCtx))
	for k, v := range j.stepsCtx {
		stepsCopy[k] = v
	}
	jobCtx := map[string]any{"status": orStatus(status), "container": map[string]any{"id": "", "network": j.network}, "services": map[string]any{}}
	for k, v := range j.jobInfo {
		jobCtx[k] = v
	}
	ws := ""
	if j.exec != nil {
		ws = j.exec.Path(j.workspace)
	}
	return &exprContext{
		github: j.github, env: env, vars: j.task.Variables, secrets: j.task.Secrets, needs: j.needs, matrix: j.matrix,
		steps: stepsCopy, inputs: j.inputs, job: jobCtx, status: status, workspace: j.workspace,
		runner: map[string]any{"os": runnerOS(), "arch": runnerArch(), "name": j.r.cfg.Name, "temp": j.pathOf(j.tempDir), "tool_cache": j.pathOf(filepath.Join(j.dir, "_tool")), "workspace": ws},
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
		"GITHUB_SHA": str("sha"), "GITHUB_REF": str("ref"), "GITHUB_REF_NAME": str("ref_name"), "GITHUB_REF_TYPE": str("ref_type"),
		"GITHUB_HEAD_REF": str("head_ref"), "GITHUB_BASE_REF": str("base_ref"), "GITHUB_ACTOR": str("actor"), "GITHUB_TRIGGERING_ACTOR": str("triggering_actor"),
		"GITHUB_EVENT_NAME": str("event_name"), "GITHUB_EVENT_PATH": j.pathOf(filepath.Join(j.tempDir, "event.json")),
		"GITHUB_RUN_ID": j.task.RunId, "GITHUB_RUN_NUMBER": str("run_number"), "GITHUB_RUN_ATTEMPT": str("run_attempt"),
		"GITHUB_JOB": stringify(j.jobInfo["key"]), "GITHUB_WORKFLOW": str("workflow"), "GITHUB_WORKFLOW_REF": str("workflow_ref"),
		"GITHUB_SERVER_URL": j.task.ServerUrl, "GITHUB_API_URL": str("api_url"), "GITHUB_GRAPHQL_URL": "",
		"GITHUB_ACTION": "", "GITHUB_ACTION_PATH": "", "GITHUB_ACTION_REPOSITORY": "",
		"RUNNER_OS": runnerOS(), "RUNNER_ARCH": runnerArch(), "RUNNER_NAME": j.r.cfg.Name, "RUNNER_TEMP": j.pathOf(j.tempDir),
		"RUNNER_TOOL_CACHE": j.pathOf(filepath.Join(j.dir, "_tool")), "RUNNER_WORKSPACE": j.pathOf(filepath.Dir(j.workspace)),
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
	for k, v := range j.task.Env {
		env[k] = v
	}
	j.mu.Lock()
	for k, v := range j.env {
		env[k] = v
	}
	j.mu.Unlock()
	return env
}

func (j *job) setStep(i int, status v1.ActionsStatus, conclusion v1.ActionsConclusion) {
	j.mu.Lock()
	s := j.steps[i]
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
	j.mu.Unlock()
}

// Appends a synthetic step row for post actions
func (j *job) addStep(name string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.stepCount++
	j.steps = append(j.steps, &v1.JobStep{Number: j.stepCount, Name: name, Status: v1.ActionsStatus_ACTIONS_STATUS_QUEUED})
	return len(j.steps) - 1
}

func (j *job) runPost(ctx context.Context) {
	j.mu.Lock()
	post := j.post
	j.post = nil
	j.mu.Unlock()
	for i := len(post) - 1; i >= 0; i-- {
		p := post[i]
		if j.stopped() {
			return
		}
		idx := j.addStep("Post " + p.name)
		j.setStep(idx, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
		j.log.Line("##[group]Post %s", p.name)
		sctx, cancel := j.stepContext(ctx, 0)
		err := p.run(sctx)
		cancel()
		j.log.Line("##[endgroup]")
		if err != nil {
			j.log.Line("##[warning]Post %s: %v", p.name, err)
			j.setStep(idx, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)
			continue
		}
		j.setStep(idx, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	}
	j.report(ctx)
}

func (j *job) stepContext(ctx context.Context, minutes int32) (context.Context, context.CancelFunc) {
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
