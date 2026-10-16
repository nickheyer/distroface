package forge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Manager owns issues, pulls, reviews, labels, milestones, projects,
// notifications, releases, and wiki business logic
type Manager struct {
	store *stores.Store
	git   *gitrepo.Manager
	mail  *mailer.Service
	res   *settings.Resolver
	log   *logger.Logger

	trigger TriggerFunc
}

func NewManager(store *stores.Store, git *gitrepo.Manager, mail *mailer.Service, res *settings.Resolver, log *logger.Logger) *Manager {
	return &Manager{store: store, git: git, mail: mail, res: res, log: log}
}

func (m *Manager) Store() *stores.Store         { return m.store }
func (m *Manager) Git() *gitrepo.Manager        { return m.git }
func (m *Manager) Mail() *mailer.Service        { return m.mail }
func (m *Manager) Settings() *settings.Resolver { return m.res }
func (m *Manager) Log() *logger.Logger          { return m.log }

// Timeline event kinds stored as typed comments
const (
	EventComment            = "comment"
	EventLabeled            = "labeled"
	EventUnlabeled          = "unlabeled"
	EventAssigned           = "assigned"
	EventUnassigned         = "unassigned"
	EventMilestoned         = "milestoned"
	EventDemilestoned       = "demilestoned"
	EventClosed             = "closed"
	EventReopened           = "reopened"
	EventReferenced         = "referenced"
	EventRenamed            = "renamed"
	EventLocked             = "locked"
	EventUnlocked           = "unlocked"
	EventPinned             = "pinned"
	EventUnpinned           = "unpinned"
	EventDueDate            = "due_date"
	EventDependencyAdded    = "dependency_added"
	EventDependencyRemoved  = "dependency_removed"
	EventBlockerClosed      = "blocker_closed"
	EventBlockerReopened    = "blocker_reopened"
	EventTimeTracked        = "time_tracked"
	EventReviewRequested    = "review_requested"
	EventReviewRemoved      = "review_request_removed"
	EventReviewed           = "reviewed"
	EventHeadPushed         = "head_pushed"
	EventForcePushed        = "force_pushed"
	EventMerged             = "merged"
	EventAutoMergeArmed     = "auto_merge_armed"
	EventAutoMergeCancelled = "auto_merge_cancelled"
	EventConvertedToDraft   = "converted_to_draft"
	EventReadyForReview     = "ready_for_review"
	EventBaseChanged        = "base_changed"
	EventBranchDeleted      = "branch_deleted"
	EventBranchRestored     = "branch_restored"
	EventProjectAdded       = "project_added"
	EventProjectMoved       = "project_moved"
	EventProjectRemoved     = "project_removed"
)

// Appends a timeline event to an issue, payload is stored as json
func (m *Manager) AddEvent(ctx context.Context, issue *db.GitIssue, actor *auth.AuthenticatedUser, kind string, payload any) (*db.GitComment, error) {
	raw := "{}"
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			raw = string(b)
		}
	}
	c := &db.GitComment{
		ID:        uuid.New().String(),
		IssueID:   issue.ID,
		Type:      kind,
		Payload:   raw,
		CreatedAt: time.Now(),
	}
	if actor != nil {
		c.AuthorID = actor.ID
	}
	if err := m.store.CreateGitComment(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Enqueues workflow runs for forge events, installed by the actions manager
type TriggerFunc func(ctx context.Context, repo *db.GitRepository, event, action, ref, sha string, actor *auth.AuthenticatedUser, payload map[string]any)

// Installs the actions trigger, set once at startup
func (m *Manager) SetTrigger(fn TriggerFunc) { m.trigger = fn }

// Fires a forge event into actions when a trigger is installed
func (m *Manager) Trigger(ctx context.Context, repo *db.GitRepository, event, action, ref, sha string, actor *auth.AuthenticatedUser, payload map[string]any) {
	if m.trigger != nil {
		m.trigger(ctx, repo, event, action, ref, sha, actor, payload)
	}
}

// Receives applied reference changes from pushes, web commits, merges, and mirrors
func (m *Manager) OnRefUpdate(ctx context.Context, ev gitrepo.RefEvent) {
	if ev.Wiki {
		return
	}
	m.onPushPulls(ctx, ev)
	m.onPushIssues(ctx, ev)
	m.onPushReleases(ctx, ev)
}
