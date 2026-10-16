package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Directories searched for workflow files, first hit wins
var WorkflowDirs = []string{".distroface/workflows", ".gitea/workflows", ".github/workflows"}

// Workflow is one parsed workflow file
type Workflow struct {
	Name        string
	On          map[string]*TriggerSpec
	Env         map[string]string
	Concurrency *Concurrency
	Jobs        map[string]*Job
	JobOrder    []string
	// Warnings collected while parsing, never fatal
	Notes []string
}

// Filters of one event in the on block
type TriggerSpec struct {
	Branches       []string
	BranchesIgnore []string
	Tags           []string
	TagsIgnore     []string
	Paths          []string
	PathsIgnore    []string
	Types          []string
	Inputs         map[string]*DispatchInput
	InputOrder     []string
	Crons          []string
}

// One workflow_dispatch input
type DispatchInput struct {
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Default     string   `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type Concurrency struct {
	Group            string
	CancelInProgress bool
}

// Job is one entry of the jobs map
type Job struct {
	Key             string              `json:"key"`
	Name            string              `json:"name"`
	RunsOn          []string            `json:"runs_on"`
	Needs           []string            `json:"needs,omitempty"`
	If              string              `json:"if,omitempty"`
	Env             map[string]string   `json:"env,omitempty"`
	TimeoutMinutes  int                 `json:"timeout_minutes,omitempty"`
	ContinueOnError bool                `json:"continue_on_error,omitempty"`
	Container       *Container          `json:"container,omitempty"`
	Services        map[string]*Service `json:"services,omitempty"`
	ServiceOrder    []string            `json:"service_order,omitempty"`
	Outputs         map[string]string   `json:"outputs,omitempty"`
	Steps           []*Step             `json:"steps"`
	Strategy        *Strategy           `json:"strategy,omitempty"`
	Defaults        *Defaults           `json:"defaults,omitempty"`
}

type Strategy struct {
	Matrix      map[string][]any `json:"matrix,omitempty"`
	MatrixOrder []string         `json:"matrix_order,omitempty"`
	Include     []map[string]any `json:"include,omitempty"`
	Exclude     []map[string]any `json:"exclude,omitempty"`
	// Whole matrix given as an expression, expanded when a run starts
	MatrixExpr  string `json:"matrix_expr,omitempty"`
	FailFast    *bool  `json:"fail_fast,omitempty"`
	MaxParallel int    `json:"max_parallel,omitempty"`
}

type Container struct {
	Image   string            `json:"image"`
	Env     map[string]string `json:"env,omitempty"`
	Ports   []string          `json:"ports,omitempty"`
	Volumes []string          `json:"volumes,omitempty"`
	Options string            `json:"options,omitempty"`
}

type Service struct {
	Image   string            `json:"image"`
	Env     map[string]string `json:"env,omitempty"`
	Ports   []string          `json:"ports,omitempty"`
	Volumes []string          `json:"volumes,omitempty"`
	Options string            `json:"options,omitempty"`
}

type Defaults struct {
	Shell            string `json:"shell,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
}

