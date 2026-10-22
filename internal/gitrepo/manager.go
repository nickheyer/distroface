package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/protocol/packp"
	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Caller errors mapped to InvalidArgument
var ErrInvalid = errors.New("invalid argument")

// Returned when an input passes a configured or built in cap
var ErrTooLarge = errors.New("too large")

// Returned when a write targets an archived repository
var ErrArchived = errors.New("repository is archived")

// Returned when a write targets a mirror
var ErrMirror = errors.New("repository is a mirror")

// Writes wait while an import still fills the repository
var ErrImporting = errors.New("repository import is in progress")

// Deletes and moves packages linked to a source repository
type PackageManager interface {
	CanManageImageRepo(ctx context.Context, user *auth.AuthenticatedUser, repo *db.Repository) bool
	CanManageArtifactRepo(ctx context.Context, user *auth.AuthenticatedUser, repo *db.ArtifactRepository) bool
	DeleteImageRepo(ctx context.Context, repo *db.Repository) error
	DeleteArtifactRepo(ctx context.Context, repo *db.ArtifactRepository) error
	MoveImageRepo(ctx context.Context, repo *db.Repository, newNamespace, newName, ownerID string, isOrg bool) error
	MoveArtifactRepo(ctx context.Context, repo *db.ArtifactRepository, newNamespace, newName, ownerID string) error
}

// Manager is the business logic shared by rpc services, transports, and hooks
type Manager struct {
	store      *stores.Store
	engine     *Engine
	blobs      *artifacts.BlobStore
	res        *settings.Resolver
	dispatcher *webhook.Dispatcher
	recorder   *audit.Recorder
	log        *logger.Logger
	access     *Access
	signing    *Signing
	packages   PackageManager

	subsMu sync.RWMutex
	subs   []RefSubscriber
	// Per repository maintenance mutexes
	maintenance sync.Map
}

func NewManager(store *stores.Store, engine *Engine, blobs *artifacts.BlobStore, res *settings.Resolver, access *Access, dispatcher *webhook.Dispatcher, recorder *audit.Recorder, log *logger.Logger) *Manager {
	m := &Manager{
		store: store, engine: engine, blobs: blobs, res: res, access: access,
		dispatcher: dispatcher, recorder: recorder, log: log,
		signing: NewSigning(store, engine.KeysDir()),
	}
	engine.SetTransferLimit(func() int {
		return int(res.System(context.Background()).GetGit().GetMaxConcurrentTransfers())
	})
	return m
}

func (m *Manager) Store() *stores.Store         { return m.store }
func (m *Manager) Engine() *Engine              { return m.engine }
func (m *Manager) Access() *Access              { return m.access }
func (m *Manager) Signing() *Signing            { return m.signing }
func (m *Manager) Blobs() *artifacts.BlobStore  { return m.blobs }
func (m *Manager) Log() *logger.Logger          { return m.log }
func (m *Manager) Settings() *settings.Resolver { return m.res }
func (m *Manager) Recorder() *audit.Recorder    { return m.recorder }

// Installs the package deleter and mover, set once at startup
func (m *Manager) SetPackageManager(p PackageManager) { m.packages = p }

// Registers post receive subscribers
func (m *Manager) Subscribe(fns ...RefSubscriber) {
	m.subsMu.Lock()
	m.subs = append(m.subs, fns...)
	m.subsMu.Unlock()
}

// Effective git settings for a repository's tier
func (m *Manager) gitSettings(ctx context.Context, repo *db.GitRepository) *v1.GitSettings {
	if repo != nil && repo.IsOrgNamespace {
		if orgID, err := m.res.RepoOrgID(ctx, repo.ID); err == nil && orgID != "" {
			return m.res.Org(ctx, orgID).GetGit()
		}
	}
	return m.res.System(ctx).GetGit()
}

// Effective git settings for a namespace before a repository exists
func (m *Manager) NamespaceGitSettings(ctx context.Context, namespace string) *v1.GitSettings {
	if org, err := m.store.GetOrganization(ctx, namespace); err == nil && org != nil {
		return m.res.Org(ctx, org.ID).GetGit()
	}
	return m.res.System(ctx).GetGit()
}

// Git settings of a repository, exported for services
func (m *Manager) GitSettings(ctx context.Context, repo *db.GitRepository) *v1.GitSettings {
	return m.gitSettings(ctx, repo)
}

// Reports whether git hosting is switched on
func (m *Manager) Enabled(ctx context.Context) bool {
	return m.res.System(ctx).GetGit().GetEnabled()
}

// ── URLs ─────────────────────────────────────────────────────────────────

// Scheme and host the caller should use for links
func (m *Manager) BaseURL(ctx context.Context) string {
	sys := m.res.System(ctx)
	host := sys.GetServer().GetPublicHostname()
	scheme := "http"
	if sys.GetTls().GetMode() == v1.TLSMode_TLS_MODE_HTTPS_ONLY {
		scheme = "https"
	}
	if p := portal.FromContext(ctx); p != nil {
		if h := portal.HostFromContext(ctx); h != "" {
			host = h
		}
		if p.TLS {
			scheme = "https"
		}
	}
	// Forwarded scheme may upgrade, a spoofed header cannot downgrade
	if portal.SchemeFromContext(ctx) == "https" {
		scheme = "https"
	}
	return scheme + "://" + host
}

// Page url of a repository
func (m *Manager) HTMLURL(ctx context.Context, repo *db.GitRepository) string {
	return m.BaseURL(ctx) + "/" + m.displayPath(ctx, repo)
}

// Path as the caller addresses it, mapped on portals
func (m *Manager) displayPath(ctx context.Context, repo *db.GitRepository) string {
	full := repo.Namespace + "/" + repo.Name
	if p := portal.FromContext(ctx); p != nil && p.MapUnqualified && p.OrgName == repo.Namespace {
		return repo.Name
	}
	return full
}

