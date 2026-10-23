package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/webhook"
)

// Approval states
const (
	approvalPending  = "pending"
	approvalApproved = "approved"
	approvalRejected = "rejected"
)

const (
	completionRetries     = 3
	approxCharsPerToken   = 3
	keepRecentToolResults = 4
)

// StartParams describes a session to queue
type StartParams struct {
	Repo        *db.GitRepository
	Def         *Definition
	Prompt      string
	Ref         string
	IssueNumber int64
	Model       string
	Trigger     string
	Actor       *auth.AuthenticatedUser
	// Bypasses the allowed callers check for automatic triggers
	System bool
}

// StartSession validates and queues a session
func (m *Manager) StartSession(ctx context.Context, p StartParams) (*db.AgentSession, error) {
	repo, def := p.Repo, p.Def
	if repo == nil || def == nil {
		return nil, fmt.Errorf("%w: repository and agent are required", ErrInvalid)
	}
	if !m.Enabled(ctx, repo) {
		return nil, ErrDisabled
	}
	if !def.Enabled {
		return nil, fmt.Errorf("%w: agent %s is disabled", ErrDisabled, def.Slug)
	}
	if repo.IsArchived {
		return nil, fmt.Errorf("%w: repository is archived", ErrConflict)
	}
	cfg := m.Settings(ctx, repo)
	if !p.System && len(def.AllowedCallerIDs) > 0 {
		if p.Actor == nil || !m.git.Access().InAllowList(ctx, p.Actor, repo, def.AllowedCallerIDs) {
			return nil, fmt.Errorf("%w: you are not allowed to start %s", ErrDenied, def.Slug)
		}
	}
	if capPerDay := cfg.GetMaxSessionsPerDay(); capPerDay > 0 {
		n, err := m.store.CountAgentSessionsSince(ctx, repo.ID, time.Now().UTC().Truncate(24*time.Hour))
		if err != nil {
			return nil, err
		}
		if n >= int64(capPerDay) {
			return nil, fmt.Errorf("%w: this repository reached its %d sessions for today", ErrBudget, capPerDay)
		}
	}
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = def.Model
	}
	provider, err := m.ResolveProvider(ctx, repo, def.ProviderID, model)
	if err != nil {
		return nil, err
	}
	if err := m.checkProviderBudget(ctx, provider.Row); err != nil {
		return nil, err
	}
	bot, err := m.Identity(ctx, def)
	if err != nil {
		return nil, err
	}
	sess := &db.AgentSession{
		RepoID: repo.ID, AgentID: def.ID, AgentSlug: def.Slug, AgentName: def.Name, AgentKind: def.Kind, AgentSource: def.Source, BotUserID: bot.ID,
		ProviderID: provider.Row.ID, ProviderName: provider.Row.Name, Model: provider.Model,
		Trigger: p.Trigger, Prompt: strings.TrimSpace(p.Prompt), Ref: strings.TrimSpace(p.Ref), Status: StatusQueued,
		Definition: def.marshal(), MaxTurns: effectiveTurns(def, cfg), MaxTokens: effectiveTokens(def, cfg),
	}
	if sess.Trigger == "" {
		sess.Trigger = TriggerManual
	}
	if p.Actor != nil && !p.Actor.IsAnonymous() {
		sess.TriggerUserID = p.Actor.ID
	}
	if p.IssueNumber > 0 {
		issue, err := m.forge.Issue(ctx, repo, p.IssueNumber)
		if err != nil {
			return nil, fmt.Errorf("%w: #%d", ErrNotFound, p.IssueNumber)
		}
		sess.IssueID, sess.IssueNumber, sess.IsPull = &issue.ID, issue.Number, issue.IsPull
		if issue.IsPull && sess.Ref == "" {
			if pr, err := m.forge.Pull(ctx, issue); err == nil {
				sess.Ref, sess.SHA = pr.HeadBranch, pr.HeadSHA
			}
		}
		sess.Title = fmt.Sprintf("#%d %s", issue.Number, issue.Title)
	}
	if sess.Prompt == "" {
		sess.Prompt = defaultPrompt(def.Kind, sess.Trigger, sess.IssueNumber, sess.IsPull)
	}
	if sess.Title == "" {
		sess.Title = firstLine(sess.Prompt)
		if len(sess.Title) > 80 {
			sess.Title = sess.Title[:80] + "..."
		}
	}
	if sess.Ref == "" {
		sess.Ref = repo.DefaultBranch
	}
	if err := m.store.CreateAgentSession(ctx, sess); err != nil {
		return nil, err
	}
	m.record(ctx, sess, EventUser, sess.Prompt, "", "", map[string]any{"trigger": sess.Trigger, "user": m.usernameOf(ctx, sess.TriggerUserID)})
	m.record(ctx, sess, EventStatus, "Queued", "", "", map[string]any{"status": StatusQueued})
	actorName, actorID := "system", ""
	if p.Actor != nil {
		actorName, actorID = p.Actor.Username, p.Actor.ID
	}
	m.git.Audit(ctx, audit.Event{Action: "Agents/session", Resource: "agents", Outcome: audit.OutcomeSuccess,
		Detail: fmt.Sprintf("%s/%s#%d %s %s", repo.Namespace, repo.Name, sess.Number, def.Slug, sess.ID), Actor: actorName, ActorID: actorID})
	m.kick(repo.ID)
	return sess, nil
}

// Zero at both levels means unlimited
func effectiveTurns(def *Definition, cfg interface{ GetMaxTurns() int32 }) int {
	limit := int(cfg.GetMaxTurns())
	if def.MaxTurns > 0 && (limit <= 0 || def.MaxTurns < limit) {
		return def.MaxTurns
	}
	return limit
}

