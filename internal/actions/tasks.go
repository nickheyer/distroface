package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestampOf(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}

// Loads a job and refuses ones not assigned to the runner
func (m *Manager) runnerOwnedJob(ctx context.Context, runner *db.ActionsRunner, jobID string) (*db.ActionsJob, error) {
	job, err := m.store.GetActionsJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("%w: job not found", ErrNotFound)
	}
	if job.RunnerID != runner.ID {
		return nil, fmt.Errorf("%w: job is not assigned to this runner", ErrDenied)
	}
	return job, nil
}

// The job with its run and repository for handlers that need them
func (m *Manager) runnerJob(ctx context.Context, runner *db.ActionsRunner, jobID string) (*db.ActionsJob, *db.ActionsRun, *db.GitRepository, error) {
	job, err := m.runnerOwnedJob(ctx, runner, jobID)
	if err != nil {
		return nil, nil, nil, err
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

// Pollers rescan an unchanged queue no more often than this
const claimRescan = 10 * time.Second

// FetchTask long polls, rescanning only once the queue moved
func (m *Manager) FetchTask(ctx context.Context, runner *db.ActionsRunner, wait time.Duration) (*v1.Task, error) {
	if wait <= 0 || wait > maxTaskWait {
		wait = maxTaskWait
	}
	deadline := time.Now().Add(wait)
	var scannedGen uint64
	var scannedAt time.Time
	rejected := map[string]bool{}
	for {
		// Taken before the scan so a wake during it is never missed
		wake := m.wakeChan()
		gen := m.queueGeneration()
		if scannedAt.IsZero() || gen != scannedGen || time.Since(scannedAt) >= claimRescan {
			if gen != scannedGen {
				// Reruns resolve labels again, mismatches last one generation
				clear(rejected)
			}
			task, err := m.tryClaim(ctx, runner, rejected)
			if err != nil {
				return nil, err
			}
			if task != nil {
				return task, nil
			}
			scannedGen, scannedAt = gen, time.Now()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		if remaining > claimRescan {
			remaining = claimRescan
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		case <-time.After(remaining):
		}
	}
}

// Queued jobs examined per poll before giving up, in pages
const (
	claimPageSize = 100
	claimMaxPages = 20
	// Mismatches excluded in sql up to this many
	claimMaxExcluded = 500
)

// Claims the oldest queued job matching scope, labels, and concurrency
func (m *Manager) tryClaim(ctx context.Context, runner *db.ActionsRunner, rejected map[string]bool) (*v1.Task, error) {
	runnerLabels := decodeStrings(runner.Labels)
	var job *db.ActionsJob
	var run *db.ActionsRun
	var repo *db.GitRepository
	var exclude []string
	if len(rejected) <= claimMaxExcluded {
		for id := range rejected {
			exclude = append(exclude, id)
		}
	}
	// Scope is filtered in sql, labels in memory, so unservable jobs never block the queue
	for page := 0; page < claimMaxPages && job == nil; page++ {
		jobs, err := m.store.ListQueuedActionsJobsInScope(ctx, runner.ScopeKind, runner.ScopeID, exclude, claimPageSize, page*claimPageSize)
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
		var runGroups, jobGroups []string
		for _, r := range runs {
			repoIDs = append(repoIDs, r.RepoID)
			if r.Concurrency != "" {
				runGroups = append(runGroups, r.Concurrency)
			}
		}
		for _, j := range jobs {
			if j.Concurrency != "" {
				jobGroups = append(jobGroups, j.Concurrency)
			}
		}
		repos, err := m.store.GetGitRepositoriesByIDs(ctx, repoIDs)
		if err != nil {
			return nil, err
		}
		// Two queries answer every group check on the page
		busyRuns, err := m.store.RunningRunsInGroups(ctx, runGroups)
		if err != nil {
			return nil, err
		}
		busyJobs, err := m.store.RunningJobsInGroups(ctx, jobGroups)
		if err != nil {
			return nil, err
		}
		var candidates []stores.ClaimCandidate
		for _, j := range jobs {
			if rejected[j.ID] {
				continue
			}
			r := runs[j.RunID]
			if r == nil || repos[r.RepoID] == nil {
				continue
			}
			if !labelsSubset(decodeStrings(j.RunsOn), runnerLabels) {
				rejected[j.ID] = true
				continue
			}
			// Interactive jobs need a runner that speaks the exec protocol
			if j.Interactive && !RunnerHasCapability(runner, CapabilityExec) {
				rejected[j.ID] = true
				continue
			}
			if r.Concurrency != "" && groupBusy(busyRuns, r.RepoID, r.Concurrency, r.ID) {
				continue
			}
			if j.Concurrency != "" && groupBusy(busyJobs, r.RepoID, j.Concurrency, j.ID) {
				continue
			}
			candidates = append(candidates, stores.ClaimCandidate{ID: j.ID, RepoID: r.RepoID, RunGroup: r.Concurrency, JobGroup: j.Concurrency})
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
	task, err := m.buildTask(ctx, repo, run, job)
	if err != nil {
		m.log.Error("actions: build task for job %s: %v", job.ID, err)
		m.failJob(ctx, repo, run, job, ConclusionFailure, "server could not prepare the job: "+err.Error())
		m.advanceRun(ctx, repo, run)
		return nil, nil
	}
	return task, nil
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

// Another group member in the same repository is running
func groupBusy(live []stores.LiveGroup, repoID, group, selfID string) bool {
	for _, g := range live {
		if g.RepoID == repoID && g.Group == group && g.ID != selfID {
			return true
		}
	}
	return false
}

// Everything the runner needs to execute one job
func (m *Manager) buildTask(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob) (*v1.Task, error) {
	if strings.TrimSpace(job.WorkflowPayload) == "" {
		return nil, errors.New("job has no workflow payload")
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	secrets, err := m.jobSecrets(ctx, repo, run, job)
	if err != nil {
		return nil, err
	}
	token, err := m.issueJobToken(ctx, repo, run, job, run.FromFork || job.ReadOnly)
	if err != nil {
		return nil, err
	}
	if token != "" {
		secrets["GITHUB_TOKEN"] = token
		secrets["DISTROFACE_TOKEN"] = token
	}
	vars, err := m.repoVariables(ctx, repo, m.repoOrgID(ctx, repo))
	if err != nil {
		return nil, err
	}
	timeout := job.TimeoutMinutes
	if timeout <= 0 {
		timeout = defaultJobTimeout
	}
	runtime, err := m.runtimeToken(job.ID, run.ID, timeout)
	if err != nil {
		return nil, err
	}
	gitCtx := m.githubContext(ctx, repo, run, job)
	gitCtx["token"] = token
	needs := map[string]*v1.TaskNeed{}
	for id, r := range needResults(job, jobs) {
		if id == job.WorkflowJobID {
			continue
		}
		needs[id] = &v1.TaskNeed{Outputs: r.Outputs, Result: ProtoConclusion(r.Result)}
	}
	return &v1.Task{
		JobId: job.ID, RunId: run.ID, RepoFullName: repo.Namespace + "/" + repo.Name, JobName: job.Name,
		WorkflowPayload:   []byte(job.WorkflowPayload),
		Context:           mustJSON(gitCtx),
		Secrets:           secrets,
		Variables:         vars,
		Needs:             needs,
		Token:             token,
		RuntimeToken:      runtime,
		TimeoutMinutes:    int32(timeout),
		CloneUrl:          m.git.HTTPCloneURL(ctx, repo, false),
		ServerUrl:         strings.TrimRight(m.git.BaseURL(ctx), "/"),
		RunsOn:            decodeStrings(job.RunsOn),
		Interactive:       job.Interactive,
		DefaultActionsUrl: m.defaultActionsURL(ctx),
	}, nil
}

// Scoped api token the runner uses for checkout and publishing
func (m *Manager) issueJobToken(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob, readOnly bool) (string, error) {
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
		Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO, ID: repo.Namespace + "/" + repo.Name, ReadOnly: readOnly,
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
	m.forgetJobStatus(job.ID)
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
	job, err := m.runnerOwnedJob(ctx, runner, jobID)
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
		owned, err := m.store.UpdateActionsJobOwned(ctx, job.ID, runner.ID, cols)
		if err != nil {
			return false, err
		}
		if !owned {
			return true, nil
		}
	}
	return m.cancelPending(ctx, job), nil
}

// AppendTaskLog stores one chunk and answers the next expected index
func (m *Manager) AppendTaskLog(ctx context.Context, runner *db.ActionsRunner, jobID string, index int, data []byte) (int, error) {
	job, err := m.runnerOwnedJob(ctx, runner, jobID)
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
	if err := m.store.AppendActionsJobLog(ctx, job.ID, runner.ID, index, data, MaxJobLogBytes); err != nil {
		switch {
		case errors.Is(err, stores.ErrJobLogNotOwned):
			return job.LogChunks, fmt.Errorf("%w: job is not running", ErrConflict)
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
	owned, err := m.store.UpdateActionsJobOwned(ctx, job.ID, runner.ID, cols)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("%w: job already finished", ErrConflict)
	}
	job.Status, job.Conclusion, job.FinishedAt = StatusCompleted, conclusion, &now
	m.revokeJobToken(ctx, job)
	m.clearCancel(job.ID)
	m.closeJobExecs(ctx, job, conclusionForExecs(conclusion), "the command workspace ended")
	m.compactJobLogs(ctx, job)
	m.writeCommitStatus(ctx, repo, run, job, conclusion)
	if conclusion == ConclusionFailure || conclusion == ConclusionTimedOut {
		m.failFast(ctx, repo, run, job)
	}
	if fresh, _ := m.store.GetActionsRunByID(ctx, run.ID); fresh != nil {
		run = fresh
	}
	m.advanceRun(ctx, repo, run)
	// A finished job may free a concurrency group
	m.notifyRunners()
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

// Open commands share the job's fate, success means unrun
func conclusionForExecs(jobConclusion string) string {
	switch jobConclusion {
	case ConclusionCancelled, ConclusionTimedOut:
		return jobConclusion
	}
	return ConclusionFailure
}