// Https clone url honoring the portal host
func (m *Manager) HTTPCloneURL(ctx context.Context, repo *db.GitRepository, wiki bool) string {
	suffix := ".git"
	if wiki {
		suffix = ".wiki.git"
	}
	return m.BaseURL(ctx) + "/" + m.displayPath(ctx, repo) + suffix
}

// Ssh clone url, short form on port 22, empty when ssh is off
func (m *Manager) SSHCloneURL(ctx context.Context, repo *db.GitRepository, wiki bool) string {
	git := m.res.System(ctx).GetGit()
	if !git.GetSshEnabled() {
		return ""
	}
	suffix := ".git"
	if wiki {
		suffix = ".wiki.git"
	}
	host := git.GetSshPublicHost()
	port := 22
	if host == "" {
		host = m.res.System(ctx).GetServer().GetPublicHostname()
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		port = int(git.GetSshPort())
	} else if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	path := repo.Namespace + "/" + repo.Name + suffix
	if p := portal.FromContext(ctx); p != nil {
		if p.SSHPort > 0 {
			return "ssh://git@" + net.JoinHostPort(portalHost(ctx, host), strconv.Itoa(p.SSHPort)) + "/" + m.displayPath(ctx, repo) + suffix
		}
		if p.HidePrimaryLink {
			return ""
		}
	}
	if port == 22 {
		return fmt.Sprintf("git@%s:%s", host, path)
	}
	return "ssh://git@" + net.JoinHostPort(host, strconv.Itoa(port)) + "/" + path
}

func portalHost(ctx context.Context, fallback string) string {
	if h := portal.HostFromContext(ctx); h != "" {
		if bare, _, err := net.SplitHostPort(h); err == nil {
			return bare
		}
		return h
	}
	return fallback
}

// ── Creation and lifecycle ───────────────────────────────────────────────

// Parameters for a new repository
type CreateParams struct {
	Namespace     string
	Name          string
	Description   string
	Website       string
	Private       bool
	DefaultBranch string
	ObjectFormat  string
	Type          v1.GitRepoType
	MirrorConfig  string
	AutoInit      bool
	Gitignore     string
	License       string
	Topics        []string
	ForkOf        *db.GitRepository
	TemplateOf    *db.GitRepository
}

// Creates the row and the bare directory, adopting same named packages
func (m *Manager) Create(ctx context.Context, user *auth.AuthenticatedUser, p CreateParams) (*db.GitRepository, error) {
	if !ValidName(p.Name) {
		return nil, fmt.Errorf("%w: repository name must match %s and not end in .git or .wiki", ErrInvalid, NamePattern.String())
	}
	if existing, err := m.store.GetGitRepository(ctx, p.Namespace, p.Name); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, fmt.Errorf("%w: repository %s/%s already exists", ErrInvalid, p.Namespace, p.Name)
	}
	if redirect, moved, err := m.store.ResolveGitRepository(ctx, p.Namespace, p.Name); err != nil {
		return nil, err
	} else if moved && redirect != nil {
		if err := m.store.DB().WithContext(ctx).Where("namespace = ? AND name = ?", p.Namespace, p.Name).Delete(&db.GitRedirect{}).Error; err != nil {
			return nil, err
		}
	}
	gs := m.NamespaceGitSettings(ctx, p.Namespace)
	if p.DefaultBranch == "" {
		p.DefaultBranch = gs.GetDefaultBranch()
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.ObjectFormat == "" {
		p.ObjectFormat = "sha1"
	}
	if p.ObjectFormat != "sha1" && p.ObjectFormat != "sha256" {
		return nil, fmt.Errorf("%w: object format must be sha1 or sha256", ErrInvalid)
	}
	if p.Type == v1.GitRepoType_GIT_REPO_TYPE_UNSPECIFIED {
		p.Type = v1.GitRepoType_GIT_REPO_TYPE_STANDARD
	}
	ownerID, isOrg := user.ID, false
	if p.Namespace != user.Username {
		if org, _ := m.store.GetOrganization(ctx, p.Namespace); org != nil {
			ownerID, isOrg = org.ID, true
		} else if u, _ := m.store.GetUserByUsername(ctx, p.Namespace); u != nil {
			ownerID = u.ID
		} else {
			return nil, fmt.Errorf("%w: namespace %q does not exist", ErrInvalid, p.Namespace)
		}
	}
	repo := &db.GitRepository{
		Namespace: p.Namespace, Name: p.Name, Description: p.Description, Website: p.Website,
		OwnerID: ownerID, IsPrivate: p.Private, IsOrgNamespace: isOrg, DefaultBranch: p.DefaultBranch,
		ObjectFormat: p.ObjectFormat, Type: p.Type, MirrorConfig: p.MirrorConfig,
		LFSEnabled: gs.GetLfsEnabled(), Units: EncodeUnits(UnitsOf(nil)), MergeSettings: EncodeMergeSettings(MergeSettingsOf(nil)),
	}
	if p.ForkOf != nil {
		id := p.ForkOf.ID
		repo.ForkOfID = &id
		repo.Type = v1.GitRepoType_GIT_REPO_TYPE_FORK
		repo.DefaultBranch = p.ForkOf.DefaultBranch
		repo.ObjectFormat = p.ForkOf.ObjectFormat
	}
	if p.TemplateOf != nil {
		id := p.TemplateOf.ID
		repo.TemplateOfID = &id
	}
	if err := m.engine.Init(repo.Namespace, repo.Name, false, repo.DefaultBranch, repo.ObjectFormat); err != nil {
		return nil, err
	}
	if err := m.store.CreateGitRepository(ctx, repo); err != nil {
		_ = m.engine.Delete(repo.Namespace, repo.Name)
		return nil, err
	}
	if len(p.Topics) > 0 {
		_ = m.store.SetGitTopics(ctx, repo.ID, p.Topics)
	}
	if isOrg {
		m.includeInCreatorTeams(ctx, user, repo)
	}
	_ = m.store.SetGitWatch(ctx, user.ID, repo.ID, "all")
	m.AdoptPackages(ctx, user, repo)
	if p.AutoInit && p.ForkOf == nil && p.TemplateOf == nil {
		if err := m.autoInit(ctx, user, repo, p); err != nil {
			m.log.Error("git auto init %s/%s: %v", repo.Namespace, repo.Name, err)
		}
	}
	m.audit(ctx, user, "Git/create", repo.Namespace+"/"+repo.Name, audit.OutcomeSuccess)
	m.dispatchRepoEvent(ctx, repo, user, "created")
	return repo, nil
}

