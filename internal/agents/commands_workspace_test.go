package agents

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/runner"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Turns commands on for the harness repository
func (h *harness) enableCommands() {
	h.t.Helper()
	patch := &v1.Settings{Git: &v1.GitSettings{
		Actions: &v1.GitActionsSettings{Enabled: proto.Bool(true)},
		Agents:  &v1.GitAgentsSettings{AllowCommands: proto.Bool(true)},
	}}
	paths := []string{"git.actions.enabled", "git.agents.allow_commands"}
	if _, err := h.agents.res.Update(h.ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", patch, paths); err != nil {
		h.t.Fatalf("settings: %v", err)
	}
	units := gitrepo.UnitsOf(h.repo)
	units.Actions = true
	h.repo.Units = gitrepo.EncodeUnits(units)
	if err := h.store.UpdateGitRepository(h.ctx, h.repo); err != nil {
		h.t.Fatalf("units: %v", err)
	}
	h.agents.res.Invalidate()
}

// Commits one file on main
func (h *harness) commitFile(path, content string) {
	h.t.Helper()
	req := gitrepo.CommitRequest{Branch: "main", Message: "Add " + path, Edits: []gitrepo.TreeEdit{{Path: path, Mode: filemode.Regular, Content: strings.NewReader(content)}}}
	if _, err := h.git.CommitToBranch(h.ctx, h.repo, h.actor, req); err != nil {
		h.t.Fatalf("commit %s: %v", path, err)
	}
}

// What the fake runner saw while serving a job
type fakeRunnerLog struct {
	execs    []*v1.ExecRequest
	jobs     []string
	tasks    []*v1.Task
	finished chan struct{}
}

// Runner side of the protocol against the real manager
func (h *harness) fakeRunner(ctx context.Context, runner *db.ActionsRunner, outputFor func(*v1.ExecRequest) string) *fakeRunnerLog {
	am := h.agents.actions
	rl := &fakeRunnerLog{finished: make(chan struct{}, 8)}
	go func() {
		for ctx.Err() == nil {
			task, err := am.FetchTask(ctx, runner, 500*time.Millisecond)
			if err != nil || task == nil {
				continue
			}
			rl.jobs = append(rl.jobs, task.JobId)
			rl.tasks = append(rl.tasks, task)
			for task.GetInteractive() && ctx.Err() == nil {
				ex, done, cancelled, err := am.FetchExec(ctx, runner, task.JobId, 500*time.Millisecond)
				if err != nil || done || cancelled {
					break
				}
				if ex == nil {
					continue
				}
				rl.execs = append(rl.execs, ex)
				_ = am.CompleteExec(ctx, runner, task.JobId, ex.Id, actions.ExecResult{Conclusion: actions.ConclusionSuccess, Output: outputFor(ex)})
			}
			_ = am.CompleteTask(ctx, runner, task.JobId, actions.ConclusionSuccess, nil, "", "")
			rl.finished <- struct{}{}
		}
	}()
	return rl
}

