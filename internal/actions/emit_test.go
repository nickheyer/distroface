package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func newTestEngine(t *testing.T) *gitrepo.Engine {
	t.Helper()
	root := t.TempDir()
	engine, err := gitrepo.NewEngine(filepath.Join(root, "git"), root)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func initTestRepo(t *testing.T, engine *gitrepo.Engine, ns, name string) {
	t.Helper()
	if err := engine.Init(ns, name, false, "main", ""); err != nil {
		t.Fatal(err)
	}
}

// Points a branch at a commit, empty name writes nothing
func setBranch(t *testing.T, engine *gitrepo.Engine, ns, name, branch string, at plumbing.Hash) {
	t.Helper()
	if branch == "" {
		return
	}
	r, err := engine.OpenRepo(ns, name, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.UpdateRef(plumbing.NewBranchReferenceName(branch), r.BranchHash(branch), at); err != nil {
		t.Fatal(err)
	}
}

// Commits files onto a branch and returns the new head
func commitFiles(t *testing.T, ctx context.Context, engine *gitrepo.Engine, ns, name, branch string, files map[string]string) plumbing.Hash {
	t.Helper()
	r, err := engine.OpenRepo(ns, name, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	parent := r.BranchHash(branch)
	base := plumbing.ZeroHash
	var parents []plumbing.Hash
	if !parent.IsZero() {
		c, err := r.Commit(parent)
		if err != nil {
			t.Fatal(err)
		}
		base, parents = c.TreeHash, []plumbing.Hash{parent}
	}
	var edits []gitrepo.TreeEdit
	for p, content := range files {
		edits = append(edits, gitrepo.TreeEdit{Path: p, Content: strings.NewReader(content)})
	}
	tree, err := r.EditTree(base, edits)
	if err != nil {
		t.Fatal(err)
	}
	sig := object.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()}
	commit, err := r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: tree, Parents: parents, Message: "test commit", Author: sig, Committer: sig})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(plumbing.NewBranchReferenceName(branch), parent, commit); err != nil {
		t.Fatal(err)
	}
	return commit
}

// Manager over a scratch database with repositories and a runner
type emitHarness struct {
	t      *testing.T
	ctx    context.Context
	m      *Manager
	engine *gitrepo.Engine
	repo   *db.GitRepository
}

func newEmitHarness(t *testing.T) *emitHarness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	log := logger.New()
	store := storetest.New(t)
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.SeedDefaultPolicies(); err != nil {
		t.Fatal(err)
	}
	res := settings.NewResolver(store, nil)
	authMgr, err := auth.NewManager(store, enforcer, "test-secret-test-secret-test-secret", res)
	if err != nil {
		t.Fatal(err)
	}
	if err := authMgr.LoadSealingKey("", root); err != nil {
		t.Fatal(err)
	}
	blobs, err := artifacts.NewBlobStore(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := gitrepo.NewEngine(filepath.Join(root, "git"), root)
	if err != nil {
		t.Fatal(err)
	}
	gitMgr := gitrepo.NewManager(store, engine, blobs, res, gitrepo.NewAccess(store, enforcer), webhook.NewDispatcher(store, log, res), audit.NewRecorder(store, res, log), log)
	forgeMgr := forge.NewManager(store, gitMgr, mailer.NewService(store, res, log), res, log)
	t.Cleanup(func() { forgeMgr.Wait(context.Background()) })
	m := NewManager(store, gitMgr, forgeMgr, artifacts.NewManager(store, blobs, res, log), authMgr, res, log)
	enable := &v1.Settings{Git: &v1.GitSettings{Enabled: proto.Bool(true), Actions: &v1.GitActionsSettings{Enabled: proto.Bool(true)}}}
	if _, err := res.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", enable, []string{"git.enabled", "git.actions.enabled"}); err != nil {
		t.Fatal(err)
	}
	h := &emitHarness{t: t, ctx: ctx, m: m, engine: engine}
	h.repo = h.newRepo("acme", "app")
	runner := &db.ActionsRunner{ID: uuid.New().String(), ScopeKind: db.ActionsScopeSystem, Name: "instance", Labels: `["ubuntu-latest","linux"]`, TokenHash: uuid.New().String()}
	if err := store.CreateActionsRunner(ctx, runner); err != nil {
		t.Fatal(err)
	}
	return h
}

