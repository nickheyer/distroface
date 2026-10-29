package actions

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/db"
)

// Mutex shared by the goroutines advancing one run
type runLock struct {
	mu   sync.Mutex
	held int
}

// Blocks until the run is free, the result releases it
func (m *Manager) lockRun(id string) func() {
	m.runMu.Lock()
	if m.runLocks == nil {
		m.runLocks = map[string]*runLock{}
	}
	l := m.runLocks[id]
	if l == nil {
		l = &runLock{}
		m.runLocks[id] = l
	}
	l.held++
	m.runMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		m.runMu.Lock()
		l.held--
		if l.held == 0 {
			delete(m.runLocks, id)
		}
		m.runMu.Unlock()
	}
}

// Queues waiting jobs whose needs finished, skips those whose if is false
func (m *Manager) advanceRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) {
	unlock := m.lockRun(run.ID)
	defer unlock()
	// A concurrent advance may have moved the run meanwhile
	if fresh, _ := m.store.GetActionsRunByID(ctx, run.ID); fresh != nil {
		run.Status, run.Conclusion, run.StartedAt, run.FinishedAt = fresh.Status, fresh.Conclusion, fresh.StartedAt, fresh.FinishedAt
	}
	if run.Status == StatusWaiting || run.Status == StatusCompleted {
		return
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return
	}
	p := m.planInput(ctx, repo, run)
	queued := false
	for changed := true; changed; {
		changed = false
		byID := make(map[string]*db.ActionsJob, len(jobs))
		for _, j := range jobs {
			byID[j.ID] = j
		}
		for _, j := range jobs {
			if j.Status != StatusWaiting {
				continue
			}
			if j.Error != "" {
				m.failJob(ctx, repo, run, j, ConclusionFailure, j.Error)
				changed = true
				continue
			}
			if j.ParentJobID != "" {
				parent := byID[j.ParentJobID]
				if parent == nil {
					m.failJob(ctx, repo, run, j, ConclusionFailure, "the calling job is gone")
					changed = true
					continue
				}
				if !parent.IsExpanded {
					continue
				}
			}
			if j.IsReusableCaller && j.IsExpanded {
				children := childrenOf(j, jobs)
				if !allCompleted(children) {
					continue
				}
				m.completeCaller(ctx, p, j, children)
				changed = true
				continue
			}
			allDone, allSucceed, missing := needsState(j, jobs)
			if missing != "" {
				m.failJob(ctx, repo, run, j, ConclusionFailure, "needs names an unknown job "+missing)
				changed = true
				continue
			}
			if !allDone {
				continue
			}
			results := needResults(j, jobs)
			// The condition decides before any matrix reads the needs
			ok, err := m.evaluateJobIf(ctx, p, j, results, allSucceed)
			if err != nil {
				m.failJob(ctx, repo, run, j, ConclusionFailure, fmt.Sprintf("if: %v", err))
				changed = true
				continue
			}
			if !ok {
				m.skipJob(ctx, repo, run, j)
				changed = true
				continue
			}
			if j.Deferred {
				fresh, err := m.expandDeferred(ctx, m.scoped(ctx, p, j), j, results)
				if err != nil {
					m.failJob(ctx, repo, run, j, ConclusionFailure, fmt.Sprintf("matrix: %v", err))
					changed = true
					continue
				}
				// New rows exist, start the pass over
				jobs, changed = fresh, true
				break
			}
			if j.IsReusableCaller {
				if err := m.expandCaller(ctx, p, j, results); err != nil {
					m.failJob(ctx, repo, run, j, ConclusionFailure, fmt.Sprintf("%s: %v", j.CallUses, err))
					changed = true
					continue
				}
				if fresh, err := m.store.ListActionsJobs(ctx, run.ID); err == nil {
					jobs = fresh
				}
				changed = true
				break
			}
			if err := m.resolveQueueTime(ctx, m.scoped(ctx, p, j), j, results); err != nil {
				m.failJob(ctx, repo, run, j, ConclusionFailure, err.Error())
				changed = true
				continue
			}
			runsOn := decodeStrings(j.RunsOn)
			if len(runsOn) == 0 {
				if d := strings.TrimSpace(m.Settings(ctx, repo).GetDefaultRunsOn()); d != "" {
					for _, l := range strings.Split(d, ",") {
						if l = strings.TrimSpace(l); l != "" {
							runsOn = append(runsOn, l)
						}
					}
				}
			}
			if !m.runnerRegisteredFor(ctx, repo, runsOn) {
				m.failJob(ctx, repo, run, j, ConclusionFailure, fmt.Sprintf("no registered runner matches runs-on %s for this repository, its organization, or the instance", strings.Join(runsOn, ", ")))
				changed = true
				continue
			}
			j.Status, j.RunsOn = StatusQueued, mustJSON(orEmpty(runsOn))
			_ = m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{"status": StatusQueued, "runs_on": j.RunsOn})
			m.writeCommitStatus(ctx, repo, run, j, "pending")
			if j.ConcurrencyCancel && j.Concurrency != "" {
				m.cancelJobGroup(ctx, repo, j)
			}
			queued, changed = true, true
		}
	}
	if allCompleted(jobs) {
		m.completeRun(ctx, repo, run, jobs)
		return
	}
	if queued {
		m.notifyRunners()
	}
}

