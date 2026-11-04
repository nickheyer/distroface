package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitea.com/gitea/runner/act/common"
	"gitea.com/gitea/runner/act/model"
	actrunner "gitea.com/gitea/runner/act/runner"
	shellquote "github.com/kballard/go-shellquote"
	"github.com/moby/moby/api/types/container"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/sirupsen/logrus"
	"go.yaml.in/yaml/v4"
)

// Wall clock cap when the server names none
const defaultJobMinutes = 360

// Username the instance registry accepts for job tokens
const jobTokenUser = "x-access-token"

// In container home of the per job binds
const containerJobDir = "/__df"

const (
	serverCancelMsg   = "The job was cancelled"
	operatorCancelMsg = "The job was cancelled by the runner operator"
)

// One task from claim to completion
type job struct {
	r    *Runner
	task *v1.Task
	log  *logStream
	rep  *reporter
	// Host directory holding the workspace, docker config, and session files
	dir string
	// Server or operator asked for a stop
	cancelled atomic.Bool
	// Job level timeout fired
	timedOut  atomic.Bool
	cancelAct context.CancelFunc
	session   *session
}

func newJob(r *Runner, task *v1.Task) *job {
	j := &job{r: r, task: task}
	j.dir = filepath.Join(r.cfg.JobRoot(), r.jobDirPrefix()+shortID(task.JobId))
	return j
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
	if j.task.RuntimeToken != "" {
		j.log.AddMask(j.task.RuntimeToken)
	}
	j.rep = newReporter(j)
	ctx, cancel := context.WithCancel(parent)
	j.cancelAct = cancel
	stop := make(chan struct{})
	go j.rep.daemon(ctx, stop)
	go func() {
		select {
		case <-parent.Done():
			j.requestCancel(operatorCancelMsg)
		case <-stop:
		}
	}()

	conclusion := j.execute(ctx)
	close(stop)
	cancel()
	j.rep.finish(conclusion)
	j.log.Line("Job finished with conclusion %s", strings.ToLower(strings.TrimPrefix(conclusion.String(), "ACTIONS_CONCLUSION_")))
	if err := j.log.Close(bg); err != nil {
		j.r.out.Printf("job %s: log upload: %v", shortID(j.task.JobId), err)
	}
	j.complete(bg, conclusion)
	_ = os.RemoveAll(j.dir)
}

// Stops act, only always and cancelled steps continue
func (j *job) requestCancel(reason string) {
	if !j.cancelled.Swap(true) {
		j.log.Line("##[error]%s", reason)
		if j.cancelAct != nil {
			j.cancelAct()
		}
	}
}

func (j *job) timeoutMinutes() int {
	if m := int(j.task.TimeoutMinutes); m > 0 {
		return m
	}
	return defaultJobMinutes
}

// Prefix act gives this job's container, network, and volumes
func (j *job) containerPrefix() string {
	return containerNamePrefix(j.r.containerPrefix(), shortID(j.task.JobId))
}

