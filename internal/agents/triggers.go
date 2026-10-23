package agents

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

var mentionPattern = regexp.MustCompile(`(?:^|[^\w/])@([a-z0-9][a-z0-9_.-]{0,39})`)

// Sessions a failing pull request may spawn before the agent gives up
const maxWorkflowFixSessions = 3

// Trigger reacts to forge events, starting sessions for agents whose triggers match
func (m *Manager) Trigger(ctx context.Context, repo *db.GitRepository, event, action, ref, sha string, actor *auth.AuthenticatedUser, payload map[string]any) {
	if repo == nil || !m.Enabled(ctx, repo) {
		return
	}
	defs, err := m.AgentsForRepo(ctx, repo)
	if err != nil || len(defs) == 0 {
		return
	}
	actorIsBot := actor != nil && m.IsAgentUser(ctx, actor.ID)
	go func() {
		ctx := context.WithoutCancel(ctx)
		for _, def := range defs {
			if !def.Enabled || def.Triggers == nil {
				continue
			}
			// Other bots only when opted in, the agent's own bot never
			if actorIsBot && (!def.Triggers.RespondToBots || m.isOwnBot(ctx, def, actor.ID)) {
				continue
			}
			m.triggerAgent(ctx, repo, def, event, action, actor, payload)
		}
	}()
}

// Whether a user id is this agent's own bot
func (m *Manager) isOwnBot(ctx context.Context, def *Definition, userID string) bool {
	ident, _ := m.store.GetAgentIdentityByUser(ctx, userID)
	return ident != nil && ident.Slug == def.Slug
}

func (m *Manager) triggerAgent(ctx context.Context, repo *db.GitRepository, def *Definition, event, action string, actor *auth.AuthenticatedUser, payload map[string]any) {
	t := def.Triggers
	number := payloadNumber(payload)
	switch event {
	case "pull_request":
		if number == 0 {
			return
		}
		pr, _ := payload["pull_request"].(map[string]any)
		draft, _ := pr["draft"].(bool)
		base, _ := pr["base"].(string)
		switch action {
		case "opened", "synchronize", "ready_for_review":
			want := (action == "opened" && t.PullRequestOpened) || (action == "synchronize" && t.PullRequestSynchronize) || (action == "ready_for_review" && t.PullRequestReadyForReview)
			if !want {
				return
			}
			if draft && !t.IncludeDrafts {
				return
			}
			if !branchAllowed(t.BaseBranches, base) {
				return
			}
			if !m.pathsAllowed(ctx, repo, t, number) {
				return
			}
			m.startReplacing(ctx, repo, def, TriggerPullRequest, actor, number, "")
		case "review_requested":
			if !t.ReviewRequested || !m.namesBot(ctx, def, payloadStrings(payload["requested_reviewer_ids"])) {
				return
			}
			if !branchAllowed(t.BaseBranches, base) {
				return
			}
			m.startReplacing(ctx, repo, def, TriggerPullRequest, actor, number, "")
		case "assigned":
			if !t.IssueAssigned || !m.namesBot(ctx, def, payloadStrings(payload["assignee_ids"])) {
				return
			}
			m.startIfIdle(ctx, repo, def, TriggerIssue, actor, number, "")
		case "labeled":
			if label := payloadLabel(payload); label != "" && containsFold(t.IssueLabels, label) {
				m.startIfIdle(ctx, repo, def, TriggerLabel, actor, number, "")
			}
		}
	case "issues":
		if number == 0 {
			return
		}
		switch action {
		case "assigned":
			if !t.IssueAssigned || !m.namesBot(ctx, def, payloadStrings(payload["assignee_ids"])) {
				return
			}
			m.startIfIdle(ctx, repo, def, TriggerIssue, actor, number, "")
		case "labeled":
			if label := payloadLabel(payload); label != "" && containsFold(t.IssueLabels, label) {
				m.startIfIdle(ctx, repo, def, TriggerLabel, actor, number, "")
			}
		}
	case "issue_comment", "pull_request_review_comment":
		if action != "created" || number == 0 || !t.Mention {
			return
		}
		comment, _ := payload["comment"].(map[string]any)
		body, _ := comment["body"].(string)
		if !m.mentioned(ctx, def, body) {
			return
		}
		prompt := strings.TrimSpace(stripMention(body, m.handles(ctx, def)))
		if path, _ := comment["path"].(string); path != "" {
			line := 0
			if f, ok := comment["line"].(float64); ok {
				line = int(f)
			} else if n, ok := comment["line"].(int); ok {
				line = n
			}
			prompt = fmt.Sprintf("Inline comment on %s line %d by @%s:\n%s", path, line, actorName(actor), prompt)
		} else {
			prompt = fmt.Sprintf("Comment by @%s:\n%s", actorName(actor), prompt)
		}
		if _, err := m.StartSession(ctx, StartParams{Repo: repo, Def: def, Prompt: prompt, IssueNumber: number, Trigger: TriggerMention, Actor: actor, System: true}); err != nil {
			m.log.Warn("agents: mention session for %s: %v", def.Slug, err)
		}
	case "pull_request_review":
		if action != "submitted" || number == 0 || !t.ReviewFeedback {
			return
		}
		review, _ := payload["review"].(map[string]any)
		state, _ := review["state"].(string)
		body, _ := review["body"].(string)
		if state != "changes_requested" && !(state == "commented" && strings.TrimSpace(body) != "") {
			return
		}
		if !m.ownsPull(ctx, repo, def, number) {
			return
		}
		prompt := fmt.Sprintf("@%s reviewed pull request #%d (%s).", actorName(actor), number, strings.ReplaceAll(state, "_", " "))
		if strings.TrimSpace(body) != "" {
			prompt += "\n\n" + strings.TrimSpace(body)
		}
		prompt += "\n\nRead the review comments with get_pull_request, address them with commits to this pull request, and reply with a summary."
		m.startIfIdle(ctx, repo, def, TriggerReview, actor, number, prompt)
	}
}