func TestCommandWorkspacePersistsAcrossCommands(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "write_file", map[string]any{"path": "hello.txt", "content": "hello\n"})}},
		{calls: []llm.ToolCall{call("c2", "run_command", map[string]any{"command": "cat hello.txt"})}},
		{calls: []llm.ToolCall{call("c3", "run_command", map[string]any{"command": "echo again"})}},
		{calls: []llm.ToolCall{call("c4", "commit", map[string]any{"message": "Add hello"})}},
		{calls: []llm.ToolCall{call("c5", "run_command", map[string]any{"command": "git log -1"})}},
		{text: "Ran the commands."},
	})
	h.enableCommands()
	builtin, _, err := runner.NewRegistry(h.store, h.agents.actions).RegisterBuiltin(h.ctx, []string{"ubuntu-latest"}, "test")
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	rl := h.fakeRunner(ctx, builtin, func(ex *v1.ExecRequest) string { return "ran " + ex.Id + "\n" })

	def := h.defineAgent(AgentSpec{Slug: "coder", Name: "Coder", Kind: KindCode, Approval: ApprovalNone, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Run things", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess {
		t.Fatalf("conclusion %s, error %q", done.Conclusion, done.Error)
	}

	// One job served everything, closed when the session ended
	select {
	case <-rl.finished:
	case <-time.After(15 * time.Second):
		h.dumpJobs(t)
		t.Fatal("the fake runner never finished the session job")
	}
	if len(rl.jobs) != 1 {
		t.Fatalf("jobs served = %d, want one workspace per session", len(rl.jobs))
	}
	if len(rl.execs) != 3 {
		t.Fatalf("execs served = %d", len(rl.execs))
	}
	first, second, third := rl.execs[0], rl.execs[1], rl.execs[2]
	// Uncommitted edits precede every command until they land
	write := base64.StdEncoding.EncodeToString([]byte("hello\n"))
	for i, ex := range []*v1.ExecRequest{first, second} {
		if !strings.Contains(ex.Script, "> 'hello.txt'") || !strings.Contains(ex.Script, write) {
			t.Fatalf("command %d must write hello.txt, got %q", i+1, ex.Script)
		}
		if strings.Contains(ex.Script, "git fetch") {
			t.Fatalf("command %d must not touch the branch, it never moved, got %q", i+1, ex.Script)
		}
	}
	if !strings.HasSuffix(first.Script, "\nset +e\ncat hello.txt") || first.Number != 1 {
		t.Fatalf("first = %+v", first)
	}
	if !strings.HasSuffix(second.Script, "\necho again") || second.Number != 2 {
		t.Fatalf("second = %+v", second)
	}
	// The commit moved the branch, the checkout syncs
	if !strings.Contains(third.Script, "git fetch") || !strings.Contains(third.Script, "git checkout -q --force -B 'agents/coder/1'") {
		t.Fatalf("third must sync the branch, got %q", third.Script)
	}
	if strings.Contains(third.Script, "base64 -d") || !strings.HasSuffix(third.Script, "\ngit log -1") {
		t.Fatalf("committed edits must not be rewritten, got %q", third.Script)
	}

	// The model saw what the runner reported
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	var results []string
	for _, ev := range events {
		if ev.Kind == EventToolResult && ev.Tool == "run_command" {
			results = append(results, ev.Text)
		}
	}
	if len(results) != 3 {
		t.Fatalf("run_command results = %d", len(results))
	}
	if !strings.Contains(results[0], "exit code 0") || !strings.HasSuffix(results[0], "ran "+first.Id+"\n") {
		t.Fatalf("first result = %q", results[0])
	}

	// Session run lists normally, hidden from commit checks
	runs, _, err := h.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %d %v", len(runs), err)
	}
	if runs[0].AgentSessionID != sess.ID || runs[0].AgentSlug != "coder" || !strings.HasPrefix(runs[0].WorkflowPath, "agent-session-") {
		t.Fatalf("run = %+v", runs[0])
	}
	if hidden, _, _ := h.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID, ExcludeAgentRuns: true}); len(hidden) != 0 {
		t.Fatalf("commit checks must skip session runs, got %d", len(hidden))
	}
	jobs, _ := h.store.ListActionsJobs(h.ctx, runs[0].ID)
	if len(jobs) != 1 || !jobs[0].Interactive || jobs[0].Status != actions.StatusCompleted {
		t.Fatalf("job = %+v", jobs[0])
	}
	execs, _ := h.store.ListActionsJobExecs(h.ctx, jobs[0].ID)
	if len(execs) != 3 || execs[2].Number != 3 || execs[0].Status != stores.ExecCompleted || execs[0].Output != "ran "+first.Id+"\n" {
		t.Fatalf("execs = %+v", execs)
	}
	if live, _, _ := h.agents.actions.LiveAgentSessionJob(h.ctx, sess.ID); live != nil {
		t.Fatal("the workspace must be closed once the session finished")
	}
	if err := h.agents.actions.RerunRun(h.ctx, h.repo, runs[0], false); err == nil {
		t.Fatal("agent runs must not be rerunnable")
	}
}

func TestCommandWorkspaceRefusesWithoutCapableRunner(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "run_command", map[string]any{"command": "true"})}},
		{text: "Could not run."},
	})
	h.enableCommands()
	// Stale runner without the exec capability
	stale := time.Now().Add(-time.Hour)
	row := &db.ActionsRunner{ScopeKind: db.ActionsScopeSystem, Name: "old", Labels: `["ubuntu-latest"]`, TokenHash: "x", LastSeen: &stale}
	if err := h.store.CreateActionsRunner(h.ctx, row); err != nil {
		t.Fatal(err)
	}
	def := h.defineAgent(AgentSpec{Slug: "coder", Name: "Coder", Kind: KindCode, Approval: ApprovalNone, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Run", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	h.waitFor(sess.ID, completed)
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	var result string
	for _, ev := range events {
		if ev.Kind == EventToolResult && ev.Tool == "run_command" {
			result = ev.Text
		}
	}
	if !strings.Contains(result, "no online runner") {
		t.Fatalf("expected an offline runner refusal, got %q", result)
	}
	if runs, _, _ := h.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID}); len(runs) != 0 {
		t.Fatal("no run may be created when no runner can serve it")
	}
}

const setupWorkflow = `on: workflow_dispatch
jobs:
  agent-setup-steps:
    runs-on: go-runner
    container: golang:1.25
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go mod download
`

