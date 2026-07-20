package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// ErrSyncInFlight rejects a manual sync while one is running
var ErrSyncInFlight = errors.New("a sync for this repository is already running")

// ErrDisabled rejects manual syncs while mirroring is switched off
var ErrDisabled = errors.New("mirroring is disabled by the administrator")

// ErrNoActiveSync rejects a stop when nothing is running
var ErrNoActiveSync = errors.New("no sync is running for this repository")

// ErrSyncStopped marks a sync an operator cancelled
var ErrSyncStopped = errors.New("sync stopped by user")

// CooldownError rejects a manual sync during an upstream rate limit
type CooldownError struct {
	Until time.Time
}

func (e *CooldownError) Error() string {
	return fmt.Sprintf("the upstream rate limited this server, next sync allowed after %s", e.Until.UTC().Format(time.RFC3339))
}

// Monitor polls mirror repos and pulls new upstream content
type Monitor struct {
	store     *stores.Store
	res       *settings.Resolver
	artifacts *artifacts.Manager
	oci       *ociSyncer
	log       *logger.Logger
	client    *http.Client

	baseCtx     context.Context
	mu          sync.Mutex
	running     bool
	inflight    map[string]bool
	cancels     map[string]context.CancelFunc
	stopped     map[string]bool
	activeSyncs map[string]Event
	events      *hub
}

func NewMonitor(store *stores.Store, res *settings.Resolver, mgr *artifacts.Manager, oci *ociSyncer, log *logger.Logger) *Monitor {
	allowPrivate := func() bool {
		return res.System(context.Background()).GetMirror().GetAllowPrivateNetworks()
	}
	pace := newPacer()
	client := &http.Client{
		Transport: &pacedTransport{inner: safeTransport(allowPrivate), pace: pace},
		Timeout:   0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("mirror: too many redirects")
			}
			// Go strips Authorization cross host, vendor tokens need the same
			if req.URL.Host != via[0].URL.Host {
				req.Header.Del("PRIVATE-TOKEN")
			}
			return nil
		},
	}
	if oci != nil {
		// Same pacing as artifact mirrors, hub abuse detection is touchy
		oci.upstreamTransport = &pacedTransport{inner: safeTransport(allowPrivate), pace: pace}
	}
	return &Monitor{
		store:     store,
		res:       res,
		artifacts: mgr,
		oci:       oci,
		log:       log,
		client:    client,
		baseCtx:     context.Background(),
		inflight:    make(map[string]bool),
		cancels:     make(map[string]context.CancelFunc),
		stopped:     make(map[string]bool),
		activeSyncs: make(map[string]Event),
		events:      newHub(),
	}
}

// Schedule polls every minute and syncs repos whose interval has lapsed
func (m *Monitor) Schedule(ctx context.Context) {
	m.baseCtx = ctx
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !m.res.System(ctx).GetMirror().GetEnabled() {
					continue
				}
				m.mu.Lock()
				if m.running {
					m.mu.Unlock()
					continue
				}
				m.running = true
				m.mu.Unlock()
				go func() {
					defer func() {
						m.mu.Lock()
						m.running = false
						m.mu.Unlock()
					}()
					m.sweep(ctx)
				}()
			}
		}
	}()
}

// Claims a repo for one sync, false when already claimed
func (m *Monitor) try(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inflight[key] {
		return false
	}
	m.inflight[key] = true
	return true
}

func (m *Monitor) done(key string) {
	m.mu.Lock()
	delete(m.inflight, key)
	m.mu.Unlock()
}

// Exposes the cancel so a stop request can reach it
func (m *Monitor) armCancel(key string, cancel context.CancelFunc) {
	m.mu.Lock()
	m.cancels[key] = cancel
	delete(m.stopped, key)
	m.mu.Unlock()
}

// Clears the cancel and reports whether an operator stopped it
func (m *Monitor) disarmCancel(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, key)
	wasStopped := m.stopped[key]
	delete(m.stopped, key)
	return wasStopped
}

