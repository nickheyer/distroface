package actions

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
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
	for _, n := range wf.Notes {
		if strings.Contains(n, "permissions") {
			t.Errorf("permissions must parse instead of being noted, got %v", wf.Notes)
		}
	}
	if wf.Permissions == nil || wf.Permissions.Scopes["contents"] != "read" || !wf.Permissions.ReadOnly() {
		t.Errorf("permissions = %+v", wf.Permissions)
	}
	if !wf.Jobs["build"].Permissions.ReadOnly() {
		t.Error("jobs inherit the workflow permissions")
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
	onlyWf, err := ParseWorkflow([]byte(`
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - goos: linux
            goarch: amd64
          - goos: linux
            goarch: arm64
            cc: aarch64-linux-gnu-gcc
          - goos: windows
            goarch: amd64
            cc: x86_64-w64-mingw32-gcc
    steps:
      - run: true
`))
	if err != nil {
		t.Fatal(err)
	}
	only := onlyWf.Jobs["build"].Strategy
	if len(only.IncludeOrder) != 3 || strings.Join(only.IncludeOrder[1], ",") != "goos,goarch,cc" {
		t.Fatalf("include order = %v", only.IncludeOrder)
	}
	combos, order, err = MatrixCombinations(only)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 3 {
		t.Fatalf("include only matrix should yield one combo per entry, got %v", combos)
	}
	if combos[0]["goos"] != "linux" || combos[0]["goarch"] != "amd64" || combos[0]["cc"] != nil {
		t.Errorf("later includes leaked into the first combo: %v", combos[0])
	}
	if combos[2]["cc"] != "x86_64-w64-mingw32-gcc" {
		t.Errorf("third include lost its own values: %v", combos[2])
	}
	if strings.Join(order, ",") != "goos,goarch,cc" {
		t.Errorf("include only order = %v", order)
	}
	if got := MatrixSuffix(combos[1], order); got != " (linux, arm64, aarch64-linux-gnu-gcc)" {
		t.Errorf("suffix = %q", got)
	}
	unordered := &Strategy{Include: []map[string]any{{"goos": "linux", "goarch": "amd64"}}}
	if _, order, _ := MatrixCombinations(unordered); strings.Join(order, ",") != "goarch,goos" {
		t.Errorf("unknown include order should sort, got %v", order)
	}
	banana := &Strategy{
		Matrix: map[string][]any{"fruit": {"apple", "pear"}, "animal": {"cat", "dog"}}, MatrixOrder: []string{"fruit", "animal"},
		Include: []map[string]any{{"fruit": "banana"}, {"fruit": "banana", "animal": "cat"}},
	}
	combos, _, err = MatrixCombinations(banana)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 6 {
		t.Fatalf("second banana include must not merge into the first, got %v", combos)
	}
	if combos[4]["animal"] != nil || combos[5]["animal"] != "cat" {
		t.Errorf("banana combos = %v %v", combos[4], combos[5])
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
		{"push docs only", triggerInput{Event: "push", Ref: "refs/heads/main", changed: knownPaths("docs/a.md")}, false},
		{"push code", triggerInput{Event: "push", Ref: "refs/heads/main", changed: knownPaths("docs/a.md", "main.go")}, true},
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

func TestParsePermissions(t *testing.T) {
	src := "on: push\npermissions: read-all\njobs:\n  a:\n    permissions:\n      contents: write\n    steps:\n      - run: true\n  b:\n    permissions: {}\n    steps:\n      - run: true\n  c:\n    steps:\n      - run: true\n  d:\n    permissions: write-all\n    steps:\n      - run: true\n  e:\n    permissions:\n      packages: write\n      contents: read\n    steps:\n      - run: true\n"
	wf, err := ParseWorkflow([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if wf.Permissions == nil || wf.Permissions.All != "read-all" {
		t.Errorf("workflow permissions = %+v", wf.Permissions)
	}
	cases := map[string]bool{"a": false, "b": true, "c": true, "d": false, "e": false}
	for key, want := range cases {
		if got := wf.Jobs[key].Permissions.ReadOnly(); got != want {
			t.Errorf("job %s read only = %v, want %v", key, got, want)
		}
	}
	var none *Permissions
	if none.ReadOnly() || !none.Writes("contents") {
		t.Error("absent permissions keep the permissive default")
	}
}

func TestDefaultsMerge(t *testing.T) {
	src := "on: push\ndefaults:\n  run:\n    shell: bash\n    working-directory: ./src\njobs:\n  a:\n    steps:\n      - run: true\n  b:\n    defaults:\n      run:\n        shell: sh\n    steps:\n      - run: true\n"
	wf, err := ParseWorkflow([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Notes) != 0 {
		t.Errorf("defaults must not be noted as ignored, got %v", wf.Notes)
	}
	if d := wf.Jobs["a"].Defaults; d == nil || d.Shell != "bash" || d.WorkingDirectory != "./src" {
		t.Errorf("job a defaults = %+v", d)
	}
	if d := wf.Jobs["b"].Defaults; d == nil || d.Shell != "sh" || d.WorkingDirectory != "./src" {
		t.Errorf("job b defaults = %+v", d)
	}
}

const callerWorkflow = `
name: Caller
on: push
env:
  TOP: caller
jobs:
  prep:
    runs-on: ubuntu-latest
    outputs:
      tag: ${{ steps.t.outputs.tag }}
    steps:
      - id: t
        run: echo tag=v1 >> $GITHUB_OUTPUT
  call:
    needs: prep
    if: github.ref == 'refs/heads/main'
    uses: ./.distroface/workflows/called.yml
    with:
      env: ${{ needs.prep.outputs.tag }}
      flag: true
    secrets: inherit
  after:
    needs: call
    runs-on: ubuntu-latest
    steps:
      - run: echo ${{ needs.call.outputs.ver }}
`

const calledWorkflow = `
name: Called
on:
  workflow_call:
    inputs:
      env:
        type: string
        required: true
      flag:
        type: boolean
        default: 'false'
    secrets:
      TOKEN:
        required: false
    outputs:
      ver:
        description: Built version
        value: ${{ jobs.build.outputs.version }}
env:
  TOP: called
permissions:
  contents: read
jobs:
  lint:
    runs-on: ubuntu-latest
    if: inputs.flag
    steps:
      - run: lint
  build:
    needs: lint
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.v.outputs.version }}
    steps:
      - id: v
        run: echo version=1.2 >> $GITHUB_OUTPUT
`

func mustParse(t *testing.T, src string) *Workflow {
	t.Helper()
	wf, err := ParseWorkflow([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

// Loader over in memory sources keyed by reference
func mapLoader(files map[string]string) callLoader {
	return func(ref, from string) (*Workflow, string, error) {
		src, ok := files[ref]
		if !ok {
			return nil, "", errors.New("repository is not on this instance")
		}
		wf, err := ParseWorkflow([]byte(src))
		if err != nil {
			return nil, "", err
		}
		return wf, ref, nil
	}
}

func TestParseCallJobAndWorkflowCall(t *testing.T) {
	caller := mustParse(t, callerWorkflow)
	call := caller.Jobs["call"]
	if call.Uses != "./.distroface/workflows/called.yml" || call.With["flag"] != "true" || !call.SecretsInherit || len(call.Steps) != 0 {
		t.Errorf("call job = %+v", call)
	}
	called := mustParse(t, calledWorkflow)
	spec := called.On["workflow_call"]
	if spec == nil || len(spec.InputOrder) != 2 || spec.Inputs["env"].Type != "string" || !spec.Inputs["env"].Required {
		t.Fatalf("workflow_call spec = %+v", spec)
	}
	if spec.Secrets["TOKEN"] == nil || spec.Secrets["TOKEN"].Required {
		t.Errorf("secrets = %+v", spec.Secrets)
	}
	if o := spec.Outputs["ver"]; o == nil || o.Description != "Built version" || !strings.Contains(o.Value, "jobs.build.outputs.version") {
		t.Errorf("outputs = %+v", spec.Outputs)
	}
	typed, err := ValidateInputs(spec, map[string]string{"env": "prod"})
	if err != nil || typed["env"] != "prod" || typed["flag"] != false {
		t.Errorf("typed call inputs = %v %v", typed, err)
	}
	if _, err := ParseWorkflow([]byte("on: push\njobs:\n  a:\n    uses: ./x.yml\n    steps:\n      - run: true\n")); err == nil {
		t.Error("uses with steps must fail")
	}
	mapped := mustParse(t, "on: push\njobs:\n  a:\n    uses: ./x.yml\n    secrets:\n      TOKEN: ${{ secrets.REAL }}\n")
	if mapped.Jobs["a"].Secrets["TOKEN"] != "${{ secrets.REAL }}" || mapped.Jobs["a"].SecretsInherit {
		t.Errorf("secrets map = %+v", mapped.Jobs["a"])
	}
	if matchTrigger(called, &triggerInput{Event: "push", Ref: "refs/heads/main"}) {
		t.Error("a workflow_call only file must not run on push")
	}
}

func TestInlineWorkflow(t *testing.T) {
	load := mapLoader(map[string]string{"./.distroface/workflows/called.yml": calledWorkflow})
	wf := ExpandCalls(mustParse(t, callerWorkflow), "root", load, nil)
	want := []string{"prep", "call/lint", "call/build", "after"}
	if strings.Join(wf.JobOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("job order = %v", wf.JobOrder)
	}
	lint, build, after := wf.Jobs["call/lint"], wf.Jobs["call/build"], wf.Jobs["after"]
	if lint.Error != "" || build.Error != "" {
		t.Fatalf("unexpected errors %q %q", lint.Error, build.Error)
	}
	if strings.Join(lint.Needs, ",") != "prep" {
		t.Errorf("root called job must take the caller needs, got %v", lint.Needs)
	}
	if lint.If != "(github.ref == 'refs/heads/main') && (inputs.flag)" {
		t.Errorf("combined if = %q", lint.If)
	}
	if strings.Join(build.Needs, ",") != "call/lint" || build.If != "" {
		t.Errorf("inner needs = %v if = %q", build.Needs, build.If)
	}
	if strings.Join(after.Needs, ",") != "call" {
		t.Errorf("caller dependents keep the caller key, got %v", after.Needs)
	}
	if lint.Name != "call / lint" || build.Name != "call / build" {
		t.Errorf("names = %q %q", lint.Name, build.Name)
	}
	if lint.WorkflowEnv["TOP"] != "called" {
		t.Errorf("inlined jobs carry the called env, got %v", lint.WorkflowEnv)
	}
	if len(build.Calls) != 1 {
		t.Fatalf("calls = %+v", build.Calls)
	}
	fr := build.Calls[0]
	if fr.Caller != "call" || fr.Prefix != "call" || !fr.Inherit || fr.Inputs["flag"] != "true" || fr.Outputs["ver"] == "" || strings.Join(fr.Needs, ",") != "prep" {
		t.Errorf("frame = %+v", fr)
	}
	if !build.Permissions.ReadOnly() {
		t.Error("called permissions apply to inlined jobs")
	}
	// Caller job rows are ordinary, the call left nothing behind
	if _, ok := wf.Jobs["call"]; ok {
		t.Error("the caller job must be replaced by the called jobs")
	}
}

func TestInlineWorkflowMatrixAndPermissions(t *testing.T) {
	src := "on: push\npermissions: read-all\njobs:\n  call:\n    strategy:\n      matrix:\n        os: [linux, mac]\n    uses: ./w.yml\n    with:\n      env: ${{ matrix.os }}\n"
	called := "on:\n  workflow_call:\n    inputs:\n      env:\n        type: string\njobs:\n  a:\n    permissions:\n      contents: write\n    steps:\n      - run: true\n  b:\n    needs: a\n    steps:\n      - run: true\n"
	wf := ExpandCalls(mustParse(t, src), "root", mapLoader(map[string]string{"./w.yml": called}), nil)
	want := []string{"call (linux)/a", "call (linux)/b", "call (mac)/a", "call (mac)/b"}
	if strings.Join(wf.JobOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("job order = %v", wf.JobOrder)
	}
	b := wf.Jobs["call (mac)/b"]
	if strings.Join(b.Needs, ",") != "call (mac)/a" {
		t.Errorf("per combination needs, got %v", b.Needs)
	}
	if b.Calls[0].Prefix != "call (mac)" || b.Calls[0].Caller != "call" || b.Calls[0].Matrix["os"] != "mac" {
		t.Errorf("frame = %+v", b.Calls[0])
	}
	if !strings.HasPrefix(b.Name, "call (mac) / ") {
		t.Errorf("name = %q", b.Name)
	}
	if !wf.Jobs["call (mac)/a"].Permissions.ReadOnly() {
		t.Error("a read only caller caps the called permissions")
	}
}

func TestInlineWorkflowNested(t *testing.T) {
	root := "on: push\njobs:\n  outer:\n    uses: ./a.yml\n"
	a := "on:\n  workflow_call:\n    outputs:\n      ver:\n        value: ${{ jobs.inner.outputs.v }}\njobs:\n  inner:\n    uses: ./b.yml\n  tail:\n    needs: inner\n    steps:\n      - run: true\n"
	b := "on:\n  workflow_call:\n    outputs:\n      v:\n        value: ${{ jobs.x.outputs.raw }}\njobs:\n  x:\n    steps:\n      - run: true\n"
	wf := ExpandCalls(mustParse(t, root), "root", mapLoader(map[string]string{"./a.yml": a, "./b.yml": b}), nil)
	if strings.Join(wf.JobOrder, ",") != "outer/inner/x,outer/tail" {
		t.Fatalf("job order = %v", wf.JobOrder)
	}
	x := wf.Jobs["outer/inner/x"]
	if len(x.Calls) != 2 || x.Calls[0].Caller != "outer" || x.Calls[1].Caller != "outer/inner" || x.Calls[1].Prefix != "outer/inner" {
		t.Fatalf("frames = %+v %+v", x.Calls[0], x.Calls[1])
	}
	if strings.Join(wf.Jobs["outer/tail"].Needs, ",") != "outer/inner" {
		t.Errorf("tail needs = %v", wf.Jobs["outer/tail"].Needs)
	}
}

func TestInlineWorkflowErrors(t *testing.T) {
	cases := []struct {
		name, root string
		files      map[string]string
		want       string
	}{
		{"missing", "on: push\njobs:\n  c:\n    uses: octo/repo/.github/workflows/x.yml@main\n", nil, "not on this instance"},
		{"no trigger", "on: push\njobs:\n  c:\n    uses: ./w.yml\n", map[string]string{"./w.yml": "on: push\njobs:\n  a:\n    steps:\n      - run: true\n"}, "workflow_call"},
		{"unknown input", "on: push\njobs:\n  c:\n    uses: ./w.yml\n    with:\n      nope: 1\n", map[string]string{"./w.yml": "on:\n  workflow_call:\njobs:\n  a:\n    steps:\n      - run: true\n"}, "not declared"},
		{"required input", "on: push\njobs:\n  c:\n    uses: ./w.yml\n", map[string]string{"./w.yml": "on:\n  workflow_call:\n    inputs:\n      env:\n        required: true\njobs:\n  a:\n    steps:\n      - run: true\n"}, "required"},
		{"required secret", "on: push\njobs:\n  c:\n    uses: ./w.yml\n", map[string]string{"./w.yml": "on:\n  workflow_call:\n    secrets:\n      TOKEN:\n        required: true\njobs:\n  a:\n    steps:\n      - run: true\n"}, "secret \"TOKEN\" is required"},
		{"cycle", "on: push\njobs:\n  c:\n    uses: ./w.yml\n", map[string]string{"./w.yml": "on:\n  workflow_call:\njobs:\n  again:\n    uses: ./w.yml\n"}, "cycle"},
		{"depth", "on: push\njobs:\n  c:\n    uses: ./1.yml\n", map[string]string{
			"./1.yml": "on:\n  workflow_call:\njobs:\n  j:\n    uses: ./2.yml\n",
			"./2.yml": "on:\n  workflow_call:\njobs:\n  j:\n    uses: ./3.yml\n",
			"./3.yml": "on:\n  workflow_call:\njobs:\n  j:\n    uses: ./4.yml\n",
			"./4.yml": "on:\n  workflow_call:\njobs:\n  j:\n    uses: ./5.yml\n",
			"./5.yml": "on:\n  workflow_call:\njobs:\n  j:\n    steps:\n      - run: true\n",
		}, "nesting"},
	}
	for _, c := range cases {
		wf := ExpandCalls(mustParse(t, c.root), "root", mapLoader(c.files), nil)
		var failed *Job
		for _, j := range wf.Jobs {
			if j.Error != "" {
				failed = j
			}
		}
		if failed == nil {
			t.Errorf("%s: expected a failed job, got %v", c.name, wf.JobOrder)
			continue
		}
		if !strings.Contains(failed.Error, c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, failed.Error, c.want)
		}
	}
	// A cycle five levels down still names the depth, not the caller key
	wf := ExpandCalls(mustParse(t, "on: push\njobs:\n  c:\n    uses: ./w.yml\n  d:\n    needs: c\n    steps:\n      - run: true\n"), "root", mapLoader(nil), nil)
	if wf.Jobs["c"] == nil || wf.Jobs["c"].Error == "" || strings.Join(wf.Jobs["d"].Needs, ",") != "c" {
		t.Error("a failed call keeps its key so dependents resolve")
	}
}

func inlinedRow(id, key, outputs string, calls ...*CallFrame) *db.ActionsJob {
	def := jobDefinition{Job: &Job{Key: key, Calls: calls, Steps: []*Step{{Run: "true"}}}}
	raw, _ := json.Marshal(def)
	return &db.ActionsJob{ID: id, JobKey: key, MatrixGroup: key, Status: StatusCompleted, Conclusion: ConclusionSuccess, Outputs: outputs, Definition: string(raw)}
}

func TestNeedsStateCallOutputs(t *testing.T) {
	frame := &CallFrame{Caller: "call", Prefix: "call", Outputs: map[string]string{"ver": "${{ jobs.build.outputs.version }}", "res": "${{ jobs.lint.result }}"}}
	rows := []*db.ActionsJob{
		inlinedRow("1", "call/lint", "{}", frame),
		inlinedRow("2", "call/build", `{"version":"1.2"}`, frame),
		inlinedRow("3", "prep", `{"tag":"v1"}`),
	}
	done, status, outputs := needsState([]string{"call", "prep"}, rows, nil)
	if !done || status != ConclusionSuccess {
		t.Fatalf("done=%v status=%s", done, status)
	}
	call := outputs["call"].(map[string]any)
	outs := call["outputs"].(map[string]any)
	if outs["ver"] != "1.2" || outs["res"] != "success" || call["result"] != ConclusionSuccess {
		t.Errorf("call outputs = %v", call)
	}
	if outputs["prep"].(map[string]any)["outputs"].(map[string]any)["tag"] != "v1" {
		t.Errorf("plain outputs = %v", outputs["prep"])
	}
	rows[0].Conclusion = ConclusionFailure
	if _, status, _ = needsState([]string{"call"}, rows, nil); status != ConclusionFailure {
		t.Errorf("worst conclusion wins, got %s", status)
	}
	rows[1].Status = StatusRunning
	if done, _, _ = needsState([]string{"call"}, rows, nil); done {
		t.Error("a running called job keeps the need pending")
	}
	// Nested calls evaluate inner outputs first
	outer := &CallFrame{Caller: "outer", Prefix: "outer", Outputs: map[string]string{"ver": "${{ jobs.inner.outputs.v }}"}}
	inner := &CallFrame{Caller: "outer/inner", Prefix: "outer/inner", Outputs: map[string]string{"v": "${{ jobs.x.outputs.raw }}"}}
	nested := []*db.ActionsJob{inlinedRow("9", "outer/inner/x", `{"raw":"9"}`, outer, inner)}
	_, _, outputs = needsState([]string{"outer", "outer/inner"}, nested, nil)
	if outputs["outer"].(map[string]any)["outputs"].(map[string]any)["ver"] != "9" {
		t.Errorf("nested outputs = %v", outputs["outer"])
	}
	if outputs["outer/inner"].(map[string]any)["outputs"].(map[string]any)["v"] != "9" {
		t.Errorf("inner outputs = %v", outputs["outer/inner"])
	}
}

func TestResolveCalls(t *testing.T) {
	spec := map[string]*DispatchInput{"env": {Type: "string", Required: true}, "flag": {Type: "boolean", Default: "false"}}
	frame := &CallFrame{
		Caller: "call", Prefix: "call", Ref: "./w.yml",
		Inputs:    map[string]string{"env": "${{ inputs.top }}-${{ matrix.os }}-${{ needs.prep.outputs.tag }}", "flag": "true"},
		InputSpec: spec, InputOrder: []string{"env", "flag"},
		Secrets: map[string]string{"TOKEN": "${{ secrets.REAL }}"},
		Needs:   []string{"prep"}, Matrix: map[string]any{"os": "linux"},
	}
	rows := []*db.ActionsJob{inlinedRow("3", "prep", `{"tag":"v1"}`)}
	ectx := &ExprContext{GitHub: map[string]any{}, Inputs: map[string]any{"top": "prod"}}
	inputs, exposed, err := resolveCalls([]*CallFrame{frame}, ectx, rows, map[string]string{"REAL": "s3", "OTHER": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if inputs["env"] != "prod-linux-v1" || inputs["flag"] != true {
		t.Errorf("inputs = %v", inputs)
	}
	if len(exposed) != 1 || exposed["TOKEN"] != "s3" {
		t.Errorf("exposed secrets = %v", exposed)
	}
	frame.Inherit = true
	if _, exposed, _ = resolveCalls([]*CallFrame{frame}, ectx, rows, map[string]string{"REAL": "s3", "OTHER": "x"}); len(exposed) != 2 {
		t.Errorf("inherit passes everything, got %v", exposed)
	}
	if inputs, _, err := resolveCalls([]*CallFrame{frame}, ectx, rows, nil); err != nil || inputs["env"] != "prod-linux-v1" {
		t.Errorf("nil secrets still type inputs, got %v %v", inputs, err)
	}
	frame.Inputs = map[string]string{"env": "x", "flag": "maybe"}
	if _, _, err := resolveCalls([]*CallFrame{frame}, ectx, rows, nil); err == nil {
		t.Error("a bad boolean must fail the job")
	}
}

func TestParseCallRef(t *testing.T) {
	full, rev, path, err := parseCallRef("./.github/workflows/x.yml", "", "me/app", "abc")
	if err != nil || full != "me/app" || rev != "abc" || path != ".github/workflows/x.yml" {
		t.Errorf("local ref = %s %s %s %v", full, rev, path, err)
	}
	full, rev, path, err = parseCallRef("./w.yaml", "other/lib/.github/workflows/a.yml@deadbeef", "me/app", "abc")
	if err != nil || full != "other/lib" || rev != "deadbeef" || path != "w.yaml" {
		t.Errorf("nested local ref = %s %s %s %v", full, rev, path, err)
	}
	full, rev, path, err = parseCallRef("other/lib/.github/workflows/a.yml@v1", "", "me/app", "abc")
	if err != nil || full != "other/lib" || rev != "v1" || path != ".github/workflows/a.yml" {
		t.Errorf("remote ref = %s %s %s %v", full, rev, path, err)
	}
	for _, bad := range []string{"other/lib@v1", "./w.yml/../../x.yml", "../w.yml", "other/lib/w.txt@v1", "other/lib/w.yml"} {
		if _, _, _, err := parseCallRef(bad, "", "me/app", "abc"); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// Fixed changed path set for trigger tests
func knownPaths(paths ...string) func() ([]string, bool) {
	return func() ([]string, bool) { return paths, true }
}

func TestChangedPathsLazy(t *testing.T) {
	calls := 0
	in := &triggerInput{Event: "push", Ref: "refs/heads/main", changed: func() ([]string, bool) {
		calls++
		return []string{"main.go"}, true
	}}
	if !in.pathsAllowed(nil, nil) || calls != 0 {
		t.Fatalf("no filter must skip the diff, calls=%d", calls)
	}
	if !in.pathsAllowed([]string{"*.go"}, nil) || calls != 1 {
		t.Fatalf("filter must diff once, calls=%d", calls)
	}
	if in.pathsAllowed([]string{"docs/**"}, nil) || calls != 1 {
		t.Fatalf("second filter must reuse the diff, calls=%d", calls)
	}
	if !(&triggerInput{Event: "pull_request"}).pathsAllowed([]string{"*.go"}, nil) {
		t.Fatal("unknown changes must pass the filter")
	}
}