// Non default teams the creator belongs to that may create repos include the new one
func (m *Manager) includeInCreatorTeams(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository) {
	org, _ := m.store.GetOrganization(ctx, repo.Namespace)
	if org == nil {
		return
	}
	teams, _ := m.store.TeamsForUser(ctx, org.ID, user.ID)
	for _, t := range teams {
		if t.CanCreateRepos && !t.IncludesAllRepos && !t.IsDefault {
			_ = m.store.AddOrgTeamRepo(ctx, t.ID, repo.ID)
		}
	}
}

// Whether a user may manage an image package, false without a package manager
func (m *Manager) CanManageImage(ctx context.Context, user *auth.AuthenticatedUser, img *db.Repository) bool {
	return m.packages != nil && m.packages.CanManageImageRepo(ctx, user, img)
}

// Whether a user may manage an artifact package, false without a package manager
func (m *Manager) CanManageArtifact(ctx context.Context, user *auth.AuthenticatedUser, art *db.ArtifactRepository) bool {
	return m.packages != nil && m.packages.CanManageArtifactRepo(ctx, user, art)
}

// Links same named packages the creator already manages, a link shares them
func (m *Manager) AdoptPackages(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository) {
	images, _ := m.store.ListUnlinkedRepositoriesForGit(ctx, repo.Namespace, repo.Name)
	for _, img := range images {
		if m.CanManageImage(ctx, user, img) {
			_ = m.store.SetRepositoryGitRepo(ctx, img.ID, &repo.ID)
		}
	}
	if art, _ := m.store.GetArtifactRepository(ctx, repo.Namespace, repo.Name); art != nil && art.GitRepoID == nil && m.CanManageArtifact(ctx, user, art) {
		_ = m.store.SetArtifactRepositoryGitRepo(ctx, art.ID, &repo.ID)
	}
}

// Links a newly created image repo to its source by name when one exists
func (m *Manager) LinkImageByName(ctx context.Context, img *db.Repository) {
	if img.GitRepoID != nil {
		return
	}
	first := img.Name
	if i := strings.IndexByte(first, '/'); i > 0 {
		first = first[:i]
	}
	if g, _ := m.store.GetGitRepository(ctx, img.Namespace, first); g != nil {
		_ = m.store.SetRepositoryGitRepo(ctx, img.ID, &g.ID)
		img.GitRepoID = &g.ID
	}
}

// Links an image repo to the git repo its source label names
func (m *Manager) LinkImageBySource(ctx context.Context, img *db.Repository, source string) {
	if img.GitRepoID != nil || source == "" {
		return
	}
	ns, name, ok := m.parseSourceURL(ctx, source)
	if !ok || ns != img.Namespace {
		return
	}
	if g, _ := m.store.GetGitRepository(ctx, ns, name); g != nil {
		if err := m.store.SetRepositoryGitRepo(ctx, img.ID, &g.ID); err == nil {
			img.GitRepoID = &g.ID
		}
	}
}

