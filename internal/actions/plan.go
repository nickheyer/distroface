package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitea.com/gitea/runner/act/model"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"go.yaml.in/yaml/v4"
)

// Matrix legs one job may expand to, GitHub's cap
const MaxMatrixJobs = 256

// Reusable workflow nesting GitHub allows
const maxCallDepth = 4

// Contexts every planning expression sees
type planInput struct {
	repo   *db.GitRepository
	run    *db.ActionsRun
	gitCtx map[string]any
	vars   map[string]string
	inputs map[string]any
}

// Run level contexts, dispatch inputs typed as declared
func (m *Manager) planInput(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) *planInput {
	vars, _ := m.repoVariables(ctx, repo, m.repoOrgID(ctx, repo))
	return &planInput{repo: repo, run: run, gitCtx: m.githubContext(ctx, repo, run, nil), vars: vars, inputs: runInputs(run)}
}

// Per job contexts, called jobs see the caller's with values
func (m *Manager) scoped(ctx context.Context, p *planInput, job *db.ActionsJob) *planInput {
	inputs := p.inputs
	if job.ParentJobID != "" {
		inputs = m.jobInputs(ctx, p.run, job)
	}
	return &planInput{repo: p.repo, run: p.run, vars: p.vars, inputs: inputs, gitCtx: m.githubContext(ctx, p.repo, p.run, job)}
}

// Interpreter over the planning contexts for one job
func (p *planInput) evaluator(jobID string, job *jobparser.Job, matrix map[string]any, results map[string]*jobparser.JobResult) *jobparser.ExpressionEvaluator {
	actJob := &model.Job{Strategy: &model.Strategy{}}
	if job != nil {
		actJob.Strategy = &model.Strategy{FailFastString: job.Strategy.FailFastString, MaxParallelString: job.Strategy.MaxParallelString, RawMatrix: job.Strategy.RawMatrix}
	}
	if results == nil {
		results = map[string]*jobparser.JobResult{}
	}
	if _, ok := results[jobID]; !ok {
		var needs []string
		if job != nil {
			needs = job.Needs()
		}
		results[jobID] = &jobparser.JobResult{ID: jobID, Needs: needs}
	}
	return jobparser.NewExpressionEvaluator(jobparser.NewInterpeter(jobID, actJob, matrix, jobparser.ToGitHubContext(p.gitCtx), results, p.vars, p.inputs))
}

