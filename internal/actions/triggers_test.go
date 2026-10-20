package actions

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v6/plumbing"
)

const triggerSource = `name: CI
on:
  push:
    branches: [main, 'release/**']
    tags-ignore: ['v0.*']
    paths-ignore: ['docs/**']
  pull_request:
    types: [opened, synchronize]
    branches: [main]
  workflow_dispatch:
  schedule:
    - cron: '30 2 * * 1-5'
  release:
    types: [published]
  issues:
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: true
`

func triggerFile(t *testing.T) *WorkflowFile {
	t.Helper()
	f := ParseWorkflowFile(".distroface/workflows/ci.yml", []byte(triggerSource))
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	return f
}

func TestWorkflowFileEventsAndSchedules(t *testing.T) {
	f := triggerFile(t)
	if f.Name != "CI" {
		t.Fatalf("name = %q", f.Name)
	}
	names := f.EventNames()
	if len(names) != 6 || names[0] != "push" {
		t.Fatalf("events = %v", names)
	}
	if crons := f.Schedules(); len(crons) != 1 || crons[0] != "30 2 * * 1-5" {
		t.Fatalf("schedules = %v", crons)
	}
	if f.Event("workflow_dispatch") == nil || f.Event("deployment") != nil {
		t.Fatal("event lookup")
	}
}

func TestMatchPush(t *testing.T) {
	f := triggerFile(t)
	known := func(paths ...string) func() ([]string, bool) {
		return func() ([]string, bool) { return paths, true }
	}
	cases := []struct {
		ref   string
		paths func() ([]string, bool)
		want  bool
	}{
		{"refs/heads/main", known("src/a.go"), true},
		{"refs/heads/release/1.2", known("src/a.go"), true},
		{"refs/heads/feature", known("src/a.go"), false},
		{"refs/heads/main", known("docs/readme.md"), false},
		{"refs/heads/main", known("docs/readme.md", "src/a.go"), true},
		{"refs/heads/main", nil, true},
		{"refs/tags/v1.0", nil, true},
		{"refs/tags/v0.1", nil, false},
	}
	for _, c := range cases {
		in := &triggerInput{Event: "push", Ref: c.ref, changed: c.paths}
		if got := matchTrigger(f, in); got != c.want {
			t.Errorf("push %s: got %v want %v", c.ref, got, c.want)
		}
	}
}

func TestMatchPullAndTypes(t *testing.T) {
	f := triggerFile(t)
	pr := func(base string) map[string]any {
		return map[string]any{"pull_request": map[string]any{"base": map[string]any{"ref": base}}}
	}
	if !matchTrigger(f, &triggerInput{Event: "pull_request", Action: "opened", Payload: pr("main")}) {
		t.Fatal("opened on main must match")
	}
	if matchTrigger(f, &triggerInput{Event: "pull_request", Action: "closed", Payload: pr("main")}) {
		t.Fatal("closed is not listed")
	}
	if matchTrigger(f, &triggerInput{Event: "pull_request", Action: "opened", Payload: pr("dev")}) {
		t.Fatal("dev base is filtered")
	}
	if !matchTrigger(f, &triggerInput{Event: "release", Action: "published"}) || matchTrigger(f, &triggerInput{Event: "release", Action: "created"}) {
		t.Fatal("release types")
	}
	if !matchTrigger(f, &triggerInput{Event: "issues", Action: "labeled"}) {
		t.Fatal("issues without types takes every action")
	}
	if !matchTrigger(f, &triggerInput{Event: "workflow_dispatch"}) || !matchTrigger(f, &triggerInput{Event: "schedule"}) {
		t.Fatal("dispatch and schedule")
	}
	if matchTrigger(f, &triggerInput{Event: "deployment"}) {
		t.Fatal("unlisted event")
	}
}

