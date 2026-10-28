package actions

import (
	"errors"
	"strings"
	"testing"
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
	wf, err := ParseWorkflow([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return agentSetupFromFile(&WorkflowFile{Path: path, Source: src, Workflow: wf})
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
	wf, err := ParseWorkflow([]byte(out))
	if err != nil {
		t.Fatalf("generated workflow does not parse: %v\n%s", err, out)
	}
	job := wf.Jobs["session"]
	if job == nil {
		t.Fatalf("no session job in\n%s", out)
	}
	if len(job.RunsOn) != 1 || job.RunsOn[0] != "go-runner" {
		t.Fatalf("job runs-on = %v", job.RunsOn)
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
	if job.Container == nil || job.Container.Image != "golang:1.25" {
		t.Fatalf("container = %+v", job.Container)
	}
	if job.Env["CGO_ENABLED"] != "0" || wf.Env["GOFLAGS"] != "-mod=mod" {
		t.Fatalf("env job=%v workflow=%v", job.Env, wf.Env)
	}
	if job.TimeoutMinutes != noJobTimeoutMinutes || wf.Permissions == nil || !wf.Permissions.ReadOnly() {
		t.Fatalf("session job keeps its own timeout and permissions, got %d %+v", job.TimeoutMinutes, wf.Permissions)
	}
}

func TestAgentSessionWorkflowWithoutSetup(t *testing.T) {
	out, err := agentSessionWorkflow(AgentRunOptions{Session: AgentRunInfo{SessionNumber: 1}, TimeoutMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := ParseWorkflow([]byte(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	job := wf.Jobs["session"]
	if len(job.Steps) != 1 || job.Steps[0].Uses != "actions/checkout@v4" || job.TimeoutMinutes != 30 {
		t.Fatalf("job = %+v", job)
	}
	if len(job.RunsOn) != 1 || job.RunsOn[0] != defaultRunsOnLabel {
		t.Fatalf("runs-on = %v", job.RunsOn)
	}
}

func TestAgentSetupFileValidation(t *testing.T) {
	if _, err := parseSetup(t, "x.yml", "on: push\njobs:\n  build:\n    steps:\n      - run: true\n"); err == nil || !strings.Contains(err.Error(), AgentSetupJob) {
		t.Fatalf("a file without the setup job must be refused, got %v", err)
	}
	if _, err := parseSetup(t, "x.yml", "on: push\njobs:\n  agent-setup-steps:\n    uses: ./w.yml\n"); err == nil || !strings.Contains(err.Error(), "steps") {
		t.Fatalf("a reusable call must be refused, got %v", err)
	}
	if _, err := agentSetupFromFile(&WorkflowFile{Path: "x.yml", Err: errors.New("yaml: boom")}); err == nil || !strings.Contains(err.Error(), "x.yml") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a parse failure must name the file, got %v", err)
	}
	// Without runs-on the session falls back to the configured label
	setup, err := parseSetup(t, "x.yml", "on: push\njobs:\n  agent-setup-steps:\n    steps:\n      - run: npm ci\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.RunsOn) != 0 {
		t.Fatalf("runs-on = %v", setup.RunsOn)
	}
	out, err := agentSessionWorkflow(AgentRunOptions{Session: AgentRunInfo{SessionNumber: 2}, RunsOn: []string{"ubuntu-latest"}, Setup: setup})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := ParseWorkflow([]byte(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if job := wf.Jobs["session"]; len(job.Steps) != 2 || job.Steps[1].Run != "npm ci" || job.RunsOn[0] != "ubuntu-latest" {
		t.Fatalf("job = %+v", job)
	}
}