type Step struct {
	Name             string            `json:"name,omitempty"`
	ID               string            `json:"id,omitempty"`
	Uses             string            `json:"uses,omitempty"`
	Run              string            `json:"run,omitempty"`
	Shell            string            `json:"shell,omitempty"`
	With             map[string]string `json:"with,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	If               string            `json:"if,omitempty"`
	ContinueOnError  bool              `json:"continue_on_error,omitempty"`
	TimeoutMinutes   int               `json:"timeout_minutes,omitempty"`
}

// Events whose types filter defaults when unspecified
var defaultEventTypes = map[string][]string{
	"pull_request":        {"opened", "synchronize", "reopened"},
	"pull_request_target": {"opened", "synchronize", "reopened"},
}

var jobKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// IsWorkflowFile reports whether a path names a workflow file
func IsWorkflowFile(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".yml" || ext == ".yaml"
}

// ParseWorkflow parses workflow yaml in the GitHub Actions syntax
func ParseWorkflow(src []byte) (*Workflow, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, errors.New("empty workflow")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("workflow must be a mapping")
	}
	wf := &Workflow{On: map[string]*TriggerSpec{}, Jobs: map[string]*Job{}}
	var onNode, jobsNode *yaml.Node
	for k, v := range mapPairs(doc) {
		switch k {
		case "name":
			wf.Name = scalarString(v)
		case "on", "true":
			onNode = v
		case "env":
			wf.Env = parseStringMap(v)
		case "concurrency":
			wf.Concurrency = parseConcurrency(v)
		case "jobs":
			jobsNode = v
		case "permissions":
			wf.Notes = append(wf.Notes, "permissions are ignored, the job token always has repo scope")
		case "defaults", "run-name":
		default:
			wf.Notes = append(wf.Notes, fmt.Sprintf("unknown top level key %q ignored", k))
		}
	}
	if onNode == nil {
		return nil, errors.New("workflow needs an on block")
	}
	if err := parseOn(onNode, wf); err != nil {
		return nil, err
	}
	if jobsNode == nil || jobsNode.Kind != yaml.MappingNode || len(jobsNode.Content) == 0 {
		return nil, errors.New("workflow needs at least one job")
	}
	for k, v := range mapPairs(jobsNode) {
		if !jobKeyPattern.MatchString(k) {
			return nil, fmt.Errorf("invalid job id %q", k)
		}
		job, err := parseJob(k, v)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", k, err)
		}
		wf.Jobs[k] = job
		wf.JobOrder = append(wf.JobOrder, k)
	}
	for _, k := range wf.JobOrder {
		for _, need := range wf.Jobs[k].Needs {
			if _, ok := wf.Jobs[need]; !ok {
				return nil, fmt.Errorf("job %s needs unknown job %q", k, need)
			}
		}
	}
	if err := checkNeedsCycle(wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// Events in declaration order
func (w *Workflow) Events() []string {
	keys := make([]string, 0, len(w.On))
	for k := range w.On {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Dispatch inputs when the workflow supports manual runs
func (w *Workflow) DispatchInputs() (map[string]*DispatchInput, []string, bool) {
	spec, ok := w.On["workflow_dispatch"]
	if !ok {
		return nil, nil, false
	}
	return spec.Inputs, spec.InputOrder, true
}

func checkNeedsCycle(wf *Workflow) error {
	state := map[string]int{}
	var visit func(k string, trail []string) error
	visit = func(k string, trail []string) error {
		switch state[k] {
		case 1:
			return fmt.Errorf("needs cycle through %s", strings.Join(append(trail, k), " -> "))
		case 2:
			return nil
		}
		state[k] = 1
		for _, n := range wf.Jobs[k].Needs {
			if err := visit(n, append(trail, k)); err != nil {
				return err
			}
		}
		state[k] = 2
		return nil
	}
	for _, k := range wf.JobOrder {
		if err := visit(k, nil); err != nil {
			return err
		}
	}
	return nil
}

// ── on ───────────────────────────────────────────────────────────────────

func parseOn(n *yaml.Node, wf *Workflow) error {
	switch n.Kind {
	case yaml.ScalarNode:
		wf.On[n.Value] = &TriggerSpec{}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			wf.On[scalarString(item)] = &TriggerSpec{}
		}
	case yaml.MappingNode:
		for k, v := range mapPairs(n) {
			spec, err := parseTriggerSpec(k, v)
			if err != nil {
				return fmt.Errorf("on.%s: %w", k, err)
			}
			wf.On[k] = spec
		}
	default:
		return errors.New("on must be a string, list, or mapping")
	}
	return nil
}

func parseTriggerSpec(event string, n *yaml.Node) (*TriggerSpec, error) {
	spec := &TriggerSpec{}
	if n.Kind != yaml.MappingNode {
		if event == "schedule" && n.Kind == yaml.SequenceNode {
			return parseSchedule(n)
		}
		return spec, nil
	}
	if event == "schedule" {
		return nil, errors.New("schedule must be a list of cron entries")
	}
	for k, v := range mapPairs(n) {
		switch k {
		case "branches":
			spec.Branches = stringList(v)
		case "branches-ignore":
			spec.BranchesIgnore = stringList(v)
		case "tags":
			spec.Tags = stringList(v)
		case "tags-ignore":
			spec.TagsIgnore = stringList(v)
		case "paths":
			spec.Paths = stringList(v)
		case "paths-ignore":
			spec.PathsIgnore = stringList(v)
		case "types":
			spec.Types = stringList(v)
		case "inputs":
			if v.Kind == yaml.MappingNode {
				spec.Inputs = map[string]*DispatchInput{}
				for name, in := range mapPairs(v) {
					di, err := parseDispatchInput(in)
					if err != nil {
						return nil, fmt.Errorf("inputs.%s: %w", name, err)
					}
					spec.Inputs[name] = di
					spec.InputOrder = append(spec.InputOrder, name)
				}
			}
		}
	}
	return spec, nil
}

func parseSchedule(n *yaml.Node) (*TriggerSpec, error) {
	spec := &TriggerSpec{}
	for _, item := range n.Content {
		cron := ""
		if item.Kind == yaml.MappingNode {
			for k, v := range mapPairs(item) {
				if k == "cron" {
					cron = scalarString(v)
				}
			}
		} else {
			cron = scalarString(item)
		}
		cron = strings.TrimSpace(cron)
		if cron == "" {
			return nil, errors.New("schedule entry needs a cron field")
		}
		if _, err := ParseCron(cron); err != nil {
			return nil, fmt.Errorf("cron %q: %w", cron, err)
		}
		spec.Crons = append(spec.Crons, cron)
	}
	return spec, nil
}

func parseDispatchInput(n *yaml.Node) (*DispatchInput, error) {
	di := &DispatchInput{Type: "string"}
	if n.Kind != yaml.MappingNode {
		return di, nil
	}
	for k, v := range mapPairs(n) {
		switch k {
		case "description":
			di.Description = scalarString(v)
		case "type":
			di.Type = scalarString(v)
		case "default":
			di.Default = scalarString(v)
		case "required":
			di.Required = scalarBool(v)
		case "options":
			di.Options = stringList(v)
		}
	}
	switch di.Type {
	case "string", "boolean", "number", "choice", "environment":
	case "":
		di.Type = "string"
	default:
		return nil, fmt.Errorf("unknown input type %q", di.Type)
	}
	if di.Type == "choice" && len(di.Options) == 0 {
		return nil, errors.New("choice input needs options")
	}
	return di, nil
}

// ValidateInputs checks and completes workflow_dispatch inputs
func ValidateInputs(spec *TriggerSpec, given map[string]string) (map[string]any, error) {
	out := map[string]any{}
	for name, val := range given {
		if spec == nil || spec.Inputs[name] == nil {
			return nil, fmt.Errorf("unknown input %q", name)
		}
		_ = val
	}
	if spec == nil {
		return out, nil
	}
	for _, name := range spec.InputOrder {
		di := spec.Inputs[name]
		raw, ok := given[name]
		if !ok || raw == "" {
			if di.Required && di.Default == "" {
				return nil, fmt.Errorf("input %q is required", name)
			}
			raw = di.Default
		}
		switch di.Type {
		case "boolean":
			switch strings.ToLower(raw) {
			case "true":
				out[name] = true
			case "false", "":
				out[name] = false
			default:
				return nil, fmt.Errorf("input %q must be true or false", name)
			}
		case "number":
			if raw == "" {
				out[name] = float64(0)
				continue
			}
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("input %q must be a number", name)
			}
			out[name] = f
		case "choice":
			if raw != "" && !containsString(di.Options, raw) {
				return nil, fmt.Errorf("input %q must be one of %s", name, strings.Join(di.Options, ", "))
			}
			out[name] = raw
		default:
			out[name] = raw
		}
	}
	return out, nil
}

func parseConcurrency(n *yaml.Node) *Concurrency {
	c := &Concurrency{}
	switch n.Kind {
	case yaml.ScalarNode:
		c.Group = n.Value
	case yaml.MappingNode:
		for k, v := range mapPairs(n) {
			switch k {
			case "group":
				c.Group = scalarString(v)
			case "cancel-in-progress":
				c.CancelInProgress = scalarBool(v)
			}
		}
	}
	if c.Group == "" {
		return nil
	}
	return c
}

// ── jobs ─────────────────────────────────────────────────────────────────

func parseJob(key string, n *yaml.Node) (*Job, error) {
	if n.Kind != yaml.MappingNode {
		return nil, errors.New("job must be a mapping")
	}
	job := &Job{Key: key, Name: key}
	for k, v := range mapPairs(n) {
		switch k {
		case "name":
			job.Name = scalarString(v)
		case "runs-on":
			job.RunsOn = parseRunsOn(v)
		case "needs":
			job.Needs = stringList(v)
		case "if":
			job.If = scalarString(v)
		case "env":
			job.Env = parseStringMap(v)
		case "timeout-minutes":
			job.TimeoutMinutes = scalarInt(v)
		case "continue-on-error":
			job.ContinueOnError = scalarBool(v)
		case "container":
			job.Container = parseContainer(v)
		case "services":
			if v.Kind == yaml.MappingNode {
				job.Services = map[string]*Service{}
				for name, sv := range mapPairs(v) {
					c := parseContainer(sv)
					if c == nil {
						continue
					}
					job.Services[name] = &Service{Image: c.Image, Env: c.Env, Ports: c.Ports, Volumes: c.Volumes, Options: c.Options}
					job.ServiceOrder = append(job.ServiceOrder, name)
				}
			}
		case "outputs":
			job.Outputs = parseStringMap(v)
		case "steps":
			steps, err := parseSteps(v)
			if err != nil {
				return nil, err
			}
			job.Steps = steps
		case "strategy":
			st, err := parseStrategy(v)
			if err != nil {
				return nil, err
			}
			job.Strategy = st
		case "defaults":
			job.Defaults = parseDefaults(v)
		case "uses":
			return nil, errors.New("reusable workflows are not supported")
		}
	}
	if job.Name == "" {
		job.Name = key
	}
	if len(job.Steps) == 0 {
		return nil, errors.New("job needs at least one step")
	}
	return job, nil
}

func parseRunsOn(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		return stringList(n)
	case yaml.MappingNode:
		for k, v := range mapPairs(n) {
			if k == "labels" {
				return stringList(v)
			}
		}
	}
	return nil
}

func parseContainer(n *yaml.Node) *Container {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return &Container{Image: n.Value}
	case yaml.MappingNode:
		c := &Container{}
		for k, v := range mapPairs(n) {
			switch k {
			case "image":
				c.Image = scalarString(v)
			case "env":
				c.Env = parseStringMap(v)
			case "ports":
				c.Ports = stringList(v)
			case "volumes":
				c.Volumes = stringList(v)
			case "options":
				c.Options = scalarString(v)
			}
		}
		if c.Image == "" {
			return nil
		}
		return c
	}
	return nil
}

func parseDefaults(n *yaml.Node) *Defaults {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	d := &Defaults{}
	for k, v := range mapPairs(n) {
		if k == "run" && v.Kind == yaml.MappingNode {
			for rk, rv := range mapPairs(v) {
				switch rk {
				case "shell":
					d.Shell = scalarString(rv)
				case "working-directory":
					d.WorkingDirectory = scalarString(rv)
				}
			}
		}
	}
	return d
}

func parseSteps(n *yaml.Node) ([]*Step, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, errors.New("steps must be a list")
	}
	var out []*Step
	for i, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("step %d must be a mapping", i+1)
		}
		st := &Step{}
		for k, v := range mapPairs(item) {
			switch k {
			case "name":
				st.Name = scalarString(v)
			case "id":
				st.ID = scalarString(v)
			case "uses":
				st.Uses = scalarString(v)
			case "run":
				st.Run = scalarString(v)
			case "shell":
				st.Shell = scalarString(v)
			case "with":
				st.With = parseStringMap(v)
			case "env":
				st.Env = parseStringMap(v)
			case "working-directory":
				st.WorkingDirectory = scalarString(v)
			case "if":
				st.If = scalarString(v)
			case "continue-on-error":
				st.ContinueOnError = scalarBool(v)
			case "timeout-minutes":
				st.TimeoutMinutes = scalarInt(v)
			}
		}
		if st.Uses == "" && st.Run == "" {
			return nil, fmt.Errorf("step %d needs run or uses", i+1)
		}
		if st.Uses != "" && st.Run != "" {
			return nil, fmt.Errorf("step %d cannot have both run and uses", i+1)
		}
		out = append(out, st)
	}
	return out, nil
}

func parseStrategy(n *yaml.Node) (*Strategy, error) {
	if n.Kind != yaml.MappingNode {
		return nil, errors.New("strategy must be a mapping")
	}
	st := &Strategy{}
	for k, v := range mapPairs(n) {
		switch k {
		case "fail-fast":
			b := scalarBool(v)
			st.FailFast = &b
		case "max-parallel":
			st.MaxParallel = scalarInt(v)
		case "matrix":
			switch v.Kind {
			case yaml.ScalarNode:
				st.MatrixExpr = v.Value
			case yaml.MappingNode:
				st.Matrix = map[string][]any{}
				for mk, mv := range mapPairs(v) {
					switch mk {
					case "include":
						st.Include = parseObjectList(mv)
					case "exclude":
						st.Exclude = parseObjectList(mv)
					default:
						if mv.Kind == yaml.ScalarNode {
							st.Matrix[mk] = []any{mv.Value}
						} else {
							st.Matrix[mk] = anyList(mv)
						}
						st.MatrixOrder = append(st.MatrixOrder, mk)
					}
				}
			default:
				return nil, errors.New("matrix must be a mapping or expression")
			}
		}
	}
	return st, nil
}

func parseObjectList(n *yaml.Node) []map[string]any {
	if n.Kind != yaml.SequenceNode {
		return nil
	}
	var out []map[string]any
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		m := map[string]any{}
		for k, v := range mapPairs(item) {
			m[k] = anyValue(v)
		}
		out = append(out, m)
	}
	return out
}

// ── Matrix ───────────────────────────────────────────────────────────────

// MatrixCombinations expands a strategy into ordered combinations
func MatrixCombinations(st *Strategy) ([]map[string]any, []string) {
	if st == nil {
		return []map[string]any{nil}, nil
	}
	keys := append([]string{}, st.MatrixOrder...)
	combos := []map[string]any{{}}
	for _, k := range keys {
		vals := st.Matrix[k]
		var next []map[string]any
		for _, c := range combos {
			for _, v := range vals {
				nc := make(map[string]any, len(c)+1)
				for ck, cv := range c {
					nc[ck] = cv
				}
				nc[k] = v
				next = append(next, nc)
			}
		}
		combos = next
	}
	if len(keys) == 0 {
		combos = nil
	}
	var filtered []map[string]any
	for _, c := range combos {
		excluded := false
		for _, ex := range st.Exclude {
			if matrixMatches(c, ex) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, c)
		}
	}
	combos = filtered
	extraKeys := []string{}
	for _, inc := range st.Include {
		added := false
		for _, c := range combos {
			if matrixIncludeFits(c, inc, keys) {
				for ik, iv := range inc {
					if _, isMatrixKey := st.Matrix[ik]; isMatrixKey {
						continue
					}
					c[ik] = iv
					if !containsString(keys, ik) && !containsString(extraKeys, ik) {
						extraKeys = append(extraKeys, ik)
					}
				}
				added = true
			}
		}
		if !added {
			nc := map[string]any{}
			for ik, iv := range inc {
				nc[ik] = iv
				if !containsString(keys, ik) && !containsString(extraKeys, ik) {
					extraKeys = append(extraKeys, ik)
				}
			}
			combos = append(combos, nc)
		}
	}
	if len(combos) == 0 {
		return []map[string]any{nil}, nil
	}
	return combos, append(keys, extraKeys...)
}

// Exclude objects match when every one of their keys agrees
func matrixMatches(c, ex map[string]any) bool {
	for k, v := range ex {
		cv, ok := c[k]
		if !ok || !looseEqual(cv, v) {
			return false
		}
	}
	return len(ex) > 0
}

// Include fits a combo when its original matrix keys all agree
func matrixIncludeFits(c, inc map[string]any, matrixKeys []string) bool {
	for k, v := range inc {
		if !containsString(matrixKeys, k) {
			continue
		}
		if cv, ok := c[k]; !ok || !looseEqual(cv, v) {
			return false
		}
	}
	return true
}

// MatrixSuffix renders the parenthesized job name suffix
func MatrixSuffix(m map[string]any, order []string) string {
	if len(m) == 0 {
		return ""
	}
	keys := []string{}
	for _, k := range order {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
		}
	}
	for k := range m {
		if !containsString(keys, k) {
			keys = append(keys, k)
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, stringify(m[k]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// ── yaml helpers ─────────────────────────────────────────────────────────

// Iterates mapping pairs in document order
func mapPairs(n *yaml.Node) func(yield func(string, *yaml.Node) bool) {
	return func(yield func(string, *yaml.Node) bool) {
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i].Value, n.Content[i+1]) {
				return
			}
		}
	}
}

func scalarString(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.SequenceNode, yaml.MappingNode:
		var v any
		if err := n.Decode(&v); err == nil {
			if b, err := yamlToJSON(v); err == nil {
				return string(b)
			}
		}
	}
	return ""
}

func scalarBool(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.ScalarNode {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(n.Value), "true")
}

func scalarInt(n *yaml.Node) int {
	if n == nil || n.Kind != yaml.ScalarNode {
		return 0
	}
	i, _ := strconv.Atoi(strings.TrimSpace(n.Value))
	return i
}

func stringList(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, item := range n.Content {
			out = append(out, scalarString(item))
		}
		return out
	}
	return nil
}

func parseStringMap(n *yaml.Node) map[string]string {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]string{}
	for k, v := range mapPairs(n) {
		out[k] = scalarString(v)
	}
	return out
}

func anyList(n *yaml.Node) []any {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]any, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, anyValue(item))
	}
	return out
}

// Scalars become string, number, bool, or nil like json
func anyValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int", "!!float":
			if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
				return f
			}
		case "!!bool":
			return strings.EqualFold(n.Value, "true")
		case "!!null":
			return nil
		}
		return n.Value
	case yaml.SequenceNode:
		return anyList(n)
	case yaml.MappingNode:
		m := map[string]any{}
		for k, v := range mapPairs(n) {
			m[k] = anyValue(v)
		}
		return m
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func yamlToJSON(v any) ([]byte, error) {
	return json.Marshal(normalizeYAML(v))
}

// Converts yaml decoded maps into json friendly values
func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeYAML(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normalizeYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalizeYAML(val)
		}
		return out
	}
	return v
}