// OnRunCompleted fixes failing workflows on pull requests an agent opened
func (m *Manager) OnRunCompleted(ctx context.Context, repo *db.GitRepository, run *db.ActionsRun) {
	if run == nil || run.PullNumber == 0 || run.Conclusion != actions.ConclusionFailure || !m.Enabled(ctx, repo) {
		return
	}
	if strings.HasPrefix(run.WorkflowPath, "agent-session-") {
		return
	}
	issue, _ := m.store.GetGitIssue(ctx, repo.ID, run.PullNumber)
	if issue == nil || issue.State != "open" || !m.IsAgentUser(ctx, issue.AuthorID) {
		return
	}
	ident, _ := m.store.GetAgentIdentityByUser(ctx, issue.AuthorID)
	if ident == nil {
		return
	}
	def, err := m.FindAgentForRepo(ctx, repo, ident.Slug)
	if err != nil || !def.Enabled || def.Triggers == nil || !def.Triggers.WorkflowFailure {
		return
	}
	go func() {
		ctx := context.WithoutCancel(ctx)
		sessions, _, err := m.store.ListAgentSessions(ctx, stores.AgentSessionListOptions{RepoID: repo.ID, AgentID: def.ID, IssueNumber: issue.Number, Trigger: TriggerWorkflowRun})
		if err != nil || len(sessions) >= maxWorkflowFixSessions {
			return
		}
		prompt := fmt.Sprintf("Workflow %s run #%d failed on pull request #%d (run id %s). Read the failing job log with get_workflow_run_log, fix the cause with commits to this pull request, and summarize what you changed.", run.WorkflowName, run.Number, run.PullNumber, run.ID)
		var actor *auth.AuthenticatedUser
		if run.TriggerUserID != "" {
			if u, _ := m.store.GetUserByID(ctx, run.TriggerUserID); u != nil && u.AuthProvider != db.AuthProviderAgent {
				actor = &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Provider: u.AuthProvider}
			}
		}
		m.startIfIdle(ctx, repo, def, TriggerWorkflowRun, actor, issue.Number, prompt)
	}()
}

// Starts a session unless one is already live for the agent on the target
func (m *Manager) startIfIdle(ctx context.Context, repo *db.GitRepository, def *Definition, trigger string, actor *auth.AuthenticatedUser, number int64, prompt string) {
	live, _ := m.store.ListLiveAgentSessionsForIssue(ctx, repo.ID, def.ID, number)
	if len(live) > 0 {
		return
	}
	if _, err := m.StartSession(ctx, StartParams{Repo: repo, Def: def, Prompt: prompt, IssueNumber: number, Trigger: trigger, Actor: actor, System: true}); err != nil {
		m.log.Warn("agents: %s session for %s on #%d: %v", trigger, def.Slug, number, err)
	}
}

// Starts a session, cancelling a live one on the same target first
func (m *Manager) startReplacing(ctx context.Context, repo *db.GitRepository, def *Definition, trigger string, actor *auth.AuthenticatedUser, number int64, prompt string) {
	live, _ := m.store.ListLiveAgentSessionsForIssue(ctx, repo.ID, def.ID, number)
	for _, s := range live {
		if s.Trigger == TriggerPullRequest {
			_ = m.Cancel(ctx, s, nil)
		}
	}
	if _, err := m.StartSession(ctx, StartParams{Repo: repo, Def: def, Prompt: prompt, IssueNumber: number, Trigger: trigger, Actor: actor, System: true}); err != nil {
		m.log.Warn("agents: %s session for %s on #%d: %v", trigger, def.Slug, number, err)
	}
}

