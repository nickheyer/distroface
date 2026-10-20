package actions

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gopkg.in/yaml.v3"
)

// Runner protocol feature behind interactive jobs
const CapabilityExec = "exec"

// Label a session job asks for when nothing is configured
const defaultRunsOnLabel = "ubuntu-latest"

// A year stands in for no cap
const noJobTimeoutMinutes = 60 * 24 * 365

// Agent session a run belongs to
type AgentRunInfo struct {
	SessionID     string
	SessionNumber int64
	AgentSlug     string
}

// Everything a session job needs at creation
type AgentRunOptions struct {
	Session AgentRunInfo
	// Branch the job checks out and the commit it pins
	Branch string
	SHA    string
	RunsOn []string
	// Wall clock cap on the whole job, zero means none
	TimeoutMinutes int
	// Repository steps that prepare the workspace, nil for checkout only
	Setup *AgentSetup
}

// Workflow file and job name that prepare command workspaces
const AgentSetupJob = "agent-setup-steps"

// Repository defined preparation of a session workspace
type AgentSetup struct {
	Path   string
	RunsOn []string
	// Workflow env and the setup job exactly as written
	env *yaml.Node
	job *yaml.Node
}

// Setup workflow at a revision, nil when none exists
func (m *Manager) LoadAgentSetup(ctx context.Context, repo *db.GitRepository, rev string) (*AgentSetup, error) {
	files, _, err := m.LoadWorkflows(ctx, repo, rev)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if workflowBaseName(f.Path) == AgentSetupJob {
			return agentSetupFromFile(f)
		}
	}
	return nil, nil
}

// Checks the setup file, keeps its job node
func agentSetupFromFile(f *WorkflowFile) (*AgentSetup, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(f.Source), &root); err != nil {
		if f.Err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, f.Err)
		}
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	if len(root.Content) == 0 {
		if f.Err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, f.Err)
		}
		return nil, fmt.Errorf("%s: empty workflow", f.Path)
	}
	doc := root.Content[0]
	s := &AgentSetup{Path: f.Path, env: mapValue(doc, "env"), job: mapValue(mapValue(doc, "jobs"), AgentSetupJob)}
	if s.job == nil {
		return nil, fmt.Errorf("%s: needs a job named %s", f.Path, AgentSetupJob)
	}
	if mapValue(s.job, "uses") != nil {
		return nil, fmt.Errorf("%s: job %s must list steps, not call a workflow", f.Path, AgentSetupJob)
	}
	if f.Err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, f.Err)
	}
	s.RunsOn = labelsOfNode(mapValue(s.job, "runs-on"))
	return s, nil
}

// Labels of a runs-on node, a scalar or a sequence
func labelsOfNode(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if v := strings.TrimSpace(n.Value); v != "" {
			return []string{v}
		}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			if v := strings.TrimSpace(c.Value); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

// Key and value pairs of a mapping node in order
func mapPairs(n *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1]
	}
	return out
}

// Mapping value under one key, nil when absent
func mapValue(n *yaml.Node, key string) *yaml.Node {
	for k, v := range mapPairs(n) {
		if k == key {
			return v
		}
	}
	return nil
}

