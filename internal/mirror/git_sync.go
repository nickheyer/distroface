package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	gittransport "github.com/go-git/go-git/v6/plumbing/transport"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

const (
	forgeGitHub = "github"
	forgeGitLab = "gitlab"
	forgeGitea  = "gitea"
)

// Upstream probes and ref listings give up after this long
const gitProbeTimeout = 20 * time.Second

// Refspecs mirroring every branch and tag
var mirrorRefspecs = []string{"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}

func gitKey(id string) string    { return "git:" + id }
func pushKey(id string) string   { return "git_push:" + id }
func importKey(id string) string { return "import:" + id }

// Parsed mirror upstream with the vendor it points at
type gitUpstream struct {
	URL   string
	Host  string
	Path  string
	Owner string
	Repo  string
	Forge string
	SSH   bool
}

// Accepts https, ssh, scp style, host/owner/repo, or owner/repo shorthand
func parseGitUpstream(raw string) (gitUpstream, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return gitUpstream{}, fmt.Errorf("%w: upstream is required", ErrInvalid)
	}
	var up gitUpstream
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return gitUpstream{}, fmt.Errorf("%w: upstream %q is not a valid url", ErrInvalid, raw)
		}
		switch u.Scheme {
		case "https", "http":
		case "ssh":
			up.SSH = true
		default:
			return gitUpstream{}, fmt.Errorf("%w: upstream scheme %q is not allowed, use https or ssh", ErrInvalid, u.Scheme)
		}
		up.URL = strings.TrimSuffix(s, "/")
		up.Host = strings.ToLower(u.Hostname())
		up.Path = cleanRepoPath(u.Path)
	case isSCPLike(s):
		userHost, p, _ := strings.Cut(s, ":")
		host := userHost
		if i := strings.LastIndex(userHost, "@"); i >= 0 {
			host = userHost[i+1:]
		}
		up.SSH = true
		up.Host = strings.ToLower(host)
		up.Path = cleanRepoPath(p)
		up.URL = "ssh://" + userHost + "/" + strings.TrimPrefix(p, "/")
	default:
		parts := strings.Split(strings.Trim(s, "/"), "/")
		host := "github.com"
		if len(parts) >= 3 && strings.Contains(parts[0], ".") {
			host, parts = parts[0], parts[1:]
		}
		if len(parts) < 2 {
			return gitUpstream{}, fmt.Errorf("%w: upstream must be a git url or owner/repo", ErrInvalid)
		}
		up.Host = strings.ToLower(host)
		up.Path = cleanRepoPath(strings.Join(parts, "/"))
		up.URL = "https://" + host + "/" + up.Path + ".git"
	}
	if up.Path == "" || !strings.Contains(up.Path, "/") {
		return gitUpstream{}, fmt.Errorf("%w: upstream %q does not name a repository", ErrInvalid, raw)
	}
	i := strings.LastIndex(up.Path, "/")
	up.Owner, up.Repo = up.Path[:i], up.Path[i+1:]
	if up.Owner == "" || up.Repo == "" {
		return gitUpstream{}, fmt.Errorf("%w: upstream %q does not name a repository", ErrInvalid, raw)
	}
	up.Forge = forgeForHost(up.Host)
	return up, nil
}

// Colon before any slash and no scheme marks scp form
func isSCPLike(s string) bool {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return false
	}
	slash := strings.Index(s, "/")
	return slash < 0 || colon < slash
}

func cleanRepoPath(p string) string {
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	return strings.Trim(p, "/")
}

// Guesses the vendor from the host name, empty when unknown
func forgeForHost(host string) string {
	h := strings.ToLower(host)
	switch {
	case h == "github.com" || strings.HasSuffix(h, ".github.com") || strings.HasPrefix(h, "github."):
		return forgeGitHub
	case h == "gitlab.com" || strings.Contains(h, "gitlab"):
		return forgeGitLab
	case h == "codeberg.org" || h == "gitea.com" || strings.Contains(h, "gitea") || strings.Contains(h, "forgejo") || strings.Contains(h, "codeberg"):
		return forgeGitea
	}
	return ""
}

// Https project url without a .git suffix
func (u gitUpstream) httpsBase() string {
	return "https://" + u.Host + "/" + u.Path
}

// Companion wiki repository beside the upstream
func (u gitUpstream) wikiURL() string {
	return strings.TrimSuffix(u.URL, ".git") + ".wiki.git"
}

func (u gitUpstream) lfsBatchURL() string {
	return u.httpsBase() + ".git/info/lfs/objects/batch"
}

// Rest api root for the vendor, empty when unknown
func (u gitUpstream) apiBase() string {
	switch u.Forge {
	case forgeGitHub:
		if u.Host == "github.com" {
			return githubAPI
		}
		return "https://" + u.Host + "/api/v3"
	case forgeGitLab:
		return "https://" + u.Host + "/api/v4"
	case forgeGitea:
		return "https://" + u.Host + "/api/v1"
	}
	return ""
}

