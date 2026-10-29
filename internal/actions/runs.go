package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/webhook"
)

// Creates a run per matching enabled workflow file at the commit
func (m *Manager) startRuns(ctx context.Context, repo *db.GitRepository, r *gitrepo.Repo, commit plumbing.Hash, in *triggerInput) {
	files := loadWorkflowFiles(ctx, r, commit)
	disabled, _ := m.store.GetDisabledWorkflows(ctx, repo.ID)
	for _, f := range files {
		if f.Err != nil || disabled[f.Path] {
			continue
		}
		if in.OnlyPath != "" && f.Path != in.OnlyPath {
			continue
		}
		if !matchTrigger(f, in) {
			continue
		}
		in.FileCommit = commit
		if _, err := m.createRun(ctx, repo, f, in); err != nil {
			m.log.Error("actions: create run %s/%s %s: %v", repo.Namespace, repo.Name, f.Path, err)
		}
	}
}

// Marks a job finished without a runner
func (m *Manager) failJob(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, j *db.ActionsJob, conclusion, note string) {
	now := time.Now().UTC()
	cols := map[string]any{"status": StatusCompleted, "conclusion": conclusion, "finished_at": now}
	if note != "" {
		var ann []any
		_ = json.Unmarshal([]byte(j.Annotations), &ann)
		ann = append(ann, map[string]any{"level": "error", "message": note})
		j.Annotations = mustJSON(ann)
		cols["annotations"] = j.Annotations
	}
	if j.StartedAt == nil {
		cols["started_at"] = now
		j.StartedAt = &now
	}
	j.Status, j.Conclusion, j.FinishedAt = StatusCompleted, conclusion, &now
	_ = m.store.UpdateActionsJobColumns(ctx, j.ID, cols)
	if note != "" {
		m.noteJobLog(ctx, j, note)
	}
	m.revokeJobToken(ctx, j)
	m.clearCancel(j.ID)
	m.closeJobExecs(ctx, j, conclusion, "the command workspace ended: "+note)
	m.writeCommitStatus(ctx, repo, run, j, conclusion)
	// Children of a failed caller never start
	if j.IsReusableCaller {
		if children, err := m.store.ListActionsJobsByParent(ctx, j.ID); err == nil {
			for _, c := range children {
				if c.Status == StatusWaiting || c.Status == StatusQueued {
					m.failJob(ctx, repo, run, c, ConclusionCancelled, "the calling job "+j.Name+" ended")
				}
			}
		}
	}
}

// Transcript line for a job the server finished itself
func (m *Manager) noteJobLog(ctx context.Context, j *db.ActionsJob, note string) {
	line := "##[error]" + strings.ReplaceAll(note, "\n", " ") + "\n"
	if err := m.store.PutActionsJobLog(ctx, j.ID, []byte(line)); err != nil {
		m.log.Warn("actions: note job log %s: %v", j.ID, err)
	}
	j.LogChunks++
	j.LogBytes += int64(len(line))
}

// Finishes the run once every job completed
func (m *Manager) completeRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, jobs []*db.ActionsJob) {
	if run.Status == StatusCompleted {
		return
	}
	conclusion := ConclusionSuccess
	allSkipped := len(jobs) > 0
	for _, j := range jobs {
		// Children report through their caller
		if j.ParentJobID != "" {
			continue
		}
		c := effectiveConclusion(j)
		if c != ConclusionSkipped {
			allSkipped = false
			if rankConclusion(c) > rankConclusion(conclusion) {
				conclusion = c
			}
		}
		m.clearCancel(j.ID)
	}
	if conclusion == ConclusionTimedOut {
		conclusion = ConclusionFailure
	}
	if allSkipped {
		conclusion = ConclusionSkipped
	}
	if run.Conclusion == ConclusionCancelled {
		conclusion = ConclusionCancelled
	}
	now := time.Now().UTC()
	cols := map[string]any{"status": StatusCompleted, "conclusion": conclusion, "finished_at": now}
	if run.StartedAt == nil {
		cols["started_at"] = now
		run.StartedAt = &now
	}
	run.Status, run.Conclusion, run.FinishedAt = StatusCompleted, conclusion, &now
	if err := m.store.UpdateActionsRunColumns(ctx, run.ID, cols); err != nil {
		m.log.Error("actions: complete run %s: %v", run.ID, err)
		return
	}
	m.afterRunCompleted(ctx, repo, run)
}

