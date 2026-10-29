package actions

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/db"
)

const planSource = `name: CI
on: push
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
      max-parallel: 2
      matrix:
        os: [linux, windows]
        node: [18, 20]
        exclude:
          - os: windows
            node: 18
    steps:
      - run: echo build
  test:
    needs: build
    runs-on: ubuntu-latest
    permissions:
      contents: write
    concurrency:
      group: test-${{ github.ref }}
      cancel-in-progress: true
    if: ${{ always() }}
    steps:
      - run: echo test
  later:
    needs: build
    runs-on: ubuntu-latest
    strategy:
      matrix: ${{ fromJSON(needs.build.outputs.legs) }}
    steps:
      - run: echo ${{ matrix.leg }}
  call:
    uses: ./.distroface/workflows/called.yml
    with:
      name: x
    secrets: inherit
`

func testPlanInput() *planInput {
	run := &db.ActionsRun{ID: "run1", Number: 3, WorkflowName: "CI", Ref: "refs/heads/main", Event: "push", Payload: "{}"}
	return &planInput{
		repo:   &db.GitRepository{ID: "r1", Namespace: "ns", Name: "app"},
		run:    run,
		gitCtx: map[string]any{"ref": "refs/heads/main", "ref_name": "main", "event_name": "push", "repository": "ns/app", "event": map[string]any{}},
		vars:   map[string]string{},
		inputs: map[string]any{},
	}
}

func rowsByKey(rows []*db.ActionsJob) map[string]*db.ActionsJob {
	out := map[string]*db.ActionsJob{}
	for _, r := range rows {
		out[r.JobKey] = r
	}
	return out
}

func TestPlanJobsExpandsMatrixAndDefers(t *testing.T) {
	m := &Manager{}
	rows := m.planJobs(testPlanInput(), []byte(planSource), nil)
	byKey := rowsByKey(rows)
	legs := 0
	for _, r := range rows {
		if r.WorkflowJobID == "build" {
			legs++
		}
	}
	if legs != 3 {
		t.Fatalf("build legs = %d, want 3 after the exclude", legs)
	}
	build := byKey["build (18, linux)"]
	if build == nil {
		t.Fatalf("no build (18, linux) row in %v", keysOf(byKey))
	}
	if build.Name != "Build linux" || build.MatrixGroup != "build" || build.FailFast || build.MaxParallel != 2 || !build.ContinueOnError || build.TimeoutMinutes != 15 {
		t.Fatalf("build row = %+v", build)
	}
	if !build.ReadOnly || !strings.Contains(build.Matrix, `"node":18`) || !strings.Contains(build.RunsOn, "self-hosted") {
		t.Fatalf("build row perms/matrix/runs-on = %v %s %s", build.ReadOnly, build.Matrix, build.RunsOn)
	}
	if !strings.Contains(build.WorkflowPayload, "echo build") || strings.Contains(build.WorkflowPayload, "needs:") {
		t.Fatalf("payload must hold the job without needs:\n%s", build.WorkflowPayload)
	}
	test := byKey["test"]
	if test == nil || test.ReadOnly || test.Concurrency != "test-refs/heads/main" || !test.ConcurrencyCancel || !test.ConcurrencyEvaluated {
		t.Fatalf("test row = %+v", test)
	}
	if strings.TrimSpace(test.Needs) != `["build"]` {
		t.Fatalf("test needs = %s", test.Needs)
	}
	later := byKey["later"]
	if later == nil || !later.Deferred || later.Error != "" {
		t.Fatalf("later row must defer until build finishes, got %+v", later)
	}
	call := byKey["call"]
	if call == nil || !call.IsReusableCaller || call.CallUses != "./.distroface/workflows/called.yml" {
		t.Fatalf("call row = %+v", call)
	}
}

