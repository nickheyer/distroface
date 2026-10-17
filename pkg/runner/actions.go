package runner

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gopkg.in/yaml.v3"
)

const maxActionDepth = 10

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Native handlers first, anything else is a fetched action
func (j *job) runUses(ctx context.Context, st *v1.TaskStep, env map[string]string, ectx *exprContext, outputs map[string]string, out io.Writer) error {
	ref := strings.TrimSpace(interpolate(st.Uses, ectx))
	with := interpolateMap(st.With, ectx)
	name, _, _ := strings.Cut(ref, "@")
	switch strings.ToLower(name) {
	case "actions/checkout":
		return j.checkout(ctx, with, env, out)
	case "actions/upload-artifact":
		return j.uploadArtifact(ctx, with, out)
	case "actions/download-artifact":
		return j.downloadArtifact(ctx, with, out)
	}
	act, err := j.resolveAction(ctx, ref, j.workspace, out)
	if err != nil {
		return err
	}
	return j.runAction(ctx, act, with, env, ectx, outputs, out, 0)
}

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
		Using      string         `yaml:"using"`
		Main       string         `yaml:"main"`
		Post       string         `yaml:"post"`
		Image      string         `yaml:"image"`
		Entrypoint string         `yaml:"entrypoint"`
		Args       []string       `yaml:"args"`
		Env        map[string]any `yaml:"env"`
		Steps      []actionStep   `yaml:"steps"`
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
	ContinueOnError  bool           `yaml:"continue-on-error"`
}

type actionDef struct {
	ref  string
	repo string
	dir  string
	root string
	meta actionMeta
}

// Locates or fetches the action and reads its metadata
func (j *job) resolveAction(ctx context.Context, ref, localBase string, out io.Writer) (*actionDef, error) {
	if strings.HasPrefix(ref, "docker://") {
		act := &actionDef{ref: ref, repo: ref}
		act.meta.Runs.Using = "docker"
		act.meta.Runs.Image = ref
		return act, nil
	}
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		dir := filepath.Join(localBase, filepath.FromSlash(ref))
		act := &actionDef{ref: ref, repo: ref, dir: dir, root: localBase}
		return act, act.load()
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
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		if err := j.fetchAction(ctx, repo, version, root, out); err != nil {
			return nil, err
		}
	}
	act := &actionDef{ref: ref, repo: repo, dir: filepath.Join(root, filepath.FromSlash(sub)), root: root}
	return act, act.load()
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
	var errs []string
	if base := j.cloneBase(); base != "" && !githubOnly(repo) {
		fmt.Fprintf(out, "Fetching action %s@%s from %s\n", repo, version, base)
		err := j.cloneHost(ctx, base+"/"+repo+".git", dest, version, 1, j.task.Token)
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", base, err))
	}
	fmt.Fprintf(out, "Fetching action %s@%s from github.com\n", repo, version)
	if err := j.cloneHost(ctx, "https://github.com/"+repo+".git", dest, version, 1, ""); err != nil {
		errs = append(errs, fmt.Sprintf("github.com: %v", err))
		return fmt.Errorf("fetch action %s@%s: %s", repo, version, strings.Join(errs, "; "))
	}
	return nil
}

// Instance base url derived from the clone url
func (j *job) cloneBase() string {
	u := strings.TrimSuffix(j.task.CloneUrl, ".git")
	if i := strings.LastIndex(u, "/"+j.task.RepoFullName); i > 0 {
		return u[:i]
	}
	return strings.TrimRight(j.task.ServerUrl, "/")
}