// Webhook, notification, and auto merge retry after a run ends
func (m *Manager) afterRunCompleted(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) {
	// Agent command workspaces end quietly, the session reports for them
	if run.AgentSessionID != "" {
		return
	}
	url := m.runURL(ctx, repo, run.ID)
	actor := m.username(ctx, run.TriggerUserID)
	m.git.DispatchGit(ctx, "git_workflow_run", repo, webhook.GitPayload{
		Action: "completed", Sender: webhook.Sender{Username: actor},
		WorkflowRun: &webhook.WorkflowRunPayload{ID: run.ID, Name: run.WorkflowName, Number: run.Number, Event: run.Event, Status: run.Status, Conclusion: run.Conclusion, URL: url},
	})
	if m.forge != nil {
		var recipients []string
		if run.TriggerUserID != "" {
			recipients = []string{run.TriggerUserID}
		}
		m.forge.Notify(ctx, forge.Notification{
			Repo: repo, Kind: forge.NotifyWorkflowRun, SHA: run.SHA,
			Subject: fmt.Sprintf("%s #%d %s", run.WorkflowName, run.Number, run.Conclusion),
			Text:    fmt.Sprintf("Workflow %s run #%d on %s finished with %s", run.WorkflowName, run.Number, plumbing.ReferenceName(run.Ref).Short(), run.Conclusion),
			URL:     url, Recipients: recipients,
		})
	}
	if m.runCompleted != nil {
		m.runCompleted(ctx, repo, run)
	}
}

// Writes the commit status row a job conclusion maps onto
func (m *Manager) writeCommitStatus(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, j *db.ActionsJob, conclusion string) {
	// Command workspaces are not checks, they never mark a commit
	if run.SHA == "" || run.AgentSessionID != "" {
		return
	}
	state, desc := "pending", "Queued"
	switch conclusion {
	case ConclusionSuccess, ConclusionNeutral:
		state, desc = "success", "Succeeded"
	case ConclusionSkipped:
		state, desc = "success", "Skipped"
	case ConclusionFailure:
		state, desc = "failure", "Failed"
	case ConclusionTimedOut:
		state, desc = "failure", "Timed out"
	case ConclusionCancelled:
		state, desc = "error", "Cancelled"
	}
	creator := run.TriggerUserID
	if creator == "" {
		creator = "system"
	}
	err := m.store.CreateGitCommitStatus(ctx, &db.GitCommitStatus{
		RepoID: repo.ID, SHA: run.SHA, Context: run.WorkflowName + " / " + j.Name, State: state,
		TargetURL: m.jobURL(ctx, repo, run.ID, j.ID), Description: desc, CreatorID: creator,
	})
	if err != nil {
		m.log.Error("actions: commit status %s: %v", run.SHA, err)
		return
	}
	if m.forge != nil && state != "pending" {
		go m.forge.RetryAutoMerge(context.WithoutCancel(ctx), repo, run.SHA)
	}
}

// ── User facing run operations ───────────────────────────────────────────

// CancelRun stops queued jobs now and asks runners to stop running ones
func (m *Manager) CancelRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) error {
	if run.Status == StatusCompleted {
		return fmt.Errorf("%w: run already finished", ErrConflict)
	}
	m.cancelRun(ctx, repo, run)
	return nil
}

func (m *Manager) cancelRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) {
	unlock := m.lockRun(run.ID)
	defer unlock()
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return
	}
	running := false
	for _, j := range jobs {
		switch j.Status {
		case StatusWaiting, StatusQueued:
			m.failJob(ctx, repo, run, j, ConclusionCancelled, "")
		case StatusRunning:
			m.requestCancel(j.ID)
			running = true
		}
	}
	run.Conclusion = ConclusionCancelled
	if running {
		_ = m.store.UpdateActionsRunColumns(ctx, run.ID, map[string]any{"conclusion": ConclusionCancelled})
		return
	}
	if fresh, err := m.store.ListActionsJobs(ctx, run.ID); err == nil {
		jobs = fresh
	}
	m.completeRun(ctx, repo, run, jobs)
}

// ApproveRun releases a fork pull request run
func (m *Manager) ApproveRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) error {
	if !run.NeedsApproval || run.Status != StatusWaiting {
		return fmt.Errorf("%w: run does not need approval", ErrConflict)
	}
	run.NeedsApproval, run.Status = false, StatusQueued
	if err := m.store.UpdateActionsRunColumns(ctx, run.ID, map[string]any{"needs_approval": false, "status": StatusQueued}); err != nil {
		return err
	}
	m.advanceRun(ctx, repo, run)
	m.notifyRunners()
	return nil
}

