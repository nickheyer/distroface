// Package agents runs model driven assistants against repositories through opaque inference connectors
package agents

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Session lifecycle states
const (
	StatusQueued          = "queued"
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusCompleted       = "completed"
)

// Conclusions of completed sessions
const (
	ConclusionSuccess        = "success"
	ConclusionFailure        = "failure"
	ConclusionCancelled      = "cancelled"
	ConclusionTimedOut       = "timed_out"
	ConclusionRejected       = "rejected"
	ConclusionBudgetExceeded = "budget_exceeded"
)

// Agent kinds
const (
	KindReview = "review"
	KindCode   = "code"
	KindChat   = "chat"
	KindTriage = "triage"
	KindCustom = "custom"
)

// Approval policies
const (
	ApprovalNone      = "none"
	ApprovalCommits   = "commits"
	ApprovalAllWrites = "all_writes"
)

// Definition sources
const (
	SourceDatabase = "database"
	SourceFile     = "file"
)

// Session triggers
const (
	TriggerManual      = "manual"
	TriggerPullRequest = "pull_request"
	TriggerIssue       = "issue"
	TriggerLabel       = "label"
	TriggerMention     = "mention"
	TriggerReview      = "review"
	TriggerWorkflowRun = "workflow_run"
	TriggerContinue    = "continue"
	TriggerRerun       = "rerun"
)

// Domain errors the rpc layer maps onto codes
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid argument")
	ErrDenied   = errors.New("access denied")
	ErrConflict = errors.New("conflict")
	ErrDisabled = errors.New("agents are disabled")
	ErrBudget   = errors.New("budget exceeded")
)

// Manager owns connectors, agent definitions, bot identities, and sessions
type Manager struct {
	store    *stores.Store
	git      *gitrepo.Manager
	forge    *forge.Manager
	actions  *actions.Manager
	auth     *auth.Manager
	res      *settings.Resolver
	log      *logger.Logger
	hub      *eventHub
	runMu    sync.Mutex
	running  map[string]context.CancelFunc
	kickMu   sync.Mutex
	kicking  map[string]bool
	limMu    sync.Mutex
	limiters map[string]*rateLimiter
	botMu    sync.Mutex
	botIDs   map[string]bool
	botAt    time.Time
	windows  sync.Map        // Context windows from connector catalogs
	base     context.Context // Base context for sessions
}

func NewManager(store *stores.Store, git *gitrepo.Manager, fm *forge.Manager, am *actions.Manager, authMgr *auth.Manager, res *settings.Resolver, log *logger.Logger) *Manager {
	return &Manager{
		store: store, git: git, forge: fm, actions: am, auth: authMgr, res: res, log: log,
		hub:      newEventHub(),
		running:  map[string]context.CancelFunc{},
		kicking:  map[string]bool{},
		limiters: map[string]*rateLimiter{},
		botIDs:   map[string]bool{},
		base:     context.Background(),
	}
}

// Starts the session dispatcher, resumes work interrupted by a restart, and the retention loop
func (m *Manager) Start(ctx context.Context) {
	m.base = ctx
	go m.resume(ctx)
	go m.retentionLoop(ctx)
	go m.reaperLoop(ctx)
}

func (m *Manager) Store() *stores.Store { return m.store }

// Effective agent settings of a repository
func (m *Manager) Settings(ctx context.Context, repo *db.GitRepository) *v1.GitAgentsSettings {
	return m.git.GitSettings(ctx, repo).GetAgents()
}

// Effective agent settings of a scope, system when the kind is unknown
func (m *Manager) ScopeSettings(ctx context.Context, scopeKind, scopeID string) *v1.GitAgentsSettings {
	switch scopeKind {
	case db.ActionsScopeOrg:
		return m.res.Org(ctx, scopeID).GetGit().GetAgents()
	case db.ActionsScopeRepo:
		return m.res.Repo(ctx, scopeID).GetGit().GetAgents()
	}
	return m.res.System(ctx).GetGit().GetAgents()
}

// Enabled reports whether sessions may run for a repository
func (m *Manager) Enabled(ctx context.Context, repo *db.GitRepository) bool {
	if repo == nil || !m.git.Enabled(ctx) {
		return false
	}
	return gitrepo.UnitEnabled(repo, db.GitUnitAgents) && m.Settings(ctx, repo).GetEnabled()
}

// PolicyEnabled reports whether the instance or org policy allows agents, unit aside
func (m *Manager) PolicyEnabled(ctx context.Context, repo *db.GitRepository) bool {
	return repo != nil && m.git.Enabled(ctx) && m.Settings(ctx, repo).GetEnabled()
}

// Organization id of an org owned repository, empty otherwise
func (m *Manager) repoOrgID(ctx context.Context, repo *db.GitRepository) string {
	if repo == nil || !repo.IsOrgNamespace {
		return ""
	}
	org, err := m.store.GetOrganization(ctx, repo.Namespace)
	if err != nil || org == nil {
		return ""
	}
	return org.ID
}

