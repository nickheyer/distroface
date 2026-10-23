package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/expr"
)

// Resolved job stored on the row, enough to build a task later
type jobDefinition struct {
	Job          *Job              `json:"job"`
	WorkflowEnv  map[string]string `json:"workflow_env,omitempty"`
	MatrixOrder  []string          `json:"matrix_order,omitempty"`
	WorkflowName string            `json:"workflow_name,omitempty"`
	// Permissions grant neither contents nor packages write
	ReadOnly bool `json:"read_only,omitempty"`
	// Planning failure recorded at run creation, fails the job on advance
	Error string `json:"error,omitempty"`
	// Matrix awaits needs outputs, expanded on advance
	Deferred bool `json:"deferred,omitempty"`
}

func decodeDefinition(j *db.ActionsJob) (*jobDefinition, error) {
	var def jobDefinition
	if err := json.Unmarshal([]byte(j.Definition), &def); err != nil {
		return nil, err
	}
	if def.Job == nil {
		return nil, fmt.Errorf("job %s has no definition", j.ID)
	}
	return &def, nil
}

func decodeMatrix(j *db.ActionsJob) map[string]any {
	var m map[string]any
	if j.Matrix == "" || json.Unmarshal([]byte(j.Matrix), &m) != nil {
		return nil
	}
	return m
}