// CreateRun materializes one workflow file into run and job rows
func (m *Manager) createRun(ctx context.Context, repo *db.GitRepository, f *WorkflowFile, in *triggerInput) (*db.ActionsRun, error) {
	if f.Err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, f.Path, f.Err)
	}
	number, err := m.store.NextGitCounter(ctx, repo.ID, "run_counter")
	if err != nil {
		return nil, err
	}
	run := &db.ActionsRun{
		ID: uuid.New().String(), RepoID: repo.ID, Number: number,
		WorkflowPath: f.Path, WorkflowName: f.Name,
		Event: in.Event, Ref: in.Ref, SHA: in.SHA,
		Status: StatusQueued, NeedsApproval: in.NeedsApproval, FromFork: in.FromFork, PullNumber: in.PullNumber,
		Payload: mustJSON(in.Payload), Workflow: f.Source, Notes: "[]",
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
	p := m.planInput(ctx, repo, run)
	if in.Inputs != nil {
		p.inputs = in.Inputs
	}
	var notes []string
	group, cancelInProgress, err := runConcurrency(p, []byte(f.Source))
	if err != nil {
		notes = append(notes, "concurrency: "+err.Error())
	} else {
		run.Concurrency = group
	}
	jobs := m.planJobs(p, []byte(f.Source), nil)
	run.Notes = mustJSON(orEmpty(notes))
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

	if cancelInProgress && run.Concurrency != "" {
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

// Workflow level concurrency group, empty when the file declares none
func runConcurrency(p *planInput, source []byte) (group string, cancel bool, err error) {
	defer recoverPlanning(&err)
	rc, err := jobparser.ReadWorkflowRawConcurrency(source)
	if err != nil || rc == nil {
		return "", false, nil
	}
	group, cancel, err = jobparser.EvaluateConcurrency(rc, "", nil, p.gitCtx, nil, p.vars, p.inputs)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(group), cancel, nil
}

// Rows per job, a planning failure becomes one failed row
func (m *Manager) planJobs(p *planInput, source []byte, parent *db.ActionsJob) []*db.ActionsJob {
	rows, err := m.planRows(p, source, parent)
	if err != nil {
		name := p.run.WorkflowName
		if parent != nil {
			name = parent.Name
		}
		return []*db.ActionsJob{errorRow(name, name, parent, err)}
	}
	return rows
}

func (m *Manager) planRows(p *planInput, source []byte, parent *db.ActionsJob) (out []*db.ActionsJob, err error) {
	defer recoverPlanning(&err)
	opts := []jobparser.ParseOption{jobparser.WithGitContext(jobparser.ToGitHubContext(p.gitCtx)), jobparser.WithVars(p.vars), jobparser.WithInputs(p.inputs)}
	swfs, err := jobparser.Parse(source, opts...)
	if err != nil {
		return nil, err
	}
	legs := map[string]int{}
	for _, swf := range swfs {
		jobID, job := swf.Job()
		if job == nil {
			continue
		}
		legs[jobID]++
		out = append(out, m.jobRow(p, swf, jobID, job, parent, nil))
	}
	for id, n := range legs {
		if n <= MaxMatrixJobs {
			continue
		}
		kept := out[:0]
		for _, row := range out {
			if row.WorkflowJobID != id {
				kept = append(kept, row)
			}
		}
		name := id
		if parent != nil {
			name = parent.Name + " / " + id
		}
		out = append(kept, errorRow(name, id, parent, fmt.Errorf("matrix expands to %d jobs, the limit is %d", n, MaxMatrixJobs)))
	}
	return out, nil
}

// One row of a single job workflow
func (m *Manager) jobRow(p *planInput, swf *jobparser.SingleWorkflow, jobID string, job *jobparser.Job, parent *db.ActionsJob, results map[string]*jobparser.JobResult) *db.ActionsJob {
	matrix := matrixOf(job)
	name := job.Name
	if name == "" {
		name = jobID
	}
	if parent != nil {
		name = parent.Name + " / " + name
	}
	row := &db.ActionsJob{
		ID: uuid.New().String(), Name: name, JobKey: jobKey(jobID, matrix), WorkflowJobID: jobID, MatrixGroup: jobID,
		RunsOn: mustJSON(orEmpty(job.RunsOn())), Needs: mustJSON(orEmpty(job.Needs())), Matrix: mustJSON(orEmptyMap(matrix)),
		Status: StatusWaiting, Attempt: 1, Steps: "[]", Outputs: "{}", Annotations: "[]", CallInputs: "{}",
		FailFast: true, ContinueOnError: job.GetContinueOnError(), Deferred: swf.Deferred,
		ReadOnly: readOnlyPermissions(swf.RawPermissions, job.RawPermissions),
	}
	if parent != nil {
		row.ParentJobID = parent.ID
	}
	if job.Uses != "" {
		row.IsReusableCaller, row.CallUses = true, strings.TrimSpace(job.Uses)
	}
	if !swf.Deferred {
		ev := p.evaluator(jobID, job, matrix, results)
		if v, err := evalBool(ev, job.Strategy.FailFastString, true); err != nil {
			noteRowError(row, "fail-fast: "+err.Error())
		} else {
			row.FailFast = v
		}
		if v, err := evalInt(ev, job.Strategy.MaxParallelString); err != nil {
			noteRowError(row, "max-parallel: "+err.Error())
		} else {
			row.MaxParallel = v
		}
		if v, err := evalInt(ev, job.TimeoutMinutes); err != nil {
			noteRowError(row, "timeout-minutes: "+err.Error())
		} else {
			row.TimeoutMinutes = v
		}
		if job.RawConcurrency != nil && !strings.Contains(concurrencyText(job.RawConcurrency), "needs.") {
			group, cancel, err := jobparser.EvaluateConcurrency(job.RawConcurrency, jobID, job, p.gitCtx, results, p.vars, p.inputs)
			if err != nil {
				noteRowError(row, "concurrency: "+err.Error())
			} else {
				row.Concurrency, row.ConcurrencyCancel, row.ConcurrencyEvaluated = strings.TrimSpace(group), cancel, true
			}
		}
	}
	// Payload carries the job alone, runner rebuilds needs
	if err := swf.SetJob(jobID, job.EraseNeeds()); err != nil {
		noteRowError(row, err.Error())
	}
	if b, err := swf.Marshal(); err != nil {
		noteRowError(row, err.Error())
	} else {
		row.WorkflowPayload = string(b)
	}
	return row
}

// Planning errors accumulate, the first one leads
func noteRowError(row *db.ActionsJob, msg string) {
	if row.Error == "" {
		row.Error = msg
		return
	}
	row.Error += ", " + msg
}

// Row that fails on advance carrying the planning error
func errorRow(name, jobID string, parent *db.ActionsJob, err error) *db.ActionsJob {
	row := &db.ActionsJob{
		ID: uuid.New().String(), Name: name, JobKey: jobID, WorkflowJobID: jobID, MatrixGroup: jobID,
		RunsOn: "[]", Needs: "[]", Matrix: "{}", Status: StatusWaiting, Attempt: 1,
		Steps: "[]", Outputs: "{}", Annotations: "[]", CallInputs: "{}", FailFast: true, Error: err.Error(),
	}
	if parent != nil {
		row.ParentJobID = parent.ID
	}
	return row
}

func concurrencyText(rc *model.RawConcurrency) string {
	return rc.RawExpression + " " + rc.Group + " " + rc.CancelInProgress
}

// Interpolates a bool field, the default when empty
func evalBool(ev *jobparser.ExpressionEvaluator, raw string, def bool) (bool, error) {
	s, err := ev.InterpolateErr(raw)
	if err != nil {
		return def, err
	}
	if s = strings.TrimSpace(s); s == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return def, fmt.Errorf("%q is not a boolean", s)
	}
	return v, nil
}

// Interpolates an integer field, zero when empty
func evalInt(ev *jobparser.ExpressionEvaluator, raw string) (int, error) {
	s, err := ev.InterpolateErr(raw)
	if err != nil {
		return 0, err
	}
	if s = strings.TrimSpace(s); s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return int(f), nil
}

// Matrix values of one leg as the parser encoded them
func matrixOf(job *jobparser.Job) map[string]any {
	out := map[string]any{}
	if job.Strategy.RawMatrix.Kind == 0 {
		return out
	}
	var raw map[string][]any
	if err := job.Strategy.RawMatrix.Decode(&raw); err != nil {
		return out
	}
	for k, v := range raw {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// Job id plus the leg's values in key order
func jobKey(jobID string, matrix map[string]any) string {
	if len(matrix) == 0 {
		return jobID
	}
	keys := make([]string, 0, len(matrix))
	for k := range matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, fmt.Sprint(matrix[k]))
	}
	return jobID + " (" + strings.Join(vals, ", ") + ")"
}

// MatrixSuffix names a leg the way GitHub does
func MatrixSuffix(matrix map[string]any) string {
	return strings.TrimPrefix(jobKey("", matrix), " ")
}

// Token stays read only without contents or packages write
func readOnlyPermissions(workflow, job yaml.Node) bool {
	n := job
	if n.Kind == 0 {
		n = workflow
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return strings.TrimSpace(n.Value) != "write-all"
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := strings.TrimSpace(n.Content[i].Value), strings.TrimSpace(n.Content[i+1].Value)
			if (k == "contents" || k == "packages") && v == "write" {
				return false
			}
		}
		return true
	}
	return false
}