// Repository row with actions on plus its bare git repository
func (h *emitHarness) newRepo(ns, name string) *db.GitRepository {
	h.t.Helper()
	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: ns, Name: name, DefaultBranch: "main", Units: `{"actions":true}`}
	if err := h.m.store.DB().Create(repo).Error; err != nil {
		h.t.Fatal(err)
	}
	initTestRepo(h.t, h.engine, ns, name)
	return repo
}

func (h *emitHarness) commit(repo *db.GitRepository, branch string, files map[string]string) plumbing.Hash {
	h.t.Helper()
	return commitFiles(h.t, h.ctx, h.engine, repo.Namespace, repo.Name, branch, files)
}

// Commits the workflow plus extras and starts a push run
func (h *emitHarness) start(src string, extra map[string]string) (*db.ActionsRun, map[string]*db.ActionsJob) {
	h.t.Helper()
	files := map[string]string{".distroface/workflows/ci.yml": src}
	for p, c := range extra {
		files[p] = c
	}
	sha := h.commit(h.repo, "main", files)
	f := ParseWorkflowFile(".distroface/workflows/ci.yml", []byte(src))
	if f.Err != nil {
		h.t.Fatal(f.Err)
	}
	in := &triggerInput{Event: "push", Ref: "refs/heads/main", SHA: sha.String(), FileCommit: sha, Payload: map[string]any{"ref": "refs/heads/main"}}
	run, err := h.m.createRun(h.ctx, h.repo, f, in)
	if err != nil {
		h.t.Fatal(err)
	}
	return run, h.jobs(run)
}

