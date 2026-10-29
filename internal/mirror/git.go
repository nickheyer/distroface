package mirror

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/proto"
)

// Returned before the git manager is wired
var ErrGitUnavailable = errors.New("git mirroring is not available")

// Installs the git manager used by git syncs
// SetSealer installs the key used to protect stored ssh keys
func (m *Monitor) SetSealer(current, legacy *auth.Sealer) {
	m.sealer = current
	m.legacySealer = legacy
}

func (m *Monitor) SetGit(g *gitrepo.Manager) {
	m.git = g
}

// Git manager handle for rpc visibility checks
func (m *Monitor) GitManager() *gitrepo.Manager {
	return m.git
}

func (m *Monitor) mirroringEnabled(ctx context.Context) bool {
	return m.res.System(ctx).GetMirror().GetEnabled()
}

// Confirms the upstream is reachable and the refs are listable
func (m *Monitor) ValidateGitMirror(ctx context.Context, cfg *v1.MirrorConfig) error {
	if m.git == nil {
		return fmt.Errorf("%w: git mirroring is unavailable", ErrInvalid)
	}
	if err := validateCommon(cfg); err != nil {
		return err
	}
	if err := m.checkInterval(ctx, cfg); err != nil {
		return err
	}
	up, err := parseGitUpstream(cfg.GetUpstream())
	if err != nil {
		return err
	}
	if _, err := gitrepo.ValidateUpstreamURL(up.URL); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	// No per repo key exists before the repo does
	if up.SSH {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()
	_, err = m.git.ListRemote(probeCtx, up.URL, m.gitHTTPAuth(up, cfg), gitProbeTimeout)
	return describeProbeError(up, err)
}

// Starts an immediate background fetch of a git mirror
func (m *Monitor) SyncGitRepoNow(repo *db.GitRepository) error {
	if m.git == nil {
		return ErrGitUnavailable
	}
	if repo.Type != v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
		return fmt.Errorf("%w: repository is not a mirror", ErrInvalid)
	}
	cfg, err := m.Config(repo.MirrorConfig)
	if err != nil {
		return err
	}
	if !m.mirroringEnabled(m.baseCtx) {
		return ErrDisabled
	}
	m.refreshLimits(m.baseCtx)
	if state := parseGitMirrorState(repo.MirrorState); state.RateLimited && state.CoolingDown(time.Now()) {
		return &CooldownError{Until: state.CooldownUntil}
	}
	key := gitKey(repo.ID)
	if !m.try(key) {
		return ErrSyncInFlight
	}
	go func() {
		defer m.done(key)
		m.execGitSync(m.baseCtx, repo, cfg)
	}()
	return nil
}

// Cancels an in flight git mirror sync or import
func (m *Monitor) StopGitSync(repo *db.GitRepository) error {
	if err := m.stopSync(gitKey(repo.ID)); !errors.Is(err, ErrNoActiveSync) {
		return err
	}
	return m.stopSync(importKey(repo.ID))
}

// Starts an immediate background push to one push mirror
func (m *Monitor) SyncPushMirrorNow(repo *db.GitRepository, pm *db.GitPushMirror) error {
	if m.git == nil {
		return ErrGitUnavailable
	}
	if !m.mirroringEnabled(m.baseCtx) {
		return ErrDisabled
	}
	m.refreshLimits(m.baseCtx)
	if state := parsePushMirrorState(pm.State); state.RateLimited && state.CoolingDown(time.Now()) {
		return &CooldownError{Until: state.CooldownUntil}
	}
	key := pushKey(pm.ID)
	if !m.try(key) {
		return ErrSyncInFlight
	}
	go func() {
		defer m.done(key)
		m.execPushSync(m.baseCtx, repo, pm)
	}()
	return nil
}