func (m *Monitor) stopSync(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel, ok := m.cancels[key]
	if !ok {
		return ErrNoActiveSync
	}
	m.stopped[key] = true
	cancel()
	return nil
}

// Cancels the running sync for an image repo
func (m *Monitor) StopImageSync(repo *db.Repository) error {
	return m.stopSync("image:" + repo.ID)
}

// Cancels the running sync for an artifact repo
func (m *Monitor) StopArtifactSync(repo *db.ArtifactRepository) error {
	return m.stopSync(fmt.Sprintf("artifact:%d", repo.ID))
}

// Publishes the admin clamps so shared helpers see fresh values
func (m *Monitor) refreshLimits(ctx context.Context) Limits {
	l := LimitsFromSettings(m.res.System(ctx).GetMirror())
	setLimits(l)
	return l
}

func (m *Monitor) defaultInterval(ctx context.Context) time.Duration {
	minutes := m.res.System(ctx).GetMirror().GetDefaultIntervalMinutes()
	if minutes <= 0 {
		minutes = 60
	}
	return time.Duration(minutes) * time.Minute
}

func (m *Monitor) due(ctx context.Context, cfg *v1.MirrorConfig, lastSync *time.Time, state SyncState) bool {
	if cfg.GetPaused() {
		return false
	}
	if state.CoolingDown(time.Now()) {
		return false
	}
	if lastSync == nil {
		return true
	}
	interval := m.defaultInterval(ctx)
	if cfg.GetSyncIntervalMinutes() > 0 {
		interval = time.Duration(cfg.GetSyncIntervalMinutes()) * time.Minute
	}
	if floor := currentLimits().MinInterval; interval < floor {
		interval = floor
	}
	return time.Since(*lastSync) >= interval
}

func (m *Monitor) sweep(ctx context.Context) {
	lim := m.refreshLimits(ctx)
	var jobs []func()
	var wg sync.WaitGroup

	repos, err := m.store.ListMirrorArtifactRepositories(ctx, MirrorArtifactTypes)
	if err != nil {
		m.log.Error("mirror sweep artifact repo list: %v", err)
	}
	for _, repo := range repos {
		cfg, err := ParseConfig(repo.MirrorConfig)
		if err != nil {
			m.log.Error("mirror config for artifact repo %s/%s: %v", repo.Namespace, repo.Name, err)
			continue
		}
		if !m.due(ctx, cfg, repo.MirrorLastSync, ParseState(repo.MirrorState)) {
			continue
		}
		repo, cfg := repo, cfg
		jobs = append(jobs, func() {
			key := fmt.Sprintf("artifact:%d", repo.ID)
			if !m.try(key) {
				return
			}
			defer m.done(key)
			m.execArtifactSync(ctx, repo, cfg)
		})
	}

	if m.oci != nil {
		imageRepos, err := m.store.ListMirrorRepositories(ctx)
		if err != nil {
			m.log.Error("mirror sweep image repo list: %v", err)
		}
		for _, repo := range imageRepos {
			cfg, err := ParseConfig(repo.MirrorConfig)
			if err != nil {
				m.log.Error("mirror config for image repo %s/%s: %v", repo.Namespace, repo.Name, err)
				continue
			}
			if !m.due(ctx, cfg, repo.MirrorLastSync, ParseState(repo.MirrorState)) {
				continue
			}
			repo, cfg := repo, cfg
			jobs = append(jobs, func() {
				key := "image:" + repo.ID
				if !m.try(key) {
					return
				}
				defer m.done(key)
				m.execImageSync(ctx, repo, cfg)
			})
		}
	}

	sem := make(chan struct{}, lim.Workers)
	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(run func()) {
			defer func() { <-sem; wg.Done() }()
			run()
		}(job)
	}
	wg.Wait()
}

