// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package jobparser

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gitea.com/gitea/runner/act/exprparser"
	"gitea.com/gitea/runner/act/model"
	"go.yaml.in/yaml/v4"
)

func Parse(content []byte, options ...ParseOption) (ret []*SingleWorkflow, err error) {
	defer RecoverDecode(&err)
	origin, err := model.ReadWorkflow(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("model.ReadWorkflow: %w", err)
	}

	workflow := &SingleWorkflow{}
	if err := yaml.Unmarshal(content, workflow); err != nil {
		return nil, fmt.Errorf("yaml.Unmarshal: %w", err)
	}

	pc := &parseContext{}
	for _, o := range options {
		o(pc)
	}
	results := map[string]*JobResult{}
	for id, job := range origin.Jobs {
		if job == nil {
			return nil, fmt.Errorf("needed job not found: %q", id)
		}
		results[id] = &JobResult{
			ID:      id,
			Needs:   job.Needs(),
			Result:  pc.jobResults[id],
			Outputs: pc.jobOutputs[id],
		}
	}

	ids, jobs, err := workflow.jobs()
	if err != nil {
		return nil, fmt.Errorf("invalid jobs: %w", err)
	}

	evaluator := NewExpressionEvaluator(exprparser.NewInterpeter(&exprparser.EvaluationEnvironment{Github: pc.gitContext, Vars: pc.vars, Inputs: pc.inputs}, exprparser.Config{}))
	if workflow.RunName, err = evaluator.InterpolateErr(workflow.RunName); err != nil {
		return nil, fmt.Errorf("evaluate run-name: %w", err)
	}

	for i, id := range ids {
		if pc.jobFilter != "" && id != pc.jobFilter {
			continue
		}
		job := jobs[i]
		originJob := origin.GetJob(id)
		if awaitsNeeds(originJob, job, results) {
			if job.Name == "" {
				job.Name = id
			}
			swf := &SingleWorkflow{
				Name:           workflow.Name,
				RawOn:          workflow.RawOn,
				Env:            workflow.Env,
				Defaults:       workflow.Defaults,
				RawPermissions: workflow.RawPermissions,
				RunName:        workflow.RunName,
				Deferred:       true,
			}
			if err := swf.SetJob(id, job); err != nil {
				return nil, fmt.Errorf("SetJob: %w", err)
			}
			ret = append(ret, swf)
			continue
		}
		if originJob.Strategy != nil && mentionsExpression(&originJob.Strategy.RawMatrix) {
			evaluator := NewExpressionEvaluator(NewInterpeter(id, originJob, nil, pc.gitContext, results, pc.vars, pc.inputs))
			if err := evaluator.EvaluateYamlNode(&originJob.Strategy.RawMatrix); err != nil {
				return nil, fmt.Errorf("evaluate matrix for job %q: %w", id, err)
			}
		}
		matricxes, err := getMatrixes(originJob)
		if err != nil {
			return nil, fmt.Errorf("getMatrixes: %w", err)
		}
		for _, matrix := range matricxes {
			job := job.Clone()
			if job.Name == "" {
				job.Name = id
			}
			job.Strategy.RawMatrix = encodeMatrix(matrix)
			evaluator := NewExpressionEvaluator(NewInterpeter(id, origin.GetJob(id), matrix, pc.gitContext, results, pc.vars, pc.inputs))
			if job.Name, err = nameWithMatrix(job.Name, matrix, evaluator); err != nil {
				return nil, fmt.Errorf("evaluate name for job %q: %w", id, err)
			}
			runsOn := origin.GetJob(id).RunsOn()
			for i, v := range runsOn {
				if runsOn[i], err = evaluator.InterpolateErr(v); err != nil {
					return nil, fmt.Errorf("evaluate runs-on for job %q: %w", id, err)
				}
			}
			job.RawRunsOn = encodeRunsOn(runsOn)
			if err := evaluator.EvaluateYamlNode(&job.RawContinueOnError); err != nil {
				return nil, fmt.Errorf("evaluate continue-on-error for job %q: %w", id, err)
			}
			swf := &SingleWorkflow{
				Name:           workflow.Name,
				RawOn:          workflow.RawOn,
				Env:            workflow.Env,
				Defaults:       workflow.Defaults,
				RawPermissions: workflow.RawPermissions,
				RunName:        workflow.RunName,
			}
			if err := swf.SetJob(id, job); err != nil {
				return nil, fmt.Errorf("SetJob: %w", err)
			}
			ret = append(ret, swf)
		}
	}
	return ret, nil
}