func keysOf(m map[string]*db.ActionsJob) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPlanJobsParseFailureBecomesOneRow(t *testing.T) {
	m := &Manager{}
	rows := m.planJobs(testPlanInput(), []byte("name: broken\non: push\njobs:\n  a:\n    runs-on: x\n    strategy:\n      matrix: ${{ fromJSON('nope') }}\n    steps:\n      - run: true\n"), nil)
	if len(rows) != 1 || rows[0].Error == "" || rows[0].Name != "CI" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDeferredMatrixExpandsWithOutputs(t *testing.T) {
	results := map[string]string{"build": "success"}
	outputs := map[string]map[string]string{"build": {"legs": `{"leg": ["a", "b"]}`}}
	swfs, err := jobparser.Parse([]byte(planSource), jobparser.WithJobResults(results), jobparser.WithJobOutputs(outputs), jobparser.WithJobFilter("later"))
	if err != nil {
		t.Fatal(err)
	}
	if len(swfs) != 2 {
		t.Fatalf("legs = %d, want 2", len(swfs))
	}
	for i, want := range []string{"a", "b"} {
		_, job := swfs[i].Job()
		if swfs[i].Deferred || job == nil || job.Name != "later ("+want+")" {
			t.Fatalf("leg %d = %+v deferred=%v", i, job, swfs[i].Deferred)
		}
	}
}

func TestReadOnlyPermissions(t *testing.T) {
	parse := func(src string) (*jobparser.SingleWorkflow, *jobparser.Job) {
		swfs, err := jobparser.Parse([]byte(src))
		if err != nil || len(swfs) != 1 {
			t.Fatalf("parse %q: %v", src, err)
		}
		_, job := swfs[0].Job()
		return swfs[0], job
	}
	cases := map[string]bool{
		"on: push\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n":                                                                 false,
		"on: push\npermissions: read-all\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n":                                          true,
		"on: push\npermissions: write-all\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n":                                         false,
		"on: push\npermissions:\n  packages: write\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n":                                false,
		"on: push\npermissions:\n  issues: write\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n":                                  true,
		"on: push\npermissions: write-all\njobs:\n  a:\n    permissions:\n      contents: read\n    runs-on: x\n    steps: [{run: true}]\n": true,
	}
	for src, want := range cases {
		swf, job := parse(src)
		if got := readOnlyPermissions(swf.RawPermissions, job.RawPermissions); got != want {
			t.Errorf("%q: read only = %v, want %v", src, got, want)
		}
	}
}

func TestParseWorkflowFileRecoversDecodePanic(t *testing.T) {
	src := "on: push\njobs:\n  a:\n    runs-on: [{a: b}]\n    steps: [{run: true}]\n"
	f := ParseWorkflowFile("ci.yml", []byte(src))
	var de *jobparser.DecodeError
	if f == nil || f.Err == nil || !errors.As(f.Err, &de) {
		t.Fatalf("file = %+v", f)
	}
	rows := (&Manager{}).planJobs(testPlanInput(), []byte(src), nil)
	if len(rows) != 1 || !strings.Contains(rows[0].Error, "cannot decode") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPlanJobsReportsUnclosedExpression(t *testing.T) {
	src := "run-name: \"${{ a }} ${{ b\"\non: push\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n"
	rows := (&Manager{}).planJobs(testPlanInput(), []byte(src), nil)
	if len(rows) != 1 || !strings.Contains(rows[0].Error, "unclosed expression") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestJobRowJoinsPlanningErrors(t *testing.T) {
	src := "on: push\njobs:\n  a:\n    runs-on: x\n    timeout-minutes: soon\n    strategy:\n      fail-fast: maybe\n    steps: [{run: true}]\n"
	rows := (&Manager{}).planJobs(testPlanInput(), []byte(src), nil)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	ff, tm := strings.Index(rows[0].Error, "fail-fast:"), strings.Index(rows[0].Error, "timeout-minutes:")
	if ff < 0 || tm < 0 || ff > tm {
		t.Fatalf("error = %q", rows[0].Error)
	}
}

func TestCheckCallCycle(t *testing.T) {
	repo := &db.GitRepository{Namespace: "ns", Name: "app"}
	run := &db.ActionsRun{WorkflowPath: ".github/workflows/ci.yml", SHA: "abc"}
	if err := checkCallCycle(repo, run, nil, &callSource{repo: "ns/app", ref: "abc"}, ".github/workflows/ci.yml"); err == nil {
		t.Fatal("a workflow calling itself must be a cycle")
	}
	chain := []*db.ActionsJob{{CallUses: "./.github/workflows/x.yml", CallSourceRepo: "ns/lib", CallSourceRef: "def"}}
	if err := checkCallCycle(repo, run, chain, &callSource{repo: "ns/lib", ref: "def"}, ".github/workflows/x.yml"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v", err)
	}
	if err := checkCallCycle(repo, run, chain, &callSource{repo: "ns/lib", ref: "def"}, ".github/workflows/y.yml"); err != nil {
		t.Fatalf("sibling file is not a cycle: %v", err)
	}
	if err := checkCallCycle(repo, run, chain, &callSource{repo: "ns/lib", ref: "v2"}, ".github/workflows/x.yml"); err != nil {
		t.Fatalf("another ref is not a cycle: %v", err)
	}
}

func TestDispatchInputsRoundTrip(t *testing.T) {
	src := "on:\n  workflow_dispatch:\n    inputs:\n      level:\n        type: choice\n        default: info\n        options: [debug, info]\n      dry:\n        type: boolean\n      count:\n        type: number\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n"
	f := ParseWorkflowFile("d.yml", []byte(src))
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	typed, err := ValidateDispatchInputs(f.DispatchInputs(), map[string]string{"dry": "true", "count": "3"})
	if err != nil {
		t.Fatal(err)
	}
	strs := inputStrings(typed)
	if strs["dry"] != "true" || strs["count"] != "3" || strs["level"] != "info" {
		t.Fatalf("event inputs = %v", strs)
	}
	run := &db.ActionsRun{Event: "workflow_dispatch", WorkflowPath: "d.yml", Workflow: src, Payload: mustJSON(map[string]any{"inputs": strs})}
	if got := runInputs(run); !reflect.DeepEqual(got, typed) {
		t.Fatalf("typed inputs = %v, want %v", got, typed)
	}
	if got := inputsFromPayload(run.Payload); got["dry"] != "true" {
		t.Fatalf("payload keeps strings, got %v", got)
	}
}

func TestNumericIDStable(t *testing.T) {
	if NumericID("42") != 42 {
		t.Fatal("decimal ids pass through")
	}
	a, b := NumericID("5a2f1c9e-0000-4000-8000-000000000001"), NumericID("5a2f1c9e-0000-4000-8000-000000000002")
	if a == b || a <= 0 || a >= 1<<53 || a != NumericID("5a2f1c9e-0000-4000-8000-000000000001") {
		t.Fatalf("ids = %d %d", a, b)
	}
}
