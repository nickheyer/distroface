package services

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-enry/go-enry/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/webhook"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Per request cache of usernames by email for commit signatures
type gitUserCache struct {
	store   *stores.Store
	byEmail map[string]string
	byID    map[string]*storage.User
}

func newGitUserCache(store *stores.Store) *gitUserCache {
	return &gitUserCache{store: store, byEmail: map[string]string{}, byID: map[string]*storage.User{}}
}

func (c *gitUserCache) usernameByEmail(ctx context.Context, email string) string {
	if email == "" {
		return ""
	}
	key := strings.ToLower(email)
	if u, ok := c.byEmail[key]; ok {
		return u
	}
	name := ""
	if u, err := c.store.GetUserByEmail(ctx, email); err == nil && u != nil {
		name = u.Username
	}
	c.byEmail[key] = name
	return name
}

// Loads many users in one query ahead of lookups
func (c *gitUserCache) prime(ctx context.Context, ids []string) {
	var missing []string
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := c.byID[id]; ok {
			continue
		}
		c.byID[id] = nil
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return
	}
	users, err := c.store.GetUsersByIDs(ctx, missing)
	if err != nil {
		for _, id := range missing {
			delete(c.byID, id)
		}
		return
	}
	for id, u := range users {
		c.byID[id] = u
	}
}

func (c *gitUserCache) user(ctx context.Context, id string) *storage.User {
	if id == "" {
		return nil
	}
	if u, ok := c.byID[id]; ok {
		return u
	}
	u, _ := c.store.GetUserByID(ctx, id)
	c.byID[id] = u
	return u
}

func (c *gitUserCache) username(ctx context.Context, id string) string {
	if u := c.user(ctx, id); u != nil {
		return u.Username
	}
	return ""
}

func gitVisibility(private bool) v1.Visibility {
	if private {
		return v1.Visibility_VISIBILITY_PRIVATE
	}
	return v1.Visibility_VISIBILITY_PUBLIC
}

func gitWatchModeProto(mode string) v1.WatchMode {
	switch mode {
	case "all":
		return v1.WatchMode_WATCH_MODE_ALL
	case "ignore":
		return v1.WatchMode_WATCH_MODE_IGNORE
	default:
		return v1.WatchMode_WATCH_MODE_PARTICIPATING
	}
}

func gitWatchModeString(mode v1.WatchMode) string {
	switch mode {
	case v1.WatchMode_WATCH_MODE_ALL:
		return "all"
	case v1.WatchMode_WATCH_MODE_IGNORE:
		return "ignore"
	default:
		return "participating"
	}
}

// Extra rows loaded once per response for a batch of repositories
type gitRepoExtras struct {
	topics   map[string][]string
	stars    map[string]int64
	starred  map[string]bool
	issues   map[string]int64
	pulls    map[string]int64
	releases map[string]int32
	parents  map[string]*storage.GitRepository
	imports  map[string]*storage.GitImport
}

func gitLoadExtras(ctx context.Context, store *stores.Store, user *auth.AuthenticatedUser, repos []*storage.GitRepository) *gitRepoExtras {
	ids := make([]string, 0, len(repos))
	var parentIDs []string
	for _, r := range repos {
		ids = append(ids, r.ID)
		if r.ForkOfID != nil {
			parentIDs = append(parentIDs, *r.ForkOfID)
		}
		if r.TemplateOfID != nil {
			parentIDs = append(parentIDs, *r.TemplateOfID)
		}
	}
	ex := &gitRepoExtras{}
	ex.topics, _ = store.GetGitTopics(ctx, ids)
	ex.stars, _ = store.GetGitStarCounts(ctx, ids)
	if user != nil && !user.IsAnonymous() {
		ex.starred, _ = store.GetGitStarredSet(ctx, user.ID, ids)
	}
	ex.issues, ex.pulls, _ = store.CountOpenGitIssues(ctx, ids)
	ex.releases, _ = store.CountGitReleases(ctx, ids)
	ex.imports, _ = store.GetGitImportsByRepoIDs(ctx, ids)
	if len(parentIDs) > 0 {
		ex.parents, _ = store.GetGitRepositoriesByIDs(ctx, parentIDs)
	}
	return ex
}