func decodeStrings(s string) []string {
	var out []string
	if s == "" || json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// ── Run creation ─────────────────────────────────────────────────────────

// Creates a run per matching enabled workflow file at the commit
func (m *Manager) startRuns(ctx context.Context, repo *db.GitRepository, r *gitrepo.Repo, commit plumbing.Hash, in *triggerInput) {
	files := loadWorkflowFiles(ctx, r, commit)
	disabled, _ := m.store.GetDisabledWorkflows(ctx, repo.ID)
	for _, f := range files {
		if f.Err != nil || f.Workflow == nil || disabled[f.Path] {
			continue
		}
		if in.OnlyPath != "" && f.Path != in.OnlyPath {
			continue
		}
		if !matchTrigger(f.Workflow, in) {
			continue
		}
		in.FileCommit = commit
		if _, err := m.createRun(ctx, repo, f, in); err != nil {
			m.log.Error("actions: create run %s/%s %s: %v", repo.Namespace, repo.Name, f.Path, err)
		}
	}
}

// CreateRun materializes one workflow file into run and job rows
func (m *Manager) createRun(ctx context.Context, repo *db.GitRepository, f *WorkflowFile, in *triggerInput) (*db.ActionsRun, error) {
	wf := f.Workflow
	number, err := m.store.NextGitCounter(ctx, repo.ID, "run_counter")
	if err != nil {
		return nil, err
	}
	name := wf.Name
	if name == "" {
		name = strings.TrimSuffix(path.Base(f.Path), path.Ext(f.Path))
	}
	run := &db.ActionsRun{
		ID: uuid.New().String(), RepoID: repo.ID, Number: number,
		WorkflowPath: f.Path, WorkflowName: name,
		Event: in.Event, Ref: in.Ref, SHA: in.SHA,
		Status: StatusQueued, NeedsApproval: in.NeedsApproval, FromFork: in.FromFork, PullNumber: in.PullNumber,
		Payload: mustJSON(in.Payload), Workflow: f.Source,
	}
	if in.Actor != nil && !in.Actor.IsAnonymous() {
		run.TriggerUserID = in.Actor.ID
	}
	if in.NeedsApproval {
		run.Status = StatusWaiting
	}
	if in.AgentSession != nil {
		run.AgentSessionID, run.AgentSlug = in.AgentSession.SessionID, in.AgentSession.AgentSlug
	}
	ectx := m.baseContext(ctx, repo, run, wf, in.Inputs)
	if wf.HasCalls() {
		wf = m.expandCalls(ctx, repo, f, in, wf, ectx)
	}
	if wf.Concurrency != nil {
		group, err := Interpolate(wf.Concurrency.Group, ectx)
		if err == nil && !HasDeferred(group) {
			run.Concurrency = strings.TrimSpace(group)
		}
	}
	jobs := m.planJobs(wf, ectx)
	run.Notes = mustJSON(orEmpty(wf.Notes))
	if in.AgentSession != nil {
		for _, j := range jobs {
			j.Interactive = true
		}
	}
	if err := m.store.CreateActionsRun(ctx, run, jobs); err != nil {
		return nil, err
	}
	actor, actorID := "system", ""
	if in.Actor != nil {
		actor, actorID = in.Actor.Username, in.Actor.ID
	}
	m.git.Audit(ctx, audit.Event{Action: "Actions/run", Resource: "actions", Outcome: audit.OutcomeSuccess,
		Detail: fmt.Sprintf("%s/%s#%d %s", repo.Namespace, repo.Name, run.Number, run.ID), Actor: actor, ActorID: actorID})

	if wf.Concurrency != nil && wf.Concurrency.CancelInProgress && run.Concurrency != "" {
		live, _ := m.store.ListLiveRunsInGroup(ctx, repo.ID, run.Concurrency, run.ID)
		for _, other := range live {
			m.cancelRun(ctx, repo, other)
		}
	}
	// Queued jobs wake the runners from inside advanceRun
	if run.Status != StatusWaiting {
		m.advanceRun(ctx, repo, run)
	}
	return run, nil
}

// Inlines reusable workflow calls, reading files through the git engine
func (m *Manager) expandCalls(ctx context.Context, repo *db.GitRepository, f *WorkflowFile, in *triggerInput, wf *Workflow, ectx *ExprContext) *Workflow {
	commit := in.FileCommit
	if commit.IsZero() && in.SHA != "" {
		if r, err := m.openRepo(repo); err == nil {
			if h, err := r.Resolve(in.SHA); err == nil {
				commit = h
			}
			r.Close()
		}
	}
	load, done := m.callLoader(ctx, repo, commit, in.Actor)
	defer done()
	return ExpandCalls(wf, callLocation(repo, f.Path, commit), load, ectx)
}

// Expands matrices into waiting rows, needs bound ones defer
func (m *Manager) planJobs(wf *Workflow, base *ExprContext) []*db.ActionsJob {
	var out []*db.ActionsJob
	for _, key := range wf.JobOrder {
		job := wf.Jobs[key]
		// Inlined jobs keep the env of the workflow they came from
		wenv := wf.Env
		if len(job.Calls) > 0 {
			wenv = job.WorkflowEnv
		}
		def := jobDefinition{Job: job, WorkflowEnv: wenv, WorkflowName: wf.Name, ReadOnly: job.Permissions.ReadOnly()}
		if job.Error != "" {
			def.Error = job.Error
			out = append(out, placeholderRow(job, key, def))
			continue
		}
		strategy, deferred, err := resolveStrategy(job.Strategy, base)
		if err == nil && deferred {
			def.Deferred = true
			out = append(out, placeholderRow(job, key, def))
			continue
		}
		var rows []*db.ActionsJob
		if err == nil {
			rows, err = m.matrixRows(job, def, strategy, base)
		}
		if err != nil {
			// One failed job carries the reason instead of a silent log line
			def.Error = fmt.Sprintf("matrix: %v", err)
			out = append(out, placeholderRow(job, key, def))
			continue
		}
		out = append(out, rows...)
	}
	return out
}

// One row per combination with the resolved strategy
func (m *Manager) matrixRows(job *Job, def jobDefinition, strategy *Strategy, base *ExprContext) ([]*db.ActionsJob, error) {
	combos, order, err := MatrixCombinations(strategy)
	if err != nil {
		return nil, err
	}
	resolved := *job
	resolved.Strategy = strategy
	def.Job, def.MatrixOrder, def.Deferred = &resolved, order, false
	maxParallel := 0
	if strategy != nil {
		maxParallel = strategy.MaxParallel
	}
	key := job.Key
	var out []*db.ActionsJob
	for _, combo := range combos {
		ectx := *base
		ectx.Matrix = callMatrix(job.Calls, combo)
		ectx.Env = mergeEnv(def.WorkflowEnv, job.Env)
		name, err := Interpolate(job.Name, &ectx)
		if err != nil || name == "" {
			name = job.Name
		}
		if job.Name == job.Key || !strings.Contains(job.Name, "matrix") {
			name += MatrixSuffix(combo, order)
		}
		row := &db.ActionsJob{
			ID: uuid.New().String(), Name: name, JobKey: key + MatrixSuffix(combo, order), MatrixGroup: key, MaxParallel: maxParallel,
			RunsOn: mustJSON(m.resolveRunsOn(job.RunsOn, &ectx)), Needs: mustJSON(orEmpty(job.Needs)),
			Matrix: mustJSON(orEmptyMap(combo)), Status: StatusWaiting, Attempt: 1,
			Steps: "[]", Outputs: "{}", Annotations: "[]", Definition: mustJSON(def),
		}
		if job.Concurrency != nil {
			if group, err := Interpolate(job.Concurrency.Group, &ectx); err == nil && !HasDeferred(group) {
				row.Concurrency = strings.TrimSpace(group)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// Single row for a failed or deferred job
func placeholderRow(job *Job, key string, def jobDefinition) *db.ActionsJob {
	return &db.ActionsJob{
		ID: uuid.New().String(), Name: job.Name, JobKey: key, MatrixGroup: key,
		RunsOn: "[]", Needs: mustJSON(orEmpty(job.Needs)), Matrix: "{}", Status: StatusWaiting, Attempt: 1,
		Steps: "[]", Outputs: "{}", Annotations: "[]", Definition: mustJSON(def),
	}
}

// Strategy with expressions evaluated, deferred when contexts lack
func resolveStrategy(st *Strategy, ectx *ExprContext) (*Strategy, bool, error) {
	if st == nil {
		return nil, false, nil
	}
	out := *st
	if st.MatrixExpr != "" {
		expanded, err := matrixFromExpr(st, ectx)
		if errors.Is(err, ErrDeferred) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		out = *expanded
	}
	if st.FailFastExpr != "" {
		v, err := Eval(st.FailFastExpr, ectx)
		if errors.Is(err, ErrDeferred) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("fail-fast: %w", err)
		}
		b := expr.Truthy(v)
		out.FailFast, out.FailFastExpr = &b, ""
	}
	if st.MaxParallelExpr != "" {
		v, err := Eval(st.MaxParallelExpr, ectx)
		if errors.Is(err, ErrDeferred) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("max-parallel: %w", err)
		}
		n := expr.ToNumber(v)
		if n != n || n < 0 {
			return nil, false, fmt.Errorf("max-parallel: %q is not a number", stringify(v))
		}
		out.MaxParallel, out.MaxParallelExpr = int(n), ""
	}
	out.MatrixExpr = ""
	return &out, false, nil
}

// Expands a deferred placeholder into its matrix legs
func (m *Manager) expandDeferred(ctx context.Context, run *db.ActionsRun, placeholder *db.ActionsJob, def *jobDefinition, ectx *ExprContext) ([]*db.ActionsJob, error) {
	strategy, deferred, err := resolveStrategy(def.Job.Strategy, ectx)
	if err != nil {
		return nil, err
	}
	if deferred {
		return nil, errors.New("expression uses a context that is unavailable at the job level")
	}
	rows, err := m.matrixRows(def.Job, *def, strategy, ectx)
	if err != nil {
		return nil, err
	}
	first := rows[0]
	cols := map[string]any{
		"name": first.Name, "job_key": first.JobKey, "runs_on": first.RunsOn, "matrix": first.Matrix,
		"max_parallel": first.MaxParallel, "concurrency": first.Concurrency, "definition": first.Definition,
	}
	if err := m.store.UpdateActionsJobColumns(ctx, placeholder.ID, cols); err != nil {
		return nil, err
	}
	// Legs sort right behind the placeholder
	for i, row := range rows[1:] {
		row.RunID = run.ID
		row.CreatedAt = placeholder.CreatedAt.Add(time.Duration(i+1) * time.Microsecond)
	}
	if err := m.store.CreateActionsJobs(ctx, rows[1:]); err != nil {
		return nil, err
	}
	return m.store.ListActionsJobs(ctx, run.ID)
}

// Evaluates job level continue-on-error and timeout-minutes, storing the results
func (m *Manager) resolveJobSettings(ctx context.Context, j *db.ActionsJob, def *jobDefinition, ectx *ExprContext) error {
	if def.Job.ContinueOnErrorExpr == "" && def.Job.TimeoutMinutesExpr == "" {
		return nil
	}
	resolved := *def.Job
	if e := resolved.ContinueOnErrorExpr; e != "" {
		v, err := Eval(e, ectx)
		if err != nil {
			return fmt.Errorf("continue-on-error: %v", err)
		}
		resolved.ContinueOnError, resolved.ContinueOnErrorExpr = expr.Truthy(v), ""
	}
	if e := resolved.TimeoutMinutesExpr; e != "" {
		v, err := Eval(e, ectx)
		if err != nil {
			return fmt.Errorf("timeout-minutes: %v", err)
		}
		n := expr.ToNumber(v)
		if n != n || n < 0 {
			return fmt.Errorf("timeout-minutes: %q is not a number", stringify(v))
		}
		resolved.TimeoutMinutes, resolved.TimeoutMinutesExpr = int(n), ""
	}
	def.Job = &resolved
	j.Definition = mustJSON(def)
	return m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{"definition": j.Definition})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Matrix given as a whole expression such as fromJSON(needs.x.outputs.m)
func matrixFromExpr(st *Strategy, ectx *ExprContext) (*Strategy, error) {
	v, err := Eval(st.MatrixExpr, ectx)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("matrix expression must yield an object")
	}
	out := &Strategy{Matrix: map[string][]any{}, FailFast: st.FailFast, MaxParallel: st.MaxParallel, FailFastExpr: st.FailFastExpr, MaxParallelExpr: st.MaxParallelExpr}
	for k, val := range obj {
		switch k {
		case "include":
			out.Include = objectList(val)
		case "exclude":
			out.Exclude = objectList(val)
		default:
			if list, ok := val.([]any); ok {
				out.Matrix[k] = list
			} else {
				out.Matrix[k] = []any{val}
			}
			out.MatrixOrder = append(out.MatrixOrder, k)
		}
	}
	return out, nil
}

func objectList(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, item := range list {
		if obj, ok := item.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

func mergeEnv(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range layers {
		for k, v := range l {
			out[k] = v
		}
	}
	return out
}

// Labels after interpolation, falling back to the configured default
func (m *Manager) resolveRunsOn(labels []string, ectx *ExprContext) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		v, err := Interpolate(l, ectx)
		if err != nil {
			v = l
		}
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			var list []string
			if json.Unmarshal([]byte(v), &list) == nil {
				out = append(out, list...)
				continue
			}
		}
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ── Contexts ─────────────────────────────────────────────────────────────

func (m *Manager) baseContext(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, wf *Workflow, inputs map[string]any) *ExprContext {
	vars, _ := m.repoVariables(ctx, repo, m.repoOrgID(ctx, repo))
	if inputs == nil {
		inputs = inputsFromPayload(run.Payload)
	}
	out := &ExprContext{
		GitHub: m.githubContext(ctx, repo, run, ""),
		Vars:   vars,
		Inputs: inputs,
		Env:    mergeEnv(wf.Env),
	}
	return out
}

func inputsFromPayload(payload string) map[string]any {
	var p map[string]any
	if json.Unmarshal([]byte(payload), &p) != nil {
		return map[string]any{}
	}
	if in, ok := p["inputs"].(map[string]any); ok {
		return in
	}
	return map[string]any{}
}

// The github context of a run, optionally bound to a job
func (m *Manager) githubContext(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, jobKey string) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal([]byte(run.Payload), &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	ref := plumbing.ReferenceName(run.Ref)
	refType := "branch"
	if ref.IsTag() {
		refType = "tag"
	}
	actor := m.username(ctx, run.TriggerUserID)
	base := m.git.BaseURL(ctx)
	g := map[string]any{
		"event_name":       run.Event,
		"event":            payload,
		"ref":              run.Ref,
		"ref_name":         ref.Short(),
		"ref_type":         refType,
		"ref_protected":    false,
		"sha":              run.SHA,
		"repository":       repo.Namespace + "/" + repo.Name,
		"repository_owner": repo.Namespace,
		"repository_id":    repo.ID,
		"actor":            actor,
		"triggering_actor": actor,
		"run_id":           run.ID,
		"run_number":       float64(run.Number),
		"run_attempt":      float64(1),
		"workflow":         run.WorkflowName,
		"workflow_ref":     repo.Namespace + "/" + repo.Name + "/" + run.WorkflowPath + "@" + run.Ref,
		"server_url":       base,
		"api_url":          base + "/api/v3",
		"graphql_url":      "",
		"job":              jobKey,
		"head_ref":         pullHeadBranch(payload),
		"base_ref":         pullBaseBranch(payload),
	}
	if run.Event == "pull_request" || run.Event == "pull_request_target" {
		g["ref"] = fmt.Sprintf("refs/pull/%d/merge", run.PullNumber)
		g["ref_name"] = fmt.Sprintf("%d/merge", run.PullNumber)
	}
	return g
}

func (m *Manager) username(ctx context.Context, userID string) string {
	if userID == "" {
		return "system"
	}
	u, err := m.store.GetUserByID(ctx, userID)
	if err != nil || u == nil {
		return "unknown"
	}
	return u.Username
}

// ── Progression ──────────────────────────────────────────────────────────

func baseKey(j *db.ActionsJob) string {
	if j.MatrixGroup != "" {
		return j.MatrixGroup
	}
	if i := strings.Index(j.JobKey, " ("); i > 0 {
		return j.JobKey[:i]
	}
	return j.JobKey
}

// Definitions decoded once per aggregation pass
type defCache map[string]*jobDefinition

func (c defCache) get(j *db.ActionsJob) *jobDefinition {
	if d, ok := c[j.ID]; ok {
		return d
	}
	d, err := decodeDefinition(j)
	if err != nil {
		d = nil
	}
	c[j.ID] = d
	return d
}

// Index of the call frame whose caller key is need, -1 when none
func frameIndex(def *jobDefinition, need string) int {
	if def == nil || def.Job == nil {
		return -1
	}
	for i, fr := range def.Job.Calls {
		if fr.Caller == need {
			return i
		}
	}
	return -1
}

// Rows a need refers to, plain jobs by key and inlined jobs by caller
func needRows(need string, jobs []*db.ActionsJob, defs defCache) []*db.ActionsJob {
	var out []*db.ActionsJob
	for _, j := range jobs {
		if baseKey(j) == need || frameIndex(defs.get(j), need) >= 0 {
			out = append(out, j)
		}
	}
	return out
}

// Aggregated state of the jobs a job needs, defs shared across a pass
func needsState(needs []string, jobs []*db.ActionsJob, defs defCache) (done bool, status string, outputs map[string]any) {
	done = true
	status = ConclusionSuccess
	outputs = map[string]any{}
	if defs == nil {
		defs = defCache{}
	}
	for _, need := range needs {
		rows := needRows(need, jobs, defs)
		result := ConclusionSuccess
		if len(rows) == 0 {
			result = ConclusionSkipped
		}
		for _, j := range rows {
			if j.Status != StatusCompleted {
				done = false
				continue
			}
			c := effectiveConclusionWith(j, defs)
			if c != ConclusionSuccess && rankConclusion(c) > rankConclusion(result) {
				result = c
			}
		}
		if rankConclusion(result) > rankConclusion(status) {
			status = result
		}
		outputs[need] = map[string]any{"result": result, "outputs": needOutputs(need, rows, defs)}
	}
	return done, status, outputs
}

func rowOutputs(j *db.ActionsJob) map[string]any {
	var outs map[string]any
	_ = json.Unmarshal([]byte(j.Outputs), &outs)
	return outs
}

// Plain rows merge their outputs, calls evaluate the workflow outputs
func needOutputs(need string, rows []*db.ActionsJob, defs defCache) map[string]any {
	merged := map[string]any{}
	var called []*db.ActionsJob
	idx := -1
	for _, j := range rows {
		if j.Status != StatusCompleted {
			continue
		}
		if i := frameIndex(defs.get(j), need); i >= 0 {
			idx = i
			called = append(called, j)
			continue
		}
		for k, v := range rowOutputs(j) {
			merged[k] = v
		}
	}
	if idx >= 0 {
		for k, v := range callOutputs(idx, called, defs) {
			merged[k] = v
		}
	}
	return merged
}

// Evaluates workflow_call outputs over the jobs below one frame
func callOutputs(idx int, rows []*db.ActionsJob, defs defCache) map[string]any {
	groups := map[string][]*db.ActionsJob{}
	var frame *CallFrame
	for _, j := range rows {
		def := defs.get(j)
		if def == nil || len(def.Job.Calls) <= idx {
			continue
		}
		fr := def.Job.Calls[idx]
		if frame == nil {
			frame = fr
		}
		calledKey := leadingJobKey(strings.TrimPrefix(baseKey(j), fr.Prefix+"/"))
		groups[calledKey] = append(groups[calledKey], j)
	}
	out := map[string]any{}
	if frame == nil {
		return out
	}
	jobsCtx := map[string]any{}
	for calledKey, grp := range groups {
		outs := map[string]any{}
		result := ConclusionSuccess
		var nested []*db.ActionsJob
		for _, j := range grp {
			if c := effectiveConclusion(j); c != ConclusionSuccess && rankConclusion(c) > rankConclusion(result) {
				result = c
			}
			if def := defs.get(j); def != nil && len(def.Job.Calls) > idx+1 {
				nested = append(nested, j)
				continue
			}
			for k, v := range rowOutputs(j) {
				outs[k] = v
			}
		}
		if len(nested) > 0 {
			for k, v := range callOutputs(idx+1, nested, defs) {
				outs[k] = v
			}
		}
		jobsCtx[calledKey] = map[string]any{"result": result, "outputs": outs}
	}
	ectx := &ExprContext{Jobs: jobsCtx, Inputs: map[string]any{}}
	for name, expr := range frame.Outputs {
		v, err := Interpolate(expr, ectx)
		if err != nil {
			v = ""
		}
		out[name] = v
	}
	return out
}

// Caller matrices beneath the job's own so with expressions see them
func callMatrix(calls []*CallFrame, own map[string]any) map[string]any {
	if len(calls) == 0 {
		return own
	}
	out := map[string]any{}
	for _, fr := range calls {
		for k, v := range fr.Matrix {
			out[k] = v
		}
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

// Walks the call chain to type the inputs and narrow the secrets
// A nil secrets map skips narrowing, used for condition evaluation
func resolveCalls(calls []*CallFrame, ectx *ExprContext, jobs []*db.ActionsJob, secrets map[string]string) (map[string]any, map[string]string, error) {
	inputs := ectx.Inputs
	avail := secrets
	for _, fr := range calls {
		fctx := *ectx
		fctx.Inputs = inputs
		fctx.Matrix = fr.Matrix
		fctx.Env = fr.Env
		fctx.Secrets = nil
		fctx.Steps = nil
		_, _, fctx.Needs = needsState(fr.Needs, jobs, nil)
		raw := map[string]string{}
		for k, v := range fr.Inputs {
			s, err := Interpolate(v, &fctx)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: input %s: %w", fr.Ref, k, err)
			}
			raw[k] = s
		}
		typed, err := ValidateInputs(&TriggerSpec{Inputs: fr.InputSpec, InputOrder: fr.InputOrder}, raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", fr.Ref, err)
		}
		inputs = typed
		if avail == nil || fr.Inherit {
			continue
		}
		sctx := fctx
		sctx.Secrets = avail
		next := map[string]string{}
		for name, v := range fr.Secrets {
			s, err := Interpolate(v, &sctx)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: secret %s: %w", fr.Ref, name, err)
			}
			next[name] = s
		}
		avail = next
	}
	return inputs, avail, nil
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
	return effectiveConclusionWith(j, nil)
}

// Same with a shared decode cache for aggregation passes
func effectiveConclusionWith(j *db.ActionsJob, defs defCache) string {
	c := j.Conclusion
	if c == ConclusionNeutral {
		return ConclusionSuccess
	}
	if c == ConclusionFailure || c == ConclusionTimedOut {
		var def *jobDefinition
		if defs != nil {
			def = defs.get(j)
		} else if d, err := decodeDefinition(j); err == nil {
			def = d
		}
		if def != nil && def.Job != nil && def.Job.ContinueOnError {
			return ConclusionSuccess
		}
	}
	return c
}

// Queues waiting jobs whose needs finished, skips those whose if is false
func (m *Manager) advanceRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) {
	if run.Status == StatusWaiting {
		return
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return
	}
	var wf *Workflow
	if run.Workflow != "" {
		wf, _ = ParseWorkflow([]byte(run.Workflow))
	}
	queued := false
	defs := defCache{}
	for changed := true; changed; {
		changed = false
		for _, j := range jobs {
			if j.Status != StatusWaiting {
				continue
			}
			def, err := decodeDefinition(j)
			if err != nil {
				m.failJob(ctx, repo, run, j, ConclusionFailure, "invalid job definition")
				changed = true
				continue
			}
			if def.Error != "" {
				m.failJob(ctx, repo, run, j, ConclusionFailure, def.Error)
				changed = true
				continue
			}
			done, status, outputs := needsState(def.Job.Needs, jobs, defs)
			if !done {
				continue
			}
			ectx := m.jobContext(ctx, repo, run, wf, def, j, jobs, outputs, status)
			ectx.Secrets = nil
			if def.Deferred {
				fresh, err := m.expandDeferred(ctx, run, j, def, ectx)
				if err != nil {
					m.failJob(ctx, repo, run, j, ConclusionFailure, fmt.Sprintf("matrix: %v", err))
					changed = true
					continue
				}
				// New rows exist, start the pass over
				jobs, changed = fresh, true
				break
			}
			ok, err := EvalBool(def.Job.If, ectx)
			if err != nil {
				if errors.Is(err, ErrDeferred) {
					ok = true
				} else {
					m.log.Warn("actions: job %s if: %v", j.Name, err)
					ok = false
				}
			}
			now := time.Now().UTC()
			if !ok {
				j.Status, j.Conclusion, j.FinishedAt = StatusCompleted, ConclusionSkipped, &now
				_ = m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{"status": StatusCompleted, "conclusion": ConclusionSkipped, "finished_at": now})
				m.writeCommitStatus(ctx, repo, run, j, ConclusionSkipped)
				changed = true
				continue
			}
			if err := m.resolveJobSettings(ctx, j, def, ectx); err != nil {
				m.failJob(ctx, repo, run, j, ConclusionFailure, err.Error())
				changed = true
				continue
			}
			defs[j.ID] = def
			// Names over needs outputs resolve now
			if HasDeferred(j.Name) {
				if name, err := Interpolate(j.Name, ectx); err == nil && name != "" && !HasDeferred(name) {
					j.Name = name
					_ = m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{"name": name})
				}
			}
			runsOn := m.resolveRunsOn(def.Job.RunsOn, ectx)
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
			if def.Job.Concurrency != nil && def.Job.Concurrency.CancelInProgress && j.Concurrency != "" {
				m.cancelJobGroup(ctx, repo, j)
			}
			queued, changed = true, true
		}
	}
	allDone := true
	for _, j := range jobs {
		if j.Status != StatusCompleted {
			allDone = false
			break
		}
	}
	if allDone {
		m.completeRun(ctx, repo, run, jobs)
		return
	}
	if queued {
		m.notifyRunners()
	}
}