// Creates a run whose single job stays open for commands
func (m *Manager) StartAgentSessionRun(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser, opts AgentRunOptions) (*db.ActionsRun, *db.ActionsJob, error) {
	if !m.Enabled(ctx, repo) {
		return nil, nil, ErrDisabled
	}
	if opts.Session.SessionID == "" {
		return nil, nil, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	r, err := m.openRepo(repo)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	branch := opts.Branch
	if branch == "" {
		branch = repo.DefaultBranch
	}
	var commit plumbing.Hash
	if opts.SHA != "" {
		commit, err = r.Resolve(opts.SHA)
	} else {
		commit, err = r.Resolve(branch)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s not found", ErrNotFound, branch)
	}
	fullRef := branch
	if !strings.HasPrefix(fullRef, "refs/") {
		fullRef = "refs/heads/" + branch
	}
	source, err := agentSessionWorkflow(opts)
	if err != nil {
		return nil, nil, err
	}
	path := fmt.Sprintf("agent-session-%d.yml", opts.Session.SessionNumber)
	file := ParseWorkflowFile(path, []byte(source))
	if file.Err != nil {
		if opts.Setup != nil {
			return nil, nil, fmt.Errorf("%w: %s: %w", ErrInvalid, opts.Setup.Path, file.Err)
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalid, file.Err)
	}
	payload := map[string]any{
		"ref":        fullRef,
		"repository": m.repoPayload(ctx, repo),
		"agent_session": map[string]any{
			"id": opts.Session.SessionID, "number": opts.Session.SessionNumber, "agent": opts.Session.AgentSlug,
		},
	}
	if actor != nil {
		payload["sender"] = map[string]any{"login": actor.Username}
	}
	in := &triggerInput{
		Event: "workflow_dispatch", Ref: fullRef, SHA: commit.String(), FileCommit: commit, Actor: actor,
		Payload: payload, Inputs: map[string]any{}, OnlyPath: path,
		// Session jobs execute model chosen commands, they never see secrets
		FromFork:     true,
		AgentSession: &opts.Session,
	}
	run, err := m.createRun(ctx, repo, file, in)
	if err != nil {
		return nil, nil, err
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil || len(jobs) == 0 {
		return run, nil, fmt.Errorf("session job was not created")
	}
	return run, jobs[0], nil
}

// Checkout then the repository's setup steps, commands arrive later
func agentSessionWorkflow(opts AgentRunOptions) (string, error) {
	timeout := opts.TimeoutMinutes
	if timeout <= 0 {
		timeout = noJobTimeoutMinutes
	}
	runsOn := opts.RunsOn
	if len(runsOn) == 0 {
		runsOn = []string{defaultRunsOnLabel}
	}
	steps := []any{map[string]any{"uses": "actions/checkout@v4"}}
	job := map[string]any{
		"name":            "Command workspace",
		"runs-on":         runsOn,
		"timeout-minutes": timeout,
		"steps":           steps,
	}
	wf := map[string]any{
		"name":        fmt.Sprintf("Agent session #%d", opts.Session.SessionNumber),
		"on":          "workflow_dispatch",
		"permissions": "read-all",
		"jobs":        map[string]any{"session": job},
	}
	// Setup nodes go in verbatim, parsed again afterwards
	if s := opts.Setup; s != nil {
		if s.env != nil {
			wf["env"] = s.env
		}
		for _, key := range []string{"container", "services", "env", "defaults"} {
			if n := mapValue(s.job, key); n != nil {
				job[key] = n
			}
		}
		if n := mapValue(s.job, "steps"); n != nil && n.Kind == yaml.SequenceNode {
			for _, st := range n.Content {
				steps = append(steps, st)
			}
			job["steps"] = steps
		}
	}
	out, err := yaml.Marshal(wf)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Whether a job may still accept commands
func jobOpen(j *db.ActionsJob) bool {
	switch j.Status {
	case StatusWaiting, StatusQueued, StatusRunning:
		return !j.CloseRequested
	}
	return false
}

// Appends a bash script to a live interactive job
func (m *Manager) EnqueueExec(ctx context.Context, jobID, script string, timeoutMinutes int) (*db.ActionsJobExec, error) {
	job, err := m.store.GetActionsJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("%w: job not found", ErrNotFound)
	}
	if !job.Interactive {
		return nil, fmt.Errorf("%w: job does not accept commands", ErrInvalid)
	}
	if !jobOpen(job) {
		return nil, fmt.Errorf("%w: the command workspace is closed", ErrConflict)
	}
	if strings.TrimSpace(script) == "" {
		return nil, fmt.Errorf("%w: script is required", ErrInvalid)
	}
	e := &db.ActionsJobExec{JobID: jobID, Script: script, TimeoutMinutes: timeoutMinutes}
	if err := m.store.CreateActionsJobExec(ctx, e); err != nil {
		return nil, err
	}
	m.execSignal(execJobKey(jobID))
	return e, nil
}

// Pollers rescan a quiet job no more often than this
const execRescan = 10 * time.Second

// Long polls for the next command or the job's end
func (m *Manager) FetchExec(ctx context.Context, runner *db.ActionsRunner, jobID string, wait time.Duration) (exec *v1.ExecRequest, done, cancelled bool, err error) {
	if wait <= 0 || wait > maxTaskWait {
		wait = maxTaskWait
	}
	deadline := time.Now().Add(wait)
	for {
		wake := m.execWaiter(execJobKey(jobID))
		job, err := m.runnerOwnedJob(ctx, runner, jobID)
		if err != nil {
			return nil, false, false, err
		}
		if !job.Interactive {
			return nil, false, false, fmt.Errorf("%w: job is not interactive", ErrInvalid)
		}
		if job.Status != StatusRunning {
			return nil, true, job.Conclusion == ConclusionCancelled, nil
		}
		if m.cancelPending(ctx, job) {
			m.closeJobExecs(ctx, job, ConclusionCancelled, "the job was cancelled")
			return nil, true, true, nil
		}
		// Queued commands run even once the job is closing
		if ex, err := m.startNextExec(ctx, jobID); err != nil || ex != nil {
			return ex, false, false, err
		}
		if job.CloseRequested {
			return nil, true, false, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false, false, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, false, ctx.Err()
		case <-wake:
		case <-time.After(min(remaining, execRescan)):
		}
	}
}

// Oldest queued command, nil when the queue is empty
func (m *Manager) startNextExec(ctx context.Context, jobID string) (*v1.ExecRequest, error) {
	for {
		next, err := m.store.NextQueuedActionsJobExec(ctx, jobID)
		if err != nil || next == nil {
			return nil, err
		}
		ok, err := m.store.StartActionsJobExec(ctx, next.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		m.execSignal(next.ID)
		return &v1.ExecRequest{Id: next.ID, Number: int32(next.Number), Script: next.Script, TimeoutMinutes: int32(next.TimeoutMinutes)}, nil
	}
}

// Outcome of one command as the runner reports it
type ExecResult struct {
	Conclusion string
	ExitCode   int
	Output     string
	Error      string
}

// Stores a command's outcome and wakes its waiter
func (m *Manager) CompleteExec(ctx context.Context, runner *db.ActionsRunner, jobID, execID string, res ExecResult) error {
	if _, err := m.runnerOwnedJob(ctx, runner, jobID); err != nil {
		return err
	}
	e, err := m.store.GetActionsJobExec(ctx, execID)
	if err != nil {
		return err
	}
	if e == nil || e.JobID != jobID {
		return fmt.Errorf("%w: command not found", ErrNotFound)
	}
	if e.Status == stores.ExecCompleted {
		return nil
	}
	switch res.Conclusion {
	case ConclusionSuccess, ConclusionFailure, ConclusionCancelled, ConclusionTimedOut:
	default:
		res.Conclusion = ConclusionFailure
	}
	cols := map[string]any{
		"status": stores.ExecCompleted, "conclusion": res.Conclusion, "exit_code": res.ExitCode,
		"output": res.Output, "error": res.Error, "finished_at": time.Now().UTC(),
	}
	if err := m.store.UpdateActionsJobExecColumns(ctx, execID, cols); err != nil {
		return err
	}
	m.execSignal(execID)
	return nil
}

// Blocks until the command finishes or the context ends
func (m *Manager) WaitExec(ctx context.Context, execID string) (*db.ActionsJobExec, error) {
	for {
		wake := m.execWaiter(execID)
		e, err := m.store.GetActionsJobExec(ctx, execID)
		if err != nil {
			return nil, err
		}
		if e == nil {
			return nil, fmt.Errorf("%w: command disappeared", ErrNotFound)
		}
		if e.Status == stores.ExecCompleted {
			return e, nil
		}
		// A job that died takes its commands with it
		if job, _ := m.store.GetActionsJob(ctx, e.JobID); job != nil && job.Status == StatusCompleted {
			if slices.Contains(m.closeJobExecs(ctx, job, ConclusionFailure, "the command workspace ended before the command finished"), e.ID) {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		case <-time.After(2 * time.Second):
		}
	}
}

// Lets the runner finish once the queue drains
func (m *Manager) RequestJobClose(ctx context.Context, jobID string) error {
	if err := m.store.UpdateActionsJobColumns(ctx, jobID, map[string]any{"close_requested": true}); err != nil {
		return err
	}
	m.execSignal(execJobKey(jobID))
	return nil
}

// Ends every live command workspace of a session
func (m *Manager) CloseAgentSessionJobs(ctx context.Context, sessionID string) {
	jobs, err := m.store.ListLiveInteractiveJobsForAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.Status == StatusRunning {
			_ = m.RequestJobClose(ctx, j.ID)
			continue
		}
		// Never claimed, nothing to drain
		run, _ := m.store.GetActionsRunByID(ctx, j.RunID)
		if run == nil {
			continue
		}
		repo, _ := m.store.GetGitRepositoryByID(ctx, run.RepoID)
		if repo == nil {
			continue
		}
		m.cancelRun(ctx, repo, run)
	}
}

// Open command workspace of a session, nil when none
func (m *Manager) LiveAgentSessionJob(ctx context.Context, sessionID string) (*db.ActionsJob, *db.ActionsRun, error) {
	jobs, err := m.store.ListLiveInteractiveJobsForAgentSession(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	for _, j := range jobs {
		if !jobOpen(j) {
			continue
		}
		run, err := m.store.GetActionsRunByID(ctx, j.RunID)
		if err != nil {
			return nil, nil, err
		}
		if run != nil {
			return j, run, nil
		}
	}
	return nil, nil, nil
}

// Fails commands a finished job never ran
func (m *Manager) closeJobExecs(ctx context.Context, job *db.ActionsJob, conclusion, reason string) []string {
	if !job.Interactive {
		return nil
	}
	ids, err := m.store.FinishOpenActionsJobExecs(ctx, job.ID, conclusion, reason)
	if err != nil {
		return nil
	}
	for _, id := range ids {
		m.execSignal(id)
	}
	m.execSignal(execJobKey(job.ID))
	return ids
}

func execJobKey(jobID string) string { return "job:" + jobID }

// Channel closed on the next signal for a key
func (m *Manager) execWaiter(key string) <-chan struct{} {
	m.execMu.Lock()
	defer m.execMu.Unlock()
	ch, ok := m.execWake[key]
	if !ok {
		ch = make(chan struct{})
		m.execWake[key] = ch
	}
	return ch
}

// Wakes every waiter on a key
func (m *Manager) execSignal(key string) {
	m.execMu.Lock()
	defer m.execMu.Unlock()
	if ch, ok := m.execWake[key]; ok {
		close(ch)
		delete(m.execWake, key)
	}
}

func (m *Manager) GetRun(ctx context.Context, id string) (*db.ActionsRun, error) {
	return m.store.GetActionsRunByID(ctx, id)
}

func (m *Manager) GetJob(ctx context.Context, id string) (*db.ActionsJob, error) {
	return m.store.GetActionsJob(ctx, id)
}

func (m *Manager) RunJobs(ctx context.Context, runID string) ([]*db.ActionsJob, error) {
	return m.store.ListActionsJobs(ctx, runID)
}

// Workflow runs on one commit, session jobs excluded
func (m *Manager) RunsForCommit(ctx context.Context, repo *db.GitRepository, sha string, limit int) ([]*db.ActionsRun, error) {
	runs, _, err := m.store.ListActionsRuns(ctx, stores.ActionsRunListOptions{RepoID: repo.ID, SHA: sha, Limit: limit, ExcludeAgentRuns: true})
	return runs, err
}

// Whole job log from live chunks or the compacted blob
func (m *Manager) JobLog(ctx context.Context, job *db.ActionsJob) ([]byte, error) {
	if job.LogDigest != "" {
		return m.ReadCompactedLog(job)
	}
	chunks, err := m.store.ListActionsJobLogs(ctx, job.ID, 0, 0)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(c.Data)
	}
	return buf.Bytes(), nil
}

func (m *Manager) RunURL(ctx context.Context, repo *db.GitRepository, runID string) string {
	return m.runURL(ctx, repo, runID)
}