// Source a deferred or rerun job plans from
func (m *Manager) jobSource(ctx context.Context, run *db.ActionsRun, job *db.ActionsJob) ([]byte, *db.ActionsJob, error) {
	if job.ParentJobID == "" {
		return []byte(run.Workflow), nil, nil
	}
	caller, err := m.store.GetActionsJob(ctx, job.ParentJobID)
	if err != nil {
		return nil, nil, err
	}
	if caller == nil {
		return nil, nil, fmt.Errorf("caller of %s is gone", job.Name)
	}
	return []byte(caller.CallContent), caller, nil
}

// Expands a deferred placeholder into its legs now that needs finished
func (m *Manager) expandDeferred(ctx context.Context, p *planInput, placeholder *db.ActionsJob, results map[string]*jobparser.JobResult) ([]*db.ActionsJob, error) {
	rows, err := m.deferredRows(ctx, p, placeholder, results)
	if err != nil {
		return nil, err
	}
	first := rows[0]
	cols := map[string]any{
		"name": first.Name, "job_key": first.JobKey, "runs_on": first.RunsOn, "matrix": first.Matrix,
		"max_parallel": first.MaxParallel, "fail_fast": first.FailFast, "continue_on_error": first.ContinueOnError,
		"timeout_minutes": first.TimeoutMinutes, "concurrency": first.Concurrency, "concurrency_cancel": first.ConcurrencyCancel,
		"concurrency_evaluated": first.ConcurrencyEvaluated, "read_only": first.ReadOnly, "workflow_payload": first.WorkflowPayload,
		"is_reusable_caller": first.IsReusableCaller, "call_uses": first.CallUses, "error": first.Error, "deferred": false,
	}
	// Legs sort right behind the placeholder
	for i, row := range rows[1:] {
		row.RunID = p.run.ID
		row.Interactive = placeholder.Interactive
		row.CreatedAt = placeholder.CreatedAt.Add(time.Duration(i+1) * time.Microsecond)
	}
	// Already expanded by a concurrent advance means nothing changes
	if _, err := m.store.ExpandActionsJob(ctx, placeholder.ID, map[string]any{"deferred": true}, cols, rows[1:]); err != nil {
		return nil, err
	}
	return m.store.ListActionsJobs(ctx, p.run.ID)
}