// Job level concurrency that waited for needs resolves at queue time
func (m *Manager) resolveQueueTime(ctx context.Context, p *planInput, j *db.ActionsJob, results map[string]*jobparser.JobResult) error {
	if j.ConcurrencyEvaluated {
		return nil
	}
	jobID, parsed, err := parseJobPayload(j.WorkflowPayload)
	if err != nil {
		return err
	}
	if parsed.RawConcurrency == nil {
		return nil
	}
	group, cancel, err := jobparser.EvaluateConcurrency(parsed.RawConcurrency, jobID, parsed, p.gitCtx, results, p.vars, p.inputs)
	if err != nil {
		return fmt.Errorf("concurrency: %w", err)
	}
	j.Concurrency, j.ConcurrencyCancel, j.ConcurrencyEvaluated = strings.TrimSpace(group), cancel, true
	return m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{"concurrency": j.Concurrency, "concurrency_cancel": cancel, "concurrency_evaluated": true})
}

// Whether a job's if holds, an empty if means every need succeeded
func (m *Manager) evaluateJobIf(ctx context.Context, p *planInput, j *db.ActionsJob, results map[string]*jobparser.JobResult, allSucceed bool) (ok bool, err error) {
	defer recoverPlanning(&err)
	jobID, parsed, err := parseJobPayload(j.WorkflowPayload)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(parsed.If.Value) == "" {
		return allSucceed, nil
	}
	ev := m.scoped(ctx, p, j).evaluator(jobID, parsed, matrixOf(parsed), results)
	return ev.EvaluateBool(parsed.If.Value)
}

// Marks a job skipped before any runner saw it
func (m *Manager) skipJob(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, j *db.ActionsJob) {
	now := time.Now().UTC()
	j.Status, j.Conclusion, j.FinishedAt = StatusCompleted, ConclusionSkipped, &now
	_ = m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{"status": StatusCompleted, "conclusion": ConclusionSkipped, "finished_at": now})
	m.writeCommitStatus(ctx, repo, run, j, ConclusionSkipped)
}

