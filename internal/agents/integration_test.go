package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
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

// Everything a session touches, wired like the container does
type harness struct {
	t      *testing.T
	store  *stores.Store
	git    *gitrepo.Manager
	forge  *forge.Manager
	agents *Manager
	user   *db.User
	actor  *auth.AuthenticatedUser
	repo   *db.GitRepository
	server *httptest.Server
	script *scriptedModel
	ctx    context.Context
}

// Fake chat completions endpoint answering from a scripted list of turns
type scriptedModel struct {
	mu       sync.Mutex
	turns    []scriptedTurn
	requests []map[string]any
}

type scriptedTurn struct {
	text  string
	calls []llm.ToolCall
}

func (s *scriptedModel) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/models" {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "test-model"}}})
		return
	}
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.requests = append(s.requests, req)
	idx := len(s.requests) - 1
	var turn scriptedTurn
	if idx < len(s.turns) {
		turn = s.turns[idx]
	} else {
		turn = scriptedTurn{text: "Done."}
	}
	s.mu.Unlock()
	msg := map[string]any{"role": "assistant", "content": turn.text}
	finish := "stop"
	if len(turn.calls) > 0 {
		var calls []map[string]any
		for _, c := range turn.calls {
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Arguments)}})
		}
		msg["tool_calls"] = calls
		msg["content"] = nil
		finish = "tool_calls"
	}
	// Each prompt carries the last answer and a tool result on top of the one before
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model":   "test-model",
		"choices": []map[string]any{{"message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 100 + 120*idx, "completion_tokens": 20},
	})
}

func call(id, name string, args map[string]any) llm.ToolCall {
	raw, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: name, Arguments: raw}
}

func newHarness(t *testing.T, turns []scriptedTurn) *harness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	log := logger.New()
	store, err := stores.NewSQLiteStore(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatalf("enforcer: %v", err)
	}
	if err := enforcer.SeedDefaultPolicies(); err != nil {
		t.Fatalf("policies: %v", err)
	}
	res := settings.NewResolver(store, nil)
	enable := &v1.Settings{Git: &v1.GitSettings{Enabled: proto.Bool(true), Agents: &v1.GitAgentsSettings{Enabled: proto.Bool(true), AllowPrivateNetworks: proto.Bool(true)}}}
	if _, err := res.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", enable, []string{"git.enabled", "git.agents.enabled", "git.agents.allow_private_networks"}); err != nil {
		t.Fatalf("settings: %v", err)
	}
	authMgr, err := auth.NewManager(store, enforcer, "test-secret-test-secret-test-secret", res)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if err := authMgr.LoadSealingKey("", root); err != nil {
		t.Fatalf("sealing key: %v", err)
	}
	blobs, err := artifacts.NewBlobStore(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	artifactMgr := artifacts.NewManager(store, blobs, res, log)
	engine, err := gitrepo.NewEngine(filepath.Join(root, "git"), root)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	dispatcher := webhook.NewDispatcher(store, log, res)
	recorder := audit.NewRecorder(store, res, log)
	gitMgr := gitrepo.NewManager(store, engine, blobs, res, gitrepo.NewAccess(store, enforcer), dispatcher, recorder, log)
	mail := mailer.NewService(store, res, log)
	forgeMgr := forge.NewManager(store, gitMgr, mail, res, log)
	actionsMgr := actions.NewManager(store, gitMgr, forgeMgr, artifactMgr, authMgr, res, log)
	agentsMgr := NewManager(store, gitMgr, forgeMgr, actionsMgr, authMgr, res, log)
	forgeMgr.SetTrigger(agentsMgr.Trigger)
	gitMgr.Subscribe(forgeMgr.OnRefUpdate)

	user := &db.User{ID: uuid.New().String(), Username: "alice", DisplayName: "Alice", AuthProvider: "local", IsActive: true}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("user: %v", err)
	}
	actor := &auth.AuthenticatedUser{ID: user.ID, Username: user.Username, Roles: []string{"user"}, Provider: "local"}
	repo, err := gitMgr.Create(ctx, actor, gitrepo.CreateParams{Namespace: "alice", Name: "app", AutoInit: true, DefaultBranch: "main"})
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	units := gitrepo.UnitsOf(repo)
	units.Agents = true
	repo.Units = gitrepo.EncodeUnits(units)
	if err := store.UpdateGitRepository(ctx, repo); err != nil {
		t.Fatalf("units: %v", err)
	}
	res.Invalidate()

	script := &scriptedModel{turns: turns}
	server := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(server.Close)
	key := "test-key"
	if _, err := agentsMgr.CreateProvider(ctx, db.ActionsScopeSystem, "", ProviderSpec{
		Name: "fake", Kind: llm.KindOpenAI, BaseURL: server.URL, APIKey: &key, DefaultModel: "test-model", ToolsSupported: true, Enabled: true, IsDefault: true, TimeoutSeconds: 10,
	}, user.ID); err != nil {
		t.Fatalf("provider: %v", err)
	}
	return &harness{t: t, store: store, git: gitMgr, forge: forgeMgr, agents: agentsMgr, user: user, actor: actor, repo: repo, server: server, script: script, ctx: ctx}
}