// Namespace and name when a clone url points at this instance
func (m *Manager) parseSourceURL(ctx context.Context, raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	var host, p string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		host, p = u.Hostname(), u.Path
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"):
		rest := raw[strings.Index(raw, "@")+1:]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			return "", "", false
		}
		host, p = rest[:colon], rest[colon+1:]
	default:
		return "", "", false
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	parts := strings.Split(p, "/")
	if pt := portal.FromContext(ctx); pt != nil && strings.EqualFold(host, portalHost(ctx, "")) {
		if len(parts) == 1 && pt.MapUnqualified && pt.OrgName != "" {
			parts = []string{pt.OrgName, parts[0]}
		}
	} else if !m.isOwnHost(ctx, host) {
		return "", "", false
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// Reports whether a hostname is the public or ssh host of this instance
func (m *Manager) isOwnHost(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	sys := m.res.System(ctx)
	for _, cand := range []string{sys.GetServer().GetPublicHostname(), sys.GetGit().GetSshPublicHost()} {
		if h, _, err := net.SplitHostPort(cand); err == nil {
			cand = h
		}
		if cand != "" && strings.EqualFold(cand, host) {
			return true
		}
	}
	return false
}

// Links a newly created artifact repo to its source by name when one exists
func (m *Manager) LinkArtifactByName(ctx context.Context, art *db.ArtifactRepository) {
	if art.GitRepoID != nil {
		return
	}
	if g, _ := m.store.GetGitRepository(ctx, art.Namespace, art.Name); g != nil {
		_ = m.store.SetArtifactRepositoryGitRepo(ctx, art.ID, &g.ID)
		art.GitRepoID = &g.ID
	}
}

// Creates the first commit with a readme and optional gitignore and license
func (m *Manager) autoInit(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, p CreateParams) error {
	var edits []TreeEdit
	readme := fmt.Sprintf("# %s\n", repo.Name)
	if repo.Description != "" {
		readme += "\n" + repo.Description + "\n"
	}
	edits = append(edits, TreeEdit{Path: "README.md", Mode: filemode.Regular, Content: strings.NewReader(readme)})
	if p.Gitignore != "" {
		if body, ok := GitignoreTemplate(p.Gitignore); ok {
			edits = append(edits, TreeEdit{Path: ".gitignore", Mode: filemode.Regular, Content: strings.NewReader(body)})
		}
	}
	if p.License != "" {
		if body, ok := LicenseTemplate(p.License, user.Username); ok {
			edits = append(edits, TreeEdit{Path: "LICENSE", Mode: filemode.Regular, Content: strings.NewReader(body)})
		}
	}
	_, err := m.CommitToBranch(ctx, repo, user, CommitRequest{
		Branch: repo.DefaultBranch, Message: "Initial commit", Edits: edits, Options: HookOptions{BypassPullRequest: true, BypassSignedCommits: true},
	})
	return err
}

// Deletes rows, directories, lfs links, and optionally linked packages
func (m *Manager) Delete(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, deleteLinked bool) error {
	images, _ := m.store.ListRepositoriesByGitRepo(ctx, repo.ID)
	arts, _ := m.store.ListArtifactRepositoriesByGitRepo(ctx, repo.ID)
	if !deleteLinked && (len(images) > 0 || len(arts) > 0) {
		return fmt.Errorf("%w: repository still has %d linked packages, pass delete_linked or unlink them first", ErrInvalid, len(images)+len(arts))
	}
	lock := m.engine.LockRepo(repo.ID)
	defer lock.Unlock()
	if deleteLinked && m.packages != nil {
		for _, img := range images {
			if err := m.packages.DeleteImageRepo(ctx, img); err != nil {
				return err
			}
		}
		for _, art := range arts {
			if err := m.packages.DeleteArtifactRepo(ctx, art); err != nil {
				return err
			}
		}
	}
	oids, err := m.store.DeleteLFSLinks(ctx, repo.ID)
	if err != nil {
		return err
	}
	for _, oid := range oids {
		m.reapLFSBlob(ctx, oid)
	}
	if err := m.store.DeleteGitRepository(ctx, repo); err != nil {
		return err
	}
	m.res.ForgetRepo(repo.ID)
	_ = m.access.enforcer.RemoveObject("git", repo.Namespace+"/"+repo.Name)
	if err := m.engine.Delete(repo.Namespace, repo.Name); err != nil {
		return err
	}
	m.audit(ctx, user, "Git/delete", repo.Namespace+"/"+repo.Name, audit.OutcomeSuccess)
	m.dispatchRepoEvent(ctx, repo, user, "deleted")
	return nil
}

// Removes an lfs blob once no artifact or lfs link references it
func (m *Manager) reapLFSBlob(ctx context.Context, oid string) {
	links, err := m.store.CountLFSLinksByOID(ctx, oid)
	if err != nil || links > 0 {
		return
	}
	digest := "sha256:" + oid
	count, err := m.store.CountArtifactsByDigest(ctx, digest)
	if err != nil || count > 0 {
		return
	}
	if err := m.blobs.DeleteBlob(digest); err != nil {
		m.log.Error("lfs blob delete %s: %v", oid, err)
	}
}

// Renames or transfers a repository and its name linked packages
func (m *Manager) Move(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, newNamespace, newName string) (*db.GitRepository, error) {
	if !ValidName(newName) {
		return nil, fmt.Errorf("%w: invalid repository name", ErrInvalid)
	}
	if newNamespace == repo.Namespace && newName == repo.Name {
		return repo, nil
	}
	if existing, _ := m.store.GetGitRepository(ctx, newNamespace, newName); existing != nil {
		return nil, fmt.Errorf("%w: %s/%s already exists", ErrInvalid, newNamespace, newName)
	}
	var ownerID string
	isOrg := false
	if org, _ := m.store.GetOrganization(ctx, newNamespace); org != nil {
		ownerID, isOrg = org.ID, true
	} else if u, _ := m.store.GetUserByUsername(ctx, newNamespace); u != nil {
		ownerID = u.ID
	} else {
		return nil, fmt.Errorf("%w: namespace %q does not exist", ErrInvalid, newNamespace)
	}
	lock := m.engine.LockRepo(repo.ID)
	defer lock.Unlock()
	defer m.engine.MarkRenaming(repo.ID)()
	oldFull := repo.Namespace + "/" + repo.Name
	undo, err := m.movePackages(ctx, repo, newNamespace, newName, ownerID, isOrg)
	if err != nil {
		return nil, err
	}
	if err := m.engine.Rename(repo.Namespace, repo.Name, newNamespace, newName); err != nil {
		undo()
		return nil, err
	}
	if err := m.store.MoveGitRepository(ctx, repo, newNamespace, newName, ownerID, isOrg); err != nil {
		_ = m.engine.Rename(newNamespace, newName, repo.Namespace, repo.Name)
		undo()
		return nil, err
	}
	m.res.ForgetRepo(repo.ID)
	newFull := newNamespace + "/" + newName
	_ = m.access.enforcer.RenameObject("git", oldFull, newFull)
	_ = m.store.RenameAPITokenScope(ctx, int32(v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO), oldFull, newFull)
	moved, err := m.store.GetGitRepositoryByID(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	m.audit(ctx, user, "Git/move", oldFull+" -> "+newFull, audit.OutcomeSuccess)
	m.dispatchRepoEvent(ctx, moved, user, "renamed")
	return moved, nil
}

// Bytes a push may still carry under the quota, zero means unlimited
func (m *Manager) pushBudget(ctx context.Context, repo *db.GitRepository, wiki bool) (int64, error) {
	if wiki {
		return 0, nil
	}
	maxMB := m.gitSettings(ctx, repo).GetMaxRepoSizeMb()
	if maxMB <= 0 {
		return 0, nil
	}
	return max(maxMB*1024*1024-repo.SizeBytes, 0) + pushSlackBytes, fmt.Errorf("%w: push exceeds the %d MB repository quota", ErrTooLarge, maxMB)
}

// Moves the name linked packages first, the undo puts back whatever succeeded
func (m *Manager) movePackages(ctx context.Context, repo *db.GitRepository, newNamespace, newName, ownerID string, isOrg bool) (func(), error) {
	var undos []func()
	undo := func() {
		for i := len(undos) - 1; i >= 0; i-- {
			undos[i]()
		}
	}
	if m.packages == nil {
		return undo, nil
	}
	images, err := m.store.ListRepositoriesByGitRepo(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	for _, img := range images {
		if img.Namespace != repo.Namespace || (img.Name != repo.Name && !strings.HasPrefix(img.Name, repo.Name+"/")) {
			continue
		}
		child := newName + strings.TrimPrefix(img.Name, repo.Name)
		if err := m.packages.MoveImageRepo(ctx, img, newNamespace, child, ownerID, isOrg); err != nil {
			undo()
			return nil, fmt.Errorf("%w: moving linked image %s/%s: %w", ErrInvalid, img.Namespace, img.Name, err)
		}
		moved := *img
		moved.Namespace, moved.Name = newNamespace, child
		undos = append(undos, func() {
			if err := m.packages.MoveImageRepo(ctx, &moved, img.Namespace, img.Name, img.OwnerID, img.IsOrgNamespace); err != nil {
				m.log.Error("restoring linked image %s/%s: %v", img.Namespace, img.Name, err)
			}
		})
	}
	arts, err := m.store.ListArtifactRepositoriesByGitRepo(ctx, repo.ID)
	if err != nil {
		undo()
		return nil, err
	}
	for _, art := range arts {
		if art.Namespace != repo.Namespace || art.Name != repo.Name {
			continue
		}
		if err := m.packages.MoveArtifactRepo(ctx, art, newNamespace, newName, ownerID); err != nil {
			undo()
			return nil, fmt.Errorf("%w: moving linked artifact repo %s/%s: %w", ErrInvalid, art.Namespace, art.Name, err)
		}
		moved := *art
		moved.Namespace, moved.Name = newNamespace, newName
		undos = append(undos, func() {
			if err := m.packages.MoveArtifactRepo(ctx, &moved, art.Namespace, art.Name, art.OwnerID); err != nil {
				m.log.Error("restoring linked artifact repo %s/%s: %v", art.Namespace, art.Name, err)
			}
		})
	}
	return undo, nil
}

// Copies every ref of a parent into a fresh repository
func (m *Manager) Fork(ctx context.Context, user *auth.AuthenticatedUser, parent *db.GitRepository, namespace, name string) (*db.GitRepository, error) {
	if name == "" {
		name = parent.Name
	}
	fork, err := m.Create(ctx, user, CreateParams{
		Namespace: namespace, Name: name, Description: parent.Description, Private: parent.IsPrivate, ForkOf: parent,
	})
	if err != nil {
		return nil, err
	}
	if err := m.FetchLocal(ctx, fork, parent, false, "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"); err != nil {
		_ = m.Delete(ctx, user, fork, false)
		return nil, err
	}
	r, err := m.engine.OpenRepo(fork.Namespace, fork.Name, false)
	if err == nil {
		if r.BranchHash(parent.DefaultBranch).IsZero() {
			if branches, _ := r.Branches(); len(branches) > 0 {
				_ = r.SetHead(sortedKeys(branches)[0])
				_ = m.store.UpdateGitRepositoryColumns(ctx, fork.ID, map[string]any{"default_branch": sortedKeys(branches)[0]})
			}
		} else {
			_ = r.SetHead(parent.DefaultBranch)
		}
		m.refreshCounts(ctx, fork, r)
		r.Close()
	}
	m.dispatchRepoEvent(ctx, parent, user, "forked")
	return m.store.GetGitRepositoryByID(ctx, fork.ID)
}

// Fetches refs from another local repository through the file transport
func (m *Manager) FetchLocal(ctx context.Context, dst, src *db.GitRepository, srcWiki bool, refspecs ...string) error {
	return m.fetchInto(ctx, dst, false, m.engine.LocalURL(src.Namespace, src.Name, srcWiki), refspecs, nil)
}

// Creates a repository from a template's default branch tree
func (m *Manager) UseTemplate(ctx context.Context, user *auth.AuthenticatedUser, tpl *db.GitRepository, p CreateParams) (*db.GitRepository, error) {
	p.TemplateOf = tpl
	repo, err := m.Create(ctx, user, p)
	if err != nil {
		return nil, err
	}
	src, err := m.engine.OpenRepo(tpl.Namespace, tpl.Name, false)
	if err != nil {
		return repo, err
	}
	defer src.Close()
	head, err := src.Resolve(tpl.DefaultBranch)
	if err != nil {
		return repo, nil
	}
	entries, truncated, err := src.Tree(ctx, head, "", true)
	if err == nil && truncated {
		err = fmt.Errorf("%w: template has more than %d files", ErrTooLarge, MaxTreeEntries)
	}
	if err != nil {
		return repo, err
	}
	var edits []TreeEdit
	for _, e := range entries {
		if !e.Mode.IsFile() && e.Mode != filemode.Symlink {
			continue
		}
		content, err := src.BlobContent(e.Hash)
		if err != nil {
			return repo, err
		}
		edits = append(edits, TreeEdit{Path: e.Path, Mode: e.Mode, Content: bytes.NewReader(content)})
	}
	_, err = m.CommitToBranch(ctx, repo, user, CommitRequest{
		Branch: repo.DefaultBranch, Message: "Initial commit from template " + tpl.Namespace + "/" + tpl.Name, Edits: edits,
		Options: HookOptions{BypassPullRequest: true, BypassSignedCommits: true},
	})
	return repo, err
}

// ── Commits through the policy path ──────────────────────────────────────

// Web commit request
type CommitRequest struct {
	Branch       string
	NewBranch    string
	ExpectedHead plumbing.Hash
	Message      string
	Edits        []TreeEdit
	Author       *object.Signature
	Wiki         bool
	Options      HookOptions
	// Builds the commit when edits do not describe it, for merges
	Build func(r *Repo, parent plumbing.Hash) (plumbing.Hash, error)
}

// Author identity for a user's web commits
func (m *Manager) Signature(ctx context.Context, user *auth.AuthenticatedUser) object.Signature {
	name := user.Username
	email := user.Email
	if u, err := m.store.GetUserByID(ctx, user.ID); err == nil && u != nil {
		if u.DisplayName != "" {
			name = u.DisplayName
		}
		if u.Email != nil && *u.Email != "" {
			email = *u.Email
		}
	}
	if email == "" {
		host := m.res.System(ctx).GetServer().GetPublicHostname()
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		email = user.Username + "@noreply." + host
	}
	return object.Signature{Name: name, Email: email, When: time.Now()}
}

// Instance signer when signing is enabled
func (m *Manager) Signer(ctx context.Context) Signer {
	if !m.res.System(ctx).GetGit().GetSigning().GetEnabled() {
		return nil
	}
	return m.signing.InstanceSigner()
}

// Commits edits onto a branch through the same policy path as a push
func (m *Manager) CommitToBranch(ctx context.Context, repo *db.GitRepository, user *auth.AuthenticatedUser, req CommitRequest) (plumbing.Hash, error) {
	if repo.IsArchived {
		return plumbing.ZeroHash, ErrArchived
	}
	if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR && !req.Options.Mirror && !req.Wiki {
		return plumbing.ZeroHash, ErrMirror
	}
	if !req.Options.Mirror && !req.Wiki && m.importInProgress(ctx, repo) {
		return plumbing.ZeroHash, ErrImporting
	}
	lock := m.engine.LockRepo(repo.ID)
	defer lock.Unlock()
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, req.Wiki)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer r.Close()

	target := req.Branch
	if req.NewBranch != "" {
		target = req.NewBranch
	}
	parent := r.BranchHash(req.Branch)
	if !req.ExpectedHead.IsZero() && parent != req.ExpectedHead {
		return plumbing.ZeroHash, ErrRefChanged
	}
	old := r.BranchHash(target)
	if req.NewBranch != "" && !old.IsZero() {
		return plumbing.ZeroHash, fmt.Errorf("%w: branch %s already exists", ErrInvalid, req.NewBranch)
	}

	var newCommit plumbing.Hash
	if req.Build != nil {
		newCommit, err = req.Build(r, parent)
		if err != nil {
			return plumbing.ZeroHash, err
		}
	} else {
		baseTree := plumbing.ZeroHash
		var parents []plumbing.Hash
		if !parent.IsZero() {
			baseTree, err = r.TreeOf(parent)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			parents = []plumbing.Hash{parent}
		}
		tree, err := r.EditTree(baseTree, req.Edits)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if tree == baseTree && !parent.IsZero() {
			return plumbing.ZeroHash, fmt.Errorf("%w: no changes to commit", ErrInvalid)
		}
		author := m.Signature(ctx, user)
		if req.Author != nil {
			author = *req.Author
		}
		committer := m.Signature(ctx, user)
		newCommit, err = r.WriteCommit(ctx, CommitSpec{Tree: tree, Parents: parents, Message: req.Message, Author: author, Committer: committer, Signer: m.Signer(ctx)})
		if err != nil {
			return plumbing.ZeroHash, err
		}
	}
	refName := plumbing.NewBranchReferenceName(target)
	cmd := &packp.Command{Name: refName, Old: old, New: newCommit}
	opts := req.Options
	opts.Wiki = req.Wiki
	if err := m.preReceive(ctx, repo, user, opts, &transport.PreReceiveInfo{Storer: r.Storer, Commands: []*packp.Command{cmd}, Progress: &bytes.Buffer{}}); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := r.UpdateRef(refName, old, newCommit); err != nil {
		return plumbing.ZeroHash, err
	}
	if r.HeadBranch() == "" {
		_ = r.SetHead(target)
	}
	m.AfterRefUpdate(ctx, RefEvent{Repo: repo, Actor: user, Updates: []RefUpdate{{Name: refName, Old: old, New: newCommit}}, Wiki: req.Wiki, Mirror: opts.Mirror}, lock)
	return newCommit, nil
}

// Creates, moves, or deletes a ref under policy, zero new deletes
func (m *Manager) UpdateRef(ctx context.Context, repo *db.GitRepository, user *auth.AuthenticatedUser, name plumbing.ReferenceName, old, new plumbing.Hash, opts HookOptions) error {
	if repo.IsArchived {
		return ErrArchived
	}
	if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR && !opts.Mirror {
		return ErrMirror
	}
	if !opts.Mirror && !opts.Wiki && m.importInProgress(ctx, repo) {
		return ErrImporting
	}
	lock := m.engine.LockRepo(repo.ID)
	defer lock.Unlock()
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, opts.Wiki)
	if err != nil {
		return err
	}
	defer r.Close()
	cmd := &packp.Command{Name: name, Old: old, New: new}
	if err := m.preReceive(ctx, repo, user, opts, &transport.PreReceiveInfo{Storer: r.Storer, Commands: []*packp.Command{cmd}, Progress: &bytes.Buffer{}}); err != nil {
		return err
	}
	if err := r.UpdateRef(name, old, new); err != nil {
		return err
	}
	forced := false
	if !old.IsZero() && !new.IsZero() {
		if ok, err := r.IsAncestor(old, new); err == nil && !ok {
			forced = true
		}
	}
	m.AfterRefUpdate(ctx, RefEvent{Repo: repo, Actor: user, Updates: []RefUpdate{{Name: name, Old: old, New: new, Forced: forced}}, Wiki: opts.Wiki, Mirror: opts.Mirror}, lock)
	return nil
}

// ── Post receive fan out ─────────────────────────────────────────────────

// Bookkeeping under store lock, fan out under push lock only
func (m *Manager) AfterRefUpdate(ctx context.Context, ev RefEvent, lock *RepoLock) {
	defer lock.UnlockStore()
	repo := ev.Repo
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, ev.Wiki)
	if err != nil {
		m.log.Error("post receive open %s/%s: %v", repo.Namespace, repo.Name, err)
		return
	}
	if !ev.Wiki {
		// Unborn HEAD lands on the configured default branch, else the first branch pushed
		if r.HeadBranch() == "" || r.BranchHash(r.HeadBranch()).IsZero() {
			chosen := ""
			for _, u := range ev.Updates {
				if u.Name.IsBranch() && !u.New.IsZero() && u.Name.Short() == repo.DefaultBranch {
					chosen = repo.DefaultBranch
				}
			}
			if chosen == "" {
				for _, u := range ev.Updates {
					if u.Name.IsBranch() && !u.New.IsZero() {
						chosen = u.Name.Short()
						break
					}
				}
			}
			if chosen != "" {
				_ = r.SetHead(chosen)
				if chosen != repo.DefaultBranch {
					repo.DefaultBranch = chosen
					_ = m.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"default_branch": chosen})
				}
			}
		}
		m.refreshCounts(ctx, repo, r)
	}
	r.Close()
	lock.UnlockStore()

	actor := "system"
	actorID := ""
	if ev.Actor != nil {
		actor, actorID = ev.Actor.Username, ev.Actor.ID
	}
	for _, u := range ev.Updates {
		detail := fmt.Sprintf("%s/%s:%s@%s", repo.Namespace, repo.Name, u.Name.Short(), u.New.String())
		if ev.Wiki {
			detail = fmt.Sprintf("%s/%s.wiki:%s@%s", repo.Namespace, repo.Name, u.Name.Short(), u.New.String())
		}
		m.recorder.Record(ctx, audit.Event{Action: "Git/push", Resource: "git", Outcome: audit.OutcomeSuccess, Detail: detail, Actor: actor, ActorID: actorID})
	}
	if ev.Wiki {
		for _, u := range ev.Updates {
			m.dispatcher.DispatchGit(ctx, "git_wiki", repo.ID, repo.Namespace, webhook.GitPayload{
				Event: "git_wiki", Action: u.Kind(), Timestamp: time.Now().UTC().Format(time.RFC3339),
				Repository: m.payloadRepo(ctx, repo), Sender: webhook.Sender{Username: actor},
				Wiki: &webhook.WikiPayload{Action: u.Kind(), Ref: u.Name.String(), Before: u.Old.String(), After: u.New.String()},
			})
		}
	} else {
		m.dispatchPush(ctx, repo, ev, actor)
	}
	m.subsMu.RLock()
	subs := append([]RefSubscriber{}, m.subs...)
	m.subsMu.RUnlock()
	for _, fn := range subs {
		fn(ctx, ev)
	}
}