// Runs the plan through act and answers the conclusion
func (j *job) execute(ctx context.Context) v1.ActionsConclusion {
	j.rep.startSetup()
	j.log.Line("Runner %s (dfcli %s) picked up job %s of %s", j.r.cfg.Name, j.r.version, j.task.JobName, j.task.RepoFullName)
	workflow, jobID, err := j.workflow()
	if err != nil {
		return j.rep.failSetup(fmt.Errorf("workflow: %w", err))
	}
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return j.rep.failSetup(err)
	}
	if j.task.GetInteractive() {
		j.session = newSession(j)
		if err := j.session.prepare(workflow, jobID); err != nil {
			return j.rep.failSetup(err)
		}
	}
	j.rep.setSteps(workflow.GetJob(jobID).Steps)
	env, err := j.prepareEnv()
	if err != nil {
		return j.rep.failSetup(err)
	}
	cfg, err := j.actConfig(env)
	if err != nil {
		return j.rep.failSetup(err)
	}
	plan, err := model.CombineWorkflowPlanner(workflow).PlanJob(jobID)
	if err != nil {
		return j.rep.failSetup(fmt.Errorf("plan: %w", err))
	}
	if err := j.pullImages(ctx, cfg, workflow, jobID); err != nil {
		return j.rep.failSetup(err)
	}
	rr, err := actrunner.New(cfg)
	if err != nil {
		return j.rep.failSetup(err)
	}
	executor := rr.NewPlanExecutor(plan)
	actCtx := common.WithLoggerHook(ctx, j.rep)
	actCtx = actrunner.WithJobLoggerFactory(actCtx, discardLogger{})
	timeout := time.Duration(j.timeoutMinutes()) * time.Minute
	actCtx, cancelTimeout := context.WithTimeout(actCtx, timeout)
	defer cancelTimeout()
	if j.session != nil {
		go j.session.serve(actCtx)
	}
	j.log.Line("Workflow prepared, %d steps", len(workflow.GetJob(jobID).Steps))
	err = j.runPlan(actCtx, executor)
	if errors.Is(actCtx.Err(), context.DeadlineExceeded) && !j.cancelled.Load() {
		j.timedOut.Store(true)
		j.log.Line("##[error]The job has exceeded the maximum execution time of %d minutes", j.timeoutMinutes())
	}
	cancelTimeout()
	if j.session != nil {
		<-j.session.done
	}
	j.rep.setOutputs(workflow.GetJob(jobID).Outputs)
	switch {
	case j.cancelled.Load():
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	case j.timedOut.Load():
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT
	case err != nil:
		// Act's job failed error repeats what the failed step already reported
		if j.rep.result() != "failure" {
			j.rep.errorLine(err.Error())
		}
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	case j.rep.result() == "failure":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	case j.rep.result() == "skipped":
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED
	}
	return v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS
}

// Panics inside act fail the job, not the runner process
func (j *job) runPlan(ctx context.Context, executor common.Executor) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		if de, ok := p.(*decodeError); ok {
			err = de
		} else {
			err = fmt.Errorf("panic in the job engine: %v", p)
		}
		j.removeContainers()
	}()
	return executor(ctx)
}

// After a panic act never cleaned up, do it here
func (j *job) removeContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli, err := dockerClient(ctx, j.r.cfg.DockerHost)
	if err != nil {
		return
	}
	defer cli.Close()
	for _, name := range removeDockerLeftovers(ctx, cli, j.containerPrefix()) {
		j.log.Line("Removed %s", name)
	}
}

// Raised by the decode hook, recovered by the job
type decodeError struct {
	node yaml.Node
	out  any
	err  error
}

func (e *decodeError) Error() string {
	return fmt.Sprintf("workflow decode: line %d column %d (%s) into %T: %v", e.node.Line, e.node.Column, e.node.Tag, e.out, e.err)
}

var decodeHookOnce sync.Once

// Act defaults to log.Fatalf, which would take the process down
func installDecodeHook() {
	decodeHookOnce.Do(func() {
		model.OnDecodeNodeError = func(node yaml.Node, out any, err error) {
			panic(&decodeError{node: node, out: out, err: err})
		}
	})
}

// Forces every lazy decode now, bad nodes fail early
func probeDecodes(w *model.Workflow, jobID string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			de, ok := p.(*decodeError)
			if !ok {
				panic(p)
			}
			err = de
		}
	}()
	w.On()
	job := w.GetJob(jobID)
	job.Container()
	job.Needs()
	job.RunsOn()
	job.Environment()
	job.Secrets()
	if _, err := job.GetMatrixes(); err != nil {
		return err
	}
	for _, s := range job.Steps {
		if s != nil {
			s.Environment()
		}
	}
	return nil
}

