package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	neturl "net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gopkg.in/yaml.v3"
)

const maxActionDepth = 10

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// Subset of action.yml the runner honors
type actionMeta struct {
	Name   string `yaml:"name"`
	Inputs map[string]struct {
		Default  any  `yaml:"default"`
		Required bool `yaml:"required"`
	} `yaml:"inputs"`
	Outputs map[string]struct {
		Value string `yaml:"value"`
	} `yaml:"outputs"`
	Runs struct {
		Using          string         `yaml:"using"`
		Pre            string         `yaml:"pre"`
		PreIf          string         `yaml:"pre-if"`
		Main           string         `yaml:"main"`
		Post           string         `yaml:"post"`
		PostIf         string         `yaml:"post-if"`
		Image          string         `yaml:"image"`
		Entrypoint     string         `yaml:"entrypoint"`
		PreEntrypoint  string         `yaml:"pre-entrypoint"`
		PostEntrypoint string         `yaml:"post-entrypoint"`
		Args           []string       `yaml:"args"`
		Env            map[string]any `yaml:"env"`
		Steps          []actionStep   `yaml:"steps"`
	} `yaml:"runs"`
}

type actionStep struct {
	ID               string         `yaml:"id"`
	Name             string         `yaml:"name"`
	Uses             string         `yaml:"uses"`
	Run              string         `yaml:"run"`
	Shell            string         `yaml:"shell"`
	WorkingDirectory string         `yaml:"working-directory"`
	If               string         `yaml:"if"`
	Env              map[string]any `yaml:"env"`
	With             map[string]any `yaml:"with"`
	ContinueOnError  any            `yaml:"continue-on-error"`
}

func (a actionStep) spec() stepSpec {
	coe, coeExpr := boolOrExpr(a.ContinueOnError)
	return stepSpec{
		ID: a.ID, Name: a.Name, Uses: a.Uses, Run: a.Run, Shell: a.Shell, WorkingDirectory: a.WorkingDirectory, If: a.If,
		Env: stringValues(a.Env), With: stringValues(a.With), ContinueOnError: coe, ContinueOnErrorExpr: coeExpr,
	}
}

// Yaml bool, bool string, or a deferred expression
func boolOrExpr(v any) (bool, string) {
	switch x := v.(type) {
	case bool:
		return x, ""
	case string:
		s := strings.TrimSpace(x)
		if strings.Contains(s, "${{") {
			return false, s
		}
		return strings.EqualFold(s, "true"), ""
	}
	return false, ""
}

func stringValues(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = stringify(v)
	}
	return out
}

type actionDef struct {
	ref     string
	repo    string
	version string
	dir     string
	root    string
	meta    actionMeta
}

func (a *actionDef) using() string {
	return strings.ToLower(strings.TrimSpace(a.meta.Runs.Using))
}

func (a *actionDef) isComposite() bool { return a.using() == "composite" }
func (a *actionDef) isDocker() bool    { return a.using() == "docker" }
func (a *actionDef) isNode() bool      { return strings.HasPrefix(a.using(), "node") }

// Whether the action declares a pre phase
func (a *actionDef) hasPre() bool {
	return a.isNode() && a.meta.Runs.Pre != "" || a.isDocker() && a.meta.Runs.PreEntrypoint != ""
}

// Whether the action declares a post phase
func (a *actionDef) hasPost() bool {
	return a.isNode() && a.meta.Runs.Post != "" || a.isDocker() && a.meta.Runs.PostEntrypoint != ""
}

// Whether a uses reference points into the checked out tree
func localAction(ref string) bool {
	return strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../")
}

// Whether a uses reference names something the runner fetches
func fetchedAction(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "docker://") || localAction(ref) {
		return false
	}
	name, _, _ := strings.Cut(ref, "@")
	return !nativeAction(name)
}

