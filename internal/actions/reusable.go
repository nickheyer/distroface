package actions

import (
	"errors"
	"fmt"
	"strings"
)

// Reusable workflow levels the planner follows before refusing
const maxCallDepth = 4

// Resolves a uses reference relative to the workflow holding it
type callLoader func(ref, from string) (wf *Workflow, location string, err error)

// ExpandCalls returns a copy of wf with reusable workflow jobs inlined
// A call that cannot be planned becomes a job carrying the reason
func ExpandCalls(wf *Workflow, location string, load callLoader, ectx *ExprContext) *Workflow {
	return inlineCalls(wf, location, load, ectx, 0, []string{location})
}

func inlineCalls(wf *Workflow, from string, load callLoader, ectx *ExprContext, depth int, trail []string) *Workflow {
	out := &Workflow{
		Name: wf.Name, On: wf.On, Env: wf.Env, Concurrency: wf.Concurrency, Defaults: wf.Defaults, Permissions: wf.Permissions,
		Jobs: map[string]*Job{}, Notes: append([]string(nil), wf.Notes...),
	}
	add := func(j *Job) {
		out.Jobs[j.Key] = j
		out.JobOrder = append(out.JobOrder, j.Key)
	}
	for _, key := range wf.JobOrder {
		job := wf.Jobs[key]
		if job.Uses == "" {
			add(job)
			continue
		}
		jobs, notes, err := expandCall(job, wf.Env, from, load, ectx, depth, trail)
		out.Notes = append(out.Notes, notes...)
		if err != nil {
			add(failedCall(job, err))
			continue
		}
		for _, j := range jobs {
			add(j)
		}
	}
	return out
}

// Copy of the caller job that fails at run time with the reason
func failedCall(job *Job, err error) *Job {
	failed := *job
	failed.Error = fmt.Sprintf("reusable workflow %s: %v", job.Uses, err)
	return &failed
}

// Loads, validates, and splices one called workflow under the caller key
func expandCall(job *Job, wfEnv map[string]string, from string, load callLoader, ectx *ExprContext, depth int, trail []string) ([]*Job, []string, error) {
	if depth >= maxCallDepth {
		return nil, nil, fmt.Errorf("nesting deeper than %d levels", maxCallDepth)
	}
	if load == nil {
		return nil, nil, errors.New("no loader for reusable workflows")
	}
	called, location, err := load(job.Uses, from)
	if err != nil {
		return nil, nil, err
	}
	for _, seen := range trail {
		if seen != "" && seen == location {
			return nil, nil, fmt.Errorf("cycle through %s", strings.Join(append(append([]string(nil), trail...), location), " -> "))
		}
	}
	spec, ok := called.On["workflow_call"]
	if !ok {
		return nil, nil, errors.New("the called workflow has no workflow_call trigger")
	}
	if err := checkCallInputs(job, spec); err != nil {
		return nil, nil, err
	}
	var notes []string
	if job.Concurrency != nil {
		notes = append(notes, fmt.Sprintf("concurrency on job %s is ignored for reusable workflow calls", job.Key))
	}
	flat := inlineCalls(called, location, load, ectx, depth+1, append(append([]string(nil), trail...), location))
	notes = append(notes, flat.Notes...)
	combos, order, err := callerCombos(job, ectx)
	if err != nil {
		return nil, notes, fmt.Errorf("matrix: %w", err)
	}
	outputs := map[string]string{}
	for name, o := range spec.Outputs {
		outputs[name] = o.Value
	}
	var out []*Job
	for _, combo := range combos {
		frame := &CallFrame{
			Caller: job.Key, Prefix: job.Key + MatrixSuffix(combo, order), Ref: job.Uses,
			Inputs: job.With, Secrets: job.Secrets, Inherit: job.SecretsInherit,
			InputSpec: spec.Inputs, InputOrder: spec.InputOrder, SecretSpec: spec.Secrets, Outputs: outputs,
			Needs: job.Needs, Matrix: combo, Env: wfEnv,
		}
		callerName := callerDisplayName(job, wfEnv, combo, order, ectx)
		for _, ck := range flat.JobOrder {
			out = append(out, inlineJob(flat.Jobs[ck], job, frame, callerName, called.Env))
		}
	}
	return out, notes, nil
}