// Legs of a deferred job planned with the finished needs
func (m *Manager) deferredRows(ctx context.Context, p *planInput, placeholder *db.ActionsJob, results map[string]*jobparser.JobResult) (rows []*db.ActionsJob, err error) {
	defer recoverPlanning(&err)
	source, parent, err := m.jobSource(ctx, p.run, placeholder)
	if err != nil {
		return nil, err
	}
	resultNames, outputs := map[string]string{}, map[string]map[string]string{}
	for id, r := range results {
		resultNames[id] = r.Result
		outputs[id] = r.Outputs
	}
	swfs, err := jobparser.Parse(source, jobparser.WithGitContext(jobparser.ToGitHubContext(p.gitCtx)), jobparser.WithVars(p.vars),
		jobparser.WithInputs(p.inputs), jobparser.WithJobResults(resultNames), jobparser.WithJobOutputs(outputs), jobparser.WithJobFilter(placeholder.WorkflowJobID))
	if err != nil {
		return nil, err
	}
	if len(swfs) == 0 {
		return nil, fmt.Errorf("job %s vanished from the workflow", placeholder.WorkflowJobID)
	}
	if len(swfs) > MaxMatrixJobs {
		return nil, fmt.Errorf("matrix expands to %d jobs, the limit is %d", len(swfs), MaxMatrixJobs)
	}
	for _, swf := range swfs {
		if swf.Deferred {
			return nil, errors.New("matrix still awaits needs that never finish")
		}
		jobID, job := swf.Job()
		rows = append(rows, m.jobRow(p, swf, jobID, job, parent, results))
	}
	return rows, nil
}

// Callers above a job, nearest first
func (m *Manager) callerChain(ctx context.Context, job *db.ActionsJob) ([]*db.ActionsJob, error) {
	var chain []*db.ActionsJob
	seen := map[string]bool{job.ID: true}
	for cur := job; cur.ParentJobID != ""; {
		if seen[cur.ParentJobID] {
			return nil, errors.New("reusable workflow calls form a cycle")
		}
		seen[cur.ParentJobID] = true
		parent, err := m.store.GetActionsJob(ctx, cur.ParentJobID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, errors.New("caller job is gone")
		}
		chain = append(chain, parent)
		cur = parent
	}
	return chain, nil
}

