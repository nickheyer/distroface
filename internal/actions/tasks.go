package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// RunnerTokenHeader carries the long lived runner token
const RunnerTokenHeader = "X-Runner-Token"

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// AuthenticateRunner resolves the runner behind a token
func (m *Manager) AuthenticateRunner(ctx context.Context, token string) (*db.ActionsRunner, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: missing runner token", ErrDenied)
	}
	r, err := m.store.GetActionsRunnerByTokenHash(ctx, hashToken(token))
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: unknown runner token", ErrDenied)
	}
	return r, nil
}

// Loads a job and refuses ones not assigned to the runner
func (m *Manager) runnerJob(ctx context.Context, runner *db.ActionsRunner, jobID string) (*db.ActionsJob, *db.ActionsRun, *db.GitRepository, error) {
	job, err := m.store.GetActionsJob(ctx, jobID)
	if err != nil {
		return nil, nil, nil, err
	}
	if job == nil {
		return nil, nil, nil, fmt.Errorf("%w: job not found", ErrNotFound)
	}
	if job.RunnerID != runner.ID {
		return nil, nil, nil, fmt.Errorf("%w: job is not assigned to this runner", ErrDenied)
	}
	run, err := m.store.GetActionsRunByID(ctx, job.RunID)
	if err != nil {
		return nil, nil, nil, err
	}
	if run == nil {
		return nil, nil, nil, fmt.Errorf("%w: run not found", ErrNotFound)
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, run.RepoID)
	if err != nil {
		return nil, nil, nil, err
	}
	if repo == nil {
		return nil, nil, nil, fmt.Errorf("%w: repository not found", ErrNotFound)
	}
	return job, run, repo, nil
}

// ── FetchTask ────────────────────────────────────────────────────────────

