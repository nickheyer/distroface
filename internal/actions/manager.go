package actions

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Run and job lifecycle states
const (
	StatusWaiting   = "waiting"
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
)

// Conclusions of completed runs, jobs, and steps
const (
	ConclusionSuccess   = "success"
	ConclusionFailure   = "failure"
	ConclusionCancelled = "cancelled"
	ConclusionSkipped   = "skipped"
	ConclusionTimedOut  = "timed_out"
	ConclusionNeutral   = "neutral"
)

// Domain errors the rpc layer maps onto codes
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid argument")
	ErrDenied   = errors.New("access denied")
	ErrConflict = errors.New("conflict")
	ErrDisabled = errors.New("actions are disabled")
	ErrTooLarge = errors.New("too large")
)

const (
	runnerOnlineWindow         = 2 * time.Minute
	runnerStaleWindow          = 10 * time.Minute
	droppedJobGrace            = 2 * time.Minute
	jobTokenDays         int32 = 1
	maxTaskWait                = 30 * time.Second
	defaultJobTimeout          = 360
	registrationTokenTTL       = time.Hour
	workflowFileCap            = 1 << 20
)

// Manager owns workflows, runs, jobs, runners, secrets, and the runner protocol
type Manager struct {
	store     *stores.Store
	git       *gitrepo.Manager
	forge     *forge.Manager
	artifacts *artifacts.Manager
	auth      *auth.Manager
	res       *settings.Resolver
	log       *logger.Logger

	cancelMu        sync.Mutex
	cancelRequested map[string]struct{}

	wakeMu sync.Mutex
	wake   chan struct{}
	// Bumps whenever queued work may have changed
	queueGen atomic.Uint64

	schedMu    sync.Mutex
	schedCache map[string]*scheduleEntry

	statusMu  sync.Mutex
	jobStatus map[string]jobStatusEntry
}

func NewManager(store *stores.Store, git *gitrepo.Manager, fm *forge.Manager, am *artifacts.Manager, authMgr *auth.Manager, res *settings.Resolver, log *logger.Logger) *Manager {
	return &Manager{
		store: store, git: git, forge: fm, artifacts: am, auth: authMgr, res: res, log: log,
		cancelRequested: map[string]struct{}{},
		wake:            make(chan struct{}),
		schedCache:      map[string]*scheduleEntry{},
		jobStatus:       map[string]jobStatusEntry{},
	}
}

// Starts the scheduler, stale job reaper, and retention loops
func (m *Manager) Start(ctx context.Context) {
	go m.scheduleLoop(ctx)
	go m.reaperLoop(ctx)
	go m.retentionLoop(ctx)
}

// Enqueues workflow runs for push and tag events
func (m *Manager) OnRefUpdate(ctx context.Context, ev gitrepo.RefEvent) {
	if ev.Repo != nil && !ev.Wiki {
		m.forgetSchedules(ev.Repo.ID)
	}
	m.onRefUpdate(ctx, ev)
}

// Enqueues workflow runs for forge events such as pull_request and issues
func (m *Manager) Trigger(ctx context.Context, repo *db.GitRepository, event, action, ref, sha string, actor *auth.AuthenticatedUser, payload map[string]any) {
	m.trigger(ctx, repo, event, action, ref, sha, actor, payload)
}

// Effective actions settings of a repository
func (m *Manager) Settings(ctx context.Context, repo *db.GitRepository) *v1.GitActionsSettings {
	return m.git.GitSettings(ctx, repo).GetActions()
}

// Enabled reports whether workflows may run for a repository
func (m *Manager) Enabled(ctx context.Context, repo *db.GitRepository) bool {
	if repo == nil || !m.git.Enabled(ctx) {
		return false
	}
	return gitrepo.UnitEnabled(repo, db.GitUnitActions) && m.Settings(ctx, repo).GetEnabled()
}

// Wakes every long polling runner after new work appears
func (m *Manager) notifyRunners() {
	m.queueGen.Add(1)
	m.wakeMu.Lock()
	close(m.wake)
	m.wake = make(chan struct{})
	m.wakeMu.Unlock()
}

