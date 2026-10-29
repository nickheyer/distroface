package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/actions/workflowpattern"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Commit message markers that suppress runs
var skipMarkers = []string{"[skip ci]", "[ci skip]", "[no ci]", "[skip actions]", "[actions skip]"}

// Everything a trigger knows about the event it carries
type triggerInput struct {
	Event         string
	Action        string
	Ref           string
	SHA           string
	Actor         *auth.AuthenticatedUser
	Payload       map[string]any
	Inputs        map[string]any
	PullNumber    int64
	NeedsApproval bool
	FromFork      bool
	OnlyPath      string
	Mirror        bool
	// Owning agent session, its job stays open for commands
	AgentSession *AgentRunInfo
	// Commit the workflow files were read from
	FileCommit plumbing.Hash
	// Paths a push touched, diffed only once a path filter asks
	changed      func() ([]string, bool)
	changedDone  bool
	changedList  []string
	changedKnown bool
}

// Changed paths and whether they are known, memoized per input
func (in *triggerInput) changedPaths() ([]string, bool) {
	if !in.changedDone {
		in.changedDone = true
		if in.changed != nil {
			in.changedList, in.changedKnown = in.changed()
		}
	}
	return in.changedList, in.changedKnown
}

// ── Push ─────────────────────────────────────────────────────────────────

func (m *Manager) onRefUpdate(ctx context.Context, ev gitrepo.RefEvent) {
	if ev.Wiki || ev.SkipCI || ev.Repo == nil {
		return
	}
	repo := ev.Repo
	if !m.Enabled(ctx, repo) {
		return
	}
	if ev.Mirror && !m.Settings(ctx, repo).GetRunOnMirror() {
		return
	}
	r, err := m.openRepo(repo)
	if err != nil {
		m.log.Error("actions: open %s/%s: %v", repo.Namespace, repo.Name, err)
		return
	}
	defer r.Close()

	for _, u := range ev.Updates {
		if u.New.IsZero() || (!u.Name.IsBranch() && !u.Name.IsTag()) {
			continue
		}
		head, err := r.Commit(u.New)
		if err != nil {
			continue
		}
		if hasSkipMarker(head.Message) {
			continue
		}
		in := &triggerInput{
			Event: "push", Ref: u.Name.String(), SHA: u.New.String(), Actor: ev.Actor,
			Payload: m.pushPayload(ctx, repo, r, u, head, ev.Actor), Mirror: ev.Mirror,
			changed: func() ([]string, bool) { return changedPaths(ctx, r, u.Old, u.New) },
		}
		m.startRuns(ctx, repo, r, u.New, in)
	}
}

func hasSkipMarker(message string) bool {
	lower := strings.ToLower(message)
	for _, mk := range skipMarkers {
		if strings.Contains(lower, mk) {
			return true
		}
	}
	return false
}

// Paths touched between two commits, unknown for new refs or huge diffs
func changedPaths(ctx context.Context, r *gitrepo.Repo, old, new plumbing.Hash) ([]string, bool) {
	if old.IsZero() || new.IsZero() {
		return nil, false
	}
	oc, err := r.Commit(old)
	if err != nil {
		return nil, false
	}
	nc, err := r.Commit(new)
	if err != nil {
		return nil, false
	}
	ot, err := oc.Tree()
	if err != nil {
		return nil, false
	}
	nt, err := nc.Tree()
	if err != nil {
		return nil, false
	}
	changes, err := object.DiffTreeWithOptions(ctx, ot, nt, &object.DiffTreeOptions{})
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, ch := range changes {
		for _, name := range []string{ch.From.Name, ch.To.Name} {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		if len(out) > 5000 {
			return nil, false
		}
	}
	return out, true
}

// GitHub style push payload for the event context
func (m *Manager) pushPayload(ctx context.Context, repo *db.GitRepository, r *gitrepo.Repo, u gitrepo.RefUpdate, head *object.Commit, actor *auth.AuthenticatedUser) map[string]any {
	commits, _, _ := r.CommitsBetween(ctx, u.Old, u.New, 20)
	list := make([]any, 0, len(commits))
	for _, c := range commits {
		list = append(list, commitPayload(m.git.HTMLURL(ctx, repo), c))
	}
	pusher := "system"
	if actor != nil {
		pusher = actor.Username
	}
	return map[string]any{
		"ref":         u.Name.String(),
		"before":      u.Old.String(),
		"after":       u.New.String(),
		"created":     u.Old.IsZero(),
		"deleted":     u.New.IsZero(),
		"forced":      u.Forced,
		"head_commit": commitPayload(m.git.HTMLURL(ctx, repo), head),
		"commits":     list,
		"pusher":      map[string]any{"name": pusher},
		"repository":  m.repoPayload(ctx, repo),
		"sender":      map[string]any{"login": pusher},
	}
}

func commitPayload(htmlURL string, c *object.Commit) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"id":        c.Hash.String(),
		"message":   c.Message,
		"url":       htmlURL + "/commit/" + c.Hash.String(),
		"timestamp": c.Committer.When.UTC().Format(time.RFC3339),
		"author":    map[string]any{"name": c.Author.Name, "email": c.Author.Email},
		"committer": map[string]any{"name": c.Committer.Name, "email": c.Committer.Email},
	}
}

