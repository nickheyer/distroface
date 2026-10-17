package actions

import (
	"strings"
	"testing"
)

const sampleWorkflow = `
name: CI
on:
  push:
    branches: [main, 'release/**']
    tags-ignore: ['v0.*']
    paths-ignore: ['docs/**']
  pull_request:
    types: [opened, synchronize]
    branches: [main]
  workflow_dispatch:
    inputs:
      level:
        description: Log level
        type: choice
        default: info
        options: [debug, info, warn]
      dry_run:
        type: boolean
        default: 'false'
      count:
        type: number
        required: true
  schedule:
    - cron: '30 2 * * 1-5'
  release:
    types: [published]
  issues:
  issue_comment:
    types: [created]
env:
  GLOBAL: one
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
permissions:
  contents: read
jobs:
  build:
    name: Build ${{ matrix.os }}
    runs-on: [self-hosted, linux]
    timeout-minutes: 15
    continue-on-error: true
    strategy:
      fail-fast: false
      matrix:
        os: [linux, windows]
        node: [18, 20]
        exclude:
          - os: windows
            node: 18
        include:
          - os: linux
            node: 20
            experimental: true
          - os: macos
            node: 20
    env:
      JOB_ENV: two
    container:
      image: node:${{ matrix.node }}
      options: --cpus 1
    services:
      redis:
        image: redis:7
        ports: ['6379:6379']
    outputs:
      version: ${{ steps.ver.outputs.version }}
    steps:
      - uses: actions/checkout@v4
      - id: ver
        name: Version
        run: echo "version=1.0" >> $GITHUB_OUTPUT
        shell: bash
        working-directory: ./src
        env:
          STEP_ENV: three
      - run: |
          npm ci
          npm test
        if: ${{ matrix.os == 'linux' }}
        continue-on-error: true
        timeout-minutes: 5
      - uses: actions/upload-artifact@v4
        with:
          name: dist
          path: dist/
  deploy:
    needs: build
    if: github.ref == 'refs/heads/main' && success()
    runs-on: ubuntu-latest
    steps:
      - run: ./deploy.sh
`

func TestParseWorkflow(t *testing.T) {
	wf, err := ParseWorkflow([]byte(sampleWorkflow))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if wf.Name != "CI" {
		t.Errorf("name = %q", wf.Name)
	}
	if wf.Env["GLOBAL"] != "one" {
		t.Errorf("env = %v", wf.Env)
	}
	if wf.Concurrency == nil || wf.Concurrency.Group != "ci-${{ github.ref }}" || !wf.Concurrency.CancelInProgress {
		t.Errorf("concurrency = %+v", wf.Concurrency)
	}
	if len(wf.Notes) == 0 || !strings.Contains(wf.Notes[0], "permissions") {
		t.Errorf("expected a permissions note, got %v", wf.Notes)
	}

	push := wf.On["push"]
	if push == nil || len(push.Branches) != 2 || push.TagsIgnore[0] != "v0.*" || push.PathsIgnore[0] != "docs/**" {
		t.Errorf("push spec = %+v", push)
	}
	pr := wf.On["pull_request"]
	if pr == nil || len(pr.Types) != 2 || pr.Branches[0] != "main" {
		t.Errorf("pull_request spec = %+v", pr)
	}
	dispatch := wf.On["workflow_dispatch"]
	if dispatch == nil || len(dispatch.InputOrder) != 3 || dispatch.InputOrder[0] != "level" {
		t.Fatalf("dispatch spec = %+v", dispatch)
	}
	if in := dispatch.Inputs["level"]; in.Type != "choice" || in.Default != "info" || len(in.Options) != 3 || in.Description != "Log level" {
		t.Errorf("level input = %+v", in)
	}
	if in := dispatch.Inputs["count"]; in.Type != "number" || !in.Required {
		t.Errorf("count input = %+v", in)
	}
	if sched := wf.On["schedule"]; sched == nil || len(sched.Crons) != 1 || sched.Crons[0] != "30 2 * * 1-5" {
		t.Errorf("schedule spec = %+v", sched)
	}
	if rel := wf.On["release"]; rel == nil || rel.Types[0] != "published" {
		t.Errorf("release spec = %+v", rel)
	}
	if _, ok := wf.On["issues"]; !ok {
		t.Error("issues trigger missing")
	}
	if ic := wf.On["issue_comment"]; ic == nil || ic.Types[0] != "created" {
		t.Errorf("issue_comment spec = %+v", ic)
	}

	if len(wf.JobOrder) != 2 || wf.JobOrder[0] != "build" || wf.JobOrder[1] != "deploy" {
		t.Errorf("job order = %v", wf.JobOrder)
	}
	build := wf.Jobs["build"]
	if build.Name != "Build ${{ matrix.os }}" || len(build.RunsOn) != 2 || build.TimeoutMinutes != 15 || !build.ContinueOnError {
		t.Errorf("build job = %+v", build)
	}
	if build.Env["JOB_ENV"] != "two" || build.Container == nil || build.Container.Image != "node:${{ matrix.node }}" || build.Container.Options != "--cpus 1" {
		t.Errorf("build env or container wrong: %+v %+v", build.Env, build.Container)
	}
	if svc := build.Services["redis"]; svc == nil || svc.Image != "redis:7" || svc.Ports[0] != "6379:6379" || build.ServiceOrder[0] != "redis" {
		t.Errorf("services = %+v", build.Services)
	}
	if build.Outputs["version"] != "${{ steps.ver.outputs.version }}" {
		t.Errorf("outputs = %v", build.Outputs)
	}
	if build.Strategy == nil || build.Strategy.FailFast == nil || *build.Strategy.FailFast || len(build.Strategy.MatrixOrder) != 2 {
		t.Fatalf("strategy = %+v", build.Strategy)
	}
	if len(build.Steps) != 4 {
		t.Fatalf("steps = %d", len(build.Steps))
	}
	if s := build.Steps[0]; s.Uses != "actions/checkout@v4" {
		t.Errorf("step 1 = %+v", s)
	}
	if s := build.Steps[1]; s.ID != "ver" || s.Name != "Version" || s.Shell != "bash" || s.WorkingDirectory != "./src" || s.Env["STEP_ENV"] != "three" || !strings.Contains(s.Run, "GITHUB_OUTPUT") {
		t.Errorf("step 2 = %+v", s)
	}
	if s := build.Steps[2]; s.If != "${{ matrix.os == 'linux' }}" || !s.ContinueOnError || s.TimeoutMinutes != 5 || !strings.Contains(s.Run, "npm test") {
		t.Errorf("step 3 = %+v", s)
	}
	if s := build.Steps[3]; s.With["name"] != "dist" || s.With["path"] != "dist/" {
		t.Errorf("step 4 = %+v", s)
	}
	deploy := wf.Jobs["deploy"]
	if len(deploy.Needs) != 1 || deploy.Needs[0] != "build" || deploy.RunsOn[0] != "ubuntu-latest" || !strings.Contains(deploy.If, "success()") {
		t.Errorf("deploy job = %+v", deploy)
	}
}