// Inserts the called workflow's jobs beneath a caller
func (m *Manager) expandCaller(ctx context.Context, p *planInput, caller *db.ActionsJob, results map[string]*jobparser.JobResult) (err error) {
	defer recoverPlanning(&err)
	chain, err := m.callerChain(ctx, caller)
	if err != nil {
		return err
	}
	if len(chain) >= maxCallDepth {
		return fmt.Errorf("reusable workflows nest deeper than %d levels", maxCallDepth)
	}
	_, parsed, err := parseJobPayload(caller.WorkflowPayload)
	if err != nil {
		return err
	}
	ref, err := jobparser.ParseUses(parsed.Uses)
	if err != nil {
		return err
	}
	src, err := m.loadCalledWorkflow(ctx, p, chain, ref)
	if err != nil {
		return err
	}
	if err := checkCallCycle(p.repo, p.run, chain, src, ref.Path); err != nil {
		return err
	}
	spec, err := jobparser.ParseWorkflowCallSpec(src.content)
	if err != nil {
		return fmt.Errorf("%s: %w", parsed.Uses, err)
	}
	inherit, mapping, err := jobparser.ParseCallerSecrets(parsed.RawSecrets)
	if err != nil {
		return err
	}
	if !inherit {
		if err := jobparser.ValidateCallerSecrets(spec, mapping); err != nil {
			return err
		}
	}
	scope := m.scoped(ctx, p, caller)
	inputs := map[string]any{}
	if len(spec.Inputs) > 0 {
		evaluated, err := jobparser.EvaluateCallerWith(caller.WorkflowJobID, parsed, scope.gitCtx, results, p.vars, scope.inputs)
		if err != nil {
			return err
		}
		if inputs, err = jobparser.MatchCallerInputsAgainstSpec(spec, evaluated); err != nil {
			return err
		}
	}
	secrets := ""
	switch {
	case inherit:
		secrets = jobparser.SecretsInherit
	case len(mapping) > 0:
		secrets = mustJSON(mapping)
	}
	caller.CallSecrets, caller.CallInputs, caller.CallContent = secrets, mustJSON(inputs), string(src.content)
	caller.CallSourceRepoID, caller.CallSourceRepo, caller.CallSourceRef = src.repoID, src.repo, src.ref
	// Event inputs stay strings, the inputs context stays typed
	child := &planInput{repo: p.repo, run: p.run, vars: p.vars, inputs: inputs, gitCtx: m.githubContext(ctx, p.repo, p.run, caller)}
	if event, ok := child.gitCtx["event"].(map[string]any); ok {
		event["inputs"] = inputStrings(inputs)
	}
	child.gitCtx["event_name"] = "workflow_call"
	rows := m.planJobs(child, src.content, caller)
	if len(rows) == 0 {
		return errors.New("called workflow has no jobs")
	}
	for i, row := range rows {
		row.RunID = p.run.ID
		row.Interactive = caller.Interactive
		row.CreatedAt = caller.CreatedAt.Add(time.Duration(i+1) * time.Microsecond)
	}
	cols := map[string]any{
		"call_secrets": secrets, "call_inputs": caller.CallInputs, "call_content": caller.CallContent,
		"call_source_repo_id": src.repoID, "call_source_repo": src.repo, "call_source_ref": src.ref, "is_expanded": true,
	}
	// Already expanded by a concurrent advance means nothing changes
	claimed, err := m.store.ExpandActionsJob(ctx, caller.ID, map[string]any{"is_expanded": false}, cols, rows)
	if err != nil {
		return err
	}
	if claimed {
		caller.IsExpanded = true
	}
	return nil
}

// Where a called workflow was read from
type callSource struct {
	content []byte
	// Local repository id, empty when fetched from the actions host
	repoID string
	// owner/repo the file lives in
	repo string
	// Resolved commit for local repositories, the ref as written otherwise
	ref string
}