// A caller finishes with its children, exposing the called workflow's outputs
func (m *Manager) completeCaller(ctx context.Context, p *planInput, caller *db.ActionsJob, children []*db.ActionsJob) {
	conclusion := ConclusionSuccess
	allSkipped := len(children) > 0
	for _, c := range children {
		ec := effectiveConclusion(c)
		if ec == ConclusionSkipped {
			continue
		}
		allSkipped = false
		if rankConclusion(ec) > rankConclusion(conclusion) {
			conclusion = ec
		}
	}
	if conclusion == ConclusionTimedOut {
		conclusion = ConclusionFailure
	}
	if allSkipped {
		conclusion = ConclusionSkipped
	}
	now := time.Now().UTC()
	cols := map[string]any{"status": StatusCompleted, "conclusion": conclusion, "finished_at": now}
	if caller.StartedAt == nil {
		cols["started_at"] = now
	}
	outputs, err := m.callerOutputs(ctx, p, caller, children)
	if err != nil {
		cols["conclusion"] = ConclusionFailure
		cols["annotations"] = mustJSON([]map[string]any{{"level": "error", "message": "outputs: " + err.Error()}})
		m.noteJobLog(ctx, caller, "outputs: "+err.Error())
	} else {
		cols["outputs"] = mustJSON(outputs)
	}
	caller.Status, caller.Conclusion, caller.FinishedAt = StatusCompleted, cols["conclusion"].(string), &now
	caller.Outputs = mustJSON(outputs)
	_ = m.store.UpdateActionsJobColumns(ctx, caller.ID, cols)
}

func childrenOf(caller *db.ActionsJob, jobs []*db.ActionsJob) []*db.ActionsJob {
	var out []*db.ActionsJob
	for _, j := range jobs {
		if j.ParentJobID == caller.ID {
			out = append(out, j)
		}
	}
	return out
}

func allCompleted(jobs []*db.ActionsJob) bool {
	for _, j := range jobs {
		if j.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// Rows a need names, siblings under the same caller
func needRows(need string, j *db.ActionsJob, jobs []*db.ActionsJob) []*db.ActionsJob {
	var out []*db.ActionsJob
	for _, o := range jobs {
		if o.ParentJobID == j.ParentJobID && o.WorkflowJobID == need {
			out = append(out, o)
		}
	}
	return out
}

// Whether every need finished and whether all of them succeeded
func needsState(j *db.ActionsJob, jobs []*db.ActionsJob) (allDone, allSucceed bool, missing string) {
	allDone, allSucceed = true, true
	for _, need := range decodeStrings(j.Needs) {
		rows := needRows(need, j, jobs)
		if len(rows) == 0 {
			return false, false, need
		}
		for _, r := range rows {
			if r.Status != StatusCompleted {
				allDone = false
				continue
			}
			if effectiveConclusion(r) != ConclusionSuccess {
				allSucceed = false
			}
		}
	}
	return allDone, allSucceed, ""
}

// Need results and outputs plus the job's own entry
func needResults(j *db.ActionsJob, jobs []*db.ActionsJob) map[string]*jobparser.JobResult {
	needs := decodeStrings(j.Needs)
	out := map[string]*jobparser.JobResult{j.WorkflowJobID: {ID: j.WorkflowJobID, Needs: orEmpty(needs)}}
	for _, need := range needs {
		rows := needRows(need, j, jobs)
		r := &jobparser.JobResult{ID: need, Result: ConclusionSuccess, Outputs: map[string]string{}}
		for _, row := range rows {
			if row.Status != StatusCompleted {
				continue
			}
			if c := effectiveConclusion(row); c != ConclusionSuccess && rankConclusion(c) > rankConclusion(r.Result) {
				r.Result = c
			}
			for k, v := range rowOutputs(row) {
				r.Outputs[k] = v
			}
		}
		if r.Result == ConclusionTimedOut {
			r.Result = ConclusionFailure
		}
		out[need] = r
	}
	return out
}

// Failure outranks cancelled outranks skipped outranks success
func rankConclusion(c string) int {
	switch c {
	case ConclusionFailure, ConclusionTimedOut:
		return 3
	case ConclusionCancelled:
		return 2
	case ConclusionSkipped:
		return 1
	}
	return 0
}

// Jobs marked continue-on-error count as success downstream
func effectiveConclusion(j *db.ActionsJob) string {
	c := j.Conclusion
	if c == ConclusionNeutral {
		return ConclusionSuccess
	}
	if (c == ConclusionFailure || c == ConclusionTimedOut) && j.ContinueOnError {
		return ConclusionSuccess
	}
	return c
}