// FetchTask long polls for a queued job the runner can serve
func (m *Manager) FetchTask(ctx context.Context, runner *db.ActionsRunner, wait time.Duration) (*v1.Task, error) {
	if wait <= 0 || wait > maxTaskWait {
		wait = maxTaskWait
	}
	deadline := time.Now().Add(wait)
	for {
		task, err := m.tryClaim(ctx, runner)
		if err != nil {
			return nil, err
		}
		if task != nil {
			return task, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		if remaining > 2*time.Second {
			remaining = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.wakeChan():
		case <-time.After(remaining):
		}
	}
}

// Queued jobs examined per poll before giving up, in pages
const (
	claimPageSize = 100
	claimMaxPages = 20
)

// Claims the oldest queued job matching scope, labels, and concurrency
func (m *Manager) tryClaim(ctx context.Context, runner *db.ActionsRunner) (*v1.Task, error) {
	runnerLabels := decodeStrings(runner.Labels)
	var job *db.ActionsJob
	var run *db.ActionsRun
	var repo *db.GitRepository
	// Scope is filtered in sql, labels in memory, so unservable jobs never block the queue
	for page := 0; page < claimMaxPages && job == nil; page++ {
		jobs, err := m.store.ListQueuedActionsJobsInScope(ctx, runner.ScopeKind, runner.ScopeID, claimPageSize, page*claimPageSize)
		if err != nil {
			return nil, err
		}
		if len(jobs) == 0 {
			return nil, nil
		}
		runIDs := make([]string, 0, len(jobs))
		for _, j := range jobs {
			runIDs = append(runIDs, j.RunID)
		}
		runs, err := m.store.GetActionsRunsByIDs(ctx, runIDs)
		if err != nil {
			return nil, err
		}
		repoIDs := make([]string, 0, len(runs))
		for _, r := range runs {
			repoIDs = append(repoIDs, r.RepoID)
		}
		repos, err := m.store.GetGitRepositoriesByIDs(ctx, repoIDs)
		if err != nil {
			return nil, err
		}
		var candidates []string
		for _, j := range jobs {
			r := runs[j.RunID]
			if r == nil || repos[r.RepoID] == nil || !labelsSubset(decodeStrings(j.RunsOn), runnerLabels) {
				continue
			}
			if r.Concurrency != "" && m.groupBusy(ctx, r) {
				continue
			}
			if j.Concurrency != "" && m.jobGroupBusy(ctx, r.RepoID, j) {
				continue
			}
			candidates = append(candidates, j.ID)
		}
		if len(candidates) == 0 {
			if len(jobs) < claimPageSize {
				return nil, nil
			}
			continue
		}
		if job, err = m.store.ClaimActionsJob(ctx, runner.ID, candidates); err != nil {
			return nil, err
		}
		if job != nil {
			run, repo = runs[job.RunID], repos[runs[job.RunID].RepoID]
		}
	}
	if job == nil {
		return nil, nil
	}
	if run.Status != StatusRunning {
		now := time.Now().UTC()
		cols := map[string]any{"status": StatusRunning}
		if run.StartedAt == nil {
			cols["started_at"] = now
			run.StartedAt = &now
		}
		run.Status = StatusRunning
		_ = m.store.UpdateActionsRunColumns(ctx, run.ID, cols)
	}
	task, err := m.buildTask(ctx, runner, repo, run, job)
	if err != nil {
		m.log.Error("actions: build task for job %s: %v", job.ID, err)
		m.failJob(ctx, repo, run, job, ConclusionFailure, "server could not prepare the job: "+err.Error())
		m.advanceRun(ctx, repo, run)
		return nil, nil
	}
	return task, nil
}

func (m *Manager) runnerServes(ctx context.Context, runner *db.ActionsRunner, repo *db.GitRepository) bool {
	switch runner.ScopeKind {
	case db.ActionsScopeSystem:
		return true
	case db.ActionsScopeOrg:
		return repo.IsOrgNamespace && m.repoOrgID(ctx, repo) == runner.ScopeID
	case db.ActionsScopeRepo:
		return runner.ScopeID == repo.ID
	}
	return false
}

// Every job label must appear on the runner, case insensitive
func labelsSubset(want, have []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if strings.EqualFold(strings.TrimSpace(w), strings.TrimSpace(h)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Another run of the same concurrency group is executing
// Another job of the same job level group is running in the repository
func (m *Manager) jobGroupBusy(ctx context.Context, repoID string, j *db.ActionsJob) bool {
	live, err := m.store.ListLiveJobsInGroup(ctx, repoID, j.Concurrency, j.ID)
	if err != nil {
		return false
	}
	for _, other := range live {
		if other.Status == StatusRunning {
			return true
		}
	}
	return false
}

func (m *Manager) groupBusy(ctx context.Context, run *db.ActionsRun) bool {
	live, err := m.store.ListLiveRunsInGroup(ctx, run.RepoID, run.Concurrency, run.ID)
	if err != nil {
		return false
	}
	for _, other := range live {
		if other.Status == StatusRunning {
			return true
		}
	}
	return false
}

// Everything the runner needs to execute one job
func (m *Manager) buildTask(ctx context.Context, runner *db.ActionsRunner, repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob) (*v1.Task, error) {
	def, err := decodeDefinition(job)
	if err != nil {
		return nil, err
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	_, status, needs := needsState(def.Job.Needs, jobs)
	var wf *Workflow
	if run.Workflow != "" {
		wf, _ = ParseWorkflow([]byte(run.Workflow))
	}
	ectx := m.jobContext(ctx, repo, run, wf, def, job, needs, status)
	orgID := m.repoOrgID(ctx, repo)
	secrets := map[string]string{}
	if !run.FromFork {
		if secrets, err = m.repoSecrets(ctx, repo, orgID); err != nil {
			return nil, err
		}
	}
	token, err := m.issueJobToken(ctx, repo, run, job)
	if err != nil {
		return nil, err
	}
	if token != "" {
		secrets["GITHUB_TOKEN"] = token
		secrets["DISTROFACE_TOKEN"] = token
	}
	ectx.Secrets = secrets

	interp := func(s string) string {
		out, err := Interpolate(s, ectx)
		if err != nil {
			return s
		}
		return out
	}
	interpMap := func(in map[string]string) map[string]string {
		out := make(map[string]string, len(in))
		for k, v := range in {
			out[k] = interp(v)
		}
		return out
	}

	task := &v1.Task{
		JobId: job.ID, RunId: run.ID, RepoFullName: repo.Namespace + "/" + repo.Name, JobName: job.Name,
		Env:            interpMap(mergeEnv(def.WorkflowEnv, def.Job.Env)),
		Secrets:        secrets,
		Variables:      ectx.Vars,
		Token:          token,
		Needs:          mustJSON(needs),
		Matrix:         job.Matrix,
		TimeoutMinutes: int32(def.Job.TimeoutMinutes),
		CloneUrl:       m.git.HTTPCloneURL(ctx, repo, false),
		ServerUrl:      m.git.BaseURL(ctx),
		RunsOn:         decodeStrings(job.RunsOn),
	}
	if task.TimeoutMinutes <= 0 {
		task.TimeoutMinutes = defaultJobTimeout
	}
	if def.Job.Defaults != nil {
		task.DefaultShell = def.Job.Defaults.Shell
	}
	if c := def.Job.Container; c != nil {
		task.ContainerImage = interp(c.Image)
		task.ContainerOptions = interp(c.Options)
		for k, v := range interpMap(c.Env) {
			task.Env[k] = v
		}
	}
	for _, name := range def.Job.ServiceOrder {
		svc := def.Job.Services[name]
		if svc == nil {
			continue
		}
		task.Services = append(task.Services, &v1.TaskService{
			Name: name, Image: interp(svc.Image), Env: interpMap(svc.Env),
			Ports: svc.Ports, Volumes: svc.Volumes, Options: interp(svc.Options),
		})
	}
	// Step fields stay raw, the runner evaluates them exactly once
	for i, st := range def.Job.Steps {
		ts := &v1.TaskStep{
			Number: int32(i + 1), Name: st.Name, Id: st.ID,
			Uses: st.Uses, Run: st.Run, Shell: st.Shell,
			WorkingDirectory: st.WorkingDirectory,
			Env:              map[string]string{}, With: map[string]string{},
			Condition: st.If, ContinueOnError: st.ContinueOnError, TimeoutMinutes: int32(st.TimeoutMinutes),
		}
		if ts.Name == "" {
			if st.Uses != "" {
				ts.Name = "Run " + st.Uses
			} else {
				ts.Name = firstLine(ts.Run)
			}
		}
		if ts.WorkingDirectory == "" && def.Job.Defaults != nil {
			ts.WorkingDirectory = def.Job.Defaults.WorkingDirectory
		}
		maps.Copy(ts.Env, st.Env)
		maps.Copy(ts.With, st.With)
		task.Steps = append(task.Steps, ts)
	}

	// Context carries github, inputs, job output expressions, and the workflow source
	outputs := def.Job.Outputs
	if outputs == nil {
		outputs = map[string]string{}
	}
	task.Context = mustJSON(map[string]any{
		"github":          ectx.GitHub,
		"inputs":          ectx.Inputs,
		"outputs":         outputs,
		"job":             map[string]any{"key": baseKey(job), "continue_on_error": def.Job.ContinueOnError, "attempt": job.Attempt},
		"workflow_path":   run.WorkflowPath,
		"workflow_source": run.Workflow,
	})
	return task, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return "Run " + s
}

// Scoped api token the runner uses for checkout and publishing
func (m *Manager) issueJobToken(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob) (string, error) {
	owner := run.TriggerUserID
	if owner == "" {
		owner = repo.OwnerID
	}
	if owner == "" {
		m.log.Warn("actions: no token owner for run %s, job runs without a token", run.ID)
		return "", nil
	}
	if u, _ := m.store.GetUserByID(ctx, owner); u == nil {
		if repo.OwnerID != "" && repo.OwnerID != owner {
			owner = repo.OwnerID
		} else {
			return "", nil
		}
	}
	days := jobTokenDays
	plain, row, err := m.auth.GenerateScopedAPIToken(ctx, owner, fmt.Sprintf("actions job %s", job.ID), &days, &auth.CredentialScope{
		Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO, ID: repo.Namespace + "/" + repo.Name, ReadOnly: run.FromFork,
	})
	if err != nil {
		return "", err
	}
	if job.TokenID != "" {
		_ = m.store.DeleteAPITokenByID(ctx, job.TokenID)
	}
	job.TokenID = row.ID
	if err := m.store.UpdateActionsJobColumns(ctx, job.ID, map[string]any{"token_id": row.ID}); err != nil {
		_ = m.store.DeleteAPITokenByID(ctx, row.ID)
		return "", err
	}
	return plain, nil
}

func (m *Manager) revokeJobToken(ctx context.Context, job *db.ActionsJob) {
	if job.TokenID == "" {
		return
	}
	if err := m.store.DeleteAPITokenByID(ctx, job.TokenID); err != nil {
		m.log.Warn("actions: revoke token for job %s: %v", job.ID, err)
	}
	job.TokenID = ""
	_ = m.store.UpdateActionsJobColumns(ctx, job.ID, map[string]any{"token_id": ""})
}

// ── Progress ─────────────────────────────────────────────────────────────

// UpdateTask records step states and tells the runner about cancellation
func (m *Manager) UpdateTask(ctx context.Context, runner *db.ActionsRunner, jobID string, steps []*v1.JobStep, outputs, annotations string) (bool, error) {
	job, _, _, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return false, err
	}
	if job.Status != StatusRunning {
		return true, nil
	}
	cols := map[string]any{}
	if steps != nil {
		cols["steps"] = encodeSteps(steps)
	}
	if outputs != "" && json.Valid([]byte(outputs)) {
		cols["outputs"] = outputs
	}
	if annotations != "" && json.Valid([]byte(annotations)) {
		cols["annotations"] = annotations
	}
	if len(cols) > 0 {
		if err := m.store.UpdateActionsJobColumns(ctx, job.ID, cols); err != nil {
			return false, err
		}
	}
	return m.cancelPending(ctx, job), nil
}

// AppendTaskLog stores one chunk and answers the next expected index
func (m *Manager) AppendTaskLog(ctx context.Context, runner *db.ActionsRunner, jobID string, index int, data []byte) (int, error) {
	job, _, _, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return 0, err
	}
	if job.Status != StatusRunning {
		return job.LogChunks, fmt.Errorf("%w: job is not running", ErrConflict)
	}
	if index < 0 {
		index = job.LogChunks
	}
	if index < job.LogChunks {
		return job.LogChunks, nil
	}
	if index > job.LogChunks {
		return job.LogChunks, fmt.Errorf("%w: expected log index %d", ErrConflict, job.LogChunks)
	}
	if len(data) == 0 {
		return job.LogChunks, nil
	}
	if len(data) > MaxJobLogChunkBytes {
		return job.LogChunks, fmt.Errorf("%w: log chunk exceeds %d KB", ErrTooLarge, MaxJobLogChunkBytes>>10)
	}
	if job.LogBytes+int64(len(data)) > MaxJobLogBytes {
		return job.LogChunks, fmt.Errorf("%w: job log exceeds %d MB", ErrTooLarge, MaxJobLogBytes>>20)
	}
	if err := m.store.AppendActionsJobLog(ctx, job.ID, index, data, MaxJobLogBytes); err != nil {
		switch {
		case errors.Is(err, stores.ErrJobLogCapped):
			return job.LogChunks, fmt.Errorf("%w: job log exceeds %d MB", ErrTooLarge, MaxJobLogBytes>>20)
		case errors.Is(err, stores.ErrJobLogConflict):
			return job.LogChunks, fmt.Errorf("%w: expected log index %d", ErrConflict, job.LogChunks)
		}
		return job.LogChunks, err
	}
	return index + 1, nil
}

// CompleteTask stores the conclusion and moves the run forward
func (m *Manager) CompleteTask(ctx context.Context, runner *db.ActionsRunner, jobID, conclusion string, steps []*v1.JobStep, outputs, annotations string) error {
	job, run, repo, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return err
	}
	if job.Status != StatusRunning {
		return fmt.Errorf("%w: job already finished", ErrConflict)
	}
	switch conclusion {
	case ConclusionSuccess, ConclusionFailure, ConclusionCancelled, ConclusionSkipped, ConclusionTimedOut, ConclusionNeutral:
	default:
		conclusion = ConclusionFailure
	}
	if m.cancelPending(ctx, job) && conclusion != ConclusionSuccess {
		conclusion = ConclusionCancelled
	}
	now := time.Now().UTC()
	cols := map[string]any{"status": StatusCompleted, "conclusion": conclusion, "finished_at": now, "token_id": ""}
	if steps != nil {
		cols["steps"] = encodeSteps(steps)
	}
	if outputs != "" && json.Valid([]byte(outputs)) {
		cols["outputs"] = outputs
		job.Outputs = outputs
	}
	if annotations != "" && json.Valid([]byte(annotations)) {
		cols["annotations"] = annotations
	}
	if err := m.store.UpdateActionsJobColumns(ctx, job.ID, cols); err != nil {
		return err
	}
	job.Status, job.Conclusion, job.FinishedAt = StatusCompleted, conclusion, &now
	m.revokeJobToken(ctx, job)
	m.clearCancel(job.ID)
	m.compactJobLogs(ctx, job)
	m.writeCommitStatus(ctx, repo, run, job, conclusion)
	if conclusion == ConclusionFailure || conclusion == ConclusionTimedOut {
		m.failFast(ctx, repo, run, job)
	}
	if fresh, _ := m.store.GetActionsRunByID(ctx, run.ID); fresh != nil {
		run = fresh
	}
	m.advanceRun(ctx, repo, run)
	return nil
}

// Steps as stored on the job row
type storedStep struct {
	Number     int32      `json:"number"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Conclusion string     `json:"conclusion,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func encodeSteps(steps []*v1.JobStep) string {
	out := make([]storedStep, 0, len(steps))
	for _, s := range steps {
		ss := storedStep{Number: s.GetNumber(), Name: s.GetName(), Status: StatusFromProto(s.GetStatus()), Conclusion: ConclusionFromProto(s.GetConclusion())}
		if s.GetStartedAt() != nil {
			t := s.GetStartedAt().AsTime()
			ss.StartedAt = &t
		}
		if s.GetFinishedAt() != nil {
			t := s.GetFinishedAt().AsTime()
			ss.FinishedAt = &t
		}
		out = append(out, ss)
	}
	return mustJSON(out)
}

// DecodeSteps reads stored steps back into proto form
func DecodeSteps(raw string) []*v1.JobStep {
	var stored []storedStep
	if raw == "" || json.Unmarshal([]byte(raw), &stored) != nil {
		return nil
	}
	out := make([]*v1.JobStep, 0, len(stored))
	for _, s := range stored {
		st := &v1.JobStep{Number: s.Number, Name: s.Name, Status: ProtoStatus(s.Status), Conclusion: ProtoConclusion(s.Conclusion)}
		if s.StartedAt != nil {
			st.StartedAt = timestampOf(*s.StartedAt)
		}
		if s.FinishedAt != nil {
			st.FinishedAt = timestampOf(*s.FinishedAt)
		}
		out = append(out, st)
	}
	return out
}