// Calling a workflow already on the chain is a cycle
func checkCallCycle(repo *db.GitRepository, run *db.ActionsRun, chain []*db.ActionsJob, src *callSource, calledPath string) error {
	key := src.repo + "/" + calledPath + "@" + src.ref
	if key == repo.Namespace+"/"+repo.Name+"/"+run.WorkflowPath+"@"+run.SHA {
		return fmt.Errorf("reusable workflow call cycle: %s calls the workflow of this run", calledPath)
	}
	for _, c := range chain {
		if c.CallUses == "" {
			continue
		}
		up, err := jobparser.ParseUses(c.CallUses)
		if err != nil {
			continue
		}
		if c.CallSourceRepo+"/"+up.Path+"@"+c.CallSourceRef == key {
			return fmt.Errorf("reusable workflow call cycle: %s is already on the call chain", c.CallUses)
		}
	}
	return nil
}

// Called source, ./ paths resolve beside the nearest caller's file
func (m *Manager) loadCalledWorkflow(ctx context.Context, p *planInput, chain []*db.ActionsJob, ref *jobparser.UsesRef) (*callSource, error) {
	if ref.Kind == jobparser.UsesKindLocalSameRepo {
		if len(chain) == 0 {
			return m.readLocalCalled(ctx, p, p.repo, p.run.SHA, ref.Path)
		}
		origin := chain[0]
		switch {
		case origin.CallSourceRepoID != "":
			called, err := m.store.GetGitRepositoryByID(ctx, origin.CallSourceRepoID)
			if err != nil {
				return nil, err
			}
			if called == nil {
				return nil, fmt.Errorf("repository %s of the calling workflow is gone", origin.CallSourceRepo)
			}
			return m.readLocalCalled(ctx, p, called, origin.CallSourceRef, ref.Path)
		case origin.CallSourceRepo != "":
			owner, name, _ := strings.Cut(origin.CallSourceRepo, "/")
			return m.fetchRemoteCalled(ctx, &jobparser.UsesRef{Kind: jobparser.UsesKindLocalCrossRepo, Owner: owner, Repo: name, Path: ref.Path, Ref: origin.CallSourceRef})
		}
		return nil, fmt.Errorf("the source of %s is unknown, rerun the workflow to plan it again", origin.CallUses)
	}
	called, err := m.store.GetGitRepository(ctx, ref.Owner, ref.Repo)
	if err != nil {
		return nil, err
	}
	if called != nil {
		return m.readLocalCalled(ctx, p, called, ref.Ref, ref.Path)
	}
	return m.fetchRemoteCalled(ctx, ref)
}

// Workflow of a local repository the run actor may read
func (m *Manager) readLocalCalled(ctx context.Context, p *planInput, called *db.GitRepository, rev, filePath string) (*callSource, error) {
	if called.ID != p.repo.ID && !m.canReadWorkflows(ctx, p.run, called) {
		return nil, fmt.Errorf("repository %s/%s is not readable by the run actor", called.Namespace, called.Name)
	}
	content, h, err := m.readWorkflowSource(called, rev, filePath)
	if err != nil {
		return nil, err
	}
	return &callSource{content: content, repoID: called.ID, repo: called.Namespace + "/" + called.Name, ref: h.String()}, nil
}

func (m *Manager) fetchRemoteCalled(ctx context.Context, ref *jobparser.UsesRef) (*callSource, error) {
	content, err := m.fetchRemoteWorkflow(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &callSource{content: content, repo: ref.Owner + "/" + ref.Repo, ref: ref.Ref}, nil
}

// Whether the run's actor may read workflow files of another repository
func (m *Manager) canReadWorkflows(ctx context.Context, run *db.ActionsRun, called *db.GitRepository) bool {
	if run.TriggerUserID != "" {
		if u, _ := m.store.GetUserByID(ctx, run.TriggerUserID); u != nil && u.IsActive {
			roles, _ := m.store.GetUserRoleNames(ctx, u.ID)
			actor := &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: roles, Provider: u.AuthProvider}
			return m.git.Access().Level(ctx, actor, called, db.GitUnitCode) >= 1
		}
	}
	return !called.IsPrivate
}