// Locates or fetches the action and reads its metadata
func (j *job) resolveAction(ctx context.Context, ref, localBase string, out io.Writer) (*actionDef, error) {
	if strings.HasPrefix(ref, "docker://") {
		act := &actionDef{ref: ref, repo: ref}
		act.meta.Runs.Using = "docker"
		act.meta.Runs.Image = ref
		return act, nil
	}
	if localAction(ref) {
		dir := filepath.Join(localBase, filepath.FromSlash(ref))
		if !within(j.dir, dir) {
			return nil, fmt.Errorf("action %q leaves the job directory", ref)
		}
		act := &actionDef{ref: ref, repo: ref, dir: dir, root: localBase}
		return act, act.load(j.readFile)
	}
	j.mu.Lock()
	cached := j.prepared[ref]
	j.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	spec, version, _ := strings.Cut(ref, "@")
	if version == "" {
		return nil, fmt.Errorf("action %q needs a version after @", ref)
	}
	parts := strings.SplitN(spec, "/", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("action %q is not owner/repo@ref", ref)
	}
	repo := parts[0] + "/" + parts[1]
	sub := ""
	if len(parts) == 3 {
		sub = parts[2]
	}
	root := filepath.Join(j.actionsDir, parts[0], parts[1], safeSegment(version))
	if _, err := j.stat(filepath.Join(root, actionMarkerFile)); err != nil {
		if err := j.fetchAction(ctx, repo, version, root, out); err != nil {
			return nil, err
		}
	}
	act := &actionDef{ref: ref, repo: repo, version: version, dir: filepath.Join(root, filepath.FromSlash(sub)), root: root}
	if err := act.load(j.readFile); err != nil {
		return nil, err
	}
	j.mu.Lock()
	j.prepared[ref] = act
	j.mu.Unlock()
	return act, nil
}

func safeSegment(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, s)
}

// Instance first, github.com second, following the DEFAULT_ACTIONS_URL rule
func (j *job) fetchAction(ctx context.Context, repo, version, dest string, out io.Writer) error {
	var sources []struct{ url, token string }
	if base := j.cloneBase(); base != "" && !githubOnly(repo) {
		sources = append(sources, struct{ url, token string }{j.hostURL(base + "/" + repo + ".git"), j.task.Token})
	}
	sources = append(sources, struct{ url, token string }{"https://github.com/" + repo + ".git", ""})
	rel, err := j.rel(dest)
	if err != nil {
		return err
	}
	root, err := j.fsRoot()
	if err != nil {
		return err
	}
	var errs []string
	for _, src := range sources {
		fmt.Fprintf(out, "Download action repository '%s@%s' from %s\n", repo, version, src.url)
		err := j.r.ext.ensureAction(ctx, src.url, version, src.token, out, func(cached string) error {
			_ = root.RemoveAll(rel)
			if err := copyTreeInto(root, cached, rel); err != nil {
				return fmt.Errorf("copy action %s@%s: %w", repo, version, err)
			}
			return root.WriteFile(filepath.Join(rel, actionMarkerFile), []byte("{}"), 0o644)
		})
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", src.url, err))
	}
	return fmt.Errorf("fetch action %s@%s: %s", repo, version, strings.Join(errs, "; "))
}

// Instance base url derived from the clone url
func (j *job) cloneBase() string {
	u := strings.TrimSuffix(j.task.CloneUrl, ".git")
	if i := strings.LastIndex(u, "/"+j.task.RepoFullName); i > 0 {
		return u[:i]
	}
	return strings.TrimRight(j.task.ServerUrl, "/")
}

// Instance urls rewritten to the runner's own server address
func (j *job) hostURL(raw string) string {
	server, err := neturl.Parse(j.r.cfg.Server)
	if err != nil || server.Host == "" {
		return raw
	}
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	instance := map[string]bool{strings.ToLower(u.Host): false}
	for _, known := range []string{j.task.ServerUrl, j.task.CloneUrl} {
		if k, err := neturl.Parse(known); err == nil && k.Host != "" {
			instance[strings.ToLower(k.Host)] = true
		}
	}
	if !instance[strings.ToLower(u.Host)] {
		return raw
	}
	u.Scheme, u.Host = server.Scheme, server.Host
	return u.String()
}