func WithGitContext(context *model.GithubContext) ParseOption {
	return func(c *parseContext) {
		c.gitContext = context
	}
}

func WithVars(vars map[string]string) ParseOption {
	return func(c *parseContext) {
		c.vars = vars
	}
}

func WithInputs(inputs map[string]any) ParseOption {
	return func(c *parseContext) {
		c.inputs = inputs
	}
}

// WithJobResults supplies the conclusions of finished jobs
func WithJobResults(results map[string]string) ParseOption {
	return func(c *parseContext) {
		c.jobResults = results
	}
}

// WithJobOutputs supplies outputs of finished jobs for needs expressions
func WithJobOutputs(outputs map[string]map[string]string) ParseOption {
	return func(c *parseContext) {
		c.jobOutputs = outputs
	}
}

// WithJobFilter limits parsing to one job id
func WithJobFilter(id string) ParseOption {
	return func(c *parseContext) {
		c.jobFilter = id
	}
}

type parseContext struct {
	jobResults map[string]string
	jobOutputs map[string]map[string]string
	gitContext *model.GithubContext
	vars       map[string]string
	inputs     map[string]any
	jobFilter  string
}

// Whether a job reads needs the results do not know yet
func awaitsNeeds(origin *model.Job, job *Job, results map[string]*JobResult) bool {
	if !mentionsNeeds(origin, job) {
		return false
	}
	for _, need := range origin.Needs() {
		r := results[need]
		if r == nil || r.Result == "" {
			return true
		}
	}
	return false
}

// Fields planned before the runner starts that may read needs
func mentionsNeeds(origin *model.Job, job *Job) bool {
	var sb strings.Builder
	sb.WriteString(origin.Name)
	sb.WriteString(origin.TimeoutMinutes)
	nodes := []*yaml.Node{&origin.RawRunsOn, &job.RawContinueOnError}
	if origin.Strategy != nil {
		nodes = append(nodes, &origin.Strategy.RawMatrix)
	}
	for _, n := range nodes {
		if n.Kind != 0 {
			if b, err := yaml.Marshal(n); err == nil {
				sb.Write(b)
			}
		}
	}
	return strings.Contains(sb.String(), "needs.")
}

func mentionsExpression(n *yaml.Node) bool {
	if n.Kind == 0 {
		return false
	}
	b, err := yaml.Marshal(n)
	return err == nil && strings.Contains(string(b), "${{")
}

type ParseOption func(c *parseContext)

func getMatrixes(job *model.Job) ([]map[string]any, error) {
	ret, err := job.GetMatrixes()
	if err != nil {
		return nil, fmt.Errorf("GetMatrixes: %w", err)
	}
	sort.Slice(ret, func(i, j int) bool {
		return matrixName(ret[i]) < matrixName(ret[j])
	})
	return ret, nil
}

func encodeMatrix(matrix map[string]any) yaml.Node {
	if len(matrix) == 0 {
		return yaml.Node{}
	}
	value := map[string][]any{}
	for k, v := range matrix {
		value[k] = []any{v}
	}
	node := yaml.Node{}
	_ = node.Encode(value)
	return node
}

func encodeRunsOn(runsOn []string) yaml.Node {
	node := yaml.Node{}
	if len(runsOn) == 1 {
		_ = node.Encode(runsOn[0])
	} else {
		_ = node.Encode(runsOn)
	}
	return node
}

func nameWithMatrix(name string, m map[string]any, evaluator *ExpressionEvaluator) (string, error) {
	if len(m) == 0 {
		return name, nil
	}

	if !strings.Contains(name, "${{") || !strings.Contains(name, "}}") {
		return name + " " + matrixName(m), nil
	}

	return evaluator.InterpolateErr(name)
}

func matrixName(m map[string]any) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	vs := make([]string, 0, len(m))
	for _, v := range ks {
		vs = append(vs, fmt.Sprint(m[v]))
	}

	return fmt.Sprintf("(%s)", strings.Join(vs, ", "))
}
