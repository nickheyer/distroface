package actions

import (
	"errors"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/actions/jobparser"
)

const setupSource = `name: Agent setup
on: workflow_dispatch
env:
  GOFLAGS: -mod=mod
jobs:
  agent-setup-steps:
    runs-on: go-runner
    container: golang:1.25
    env:
      CGO_ENABLED: "0"
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Deps
        run: |
          go mod download
          go version
`

func parseSetup(t *testing.T, path, src string) (*AgentSetup, error) {
	t.Helper()
	return agentSetupFromFile(ParseWorkflowFile(path, []byte(src)))
}

// Single job of a generated session workflow as the runner will read it
func sessionJob(t *testing.T, src string) (*jobparser.SingleWorkflow, *jobparser.Job) {
	t.Helper()
	swfs, err := jobparser.Parse([]byte(src))
	if err != nil {
		t.Fatalf("generated workflow does not parse: %v\n%s", err, src)
	}
	if len(swfs) != 1 {
		t.Fatalf("expected one job, got %d\n%s", len(swfs), src)
	}
	id, job := swfs[0].Job()
	if id != "session" || job == nil {
		t.Fatalf("no session job in\n%s", src)
	}
	return swfs[0], job
}

func TestAgentSessionWorkflowSplicesSetupSteps(t *testing.T) {
	setup, err := parseSetup(t, ".distroface/workflows/agent-setup-steps.yml", setupSource)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(setup.RunsOn) != 1 || setup.RunsOn[0] != "go-runner" {
		t.Fatalf("runs-on = %v", setup.RunsOn)
	}
	out, err := agentSessionWorkflow(AgentRunOptions{Session: AgentRunInfo{SessionNumber: 7}, RunsOn: setup.RunsOn, Setup: setup})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	swf, job := sessionJob(t, out)
	if got := job.RunsOn(); len(got) != 1 || got[0] != "go-runner" {
		t.Fatalf("job runs-on = %v", got)
	}
	// Checkout first, then the setup steps as written
	if len(job.Steps) != 3 || job.Steps[0].Uses != "actions/checkout@v4" {
		t.Fatalf("steps = %+v", job.Steps)
	}
	if job.Steps[1].Uses != "actions/setup-go@v5" || job.Steps[1].With["go-version-file"] != "go.mod" {
		t.Fatalf("setup step = %+v", job.Steps[1])
	}
	if job.Steps[2].Name != "Deps" || job.Steps[2].Run != "go mod download\ngo version\n" {
		t.Fatalf("run step = %+v", job.Steps[2])
	}
	var image string
	if job.RawContainer.Kind == 0 || job.RawContainer.Decode(&image) != nil || image != "golang:1.25" {
		t.Fatalf("container = %+v", job.RawContainer)
	}
	var env map[string]string
	if job.Env.Decode(&env) != nil || env["CGO_ENABLED"] != "0" || swf.Env["GOFLAGS"] != "-mod=mod" {
		t.Fatalf("env job=%v workflow=%v", env, swf.Env)
	}
	if job.TimeoutMinutes != "525600" || !readOnlyPermissions(swf.RawPermissions, job.RawPermissions) {
		t.Fatalf("session job keeps its own timeout and read only permissions, got %q", job.TimeoutMinutes)
	}
}

func TestAgentSessionWorkflowWithoutSetup(t *testing.T) {
	out, err := agentSessionWorkflow(AgentRunOptions{Session: AgentRunInfo{SessionNumber: 1}, TimeoutMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	_, job := sessionJob(t, out)
	if len(job.Steps) != 1 || job.Steps[0].Uses != "actions/checkout@v4" || job.TimeoutMinutes != "30" {
		t.Fatalf("job = %+v", job)
	}
	if got := job.RunsOn(); len(got) != 1 || got[0] != defaultRunsOnLabel {
		t.Fatalf("runs-on = %v", got)
	}
}

func TestAgentSetupFileValidation(t *testing.T) {
	if _, err := parseSetup(t, "x.yml", "on: push\njobs:\n  build:\n    runs-on: x\n    steps:\n      - run: true\n"); err == nil || !strings.Contains(err.Error(), AgentSetupJob) {
		t.Fatalf("a file without the setup job must be refused, got %v", err)
	}
	if _, err := parseSetup(t, "x.yml", "on: push\njobs:\n  agent-setup-steps:\n    uses: ./w.yml\n"); err == nil || !strings.Contains(err.Error(), "steps") {
		t.Fatalf("a reusable call must be refused, got %v", err)
	}
	if _, err := agentSetupFromFile(&WorkflowFile{Path: "x.yml", Err: errors.New("yaml: boom")}); err == nil || !strings.Contains(err.Error(), "x.yml") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a parse failure must name the file, got %v", err)
	}
	// Without runs-on the session falls back to the configured label
	setup, err := parseSetup(t, "x.yml", "on: push\njobs:\n  agent-setup-steps:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm ci\n")
	if err != nil {
		t.Fatal(err)
	}
	setup.RunsOn = nil
	out, err := agentSessionWorkflow(AgentRunOptions{Session: AgentRunInfo{SessionNumber: 2}, RunsOn: []string{"ubuntu-latest"}, Setup: setup})
	if err != nil {
		t.Fatal(err)
	}
	if _, job := sessionJob(t, out); len(job.Steps) != 2 || job.Steps[1].Run != "npm ci" || job.RunsOn()[0] != "ubuntu-latest" {
		t.Fatalf("job = %+v", job)
	}
}