func (a *actionDef) load(read func(string) ([]byte, error)) error {
	var data []byte
	var err error
	for _, name := range []string{"action.yml", "action.yaml"} {
		data, err = read(filepath.Join(a.dir, name))
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("action %s has no action.yml", a.ref)
	}
	if err := yaml.Unmarshal(data, &a.meta); err != nil {
		return fmt.Errorf("action %s: parse action.yml: %w", a.ref, err)
	}
	return nil
}

// Downloads every fetched action before the first step
func (j *job) prepareActions(ctx context.Context) error {
	for _, st := range j.task.Steps {
		if !fetchedAction(st.Uses) {
			continue
		}
		act, err := j.resolveAction(ctx, strings.TrimSpace(st.Uses), j.workspace, j.log)
		if err != nil {
			return err
		}
		if err := j.prepareNested(ctx, act, 0); err != nil {
			return err
		}
	}
	return nil
}

// Composite actions pull in the remote actions their steps use
func (j *job) prepareNested(ctx context.Context, act *actionDef, depth int) error {
	if depth > maxActionDepth || !act.isComposite() {
		return nil
	}
	for _, st := range act.meta.Runs.Steps {
		if !fetchedAction(st.Uses) {
			continue
		}
		nested, err := j.resolveAction(ctx, strings.TrimSpace(st.Uses), act.root, j.log)
		if err != nil {
			return err
		}
		if err := j.prepareNested(ctx, nested, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// One task step whose action declares a pre phase
type preStep struct {
	index int
	row   int
	act   *actionDef
}

// Gives every pre phase its own row after Set up job
func (j *job) planPreSteps() {
	var names []string
	for i, ts := range j.task.Steps {
		if !fetchedAction(ts.Uses) {
			continue
		}
		j.mu.Lock()
		act := j.prepared[strings.TrimSpace(ts.Uses)]
		j.mu.Unlock()
		if act == nil || !act.hasPre() {
			continue
		}
		j.preSteps = append(j.preSteps, preStep{index: i, row: 1 + len(names), act: act})
		names = append(names, "Pre "+ts.Name)
	}
	if len(names) > 0 {
		j.insertRows(1, names)
	}
}

// Runs one pre phase as its own step row
func (j *job) runPreStep(ctx context.Context, p preStep) {
	ts := j.task.Steps[p.index]
	st := taskStepSpec(ts)
	if j.hardCancel.Load() {
		j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED)
		return
	}
	wasStopped := j.stopped()
	sc := j.jobScope(p.row)
	ectx := sc.exprContext()
	name, err := interpolate(st.Name, ectx)
	if err != nil {
		j.log.Line("##[group]Pre %s", ts.Name)
		j.fail(p.row, "step %q: name: %v", ts.Name, err)
		j.log.Line("##[endgroup]")
		j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)
		j.sync(ctx)
		return
	}
	if name == "" {
		name = defaultStepName(st)
	}
	j.nameStep(p.row, "Pre "+name)
	cond := p.act.meta.Runs.PreIf
	if cond == "" {
		cond = "always()"
	}
	ok, err := evalCondition(cond, ectx)
	if err != nil {
		j.log.Line("##[group]Pre %s", name)
		j.fail(p.row, "%s: bad pre-if expression: %v", p.act.ref, err)
		j.log.Line("##[endgroup]")
		j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)
		j.sync(ctx)
		return
	}
	if !ok {
		j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED)
		return
	}
	j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_RUNNING, 0)
	j.sync(ctx)
	if j.hardCancel.Load() || (!wasStopped && j.stopped()) {
		j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED)
		return
	}
	j.log.Line("##[group]Pre %s", name)
	started := time.Now()
	var limit int32
	if wasStopped {
		limit = postStepMinutes
	}
	sctx, cancel := j.stepContext(ctx, limit, wasStopped)
	err = j.runPre(sctx, sc, st, p.index, p.act, name)
	interrupted := !wasStopped && j.stopped()
	cancel()
	outcome := "success"
	switch {
	case interrupted:
		outcome = "cancelled"
	case err != nil:
		outcome = "failure"
		j.log.Line("##[error]%v", err)
		j.annotate(Annotation{Level: "error", Message: fmt.Sprintf("Pre %s: %v", name, err), Step: j.rowNumber(p.row)})
		j.markFailed()
	}
	j.log.Line("##[endgroup]")
	j.setStep(p.row, v1.ActionsStatus_ACTIONS_STATUS_COMPLETED, protoConclusion(outcome))
	j.r.out.Printf("job %s: pre step %d %q %s in %s", shortID(j.task.JobId), j.rowNumber(p.row), name, outcome, time.Since(started).Round(time.Millisecond))
	j.sync(ctx)
}