// Recomputes size and ref counts after a write
func (m *Manager) refreshCounts(ctx context.Context, repo *db.GitRepository, r *Repo) {
	size, err := m.engine.RefreshSize(repo.Namespace, repo.Name, false)
	if err != nil {
		size = repo.SizeBytes
	}
	branches, tags := r.RefCounts()
	if err := m.store.RecordGitPush(ctx, repo.ID, size, branches, tags); err != nil {
		m.log.Error("git counters %s/%s: %v", repo.Namespace, repo.Name, err)
	}
	repo.SizeBytes, repo.BranchCount, repo.TagCount = size, branches, tags
}

// Sends push, branch, and tag webhooks for one event
func (m *Manager) dispatchPush(ctx context.Context, repo *db.GitRepository, ev RefEvent, actor string) {
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	defer r.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, u := range ev.Updates {
		base := webhook.GitPayload{Timestamp: now, Repository: m.payloadRepo(ctx, repo), Sender: webhook.Sender{Username: actor}}
		switch {
		case u.Name.IsBranch():
			if u.Old.IsZero() || u.New.IsZero() {
				p := base
				p.Event, p.Action = "git_branch", u.Kind()
				p.Ref = &webhook.RefPayload{Ref: u.Name.String(), Name: u.Name.Short(), SHA: u.New.String()}
				m.dispatcher.DispatchGit(ctx, "git_branch", repo.ID, repo.Namespace, p)
			}
			if !u.New.IsZero() {
				commits, truncated, _ := r.CommitsBetween(ctx, u.Old, u.New, 20)
				p := base
				p.Event, p.Action = "git_push", u.Kind()
				push := &webhook.PushPayload{Ref: u.Name.String(), Before: u.Old.String(), After: u.New.String(), Forced: u.Forced, Truncated: truncated, Pusher: actor,
					CompareURL: fmt.Sprintf("%s/compare/%s...%s", m.HTMLURL(ctx, repo), shortHash(u.Old), shortHash(u.New))}
				for _, c := range commits {
					push.Commits = append(push.Commits, webhook.CommitPayload{
						SHA: c.Hash.String(), Message: c.Message, URL: m.HTMLURL(ctx, repo) + "/commit/" + c.Hash.String(),
						Author: webhook.Person{Name: c.Author.Name, Email: c.Author.Email}, Committer: webhook.Person{Name: c.Committer.Name, Email: c.Committer.Email},
						Timestamp: c.Committer.When.UTC().Format(time.RFC3339),
					})
				}
				p.Push = push
				m.dispatcher.DispatchGit(ctx, "git_push", repo.ID, repo.Namespace, p)
			}
		case u.Name.IsTag():
			p := base
			p.Event, p.Action = "git_tag", u.Kind()
			p.Ref = &webhook.RefPayload{Ref: u.Name.String(), Name: u.Name.Short(), SHA: u.New.String()}
			m.dispatcher.DispatchGit(ctx, "git_tag", repo.ID, repo.Namespace, p)
		}
	}
}