// Queue generation, unchanged means a rescan finds the same
func (m *Manager) queueGeneration() uint64 {
	return m.queueGen.Load()
}

func (m *Manager) wakeChan() <-chan struct{} {
	m.wakeMu.Lock()
	defer m.wakeMu.Unlock()
	return m.wake
}

// Decodes a stored json string list
func StringList(raw string) []string { return decodeStrings(raw) }

func (m *Manager) requestCancel(jobID string) {
	m.cancelMu.Lock()
	m.cancelRequested[jobID] = struct{}{}
	m.cancelMu.Unlock()
}

func (m *Manager) clearCancel(jobID string) {
	m.cancelMu.Lock()
	delete(m.cancelRequested, jobID)
	m.cancelMu.Unlock()
}

// Cancellation survives restarts through the run conclusion marker
func (m *Manager) cancelPending(ctx context.Context, job *db.ActionsJob) bool {
	m.cancelMu.Lock()
	_, ok := m.cancelRequested[job.ID]
	m.cancelMu.Unlock()
	if ok {
		return true
	}
	run, err := m.store.GetActionsRunByID(ctx, job.RunID)
	if err != nil || run == nil {
		return false
	}
	return run.Conclusion == ConclusionCancelled && run.Status != StatusCompleted
}

// Run page url for links and statuses
func (m *Manager) runURL(ctx context.Context, repo *db.GitRepository, runID string) string {
	return m.git.HTMLURL(ctx, repo) + "/actions/runs/" + runID
}

func (m *Manager) jobURL(ctx context.Context, repo *db.GitRepository, runID, jobID string) string {
	return m.runURL(ctx, repo, runID) + "/jobs/" + jobID
}

func nowPtr() *time.Time {
	t := time.Now().UTC()
	return &t
}

// ProtoStatus maps a stored status onto the enum
func ProtoStatus(s string) v1.ActionsStatus {
	switch s {
	case StatusWaiting:
		return v1.ActionsStatus_ACTIONS_STATUS_WAITING
	case StatusQueued:
		return v1.ActionsStatus_ACTIONS_STATUS_QUEUED
	case StatusRunning:
		return v1.ActionsStatus_ACTIONS_STATUS_RUNNING
	case StatusCompleted:
		return v1.ActionsStatus_ACTIONS_STATUS_COMPLETED
	}
	return v1.ActionsStatus_ACTIONS_STATUS_UNSPECIFIED
}

// StatusFromProto maps the enum onto a stored status
func StatusFromProto(s v1.ActionsStatus) string {
	switch s {
	case v1.ActionsStatus_ACTIONS_STATUS_WAITING:
		return StatusWaiting
	case v1.ActionsStatus_ACTIONS_STATUS_QUEUED:
		return StatusQueued
	case v1.ActionsStatus_ACTIONS_STATUS_RUNNING:
		return StatusRunning
	case v1.ActionsStatus_ACTIONS_STATUS_COMPLETED:
		return StatusCompleted
	}
	return ""
}

// ProtoConclusion maps a stored conclusion onto the enum
func ProtoConclusion(s string) v1.ActionsConclusion {
	switch s {
	case ConclusionSuccess:
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS
	case ConclusionFailure:
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE
	case ConclusionCancelled:
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED
	case ConclusionSkipped:
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED
	case ConclusionTimedOut:
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT
	case ConclusionNeutral:
		return v1.ActionsConclusion_ACTIONS_CONCLUSION_NEUTRAL
	}
	return v1.ActionsConclusion_ACTIONS_CONCLUSION_UNSPECIFIED
}

// ConclusionFromProto maps the enum onto a stored conclusion
func ConclusionFromProto(c v1.ActionsConclusion) string {
	switch c {
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS:
		return ConclusionSuccess
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE:
		return ConclusionFailure
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_CANCELLED:
		return ConclusionCancelled
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED:
		return ConclusionSkipped
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_TIMED_OUT:
		return ConclusionTimedOut
	case v1.ActionsConclusion_ACTIONS_CONCLUSION_NEUTRAL:
		return ConclusionNeutral
	}
	return ""
}