// Runs the pre phase with the step's inputs and queues the post phase
func (j *job) runPre(ctx context.Context, sc *stepScope, st stepSpec, index int, act *actionDef, name string) error {
	ectx := sc.exprContext()
	with, err := interpolateMap(st.With, ectx)
	if err != nil {
		return fmt.Errorf("with: %w", err)
	}
	stepEnv, err := interpolateMap(st.Env, ectx)
	if err != nil {
		return fmt.Errorf("env: %w", err)
	}
	files, err := j.newCommandFiles()
	if err != nil {
		return err
	}
	defer files.cleanup()
	env := sc.baseEnv()
	maps.Copy(env, stepEnv)
	env["GITHUB_ACTION"] = j.stepActions[index]
	files.apply(j, env)
	state := map[string]string{}
	cw := j.newCommandWriter(sc.out, j.rowNumber(sc.row), map[string]string{}, state)
	call, err := j.prepareCall(act, with, env, ectx.withEnv(stepEnv), strconv.Itoa(index), name, cw)
	if err != nil {
		return err
	}
	j.registerActionPost(call)
	err = j.runPhase(ctx, call, act.meta.Runs.PreEntrypoint, act.meta.Runs.Pre, call.env, cw)
	cw.Flush()
	j.applyCommandFiles(files, map[string]string{})
	maps.Copy(state, files.stateValues())
	j.mergeState(call.stateKey, state)
	return firstErr(err, cw.failure())
}

// One invocation of an action with its inputs and environment resolved
type actionCall struct {
	act    *actionDef
	with   map[string]string
	inputs map[string]any
	// Full environment the action's processes receive
	env map[string]string
	// github context with the action fields filled in
	github map[string]any
	// Key pre, main, and post share their state under
	stateKey string
	// Display name of the step, post rows are named after it
	name string
}

// Validates inputs, fills defaults, and builds the action environment
func (j *job) prepareCall(act *actionDef, with, env map[string]string, ectx *exprContext, stateKey, name string, out io.Writer) (*actionCall, error) {
	for k, def := range act.meta.Inputs {
		if _, ok := with[k]; !ok && def.Required && def.Default == nil {
			return nil, fmt.Errorf("action %s: input required and not supplied: %s", act.ref, k)
		}
	}
	if act.isDocker() || act.isNode() || act.isComposite() {
		if unexpected := unexpectedInputs(act, with); len(unexpected) > 0 {
			fmt.Fprintf(out, "##[warning]Unexpected input(s) %s, valid inputs are [%s]\n", quoteInputs(unexpected), quoteInputs(sortedInputs(act)))
		}
	}
	inputs := map[string]any{}
	for k, def := range act.meta.Inputs {
		if v, ok := with[k]; ok {
			inputs[k] = v
			continue
		}
		if def.Default != nil {
			v, err := interpolate(stringify(def.Default), ectx)
			if err != nil {
				return nil, fmt.Errorf("action %s: default of input %s: %w", act.ref, k, err)
			}
			inputs[k] = v
		}
	}
	for k, v := range with {
		if _, ok := inputs[k]; !ok {
			inputs[k] = v
		}
	}
	call := &actionCall{act: act, with: with, inputs: inputs, stateKey: stateKey, name: name}
	call.env = make(map[string]string, len(env)+len(inputs)+4)
	maps.Copy(call.env, env)
	// Composite steps see inputs through the context alone
	if act.isNode() || act.isDocker() {
		for k, v := range inputs {
			call.env["INPUT_"+strings.ToUpper(strings.ReplaceAll(k, " ", "_"))] = stringify(v)
		}
	}
	call.env["GITHUB_ACTION_REPOSITORY"] = act.repo
	call.env["GITHUB_ACTION_REF"] = act.version
	if act.dir != "" {
		call.env["GITHUB_ACTION_PATH"] = j.pathOf(act.dir)
	}
	call.github = make(map[string]any, len(ectx.github)+4)
	maps.Copy(call.github, ectx.github)
	call.github["action"] = env["GITHUB_ACTION"]
	call.github["action_repository"] = act.repo
	call.github["action_ref"] = act.version
	if act.dir != "" {
		call.github["action_path"] = j.pathOf(act.dir)
	}
	actx := *ectx
	actx.inputs = inputs
	actx.github = call.github
	for k, v := range act.meta.Runs.Env {
		s, err := interpolate(stringify(v), &actx)
		if err != nil {
			return nil, fmt.Errorf("action %s: runs.env %s: %w", act.ref, k, err)
		}
		call.env[k] = s
	}
	return call, nil
}