// Contexts for a job level expression
func (m *Manager) jobContext(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, wf *Workflow, def *jobDefinition, j *db.ActionsJob, jobs []*db.ActionsJob, needs map[string]any, status string) *ExprContext {
	var ectx *ExprContext
	if wf != nil {
		ectx = m.baseContext(ctx, repo, run, wf, nil)
	} else {
		vars, _ := m.repoVariables(ctx, repo, m.repoOrgID(ctx, repo))
		ectx = &ExprContext{GitHub: m.githubContext(ctx, repo, run, ""), Vars: vars, Inputs: inputsFromPayload(run.Payload)}
	}
	ectx.GitHub["job"] = baseKey(j)
	ectx.GitHub["run_attempt"] = float64(j.Attempt)
	ectx.Matrix = callMatrix(def.Job.Calls, decodeMatrix(j))
	ectx.Needs = orMap(needs)
	ectx.Strategy = strategyContext(def.Job.Strategy, j, jobs)
	ectx.Status = status
	ectx.Env = mergeEnv(def.WorkflowEnv, def.Job.Env)
	if len(def.Job.Calls) > 0 {
		if inputs, _, err := resolveCalls(def.Job.Calls, ectx, jobs, nil); err == nil {
			ectx.Inputs = inputs
		}
	}
	return ectx
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
	m.revokeJobToken(ctx, j)
	m.clearCancel(j.ID)
	m.closeJobExecs(ctx, j, conclusion, "the command workspace ended: "+note)
	m.writeCommitStatus(ctx, repo, run, j, conclusion)
}