func effectiveTokens(def *Definition, cfg interface{ GetTokenBudgetPerSession() int64 }) int64 {
	limit := cfg.GetTokenBudgetPerSession()
	if def.MaxTokens > 0 && (limit <= 0 || def.MaxTokens < limit) {
		return def.MaxTokens
	}
	return limit
}

// Launches queued sessions of a repository within the concurrency limits
func (m *Manager) kick(repoID string) {
	m.kickMu.Lock()
	if m.kicking[repoID] {
		m.kickMu.Unlock()
		return
	}
	m.kicking[repoID] = true
	m.kickMu.Unlock()
	go func() {
		defer func() {
			m.kickMu.Lock()
			delete(m.kicking, repoID)
			m.kickMu.Unlock()
		}()
		ctx := m.base
		repo, err := m.store.GetGitRepositoryByID(ctx, repoID)
		if err != nil || repo == nil {
			return
		}
		queued, err := m.store.ListQueuedAgentSessions(ctx, repoID)
		if err != nil || len(queued) == 0 {
			return
		}
		limit := int64(m.Settings(ctx, repo).GetMaxConcurrentSessions())
		for _, sess := range queued {
			active, _ := m.store.CountActiveAgentSessions(ctx, repoID, "")
			if limit > 0 && active >= limit {
				return
			}
			if def := definitionFromJSON(sess.Definition); def != nil && def.Concurrency > 0 {
				own, _ := m.store.CountActiveAgentSessions(ctx, repoID, sess.AgentID)
				if own >= int64(def.Concurrency) {
					continue
				}
			}
			m.launch(sess)
		}
	}()
}

// Marks a session running and starts its goroutine
func (m *Manager) launch(sess *db.AgentSession) {
	m.runMu.Lock()
	if _, exists := m.running[sess.ID]; exists {
		m.runMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	m.running[sess.ID] = cancel
	m.runMu.Unlock()
	cols := map[string]any{"status": StatusRunning, "updated_at": time.Now().UTC()}
	if sess.StartedAt == nil {
		sess.StartedAt = nowPtr()
		cols["started_at"] = *sess.StartedAt
	}
	sess.Status = StatusRunning
	_ = m.store.UpdateAgentSessionColumns(ctx, sess.ID, cols)
	m.record(ctx, sess, EventStatus, "Running", "", "", map[string]any{"status": StatusRunning})
	go m.run(ctx, sess)
}

func (m *Manager) release(sessionID string) {
	m.runMu.Lock()
	if cancel, ok := m.running[sessionID]; ok {
		cancel()
		delete(m.running, sessionID)
	}
	m.runMu.Unlock()
}

// Executes the agent loop of one session until it finishes or pauses
func (m *Manager) run(ctx context.Context, sess *db.AgentSession) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("agents: session %s panicked: %v", sess.ID, r)
			m.finish(m.base, sess, nil, ConclusionFailure, "", fmt.Sprintf("internal error: %v", r))
		}
		m.release(sess.ID)
		m.kick(sess.RepoID)
	}()
	repo, err := m.store.GetGitRepositoryByID(ctx, sess.RepoID)
	if err != nil || repo == nil {
		m.finish(ctx, sess, nil, ConclusionFailure, "", "repository no longer exists")
		return
	}
	cfg := m.Settings(ctx, repo)
	if minutes := cfg.GetMaxSessionMinutes(); minutes > 0 && sess.StartedAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, sess.StartedAt.Add(time.Duration(minutes)*time.Minute))
		defer cancel()
	}
	rc, err := m.buildRunContext(ctx, sess, repo)
	if err != nil {
		m.finish(ctx, sess, rc, ConclusionFailure, "", err.Error())
		return
	}
	msgs, pending, err := m.transcript(ctx, rc)
	if err != nil {
		m.finish(ctx, sess, rc, ConclusionFailure, "", err.Error())
		return
	}
	tools := toolsFor(rc.def, cfg, rc.issue != nil, rc.pr != nil)

	// Calls interrupted by an approval pause or a restart come first
	if len(pending) > 0 {
		results, paused := m.executeCalls(rc, pending, true)
		if paused {
			return
		}
		msgs = append(msgs, results...)
	}

	for {
		if ctx.Err() != nil {
			m.finishContext(ctx, sess, rc)
			return
		}
		if sess.MaxTurns > 0 && sess.Turns >= sess.MaxTurns {
			m.finish(ctx, sess, rc, ConclusionBudgetExceeded, lastAssistantText(msgs), fmt.Sprintf("turn limit of %d reached", sess.MaxTurns))
			return
		}
		if sess.MaxTokens > 0 && sess.InputTokens+sess.OutputTokens >= sess.MaxTokens {
			m.finish(ctx, sess, rc, ConclusionBudgetExceeded, lastAssistantText(msgs), fmt.Sprintf("token budget of %d reached", sess.MaxTokens))
			return
		}
		if err := m.checkProviderBudget(ctx, rc.provider.Row); err != nil {
			m.finish(ctx, sess, rc, ConclusionBudgetExceeded, lastAssistantText(msgs), err.Error())
			return
		}
		resp, err := m.complete(rc, msgs, tools)
		if err != nil {
			if ctx.Err() != nil {
				m.finishContext(ctx, sess, rc)
				return
			}
			m.finish(ctx, sess, rc, ConclusionFailure, lastAssistantText(msgs), err.Error())
			return
		}
		sess.Turns++
		sess.InputTokens += resp.Usage.InputTokens
		sess.OutputTokens += resp.Usage.OutputTokens
		_ = m.store.UpdateAgentSessionColumns(ctx, sess.ID, map[string]any{"turns": sess.Turns, "input_tokens": sess.InputTokens, "output_tokens": sess.OutputTokens, "updated_at": time.Now().UTC()})
		m.record(ctx, sess, EventUsage, "", "", "", map[string]any{"input_tokens": resp.Usage.InputTokens, "output_tokens": resp.Usage.OutputTokens, "total_input": sess.InputTokens, "total_output": sess.OutputTokens, "turn": sess.Turns, "model": resp.Model})

		calls := resp.ToolCalls
		m.record(ctx, sess, EventAssistant, resp.Content, "", "", map[string]any{"tool_calls": encodeCalls(calls), "stop_reason": resp.StopReason})
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: calls})
		if len(calls) == 0 {
			if resp.StopReason == llm.StopLength && strings.TrimSpace(resp.Content) == "" {
				m.finish(ctx, sess, rc, ConclusionFailure, "", "the model returned an empty answer at its output limit")
				return
			}
			m.finish(ctx, sess, rc, ConclusionSuccess, strings.TrimSpace(resp.Content), "")
			return
		}
		results, paused := m.executeCalls(rc, calls, false)
		if paused {
			return
		}
		msgs = append(msgs, results...)
	}
}