// Jobs of a run keyed by their display name
func (h *emitHarness) jobs(run *db.ActionsRun) map[string]*db.ActionsJob {
	h.t.Helper()
	rows, err := h.m.store.ListActionsJobs(h.ctx, run.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]*db.ActionsJob{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

func (h *emitHarness) job(run *db.ActionsRun, name string) *db.ActionsJob {
	h.t.Helper()
	j := h.jobs(run)[name]
	if j == nil {
		h.t.Fatalf("no job %q in %v", name, keysOf(h.jobs(run)))
	}
	return j
}

func (h *emitHarness) reload(run *db.ActionsRun) *db.ActionsRun {
	h.t.Helper()
	fresh, err := h.m.store.GetActionsRunByID(h.ctx, run.ID)
	if err != nil || fresh == nil {
		h.t.Fatalf("run %s: %v", run.ID, err)
	}
	return fresh
}

// Finishes a queued job like a runner report, then advances
func (h *emitHarness) finish(run *db.ActionsRun, name, conclusion string, outputs map[string]any) {
	h.t.Helper()
	j := h.job(run, name)
	if j.Status != StatusQueued {
		h.t.Fatalf("%s is %s, not queued", name, j.Status)
	}
	now := time.Now().UTC()
	j.Status, j.Conclusion, j.Outputs, j.FinishedAt = StatusCompleted, conclusion, mustJSON(orEmptyMap(outputs)), &now
	cols := map[string]any{"status": StatusCompleted, "conclusion": conclusion, "finished_at": now, "outputs": j.Outputs}
	if err := h.m.store.UpdateActionsJobColumns(h.ctx, j.ID, cols); err != nil {
		h.t.Fatal(err)
	}
	if conclusion == ConclusionFailure {
		h.m.failFast(h.ctx, h.repo, run, j)
	}
	h.m.advanceRun(h.ctx, h.repo, run)
}

func (h *emitHarness) secret(repo *db.GitRepository, name, value string) {
	h.t.Helper()
	sealed, err := h.m.Seal(value)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.m.store.UpsertActionsSecret(h.ctx, &db.ActionsSecret{ScopeKind: db.ActionsScopeRepo, ScopeID: repo.ID, Name: name, Sealed: sealed}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *emitHarness) expect(run *db.ActionsRun, name, status, conclusion string) *db.ActionsJob {
	h.t.Helper()
	j := h.job(run, name)
	if j.Status != status || j.Conclusion != conclusion {
		h.t.Fatalf("%s = %s/%s %s, want %s/%s", name, j.Status, j.Conclusion, j.Annotations, status, conclusion)
	}
	return j
}

const needsRunSource = `name: needs
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: true}]
  when:
    needs: a
    if: needs.a.outputs.flag == 'yes'
    runs-on: ubuntu-latest
    steps: [{run: true}]
  unless:
    needs: a
    if: needs.a.outputs.flag == 'no'
    runs-on: ubuntu-latest
    steps: [{run: true}]
  flaky:
    needs: a
    continue-on-error: ${{ needs.a.outputs.flaky == 'true' }}
    timeout-minutes: ${{ needs.a.outputs.t }}
    runs-on: ubuntu-latest
    steps: [{run: true}]
`

func TestAdvanceRunPropagatesNeeds(t *testing.T) {
	h := newEmitHarness(t)
	run, jobs := h.start(needsRunSource, nil)
	if jobs["a"].Status != StatusQueued || jobs["when"].Status != StatusWaiting || !jobs["flaky"].Deferred {
		t.Fatalf("initial rows: a=%s when=%s flaky deferred=%v", jobs["a"].Status, jobs["when"].Status, jobs["flaky"].Deferred)
	}
	h.finish(run, "a", ConclusionSuccess, map[string]any{"flag": "yes", "flaky": "true", "t": "7"})
	h.expect(run, "when", StatusQueued, "")
	h.expect(run, "unless", StatusCompleted, ConclusionSkipped)
	flaky := h.expect(run, "flaky", StatusQueued, "")
	if flaky.Deferred || !flaky.ContinueOnError || flaky.TimeoutMinutes != 7 {
		t.Fatalf("flaky = deferred %v continue-on-error %v timeout %d", flaky.Deferred, flaky.ContinueOnError, flaky.TimeoutMinutes)
	}
	h.finish(run, "when", ConclusionSuccess, nil)
	h.finish(run, "flaky", ConclusionFailure, nil)
	if fresh := h.reload(run); fresh.Status != StatusCompleted || fresh.Conclusion != ConclusionSuccess {
		t.Fatalf("run = %s/%s, continue-on-error keeps the run green", fresh.Status, fresh.Conclusion)
	}
}

const failedNeedSource = `name: failed need
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: true}]
  later:
    needs: a
    strategy:
      matrix: ${{ fromJSON(needs.a.outputs.legs) }}
    runs-on: ubuntu-latest
    steps: [{run: true}]
  after:
    needs: a
    if: always()
    runs-on: ubuntu-latest
    steps: [{run: true}]
`

func TestFailedNeedSkipsDeferredMatrix(t *testing.T) {
	h := newEmitHarness(t)
	run, _ := h.start(failedNeedSource, nil)
	h.finish(run, "a", ConclusionFailure, nil)
	later := h.expect(run, "later", StatusCompleted, ConclusionSkipped)
	if strings.Contains(later.Annotations, "matrix") {
		t.Fatalf("skipped job carries a matrix error: %s", later.Annotations)
	}
	h.expect(run, "after", StatusQueued, "")
	h.finish(run, "after", ConclusionSuccess, nil)
	if fresh := h.reload(run); fresh.Status != StatusCompleted || fresh.Conclusion != ConclusionFailure {
		t.Fatalf("run = %s/%s", fresh.Status, fresh.Conclusion)
	}
}

func TestFailFastCancelsSiblings(t *testing.T) {
	src := "name: ff\non: push\njobs:\n  build:\n    strategy:\n      matrix:\n        n: [1, 2]\n    runs-on: ubuntu-latest\n    steps: [{run: true}]\n"
	h := newEmitHarness(t)
	run, _ := h.start(src, nil)
	h.finish(run, "build (1)", ConclusionFailure, nil)
	h.expect(run, "build (2)", StatusCompleted, ConclusionCancelled)
	if fresh := h.reload(run); fresh.Status != StatusCompleted || fresh.Conclusion != ConclusionFailure {
		t.Fatalf("run = %s/%s", fresh.Status, fresh.Conclusion)
	}

	relaxed := strings.Replace(src, "strategy:\n", "strategy:\n      fail-fast: false\n", 1)
	run, _ = h.start(relaxed, nil)
	h.finish(run, "build (1)", ConclusionFailure, nil)
	h.expect(run, "build (2)", StatusQueued, "")
}

func TestDeferredMatrixExpandsOnce(t *testing.T) {
	src := "name: deferred\non: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps: [{run: true}]\n  later:\n    needs: a\n    strategy:\n      matrix: ${{ fromJSON(needs.a.outputs.legs) }}\n    runs-on: ubuntu-latest\n    steps: [{run: true}]\n"
	h := newEmitHarness(t)
	run, before := h.start(src, nil)
	placeholder := before["later"]
	h.finish(run, "a", ConclusionSuccess, map[string]any{"legs": `{"leg": ["x", "y"]}`})
	x, y := h.expect(run, "later (x)", StatusQueued, ""), h.expect(run, "later (y)", StatusQueued, "")
	if x.Deferred || y.Deferred || (x.ID != placeholder.ID && y.ID != placeholder.ID) {
		t.Fatalf("placeholder %s must become one leg, got %s %s", placeholder.ID, x.ID, y.ID)
	}
	// A second advance holding the stale placeholder changes nothing
	rows, _ := h.m.store.ListActionsJobs(h.ctx, run.ID)
	p := h.m.planInput(h.ctx, h.repo, run)
	if _, err := h.m.expandDeferred(h.ctx, h.m.scoped(h.ctx, p, placeholder), placeholder, needResults(placeholder, rows)); err != nil {
		t.Fatal(err)
	}
	if again, _ := h.m.store.ListActionsJobs(h.ctx, run.ID); len(again) != len(rows) {
		t.Fatalf("legs duplicated: %d then %d rows", len(rows), len(again))
	}
}

const calledSource = `on:
  workflow_call:
    inputs:
      name:
        type: string
        required: true
      runner:
        type: string
        default: ubuntu-latest
      count:
        type: number
        default: 3
    secrets:
      TOKEN:
        required: true
    outputs:
      result:
        value: ${{ jobs.inner.outputs.r }}
jobs:
  inner:
    runs-on: ${{ inputs.runner }}
    outputs:
      r: ${{ steps.s.outputs.r }}
    steps:
      - id: s
        run: echo "r=${{ inputs.name }}" >> $GITHUB_OUTPUT
`

const callerSource = `name: caller
on: push
jobs:
  call:
    uses: ./.distroface/workflows/called.yml
    with:
      name: x
    secrets:
      TOKEN: ${{ secrets.REAL }}
  after:
    needs: call
    runs-on: ubuntu-latest
    steps: [{run: true}]
`

func TestCallerExpandsCompletesAndNarrowsSecrets(t *testing.T) {
	h := newEmitHarness(t)
	h.secret(h.repo, "REAL", "abc")
	h.secret(h.repo, "OTHER", "def")
	run, before := h.start(callerSource, map[string]string{".distroface/workflows/called.yml": calledSource})
	stale := before["call"]
	call := h.job(run, "call")
	if !call.IsExpanded || call.CallSourceRepo != "acme/app" || call.CallSourceRef != run.SHA || call.CallSourceRepoID != h.repo.ID {
		t.Fatalf("caller = expanded %v source %s@%s", call.IsExpanded, call.CallSourceRepo, call.CallSourceRef)
	}
	inner := h.expect(run, "call / inner", StatusQueued, "")
	if inner.ParentJobID != call.ID || !strings.Contains(inner.RunsOn, "ubuntu-latest") {
		t.Fatalf("inner = parent %s runs-on %s, the caller's inputs resolve runs-on", inner.ParentJobID, inner.RunsOn)
	}
	typed := h.m.jobInputs(h.ctx, run, inner)
	if typed["count"] != float64(3) || typed["name"] != "x" {
		t.Fatalf("typed inputs = %v", typed)
	}
	event := h.m.githubContext(h.ctx, h.repo, run, inner)["event"].(map[string]any)
	if got := event["inputs"].(map[string]any); got["count"] != "3" || got["name"] != "x" {
		t.Fatalf("event inputs = %v, GitHub keeps them as strings", got)
	}
	narrowed, err := h.m.jobSecrets(h.ctx, h.repo, run, inner)
	if err != nil || len(narrowed) != 1 || narrowed["TOKEN"] != "abc" {
		t.Fatalf("called job secrets = %v, %v", narrowed, err)
	}
	full, err := h.m.jobSecrets(h.ctx, h.repo, run, h.job(run, "after"))
	if err != nil || full["REAL"] != "abc" || full["OTHER"] != "def" {
		t.Fatalf("root job secrets = %v, %v", full, err)
	}
	// A second expansion of the same caller is a no-op
	p := h.m.planInput(h.ctx, h.repo, run)
	rows, _ := h.m.store.ListActionsJobs(h.ctx, run.ID)
	if err := h.m.expandCaller(h.ctx, p, stale, needResults(stale, rows)); err != nil {
		t.Fatal(err)
	}
	if children, _ := h.m.store.ListActionsJobsByParent(h.ctx, call.ID); len(children) != 1 {
		t.Fatalf("children duplicated: %d", len(children))
	}
	h.finish(run, "call / inner", ConclusionSuccess, map[string]any{"r": "x-done"})
	call = h.expect(run, "call", StatusCompleted, ConclusionSuccess)
	if !strings.Contains(call.Outputs, `"result":"x-done"`) {
		t.Fatalf("caller outputs = %s", call.Outputs)
	}
	h.expect(run, "after", StatusQueued, "")
	h.finish(run, "after", ConclusionSuccess, nil)
	if fresh := h.reload(run); fresh.Status != StatusCompleted || fresh.Conclusion != ConclusionSuccess {
		t.Fatalf("run = %s/%s", fresh.Status, fresh.Conclusion)
	}
}

func TestCallerWithoutJobsFails(t *testing.T) {
	src := "name: empty\non: push\njobs:\n  call:\n    uses: ./.distroface/workflows/empty.yml\n"
	h := newEmitHarness(t)
	run, _ := h.start(src, map[string]string{".distroface/workflows/empty.yml": "on: workflow_call\njobs: {}\n"})
	call := h.expect(run, "call", StatusCompleted, ConclusionFailure)
	if !strings.Contains(call.Annotations, "called workflow has no jobs") {
		t.Fatalf("annotations = %s", call.Annotations)
	}
	if fresh := h.reload(run); fresh.Conclusion != ConclusionFailure {
		t.Fatalf("run conclusion = %s", fresh.Conclusion)
	}
}

func TestNestedLocalUsesFollowCallerSource(t *testing.T) {
	h := newEmitHarness(t)
	lib := h.newRepo("acme", "lib")
	libSHA := h.commit(lib, "main", map[string]string{
		".github/workflows/b.yml": "on: workflow_call\njobs:\n  tob:\n    uses: ./.github/workflows/c.yml\n",
		".github/workflows/c.yml": "on: workflow_call\njobs:\n  toc:\n    uses: ./.github/workflows/d.yml\n",
		".github/workflows/d.yml": "on: workflow_call\njobs:\n  leaf:\n    runs-on: ubuntu-latest\n    steps: [{run: true}]\n",
	})
	run, _ := h.start("name: nested\non: push\njobs:\n  top:\n    uses: acme/lib/.github/workflows/b.yml@main\n", nil)
	for _, name := range []string{"top", "top / tob", "top / tob / toc"} {
		j := h.job(run, name)
		if !j.IsExpanded || j.CallSourceRepo != "acme/lib" || j.CallSourceRef != libSHA.String() || j.CallSourceRepoID != lib.ID {
			t.Fatalf("%s = expanded %v source %s@%s %s", name, j.IsExpanded, j.CallSourceRepo, j.CallSourceRef, j.Annotations)
		}
	}
	h.expect(run, "top / tob / toc / leaf", StatusQueued, "")
}

func TestCallCycleFailsTheCaller(t *testing.T) {
	h := newEmitHarness(t)
	run, _ := h.start("name: self\non: push\njobs:\n  self:\n    uses: ./.distroface/workflows/ci.yml\n", nil)
	self := h.expect(run, "self", StatusCompleted, ConclusionFailure)
	if !strings.Contains(self.Annotations, "cycle") {
		t.Fatalf("annotations = %s", self.Annotations)
	}
	run, _ = h.start("name: pair\non: push\njobs:\n  top:\n    uses: ./.distroface/workflows/x.yml\n", map[string]string{
		".distroface/workflows/x.yml": "on: workflow_call\njobs:\n  toy:\n    uses: ./.distroface/workflows/y.yml\n",
		".distroface/workflows/y.yml": "on: workflow_call\njobs:\n  tox:\n    uses: ./.distroface/workflows/x.yml\n",
	})
	tox := h.expect(run, "top / toy / tox", StatusCompleted, ConclusionFailure)
	if !strings.Contains(tox.Annotations, "cycle") {
		t.Fatalf("annotations = %s", tox.Annotations)
	}
}

func TestPullRequestTriggerRaisesTargetOnBase(t *testing.T) {
	src := "name: pr\non:\n  pull_request:\n    paths: ['src/**']\n  pull_request_target:\n    branches: [main]\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps: [{run: true}]\n"
	h := newEmitHarness(t)
	mainSHA := h.commit(h.repo, "main", map[string]string{".distroface/workflows/ci.yml": src, "docs/x.md": "x"})
	setBranch(t, h.engine, "acme", "app", "feature", mainSHA)
	head := h.commit(h.repo, "feature", map[string]string{"docs/y.md": "y"})
	payload := func(head plumbing.Hash) map[string]any {
		return map[string]any{"number": float64(1), "pull_request": map[string]any{"number": float64(1), "base": "main", "head": "feature", "head_sha": head.String(), "head_repo": "acme/app"}}
	}
	h.m.trigger(h.ctx, h.repo, "pull_request", "opened", "refs/pull/1/head", head.String(), nil, payload(head))
	runs, _, err := h.m.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Event != "pull_request_target" {
		t.Fatalf("a docs only pull request runs the target workflow alone, got %d runs", len(runs))
	}
	target := runs[0]
	if target.Ref != "refs/heads/main" || target.SHA != mainSHA.String() || target.FromFork || target.NeedsApproval || target.PullNumber != 1 {
		t.Fatalf("target run = %+v", target)
	}
	ctx := h.m.githubContext(h.ctx, h.repo, target, nil)
	if ctx["ref"] != "refs/heads/main" || ctx["base_ref"] != "main" || ctx["head_ref"] != "feature" {
		t.Fatalf("target context ref=%v base=%v head=%v", ctx["ref"], ctx["base_ref"], ctx["head_ref"])
	}

	head = h.commit(h.repo, "feature", map[string]string{"src/a.go": "package a"})
	h.m.trigger(h.ctx, h.repo, "pull_request", "synchronize", "refs/pull/1/head", head.String(), nil, payload(head))
	runs, _, _ = h.m.store.ListActionsRuns(h.ctx, stores.ActionsRunListOptions{RepoID: h.repo.ID, Limit: 10})
	events := map[string]*db.ActionsRun{}
	for _, r := range runs {
		events[r.Event] = r
	}
	pr := events["pull_request"]
	if len(runs) != 3 || pr == nil || pr.SHA != head.String() || pr.Ref != "refs/pull/1/head" {
		t.Fatalf("source change must run both events, got %d runs, pull_request=%+v", len(runs), pr)
	}
	ctx = h.m.githubContext(h.ctx, h.repo, pr, nil)
	if ctx["ref"] != "refs/pull/1/head" || ctx["ref_name"] != "1/head" {
		t.Fatalf("pull_request context ref=%v ref_name=%v", ctx["ref"], ctx["ref_name"])
	}
}