// Upstream string the release drivers accept
func (u gitUpstream) releaseUpstream() string {
	if u.Forge == forgeGitHub && u.Host == "github.com" {
		return u.Path
	}
	return u.httpsBase()
}

// Container registry serving the upstream's packages
func (u gitUpstream) registryHost() string {
	switch u.Forge {
	case forgeGitHub:
		return "ghcr.io"
	case forgeGitLab:
		if u.Host == "gitlab.com" {
			return "registry.gitlab.com"
		}
		return "registry." + u.Host
	}
	return u.Host
}

// Sync bookkeeping plus the ssh identity for ssh upstreams
type GitMirrorState struct {
	SyncState
	// Sealed pem, never stored in the clear
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	SSHPublicKey  string `json:"ssh_public_key,omitempty"`
	HostKey       string `json:"host_key,omitempty"`
}

func parseGitMirrorState(raw string) GitMirrorState {
	var st GitMirrorState
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s GitMirrorState) Encode() string {
	if s == (GitMirrorState{}) {
		return ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// Basic auth pair for https upstreams following vendor conventions
func gitBasicAuth(forge, username, token string) (string, string) {
	if token == "" {
		return "", ""
	}
	if username != "" {
		return username, token
	}
	switch forge {
	case forgeGitHub:
		return "x-access-token", token
	case forgeGitLab:
		return "oauth2", token
	}
	return "token", token
}

func (m *Monitor) gitHTTPAuth(up gitUpstream, cfg *v1.MirrorConfig) gitrepo.RemoteAuth {
	user, pass := gitBasicAuth(up.Forge, cfg.GetUsername(), cfg.GetAuthToken())
	return gitrepo.RemoteAuth{HTTPClient: m.client, Username: user, Password: pass}
}

// Transport credentials, ssh pins the host key and never falls back to an agent
func (m *Monitor) gitAuth(up gitUpstream, cfg *v1.MirrorConfig, state *GitMirrorState) (gitrepo.RemoteAuth, error) {
	if !up.SSH {
		return m.gitHTTPAuth(up, cfg), nil
	}
	if state.SSHPrivateKey == "" {
		return gitrepo.RemoteAuth{}, fmt.Errorf("%w: upstream uses ssh, generate the repository ssh key and install it upstream first", ErrInvalid)
	}
	pem, err := m.openMirrorKey(state.SSHPrivateKey)
	if err != nil {
		return gitrepo.RemoteAuth{}, fmt.Errorf("%w: the stored ssh key cannot be decrypted, generate it again", ErrInvalid)
	}
	a := gitrepo.RemoteAuth{HostKey: state.HostKey, Dialer: m.dial, SSHKeyPEM: pem}
	a.OnHostKey = func(fp string) { state.HostKey = fp }
	return a, nil
}

// Decrypts the stored ssh key with the sealing key, then the jwt derived one it may predate
func (m *Monitor) openMirrorKey(sealed string) ([]byte, error) {
	if sealed == "" {
		return nil, nil
	}
	plain, err := m.sealer.Open(sealed)
	if err != nil && m.legacySealer != nil {
		if legacy, lerr := m.legacySealer.Open(sealed); lerr == nil {
			return []byte(legacy), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}

// Reads rate limit answers and empty upstreams into typed outcomes
func classifyGitErr(err error) error {
	if err == nil {
		return nil
	}
	var he *githttp.Err
	if errors.As(err, &he) {
		limited := he.Status == http.StatusTooManyRequests ||
			(he.Status == http.StatusForbidden && strings.Contains(strings.ToLower(he.Reason), "rate limit"))
		if limited {
			return &rateLimitedError{until: retryDeadline(http.Header{})}
		}
	}
	if errors.Is(err, gittransport.ErrEmptyRemoteRepository) {
		return &nothingMirroredError{reason: "upstream repository is empty"}
	}
	return err
}

// Turns a probe failure into a message for the form
func describeProbeError(up gitUpstream, err error) error {
	if err == nil {
		return nil
	}
	err = classifyGitErr(err)
	var nothing *nothingMirroredError
	if errors.As(err, &nothing) {
		return nil
	}
	if _, limited := RetryAfter(err); limited {
		return fmt.Errorf("%s rate limited this server, wait before validating again: %w", up.Host, err)
	}
	switch {
	case errors.Is(err, gitrepo.ErrInvalid):
		return fmt.Errorf("%w: %s %v", ErrInvalid, up.Host, strings.TrimPrefix(err.Error(), gitrepo.ErrInvalid.Error()+": "))
	case errors.Is(err, gittransport.ErrAuthenticationRequired):
		return fmt.Errorf("%w: %s requires credentials for %s (private repositories need a token)", ErrInvalid, up.Host, up.Path)
	case errors.Is(err, gittransport.ErrAuthorizationFailed):
		return fmt.Errorf("%w: %s rejected the credentials for %s", ErrInvalid, up.Host, up.Path)
	case errors.Is(err, gittransport.ErrRepositoryNotFound):
		return fmt.Errorf("%w: repository %s not found on %s (private repositories need a token)", ErrInvalid, up.Path, up.Host)
	}
	return fmt.Errorf("git validation failed: %w", err)
}

// Jobs for every due git mirror, import, and push mirror
func (m *Monitor) gitJobs(ctx context.Context) []func() {
	var jobs []func()
	pending := map[string]*db.GitImport{}
	imports, err := m.store.ListPendingGitImports(ctx)
	if err != nil {
		m.log.Error("mirror sweep import list: %v", err)
	}
	for _, imp := range imports {
		pending[imp.RepoID] = imp
	}

	repos, err := m.store.ListMirrorGitRepositories(ctx)
	if err != nil {
		m.log.Error("mirror sweep git repo list: %v", err)
	}
	for _, repo := range repos {
		// Imports own the fetch until they finish
		if _, importing := pending[repo.ID]; importing || repo.IsArchived {
			continue
		}
		cfg, err := m.Config(repo.MirrorConfig)
		if err != nil {
			m.log.Error("mirror config for git repo %s/%s: %v", repo.Namespace, repo.Name, err)
			continue
		}
		if !m.due(ctx, cfg, repo.MirrorLastSync, parseGitMirrorState(repo.MirrorState).SyncState) {
			continue
		}
		jobs = append(jobs, func() {
			key := gitKey(repo.ID)
			if !m.try(key) {
				return
			}
			defer m.done(key)
			m.execGitSync(ctx, repo, cfg)
		})
	}

	var importIDs []string
	for id := range pending {
		importIDs = append(importIDs, id)
	}
	pushMirrors, err := m.store.ListAllGitPushMirrors(ctx)
	if err != nil {
		m.log.Error("mirror sweep push mirror list: %v", err)
	}
	ids := append([]string{}, importIDs...)
	for _, pm := range pushMirrors {
		ids = append(ids, pm.RepoID)
	}
	owners := map[string]*db.GitRepository{}
	if len(ids) > 0 {
		if owners, err = m.store.GetGitRepositoriesByIDs(ctx, ids); err != nil {
			m.log.Error("mirror sweep git repo lookup: %v", err)
			owners = map[string]*db.GitRepository{}
		}
	}

	now := time.Now()
	for _, imp := range pending {
		repo := owners[imp.RepoID]
		if repo == nil || parseImportState(imp.State).CoolingDown(now) {
			continue
		}
		jobs = append(jobs, func() {
			key := importKey(repo.ID)
			if !m.try(key) {
				return
			}
			defer m.done(key)
			m.execImport(ctx, repo, imp)
		})
	}

	for _, pm := range pushMirrors {
		repo := owners[pm.RepoID]
		if repo == nil {
			continue
		}
		cfg := &v1.MirrorConfig{SyncIntervalMinutes: pm.IntervalMinutes}
		if !m.due(ctx, cfg, pm.LastSync, parsePushMirrorState(pm.State).SyncState) {
			continue
		}
		jobs = append(jobs, func() {
			key := pushKey(pm.ID)
			if !m.try(key) {
				return
			}
			defer m.done(key)
			m.execPushSync(ctx, repo, pm)
		})
	}
	return jobs
}

func (m *Monitor) execGitSync(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig) {
	key := gitKey(repo.ID)
	ev := Event{
		Kind:      "git",
		RepoID:    repo.ID,
		Namespace: repo.Namespace,
		Name:      repo.Name,
		Private:   repo.IsPrivate,
		OwnerID:   repo.OwnerID,
	}
	state := parseGitMirrorState(repo.MirrorState)
	runCtx, cancel := context.WithTimeout(ctx, currentLimits().SyncTimeout)
	m.armCancel(key, cancel)
	m.beginSync(key, ev)

	syncErr := m.syncGitRepo(runCtx, repo, cfg, &state)
	if syncErr == nil && cfg.GetMirrorReleases() {
		if up, err := parseGitUpstream(cfg.GetUpstream()); err == nil {
			syncErr = m.syncGitReleases(runCtx, repo, cfg, up)
		}
	}
	cancel()
	if m.disarmCancel(key) && syncErr != nil {
		syncErr = ErrSyncStopped
	}

	var msg string
	state.SyncState, msg = nextState(state.SyncState, syncErr)
	var nothing *nothingMirroredError
	if errors.Is(syncErr, ErrSyncStopped) {
		m.log.Info("mirror sync for git repo %s/%s stopped by user", repo.Namespace, repo.Name)
	} else if errors.As(syncErr, &nothing) {
		m.log.Warn("mirror sync for git repo %s/%s: %v", repo.Namespace, repo.Name, syncErr)
	} else if syncErr != nil {
		m.log.Error("mirror sync for git repo %s/%s: %v", repo.Namespace, repo.Name, syncErr)
	}
	m.writeGitMirrorStatus(statusCtx(ctx), repo, state, msg)
	m.endSync(key, ev, syncErr)
}

// Persists status without clobbering a key generated mid sync
func (m *Monitor) writeGitMirrorStatus(ctx context.Context, repo *db.GitRepository, state GitMirrorState, msg string) {
	if fresh, err := m.store.GetGitRepositoryByID(ctx, repo.ID); err == nil && fresh != nil {
		fs := parseGitMirrorState(fresh.MirrorState)
		if fs.SSHPrivateKey != "" && fs.SSHPrivateKey != state.SSHPrivateKey {
			state.SSHPrivateKey, state.SSHPublicKey, state.HostKey = fs.SSHPrivateKey, fs.SSHPublicKey, fs.HostKey
		}
	}
	encoded := state.Encode()
	if err := m.store.SetGitRepoMirrorStatus(ctx, repo.ID, time.Now().UTC(), msg, encoded); err != nil {
		m.log.Error("mirror status update for git repo %s: %v", repo.ID, err)
	}
	repo.MirrorState = encoded
}

// Fetches branches and tags, prunes what upstream or pattern dropped
func (m *Monitor) syncGitRepo(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig, state *GitMirrorState) error {
	up, err := parseGitUpstream(cfg.GetUpstream())
	if err != nil {
		return err
	}
	if _, err := gitrepo.ValidateUpstreamURL(up.URL); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	auth, err := m.gitAuth(up, cfg, state)
	if err != nil {
		return err
	}
	refspecs := mirrorRefspecs
	var wanted map[plumbing.ReferenceName]bool
	if pattern := cfg.GetPattern(); pattern != "" {
		refs, err := m.git.ListRemote(ctx, up.URL, auth, gitProbeTimeout)
		if err != nil {
			return classifyGitErr(err)
		}
		refspecs, wanted = patternRefspecs(refs, pattern)
		if len(refspecs) == 0 {
			return &nothingMirroredError{reason: fmt.Sprintf("no upstream branches or tags match pattern %q", pattern)}
		}
	}
	if err := m.git.FetchRemote(ctx, repo, false, up.URL, refspecs, auth); err != nil {
		return classifyGitErr(err)
	}
	if wanted != nil {
		m.pruneUnwanted(ctx, repo, wanted)
	}
	return nil
}

// Explicit refspecs for the branches and tags a glob keeps
func patternRefspecs(refs []*plumbing.Reference, pattern string) ([]string, map[plumbing.ReferenceName]bool) {
	var specs []string
	wanted := map[plumbing.ReferenceName]bool{}
	for _, ref := range refs {
		if ref == nil || ref.Type() != plumbing.HashReference {
			continue
		}
		name := ref.Name()
		if (!name.IsBranch() && !name.IsTag()) || strings.HasSuffix(name.String(), "^{}") {
			continue
		}
		if !matchesPattern(pattern, name.Short()) {
			continue
		}
		wanted[name] = true
		specs = append(specs, "+"+name.String()+":"+name.String())
	}
	return specs, wanted
}

// Deletes local branches and tags the pattern no longer keeps
func (m *Monitor) pruneUnwanted(ctx context.Context, repo *db.GitRepository, wanted map[plumbing.ReferenceName]bool) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	var stale []*plumbing.Reference
	if iter, err := r.Repo.References(); err == nil {
		_ = iter.ForEach(func(ref *plumbing.Reference) error {
			name := ref.Name()
			if ref.Type() != plumbing.HashReference || (!name.IsBranch() && !name.IsTag()) || wanted[name] {
				return nil
			}
			if name.IsBranch() && name.Short() == repo.DefaultBranch {
				return nil
			}
			stale = append(stale, ref)
			return nil
		})
	}
	r.Close()
	opts := gitrepo.HookOptions{Mirror: true, BypassPullRequest: true, BypassSignedCommits: true, BypassAllowedPushers: true}
	for _, ref := range stale {
		if err := m.git.UpdateRef(ctx, repo, nil, ref.Name(), ref.Hash(), plumbing.ZeroHash, opts); err != nil {
			m.log.Warn("mirror prune %s on %s/%s: %v", ref.Name().Short(), repo.Namespace, repo.Name, err)
		}
	}
}