func (m *Manager) repoPayload(ctx context.Context, repo *db.GitRepository) map[string]any {
	return map[string]any{
		"name":           repo.Name,
		"full_name":      repo.Namespace + "/" + repo.Name,
		"owner":          map[string]any{"login": repo.Namespace},
		"default_branch": repo.DefaultBranch,
		"private":        repo.IsPrivate,
		"clone_url":      m.git.HTTPCloneURL(ctx, repo, false),
		"html_url":       m.git.HTMLURL(ctx, repo),
	}
}

// ── Forge events ─────────────────────────────────────────────────────────

func (m *Manager) trigger(ctx context.Context, repo *db.GitRepository, event, action, ref, sha string, actor *auth.AuthenticatedUser, payload map[string]any) {
	if repo == nil || !m.Enabled(ctx, repo) {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if action != "" {
		payload["action"] = action
	}
	if _, ok := payload["repository"]; !ok {
		payload["repository"] = m.repoPayload(ctx, repo)
	}
	if actor != nil {
		if _, ok := payload["sender"]; !ok {
			payload["sender"] = map[string]any{"login": actor.Username}
		}
	}
	r, err := m.openRepo(repo)
	if err != nil {
		m.log.Error("actions: open %s/%s: %v", repo.Namespace, repo.Name, err)
		return
	}
	defer r.Close()

	in := &triggerInput{Event: event, Action: action, Ref: ref, SHA: sha, Actor: actor, Payload: payload}
	switch event {
	case "pull_request":
		in.PullNumber = pullNumber(payload)
		if pullFromFork(repo, payload) {
			// The author owns the head code, whoever caused the event
			in.NeedsApproval = !m.pullAuthorCanWrite(ctx, repo, in.PullNumber)
			in.FromFork = true
		}
		if in.Ref == "" {
			in.Ref = fmt.Sprintf("refs/pull/%d/head", in.PullNumber)
		}
		if in.SHA == "" {
			in.SHA = pullHeadSHA(r, payload, in.PullNumber)
		}
		head := plumbing.NewHash(in.SHA)
		in.changed = func() ([]string, bool) { return pullChangedPaths(ctx, r, pullBaseBranch(payload), head) }
	case "pull_request_target":
		// Base branch run, head only reaches it through the diff
		in.PullNumber = pullNumber(payload)
		base := pullBaseBranch(payload)
		if in.Ref == "" && base != "" {
			in.Ref = "refs/heads/" + base
		}
		if in.SHA == "" && in.Ref != "" {
			if h, err := r.Resolve(in.Ref); err == nil {
				in.SHA = h.String()
			}
		}
		head := plumbing.NewHash(pullHeadSHA(r, payload, in.PullNumber))
		in.changed = func() ([]string, bool) { return pullChangedPaths(ctx, r, base, head) }
	}
	if in.Ref == "" {
		in.Ref = "refs/heads/" + repo.DefaultBranch
	}
	// Non code events read workflows from the default branch
	fileRev := in.SHA
	if event != "push" && event != "pull_request" && event != "pull_request_target" {
		fileRev = repo.DefaultBranch
	}
	commit, err := r.Resolve(fileRev)
	if err != nil {
		if sha != "" {
			commit, err = r.Resolve(sha)
		}
		if err != nil {
			return
		}
	}
	if in.SHA == "" {
		in.SHA = commit.String()
	}
	if event == "pull_request" {
		if c, err := r.Commit(commit); err == nil && hasSkipMarker(c.Message) {
			return
		}
	}
	m.startRuns(ctx, repo, r, commit, in)
	// GitHub raises pull_request_target beside every pull_request, on the base
	if event == "pull_request" {
		target := make(map[string]any, len(payload))
		for k, v := range payload {
			target[k] = v
		}
		m.trigger(ctx, repo, "pull_request_target", action, "", "", actor, target)
	}
}

// Head commit from the payload, else from the pull ref
func pullHeadSHA(r *gitrepo.Repo, payload map[string]any, number int64) string {
	if pr, ok := payload["pull_request"].(map[string]any); ok {
		if sha, _ := pr["head_sha"].(string); sha != "" {
			return sha
		}
		if head, ok := pr["head"].(map[string]any); ok {
			if sha, _ := head["sha"].(string); sha != "" {
				return sha
			}
		}
	}
	if number > 0 {
		if h, err := r.Resolve(fmt.Sprintf("refs/pull/%d/head", number)); err == nil {
			return h.String()
		}
	}
	return ""
}

// Paths changed since the merge base with the base branch
func pullChangedPaths(ctx context.Context, r *gitrepo.Repo, baseBranch string, head plumbing.Hash) ([]string, bool) {
	if baseBranch == "" || head.IsZero() {
		return nil, false
	}
	base := r.BranchHash(baseBranch)
	if base.IsZero() {
		return nil, false
	}
	mergeBase, err := r.MergeBase(base, head)
	if err != nil || mergeBase.IsZero() {
		return nil, false
	}
	return changedPaths(ctx, r, mergeBase, head)
}

func (m *Manager) actorCanWrite(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser) bool {
	if actor == nil || actor.IsAnonymous() {
		return false
	}
	return m.git.Access().Level(ctx, actor, repo, db.GitUnitActions) >= gitrepo.LevelWrite
}

// Write access of the pull request author, who controls the head code
func (m *Manager) pullAuthorCanWrite(ctx context.Context, repo *db.GitRepository, number int64) bool {
	issue, err := m.store.GetGitIssue(ctx, repo.ID, number)
	if err != nil || issue == nil {
		return false
	}
	u, err := m.store.GetUserByID(ctx, issue.AuthorID)
	if err != nil || u == nil || !u.IsActive {
		return false
	}
	roles, _ := m.store.GetUserRoleNames(ctx, u.ID)
	return m.actorCanWrite(ctx, repo, &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: roles, Provider: u.AuthProvider})
}