// Finishes the run once every job completed
func (m *Manager) completeRun(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, jobs []*db.ActionsJob) {
	if run.Status == StatusCompleted {
		return
	}
	conclusion := ConclusionSuccess
	allSkipped := len(jobs) > 0
	for _, j := range jobs {
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
	reset := map[string]bool{}
	for _, j := range jobs {
		if !failedOnly {
			reset[j.ID] = true
			continue
		}
		switch j.Conclusion {
		case ConclusionFailure, ConclusionCancelled, ConclusionTimedOut, ConclusionSkipped:
			reset[j.ID] = true
		}
	}
	// Dependents of a reset job need fresh outputs too
	defs := defCache{}
	for changed := true; changed; {
		changed = false
		for _, j := range jobs {
			if reset[j.ID] {
				continue
			}
			for _, need := range decodeStrings(j.Needs) {
				for _, other := range jobs {
					if reset[other.ID] && (baseKey(other) == need || frameIndex(defs.get(other), need) >= 0) {
						reset[j.ID], changed = true, true
					}
				}
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
		_ = m.store.UpdateActionsJobColumns(ctx, j.ID, map[string]any{
			"status": StatusWaiting, "conclusion": "", "runner_id": "", "attempt": j.Attempt + 1,
			"steps": "[]", "outputs": "{}", "annotations": "[]", "token_id": "", "log_chunks": 0, "log_digest": "",
			"started_at": nil, "finished_at": nil,
		})
	}
	run.Status, run.Conclusion, run.StartedAt, run.FinishedAt = StatusQueued, "", nil, nil
	if err := m.store.UpdateActionsRunColumns(ctx, run.ID, map[string]any{"status": StatusQueued, "conclusion": "", "started_at": nil, "finished_at": nil}); err != nil {
		return err
	}
	m.advanceRun(ctx, repo, run)
	m.notifyRunners()
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
	spec, ok := file.Workflow.On["workflow_dispatch"]
	if !ok {
		return nil, fmt.Errorf("%w: workflow has no workflow_dispatch trigger", ErrInvalid)
	}
	disabled, _ := m.store.GetDisabledWorkflows(ctx, repo.ID)
	if disabled[wfPath] {
		return nil, fmt.Errorf("%w: workflow is disabled", ErrConflict)
	}
	typed, err := ValidateInputs(spec, inputs)
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
	payload := map[string]any{
		"inputs": typed, "ref": fullRef, "workflow": wfPath,
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
	for id, run := range touched {
		if id != j.RunID {
			m.advanceRun(ctx, repo, run)
		}
	}
}

// Cancels the remaining jobs of a matrix once one of them failed
func (m *Manager) failFast(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, failed *db.ActionsJob) {
	def, err := decodeDefinition(failed)
	if err != nil || def.Job.ContinueOnError || def.Job.Strategy == nil || (def.Job.Strategy.FailFast != nil && !*def.Job.Strategy.FailFast) {
		return
	}
	jobs, err := m.store.ListActionsJobs(ctx, run.ID)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.ID == failed.ID || j.MatrixGroup != failed.MatrixGroup {
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