// Whether the pull request was opened by this agent
func (m *Manager) ownsPull(ctx context.Context, repo *db.GitRepository, def *Definition, number int64) bool {
	issue, _ := m.store.GetGitIssue(ctx, repo.ID, number)
	if issue == nil || !issue.IsPull {
		return false
	}
	return m.isOwnBot(ctx, def, issue.AuthorID)
}

// Whether any of the user ids is this agent's bot
func (m *Manager) namesBot(ctx context.Context, def *Definition, ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	ident, _ := m.store.GetAgentIdentity(ctx, def.Slug)
	if ident == nil {
		return false
	}
	for _, id := range ids {
		if id == ident.UserID {
			return true
		}
	}
	return false
}

// Handles that address this agent, the slug and its bot username
func (m *Manager) handles(ctx context.Context, def *Definition) []string {
	out := []string{strings.ToLower(def.Slug)}
	if u := m.BotUsername(ctx, def.Slug); u != "" && !strings.EqualFold(u, def.Slug) {
		out = append(out, strings.ToLower(u))
	}
	return out
}

func (m *Manager) mentioned(ctx context.Context, def *Definition, body string) bool {
	if body == "" {
		return false
	}
	handles := m.handles(ctx, def)
	for _, match := range mentionPattern.FindAllStringSubmatch(strings.ToLower(body), -1) {
		name := strings.TrimRight(match[1], ".")
		for _, h := range handles {
			if name == h {
				return true
			}
		}
	}
	return false
}

// Removes the agent's own mention so the prompt reads as an instruction
func stripMention(body string, handles []string) string {
	out := body
	// Longer handles first so a prefix handle cannot split a longer one
	sorted := append([]string(nil), handles...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, h := range sorted {
		re := regexp.MustCompile(`(?i)(^|[^\w/])@` + regexp.QuoteMeta(h) + `\b`)
		out = re.ReplaceAllString(out, "$1")
	}
	return strings.TrimSpace(out)
}

func actorName(actor *auth.AuthenticatedUser) string {
	if actor == nil {
		return "someone"
	}
	return actor.Username
}

func payloadNumber(payload map[string]any) int64 {
	for _, key := range []string{"pull_request", "issue"} {
		if obj, ok := payload[key].(map[string]any); ok {
			switch n := obj["number"].(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			}
		}
	}
	return 0
}

func payloadLabel(payload map[string]any) string {
	if l, ok := payload["label"].(map[string]any); ok {
		name, _ := l["name"].(string)
		return name
	}
	return ""
}

func payloadStrings(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, x := range list {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

// Glob filters over a branch name, empty matches everything
func branchAllowed(patterns []string, branch string) bool {
	if len(patterns) == 0 {
		return true
	}
	branch = strings.TrimPrefix(branch, "refs/heads/")
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if ok, _ := doublestar.Match(p, branch); ok || p == branch {
			return true
		}
	}
	return false
}

// Path filters need the changed files of the pull request
func (m *Manager) pathsAllowed(ctx context.Context, repo *db.GitRepository, t *v1.AgentTriggers, number int64) bool {
	if len(t.Paths) == 0 && len(t.PathsIgnore) == 0 {
		return true
	}
	issue, _ := m.store.GetGitIssue(ctx, repo.ID, number)
	if issue == nil {
		return true
	}
	pr, err := m.forge.Pull(ctx, issue)
	if err != nil {
		return true
	}
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return true
	}
	defer r.Close()
	head := plumbing.NewHash(pr.HeadSHA)
	base := plumbing.ZeroHash
	if pr.MergeBaseSHA != "" {
		base = plumbing.NewHash(pr.MergeBaseSHA)
	} else if tip := r.BranchHash(pr.BaseBranch); !tip.IsZero() {
		if mb, err := r.MergeBase(tip, head); err == nil {
			base = mb
		}
	}
	diff, err := r.Diff(ctx, base, head, gitrepo.DiffOptions{})
	if err != nil {
		return true
	}
	changed := make([]string, 0, len(diff.Files))
	for _, f := range diff.Files {
		if f.NewPath != "" {
			changed = append(changed, f.NewPath)
		}
		if f.OldPath != "" && f.OldPath != f.NewPath {
			changed = append(changed, f.OldPath)
		}
	}
	return pathsMatch(t.Paths, t.PathsIgnore, changed)
}

// Any changed path inside the include set and outside the ignore set
func pathsMatch(include, ignore, changed []string) bool {
	for _, p := range changed {
		if len(include) > 0 && !anyGlob(include, p) {
			continue
		}
		if len(ignore) > 0 && anyGlob(ignore, p) {
			continue
		}
		return true
	}
	return false
}

func anyGlob(patterns []string, p string) bool {
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if ok, _ := doublestar.Match(pat, p); ok {
			return true
		}
	}
	return false
}