// Reads the single job payload and wires the needs the server resolved
func (j *job) workflow() (*model.Workflow, string, error) {
	workflow, err := model.ReadWorkflow(bytes.NewReader(j.task.WorkflowPayload))
	if err != nil {
		return nil, "", err
	}
	ids := workflow.GetJobIDs()
	if len(ids) != 1 {
		return nil, "", fmt.Errorf("payload holds %d jobs, expected one", len(ids))
	}
	jobID := ids[0]
	needIDs := make([]string, 0, len(j.task.Needs))
	for id, need := range j.task.Needs {
		needIDs = append(needIDs, id)
		workflow.Jobs[id] = &model.Job{
			Outputs: need.GetOutputs(),
			Result:  conclusionString(need.GetResult()),
		}
	}
	sort.Strings(needIDs)
	rawNeeds := yaml.Node{Kind: yaml.SequenceNode}
	for _, id := range needIDs {
		rawNeeds.Content = append(rawNeeds.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: id})
	}
	workflow.Jobs[jobID].RawNeeds = rawNeeds
	if err := probeDecodes(workflow, jobID); err != nil {
		return nil, "", err
	}
	return workflow, jobID, nil
}

func conclusionString(c v1.ActionsConclusion) string {
	switch c {
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS, v1.ActionsConclusion_ACTIONS_CONCLUSION_NEUTRAL:
		return "success"
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED:
		return "cancelled"
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED:
		return "skipped"
	}
	return "failure"
}

// Registry host of the instance, where the job token logs in
func registryHost(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// Environment every step sees, docker config and session paths included
func (j *job) prepareEnv() (map[string]string, error) {
	server := strings.TrimRight(j.task.ServerUrl, "/")
	env := map[string]string{
		"ACTIONS_CACHE_URL":                 server + "/api/actions/cache/",
		"ACTIONS_RUNTIME_URL":               server + "/api/actions_pipeline/",
		"ACTIONS_RESULTS_URL":               server,
		"GITEA_ACTIONS":                     "true",
		"GITEA_ACTIONS_RUNNER_VERSION":      j.r.version,
		"DISTROFACE_ACTIONS":                "true",
		"DISTROFACE_ACTIONS_RUNNER_VERSION": j.r.version,
	}
	if j.task.RuntimeToken != "" {
		env["ACTIONS_RUNTIME_TOKEN"] = j.task.RuntimeToken
	}
	host := registryHost(j.task.ServerUrl)
	if host != "" {
		env["DISTROFACE_REGISTRY"] = host
	}
	dockerDir := filepath.Join(j.dir, "docker")
	if err := os.MkdirAll(dockerDir, 0o700); err != nil {
		return nil, err
	}
	auths := map[string]any{}
	if host != "" && j.task.Token != "" {
		auths[host] = map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(jobTokenUser + ":" + j.task.Token))}
	}
	cfg, err := json.Marshal(map[string]any{"auths": auths})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dockerDir, "config.json"), cfg, 0o600); err != nil {
		return nil, err
	}
	if j.hostJob() {
		env["DOCKER_CONFIG"] = dockerDir
	} else {
		env["DOCKER_CONFIG"] = containerJobDir + "/docker"
	}
	if j.session != nil {
		env["DISTROFACE_SESSION_DIR"] = j.session.stepDir()
	}
	return env, nil
}

// Whether the labels map to the host executor
func (j *job) hostJob() bool {
	_, host, ok := j.r.cfg.ImageFor(j.task.RunsOn)
	return ok && host
}

// Image act runs the job in, the host marker for host labels
func (j *job) pickPlatform(labels []string) string {
	image, host, ok := j.r.cfg.ImageFor(labels)
	switch {
	case ok && host:
		return "-self-hosted"
	case ok:
		return image
	}
	return j.r.cfg.FallbackImage()
}

// Repository checkout root on the host and inside the container
func (j *job) workdir() string {
	return filepath.Join(j.dir, "work", filepath.FromSlash(j.task.RepoFullName))
}