// SyncArtifactRepoNow starts an immediate background sync
func (m *Monitor) SyncArtifactRepoNow(repo *db.ArtifactRepository) error {
	cfg, err := ParseConfig(repo.MirrorConfig)
	if err != nil {
		return err
	}
	if driverFor(repo.Type) == nil {
		return fmt.Errorf("%w: repository is not a mirror", ErrInvalid)
	}
	if !m.res.System(m.baseCtx).GetMirror().GetEnabled() {
		return ErrDisabled
	}
	m.refreshLimits(m.baseCtx)
	if state := ParseState(repo.MirrorState); state.RateLimited && state.CoolingDown(time.Now()) {
		return &CooldownError{Until: state.CooldownUntil}
	}
	key := fmt.Sprintf("artifact:%d", repo.ID)
	if !m.try(key) {
		return ErrSyncInFlight
	}
	go func() {
		defer m.done(key)
		m.execArtifactSync(m.baseCtx, repo, cfg)
	}()
	return nil
}

// SyncImageRepoNow starts an immediate background sync
func (m *Monitor) SyncImageRepoNow(repo *db.Repository) error {
	if m.oci == nil || repo.Type != v1.RepositoryType_REPOSITORY_TYPE_MIRROR {
		return fmt.Errorf("%w: repository is not a mirror", ErrInvalid)
	}
	cfg, err := ParseConfig(repo.MirrorConfig)
	if err != nil {
		return err
	}
	if !m.res.System(m.baseCtx).GetMirror().GetEnabled() {
		return ErrDisabled
	}
	m.refreshLimits(m.baseCtx)
	if state := ParseState(repo.MirrorState); state.RateLimited && state.CoolingDown(time.Now()) {
		return &CooldownError{Until: state.CooldownUntil}
	}
	key := "image:" + repo.ID
	if !m.try(key) {
		return ErrSyncInFlight
	}
	go func() {
		defer m.done(key)
		m.execImageSync(m.baseCtx, repo, cfg)
	}()
	return nil
}

func (m *Monitor) execArtifactSync(ctx context.Context, repo *db.ArtifactRepository, cfg *v1.MirrorConfig) {
	key := fmt.Sprintf("artifact:%d", repo.ID)
	ev := Event{
		Kind:      "artifact",
		RepoID:    fmt.Sprintf("%d", repo.ID),
		Namespace: repo.Namespace,
		Name:      repo.Name,
		Private:   repo.IsPrivate,
		OwnerID:   repo.OwnerID,
	}
	state := ParseState(repo.MirrorState)
	runCtx, cancel := context.WithTimeout(ctx, currentLimits().SyncTimeout)
	m.armCancel(key, cancel)
	m.beginSync(key, ev)

	syncErr := m.syncArtifactRepo(runCtx, repo, cfg, &state)
	cancel()
	if m.disarmCancel(key) && syncErr != nil {
		syncErr = ErrSyncStopped
	}

	state, msg := nextState(state, syncErr)
	if errors.Is(syncErr, ErrSyncStopped) {
		m.log.Info("mirror sync for artifact repo %s/%s stopped by user", repo.Namespace, repo.Name)
	} else if syncErr != nil {
		m.log.Error("mirror sync for artifact repo %s/%s: %v", repo.Namespace, repo.Name, syncErr)
	}
	if err := m.store.SetArtifactRepoMirrorStatus(statusCtx(ctx), repo.ID, time.Now().UTC(), msg, state.Encode()); err != nil {
		m.log.Error("mirror status update for artifact repo %d: %v", repo.ID, err)
	}
	m.endSync(key, ev, syncErr)
}

func (m *Monitor) execImageSync(ctx context.Context, repo *db.Repository, cfg *v1.MirrorConfig) {
	key := "image:" + repo.ID
	ev := Event{
		Kind:      "image",
		RepoID:    repo.ID,
		Namespace: repo.Namespace,
		Name:      repo.Name,
		Private:   repo.IsPrivate,
		OwnerID:   repo.OwnerID,
	}
	state := ParseState(repo.MirrorState)
	runCtx, cancel := context.WithTimeout(ctx, currentLimits().SyncTimeout)
	m.armCancel(key, cancel)
	m.beginSync(key, ev)

	syncErr := m.oci.syncRepo(runCtx, repo, cfg, m.log)
	cancel()
	if m.disarmCancel(key) && syncErr != nil {
		syncErr = ErrSyncStopped
	}

	state, msg := nextState(state, syncErr)
	if errors.Is(syncErr, ErrSyncStopped) {
		m.log.Info("mirror sync for image repo %s/%s stopped by user", repo.Namespace, repo.Name)
	} else if syncErr != nil {
		m.log.Error("mirror sync for image repo %s/%s: %v", repo.Namespace, repo.Name, syncErr)
	}
	if err := m.store.SetRepositoryMirrorStatus(statusCtx(ctx), repo.ID, time.Now().UTC(), msg, state.Encode()); err != nil {
		m.log.Error("mirror status update for image repo %s: %v", repo.ID, err)
	}
	m.endSync(key, ev, syncErr)
}