func TestParseOnShapes(t *testing.T) {
	wf, err := ParseWorkflow([]byte("on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wf.On["push"]; !ok {
		t.Error("string on should register push")
	}
	wf, err = ParseWorkflow([]byte("on: [push, pull_request]\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.On) != 2 {
		t.Errorf("list on = %v", wf.On)
	}
	if wf.Jobs["a"].Name != "a" {
		t.Errorf("job name should default to key, got %q", wf.Jobs["a"].Name)
	}
}

func TestParseWorkflowErrors(t *testing.T) {
	cases := map[string]string{
		"no on":        "name: x\njobs:\n  a:\n    steps:\n      - run: true\n",
		"no jobs":      "on: push\n",
		"no steps":     "on: push\njobs:\n  a:\n    runs-on: x\n",
		"bad need":     "on: push\njobs:\n  a:\n    runs-on: x\n    needs: nope\n    steps:\n      - run: true\n",
		"cycle":        "on: push\njobs:\n  a:\n    needs: b\n    steps:\n      - run: true\n  b:\n    needs: a\n    steps:\n      - run: true\n",
		"run and uses": "on: push\njobs:\n  a:\n    steps:\n      - run: true\n        uses: x@v1\n",
		"bad cron":     "on:\n  schedule:\n    - cron: 'bogus'\njobs:\n  a:\n    steps:\n      - run: true\n",
		"bad input":    "on:\n  workflow_dispatch:\n    inputs:\n      x:\n        type: choice\njobs:\n  a:\n    steps:\n      - run: true\n",
		"bad job id":   "on: push\njobs:\n  'bad id':\n    steps:\n      - run: true\n",
		"not yaml":     "on: [\n",
	}
	for name, src := range cases {
		if _, err := ParseWorkflow([]byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMatrixCombinations(t *testing.T) {
	wf, err := ParseWorkflow([]byte(sampleWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	combos, order, err := MatrixCombinations(wf.Jobs["build"].Strategy)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 4 {
		t.Fatalf("got %d combinations: %v", len(combos), combos)
	}
	if len(order) != 3 || order[0] != "os" || order[1] != "node" || order[2] != "experimental" {
		t.Errorf("order = %v", order)
	}
	var sawExperimental, sawMacos bool
	for _, c := range combos {
		if c["os"] == "windows" && looseEqual(c["node"], float64(18)) {
			t.Errorf("excluded combination present: %v", c)
		}
		if c["os"] == "linux" && looseEqual(c["node"], float64(20)) {
			if c["experimental"] != true {
				t.Errorf("include should extend linux/20: %v", c)
			}
			sawExperimental = true
		}
		if c["os"] == "macos" {
			sawMacos = true
			if _, ok := c["experimental"]; ok {
				t.Errorf("new include combo must not inherit extra keys: %v", c)
			}
		}
	}
	if !sawExperimental || !sawMacos {
		t.Errorf("include handling incomplete: %v", combos)
	}
	if got := MatrixSuffix(map[string]any{"os": "linux", "node": float64(20)}, []string{"os", "node"}); got != " (linux, 20)" {
		t.Errorf("suffix = %q", got)
	}
	none, _, _ := MatrixCombinations(nil)
	if len(none) != 1 || none[0] != nil {
		t.Errorf("nil strategy should yield one empty combination")
	}
	big := &Strategy{Matrix: map[string][]any{}, MatrixOrder: []string{"a", "b", "c", "d", "e"}}
	for _, k := range big.MatrixOrder {
		for i := range 10 {
			big.Matrix[k] = append(big.Matrix[k], i)
		}
	}
	if _, _, err := MatrixCombinations(big); err == nil {
		t.Error("ten to the fifth combinations should be refused")
	}
}

func TestValidateInputs(t *testing.T) {
	wf, err := ParseWorkflow([]byte(sampleWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	spec := wf.On["workflow_dispatch"]
	if _, err := ValidateInputs(spec, map[string]string{}); err == nil {
		t.Error("missing required count should fail")
	}
	if _, err := ValidateInputs(spec, map[string]string{"count": "x"}); err == nil {
		t.Error("non numeric count should fail")
	}
	if _, err := ValidateInputs(spec, map[string]string{"count": "1", "level": "loud"}); err == nil {
		t.Error("bad choice should fail")
	}
	if _, err := ValidateInputs(spec, map[string]string{"count": "1", "dry_run": "maybe"}); err == nil {
		t.Error("bad boolean should fail")
	}
	if _, err := ValidateInputs(spec, map[string]string{"count": "1", "unknown": "x"}); err == nil {
		t.Error("unknown input should fail")
	}
	out, err := ValidateInputs(spec, map[string]string{"count": "3", "dry_run": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if out["level"] != "info" || out["dry_run"] != true || out["count"] != float64(3) {
		t.Errorf("typed inputs = %v", out)
	}
}

func TestMatchTrigger(t *testing.T) {
	wf, err := ParseWorkflow([]byte(sampleWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   triggerInput
		want bool
	}{
		{"push main", triggerInput{Event: "push", Ref: "refs/heads/main"}, true},
		{"push release glob", triggerInput{Event: "push", Ref: "refs/heads/release/1.x"}, true},
		{"push other branch", triggerInput{Event: "push", Ref: "refs/heads/feature"}, false},
		{"push ignored tag", triggerInput{Event: "push", Ref: "refs/tags/v0.1"}, false},
		{"push tag", triggerInput{Event: "push", Ref: "refs/tags/v1.0"}, true},
		{"push docs only", triggerInput{Event: "push", Ref: "refs/heads/main", Changed: []string{"docs/a.md"}, ChangedKnown: true}, false},
		{"push code", triggerInput{Event: "push", Ref: "refs/heads/main", Changed: []string{"docs/a.md", "main.go"}, ChangedKnown: true}, true},
		{"pr opened", triggerInput{Event: "pull_request", Action: "opened", Payload: map[string]any{"pull_request": map[string]any{"base": map[string]any{"ref": "main"}}}}, true},
		{"pr closed", triggerInput{Event: "pull_request", Action: "closed"}, false},
		{"pr other base", triggerInput{Event: "pull_request", Action: "opened", Payload: map[string]any{"pull_request": map[string]any{"base": map[string]any{"ref": "dev"}}}}, false},
		{"release published", triggerInput{Event: "release", Action: "published"}, true},
		{"release created", triggerInput{Event: "release", Action: "created"}, false},
		{"issues any", triggerInput{Event: "issues", Action: "labeled"}, true},
		{"comment created", triggerInput{Event: "issue_comment", Action: "created"}, true},
		{"comment edited", triggerInput{Event: "issue_comment", Action: "edited"}, false},
		{"dispatch", triggerInput{Event: "workflow_dispatch"}, true},
		{"schedule", triggerInput{Event: "schedule"}, true},
		{"unknown event", triggerInput{Event: "fork"}, false},
	}
	for _, c := range cases {
		in := c.in
		if got := matchTrigger(wf, &in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestPushFilterOnlyTagsOrBranches(t *testing.T) {
	src := "on:\n  push:\n    tags: ['v*']\njobs:\n  a:\n    steps:\n      - run: true\n"
	wf, err := ParseWorkflow([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if matchTrigger(wf, &triggerInput{Event: "push", Ref: "refs/heads/main"}) {
		t.Error("tags only filter must skip branch pushes")
	}
	if !matchTrigger(wf, &triggerInput{Event: "push", Ref: "refs/tags/v2"}) {
		t.Error("matching tag should run")
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"main", "main", true},
		{"release/**", "release/1.x/hotfix", true},
		{"release/*", "release/1.x/hotfix", false},
		{"feature/*", "feature/login", true},
		{"**/*.md", "docs/guide/a.md", true},
		{"**/*.md", "README.md", true},
		{"*.md", "docs/a.md", false},
		{"v?.?", "v1.2", true},
		{"v+", "v12", true},
		{"src/**", "src", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.s); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
	if !MatchFilter([]string{"release/**", "!release/**/skip"}, "release/a") || MatchFilter([]string{"release/**", "!release/**/skip"}, "release/a/skip") {
		t.Error("negation handling wrong")
	}
}