func shortHash(h plumbing.Hash) string {
	if h.IsZero() {
		return "0000000"
	}
	return h.String()[:7]
}

// Repository section every git webhook carries
func (m *Manager) payloadRepo(ctx context.Context, repo *db.GitRepository) webhook.GitRepoPayload {
	return webhook.GitRepoPayload{
		Namespace: repo.Namespace, Name: repo.Name, FullName: repo.Namespace + "/" + repo.Name,
		CloneURL: m.HTTPCloneURL(ctx, repo, false), SSHURL: m.SSHCloneURL(ctx, repo, false), HTMLURL: m.HTMLURL(ctx, repo),
		Private: repo.IsPrivate, DefaultBranch: repo.DefaultBranch,
	}
}

// Sends the repository lifecycle webhook
func (m *Manager) dispatchRepoEvent(ctx context.Context, repo *db.GitRepository, user *auth.AuthenticatedUser, action string) {
	actor := "system"
	if user != nil {
		actor = user.Username
	}
	m.dispatcher.DispatchGit(ctx, "git_repository", repo.ID, repo.Namespace, webhook.GitPayload{
		Event: "git_repository", Action: action, Timestamp: time.Now().UTC().Format(time.RFC3339),
		Repository: m.payloadRepo(ctx, repo), Sender: webhook.Sender{Username: actor},
	})
}