// Mints the repo ssh key, returns the public line
func (m *Monitor) GenerateGitMirrorKey(ctx context.Context, repo *db.GitRepository) (string, error) {
	if m.git == nil {
		return "", ErrGitUnavailable
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	comment := fmt.Sprintf("distroface-mirror-%s-%s", repo.Namespace, repo.Name)
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	sealed, err := m.sealer.Seal(string(pem.EncodeToMemory(block)))
	if err != nil {
		return "", err
	}
	state := parseGitMirrorState(repo.MirrorState)
	state.SSHPrivateKey = sealed
	state.SSHPublicKey = line
	state.HostKey = ""
	encoded := state.Encode()
	if err := m.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"mirror_state": encoded}); err != nil {
		return "", err
	}
	repo.MirrorState = encoded
	return line, nil
}

// Public half of the repo ssh key, empty when absent
func (m *Monitor) GitMirrorPublicKey(repo *db.GitRepository) string {
	if repo == nil {
		return ""
	}
	return parseGitMirrorState(repo.MirrorState).SSHPublicKey
}

// What a one time import copies besides branches and tags
type ImportOptions struct {
	Metadata bool `json:"metadata"`
	Wiki     bool `json:"wiki"`
	LFS      bool `json:"lfs"`
	// Release entries plus their assets into a linked package
	Releases bool `json:"releases"`
}

// Import sources ride in the mirror config column meanwhile
func importConfig(src *v1.GitImportSource) *v1.MirrorConfig {
	cfg := &v1.MirrorConfig{Upstream: strings.TrimSpace(src.GetUpstream()), Username: strings.TrimSpace(src.GetUsername())}
	if src.AuthToken != nil {
		cfg.AuthToken = proto.String(src.GetAuthToken())
	}
	return cfg
}

// Confirms an import upstream is reachable before the repository exists
func (m *Monitor) ValidateGitImport(ctx context.Context, src *v1.GitImportSource) error {
	return m.ValidateGitMirror(ctx, importConfig(src))
}

// EncodeImportSource stores an import source with its token sealed
func (m *Monitor) EncodeImportSource(src *v1.GitImportSource) (string, error) {
	return m.EncodeConfig(importConfig(src))
}

// Whether an import row still has phases left
func ImportPending(imp *db.GitImport) bool {
	return imp != nil && imp.Phase != importDone
}

// Notes an import recorded, skipped pieces and upstream gaps
func ImportNotes(imp *db.GitImport) []string {
	if imp == nil {
		return nil
	}
	return parseImportState(imp.State).Notes
}

// Whether the importer is working on the repository right now
func (m *Monitor) ImportRunning(repoID string) bool {
	return m.IsSyncing(importKey(repoID))
}

// Starts an import, nil options resume the recorded one
func (m *Monitor) StartGitImport(repo *db.GitRepository, opts *ImportOptions) error {
	if m.git == nil {
		return ErrGitUnavailable
	}
	ctx := m.baseCtx
	cfg, err := m.Config(repo.MirrorConfig)
	if err != nil {
		return err
	}
	up, err := parseGitUpstream(cfg.GetUpstream())
	if err != nil {
		return err
	}
	imp, err := m.store.GetGitImport(ctx, repo.ID)
	if err != nil {
		return err
	}
	switch {
	case imp == nil && opts == nil:
		return fmt.Errorf("%w: repository has no import to resume", ErrInvalid)
	case imp == nil:
		st := parseImportState("{}")
		st.Options = *opts
		imp = &db.GitImport{RepoID: repo.ID, Source: up.Forge, Upstream: cfg.GetUpstream(), State: st.encode()}
	case imp.Phase == importDone:
		return nil
	}
	// The sweep resumes the row once an admin enables mirroring
	if !m.mirroringEnabled(ctx) {
		imp.LastError = "mirroring is disabled on this instance, the import starts once an admin enables it"
		return m.store.SaveGitImport(ctx, imp)
	}
	imp.LastError = ""
	if err := m.store.SaveGitImport(ctx, imp); err != nil {
		return err
	}
	m.refreshLimits(ctx)
	key := importKey(repo.ID)
	if !m.try(key) {
		return ErrSyncInFlight
	}
	go func() {
		defer m.done(key)
		m.execImport(ctx, repo, imp)
	}()
	return nil
}