// Converts a repository row, detail adds the viewer levels, counts, and emptiness
func (s *GitService) gitRepoToProto(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, ex *gitRepoExtras, detail bool) *v1.GitRepository {
	if ex == nil {
		ex = gitLoadExtras(ctx, s.store, user, []*storage.GitRepository{repo})
	}
	out := &v1.GitRepository{
		Id:             repo.ID,
		Namespace:      repo.Namespace,
		Name:           repo.Name,
		FullName:       repo.Namespace + "/" + repo.Name,
		Description:    repo.Description,
		Website:        repo.Website,
		Visibility:     gitVisibility(repo.IsPrivate),
		OwnerId:        repo.OwnerID,
		IsOrgNamespace: repo.IsOrgNamespace,
		DefaultBranch:  repo.DefaultBranch,
		ObjectFormat:   repo.ObjectFormat,
		Type:           repo.Type,
		IsArchived:     repo.IsArchived,
		Units:          gitrepo.UnitsOf(repo),
		LfsEnabled:     repo.LFSEnabled,
		Merge:          gitrepo.MergeSettingsOf(repo),
		SizeBytes:      repo.SizeBytes,
		LfsSizeBytes:   repo.LFSSizeBytes,
		BranchCount:    repo.BranchCount,
		TagCount:       repo.TagCount,
		PullCount:      repo.PullCount,
		PushCount:      repo.PushCount,
		HttpCloneUrl:   s.manager.HTTPCloneURL(ctx, repo, false),
		SshCloneUrl:    s.manager.SSHCloneURL(ctx, repo, false),
		StarCount:      ex.stars[repo.ID],
		IsStarred:      ex.starred[repo.ID],
		Topics:         ex.topics[repo.ID],
		OpenIssueCount: ex.issues[repo.ID],
		OpenPullCount:  ex.pulls[repo.ID],
		ReleaseCount:   ex.releases[repo.ID],
		CreatedAt:      timestamppb.New(repo.CreatedAt),
		UpdatedAt:      timestamppb.New(repo.UpdatedAt),
	}
	if out.Topics == nil {
		out.Topics = []string{}
	}
	if repo.LastPush != nil {
		out.LastPushedAt = timestamppb.New(*repo.LastPush)
	}
	if repo.ForkOfID != nil {
		if p := ex.parents[*repo.ForkOfID]; p != nil {
			out.ForkOf = p.Namespace + "/" + p.Name
		}
	}
	if repo.TemplateOfID != nil {
		if p := ex.parents[*repo.TemplateOfID]; p != nil {
			out.TemplateOf = p.Namespace + "/" + p.Name
		}
	}
	if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
		out.Mirror = mirror.Redacted(repo.MirrorConfig)
		out.MirrorLastError = repo.MirrorLastError
		if repo.MirrorLastSync != nil {
			out.MirrorLastSync = timestamppb.New(*repo.MirrorLastSync)
		}
		if st := mirror.ParseState(repo.MirrorState); st.CoolingDown(time.Now()) {
			out.MirrorNextAttempt = timestamppb.New(st.CooldownUntil)
		}
		if s.mirrors != nil {
			out.MirrorSyncing = s.mirrors.IsSyncing("git:" + repo.ID)
		}
	}
	// Finished imports without notes leave no trace on the repository
	if imp := ex.imports[repo.ID]; imp != nil {
		notes := mirror.ImportNotes(imp)
		if mirror.ImportPending(imp) || len(notes) > 0 {
			out.ImportStatus = &v1.GitImportStatus{
				Upstream: redactUpstream(imp.Upstream), Phase: imp.Phase, Error: imp.LastError, Notes: notes, UpdatedAt: timestamppb.New(imp.UpdatedAt),
			}
			if s.mirrors != nil {
				out.ImportStatus.Running = s.mirrors.ImportRunning(repo.ID)
			}
		}
	}
	if detail {
		if s.mirrors != nil {
			out.MirrorSshPublicKey = s.mirrors.GitMirrorPublicKey(repo)
		}
		out.Viewer = s.manager.Access().Levels(ctx, user, repo).Proto()
		if user != nil && !user.IsAnonymous() {
			mode, _ := s.store.GetGitWatch(ctx, user.ID, repo.ID)
			out.WatchMode = gitWatchModeProto(mode)
		}
		if n, err := s.store.CountGitForks(ctx, repo.ID); err == nil {
			out.ForkCount = int32(n)
		}
		if r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false); err == nil {
			out.IsEmpty = r.IsEmpty()
			r.Close()
		}
	}
	return out
}