func (h *harness) defineAgent(spec AgentSpec) *Definition {
	h.t.Helper()
	row, err := h.agents.CreateAgent(h.ctx, db.ActionsScopeSystem, "", spec, h.user.ID)
	if err != nil {
		h.t.Fatalf("agent: %v", err)
	}
	def, err := h.agents.FindAgentForRepo(h.ctx, h.repo, row.Slug)
	if err != nil {
		h.t.Fatalf("find agent: %v", err)
	}
	return def
}

func (h *harness) waitFor(sessionID string, want func(*db.AgentSession) bool) *db.AgentSession {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		sess, err := h.store.GetAgentSessionByID(h.ctx, sessionID)
		if err != nil {
			h.t.Fatalf("session: %v", err)
		}
		if sess != nil && want(sess) {
			return sess
		}
		time.Sleep(50 * time.Millisecond)
	}
	sess, _ := h.store.GetAgentSessionByID(h.ctx, sessionID)
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sessionID, 0, 0)
	for _, ev := range events {
		h.t.Logf("event %d %s %s %s %s", ev.Seq, ev.Kind, ev.Tool, firstLine(ev.Text), ev.Data)
	}
	h.t.Fatalf("session never reached the wanted state, status=%s conclusion=%s error=%s", sess.Status, sess.Conclusion, sess.Error)
	return nil
}

func completed(s *db.AgentSession) bool { return s.Status == StatusCompleted }