// Dispatches a webhook built by a forge feature
func (m *Manager) DispatchGit(ctx context.Context, event string, repo *db.GitRepository, payload webhook.GitPayload) {
	payload.Event = event
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	payload.Repository = m.payloadRepo(ctx, repo)
	m.dispatcher.DispatchGit(ctx, event, repo.ID, repo.Namespace, payload)
}

// Audit row for a repository level action
func (m *Manager) audit(ctx context.Context, user *auth.AuthenticatedUser, action, detail, outcome string) {
	ev := audit.Event{Action: action, Resource: "git", Outcome: outcome, Detail: detail}
	if user != nil {
		ev.Actor, ev.ActorID = user.Username, user.ID
	}
	m.recorder.Record(ctx, ev)
}

// Records an audit event with the caller's source ip for transports
func (m *Manager) Audit(ctx context.Context, ev audit.Event) {
	m.recorder.Record(ctx, ev)
}

// ── Mirrors ──────────────────────────────────────────────────────────────

// Fetches refspecs from a url into a repository, every write goes through the hooks
func (m *Manager) fetchInto(ctx context.Context, repo *db.GitRepository, wiki bool, remoteURL string, refspecs []string, clientOpts []fetchOption) error {
	lock := m.engine.LockRepo(repo.ID)
	defer lock.Unlock()
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, wiki)
	if err != nil {
		return err
	}
	defer r.Close()
	before := snapshotRefs(r)
	if err := fetchRefs(ctx, m.engine, r, remoteURL, refspecs, clientOpts); err != nil {
		return err
	}
	after := snapshotRefs(r)
	// Server owned staging refs are plumbing, nobody is told about them
	var updates []RefUpdate
	for _, u := range diffSnapshots(before, after) {
		if !IsStagingRef(u.Name) {
			updates = append(updates, u)
		}
	}
	if len(updates) > 0 {
		m.AfterRefUpdate(ctx, RefEvent{Repo: repo, Actor: nil, Updates: updates, Wiki: wiki, Mirror: true}, lock)
	}
	return nil
}