// Static checks GitHub performs before a run starts
func checkCallInputs(job *Job, spec *TriggerSpec) error {
	for k := range job.With {
		if spec.Inputs[k] == nil {
			return fmt.Errorf("input %q is not declared by the called workflow", k)
		}
	}
	for _, name := range spec.InputOrder {
		in := spec.Inputs[name]
		if _, ok := job.With[name]; !ok && in.Required && in.Default == "" {
			return fmt.Errorf("input %q is required", name)
		}
	}
	if job.SecretsInherit {
		return nil
	}
	for k := range job.Secrets {
		if spec.Secrets[k] == nil {
			return fmt.Errorf("secret %q is not declared by the called workflow", k)
		}
	}
	for _, name := range spec.SecretOrder {
		if cs := spec.Secrets[name]; cs != nil && cs.Required {
			if _, ok := job.Secrets[name]; !ok {
				return fmt.Errorf("secret %q is required", name)
			}
		}
	}
	return nil
}

// Matrix combinations of the caller job, one nil entry without a strategy
func callerCombos(job *Job, ectx *ExprContext) ([]map[string]any, []string, error) {
	st := job.Strategy
	if st != nil && st.MatrixExpr != "" {
		if ectx == nil {
			return nil, nil, errors.New("matrix expression needs a run context")
		}
		var err error
		if st, err = matrixFromExpr(st, ectx); err != nil {
			if errors.Is(err, ErrDeferred) {
				return nil, nil, errors.New("a matrix over needs outputs cannot drive a reusable workflow call")
			}
			return nil, nil, err
		}
	}
	return MatrixCombinations(st)
}

// Caller job name the way planJobs would render it
func callerDisplayName(job *Job, wfEnv map[string]string, combo map[string]any, order []string, ectx *ExprContext) string {
	name := job.Name
	if ectx != nil {
		c := *ectx
		c.Matrix = combo
		c.Env = mergeEnv(wfEnv, job.Env)
		if v, err := Interpolate(job.Name, &c); err == nil && v != "" {
			name = v
		}
	}
	if job.Name == job.Key || !strings.Contains(job.Name, "matrix") {
		name += MatrixSuffix(combo, order)
	}
	return name
}

// One called job rewritten into the caller's namespace
func inlineJob(cj, caller *Job, frame *CallFrame, callerName string, calledEnv map[string]string) *Job {
	nj := *cj
	nj.Key = frame.Prefix + "/" + cj.Key
	nj.Name = callerName + " / " + cj.Name
	if len(cj.Needs) == 0 {
		nj.Needs = append([]string(nil), caller.Needs...)
		nj.If = combineIf(caller.If, cj.If)
	} else {
		nj.Needs = prefixKeys(frame.Prefix, cj.Needs)
	}
	calls := make([]*CallFrame, 0, len(cj.Calls)+1)
	calls = append(calls, frame)
	for _, inner := range cj.Calls {
		f := *inner
		f.Caller = frame.Prefix + "/" + inner.Caller
		f.Prefix = frame.Prefix + "/" + inner.Prefix
		f.Needs = prefixKeys(frame.Prefix, inner.Needs)
		calls = append(calls, &f)
	}
	nj.Calls = calls
	if len(cj.Calls) == 0 {
		nj.WorkflowEnv = calledEnv
	}
	nj.Permissions = callPermissions(caller.Permissions, cj.Permissions)
	return &nj
}

func prefixKeys(prefix string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, prefix+"/"+k)
	}
	return out
}

// Caller condition gates root called jobs on top of their own
func combineIf(callerIf, calledIf string) string {
	a, b := bareExpr(callerIf), bareExpr(calledIf)
	switch {
	case a == "":
		return calledIf
	case b == "":
		return callerIf
	}
	return "(" + a + ") && (" + b + ")"
}

func bareExpr(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		return strings.TrimSpace(s[3 : len(s)-2])
	}
	return s
}

// Called permissions never exceed the caller's
func callPermissions(caller, called *Permissions) *Permissions {
	if caller.ReadOnly() {
		return &Permissions{All: "read-all"}
	}
	if called == nil {
		return caller
	}
	return called
}

// Job key at the start of a key remainder, before any suffix or slash
func leadingJobKey(s string) string {
	for i := range s {
		c := s[i]
		if c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return s[:i]
	}
	return s
}