func TestCodeSessionCommitsAndOpensPullRequest(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "read_file", map[string]any{"path": "README.md"})}},
		{calls: []llm.ToolCall{call("c2", "write_file", map[string]any{"path": "hello.txt", "content": "hello world\n"})}},
		{calls: []llm.ToolCall{call("c3", "commit", map[string]any{"message": "Add hello"})}},
		{calls: []llm.ToolCall{call("c4", "open_pull_request", map[string]any{"title": "Add hello", "body": "Adds a greeting."})}},
		{text: "Added hello.txt and opened a pull request."},
	})
	def := h.defineAgent(AgentSpec{Slug: "coder", Name: "Coder", Kind: KindCode, Approval: ApprovalNone, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Add a hello file", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess {
		t.Fatalf("conclusion %s, error %q", done.Conclusion, done.Error)
	}
	if done.ResultPullNumber == 0 {
		t.Fatal("no pull request opened")
	}
	if done.Branch != "agents/coder/1" {
		t.Fatalf("branch %q", done.Branch)
	}
	var commits []string
	_ = json.Unmarshal([]byte(done.ResultCommits), &commits)
	if len(commits) != 1 {
		t.Fatalf("commits %v", commits)
	}
	if !strings.Contains(done.Summary, "hello") {
		t.Fatalf("summary %q", done.Summary)
	}
	if done.Turns != 5 || done.InputTokens != 500 || done.OutputTokens != 100 {
		t.Fatalf("usage turns=%d in=%d out=%d", done.Turns, done.InputTokens, done.OutputTokens)
	}
	if done.PromptTokens != 1700 || done.ContextTokens != 600 {
		t.Fatalf("billed prompt=%d context=%d", done.PromptTokens, done.ContextTokens)
	}
	// The branch holds the file and the pull request belongs to the bot
	r, err := h.git.Engine().OpenRepo(h.repo.Namespace, h.repo.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tip := r.BranchHash("agents/coder/1")
	if tip.IsZero() {
		t.Fatal("working branch missing")
	}
	blob, err := r.Blob(tip, "hello.txt", 1024)
	if err != nil || string(blob.Content) != "hello world\n" {
		t.Fatalf("hello.txt %v %v", err, blob)
	}
	issue, err := h.store.GetGitIssue(h.ctx, h.repo.ID, done.ResultPullNumber)
	if err != nil || issue == nil || !issue.IsPull {
		t.Fatalf("pull request row %v %v", issue, err)
	}
	if !h.agents.IsAgentUser(h.ctx, issue.AuthorID) {
		t.Fatal("pull request author is not the bot")
	}
	bot, _ := h.store.GetUserByID(h.ctx, issue.AuthorID)
	if bot == nil || bot.Username != "coder" || bot.AuthProvider != db.AuthProviderAgent {
		t.Fatalf("bot user %+v", bot)
	}
	// The model saw the toolset and the task
	h.script.mu.Lock()
	first := h.script.requests[0]
	h.script.mu.Unlock()
	if tools, _ := first["tools"].([]any); len(tools) == 0 {
		t.Fatal("no tools sent to the model")
	}
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	kinds := map[string]int{}
	for _, ev := range events {
		kinds[ev.Kind]++
	}
	if kinds[EventToolCall] != 4 || kinds[EventToolResult] != 4 || kinds[EventResult] != 1 || kinds[EventSystem] != 1 {
		t.Fatalf("event kinds %v", kinds)
	}
}

func TestReviewSessionPausesForApproval(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "get_diff", map[string]any{})}},
		{calls: []llm.ToolCall{call("c2", "submit_review", map[string]any{"event": "comment", "body": "Looks fine.", "comments": []map[string]any{{"path": "feature.txt", "line": 1, "body": "nit: wording"}}})}},
		{text: "Reviewed."},
	})
	// A branch with a change and a pull request from alice
	headTip, err := h.git.CommitToBranch(h.ctx, h.repo, h.actor, gitrepo.CommitRequest{Branch: "main", NewBranch: "feature", Message: "feature", Edits: []gitrepo.TreeEdit{{Path: "feature.txt", Mode: 0o100644, Content: strings.NewReader("new feature\n")}}})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	_ = headTip
	issue, _, err := h.forge.CreatePull(h.ctx, h.repo, forge.Actor{User: h.actor, CanWrite: true}, forge.NewPull{Base: "main", Head: "feature", Title: "Feature"})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	def := h.defineAgent(AgentSpec{Slug: "reviewer", Name: "Reviewer", Kind: KindReview, Approval: ApprovalAllWrites, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, IssueNumber: issue.Number, Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waiting := h.waitFor(sess.ID, func(s *db.AgentSession) bool { return s.Status == StatusWaitingApproval })
	pending, err := h.store.GetPendingAgentApproval(h.ctx, waiting.ID)
	if err != nil || pending == nil || pending.Tool != "submit_review" {
		t.Fatalf("pending approval %v %v", pending, err)
	}
	if err := h.agents.ResolveApproval(h.ctx, waiting, pending, true, "go ahead", h.actor); err != nil {
		t.Fatalf("approve: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess || done.ResultReviewID == "" {
		t.Fatalf("conclusion %s review %q error %q", done.Conclusion, done.ResultReviewID, done.Error)
	}
	reviews, _, err := h.store.ListGitReviews(h.ctx, issue.ID, false, 0, 0)
	if err != nil || len(reviews) != 1 || reviews[0].State != forge.ReviewCommented {
		t.Fatalf("reviews %v %v", reviews, err)
	}
	comments, err := h.store.ListGitReviewComments(h.ctx, issue.ID, "", "")
	if err != nil || len(comments) != 1 || comments[0].Path != "feature.txt" || comments[0].Line != 1 {
		t.Fatalf("inline comments %v %v", comments, err)
	}
}

func TestMentionStartsSessionAndBotsAreIgnored(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "post_comment", map[string]any{"body": "Here is the answer."})}},
		{text: "Answered."},
	})
	def := h.defineAgent(AgentSpec{Slug: "helper", Name: "Helper", Kind: KindChat, Approval: ApprovalNone, Enabled: true, Triggers: &v1.AgentTriggers{Mention: true}})
	issue, err := h.forge.CreateIssue(h.ctx, h.repo, forge.Actor{User: h.actor, CanWrite: true}, forge.NewIssue{Title: "Question", Body: "How does this work?"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// The forge comment path fires the mention trigger through the installed hook
	if _, err := h.forge.CreateComment(h.ctx, h.repo, forge.Actor{User: h.actor, CanWrite: true}, issue, "@helper can you explain?", false); err != nil {
		t.Fatalf("comment: %v", err)
	}
	var sess *db.AgentSession
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rows, _, _ := h.store.ListAgentSessions(h.ctx, stores.AgentSessionListOptions{RepoID: h.repo.ID, Trigger: TriggerMention})
		if len(rows) > 0 {
			sess = rows[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sess == nil {
		t.Fatal("mention did not start a session")
	}
	if !strings.Contains(sess.Prompt, "can you explain?") || strings.Contains(sess.Prompt, "@helper") {
		t.Fatalf("prompt %q", sess.Prompt)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess {
		t.Fatalf("conclusion %s error %q", done.Conclusion, done.Error)
	}
	// The bot's own comment must not trigger another session
	time.Sleep(300 * time.Millisecond)
	rows, total, _ := h.store.ListAgentSessions(h.ctx, stores.AgentSessionListOptions{RepoID: h.repo.ID})
	if total != 1 {
		t.Fatalf("expected one session, got %d: %+v", total, rows)
	}
	comments, _, _ := h.store.ListGitComments(h.ctx, issue.ID, true, 0, 0)
	if len(comments) != 2 {
		t.Fatalf("expected the question and one answer, got %d", len(comments))
	}
	if _, err := h.agents.Identity(h.ctx, def); err != nil {
		t.Fatal(err)
	}
	if !h.agents.IsAgentUser(h.ctx, comments[1].AuthorID) {
		t.Fatal("answer was not posted by the bot")
	}
}

func TestSessionLimitsAndCancel(t *testing.T) {
	// A model that never stops calling tools hits the turn cap
	var turns []scriptedTurn
	for range 20 {
		turns = append(turns, scriptedTurn{calls: []llm.ToolCall{call("c", "list_files", map[string]any{})}})
	}
	h := newHarness(t, turns)
	def := h.defineAgent(AgentSpec{Slug: "looper", Name: "Looper", Kind: KindChat, Approval: ApprovalNone, Enabled: true, MaxTurns: 3})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "loop", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionBudgetExceeded || done.Turns != 3 {
		t.Fatalf("conclusion %s turns %d", done.Conclusion, done.Turns)
	}
	// Continuing a finished session queues it again with the new instruction on the transcript
	if err := h.agents.Continue(h.ctx, done, "stop looping", h.actor); err != nil {
		t.Fatalf("continue: %v", err)
	}
	again := h.waitFor(sess.ID, func(s *db.AgentSession) bool { return s.Status == StatusCompleted && s.Turns > 3 })
	if again.Turns < 4 {
		t.Fatalf("continue did not run, turns %d", again.Turns)
	}
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	users := 0
	for _, ev := range events {
		if ev.Kind == EventUser {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("expected two user turns, got %d", users)
	}
}

func TestSearchIssuesToolQueriesTheStore(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "search_issues", map[string]any{"query": "metrics readable", "state": "open"})}},
		{calls: []llm.ToolCall{call("c2", "search_issues", map[string]any{})}},
		{text: "Done."},
	})
	def := h.defineAgent(AgentSpec{Slug: "triager", Name: "Triager", Kind: KindTriage, Approval: ApprovalNone, Enabled: true})
	if _, err := h.forge.CreateIssue(h.ctx, h.repo, forge.Actor{User: h.actor, CanWrite: true}, forge.NewIssue{Title: "Make sorted metrics readable", Body: "Hard to read."}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Find the issue", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess {
		t.Fatalf("conclusion %s error %q", done.Conclusion, done.Error)
	}
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	var results []string
	for _, ev := range events {
		if ev.Kind == EventToolResult && ev.Tool == "search_issues" {
			results = append(results, ev.Text)
		}
	}
	if len(results) != 2 {
		t.Fatalf("expected two search results, got %q", results)
	}
	for _, text := range results {
		if strings.HasPrefix(text, "Error:") || !strings.Contains(text, "Make sorted metrics readable") {
			t.Fatalf("search result %q", text)
		}
	}
}