// Refs under refs/distroface that only exist while an operation runs
func IsStagingRef(name plumbing.ReferenceName) bool {
	return strings.HasPrefix(name.String(), "refs/distroface/")
}

// Removes a staging ref, the objects it pinned fall to maintenance
func (m *Manager) DropStagingRef(repo *db.GitRepository, ref plumbing.ReferenceName) {
	if !IsStagingRef(ref) {
		return
	}
	unlock := m.engine.LockStore(repo.ID)
	defer unlock()
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	defer r.Close()
	if current, err := r.Resolve(ref.String()); err == nil {
		_ = r.UpdateRef(ref, current, plumbing.ZeroHash)
	}
}

func snapshotRefs(r *Repo) map[plumbing.ReferenceName]plumbing.Hash {
	out := map[plumbing.ReferenceName]plumbing.Hash{}
	iter, err := r.Repo.References()
	if err != nil {
		return out
	}
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference && (ref.Name().IsBranch() || ref.Name().IsTag()) {
			out[ref.Name()] = ref.Hash()
		}
		return nil
	})
	return out
}

func diffSnapshots(before, after map[plumbing.ReferenceName]plumbing.Hash) []RefUpdate {
	var out []RefUpdate
	for name, h := range after {
		if old, ok := before[name]; !ok {
			out = append(out, RefUpdate{Name: name, Old: plumbing.ZeroHash, New: h})
		} else if old != h {
			out = append(out, RefUpdate{Name: name, Old: old, New: h})
		}
	}
	for name, old := range before {
		if _, ok := after[name]; !ok {
			out = append(out, RefUpdate{Name: name, Old: old, New: plumbing.ZeroHash})
		}
	}
	return out
}

// Parses stored mirror configuration
func ParseMirrorConfig(raw string) *v1.MirrorConfig {
	cfg := &v1.MirrorConfig{}
	if strings.TrimSpace(raw) != "" {
		_ = protojson.Unmarshal([]byte(raw), cfg)
	}
	return cfg
}

// Validates an upstream git url for mirrors
func ValidateUpstreamURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: upstream must be an https or ssh url", ErrInvalid)
	}
	switch u.Scheme {
	case "https", "http", "ssh":
	default:
		return nil, fmt.Errorf("%w: upstream scheme %q is not allowed, use https or ssh", ErrInvalid, u.Scheme)
	}
	return u, nil
}
