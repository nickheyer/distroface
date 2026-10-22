package actions

import (
	"context"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/expr"
)

// Commit message markers that suppress runs
var skipMarkers = []string{"[skip ci]", "[ci skip]", "[no ci]", "[skip actions]", "[actions skip]"}

// WorkflowFile is one file under a workflows directory
type WorkflowFile struct {
	Path     string
	Source   string
	Workflow *Workflow
	Err      error
}

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

// Path filters cost a tree diff, skip it when the workflow has none
func (in *triggerInput) pathsAllowed(include, ignore []string) bool {
	if len(include) == 0 && len(ignore) == 0 {
		return true
	}
	changed, known := in.changedPaths()
	return pathsAllowed(include, ignore, changed, known)
}

func (m *Manager) openRepo(repo *db.GitRepository) (*gitrepo.Repo, error) {
	return m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
}

// LoadWorkflows reads and parses every workflow file at a revision
func (m *Manager) LoadWorkflows(ctx context.Context, repo *db.GitRepository, rev string) ([]*WorkflowFile, plumbing.Hash, error) {
	r, err := m.openRepo(repo)
	if err != nil {
		return nil, plumbing.ZeroHash, err
	}
	defer r.Close()
	if rev == "" {
		rev = repo.DefaultBranch
	}
	commit, err := r.Resolve(rev)
	if err != nil {
		if errors.Is(err, gitrepo.ErrEmptyRepo) || errors.Is(err, gitrepo.ErrNotFound) {
			return nil, plumbing.ZeroHash, nil
		}
		return nil, plumbing.ZeroHash, err
	}
	return loadWorkflowFiles(ctx, r, commit), commit, nil
}