func unexpectedInputs(act *actionDef, with map[string]string) []string {
	var out []string
	for k := range with {
		if _, ok := act.meta.Inputs[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedInputs(act *actionDef) []string {
	out := make([]string, 0, len(act.meta.Inputs))
	for k := range act.meta.Inputs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func quoteInputs(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "'" + it + "'"
	}
	return strings.Join(quoted, ", ")
}

// Expression context inside the action
func (c *actionCall) exprContext(base *exprContext) *exprContext {
	actx := *base
	actx.inputs = c.inputs
	actx.github = c.github
	return &actx
}

// Dispatches on runs.using
func (j *job) runAction(ctx context.Context, sc *stepScope, call *actionCall, outputs map[string]string, out io.Writer) error {
	if sc.depth > maxActionDepth {
		return fmt.Errorf("action nesting deeper than %d", maxActionDepth)
	}
	act := call.act
	switch {
	case act.isComposite():
		return j.runComposite(ctx, sc, call, outputs, out)
	case act.isDocker():
		return j.runDockerAction(ctx, call, out)
	case act.isNode():
		return j.runNodeAction(ctx, call, out)
	}
	return fmt.Errorf("action %s: unsupported runs.using %q", act.ref, act.meta.Runs.Using)
}

func (j *job) runComposite(ctx context.Context, parent *stepScope, call *actionCall, outputs map[string]string, out io.Writer) error {
	sc := &stepScope{j: j, row: parent.row, steps: map[string]any{}, call: call, stateKey: call.stateKey, out: out, depth: parent.depth + 1}
	for i, as := range call.act.meta.Runs.Steps {
		st := as.spec()
		ectx := sc.exprContext()
		ok, err := evalCondition(st.If, ectx)
		if err != nil {
			return fmt.Errorf("composite step %d: bad if expression: %w", i+1, err)
		}
		if !ok {
			sc.record(st.ID, "skipped", "skipped", nil)
			continue
		}
		name, err := interpolate(st.Name, ectx)
		if err != nil {
			return fmt.Errorf("composite step %d: name: %w", i+1, err)
		}
		if name == "" {
			name = defaultStepName(st)
		}
		continueOnError, err := st.continueOnError(ectx)
		if err != nil {
			return fmt.Errorf("composite step %d: %w", i+1, err)
		}
		fmt.Fprintf(out, "##[group]%s\n", name)
		stepOut, err := sc.exec(ctx, stepRun{spec: st, ectx: ectx, name: name, action: j.nextActionName(st), key: sc.stateKey + "/" + strconv.Itoa(i)})
		if err != nil {
			fmt.Fprintf(out, "##[error]%v\n", err)
		}
		fmt.Fprint(out, "##[endgroup]\n")
		outcome, conclusion := stepOutcome(err, continueOnError)
		sc.record(st.ID, outcome, conclusion, stepOut)
		if conclusion == "failure" {
			sc.status = "failure"
		}
	}
	final := sc.exprContext()
	for k, def := range call.act.meta.Outputs {
		v, err := interpolate(def.Value, final)
		if err != nil {
			return fmt.Errorf("action %s: output %s: %w", call.act.ref, k, err)
		}
		outputs[k] = v
	}
	if sc.status == "failure" {
		return errors.New("composite action failed")
	}
	return nil
}

func (j *job) runNodeAction(ctx context.Context, call *actionCall, out io.Writer) error {
	act := call.act
	if act.meta.Runs.Main == "" {
		return fmt.Errorf("action %s has no runs.main", act.ref)
	}
	j.registerActionPost(call)
	return j.runNodeScript(ctx, call, act.meta.Runs.Main, call.env, out)
}

// Docker actions run an entrypoint, node actions a script
func (j *job) runPhase(ctx context.Context, call *actionCall, entrypoint, script string, env map[string]string, out io.Writer) error {
	if call.act.isDocker() {
		return j.runDockerPhase(ctx, call, entrypoint, j.dockerActionArgs(call), env, out)
	}
	return j.runNodeScript(ctx, call, script, env, out)
}

// Runs one javascript action script with the shared state
func (j *job) runNodeScript(ctx context.Context, call *actionCall, script string, env map[string]string, out io.Writer) error {
	act := call.act
	node, nodeEnv, err := j.nodeCommand(ctx, act.meta.Runs.Using)
	if err != nil {
		return fmt.Errorf("action %s: %w", act.ref, err)
	}
	env = maps.Clone(env)
	for k, v := range j.stateFor(call.stateKey) {
		env["STATE_"+k] = v
	}
	main := j.pathOf(filepath.Join(act.dir, filepath.FromSlash(script)))
	code, err := j.exec.Exec(ctx, execSpec{Cmd: append(append([]string{}, node...), main), Env: append(append([]string{}, nodeEnv...), envSlice(env)...), Dir: j.pathOf(j.workspace), Out: out, Path: j.pathAdds()})
	return exitErr(code, err)
}

// Queues the post phase once, from pre or main, whichever runs first
func (j *job) registerActionPost(call *actionCall) {
	if !call.act.hasPost() {
		return
	}
	key := call.stateKey
	j.mu.Lock()
	_, queued := j.actionPosts[key]
	j.actionPosts[key] = call
	j.mu.Unlock()
	if queued {
		return
	}
	j.registerPost(call.name, func(ctx context.Context, cw *commandWriter) error {
		j.mu.Lock()
		latest := j.actionPosts[key]
		j.mu.Unlock()
		return j.runActionPost(ctx, latest, cw)
	})
}

// Post phase under post-if with the inputs main resolved
func (j *job) runActionPost(ctx context.Context, call *actionCall, cw *commandWriter) error {
	ectx := call.exprContext(j.exprContext())
	cond := call.act.meta.Runs.PostIf
	if cond == "" {
		cond = "always()"
	}
	ok, err := evalCondition(cond, ectx)
	if err != nil {
		return fmt.Errorf("bad post-if expression: %w", err)
	}
	if !ok {
		return errSkipped
	}
	// Later GITHUB_ENV additions apply on top of the captured env
	env := maps.Clone(call.env)
	j.mu.Lock()
	maps.Copy(env, j.env)
	j.mu.Unlock()
	files, err := j.newCommandFiles()
	if err != nil {
		return err
	}
	defer files.cleanup()
	files.apply(j, env)
	err = j.runPhase(ctx, call, call.act.meta.Runs.PostEntrypoint, call.act.meta.Runs.Post, env, cw)
	j.applyCommandFiles(files, map[string]string{})
	return err
}

// Queues cleanup that runs after the steps in reverse order
func (j *job) registerPost(name string, run func(ctx context.Context, cw *commandWriter) error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.post = append(j.post, postStep{name: name, run: run})
}

func (j *job) runDockerAction(ctx context.Context, call *actionCall, out io.Writer) error {
	act := call.act
	entrypoint := act.meta.Runs.Entrypoint
	// Workflow entrypoint overrides the action's own for main alone
	if v, ok := call.with["entrypoint"]; ok && strings.TrimSpace(v) != "" {
		entrypoint = strings.TrimSpace(v)
	}
	j.registerActionPost(call)
	return j.runDockerPhase(ctx, call, entrypoint, j.dockerActionArgs(call), call.env, out)
}

// Container arguments, with.args wins over the action's runs.args
func (j *job) dockerActionArgs(call *actionCall) []string {
	if v, ok := call.with["args"]; ok && strings.TrimSpace(v) != "" {
		return splitShellWords(v)
	}
	ectx := call.exprContext(j.exprContext())
	var args []string
	for _, a := range call.act.meta.Runs.Args {
		v, err := interpolate(a, ectx)
		if err != nil {
			v = a
		}
		args = append(args, v)
	}
	return args
}

// Mounts of a docker action the way the hosted runner lays them out
const (
	actionWorkspaceMount    = "/github/workspace"
	actionHomeMount         = "/github/home"
	actionWorkflowMount     = "/github/workflow"
	actionFileCommandsMount = "/github/file_commands"
)

// Executor path of the job dir as a docker action container sees it
func (j *job) dockerActionPath(execPath string) string {
	root := j.pathOf(j.dir)
	rel, ok := strings.CutPrefix(execPath, root)
	if !ok || (rel != "" && rel[0] != '/') {
		return execPath
	}
	host := filepath.Join(j.dir, filepath.FromSlash(rel))
	for _, m := range []struct{ host, mount string }{
		{j.workspace, actionWorkspaceMount}, {j.homeDir(), actionHomeMount}, {j.workflowDir(), actionWorkflowMount}, {j.fileCommandsDir(), actionFileCommandsMount},
	} {
		if within(m.host, host) {
			r, _ := filepath.Rel(m.host, host)
			return path.Join(m.mount, filepath.ToSlash(r))
		}
	}
	return execPath
}

// Resolves the action image and runs it once with the step's state
func (j *job) runDockerPhase(ctx context.Context, call *actionCall, entrypoint string, args []string, env map[string]string, out io.Writer) error {
	act := call.act
	cli, err := j.dockerClient()
	if err != nil {
		return err
	}
	env = maps.Clone(env)
	for k, v := range j.stateFor(call.stateKey) {
		env["STATE_"+k] = v
	}
	image := act.meta.Runs.Image
	switch {
	case strings.HasPrefix(image, "docker://"):
		image = strings.TrimPrefix(image, "docker://")
		if err := pullImage(ctx, cli, image, j.pullAuth(image), out); err != nil {
			return err
		}
	case image != "":
		tag := "dfcli-action-" + shortID(strings.ToLower(strings.NewReplacer("/", "-", "@", "-", ".", "-").Replace(act.ref))) + ":" + shortID(j.task.JobId)
		if _, err := cli.ImageInspect(ctx, tag); err != nil {
			fmt.Fprintf(out, "Building action image %s\n", tag)
			if err := j.buildLocalImage(ctx, cli, act.dir, image, tag, out); err != nil {
				return err
			}
		}
		image = tag
	default:
		return fmt.Errorf("action %s has no runs.image", act.ref)
	}
	var entry []string
	if entrypoint != "" {
		entry = []string{entrypoint}
	}
	for k, v := range env {
		env[k] = j.dockerActionPath(v)
	}
	env["HOME"] = actionHomeMount
	binds := []string{
		j.dir + ":" + j.pathOf(j.dir),
		j.workspace + ":" + actionWorkspaceMount,
		j.homeDir() + ":" + actionHomeMount,
		j.workflowDir() + ":" + actionWorkflowMount,
		j.fileCommandsDir() + ":" + actionFileCommandsMount,
	}
	if j.r.cfg.PrivilegedJobs {
		if sock := dockerSocket(); sock != "" {
			binds = append(binds, sock+":/var/run/docker.sock")
		}
	}
	code, err := runContainer(ctx, cli, containerRun{
		Image: image, Cmd: args, Entrypoint: entry, Env: envSlice(env), Dir: actionWorkspaceMount,
		Binds: binds, Network: j.network, Hosts: j.hosts, Labels: j.r.labels(j.task.JobId), Out: out,
	})
	return exitErr(code, err)
}

// Namespaces reserved on the instance resolve on github.com alone
func githubOnly(repo string) bool {
	owner, _, _ := strings.Cut(repo, "/")
	switch strings.ToLower(owner) {
	case "actions", "docker", "github":
		return true
	}
	return false
}