func (a *actionDef) load() error {
	var data []byte
	var err error
	for _, name := range []string{"action.yml", "action.yaml"} {
		data, err = os.ReadFile(filepath.Join(a.dir, name))
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

// Dispatches on runs.using
func (j *job) runAction(ctx context.Context, act *actionDef, with map[string]string, env map[string]string, ectx *exprContext, outputs map[string]string, out io.Writer, depth int) error {
	if depth > maxActionDepth {
		return fmt.Errorf("action nesting deeper than %d", maxActionDepth)
	}
	inputs := map[string]any{}
	for k, def := range act.meta.Inputs {
		if v, ok := with[k]; ok {
			inputs[k] = v
			continue
		}
		if def.Default != nil {
			inputs[k] = interpolate(stringify(def.Default), ectx)
		} else if def.Required {
			return fmt.Errorf("action %s: input %q is required", act.ref, k)
		}
	}
	for k, v := range with {
		if _, ok := inputs[k]; !ok {
			inputs[k] = v
		}
	}
	aenv := make(map[string]string, len(env)+len(inputs)+4)
	for k, v := range env {
		aenv[k] = v
	}
	for k, v := range inputs {
		aenv["INPUT_"+strings.ToUpper(strings.ReplaceAll(k, " ", "_"))] = stringify(v)
	}
	aenv["GITHUB_ACTION"] = act.ref
	aenv["GITHUB_ACTION_REPOSITORY"] = act.repo
	if act.dir != "" {
		aenv["GITHUB_ACTION_PATH"] = j.pathOf(act.dir)
	}
	actx := *ectx
	actx.inputs = inputs
	for k, v := range act.meta.Runs.Env {
		aenv[k] = interpolate(stringify(v), &actx)
	}
	using := strings.ToLower(act.meta.Runs.Using)
	switch {
	case using == "composite":
		return j.runComposite(ctx, act, aenv, &actx, outputs, out, depth)
	case using == "docker":
		return j.runDockerAction(ctx, act, aenv, &actx, out)
	case strings.HasPrefix(using, "node"):
		return j.runNodeAction(ctx, act, aenv, inputs, out)
	}
	return fmt.Errorf("action %s: unsupported runs.using %q", act.ref, act.meta.Runs.Using)
}

func (j *job) runComposite(ctx context.Context, act *actionDef, env map[string]string, ectx *exprContext, outputs map[string]string, out io.Writer, depth int) error {
	local := map[string]any{}
	status := ""
	for i, st := range act.meta.Runs.Steps {
		sctx := *ectx
		sctx.steps = local
		sctx.status = status
		ok, err := evalCondition(st.If, &sctx)
		if err != nil {
			return fmt.Errorf("composite step %d: bad if expression: %w", i+1, err)
		}
		if !ok {
			if st.ID != "" {
				local[st.ID] = map[string]any{"outcome": "skipped", "conclusion": "skipped", "outputs": map[string]any{}}
			}
			continue
		}
		name := interpolate(st.Name, &sctx)
		if name == "" {
			name = fmt.Sprintf("step %d", i+1)
		}
		fmt.Fprintf(out, "##[group]%s\n", name)
		stepOut, err := j.runCompositeStep(ctx, act, st, env, &sctx, out, depth)
		fmt.Fprint(out, "##[endgroup]\n")
		outcome := "success"
		if err != nil {
			outcome = "failure"
			fmt.Fprintf(out, "##[error]%v\n", err)
		}
		conclusion := outcome
		if err != nil && st.ContinueOnError {
			conclusion = "success"
		}
		if st.ID != "" {
			outs := map[string]any{}
			for k, v := range stepOut {
				outs[k] = v
			}
			local[st.ID] = map[string]any{"outcome": outcome, "conclusion": conclusion, "outputs": outs}
		}
		if conclusion == "failure" {
			status = "failure"
		}
	}
	final := *ectx
	final.steps = local
	for k, def := range act.meta.Outputs {
		outputs[k] = interpolate(def.Value, &final)
	}
	if status == "failure" {
		return errors.New("composite action failed")
	}
	return nil
}

func (j *job) runCompositeStep(ctx context.Context, act *actionDef, st actionStep, env map[string]string, ectx *exprContext, out io.Writer, depth int) (map[string]string, error) {
	files, err := j.newCommandFiles()
	if err != nil {
		return nil, err
	}
	defer files.cleanup()
	senv := make(map[string]string, len(env))
	for k, v := range env {
		senv[k] = v
	}
	j.mu.Lock()
	for k, v := range j.env {
		senv[k] = v
	}
	j.mu.Unlock()
	for k, v := range st.Env {
		senv[k] = interpolate(stringify(v), ectx)
	}
	files.apply(j, senv)
	outputs := map[string]string{}
	cw := &commandWriter{out: out, onMask: j.log.AddMask, onOutput: func(k, v string) { outputs[k] = v }}
	cw.onAnnotation = func(a Annotation) { j.annotate(a) }
	if st.Uses != "" {
		with := map[string]string{}
		for k, v := range st.With {
			with[k] = interpolate(stringify(v), ectx)
		}
		ref := interpolate(st.Uses, ectx)
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
			nested, err := j.resolveAction(ctx, ref, act.root, out)
			if err != nil {
				nested, err = j.resolveAction(ctx, ref, j.workspace, out)
			}
			if err != nil {
				return nil, err
			}
			err = j.runAction(ctx, nested, with, senv, ectx, outputs, cw, depth+1)
			cw.Flush()
			j.applyCommandFiles(files, outputs)
			return outputs, firstErr(err, cw.failure())
		}
		name, _, _ := strings.Cut(ref, "@")
		switch strings.ToLower(name) {
		case "actions/checkout":
			err = j.checkout(ctx, with, senv, cw)
		case "actions/upload-artifact":
			err = j.uploadArtifact(ctx, with, cw)
		case "actions/download-artifact":
			err = j.downloadArtifact(ctx, with, cw)
		default:
			var nested *actionDef
			nested, err = j.resolveAction(ctx, ref, j.workspace, out)
			if err == nil {
				err = j.runAction(ctx, nested, with, senv, ectx, outputs, cw, depth+1)
			}
		}
		cw.Flush()
		j.applyCommandFiles(files, outputs)
		return outputs, firstErr(err, cw.failure())
	}
	dir := j.workDir(interpolate(st.WorkingDirectory, ectx))
	code, err := j.runScript(ctx, interpolate(st.Run, ectx), st.Shell, dir, senv, cw)
	cw.Flush()
	j.applyCommandFiles(files, outputs)
	return outputs, firstErr(exitErr(code, err), cw.failure())
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (j *job) runNodeAction(ctx context.Context, act *actionDef, env map[string]string, inputs map[string]any, out io.Writer) error {
	if !j.exec.Has(ctx, "node") {
		return fmt.Errorf("action %s needs node in the job environment", act.ref)
	}
	if act.meta.Runs.Main == "" {
		return fmt.Errorf("action %s has no runs.main", act.ref)
	}
	files, err := j.newCommandFiles()
	if err != nil {
		return err
	}
	main := j.pathOf(filepath.Join(act.dir, filepath.FromSlash(act.meta.Runs.Main)))
	env["GITHUB_STATE"] = j.pathOf(files.state)
	code, err := j.exec.Exec(ctx, execSpec{Cmd: j.withPath([]string{"node", main}), Env: envSlice(env), Dir: j.pathOf(j.workspace), Out: out})
	state := files.stateValues()
	files.cleanup()
	if post := act.meta.Runs.Post; post != "" {
		postPath := j.pathOf(filepath.Join(act.dir, filepath.FromSlash(post)))
		j.mu.Lock()
		j.post = append(j.post, postStep{name: act.ref, run: func(ctx context.Context) error {
			penv := j.baseEnv()
			for k, v := range inputs {
				penv["INPUT_"+strings.ToUpper(strings.ReplaceAll(k, " ", "_"))] = stringify(v)
			}
			for k, v := range state {
				penv["STATE_"+k] = v
			}
			penv["GITHUB_ACTION"], penv["GITHUB_ACTION_PATH"], penv["GITHUB_ACTION_REPOSITORY"] = act.ref, j.pathOf(act.dir), act.repo
			code, err := j.exec.Exec(ctx, execSpec{Cmd: j.withPath([]string{"node", postPath}), Env: envSlice(penv), Dir: j.pathOf(j.workspace), Out: j.log})
			return exitErr(code, err)
		}})
		j.mu.Unlock()
	}
	return exitErr(code, err)
}

func (j *job) runDockerAction(ctx context.Context, act *actionDef, env map[string]string, ectx *exprContext, out io.Writer) error {
	cli := j.docker
	if cli == nil {
		var err error
		if cli, err = newDockerClient(j.r.cfg.DockerHost); err != nil {
			return err
		}
		j.docker = cli
	}
	image := act.meta.Runs.Image
	switch {
	case strings.HasPrefix(image, "docker://"):
		image = strings.TrimPrefix(image, "docker://")
		if _, err := cli.ImageInspect(ctx, image); err != nil {
			fmt.Fprintf(out, "Pulling image %s\n", image)
			rc, err := cli.ImagePull(ctx, image, imagePullOptions())
			if err != nil {
				return fmt.Errorf("pull %s: %w", image, err)
			}
			err = drainJSONStream(rc, nil)
			rc.Close()
			if err != nil {
				return err
			}
		}
	case image != "":
		tag := "dfcli-action-" + shortID(strings.ToLower(strings.NewReplacer("/", "-", "@", "-", ".", "-").Replace(act.ref))) + ":" + shortID(j.task.JobId)
		fmt.Fprintf(out, "Building action image %s\n", tag)
		if err := buildImage(ctx, cli, act.dir, image, tag, out); err != nil {
			return err
		}
		image = tag
	default:
		return fmt.Errorf("action %s has no runs.image", act.ref)
	}
	var args []string
	for _, a := range act.meta.Runs.Args {
		args = append(args, interpolate(a, ectx))
	}
	var entrypoint []string
	if act.meta.Runs.Entrypoint != "" {
		entrypoint = []string{act.meta.Runs.Entrypoint}
	}
	mount := j.dir + ":" + j.pathOf(j.dir)
	code, err := runContainer(ctx, cli, containerRun{
		Image: image, Cmd: args, Entrypoint: entrypoint, Env: envSlice(env), Dir: j.pathOf(j.workspace),
		Binds: []string{mount}, Network: j.network, Out: out,
	})
	return exitErr(code, err)
}

// actions/checkout

func (j *job) checkout(ctx context.Context, with map[string]string, env map[string]string, out io.Writer) error {
	repo := strings.TrimSpace(with["repository"])
	if repo == "" {
		repo = j.task.RepoFullName
	}
	ref := strings.TrimSpace(with["ref"])
	sha := ""
	if repo == j.task.RepoFullName && ref == "" {
		sha = stringify(j.github["sha"])
		ref = stringify(j.github["ref"])
	}
	if shaPattern.MatchString(ref) {
		sha, ref = ref, ""
	}
	if ref != "" && !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	token := with["token"]
	if token == "" {
		token = j.task.Token
	}
	depth := 1
	if v := strings.TrimSpace(with["fetch-depth"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			depth = n
		}
	}
	dest := j.workspace
	if p := strings.TrimSpace(with["path"]); p != "" {
		dest = filepath.Join(j.workspace, filepath.FromSlash(p))
	}
	url := j.task.CloneUrl
	if repo != j.task.RepoFullName {
		url = j.cloneBase() + "/" + repo + ".git"
	}
	if err := os.MkdirAll(dest, 0o777); err != nil {
		return err
	}
	_ = os.Chmod(dest, 0o777)
	fmt.Fprintf(out, "Checking out %s at %s into %s\n", repo, firstNonEmpty(sha, ref), j.pathOf(dest))
	if j.exec.Has(ctx, "git") {
		return j.checkoutWithGit(ctx, url, j.pathOf(dest), ref, sha, depth, token, with, env, out)
	}
	fmt.Fprintln(out, "No git in the job environment, cloning from the runner host")
	if err := j.cloneHost(ctx, url, dest, firstNonEmpty(sha, ref), depth, token); err != nil {
		return err
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Fetches inside the job environment with basic auth
func (j *job) checkoutWithGit(ctx context.Context, url, dest, ref, sha string, depth int, token string, with map[string]string, env map[string]string, out io.Writer) error {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	fmt.Fprintf(&sb, "cd %s\n", shellQuote(dest))
	sb.WriteString("git init -q .\n")
	sb.WriteString("git config --global --add safe.directory '*' 2>/dev/null || true\n")
	if token != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		j.log.AddMask(b64)
		scope := url
		if u, err := neturl.Parse(url); err == nil && u.Host != "" {
			scope = u.Scheme + "://" + u.Host + "/"
		}
		fmt.Fprintf(&sb, "git config --local http.%s.extraheader %s\n", shellQuote(scope), shellQuote("AUTHORIZATION: basic "+b64))
	}
	fmt.Fprintf(&sb, "git remote add origin %s 2>/dev/null || git remote set-url origin %s\n", shellQuote(url), shellQuote(url))
	depthArg := ""
	if depth > 0 {
		depthArg = fmt.Sprintf("--depth=%d ", depth)
	}
	target := firstNonEmpty(sha, ref)
	if sha != "" && ref != "" {
		fmt.Fprintf(&sb, "git fetch %sorigin %s 2>/dev/null || git fetch %sorigin %s\n", depthArg, shellQuote(sha), depthArg, shellQuote(ref))
	} else {
		fmt.Fprintf(&sb, "git fetch %sorigin %s\n", depthArg, shellQuote(target))
	}
	if sha != "" {
		fmt.Fprintf(&sb, "git checkout -q --force %s 2>/dev/null || git checkout -q --force FETCH_HEAD\n", shellQuote(sha))
	} else {
		sb.WriteString("git checkout -q --force FETCH_HEAD\n")
	}
	if ref != "" && strings.HasPrefix(ref, "refs/heads/") {
		branch := strings.TrimPrefix(ref, "refs/heads/")
		fmt.Fprintf(&sb, "git checkout -q -B %s 2>/dev/null || true\n", shellQuote(branch))
	}
	switch strings.ToLower(strings.TrimSpace(with["submodules"])) {
	case "true":
		sb.WriteString("git submodule update --init\n")
	case "recursive":
		sb.WriteString("git submodule update --init --recursive\n")
	}
	sb.WriteString("git log -1 --format='HEAD is now at %h %s'\n")
	code, err := j.runScript(ctx, sb.String(), "sh", dest, env, out)
	return exitErr(code, err)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Host side go-git clone trying branch, tag, then sha
func (j *job) cloneHost(ctx context.Context, url, dest, ref string, depth int, token string) error {
	var copts []client.Option
	if j.r.http != nil {
		copts = append(copts, client.WithHTTPClient(j.r.http))
	}
	if token != "" {
		copts = append(copts, client.WithHTTPAuth(&githttp.BasicAuth{Username: "x-access-token", Password: token}))
	}
	ref = strings.TrimSpace(ref)
	if err := os.MkdirAll(dest, 0o777); err != nil {
		return err
	}
	if entries, _ := os.ReadDir(dest); len(entries) > 0 {
		return fmt.Errorf("destination %s is not empty", dest)
	}
	clone := func(name plumbing.ReferenceName, d int) error {
		_, err := git.PlainCloneContext(ctx, dest, &git.CloneOptions{URL: url, ReferenceName: name, SingleBranch: name != "", Depth: d, ClientOptions: copts, Tags: git.NoTags})
		if err != nil {
			_ = os.RemoveAll(dest)
			_ = os.MkdirAll(dest, 0o777)
		}
		return err
	}
	if ref == "" {
		return clone("", depth)
	}
	if shaPattern.MatchString(ref) {
		if err := clone("", 0); err != nil {
			return err
		}
		return checkoutHash(dest, ref)
	}
	var candidates []plumbing.ReferenceName
	if strings.HasPrefix(ref, "refs/") {
		candidates = append(candidates, plumbing.ReferenceName(ref))
	} else {
		candidates = append(candidates, plumbing.NewBranchReferenceName(ref), plumbing.NewTagReferenceName(ref))
	}
	var last error
	for _, c := range candidates {
		if last = clone(c, depth); last == nil {
			return nil
		}
	}
	if err := clone("", 0); err != nil {
		return last
	}
	return checkoutHash(dest, ref)
}

func checkoutHash(dest, rev string) error {
	repo, err := git.PlainOpen(dest)
	if err != nil {
		return err
	}
	h, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", rev, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Checkout(&git.CheckoutOptions{Hash: *h, Force: true})
}

// actions/upload-artifact and actions/download-artifact

func (j *job) uploadArtifact(ctx context.Context, with map[string]string, out io.Writer) error {
	name := strings.TrimSpace(with["name"])
	if name == "" {
		name = "artifact"
	}
	files, err := j.collectFiles(with["path"])
	if err != nil {
		return err
	}
	if len(files) == 0 {
		msg := "No files were found with the provided path: " + strings.TrimSpace(with["path"])
		switch strings.ToLower(strings.TrimSpace(with["if-no-files-found"])) {
		case "ignore":
		case "error":
			return errors.New(msg)
		default:
			fmt.Fprintf(out, "##[warning]%s\n", msg)
		}
		return nil
	}
	root := commonDir(files)
	var total int64
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		fh, err := os.Open(f)
		if err != nil {
			return err
		}
		info, _ := fh.Stat()
		_, err = j.r.client.UploadArtifact(ctx, j.task.JobId, j.task.Token, name, filepath.ToSlash(rel), fh)
		fh.Close()
		if err != nil {
			return fmt.Errorf("upload %s: %w", rel, err)
		}
		if info != nil {
			total += info.Size()
		}
	}
	fmt.Fprintf(out, "Uploaded %d file(s), %d bytes, to artifact %q\n", len(files), total, name)
	return nil
}

// Expands upload patterns against the host side mount
func (j *job) collectFiles(spec string) ([]string, error) {
	var include, exclude []string
	for _, line := range strings.Split(spec, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pat, err := j.hostPattern(strings.TrimPrefix(line, "!"))
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "!") {
			exclude = append(exclude, pat)
		} else {
			include = append(include, pat)
		}
	}
	seen := map[string]bool{}
	var files []string
	for _, pat := range include {
		if info, err := os.Stat(pat); err == nil {
			if info.IsDir() {
				pat = filepath.Join(pat, "**")
			} else {
				if !seen[pat] {
					seen[pat] = true
					files = append(files, pat)
				}
				continue
			}
		}
		matches, err := doublestar.FilepathGlob(pat, doublestar.WithFilesOnly())
		if err != nil {
			return nil, fmt.Errorf("bad pattern %q: %w", pat, err)
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	var kept []string
	for _, f := range files {
		skip := false
		for _, ex := range exclude {
			if ok, _ := doublestar.PathMatch(filepath.ToSlash(ex), filepath.ToSlash(f)); ok {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, f)
		}
	}
	for _, f := range kept {
		if !j.realInside(f) {
			return nil, fmt.Errorf("path %q resolves outside the job directory", j.pathOf(f))
		}
	}
	sort.Strings(kept)
	return kept, nil
}

// Host path for an executor path, confined to the job dir in containers
func (j *job) hostPattern(p string) (string, error) {
	p = filepath.ToSlash(p)
	root := filepath.ToSlash(j.pathOf(j.dir))
	var host string
	switch {
	case root != "" && (p == root || strings.HasPrefix(p, root+"/")):
		host = filepath.Join(j.dir, filepath.FromSlash(strings.TrimPrefix(p, root)))
	case path.IsAbs(p):
		host = filepath.FromSlash(p)
	default:
		host = filepath.Join(j.workspace, filepath.FromSlash(p))
	}
	if j.confined && !within(j.dir, host) {
		return "", fmt.Errorf("path %q is outside the job directory", p)
	}
	return host, nil
}

// Whether target sits under root after cleaning, symlinks not followed
func within(root, target string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Symlink resolved containment, always true outside containers
func (j *job) realInside(p string) bool {
	if !j.confined {
		return true
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	root, err := filepath.EvalSymlinks(j.dir)
	if err != nil {
		return false
	}
	return within(root, resolved)
}

// Creates a directory in the job, refusing symlinked escapes
func (j *job) mkdirInside(dir string) error {
	if j.confined {
		existing := dir
		for {
			if _, err := os.Lstat(existing); err == nil {
				break
			}
			parent := filepath.Dir(existing)
			if parent == existing {
				break
			}
			existing = parent
		}
		if !j.realInside(existing) {
			return fmt.Errorf("path %q resolves outside the job directory", j.pathOf(dir))
		}
	}
	return os.MkdirAll(dir, 0o777)
}

func commonDir(files []string) string {
	if len(files) == 0 {
		return ""
	}
	dir := filepath.Dir(files[0])
	for _, f := range files[1:] {
		for !strings.HasPrefix(f, dir+string(filepath.Separator)) && dir != filepath.Dir(dir) {
			dir = filepath.Dir(dir)
		}
	}
	return dir
}

func (j *job) downloadArtifact(ctx context.Context, with map[string]string, out io.Writer) error {
	name := strings.TrimSpace(with["name"])
	dest := j.workspace
	if p := strings.TrimSpace(with["path"]); p != "" {
		var err error
		if dest, err = j.hostPattern(p); err != nil {
			return err
		}
	}
	ns, repo, _ := strings.Cut(j.task.RepoFullName, "/")
	version := "actions-" + stringify(j.github["run_number"])
	prefix := ""
	if name != "" {
		prefix = sanitizeArtifactName(name) + "/"
	}
	body, err := j.r.client.DownloadArtifacts(ctx, j.task.Token, ns, repo, version, prefix)
	if errors.Is(err, errNoArtifacts) {
		return fmt.Errorf("no artifact named %q was found for this run", name)
	}
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(j.tempDir, "artifact-*.zip")
	if err != nil {
		body.Close()
		return err
	}
	size, err := io.Copy(tmp, body)
	_ = body.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	defer os.Remove(tmp.Name())
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return fmt.Errorf("artifact archive: %w", err)
	}
	defer zr.Close()
	count := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasPrefix(f.Name, version+"/") {
			continue
		}
		rel := strings.TrimPrefix(f.Name, version+"/")
		if prefix != "" {
			if !strings.HasPrefix(rel, prefix) {
				continue
			}
			rel = strings.TrimPrefix(rel, prefix)
		}
		rel = path.Clean(rel)
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := j.mkdirInside(filepath.Dir(target)); err != nil {
			return err
		}
		if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			os.Remove(target)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		_ = rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		count++
	}
	if count == 0 && name != "" {
		return fmt.Errorf("no artifact named %q was found for this run", name)
	}
	fmt.Fprintf(out, "Downloaded %d file(s), %d bytes, into %s\n", count, size, j.pathOf(dest))
	return nil
}

// Mirrors artifacts.SanitizePath on the server
func sanitizeArtifactName(name string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, name)
	s = strings.Trim(s, "-.")
	if s == "" {
		return "artifact"
	}
	return s
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