// Raw file of a workflow hosted where actions come from
func (m *Manager) fetchRemoteWorkflow(ctx context.Context, ref *jobparser.UsesRef) ([]byte, error) {
	base := strings.TrimRight(m.defaultActionsURL(ctx), "/")
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	var candidates []string
	if strings.EqualFold(u.Host, "github.com") {
		candidates = []string{fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", ref.Owner, ref.Repo, url.PathEscape(ref.Ref), ref.Path)}
	} else {
		for _, kind := range []string{"branch", "tag", "commit"} {
			candidates = append(candidates, fmt.Sprintf("%s/%s/%s/raw/%s/%s/%s", base, ref.Owner, ref.Repo, kind, url.PathEscape(ref.Ref), ref.Path))
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var last error
	for _, c := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, workflowFileCap+1))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("%s answered %s", c, resp.Status)
			continue
		}
		if len(body) > workflowFileCap {
			return nil, fmt.Errorf("%s exceeds %d bytes", path.Base(ref.Path), workflowFileCap)
		}
		return body, nil
	}
	return nil, fmt.Errorf("fetch %s/%s/%s@%s: %w", ref.Owner, ref.Repo, ref.Path, ref.Ref, last)
}

// Secrets a job may read, narrowed by each caller
func (m *Manager) jobSecrets(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob) (map[string]string, error) {
	if run.FromFork {
		return map[string]string{}, nil
	}
	secrets, err := m.repoSecrets(ctx, repo, m.repoOrgID(ctx, repo))
	if err != nil {
		return nil, err
	}
	chain, err := m.callerChain(ctx, job)
	if err != nil {
		return nil, err
	}
	// Outermost caller narrows first
	for i := len(chain) - 1; i >= 0; i-- {
		caller := chain[i]
		if caller.CallSecrets == "" {
			secrets = map[string]string{}
			continue
		}
		if caller.CallSecrets == jobparser.SecretsInherit {
			continue
		}
		var mapping map[string]string
		if err := json.Unmarshal([]byte(caller.CallSecrets), &mapping); err != nil {
			return nil, fmt.Errorf("caller %s secrets: %w", caller.Name, err)
		}
		next := map[string]string{}
		for alias, source := range mapping {
			if v, ok := secrets[source]; ok {
				next[alias] = v
			}
		}
		secrets = next
	}
	return secrets, nil
}

// Outputs a finished caller exposes, evaluated over its children
func (m *Manager) callerOutputs(ctx context.Context, p *planInput, caller *db.ActionsJob, children []*db.ActionsJob) (out map[string]string, err error) {
	defer recoverPlanning(&err)
	spec, err := jobparser.ParseWorkflowCallSpec([]byte(caller.CallContent))
	if err != nil {
		return nil, err
	}
	jobOutputs := jobparser.JobOutputs{}
	for _, c := range children {
		if c.Status != StatusCompleted {
			continue
		}
		merged := jobOutputs[c.WorkflowJobID]
		if merged == nil {
			merged = map[string]string{}
			jobOutputs[c.WorkflowJobID] = merged
		}
		for k, v := range rowOutputs(c) {
			merged[k] = v
		}
	}
	inputs := decodePayload(caller.CallInputs)
	gitCtx := m.githubContext(ctx, p.repo, p.run, caller)
	if event, ok := gitCtx["event"].(map[string]any); ok {
		event["inputs"] = inputStrings(inputs)
	}
	gitCtx["event_name"] = "workflow_call"
	return jobparser.EvaluateWorkflowCallOutputs(spec, jobparser.ToGitHubContext(gitCtx), p.vars, inputs, jobOutputs)
}

// Where uses references without a host resolve
func (m *Manager) defaultActionsURL(ctx context.Context) string {
	if u := strings.TrimSpace(m.res.System(ctx).GetGit().GetActions().GetDefaultActionsUrl()); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "https://github.com"
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

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func decodeStrings(s string) []string {
	var out []string
	if s == "" || json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// Outputs a job reported, strings only as GitHub keeps them
func rowOutputs(j *db.ActionsJob) map[string]string {
	var raw map[string]any
	_ = json.Unmarshal([]byte(j.Outputs), &raw)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch x := v.(type) {
		case string:
			out[k] = x
		case nil:
			out[k] = ""
		default:
			out[k] = mustJSON(x)
		}
	}
	return out
}