// Stops and forgets an import, the repository keeps what arrived
func (m *Monitor) DismissGitImport(ctx context.Context, repo *db.GitRepository) error {
	if err := m.stopSync(importKey(repo.ID)); err != nil && !errors.Is(err, ErrNoActiveSync) {
		return err
	}
	if err := m.store.DeleteGitImport(ctx, repo.ID); err != nil {
		return err
	}
	if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
		return nil
	}
	return m.clearImportSource(ctx, repo)
}

// Drops the upstream and credentials once nothing needs them
func (m *Monitor) clearImportSource(ctx context.Context, repo *db.GitRepository) error {
	cols := map[string]any{
		"type": v1.GitRepoType_GIT_REPO_TYPE_STANDARD, "mirror_config": "", "mirror_state": "", "mirror_last_error": "",
		"updated_at": time.Now().UTC(),
	}
	if err := m.store.UpdateGitRepositoryColumns(ctx, repo.ID, cols); err != nil {
		return err
	}
	repo.Type = v1.GitRepoType_GIT_REPO_TYPE_STANDARD
	repo.MirrorConfig, repo.MirrorState, repo.MirrorLastError = "", "", ""
	return nil
}

// Turns on release mirroring and creates linked image mirrors
func (m *Monitor) SetupCompositeMirror(ctx context.Context, repo *db.GitRepository, releases bool, images []string) error {
	if m.git == nil {
		return ErrGitUnavailable
	}
	cfg, err := m.Config(repo.MirrorConfig)
	if err != nil {
		return err
	}
	up, err := parseGitUpstream(cfg.GetUpstream())
	if err != nil {
		return err
	}
	var errs []error
	if releases {
		if up.Forge == "" {
			if forge, err := m.detectForge(ctx, up); err == nil {
				up.Forge = forge
			}
		}
		if forgeDriverFor(up.Forge) == nil {
			errs = append(errs, fmt.Errorf("releases: %w: cannot tell which forge hosts %s", ErrInvalid, up.Host))
		} else if err := m.EnableReleaseMirroring(ctx, repo); err != nil {
			errs = append(errs, fmt.Errorf("releases: %w", err))
		}
	}
	for _, ref := range images {
		if err := m.createImageMirror(ctx, repo, cfg, up, ref, false); err != nil {
			errs = append(errs, fmt.Errorf("image %s: %w", ref, err))
		}
	}
	return errors.Join(errs...)
}

// Creates linked image repositories that copy the upstream once and then stand alone
func (m *Monitor) SetupImportImages(ctx context.Context, repo *db.GitRepository, images []string) error {
	if m.git == nil {
		return ErrGitUnavailable
	}
	cfg, err := m.Config(repo.MirrorConfig)
	if err != nil {
		return err
	}
	up, err := parseGitUpstream(cfg.GetUpstream())
	if err != nil {
		return err
	}
	if up.Forge == "" {
		if forge, err := m.detectForge(ctx, up); err == nil {
			up.Forge = forge
		}
	}
	var errs []error
	for _, ref := range images {
		if err := m.createImageMirror(ctx, repo, cfg, up, ref, true); err != nil {
			errs = append(errs, fmt.Errorf("image %s: %w", ref, err))
		}
	}
	return errors.Join(errs...)
}

func releaseTypeFor(forge string) v1.ArtifactRepoType {
	switch forge {
	case forgeGitHub:
		return v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITHUB_RELEASES
	case forgeGitLab:
		return v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITLAB_RELEASES
	case forgeGitea:
		return v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITEA_RELEASES
	}
	return v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_UNSPECIFIED
}