// Ends a session whose context closed, by cancel or by deadline
func (m *Manager) finishContext(ctx context.Context, sess *db.AgentSession, rc *runContext) {
	fresh, _ := m.store.GetAgentSessionByID(m.base, sess.ID)
	if fresh != nil && fresh.Status == StatusCompleted {
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		m.finish(m.base, sess, rc, ConclusionTimedOut, "", "the session exceeded its time limit")
		return
	}
	m.finish(m.base, sess, rc, ConclusionCancelled, "", "")
}

// Resolves everything a run needs, target, identity, connector, workspace
func (m *Manager) buildRunContext(ctx context.Context, sess *db.AgentSession, repo *db.GitRepository) (*runContext, error) {
	def := definitionFromJSON(sess.Definition)
	if def == nil {
		return nil, fmt.Errorf("session definition is unreadable")
	}
	bot, botUser, err := m.BotActor(ctx, def)
	if err != nil {
		return nil, err
	}
	provider, err := m.ResolveProvider(ctx, repo, sess.ProviderID, sess.Model)
	if err != nil {
		if sess.ProviderID != "" {
			// The connector went away, fall back to the scope default
			provider, err = m.ResolveProvider(ctx, repo, "", "")
		}
		if err != nil {
			return nil, err
		}
	}
	rc := &runContext{m: m, ctx: ctx, sess: sess, repo: repo, def: def, bot: bot, botUser: botUser, provider: provider, settings: m.Settings(ctx, repo)}
	rc.actor = forge.Actor{User: bot, CanWrite: true}
	if sess.IssueID != nil {
		issue, err := m.store.GetGitIssueByID(ctx, *sess.IssueID)
		if err != nil {
			return nil, err
		}
		if issue == nil || issue.RepoID != repo.ID {
			return nil, fmt.Errorf("the targeted issue no longer exists")
		}
		rc.issue = issue
		if issue.IsPull {
			pr, err := m.forge.Pull(ctx, issue)
			if err != nil {
				return nil, err
			}
			rc.pr = pr
		}
	}
	if err := m.prepareWorkspace(rc); err != nil {
		return nil, err
	}
	return rc, nil
}

// Chooses the working branch and resolves its tip
func (m *Manager) prepareWorkspace(rc *runContext) error {
	sess, repo := rc.sess, rc.repo
	baseBranch := sess.Ref
	if baseBranch == "" {
		baseBranch = repo.DefaultBranch
	}
	branch := sess.Branch
	if rc.pr != nil {
		// Feedback on a pull request this agent opened lands on the pull request itself
		if rc.pr.HeadRepoID == repo.ID && rc.issue.AuthorID == rc.botUser.ID {
			baseBranch, branch = rc.pr.BaseBranch, rc.pr.HeadBranch
		} else if rc.pr.HeadRepoID == repo.ID {
			// Proposals onto someone else's pull request target its head branch
			baseBranch = rc.pr.HeadBranch
		} else {
			baseBranch = rc.pr.BaseBranch
		}
	}
	if branch == "" {
		branch = fmt.Sprintf("agents/%s/%d", rc.def.Slug, sess.Number)
	}
	r, err := rc.openRepo()
	if err != nil {
		return err
	}
	defer r.Close()
	head := r.BranchHash(branch)
	if head.IsZero() {
		head = r.BranchHash(baseBranch)
	}
	if head.IsZero() && rc.pr != nil && rc.pr.HeadSHA != "" {
		head = plumbing.NewHash(rc.pr.HeadSHA)
	}
	if head.IsZero() && !r.IsEmpty() {
		if h, err := r.Resolve(baseBranch); err == nil {
			head = h
		}
	}
	rc.ws = newWorkspace(repo, branch, baseBranch, head)
	if sess.ResultCommits != "" {
		_ = json.Unmarshal([]byte(sess.ResultCommits), &rc.ws.commits)
	}
	if sess.Branch != branch {
		sess.Branch = branch
		_ = m.store.UpdateAgentSessionColumns(rc.ctx, sess.ID, map[string]any{"branch": branch})
	}
	if sess.SHA == "" && !head.IsZero() {
		sess.SHA = head.String()
		_ = m.store.UpdateAgentSessionColumns(rc.ctx, sess.ID, map[string]any{"sha": sess.SHA})
	}
	return nil
}