// Status writes survive a cancelled sync context
func statusCtx(ctx context.Context) context.Context {
	if ctx.Err() == nil {
		return ctx
	}
	return context.Background()
}

// Folds a sync outcome into cooldown bookkeeping and a status line
func nextState(state SyncState, syncErr error) (SyncState, string) {
	switch until, limited := RetryAfter(syncErr); {
	case syncErr == nil:
		state.Failures = 0
		state.CooldownUntil = time.Time{}
		state.RateLimited = false
		return state, ""
	// Operator stops skip failure accounting and cooldowns
	case errors.Is(syncErr, ErrSyncStopped):
		return state, ErrSyncStopped.Error()
	case limited:
		state.RateLimited = true
		state.CooldownUntil = until
		return state, fmt.Sprintf("upstream rate limited, next attempt after %s", until.UTC().Format(time.RFC3339))
	default:
		state.Failures++
		state.RateLimited = false
		state.CooldownUntil = time.Now().Add(backoffFor(state.Failures))
		return state, truncate(syncErr.Error(), 1000)
	}
}

// Applies prerelease and depth filters
func filterReleases(rels []release, cfg *v1.MirrorConfig) []release {
	out := rels[:0]
	for _, r := range rels {
		if r.prerelease && !cfg.GetIncludePrereleases() {
			continue
		}
		out = append(out, r)
	}
	if depth := effectiveDepth(cfg); depth > 0 && len(out) > depth {
		out = out[:depth]
	}
	return out
}

func (m *Monitor) syncArtifactRepo(ctx context.Context, repo *db.ArtifactRepository, cfg *v1.MirrorConfig, state *SyncState) error {
	drv := driverFor(repo.Type)
	if drv == nil {
		return fmt.Errorf("unsupported artifact repo type %v", repo.Type)
	}
	list, err := drv.releases(ctx, m.client, cfg, state.ListETag)
	if err != nil {
		state.ListETag = ""
		return err
	}
	if list.notModified {
		return nil
	}
	state.ListETag = list.etag
	rels := filterReleases(list.releases, cfg)

	maxBytes := m.artifacts.EffectiveMaxFileSizeBytes(ctx, repo.Namespace)
	var errs []error
	synced := 0
	// Oldest first so ingest order matches release chronology
	for i := len(rels) - 1; i >= 0; i-- {
		rel := rels[i]
		// Unusable tags and names are permanent, skip without failing
		if artifacts.ValidateVersion(rel.version) != nil {
			continue
		}
		for _, a := range rel.assets {
			if !matchesPattern(cfg.GetPattern(), a.name) {
				continue
			}
			if artifacts.ValidatePath(a.name) != nil {
				continue
			}
			existing, err := m.store.GetArtifactByPathVersion(ctx, repo.ID, rel.version, a.name)
			if err == nil && existing != nil && (a.size == 0 || existing.Size == a.size) {
				continue
			}
			if maxBytes > 0 && a.size > maxBytes {
				errs = append(errs, fmt.Errorf("%s@%s exceeds the size limit", a.name, rel.version))
				continue
			}
			if err := m.ingestAsset(ctx, repo, cfg, rel.version, a); err != nil {
				errs = append(errs, fmt.Errorf("%s@%s: %w", a.name, rel.version, err))
				// A rate limit taints every further request, stop now
				if _, limited := RetryAfter(err); limited {
					state.ListETag = ""
					return errors.Join(errs...)
				}
				continue
			}
			synced++
		}
	}
	if synced > 0 {
		m.log.Info("mirror synced %d assets into %s/%s from %s", synced, repo.Namespace, repo.Name, cfg.GetUpstream())
	}
	if len(errs) > 0 {
		// Stale etag forces a full relisting so failures retry
		state.ListETag = ""
		return errors.Join(errs...)
	}
	return nil
}