// Act configuration for one task
func (j *job) actConfig(env map[string]string) (*actrunner.Config, error) {
	var gitCtx map[string]any
	if err := json.Unmarshal([]byte(j.task.Context), &gitCtx); err != nil {
		return nil, fmt.Errorf("task context: %w", err)
	}
	str := func(k string) string {
		v, _ := gitCtx[k].(string)
		return v
	}
	event, _ := gitCtx["event"].(map[string]any)
	if event == nil {
		event = map[string]any{}
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	preset := &model.GithubContext{
		Event: event, RunID: str("run_id"), RunNumber: str("run_number"), RunAttempt: str("run_attempt"),
		Actor: str("actor"), Repository: str("repository"), EventName: str("event_name"), Sha: str("sha"),
		Ref: str("ref"), RefName: str("ref_name"), RefType: str("ref_type"), HeadRef: str("head_ref"), BaseRef: str("base_ref"),
		Token: j.task.Token, RepositoryOwner: str("repository_owner"), RetentionDays: str("retention_days"), Workflow: str("workflow"),
	}
	secrets := map[string]string{}
	for k, v := range j.task.Secrets {
		secrets[k] = v
	}
	options := strings.TrimSpace(j.r.cfg.ContainerOptions)
	validVolumes := append([]string{}, j.r.cfg.ValidVolumes...)
	if !j.hostJob() {
		binds := []string{shellquote.Join("-v", filepath.Join(j.dir, "docker")+":"+containerJobDir+"/docker")}
		if j.session != nil {
			binds = append(binds, shellquote.Join("-v", j.session.dir+":"+j.session.stepDir()))
		}
		options = strings.TrimSpace(options + " " + strings.Join(binds, " "))
		validVolumes = append(validVolumes, filepath.Join(j.dir, "docker"), filepath.Join(j.dir, "session"))
	}
	socket := "-"
	if j.r.cfg.PrivilegedJobs {
		socket = ""
	}
	cfg := &actrunner.Config{
		Workdir:               j.workdir(),
		BindWorkdir:           false,
		ActionCacheDir:        filepath.Join(j.r.cfg.JobRoot(), "_actions"),
		ReuseContainers:       false,
		ForcePull:             false,
		LogOutput:             true,
		Env:                   env,
		Secrets:               secrets,
		Vars:                  j.task.Variables,
		Token:                 j.task.Token,
		GitHubInstance:        strings.TrimRight(j.task.ServerUrl, "/"),
		AutoRemove:            true,
		NoSkipCheckout:        true,
		PresetGitHubContext:   preset,
		EventJSON:             string(eventJSON),
		ContainerNamePrefix:   j.containerPrefix(),
		ContainerMaxLifetime:  time.Duration(j.timeoutMinutes())*time.Minute + time.Hour,
		CleanWorkdir:          true,
		ContainerNetworkMode:  container.NetworkMode(j.r.cfg.ContainerNetwork),
		ContainerOptions:      options,
		ContainerDaemonSocket: socket,
		Privileged:            j.r.cfg.PrivilegedJobs,
		DefaultActionInstance: strings.TrimRight(j.task.DefaultActionsUrl, "/"),
		PlatformPicker:        j.pickPlatform,
		ValidVolumes:          validVolumes,
		InsecureSkipTLS:       j.r.cfg.InsecureSkipTLS,
	}
	return cfg, nil
}

// Reports the final conclusion, retrying transient failures
func (j *job) complete(ctx context.Context, conclusion v1.ActionsConclusion) {
	steps, outputs, ann := j.rep.snapshot()
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
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
}

// Job loggers stay quiet, the hook carries every entry to the server
type discardLogger struct{}

func (discardLogger) WithJobLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(nopWriter{})
	l.SetLevel(logrus.TraceLevel)
	return l
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