// Stored shape of one tool call
type storedCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func encodeCalls(calls []llm.ToolCall) []storedCall {
	out := make([]storedCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, storedCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	return out
}

// Rebuilds the conversation from the transcript, returning tool calls still owed a result
func (m *Manager) transcript(ctx context.Context, rc *runContext) ([]llm.Message, []llm.ToolCall, error) {
	events, err := m.store.ListAgentSessionEvents(ctx, rc.sess.ID, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	system := ""
	for _, ev := range events {
		if ev.Kind == EventSystem {
			system = ev.Text
			break
		}
	}
	if system == "" {
		system = m.systemPrompt(rc)
		m.record(ctx, rc.sess, EventSystem, system, "", "", nil)
	}
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: system}}
	answered := map[string]bool{}
	var lastCalls []llm.ToolCall
	firstUser := true
	for _, ev := range events {
		switch ev.Kind {
		case EventUser:
			text := ev.Text
			if firstUser {
				text = m.initialMessage(rc, ev.Text)
				firstUser = false
			}
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: text})
		case EventAssistant:
			var data struct {
				ToolCalls []storedCall `json:"tool_calls"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &data)
			msg := llm.Message{Role: llm.RoleAssistant, Content: ev.Text}
			lastCalls = nil
			for _, c := range data.ToolCalls {
				tc := llm.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments}
				msg.ToolCalls = append(msg.ToolCalls, tc)
				lastCalls = append(lastCalls, tc)
			}
			msgs = append(msgs, msg)
		case EventToolResult:
			answered[ev.ToolCallID] = true
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, Content: ev.Text, ToolCallID: ev.ToolCallID, ToolName: ev.Tool})
		}
	}
	var pending []llm.ToolCall
	for _, c := range lastCalls {
		if !answered[c.ID] {
			pending = append(pending, c)
		}
	}
	// Tool results must follow their assistant turn, drop stragglers that cannot be placed
	return msgs, pending, nil
}

// Runs tool calls in order, pausing on the first one that needs approval
func (m *Manager) executeCalls(rc *runContext, calls []llm.ToolCall, resumed bool) ([]llm.Message, bool) {
	var out []llm.Message
	ctx := rc.ctx
	for _, call := range calls {
		if ctx.Err() != nil {
			return out, false
		}
		started := time.Now()
		spec := toolRegistry[call.Name]
		args := map[string]any{}
		if len(call.Arguments) > 0 {
			_ = json.Unmarshal(call.Arguments, &args)
		}
		var result string
		var err error
		switch {
		case spec == nil:
			err = fmt.Errorf("unknown tool %q", call.Name)
		case !toolAllowed(rc, call.Name):
			err = fmt.Errorf("tool %q is not available to this agent", call.Name)
		default:
			approval, _ := m.approvalForCall(ctx, rc.sess, call.ID)
			switch {
			case approval != nil && approval.Status == approvalRejected:
				note := strings.TrimSpace(approval.Note)
				if note == "" {
					note = "no reason given"
				}
				err = fmt.Errorf("the user rejected this action: %s", note)
			case approval != nil && approval.Status == approvalPending:
				m.pause(rc, approval)
				return out, true
			case approval == nil && needsApproval(rc.def, spec):
				approval = m.requestApproval(rc, call, args)
				if approval == nil {
					err = fmt.Errorf("could not request approval")
					break
				}
				// Earlier results of this turn are recorded, the pause resumes from here
				m.pause(rc, approval)
				return out, true
			case resumed && approval == nil && (spec.Write || spec.Commit) && m.callRecorded(ctx, rc.sess, call.ID):
				err = fmt.Errorf("this action was interrupted by a restart, verify the current state before repeating it")
			default:
				m.record(ctx, rc.sess, EventToolCall, "", call.Name, call.ID, map[string]any{"arguments": args})
				result, err = m.execTool(rc, spec, args)
			}
		}
		text, data := formatResult(result, err)
		m.debugToolCall(rc, call, args, text, err, time.Since(started))
		rc.sess.ToolCalls++
		_ = m.store.UpdateAgentSessionColumns(ctx, rc.sess.ID, map[string]any{"tool_calls": rc.sess.ToolCalls})
		m.record(ctx, rc.sess, EventToolResult, text, call.Name, call.ID, data)
		out = append(out, llm.Message{Role: llm.RoleTool, Content: text, ToolCallID: call.ID, ToolName: call.Name})
	}
	return out, false
}

func toolAllowed(rc *runContext, name string) bool {
	for _, t := range rc.def.ToolNames() {
		if t == name {
			if name == "run_command" && !rc.settings.GetAllowCommands() {
				return false
			}
			return true
		}
	}
	return false
}

// Runs one tool with panic safety and output capping
func (m *Manager) execTool(rc *runContext, spec *toolSpec, args map[string]any) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool crashed: %v", r)
		}
	}()
	return spec.Run(rc, args)
}

func formatResult(result string, err error) (string, map[string]any) {
	if err != nil {
		return "Error: " + err.Error(), map[string]any{"ok": false}
	}
	text := result
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}
	return text, map[string]any{"ok": true}
}

// Logs a call and its outcome when the instance asks
func (m *Manager) debugToolCall(rc *runContext, call llm.ToolCall, args map[string]any, text string, err error, took time.Duration) {
	if !m.Settings(rc.ctx, rc.repo).GetDebugToolCalls() {
		return
	}
	rawArgs, _ := json.Marshal(args)
	m.log.Info("agents: session %s #%d tool %s call %s args %s", rc.sess.ID, rc.sess.Number, call.Name, call.ID, rawArgs)
	if err != nil {
		m.log.Warn("agents: session %s #%d tool %s call %s failed after %s: %v", rc.sess.ID, rc.sess.Number, call.Name, call.ID, took.Round(time.Millisecond), err)
		return
	}
	m.log.Info("agents: session %s #%d tool %s call %s ok after %s: %s", rc.sess.ID, rc.sess.Number, call.Name, call.ID, took.Round(time.Millisecond), text)
}

// Whether a tool call already executed before an interruption
func (m *Manager) callRecorded(ctx context.Context, sess *db.AgentSession, callID string) bool {
	events, err := m.store.ListAgentSessionEvents(ctx, sess.ID, 0, 0)
	if err != nil {
		return false
	}
	for _, ev := range events {
		if ev.Kind == EventToolCall && ev.ToolCallID == callID {
			return true
		}
	}
	return false
}

// Approval row of a call, nil when none was ever requested
func (m *Manager) approvalForCall(ctx context.Context, sess *db.AgentSession, callID string) (*db.AgentApproval, error) {
	rows, err := m.store.ListAgentApprovals(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range rows {
		var args struct {
			CallID string `json:"call_id"`
		}
		_ = json.Unmarshal([]byte(a.Arguments), &args)
		if args.CallID == callID {
			return a, nil
		}
	}
	return nil, nil
}

// Creates the approval row and event for a gated call
func (m *Manager) requestApproval(rc *runContext, call llm.ToolCall, args map[string]any) *db.AgentApproval {
	payload, _ := json.Marshal(map[string]any{"call_id": call.ID, "tool": call.Name, "arguments": args})
	a := &db.AgentApproval{SessionID: rc.sess.ID, Tool: call.Name, Title: approvalTitle(call.Name, args), Arguments: string(payload), Status: approvalPending}
	if err := m.store.CreateAgentApproval(rc.ctx, a); err != nil {
		m.log.Error("agents: approval for %s: %v", rc.sess.ID, err)
		return nil
	}
	ev := m.record(rc.ctx, rc.sess, EventApproval, a.Title, call.Name, call.ID, map[string]any{"approval_id": a.ID, "status": approvalPending, "arguments": args})
	a.EventSeq = ev.Seq
	_ = m.store.UpdateAgentApproval(rc.ctx, a)
	return a
}

func approvalTitle(tool string, args map[string]any) string {
	switch tool {
	case "commit":
		return "Commit: " + firstLine(argString(args, "message"))
	case "open_pull_request":
		return "Open pull request: " + firstLine(argString(args, "title"))
	case "run_command":
		return "Run command: " + firstLine(argString(args, "command"))
	case "post_comment":
		return "Post comment: " + firstLine(argString(args, "body"))
	case "submit_review":
		return "Submit review: " + argString(args, "event")
	case "update_issue":
		return "Update issue"
	case "request_review":
		return "Request review from " + strings.Join(argStrings(args, "usernames"), ", ")
	}
	return "Run " + tool
}

// Parks the session until a human decides
func (m *Manager) pause(rc *runContext, a *db.AgentApproval) {
	sess := rc.sess
	sess.Status = StatusWaitingApproval
	_ = m.store.UpdateAgentSessionColumns(rc.ctx, sess.ID, map[string]any{"status": StatusWaitingApproval, "updated_at": time.Now().UTC()})
	m.record(rc.ctx, sess, EventStatus, "Waiting for approval: "+a.Title, "", "", map[string]any{"status": StatusWaitingApproval, "approval_id": a.ID})
	url := m.sessionURL(rc.ctx, rc.repo, sess.ID)
	var recipients []string
	if sess.TriggerUserID != "" {
		recipients = []string{sess.TriggerUserID}
	}
	m.forge.Notify(rc.ctx, forge.Notification{
		Repo: rc.repo, Issue: rc.issue, Kind: forge.NotifyAgentSession, Actor: rc.bot, Reason: "approval",
		Subject: fmt.Sprintf("[%s/%s] %s needs approval", rc.repo.Namespace, rc.repo.Name, rc.def.Name),
		Text:    fmt.Sprintf("Agent %s is waiting for approval of: %s", rc.def.Name, a.Title),
		URL:     url, Recipients: recipients, ExplicitOnly: true,
	})
	m.dispatchWebhook(rc.ctx, rc.repo, sess, "waiting_approval")
}

// ResolveApproval decides a gated call and resumes the session
func (m *Manager) ResolveApproval(ctx context.Context, sess *db.AgentSession, a *db.AgentApproval, approve bool, note string, user *auth.AuthenticatedUser) error {
	if a.Status != approvalPending {
		return fmt.Errorf("%w: this action was already decided", ErrConflict)
	}
	if sess.Status != StatusWaitingApproval {
		return fmt.Errorf("%w: the session is not waiting for approval", ErrConflict)
	}
	a.Status = approvalRejected
	if approve {
		a.Status = approvalApproved
	}
	a.Note, a.DecidedAt = strings.TrimSpace(note), nowPtr()
	if user != nil {
		a.DecidedBy = user.ID
	}
	if err := m.store.UpdateAgentApproval(ctx, a); err != nil {
		return err
	}
	username := ""
	if user != nil {
		username = user.Username
	}
	m.record(ctx, sess, EventApproval, a.Title, a.Tool, "", map[string]any{"approval_id": a.ID, "status": a.Status, "decided_by": username, "note": a.Note})
	sess.Status = StatusRunning
	_ = m.store.UpdateAgentSessionColumns(ctx, sess.ID, map[string]any{"status": StatusRunning, "updated_at": time.Now().UTC()})
	m.launch(sess)
	return nil
}

// Cancel stops a session in any live state
func (m *Manager) Cancel(ctx context.Context, sess *db.AgentSession, user *auth.AuthenticatedUser) error {
	if sess.Status == StatusCompleted {
		return fmt.Errorf("%w: session already finished", ErrConflict)
	}
	conclusion := ConclusionCancelled
	if pending, _ := m.store.GetPendingAgentApproval(ctx, sess.ID); pending != nil {
		pending.Status, pending.DecidedAt, pending.Note = approvalRejected, nowPtr(), "session cancelled"
		if user != nil {
			pending.DecidedBy = user.ID
		}
		_ = m.store.UpdateAgentApproval(ctx, pending)
		conclusion = ConclusionRejected
	}
	fresh, _ := m.store.GetAgentSessionByID(ctx, sess.ID)
	if fresh != nil {
		sess = fresh
	}
	if sess.Status == StatusCompleted {
		return nil
	}
	who := "system"
	if user != nil {
		who = user.Username
	}
	// The row completes before the goroutine is interrupted so it cannot finish twice
	m.finish(ctx, sess, nil, conclusion, "", "cancelled by "+who)
	m.release(sess.ID)
	return nil
}

// Continue appends an instruction to a finished session and queues it again
func (m *Manager) Continue(ctx context.Context, sess *db.AgentSession, prompt string, user *auth.AuthenticatedUser) error {
	if sess.Status != StatusCompleted {
		return fmt.Errorf("%w: the session is still active", ErrConflict)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("%w: an instruction is required", ErrInvalid)
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, sess.RepoID)
	if err != nil || repo == nil {
		return fmt.Errorf("%w: repository", ErrNotFound)
	}
	if !m.Enabled(ctx, repo) {
		return ErrDisabled
	}
	username := ""
	if user != nil {
		username = user.Username
	}
	m.record(ctx, sess, EventUser, prompt, "", "", map[string]any{"trigger": TriggerContinue, "user": username})
	cols := map[string]any{"status": StatusQueued, "conclusion": "", "error": "", "finished_at": nil, "updated_at": time.Now().UTC()}
	// A follow up is a fresh leg, it gets the agent's budget again on top of what was spent
	if def := definitionFromJSON(sess.Definition); def != nil {
		cfg := m.Settings(ctx, repo)
		sess.MaxTurns = 0
		if turns := effectiveTurns(def, cfg); turns > 0 {
			sess.MaxTurns = sess.Turns + turns
		}
		cols["max_turns"] = sess.MaxTurns
		sess.MaxTokens = 0
		if budget := effectiveTokens(def, cfg); budget > 0 {
			sess.MaxTokens = sess.InputTokens + sess.OutputTokens + budget
		}
		cols["max_tokens"] = sess.MaxTokens
	}
	if user != nil && !user.IsAnonymous() {
		cols["trigger_user_id"] = user.ID
		sess.TriggerUserID = user.ID
	}
	sess.Status, sess.Conclusion, sess.Error, sess.FinishedAt = StatusQueued, "", "", nil
	if err := m.store.UpdateAgentSessionColumns(ctx, sess.ID, cols); err != nil {
		return err
	}
	m.record(ctx, sess, EventStatus, "Queued", "", "", map[string]any{"status": StatusQueued})
	m.kick(sess.RepoID)
	return nil
}

// Rerun starts a fresh session with the same inputs
func (m *Manager) Rerun(ctx context.Context, repo *db.GitRepository, sess *db.AgentSession, user *auth.AuthenticatedUser) (*db.AgentSession, error) {
	def, err := m.FindAgentForRepo(ctx, repo, sess.AgentSlug)
	if err != nil {
		if snap := definitionFromJSON(sess.Definition); snap != nil {
			def = snap
		} else {
			return nil, err
		}
	}
	return m.StartSession(ctx, StartParams{Repo: repo, Def: def, Prompt: sess.Prompt, Ref: sess.Ref, IssueNumber: sess.IssueNumber, Model: sess.Model, Trigger: TriggerRerun, Actor: user})
}

// Completes the session, applying kind specific follow ups and notifying
func (m *Manager) finish(ctx context.Context, sess *db.AgentSession, rc *runContext, conclusion, summary, errText string) {
	ctx = context.WithoutCancel(ctx)
	// A cancel racing the loop finishes once, whoever wrote the row first wins
	if fresh, _ := m.store.GetAgentSessionByID(ctx, sess.ID); fresh != nil && fresh.Status == StatusCompleted && fresh.FinishedAt != nil && sess.FinishedAt == nil {
		return
	}
	if rc != nil && conclusion == ConclusionSuccess {
		m.wrapUp(rc, &summary)
	}
	if rc != nil && rc.issue != nil && sess.Trigger != TriggerManual && !rc.posted &&
		(conclusion == ConclusionFailure || conclusion == ConclusionTimedOut || conclusion == ConclusionBudgetExceeded) {
		body := fmt.Sprintf("I could not finish this task: %s\n\n[Session #%d](%s)", errText, sess.Number, m.sessionURL(ctx, rc.repo, sess.ID))
		if _, err := m.forge.CreateComment(ctx, rc.repo, rc.actor, rc.issue, body, false); err == nil {
			rc.posted = true
		}
	}
	now := time.Now().UTC()
	sess.Status, sess.Conclusion, sess.Summary, sess.Error, sess.FinishedAt = StatusCompleted, conclusion, summary, errText, &now
	cols := map[string]any{"status": StatusCompleted, "conclusion": conclusion, "summary": summary, "error": errText, "finished_at": now, "updated_at": now}
	if rc != nil {
		cols["result_pull_number"], cols["result_review_id"], cols["result_commits"], cols["branch"] = sess.ResultPullNumber, sess.ResultReviewID, sess.ResultCommits, sess.Branch
	}
	_ = m.store.UpdateAgentSessionColumns(ctx, sess.ID, cols)
	if errText != "" {
		m.record(ctx, sess, EventError, errText, "", "", map[string]any{"conclusion": conclusion})
	}
	if summary != "" {
		m.record(ctx, sess, EventResult, summary, "", "", map[string]any{"conclusion": conclusion, "pull_number": sess.ResultPullNumber, "review_id": sess.ResultReviewID})
	}
	m.record(ctx, sess, EventStatus, "Completed: "+conclusion, "", "", map[string]any{"status": StatusCompleted, "conclusion": conclusion})
	repo, _ := m.store.GetGitRepositoryByID(ctx, sess.RepoID)
	if repo == nil {
		return
	}
	m.dispatchWebhook(ctx, repo, sess, "completed")
	var recipients []string
	if sess.TriggerUserID != "" && !m.IsAgentUser(ctx, sess.TriggerUserID) {
		recipients = []string{sess.TriggerUserID}
	}
	var issue *db.GitIssue
	if sess.IssueID != nil {
		issue, _ = m.store.GetGitIssueByID(ctx, *sess.IssueID)
	}
	text := summary
	if errText != "" {
		text = errText
	}
	m.forge.Notify(ctx, forge.Notification{
		Repo: repo, Issue: issue, Kind: forge.NotifyAgentSession, Reason: "completed",
		Subject: fmt.Sprintf("[%s/%s] %s session #%d %s", repo.Namespace, repo.Name, sess.AgentName, sess.Number, conclusion),
		Text:    fmt.Sprintf("Agent %s finished session #%d with %s.\n\n%s", sess.AgentName, sess.Number, conclusion, text),
		URL:     m.sessionURL(ctx, repo, sess.ID), Recipients: recipients, ExplicitOnly: true,
	})
}

// Kind specific completion, publishing what the model left implicit
func (m *Manager) wrapUp(rc *runContext, summary *string) {
	ctx := context.WithoutCancel(rc.ctx)
	text := strings.TrimSpace(*summary)
	switch rc.def.Kind {
	case KindReview:
		if rc.pr != nil && !rc.reviewed && text != "" && rc.issue.State == "open" {
			if rv, err := m.forge.CreateReview(ctx, rc.repo, rc.actor, rc.issue, rc.pr, forge.ReviewCommented, text, rc.pr.HeadSHA, nil); err == nil {
				rc.reviewed = true
				rc.sess.ResultReviewID = rv.ID
			}
		}
	case KindChat:
		if rc.issue != nil && !rc.posted && text != "" {
			if _, err := m.forge.CreateComment(ctx, rc.repo, rc.actor, rc.issue, text, false); err == nil {
				rc.posted = true
			}
		}
	case KindCode:
		committed := len(rc.ws.commits) > 0
		if committed && rc.sess.ResultPullNumber == 0 && (rc.pr == nil || rc.ws.branch != rc.pr.HeadBranch) {
			title := rc.sess.Title
			if rc.issue != nil && !rc.issue.IsPull {
				title = rc.issue.Title
			}
			body := text
			if rc.issue != nil && !rc.issue.IsPull {
				body += fmt.Sprintf("\n\nCloses #%d", rc.issue.Number)
			}
			body += fmt.Sprintf("\n\n---\nOpened by agent @%s from session #%d.", rc.botUser.Username, rc.sess.Number)
			if issue, _, err := m.forge.CreatePull(ctx, rc.repo, rc.actor, forge.NewPull{Base: rc.ws.baseBranch, Head: rc.ws.branch, Title: title, Body: body}); err == nil {
				rc.sess.ResultPullNumber = issue.Number
				rc.prOpened = true
			}
		}
		if rc.issue != nil && !rc.posted && text != "" && (committed || rc.pr != nil) {
			body := text
			if rc.sess.ResultPullNumber > 0 && rc.sess.ResultPullNumber != rc.issue.Number {
				body += fmt.Sprintf("\n\nSee pull request #%d.", rc.sess.ResultPullNumber)
			}
			if _, err := m.forge.CreateComment(ctx, rc.repo, rc.actor, rc.issue, body, false); err == nil {
				rc.posted = true
			}
		}
	}
	if len(rc.ws.pendingPaths()) > 0 {
		*summary = text + fmt.Sprintf("\n\n(%d edited files were left uncommitted)", len(rc.ws.pendingPaths()))
	}
}

func (m *Manager) dispatchWebhook(ctx context.Context, repo *db.GitRepository, sess *db.AgentSession, action string) {
	payload := &webhook.AgentSessionPayload{
		ID: sess.ID, Number: sess.Number, Agent: sess.AgentSlug, AgentName: sess.AgentName, Kind: sess.AgentKind, Trigger: sess.Trigger,
		Status: sess.Status, Conclusion: sess.Conclusion, Branch: sess.Branch, Summary: sess.Summary, URL: m.sessionURL(ctx, repo, sess.ID),
	}
	if sess.IsPull {
		payload.PullNumber = sess.IssueNumber
	} else {
		payload.IssueNumber = sess.IssueNumber
	}
	if sess.ResultPullNumber > 0 {
		payload.PullNumber = sess.ResultPullNumber
	}
	m.git.DispatchGit(ctx, "git_agent_session", repo, webhook.GitPayload{
		Action: action, Sender: webhook.Sender{Username: m.usernameOf(ctx, sess.TriggerUserID)}, AgentSession: payload,
	})
}

// Last assistant text, for summaries of sessions cut short
func lastAssistantText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

// One completion with retries on transient failures and compaction on context overflow
func (m *Manager) complete(rc *runContext, msgs []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	ctx := rc.ctx
	working := msgs
	// Zero compacts only after the provider rejects the context
	if window := rc.provider.Row.ContextWindow; window > 0 && estimateChars(working) > window*approxCharsPerToken {
		working = compact(working, window*approxCharsPerToken)
	}
	var temp *float64
	if rc.def.Temperature != nil {
		temp = rc.def.Temperature
	}
	build := func(list []llm.Message) *llm.Request {
		req := &llm.Request{Model: rc.provider.Model, Tools: tools, MaxTokens: rc.provider.Row.MaxTokens, Temperature: temp}
		for _, msg := range list {
			if msg.Role == llm.RoleSystem {
				req.System = msg.Content
				continue
			}
			req.Messages = append(req.Messages, msg)
		}
		return req
	}
	var lastErr error
	compacted := false
	for attempt := 0; attempt < completionRetries; attempt++ {
		if err := m.limiter(rc.provider.Row.ID).wait(ctx, rc.provider.Row.RequestsPerMinute); err != nil {
			return nil, err
		}
		resp, err := rc.provider.Client.Complete(ctx, build(working))
		if err == nil {
			return resp, nil
		}
		lastErr = err
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, llm.ErrContextLength) && !compacted:
			working = compact(working, estimateChars(working)/2)
			compacted = true
			attempt--
			continue
		case errors.Is(err, llm.ErrRateLimited), errors.Is(err, llm.ErrUnavailable):
			m.record(ctx, rc.sess, EventStatus, fmt.Sprintf("Connector error, retrying: %v", err), "", "", map[string]any{"retry": attempt + 1})
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(2+attempt*4) * time.Second):
			}
			continue
		default:
			return nil, err
		}
	}
	return nil, lastErr
}

func estimateChars(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Arguments) + len(tc.Name)
		}
	}
	return n
}

// Trims old tool results, then old assistant text, then drops middle turns until under budget
func compact(msgs []llm.Message, budget int) []llm.Message {
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	toolIdx := []int{}
	for i, m := range out {
		if m.Role == llm.RoleTool {
			toolIdx = append(toolIdx, i)
		}
	}
	cut := len(toolIdx) - keepRecentToolResults
	for k := 0; k < cut && estimateChars(out) > budget; k++ {
		i := toolIdx[k]
		if len(out[i].Content) > 400 {
			out[i].Content = out[i].Content[:300] + "\n[earlier output trimmed to save context]"
		}
	}
	for i := 1; i < len(out)-2 && estimateChars(out) > budget; i++ {
		if out[i].Role == llm.RoleAssistant && len(out[i].Content) > 2000 {
			out[i].Content = out[i].Content[:1500] + "\n[trimmed]"
		}
	}
	// Keep the system prompt and the first user message, drop the oldest exchanges after them
	for estimateChars(out) > budget && len(out) > 6 {
		// Remove the message after the first user turn, keeping tool results attached to their call
		idx := 2
		if idx < len(out) && out[idx].Role == llm.RoleTool {
			idx++
		}
		if idx >= len(out)-2 {
			break
		}
		removed := out[idx]
		out = append(out[:idx], out[idx+1:]...)
		if removed.Role == llm.RoleAssistant && len(removed.ToolCalls) > 0 {
			for idx < len(out) && out[idx].Role == llm.RoleTool {
				out = append(out[:idx], out[idx+1:]...)
			}
		}
	}
	return out
}

// ── Background loops ─────────────────────────────────────────────────────

// Relaunches sessions interrupted by a restart and drains queues
func (m *Manager) resume(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	running, err := m.store.ListAgentSessionsByStatus(ctx, []string{StatusRunning}, 0)
	if err == nil {
		for _, sess := range running {
			m.record(ctx, sess, EventStatus, "Resumed after a server restart", "", "", map[string]any{"status": StatusRunning})
			m.launch(sess)
		}
	}
	queued, err := m.store.ListAgentSessionsByStatus(ctx, []string{StatusQueued}, 0)
	if err == nil {
		seen := map[string]bool{}
		for _, sess := range queued {
			if !seen[sess.RepoID] {
				seen[sess.RepoID] = true
				m.kick(sess.RepoID)
			}
		}
	}
}

// Fails sessions that outlived their time limit without a live goroutine
func (m *Manager) reaperLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		sessions, err := m.store.ListAgentSessionsByStatus(ctx, []string{StatusRunning}, 0)
		if err != nil {
			continue
		}
		for _, sess := range sessions {
			m.runMu.Lock()
			_, live := m.running[sess.ID]
			m.runMu.Unlock()
			if live || sess.StartedAt == nil {
				continue
			}
			repo, _ := m.store.GetGitRepositoryByID(ctx, sess.RepoID)
			if repo == nil {
				m.finish(ctx, sess, nil, ConclusionFailure, "", "repository no longer exists")
				continue
			}
			// Without a limit an orphan only gets relaunched
			minutes := m.Settings(ctx, repo).GetMaxSessionMinutes()
			if minutes > 0 && time.Since(*sess.StartedAt) > time.Duration(minutes)*time.Minute+5*time.Minute {
				m.finish(ctx, sess, nil, ConclusionTimedOut, "", "the session exceeded its time limit")
			} else {
				m.launch(sess)
			}
		}
	}
}

// Deletes finished sessions past the retention window of their repository
func (m *Manager) retentionLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Minute):
	}
	m.applyRetention(ctx)
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.applyRetention(ctx)
		}
	}
}

func (m *Manager) applyRetention(ctx context.Context) {
	repos, err := m.store.ListAllGitRepositories(ctx)
	if err != nil {
		return
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		days := m.Settings(ctx, repo).GetLogRetentionDays()
		if days <= 0 {
			continue
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -int(days))
		old, err := m.store.ListFinishedAgentSessionsBefore(ctx, repo.ID, cutoff, 200)
		if err != nil {
			continue
		}
		for _, sess := range old {
			if err := m.store.DeleteAgentSession(ctx, sess.ID); err != nil {
				m.log.Warn("agents: retention delete %s: %v", sess.ID, err)
			}
		}
	}
}

// Delete removes a finished session and its transcript
func (m *Manager) Delete(ctx context.Context, sess *db.AgentSession) error {
	if sess.Status != StatusCompleted {
		return fmt.Errorf("%w: cancel the session before deleting it", ErrConflict)
	}
	return m.store.DeleteAgentSession(ctx, sess.ID)
}