// Streams one upstream asset into the blob store as an artifact
func (m *Monitor) ingestAsset(ctx context.Context, repo *db.ArtifactRepository, cfg *v1.MirrorConfig, version string, a asset) error {
	var lastErr error
	for _, src := range a.sources {
		err := m.downloadAsset(ctx, repo, cfg, version, a, src)
		if err == nil {
			return nil
		}
		if _, limited := RetryAfter(err); limited {
			return err
		}
		lastErr = err
		// Auth shaped failures fall through to the next source
		var ue *upstreamError
		if errors.As(err, &ue) {
			switch ue.status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
				continue
			}
		}
		return err
	}
	if lastErr == nil {
		return fmt.Errorf("no download source for %s", a.name)
	}
	return lastErr
}

func (m *Monitor) downloadAsset(ctx context.Context, repo *db.ArtifactRepository, cfg *v1.MirrorConfig, version string, a asset, src assetSource) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return err
	}
	for k, v := range src.headers {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := classifyResponse(resp, src.url); err != nil {
		return err
	}

	blobs := m.artifacts.Blobs()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		return err
	}
	if _, err := blobs.AppendChunk(uploadID, resp.Body); err != nil {
		blobs.CancelUpload(uploadID)
		return err
	}

	meta, _ := json.Marshal(map[string]string{"mirror_url": src.url})
	props := map[string]string{
		"mirror.source":   sourceLabel(repo.Type),
		"mirror.upstream": cfg.GetUpstream(),
	}
	_, err = m.artifacts.CompleteUpload(ctx, repo, uploadID, version, a.name, string(meta), props)
	return err
}

func sourceLabel(t v1.ArtifactRepoType) string {
	switch t {
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITHUB_RELEASES:
		return "github-releases"
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITLAB_RELEASES:
		return "gitlab-releases"
	case v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_GITEA_RELEASES:
		return "gitea-releases"
	default:
		return "unknown"
	}
}

// Rejects intervals under the admin floor
func (m *Monitor) checkInterval(ctx context.Context, cfg *v1.MirrorConfig) error {
	lim := m.refreshLimits(ctx)
	if lim.MinInterval <= 0 || cfg.GetSyncIntervalMinutes() <= 0 {
		return nil
	}
	if time.Duration(cfg.GetSyncIntervalMinutes())*time.Minute < lim.MinInterval {
		return fmt.Errorf("%w: sync interval must be at least %d minutes", ErrInvalid, int(lim.MinInterval/time.Minute))
	}
	return nil
}

// ValidateArtifactMirror rejects bad configs and probes the upstream live
func (m *Monitor) ValidateArtifactMirror(ctx context.Context, t v1.ArtifactRepoType, cfg *v1.MirrorConfig) error {
	drv := driverFor(t)
	if drv == nil {
		return fmt.Errorf("%w: repo type %v does not support mirroring", ErrInvalid, t)
	}
	if err := validateCommon(cfg); err != nil {
		return err
	}
	if err := m.checkInterval(ctx, cfg); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return drv.validate(probeCtx, m.client, cfg)
}

// ValidateRegistryMirror rejects bad configs and probes the oci upstream live
func (m *Monitor) ValidateRegistryMirror(ctx context.Context, cfg *v1.MirrorConfig) error {
	if m.oci == nil {
		return fmt.Errorf("%w: registry mirroring is unavailable", ErrInvalid)
	}
	if err := validateCommon(cfg); err != nil {
		return err
	}
	if err := m.checkInterval(ctx, cfg); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return m.oci.validate(probeCtx, cfg)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