func payloadNumber(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	}
	return 0
}

func pullNumber(payload map[string]any) int64 {
	if n := payloadNumber(payload["number"]); n > 0 {
		return n
	}
	if pr, ok := payload["pull_request"].(map[string]any); ok {
		if n := payloadNumber(pr["number"]); n > 0 {
			return n
		}
	}
	return 0
}

// Head repository differs from the base repository
func pullFromFork(repo *db.GitRepository, payload map[string]any) bool {
	if v, ok := payload["fork"].(bool); ok {
		return v
	}
	full := repo.Namespace + "/" + repo.Name
	pr, _ := payload["pull_request"].(map[string]any)
	if pr == nil {
		return false
	}
	if head, ok := pr["head"].(map[string]any); ok {
		if hr, ok := head["repo"].(map[string]any); ok {
			if fn, _ := hr["full_name"].(string); fn != "" {
				return fn != full
			}
		}
	}
	if hr, _ := pr["head_repo"].(string); hr != "" {
		return hr != full
	}
	if id, _ := pr["head_repo_id"].(string); id != "" {
		return id != repo.ID
	}
	return false
}

func pullBaseBranch(payload map[string]any) string {
	pr, _ := payload["pull_request"].(map[string]any)
	if pr == nil {
		return ""
	}
	if base, ok := pr["base"].(map[string]any); ok {
		if ref, _ := base["ref"].(string); ref != "" {
			return strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	if b, _ := pr["base"].(string); b != "" {
		return strings.TrimPrefix(b, "refs/heads/")
	}
	if b, _ := pr["base_branch"].(string); b != "" {
		return b
	}
	return ""
}

func pullHeadBranch(payload map[string]any) string {
	pr, _ := payload["pull_request"].(map[string]any)
	if pr == nil {
		return ""
	}
	if head, ok := pr["head"].(map[string]any); ok {
		if ref, _ := head["ref"].(string); ref != "" {
			return strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	if b, _ := pr["head"].(string); b != "" {
		return strings.TrimPrefix(b, "refs/heads/")
	}
	if b, _ := pr["head_branch"].(string); b != "" {
		return b
	}
	return ""
}

// ── Matching ─────────────────────────────────────────────────────────────

// Activity types GitHub assumes when a trigger lists none
var defaultEventTypes = map[string][]string{
	"pull_request":        {"opened", "synchronize", "reopened"},
	"pull_request_target": {"opened", "synchronize", "reopened"},
}

// Reports whether a workflow listens to the event described
func matchTrigger(f *WorkflowFile, in *triggerInput) bool {
	for _, evt := range f.eventsNamed(in.Event) {
		if matchEvent(evt, in) {
			return true
		}
	}
	return false
}

func matchEvent(evt *jobparser.Event, in *triggerInput) bool {
	switch in.Event {
	case "push":
		return matchPush(evt, in)
	case "pull_request", "pull_request_target":
		return matchPull(evt, in)
	case "workflow_dispatch", "schedule":
		return true
	}
	return matchTypes(evt, in)
}

// Activity type filters, every type when none are listed
func matchTypes(evt *jobparser.Event, in *triggerInput) bool {
	types := evt.Acts()["types"]
	if len(types) == 0 {
		types = defaultEventTypes[in.Event]
	}
	if len(types) == 0 || in.Action == "" {
		return true
	}
	return containsString(types, in.Action)
}

// Branch or tag names that pass include and ignore filters
func refAllowed(include, ignore []string, name string) bool {
	if len(include) > 0 {
		patterns, err := workflowpattern.CompilePatterns(include...)
		if err != nil || workflowpattern.Skip(patterns, []string{name}) {
			return false
		}
	}
	if len(ignore) > 0 {
		patterns, err := workflowpattern.CompilePatterns(ignore...)
		if err != nil || workflowpattern.Filter(patterns, []string{name}) {
			return false
		}
	}
	return true
}

// Path filters cost a diff, skipped when absent
func (in *triggerInput) pathsAllowed(include, ignore []string) bool {
	if len(include) == 0 && len(ignore) == 0 {
		return true
	}
	changed, known := in.changedPaths()
	// A diff that cannot be taken never silences a run
	if !known {
		return true
	}
	if len(include) > 0 {
		patterns, err := workflowpattern.CompilePatterns(include...)
		if err != nil || workflowpattern.Skip(patterns, changed) {
			return false
		}
	}
	if len(ignore) > 0 {
		patterns, err := workflowpattern.CompilePatterns(ignore...)
		if err != nil || workflowpattern.Filter(patterns, changed) {
			return false
		}
	}
	return true
}

func matchPush(evt *jobparser.Event, in *triggerInput) bool {
	acts := evt.Acts()
	ref := plumbing.ReferenceName(in.Ref)
	hasBranchFilter := len(acts["branches"]) > 0 || len(acts["branches-ignore"]) > 0
	hasTagFilter := len(acts["tags"]) > 0 || len(acts["tags-ignore"]) > 0
	switch {
	case ref.IsBranch():
		if hasTagFilter && !hasBranchFilter {
			return false
		}
		if !refAllowed(acts["branches"], acts["branches-ignore"], ref.Short()) {
			return false
		}
		return in.pathsAllowed(acts["paths"], acts["paths-ignore"])
	case ref.IsTag():
		if hasBranchFilter && !hasTagFilter {
			return false
		}
		// Tags carry no diff, path filters never apply to them
		return refAllowed(acts["tags"], acts["tags-ignore"], ref.Short())
	}
	return false
}

func matchPull(evt *jobparser.Event, in *triggerInput) bool {
	if !matchTypes(evt, in) {
		return false
	}
	acts := evt.Acts()
	if base := pullBaseBranch(in.Payload); base != "" && !refAllowed(acts["branches"], acts["branches-ignore"], base) {
		return false
	}
	return in.pathsAllowed(acts["paths"], acts["paths-ignore"])
}