// Session page url for links and notifications
func (m *Manager) sessionURL(ctx context.Context, repo *db.GitRepository, sessionID string) string {
	return m.git.HTMLURL(ctx, repo) + "/agents/sessions/" + sessionID
}

func nowPtr() *time.Time {
	t := time.Now().UTC()
	return &t
}

// ── Proto mapping ────────────────────────────────────────────────────────

func ProtoStatus(s string) v1.AgentSessionStatus {
	switch s {
	case StatusQueued:
		return v1.AgentSessionStatus_AGENT_SESSION_STATUS_QUEUED
	case StatusRunning:
		return v1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING
	case StatusWaitingApproval:
		return v1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_APPROVAL
	case StatusCompleted:
		return v1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED
	}
	return v1.AgentSessionStatus_AGENT_SESSION_STATUS_UNSPECIFIED
}

func StatusFromProto(s v1.AgentSessionStatus) string {
	switch s {
	case v1.AgentSessionStatus_AGENT_SESSION_STATUS_QUEUED:
		return StatusQueued
	case v1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING:
		return StatusRunning
	case v1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_APPROVAL:
		return StatusWaitingApproval
	case v1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED:
		return StatusCompleted
	}
	return ""
}

func ProtoConclusion(s string) v1.AgentSessionConclusion {
	switch s {
	case ConclusionSuccess:
		return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_SUCCESS
	case ConclusionFailure:
		return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_FAILURE
	case ConclusionCancelled:
		return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_CANCELLED
	case ConclusionTimedOut:
		return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_TIMED_OUT
	case ConclusionRejected:
		return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_REJECTED
	case ConclusionBudgetExceeded:
		return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_BUDGET_EXCEEDED
	}
	return v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_UNSPECIFIED
}

func ConclusionFromProto(c v1.AgentSessionConclusion) string {
	switch c {
	case v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_SUCCESS:
		return ConclusionSuccess
	case v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_FAILURE:
		return ConclusionFailure
	case v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_CANCELLED:
		return ConclusionCancelled
	case v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_TIMED_OUT:
		return ConclusionTimedOut
	case v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_REJECTED:
		return ConclusionRejected
	case v1.AgentSessionConclusion_AGENT_SESSION_CONCLUSION_BUDGET_EXCEEDED:
		return ConclusionBudgetExceeded
	}
	return ""
}

func ProtoKind(k string) v1.AgentKind {
	switch k {
	case KindReview:
		return v1.AgentKind_AGENT_KIND_REVIEW
	case KindCode:
		return v1.AgentKind_AGENT_KIND_CODE
	case KindChat:
		return v1.AgentKind_AGENT_KIND_CHAT
	case KindTriage:
		return v1.AgentKind_AGENT_KIND_TRIAGE
	case KindCustom:
		return v1.AgentKind_AGENT_KIND_CUSTOM
	}
	return v1.AgentKind_AGENT_KIND_UNSPECIFIED
}

func KindFromProto(k v1.AgentKind) string {
	switch k {
	case v1.AgentKind_AGENT_KIND_REVIEW:
		return KindReview
	case v1.AgentKind_AGENT_KIND_CODE:
		return KindCode
	case v1.AgentKind_AGENT_KIND_CHAT:
		return KindChat
	case v1.AgentKind_AGENT_KIND_TRIAGE:
		return KindTriage
	case v1.AgentKind_AGENT_KIND_CUSTOM:
		return KindCustom
	}
	return ""
}

func ProtoApproval(a string) v1.AgentApprovalPolicy {
	switch a {
	case ApprovalNone:
		return v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_NONE
	case ApprovalCommits:
		return v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_COMMITS
	case ApprovalAllWrites:
		return v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_ALL_WRITES
	}
	return v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_UNSPECIFIED
}

func ApprovalFromProto(a v1.AgentApprovalPolicy) string {
	switch a {
	case v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_NONE:
		return ApprovalNone
	case v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_COMMITS:
		return ApprovalCommits
	case v1.AgentApprovalPolicy_AGENT_APPROVAL_POLICY_ALL_WRITES:
		return ApprovalAllWrites
	}
	return ""
}

func ProtoSource(s string) v1.AgentSource {
	switch s {
	case SourceDatabase:
		return v1.AgentSource_AGENT_SOURCE_DATABASE
	case SourceFile:
		return v1.AgentSource_AGENT_SOURCE_FILE
	}
	return v1.AgentSource_AGENT_SOURCE_UNSPECIFIED
}

func ProtoApprovalStatus(s string) v1.AgentApprovalStatus {
	switch s {
	case approvalPending:
		return v1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING
	case approvalApproved:
		return v1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED
	case approvalRejected:
		return v1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED
	}
	return v1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED
}