// Columns that return a job to the queue for another attempt
func resetJobColumns(attempt int) map[string]any {
	return map[string]any{
		"status": StatusWaiting, "conclusion": "", "runner_id": "", "attempt": attempt,
		"steps": "[]", "outputs": "{}", "annotations": "[]", "token_id": "", "log_chunks": 0, "log_bytes": 0, "log_digest": "",
		"started_at": nil, "finished_at": nil,
	}
}

// RerunRun resets failed or every job and queues the run again
func (m *Manager) RerunRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, failedOnly bool) error {
	if run.AgentSessionID != "" {
		return fmt.Errorf("%w: agent session runs cannot be rerun, continue the session instead", ErrConflict)
	}
	if run.Status != StatusCompleted {
		return fmt.Errorf("%w: run is still active", ErrConflict)
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return err
	}
	attempt := 1
	legacy := false
	for _, j := range jobs {
		attempt = max(attempt, j.Attempt+1)
		if j.WorkflowPayload == "" && j.Error == "" {
			legacy = true
		}
	}
	if !failedOnly || legacy {
		if err := m.replanRun(ctx, repo, run, jobs, attempt); err != nil {
			return err
		}
	} else if err := m.resetFailedJobs(ctx, jobs, attempt); err != nil {
		return err
	}
	run.Status, run.Conclusion, run.StartedAt, run.FinishedAt = StatusQueued, "", nil, nil
	if err := m.store.UpdateActionsRunColumns(ctx, run.ID, map[string]any{"status": StatusQueued, "conclusion": "", "started_at": nil, "finished_at": nil}); err != nil {
		return err
	}
	m.advanceRun(ctx, repo, run)
	m.notifyRunners()
	return nil
}

// Plans the run again from its stored source, the number stays
func (m *Manager) replanRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, jobs []*db.ActionsJob, attempt int) error {
	for _, j := range jobs {
		m.dropJobLogs(ctx, j)
		m.revokeJobToken(ctx, j)
	}
	if err := m.store.DeleteActionsJobs(ctx, run.ID); err != nil {
		return err
	}
	p := m.planInput(ctx, repo, run)
	rows := m.planJobs(p, []byte(run.Workflow), nil)
	for _, r := range rows {
		r.RunID, r.Attempt, r.Interactive = run.ID, attempt, run.AgentSessionID != ""
	}
	return m.store.CreateActionsJobs(ctx, rows)
}

// Returns failed jobs and whatever depends on them to the queue
func (m *Manager) resetFailedJobs(ctx context.Context, jobs []*db.ActionsJob, attempt int) error {
	byID := make(map[string]*db.ActionsJob, len(jobs))
	reset := map[string]bool{}
	// Skipped by condition stays skipped unless a need resets
	for _, j := range jobs {
		byID[j.ID] = j
		switch j.Conclusion {
		case ConclusionFailure, ConclusionCancelled, ConclusionTimedOut:
			if !j.IsReusableCaller || !j.IsExpanded {
				reset[j.ID] = true
			}
		}
	}
	// Dependents of a reset job need fresh outputs too
	for changed := true; changed; {
		changed = false
		for _, j := range jobs {
			if reset[j.ID] {
				continue
			}
			for _, need := range decodeStrings(j.Needs) {
				for _, other := range needRows(need, j, jobs) {
					if reset[other.ID] {
						reset[j.ID], changed = true, true
					}
				}
			}
			if j.ParentJobID != "" && reset[j.ParentJobID] && !byID[j.ParentJobID].IsExpanded {
				reset[j.ID], changed = true, true
			}
		}
	}
	if len(reset) == 0 {
		return fmt.Errorf("%w: nothing to rerun", ErrConflict)
	}
	for _, j := range jobs {
		if !reset[j.ID] {
			continue
		}
		m.dropJobLogs(ctx, j)
		m.revokeJobToken(ctx, j)
		cols := resetJobColumns(attempt)
		if j.IsReusableCaller && !j.IsExpanded {
			// An unexpanded caller plans its children afresh
			if err := m.store.DeleteActionsJobsByParent(ctx, j.ID); err != nil {
				return err
			}
			cols["call_inputs"], cols["call_content"], cols["call_secrets"] = "{}", "", ""
		}
		if err := m.store.UpdateActionsJobColumns(ctx, j.ID, cols); err != nil {
			return err
		}
	}
	// Callers of reset children wait for them again and recompute outputs
	for _, j := range jobs {
		if !reset[j.ID] || j.ParentJobID == "" {
			continue
		}
		for cur := byID[j.ParentJobID]; cur != nil; cur = byID[cur.ParentJobID] {
			cols := map[string]any{"status": StatusWaiting, "conclusion": "", "outputs": "{}", "finished_at": nil, "attempt": attempt}
			if err := m.store.UpdateActionsJobColumns(ctx, cur.ID, cols); err != nil {
				return err
			}
		}
	}
	return nil
}

