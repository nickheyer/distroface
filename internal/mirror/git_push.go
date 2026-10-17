package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Quiet period after a client push before mirroring
const pushDebounce = 30 * time.Second

// Push bookkeeping plus the pinned remote host key
type pushMirrorState struct {
	SyncState
	HostKey string `json:"host_key,omitempty"`
}

func parsePushMirrorState(raw string) pushMirrorState {
	var st pushMirrorState
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s pushMirrorState) encode() string {
	if s == (pushMirrorState{}) {
		return ""
	}
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// Post receive subscriber, every ref change may need pushing out
func (m *Monitor) OnRefUpdate(_ context.Context, ev gitrepo.RefEvent) {
	if ev.Wiki {
		return
	}
	m.SchedulePushMirrors(ev.Repo)
}

// Schedules a push mirror sync after a push, debounced
func (m *Monitor) SchedulePushMirrors(repo *db.GitRepository) {
	if m.git == nil || repo == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.pushTimers[repo.ID]; ok {
		t.Reset(pushDebounce)
		return
	}
	id := repo.ID
	m.pushTimers[id] = time.AfterFunc(pushDebounce, func() {
		m.mu.Lock()
		delete(m.pushTimers, id)
		m.mu.Unlock()
		m.runPushMirrors(id)
	})
}

// Pushes every sync on push mirror of a repository
func (m *Monitor) runPushMirrors(repoID string) {
	ctx := m.baseCtx
	if !m.mirroringEnabled(ctx) {
		return
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, repoID)
	if err != nil || repo == nil {
		return
	}
	mirrors, err := m.store.ListGitPushMirrors(ctx, repoID)
	if err != nil {
		m.log.Error("push mirror list for %s/%s: %v", repo.Namespace, repo.Name, err)
		return
	}
	m.refreshLimits(ctx)
	now := time.Now()
	for _, pm := range mirrors {
		if !pm.SyncOnPush || parsePushMirrorState(pm.State).CoolingDown(now) {
			continue
		}
		key := pushKey(pm.ID)
		if !m.try(key) {
			continue
		}
		go func(pm *db.GitPushMirror) {
			defer m.done(key)
			m.execPushSync(ctx, repo, pm)
		}(pm)
	}
}

func (m *Monitor) execPushSync(ctx context.Context, repo *db.GitRepository, pm *db.GitPushMirror) {
	key := pushKey(pm.ID)
	ev := Event{
		Kind:      "git_push",
		RepoID:    pm.ID,
		Namespace: repo.Namespace,
		Name:      repo.Name,
		Private:   repo.IsPrivate,
		OwnerID:   repo.OwnerID,
	}
	state := parsePushMirrorState(pm.State)
	runCtx, cancel := context.WithTimeout(ctx, currentLimits().SyncTimeout)
	m.armCancel(key, cancel)
	m.beginSync(key, ev)

	syncErr := m.pushToMirror(runCtx, repo, pm, &state)
	cancel()
	if m.disarmCancel(key) && syncErr != nil {
		syncErr = ErrSyncStopped
	}

	var msg string
	state.SyncState, msg = nextState(state.SyncState, syncErr)
	if errors.Is(syncErr, ErrSyncStopped) {
		m.log.Info("push mirror %s for %s/%s stopped by user", pm.RemoteURL, repo.Namespace, repo.Name)
	} else if syncErr != nil {
		m.log.Error("push mirror %s for %s/%s: %v", pm.RemoteURL, repo.Namespace, repo.Name, syncErr)
	}
	encoded := state.encode()
	if err := m.store.SetGitPushMirrorStatus(statusCtx(ctx), pm.ID, time.Now().UTC(), msg, encoded); err != nil {
		m.log.Error("push mirror status update for %s: %v", pm.ID, err)
	}
	pm.State = encoded
	m.endSync(key, ev, syncErr)
}

// Force pushes every branch and tag to the remote
func (m *Monitor) pushToMirror(ctx context.Context, repo *db.GitRepository, pm *db.GitPushMirror, state *pushMirrorState) error {
	u, err := gitrepo.ValidateUpstreamURL(pm.RemoteURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	auth := gitrepo.RemoteAuth{HTTPClient: m.client, Dialer: m.dial}
	switch {
	case pm.UseSSHKey:
		keys := parseGitMirrorState(repo.MirrorState)
		if keys.SSHPrivateKey == "" {
			return fmt.Errorf("%w: generate the repository ssh key first", ErrInvalid)
		}
		pem, err := m.openMirrorKey(keys.SSHPrivateKey)
		if err != nil {
			return fmt.Errorf("%w: stored ssh key unreadable, generate it again", ErrInvalid)
		}
		auth.SSHKeyPEM = pem
		auth.HostKey = state.HostKey
		auth.OnHostKey = func(fp string) { state.HostKey = fp }
	case u.Scheme == "ssh":
		return fmt.Errorf("%w: ssh remotes need the repository ssh key", ErrInvalid)
	case pm.AuthToken != "":
		auth.Username, auth.Password = gitBasicAuth(forgeForHost(u.Hostname()), pm.Username, m.openToken(pm.AuthToken))
	}
	return classifyGitErr(m.git.PushRemote(ctx, repo, pm.RemoteURL, mirrorRefspecs, auth))
}