// Reads the first workflows directory that holds yaml files
func loadWorkflowFiles(ctx context.Context, r *gitrepo.Repo, commit plumbing.Hash) []*WorkflowFile {
	for _, dir := range WorkflowDirs {
		entries, _, err := r.Tree(ctx, commit, dir, false)
		if err != nil {
			continue
		}
		var out []*WorkflowFile
		for _, e := range entries {
			if !e.Mode.IsFile() || !IsWorkflowFile(e.Name) {
				continue
			}
			p := pathpkg.Join(dir, e.Name)
			wf := &WorkflowFile{Path: p}
			blob, err := r.Blob(commit, p, workflowFileCap)
			if err != nil {
				wf.Err = err
			} else if blob.Truncated {
				wf.Err = fmt.Errorf("workflow file exceeds %d bytes", workflowFileCap)
			} else {
				wf.Source = string(blob.Content)
				wf.Workflow, wf.Err = ParseWorkflow(blob.Content)
			}
			out = append(out, wf)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// Location token of a workflow file, repository plus path at a commit
func callLocation(repo *db.GitRepository, path string, commit plumbing.Hash) string {
	return repo.Namespace + "/" + repo.Name + "/" + path + "@" + commit.String()
}

// Repository full name and commit of a location token
func splitLocation(loc string) (full, rev string) {
	at := strings.LastIndex(loc, "@")
	if at < 0 {
		return "", ""
	}
	rev = loc[at+1:]
	parts := strings.SplitN(loc[:at], "/", 3)
	if len(parts) < 2 {
		return "", rev
	}
	return parts[0] + "/" + parts[1], rev
}

// Splits a uses reference into repository, revision, and file path
func parseCallRef(ref, from, root, rootRev string) (full, rev, path string, err error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "./") {
		full, rev = root, rootRev
		if from != "" {
			if f, r := splitLocation(from); f != "" {
				full, rev = f, r
			}
		}
		path = strings.TrimPrefix(ref, "./")
	} else {
		at := strings.LastIndex(ref, "@")
		if at <= 0 {
			return "", "", "", fmt.Errorf("%q must be ./path or owner/repo/path@ref", ref)
		}
		rev = ref[at+1:]
		parts := strings.SplitN(ref[:at], "/", 3)
		if len(parts) < 3 {
			return "", "", "", fmt.Errorf("%q must be ./path or owner/repo/path@ref", ref)
		}
		full, path = parts[0]+"/"+parts[1], parts[2]
	}
	path = pathpkg.Clean(path)
	if rev == "" || path == "." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") || !IsWorkflowFile(path) {
		return "", "", "", fmt.Errorf("%q does not name a workflow file", ref)
	}
	return full, rev, path, nil
}

// Whether the run actor may read workflow files of another repository
func (m *Manager) canReadWorkflows(ctx context.Context, actor *auth.AuthenticatedUser, called *db.GitRepository) bool {
	if actor != nil && !actor.IsAnonymous() {
		return m.git.Access().Level(ctx, actor, called, db.GitUnitCode) >= gitrepo.LevelRead
	}
	return !called.IsPrivate
}

// Loader for reusable workflow references, instance repositories only
func (m *Manager) callLoader(ctx context.Context, repo *db.GitRepository, commit plumbing.Hash, actor *auth.AuthenticatedUser) (callLoader, func()) {
	type opened struct {
		repo *db.GitRepository
		r    *gitrepo.Repo
	}
	root := repo.Namespace + "/" + repo.Name
	repos := map[string]*opened{}
	parsed := map[string]*Workflow{}
	open := func(full string) (*opened, error) {
		if o, ok := repos[full]; ok {
			return o, nil
		}
		target := repo
		if full != root {
			ns, name, _ := strings.Cut(full, "/")
			called, err := m.store.GetGitRepository(ctx, ns, name)
			if err != nil {
				return nil, err
			}
			if called == nil {
				return nil, fmt.Errorf("repository %s is not on this instance, workflows on github.com cannot be called", full)
			}
			if !m.canReadWorkflows(ctx, actor, called) {
				return nil, fmt.Errorf("repository %s is not readable by the run actor", full)
			}
			target = called
		}
		r, err := m.openRepo(target)
		if err != nil {
			return nil, err
		}
		o := &opened{repo: target, r: r}
		repos[full] = o
		return o, nil
	}
	load := func(ref, from string) (*Workflow, string, error) {
		full, rev, path, err := parseCallRef(ref, from, root, commit.String())
		if err != nil {
			return nil, "", err
		}
		o, err := open(full)
		if err != nil {
			return nil, "", err
		}
		h, err := o.r.Resolve(rev)
		if err != nil {
			return nil, "", fmt.Errorf("%s not found in %s", rev, full)
		}
		loc := full + "/" + path + "@" + h.String()
		if wf, ok := parsed[loc]; ok {
			return wf, loc, nil
		}
		blob, err := o.r.Blob(h, path, workflowFileCap)
		if err != nil {
			return nil, "", fmt.Errorf("%s not found in %s at %s", path, full, rev)
		}
		if blob.Truncated {
			return nil, "", fmt.Errorf("%s exceeds %d bytes", path, workflowFileCap)
		}
		wf, err := ParseWorkflow(blob.Content)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", path, err)
		}
		parsed[loc] = wf
		return wf, loc, nil
	}
	closeAll := func() {
		for _, o := range repos {
			o.r.Close()
		}
	}
	return load, closeAll
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
	case "pull_request", "pull_request_target":
		in.PullNumber = pullNumber(payload)
		if pullFromFork(repo, payload) {
			// The author owns the head code, whoever caused the event
			in.NeedsApproval = !m.pullAuthorCanWrite(ctx, repo, in.PullNumber)
			in.FromFork = event == "pull_request"
		}
		if in.Ref == "" {
			in.Ref = fmt.Sprintf("refs/pull/%d/head", in.PullNumber)
		}
	}
	if in.Ref == "" {
		in.Ref = "refs/heads/" + repo.DefaultBranch
	}
	// Non code events read workflows from the default branch
	fileRev := in.SHA
	if event != "push" && event != "pull_request" {
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

func pullNumber(payload map[string]any) int64 {
	if n := expr.ToNumber(payload["number"]); n > 0 {
		return int64(n)
	}
	if pr, ok := payload["pull_request"].(map[string]any); ok {
		if n := expr.ToNumber(pr["number"]); n > 0 {
			return int64(n)
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

// ── Matching ─────────────────────────────────────────────────────────────

// Reports whether a workflow listens to the event described
func matchTrigger(wf *Workflow, in *triggerInput) bool {
	spec, ok := wf.On[in.Event]
	if !ok {
		return false
	}
	switch in.Event {
	case "push":
		return matchPush(spec, in)
	case "pull_request", "pull_request_target":
		return matchPull(spec, in)
	case "workflow_dispatch", "schedule":
		return true
	}
	types := spec.Types
	if len(types) == 0 {
		types = defaultEventTypes[in.Event]
	}
	if len(types) == 0 || in.Action == "" {
		return true
	}
	return containsString(types, in.Action)
}

func matchPush(spec *TriggerSpec, in *triggerInput) bool {
	ref := plumbing.ReferenceName(in.Ref)
	hasBranchFilter := len(spec.Branches) > 0 || len(spec.BranchesIgnore) > 0
	hasTagFilter := len(spec.Tags) > 0 || len(spec.TagsIgnore) > 0
	switch {
	case ref.IsBranch():
		if hasTagFilter && !hasBranchFilter {
			return false
		}
		if !refAllowed(spec.Branches, spec.BranchesIgnore, ref.Short()) {
			return false
		}
	case ref.IsTag():
		if hasBranchFilter && !hasTagFilter {
			return false
		}
		if !refAllowed(spec.Tags, spec.TagsIgnore, ref.Short()) {
			return false
		}
	default:
		return false
	}
	return in.pathsAllowed(spec.Paths, spec.PathsIgnore)
}

func matchPull(spec *TriggerSpec, in *triggerInput) bool {
	types := spec.Types
	if len(types) == 0 {
		types = defaultEventTypes["pull_request"]
	}
	if in.Action != "" && !containsString(types, in.Action) {
		return false
	}
	base := pullBaseBranch(in.Payload)
	if base != "" && !refAllowed(spec.Branches, spec.BranchesIgnore, base) {
		return false
	}
	return in.pathsAllowed(spec.Paths, spec.PathsIgnore)
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