func TestSessionOpensIssueAndAdoptsIt(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "create_issue", map[string]any{"title": "Close session keybind is unset", "body": "Expected ctrl+d.", "labels": []string{"bug"}, "create_labels": true})}},
		{calls: []llm.ToolCall{call("c2", "post_comment", map[string]any{"body": "Looking into it."})}},
		{calls: []llm.ToolCall{call("c3", "post_comment", map[string]any{"number": 1, "body": "Related to the new issue."})}},
		{calls: []llm.ToolCall{call("c4", "write_file", map[string]any{"path": "keys.txt", "content": "ctrl+d\n"})}},
		{calls: []llm.ToolCall{call("c5", "commit", map[string]any{"message": "Set the default"})}},
		{calls: []llm.ToolCall{call("c6", "open_pull_request", map[string]any{"title": "Set the default", "body": "Sets ctrl+d."})}},
		{text: "Opened the issue and a pull request."},
	})
	older, err := h.forge.CreateIssue(h.ctx, h.repo, forge.Actor{User: h.actor, CanWrite: true}, forge.NewIssue{Title: "Older", Body: "An existing issue."})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	def := h.defineAgent(AgentSpec{Slug: "filer", Name: "Filer", Kind: KindCustom, Approval: ApprovalNone, Enabled: true,
		Tools: []string{"create_issue", "post_comment", "write_file", "commit", "open_pull_request"}})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Open an issue for the missing keybind, then fix it", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess {
		t.Fatalf("conclusion %s error %q", done.Conclusion, done.Error)
	}
	// The model saw every configured tool although the session started without a target
	h.script.mu.Lock()
	first := h.script.requests[0]
	h.script.mu.Unlock()
	offered := map[string]bool{}
	for _, raw := range first["tools"].([]any) {
		fn := raw.(map[string]any)["function"].(map[string]any)
		offered[fn["name"].(string)] = true
	}
	for _, name := range []string{"create_issue", "post_comment", "commit", "open_pull_request"} {
		if !offered[name] {
			t.Fatalf("%s was withheld from the model, offered %v", name, offered)
		}
	}
	// The new issue is the bot's, carries the label, and became the session target
	issue, err := h.store.GetGitIssue(h.ctx, h.repo.ID, 2)
	if err != nil || issue == nil || issue.IsPull || issue.Title != "Close session keybind is unset" {
		t.Fatalf("issue %v %v", issue, err)
	}
	if !h.agents.IsAgentUser(h.ctx, issue.AuthorID) {
		t.Fatal("issue author is not the bot")
	}
	if done.IssueID == nil || *done.IssueID != issue.ID || done.IssueNumber != issue.Number || done.IsPull {
		t.Fatalf("session did not adopt the issue: %+v", done)
	}
	labels, _ := h.store.GetGitIssueLabels(h.ctx, []string{issue.ID})
	if len(labels[issue.ID]) != 1 || labels[issue.ID][0].Name != "bug" {
		t.Fatalf("labels %v", labels[issue.ID])
	}
	// The untargeted comment went to the new issue, the numbered one to the older issue
	comments, _, _ := h.store.ListGitComments(h.ctx, issue.ID, true, 0, 0)
	if len(comments) != 1 || comments[0].Body != "Looking into it." {
		t.Fatalf("new issue comments %+v", comments)
	}
	olderComments, _, _ := h.store.ListGitComments(h.ctx, older.ID, true, 0, 0)
	if len(olderComments) != 1 || olderComments[0].Body != "Related to the new issue." {
		t.Fatalf("older issue comments %+v", olderComments)
	}
	// The pull request closes the adopted issue
	if done.ResultPullNumber != 3 {
		t.Fatalf("pull request number %d", done.ResultPullNumber)
	}
	pull, err := h.store.GetGitIssue(h.ctx, h.repo.ID, 3)
	if err != nil || pull == nil || !pull.IsPull || !strings.Contains(pull.Body, "Closes #2") {
		t.Fatalf("pull request %v %v", pull, err)
	}
}