// Upstream url without any credentials typed into it
func redactUpstream(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func (s *GitService) gitReposToProto(ctx context.Context, user *auth.AuthenticatedUser, repos []*storage.GitRepository) []*v1.GitRepository {
	ex := gitLoadExtras(ctx, s.store, user, repos)
	out := make([]*v1.GitRepository, len(repos))
	for i, r := range repos {
		out[i] = s.gitRepoToProto(ctx, user, r, ex, false)
	}
	return out
}

// Commit conversion with usernames, verification, and combined status
type gitCommitEnricher struct {
	ctx      context.Context
	store    *stores.Store
	manager  *gitrepo.Manager
	repo     *storage.GitRepository
	r        *gitrepo.Repo
	users    *gitUserCache
	statuses map[string][]*storage.GitCommitStatus
}

func newGitCommitEnricher(ctx context.Context, store *stores.Store, manager *gitrepo.Manager, repo *storage.GitRepository, r *gitrepo.Repo) *gitCommitEnricher {
	return &gitCommitEnricher{ctx: ctx, store: store, manager: manager, repo: repo, r: r, users: newGitUserCache(store)}
}

// Preloads the latest statuses for a batch of commits
func (e *gitCommitEnricher) loadStatuses(commits []*object.Commit) {
	shas := make([]string, 0, len(commits))
	for _, c := range commits {
		shas = append(shas, c.Hash.String())
	}
	e.statuses, _ = e.store.LatestGitCommitStatusesBulk(e.ctx, e.repo.ID, shas)
}

func (e *gitCommitEnricher) commit(c *object.Commit, withStats, withVerification bool) *v1.GitCommit {
	out := gitrepo.CommitToProto(c)
	if out == nil {
		return nil
	}
	out.Author.Username = e.users.usernameByEmail(e.ctx, c.Author.Email)
	out.Committer.Username = e.users.usernameByEmail(e.ctx, c.Committer.Email)
	if withStats {
		out.Additions, out.Deletions, out.ChangedFiles = e.r.CommitStats(e.ctx, c)
	}
	if withVerification {
		out.Verification = e.manager.Signing().VerifyCommit(e.ctx, c).Proto()
	}
	if e.statuses != nil {
		out.Status = gitCombinedState(e.statuses[c.Hash.String()])
	}
	return out
}

func gitStatusStateProto(state string) v1.CommitStatusState {
	switch state {
	case "pending":
		return v1.CommitStatusState_COMMIT_STATUS_STATE_PENDING
	case "success":
		return v1.CommitStatusState_COMMIT_STATUS_STATE_SUCCESS
	case "failure":
		return v1.CommitStatusState_COMMIT_STATUS_STATE_FAILURE
	case "error":
		return v1.CommitStatusState_COMMIT_STATUS_STATE_ERROR
	}
	return v1.CommitStatusState_COMMIT_STATUS_STATE_UNSPECIFIED
}

func gitStatusStateString(state v1.CommitStatusState) string {
	switch state {
	case v1.CommitStatusState_COMMIT_STATUS_STATE_PENDING:
		return "pending"
	case v1.CommitStatusState_COMMIT_STATUS_STATE_SUCCESS:
		return "success"
	case v1.CommitStatusState_COMMIT_STATUS_STATE_FAILURE:
		return "failure"
	case v1.CommitStatusState_COMMIT_STATUS_STATE_ERROR:
		return "error"
	}
	return ""
}

// Folds the latest status per context into one state
func gitCombinedState(statuses []*storage.GitCommitStatus) v1.CommitStatusState {
	if len(statuses) == 0 {
		return v1.CommitStatusState_COMMIT_STATUS_STATE_UNSPECIFIED
	}
	state := v1.CommitStatusState_COMMIT_STATUS_STATE_SUCCESS
	for _, st := range statuses {
		switch st.State {
		case "failure", "error":
			return v1.CommitStatusState_COMMIT_STATUS_STATE_FAILURE
		case "pending":
			state = v1.CommitStatusState_COMMIT_STATUS_STATE_PENDING
		}
	}
	return state
}

func gitCommitStatusToProto(ctx context.Context, users *gitUserCache, st *storage.GitCommitStatus) *v1.GitCommitStatus {
	return &v1.GitCommitStatus{
		Id:          st.ID,
		Sha:         st.SHA,
		Context:     st.Context,
		State:       gitStatusStateProto(st.State),
		TargetUrl:   st.TargetURL,
		Description: st.Description,
		Creator:     users.username(ctx, st.CreatorID),
		CreatedAt:   timestamppb.New(st.CreatedAt),
	}
}

func gitBranchProtectionToProto(p *storage.GitBranchProtection) *v1.GitBranchProtection {
	return &v1.GitBranchProtection{
		Id:                     p.ID,
		Pattern:                p.Pattern,
		Priority:               int32(p.Priority),
		BlockForcePush:         p.BlockForcePush,
		BlockDeletion:          p.BlockDeletion,
		RequirePullRequest:     p.RequirePullRequest,
		RequiredApprovals:      int32(p.RequiredApprovals),
		DismissStaleApprovals:  p.DismissStaleApprovals,
		RequireCodeOwnerReview: p.RequireCodeOwnerReview,
		RequireStatusChecks:    p.RequireStatusChecks,
		StatusCheckContexts:    gitrepo.DecodeIDs(p.StatusCheckContexts),
		RequireSignedCommits:   p.RequireSignedCommits,
		RequireLinearHistory:   p.RequireLinearHistory,
		RequireUpToDate:        p.RequireUpToDate,
		AllowedPushers:         gitrepo.DecodeIDs(p.AllowedPushers),
		AllowedMergers:         gitrepo.DecodeIDs(p.AllowedMergers),
		BypassIds:              gitrepo.DecodeIDs(p.BypassIDs),
	}
}

func gitTagProtectionToProto(p *storage.GitTagProtection) *v1.GitTagProtection {
	return &v1.GitTagProtection{
		Id:             p.ID,
		Pattern:        p.Pattern,
		BlockDeletion:  p.BlockDeletion,
		AllowedPushers: gitrepo.DecodeIDs(p.AllowedPushers),
	}
}

func gitCollaboratorToProto(ctx context.Context, users *gitUserCache, c *storage.GitCollaborator) *v1.GitCollaborator {
	out := &v1.GitCollaborator{
		UserId:     c.UserID,
		Permission: gitrepo.LevelFromString(c.Permission).Proto(),
		Accepted:   c.Accepted,
		CreatedAt:  timestamppb.New(c.CreatedAt),
	}
	if u := users.user(ctx, c.UserID); u != nil {
		out.Username = u.Username
		out.DisplayName = u.DisplayName
	}
	return out
}

func gitTransferToProto(t *storage.GitTransfer, repo *storage.GitRepository) *v1.GitTransfer {
	out := &v1.GitTransfer{
		Id:          t.ID,
		RepoId:      t.RepoID,
		ToNamespace: t.ToNamespace,
		RequestedBy: t.RequestedBy,
		ExpiresAt:   timestamppb.New(t.ExpiresAt),
		CreatedAt:   timestamppb.New(t.CreatedAt),
	}
	if repo != nil {
		out.FullName = repo.Namespace + "/" + repo.Name
	}
	return out
}

func (s *GitService) gitPushMirrorToProto(pm *storage.GitPushMirror) *v1.GitPushMirror {
	out := &v1.GitPushMirror{
		Id:              pm.ID,
		RemoteUrl:       pm.RemoteURL,
		HasToken:        pm.AuthToken != "",
		UseSshKey:       pm.UseSSHKey,
		IntervalMinutes: pm.IntervalMinutes,
		SyncOnPush:      pm.SyncOnPush,
		LastError:       pm.LastError,
	}
	if pm.LastSync != nil {
		out.LastSync = timestamppb.New(*pm.LastSync)
	}
	if s.mirrors != nil {
		out.Syncing = s.mirrors.IsSyncing("git_push:" + pm.ID)
	}
	return out
}

func gitBlobToProto(b *gitrepo.Blob, rawURL string, lfsPresent bool, lfsURL string) *v1.GitBlob {
	out := &v1.GitBlob{
		Sha:         b.Hash.String(),
		Path:        b.Path,
		Name:        b.Name,
		Mode:        fmt.Sprintf("%06o", uint32(b.Mode)),
		Size:        b.Size,
		IsBinary:    b.IsBinary,
		Truncated:   b.Truncated,
		Content:     b.Content,
		ContentType: b.ContentType,
		IsLfs:       b.LFS != nil,
		RawUrl:      rawURL,
	}
	if b.LFS != nil {
		out.Lfs = &v1.GitLFSPointer{Oid: b.LFS.OID, Size: b.LFS.Size, Present: lfsPresent, DownloadUrl: lfsURL}
	}
	if !b.IsBinary && len(b.Content) > 0 {
		out.Language = enry.GetLanguage(b.Name, b.Content)
	} else {
		out.Language, _ = enry.GetLanguageByExtension(b.Name)
	}
	return out
}

// Natural ordering for tag names so v1.10 sorts after v1.9
func gitNaturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ca, cb := a[ai], b[bi]
		if isDigit(ca) && isDigit(cb) {
			aj, bj := ai, bi
			for aj < len(a) && isDigit(a[aj]) {
				aj++
			}
			for bj < len(b) && isDigit(b[bj]) {
				bj++
			}
			na := strings.TrimLeft(a[ai:aj], "0")
			nb := strings.TrimLeft(b[bi:bj], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			ai, bi = aj, bj
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Sorts map keys with natural ordering, newest looking first
func gitSortedTagNames(tags map[string]plumbing.Hash) []string {
	names := make([]string, 0, len(tags))
	for n := range tags {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return gitNaturalLess(names[j], names[i]) })
	return names
}

func gitSortedBranchNames(branches map[string]plumbing.Hash) []string {
	names := make([]string, 0, len(branches))
	for n := range branches {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Repository lifecycle webhook body for an action by a user
func gitRepoEventPayload(user *auth.AuthenticatedUser, action string) webhook.GitPayload {
	p := webhook.GitPayload{Action: action, Sender: webhook.Sender{Username: "system"}}
	if user != nil {
		p.Sender.Username = user.Username
	}
	return p
}

// Enry color for a language, empty when unknown
func gitLanguageColor(name string) string {
	return enry.GetColor(name)
}