// Dispatch starts a workflow_dispatch run with validated inputs
func (m *Manager) Dispatch(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser, wfPath, ref string, inputs map[string]string) (*db.ActionsRun, error) {
	if !m.Enabled(ctx, repo) {
		return nil, ErrDisabled
	}
	if ref == "" {
		ref = repo.DefaultBranch
	}
	files, commit, err := m.LoadWorkflows(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	var file *WorkflowFile
	for _, f := range files {
		if f.Path == wfPath {
			file = f
		}
	}
	if file == nil {
		return nil, fmt.Errorf("%w: workflow %s not found at %s", ErrNotFound, wfPath, ref)
	}
	if file.Err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, file.Err)
	}
	if file.Event("workflow_dispatch") == nil {
		return nil, fmt.Errorf("%w: workflow has no workflow_dispatch trigger", ErrInvalid)
	}
	disabled, _ := m.store.GetDisabledWorkflows(ctx, repo.ID)
	if disabled[wfPath] {
		return nil, fmt.Errorf("%w: workflow is disabled", ErrConflict)
	}
	typed, err := ValidateDispatchInputs(file.DispatchInputs(), inputs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	fullRef := ref
	if !strings.HasPrefix(ref, "refs/") {
		rr, err := m.openRepo(repo)
		if err == nil {
			if !rr.TagHash(ref).IsZero() {
				fullRef = "refs/tags/" + ref
			} else {
				fullRef = "refs/heads/" + ref
			}
			rr.Close()
		}
	}
	// Event inputs stay strings, the inputs context stays typed
	payload := map[string]any{
		"inputs": inputStrings(typed), "ref": fullRef, "workflow": wfPath,
		"repository": m.repoPayload(ctx, repo), "sender": map[string]any{"login": actor.Username},
	}
	in := &triggerInput{Event: "workflow_dispatch", Ref: fullRef, SHA: commit.String(), FileCommit: commit, Actor: actor, Payload: payload, Inputs: typed, OnlyPath: wfPath}
	return m.createRun(ctx, repo, file, in)
}

// Reports whether a registered runner in scope carries every label
func (m *Manager) runnerRegisteredFor(ctx context.Context, repo *db.GitRepository, runsOn []string) bool {
	runners, err := m.store.ListActionsRunnersForRepo(ctx, repo.ID, m.repoOrgID(ctx, repo))
	if err != nil {
		return true
	}
	for _, r := range runners {
		if labelsSubset(runsOn, decodeStrings(r.Labels)) {
			return true
		}
	}
	return false
}

// Cancels the other live jobs of a job level concurrency group
func (m *Manager) cancelJobGroup(ctx context.Context, repo *db.GitRepository, j *db.ActionsJob) {
	live, err := m.store.ListLiveJobsInGroup(ctx, repo.ID, j.Concurrency, j.ID)
	if err != nil {
		return
	}
	touched := map[string]*db.ActionsRun{}
	for _, other := range live {
		run := touched[other.RunID]
		if run == nil {
			if run, _ = m.store.GetActionsRunByID(ctx, other.RunID); run == nil {
				continue
			}
			touched[other.RunID] = run
		}
		switch other.Status {
		case StatusWaiting, StatusQueued:
			m.failJob(ctx, repo, run, other, ConclusionCancelled, "cancelled by concurrency group "+j.Concurrency)
		case StatusRunning:
			m.requestCancel(other.ID)
		}
	}
	// Other runs advance outside this lock, never waiting on it
	for id, run := range touched {
		if id != j.RunID {
			go m.advanceRun(context.WithoutCancel(ctx), repo, run)
		}
	}
}

// Cancels the remaining legs of a matrix once one of them failed
func (m *Manager) failFast(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, failed *db.ActionsJob) {
	if !failed.FailFast || failed.ContinueOnError {
		return
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.ID == failed.ID || j.MatrixGroup != failed.MatrixGroup || j.ParentJobID != failed.ParentJobID {
			continue
		}
		switch j.Status {
		case StatusWaiting, StatusQueued:
			m.failJob(ctx, repo, run, j, ConclusionCancelled, "cancelled by fail-fast after "+failed.Name+" failed")
		case StatusRunning:
			m.requestCancel(j.ID)
		}
	}
}