func TestFinalReplyIsRecordedOnce(t *testing.T) {
	h := newHarness(t, []scriptedTurn{
		{calls: []llm.ToolCall{call("c1", "read_file", map[string]any{"path": "README.md"})}},
		{text: "All done here."},
	})
	def := h.defineAgent(AgentSpec{Slug: "helper", Name: "Helper", Kind: KindChat, Approval: ApprovalNone, Enabled: true})
	sess, err := h.agents.StartSession(h.ctx, StartParams{Repo: h.repo, Def: def, Prompt: "Look around", Trigger: TriggerManual, Actor: h.actor})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := h.waitFor(sess.ID, completed)
	if done.Conclusion != ConclusionSuccess || done.Summary != "All done here." {
		t.Fatalf("conclusion %s summary %q error %q", done.Conclusion, done.Summary, done.Error)
	}
	events, _ := h.store.ListAgentSessionEvents(h.ctx, sess.ID, 0, 0)
	assistant, results := 0, 0
	for _, ev := range events {
		switch {
		case ev.Kind == EventAssistant && strings.Contains(ev.Text, "All done here"):
			assistant++
		case ev.Kind == EventResult:
			results++
			if ev.Text != "All done here." {
				t.Fatalf("result = %q", ev.Text)
			}
		}
	}
	if assistant != 0 || results != 1 {
		t.Fatalf("the final reply appears as %d assistant and %d result events, want only the result", assistant, results)
	}
}