func TestCommandWorkspaceRunsAgentSetupSteps(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "run_command", map[string]any{"command": "go version"})}},
		{text: "Go is there."},
	})
	h.enableCommands()
	h.commitFile(".distroface/workflows/agent-setup-steps.yml", setupWorkflow)
	// Only the setup job's label has a runner
	builtin, _, err := runner.NewRegistry(h.store, h.agents.actions).RegisterBuiltin(h.ctx, []string{"go-runner"}, "test")
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	rl := h.fakeRunner(ctx, builtin, func(*v1.ExecRequest) string { return "go version go1.25 linux/amd64\n" })

	def := h.defineAgent(AgentSpec{Slug: "coder", Name: "Coder", Kind: KindCode, Approval: ApprovalNone, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Check go", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess {
		t.Fatalf("conclusion %s, error %q", done.Conclusion, done.Error)
	}
	select {
	case <-rl.finished:
	case <-time.After(15 * time.Second):
		h.dumpJobs(t)
		t.Fatal("the fake runner never finished the session job")
	}
	if len(rl.tasks) != 1 || len(rl.execs) != 1 {
		t.Fatalf("tasks = %d execs = %d", len(rl.tasks), len(rl.execs))
	}
	// The setup job shapes the session job, checkout stays first
	task := rl.tasks[0]
	if len(task.RunsOn) != 1 || task.RunsOn[0] != "go-runner" || !task.Interactive {
		t.Fatalf("task = runs_on %v interactive %v", task.RunsOn, task.Interactive)
	}
	swfs, err := jobparser.Parse(task.WorkflowPayload)
	if err != nil || len(swfs) != 1 {
		t.Fatalf("payload: %v\n%s", err, task.WorkflowPayload)
	}
	_, job := swfs[0].Job()
	var image string
	if job == nil || job.RawContainer.Decode(&image) != nil || image != "golang:1.25" {
		t.Fatalf("job container = %+v", job)
	}
	if len(job.Steps) != 3 || job.Steps[0].Uses != "actions/checkout@v4" || job.Steps[1].Uses != "actions/setup-go@v5" || job.Steps[1].With["go-version-file"] != "go.mod" || strings.TrimSpace(job.Steps[2].Run) != "go mod download" {
		t.Fatalf("steps = %+v", job.Steps)
	}
	runs, _, _ := h.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID})
	if len(runs) != 1 || !strings.Contains(runs[0].Workflow, "actions/setup-go@v5") {
		t.Fatalf("runs = %d, workflow must carry the setup steps", len(runs))
	}
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	var opened string
	for _, ev := range events {
		if ev.Kind == EventStatus && strings.HasPrefix(ev.Text, "Opened command workspace") {
			opened = ev.Text
		}
	}
	if !strings.HasSuffix(opened, "prepared by .distroface/workflows/agent-setup-steps.yml") {
		t.Fatalf("status = %q", opened)
	}
}

func TestCommandWorkspaceRefusesBrokenSetupWorkflow(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "run_command", map[string]any{"command": "true"})}},
		{text: "Could not run."},
	})
	h.enableCommands()
	h.commitFile(".distroface/workflows/agent-setup-steps.yml", "on: push\njobs:\n  build:\n    steps:\n      - run: true\n")
	builtin, _, err := runner.NewRegistry(h.store, h.agents.actions).RegisterBuiltin(h.ctx, []string{"ubuntu-latest"}, "test")
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.fakeRunner(ctx, builtin, func(*v1.ExecRequest) string { return "" })
	def := h.defineAgent(AgentSpec{Slug: "coder", Name: "Coder", Kind: KindCode, Approval: ApprovalNone, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Run", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	h.waitFor(sess.ID, completed)
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	var result string
	for _, ev := range events {
		if ev.Kind == EventToolResult && ev.Tool == "run_command" {
			result = ev.Text
		}
	}
	if !strings.Contains(result, "agent-setup-steps.yml") || !strings.Contains(result, "needs a job named agent-setup-steps") {
		t.Fatalf("expected the setup file to be named, got %q", result)
	}
	if runs, _, _ := h.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID}); len(runs) != 0 {
		t.Fatal("no run may be created from a broken setup workflow")
	}
}

// Job rows of every run, for failures that stall before the runner
func (h *harness) dumpJobs(t *testing.T) {
	t.Helper()
	runs, _, _ := h.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID})
	t.Logf("runs = %d", len(runs))
	sessions, _, _ := h.store.ListAgentSessions(h.ctx, stores.AgentSessionListOptions{RepoID: h.repo.ID})
	for _, sess := range sessions {
		events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
		for _, ev := range events {
			t.Logf("event %s %s: %s", ev.Kind, ev.Tool, ev.Text)
		}
	}
	for _, run := range runs {
		jobs, _ := h.store.ListActionsJobs(h.ctx, run.ID)
		for _, j := range jobs {
			t.Logf("run %s %s/%s job %s status=%s conclusion=%s error=%q runs_on=%s annotations=%s", run.ID, run.Status, run.Conclusion, j.Name, j.Status, j.Conclusion, j.Error, j.RunsOn, j.Annotations)
		}
	}
}