func TestValidateDispatchInputs(t *testing.T) {
	src := "on:\n  workflow_dispatch:\n    inputs:\n      level:\n        type: choice\n        default: info\n        options: [debug, info]\n      dry:\n        type: boolean\n      count:\n        type: number\n        required: true\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n"
	f := ParseWorkflowFile("d.yml", []byte(src))
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	decl := f.DispatchInputs()
	if len(decl) != 3 || decl[0].Name != "level" {
		t.Fatalf("inputs = %+v", decl)
	}
	typed, err := ValidateDispatchInputs(decl, map[string]string{"count": "3", "dry": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if typed["level"] != "info" || typed["dry"] != true || typed["count"] != float64(3) {
		t.Fatalf("typed = %v", typed)
	}
	if _, err := ValidateDispatchInputs(decl, map[string]string{"level": "warn", "count": "1"}); err == nil {
		t.Fatal("choice outside options")
	}
	if _, err := ValidateDispatchInputs(decl, map[string]string{"level": "info"}); err == nil {
		t.Fatal("required count missing")
	}
	if _, err := ValidateDispatchInputs(decl, map[string]string{"count": "1", "bogus": "x"}); err == nil {
		t.Fatal("unknown input")
	}
}

func TestMatchPullPaths(t *testing.T) {
	src := "on:\n  pull_request:\n    paths: ['src/**']\n  pull_request_target:\n    paths-ignore: ['docs/**']\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n"
	f := ParseWorkflowFile("ci.yml", []byte(src))
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	known := func(paths ...string) func() ([]string, bool) {
		return func() ([]string, bool) { return paths, true }
	}
	pr := map[string]any{"pull_request": map[string]any{"base": "main"}}
	cases := []struct {
		event string
		paths func() ([]string, bool)
		want  bool
	}{
		{"pull_request", known("src/a.go"), true},
		{"pull_request", known("docs/x.md"), false},
		{"pull_request", nil, true},
		{"pull_request_target", known("docs/x.md"), false},
		{"pull_request_target", known("docs/x.md", "src/a.go"), true},
	}
	for _, c := range cases {
		in := &triggerInput{Event: c.event, Action: "opened", Payload: pr, changed: c.paths}
		if got := matchTrigger(f, in); got != c.want {
			t.Errorf("%s: got %v want %v", c.event, got, c.want)
		}
	}
}

func TestPullChangedPathsAgainstMergeBase(t *testing.T) {
	ctx := context.Background()
	engine := newTestEngine(t)
	initTestRepo(t, engine, "acme", "app")
	a := commitFiles(t, ctx, engine, "acme", "app", "main", map[string]string{"a.txt": "1", "docs/x.md": "x"})
	setBranch(t, engine, "acme", "app", "feature", a)
	b := commitFiles(t, ctx, engine, "acme", "app", "feature", map[string]string{"src/b.go": "package b"})
	commitFiles(t, ctx, engine, "acme", "app", "main", map[string]string{"docs/y.md": "y"})
	r, err := engine.OpenRepo("acme", "app", false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	paths, known := pullChangedPaths(ctx, r, "main", b)
	if !known || len(paths) != 1 || paths[0] != "src/b.go" {
		t.Fatalf("paths = %v known = %v, the base branch's own commits must not count", paths, known)
	}
	if _, known := pullChangedPaths(ctx, r, "nope", b); known {
		t.Fatal("a missing base branch leaves the paths unknown")
	}
	if got := pullHeadSHA(r, map[string]any{"pull_request": map[string]any{"head_sha": b.String()}}, 0); got != b.String() {
		t.Fatalf("head sha = %s", got)
	}
	setBranch(t, engine, "acme", "app", "", b)
	if err := r.UpdateRef("refs/pull/7/head", plumbing.ZeroHash, b); err != nil {
		t.Fatal(err)
	}
	if got := pullHeadSHA(r, map[string]any{}, 7); got != b.String() {
		t.Fatalf("head sha from the pull ref = %s", got)
	}
}