func forgeForType(t v1.ArtifactRepoType) string {
	switch t {
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITHUB_RELEASES:
		return forgeGitHub
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITLAB_RELEASES:
		return forgeGitLab
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITEA_RELEASES:
		return forgeGitea
	}
	return ""
}

func (m *Monitor) createImageMirror(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig, up gitUpstream, entry string, once bool) error {
	if m.oci == nil {
		return fmt.Errorf("%w: registry mirroring is unavailable", ErrInvalid)
	}
	upstream, local, err := resolveImageRef(up, repo.Name, entry)
	if err != nil {
		return err
	}
	existing, err := m.store.GetRepository(ctx, repo.Namespace, local)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	ic := &v1.MirrorConfig{
		Upstream:            upstream,
		SyncIntervalMinutes: cfg.GetSyncIntervalMinutes(),
		Paused:              cfg.GetPaused(),
	}
	if cfg.GetAuthToken() != "" {
		ic.AuthToken = cfg.AuthToken
		ic.Username = registryUser(up, cfg)
	}
	if err := m.ValidateRegistryMirror(ctx, ic); err != nil {
		return err
	}
	raw, err := EncodeConfig(ic)
	if err != nil {
		return err
	}
	gitID := repo.ID
	row := &db.Repository{
		ID:             uuid.New().String(),
		Namespace:      repo.Namespace,
		Name:           local,
		Description:    "Mirror of " + upstream,
		OwnerID:        repo.OwnerID,
		IsPrivate:      repo.IsPrivate,
		IsOrgNamespace: repo.IsOrgNamespace,
		Type:           v1.RepositoryType_REPOSITORY_TYPE_MIRROR,
		MirrorConfig:   raw,
		GitRepoID:      &gitID,
	}
	if once {
		row.Description = "Imported from " + upstream
		row.MirrorState = SyncState{ImportOnce: true}.Encode()
	}
	if err := m.store.CreateRepository(ctx, row); err != nil {
		return err
	}
	_ = m.SyncImageRepoNow(row)
	return nil
}

// Registry login that pairs with a forge token
func registryUser(up gitUpstream, cfg *v1.MirrorConfig) string {
	if u := cfg.GetUsername(); u != "" {
		return u
	}
	switch up.Forge {
	case forgeGitHub, forgeGitea:
		return up.Owner
	}
	return ""
}

// Maps an image entry to upstream reference and local name
func resolveImageRef(up gitUpstream, rootName, entry string) (string, string, error) {
	e := strings.Trim(strings.TrimSpace(entry), "/")
	if e == "" {
		return "", "", fmt.Errorf("%w: empty image reference", ErrInvalid)
	}
	registry := up.registryHost()
	first, rest, hasSlash := strings.Cut(e, "/")
	var p string
	switch {
	case strings.ContainsAny(first, ".:") || first == "localhost":
		if !hasSlash || rest == "" {
			return "", "", fmt.Errorf("%w: image reference %q names only a registry", ErrInvalid, entry)
		}
		registry, p = first, rest
	case strings.EqualFold(e, up.Path) || strings.HasPrefix(strings.ToLower(e), strings.ToLower(up.Path)+"/"):
		p = e
	default:
		p = up.Path + "/" + e
	}
	// Tags and digests never become part of the name
	if i := strings.LastIndex(p, "/"); true {
		last := p[i+1:]
		if j := strings.IndexAny(last, ":@"); j >= 0 {
			p = p[:i+1] + last[:j]
		}
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return "", "", fmt.Errorf("%w: image reference %q has no path", ErrInvalid, entry)
	}
	lower, prefix := strings.ToLower(p), strings.ToLower(up.Path)
	var local string
	switch {
	case lower == prefix:
		local = rootName
	case strings.HasPrefix(lower, prefix+"/"):
		local = rootName + "/" + p[len(up.Path)+1:]
	default:
		local = rootName + "/" + p[strings.LastIndex(p, "/")+1:]
	}
	return registry + "/" + p, local, nil
}
