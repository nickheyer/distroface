package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/pages"
)

// Caller errors the services map onto connect codes
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid argument")
	ErrLocked    = errors.New("the conversation is locked")
	ErrBlocked   = errors.New("blocked by open dependencies")
	ErrConflict  = errors.New("conflict")
	ErrDisabled  = errors.New("feature disabled")
)

// Pinned issues shown at the top of a list
const MaxPinnedIssues = 3

// Comment and issue counts stay sane
const MaxBodyBytes = 1 << 20

// Who is acting and what they may do on the issues unit
type Actor struct {
	User     *auth.AuthenticatedUser
	CanWrite bool
	CanAdmin bool
}

func (a Actor) id() string {
	if a.User == nil {
		return ""
	}
	return a.User.ID
}

func (a Actor) username() string {
	if a.User == nil {
		return "system"
	}
	return a.User.Username
}

// Issue row with everything a list or detail view shows
type IssueView struct {
	Issue     *db.GitIssue
	Repo      *db.GitRepository
	Author    *db.User
	Labels    []*db.GitLabel
	Milestone *db.GitMilestone
	Assignees []*db.User
	ClosedBy  *db.User
	Reactions []*db.GitReaction
	Blocked   bool
	Tracked   int64
	// Seconds spent open according to the timeline
	OpenSeconds int64
	Subscribed  bool
	Pull        *db.GitPullRequest
	HeadRepo    *db.GitRepository
	// Board placement, nil when the viewer cannot see the board
	Project *db.GitProjectIssue
}

// Loads an issue by number in a repository
func (m *Manager) Issue(ctx context.Context, repo *db.GitRepository, number int64) (*db.GitIssue, error) {
	issue, err := m.store.GetGitIssue(ctx, repo.ID, number)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, fmt.Errorf("%w: issue #%d", ErrNotFound, number)
	}
	return issue, nil
}

// Batch loads the view rows for a set of issues as seen by a viewer
func (m *Manager) IssueViews(ctx context.Context, viewer *auth.AuthenticatedUser, repos map[string]*db.GitRepository, issues []*db.GitIssue) ([]*IssueView, error) {
	viewerID := ""
	if viewer != nil && !viewer.IsAnonymous() {
		viewerID = viewer.ID
	}
	ids := make([]string, 0, len(issues))
	var milestoneIDs, repoIDs []string
	userIDs := map[string]bool{}
	for _, is := range issues {
		ids = append(ids, is.ID)
		userIDs[is.AuthorID] = true
		if is.ClosedBy != "" {
			userIDs[is.ClosedBy] = true
		}
		if is.MilestoneID != nil {
			milestoneIDs = append(milestoneIDs, *is.MilestoneID)
		}
		if repos[is.RepoID] == nil {
			repoIDs = append(repoIDs, is.RepoID)
		}
	}
	if len(repoIDs) > 0 {
		more, err := m.store.GetGitRepositoriesByIDs(ctx, repoIDs)
		if err != nil {
			return nil, err
		}
		if repos == nil {
			repos = map[string]*db.GitRepository{}
		}
		for k, v := range more {
			repos[k] = v
		}
	}
	labels, _ := m.store.GetGitIssueLabels(ctx, ids)
	assignees, _ := m.store.GetGitIssueAssignees(ctx, ids)
	milestones, _ := m.store.GetGitMilestonesByIDs(ctx, milestoneIDs)
	reactions, _ := m.store.ListGitReactions(ctx, "issue", ids)
	blockers, _ := m.store.CountOpenGitBlockers(ctx, ids)
	tracked, _ := m.store.SumGitTrackedTime(ctx, ids)
	pulls, _ := m.store.GetGitPullRequests(ctx, ids)
	placements, _ := m.store.GetGitProjectIssues(ctx, ids)
	stateEvents, _ := m.store.ListGitIssueStateEvents(ctx, ids)
	now := time.Now()
	var headRepoIDs []string
	for _, pr := range pulls {
		if repos[pr.HeadRepoID] == nil {
			headRepoIDs = append(headRepoIDs, pr.HeadRepoID)
		}
	}
	if len(headRepoIDs) > 0 {
		more, _ := m.store.GetGitRepositoriesByIDs(ctx, headRepoIDs)
		for k, v := range more {
			repos[k] = v
		}
	}
	users, _ := m.store.GetUsersByIDs(ctx, keys(userIDs))
	boards := m.visibleBoards(ctx, viewer, repos, issues, placements)
	out := make([]*IssueView, 0, len(issues))
	for _, is := range issues {
		v := &IssueView{
			Issue:       is,
			Repo:        repos[is.RepoID],
			Author:      users[is.AuthorID],
			Labels:      labels[is.ID],
			Assignees:   assignees[is.ID],
			Reactions:   reactions[is.ID],
			Blocked:     blockers[is.ID] > 0,
			Tracked:     tracked[is.ID],
			OpenSeconds: OpenSeconds(is, stateEvents[is.ID], now),
			Pull:        pulls[is.ID],
		}
		if pl := placements[is.ID]; pl != nil && boards[pl.ProjectID] {
			v.Project = pl
		}
		if is.ClosedBy != "" {
			v.ClosedBy = users[is.ClosedBy]
		}
		if is.MilestoneID != nil {
			v.Milestone = milestones[*is.MilestoneID]
		}
		if v.Pull != nil {
			v.HeadRepo = repos[v.Pull.HeadRepoID]
		}
		if viewerID != "" {
			if sub, _ := m.store.GetGitIssueSubscription(ctx, is.ID, viewerID); sub != nil {
				v.Subscribed = *sub
			} else {
				v.Subscribed = is.AuthorID == viewerID || containsUser(v.Assignees, viewerID)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Single issue view
func (m *Manager) IssueView(ctx context.Context, viewer *auth.AuthenticatedUser, repo *db.GitRepository, issue *db.GitIssue) (*IssueView, error) {
	views, err := m.IssueViews(ctx, viewer, map[string]*db.GitRepository{repo.ID: repo}, []*db.GitIssue{issue})
	if err != nil {
		return nil, err
	}
	return views[0], nil
}

// Board ids among the placements the viewer may see
func (m *Manager) visibleBoards(ctx context.Context, viewer *auth.AuthenticatedUser, repos map[string]*db.GitRepository, issues []*db.GitIssue, placements map[string]*db.GitProjectIssue) map[string]bool {
	out := map[string]bool{}
	if len(placements) == 0 {
		return out
	}
	ids := map[string]bool{}
	for _, pl := range placements {
		ids[pl.ProjectID] = true
	}
	projects, _ := m.store.GetGitProjectsByIDs(ctx, keys(ids))
	for _, is := range issues {
		pl := placements[is.ID]
		if pl == nil {
			continue
		}
		if _, seen := out[pl.ProjectID]; seen {
			continue
		}
		out[pl.ProjectID] = m.ProjectVisible(ctx, viewer, projects[pl.ProjectID], repos[is.RepoID])
	}
	return out
}

func containsUser(users []*db.User, id string) bool {
	for _, u := range users {
		if u.ID == id {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Fields of a new issue
type NewIssue struct {
	Title       string
	Body        string
	LabelIDs    []string
	MilestoneID string
	AssigneeIDs []string
	DueDate     *time.Time
	ProjectID   string
	Template    string
}

func validTitle(t string) bool {
	t = strings.TrimSpace(t)
	return t != "" && len(t) <= 500
}

// Opens an issue, counters, events, notifications, webhooks, and actions included
func (m *Manager) CreateIssue(ctx context.Context, repo *db.GitRepository, actor Actor, in NewIssue) (*db.GitIssue, error) {
	if actor.User == nil || actor.User.IsAnonymous() {
		return nil, fmt.Errorf("%w: sign in to open issues", ErrForbidden)
	}
	if !validTitle(in.Title) {
		return nil, fmt.Errorf("%w: a title is required", ErrInvalid)
	}
	if len(in.Body) > MaxBodyBytes {
		return nil, fmt.Errorf("%w: body is too large", ErrInvalid)
	}
	if repo.IsArchived {
		return nil, gitrepo.ErrArchived
	}
	if !gitrepo.UnitEnabled(repo, db.GitUnitIssues) {
		return nil, fmt.Errorf("%w: issues are disabled on this repository", ErrForbidden)
	}
	if !actor.CanWrite {
		in.LabelIDs, in.AssigneeIDs, in.MilestoneID, in.DueDate, in.ProjectID = nil, nil, "", nil, ""
	}
	number, err := m.store.NextGitCounter(ctx, repo.ID, "issue_counter")
	if err != nil {
		return nil, err
	}
	issue := &db.GitIssue{
		ID:       uuid.New().String(),
		RepoID:   repo.ID,
		Number:   number,
		Title:    strings.TrimSpace(in.Title),
		Body:     in.Body,
		State:    "open",
		AuthorID: actor.User.ID,
		DueDate:  in.DueDate,
	}
	if in.MilestoneID != "" {
		if ms, _ := m.store.GetGitMilestone(ctx, repo.ID, in.MilestoneID); ms != nil {
			issue.MilestoneID = &ms.ID
		}
	}
	if err := m.store.CreateGitIssue(ctx, issue); err != nil {
		return nil, err
	}
	if len(in.LabelIDs) > 0 {
		labels, _ := m.validLabels(ctx, repo, in.LabelIDs)
		_ = m.store.SetGitIssueLabels(ctx, issue.ID, labelIDs(labels))
	}
	if len(in.AssigneeIDs) > 0 {
		_ = m.store.SetGitIssueAssignees(ctx, issue.ID, m.validAssignees(ctx, repo, in.AssigneeIDs))
	}
	if in.ProjectID != "" {
		if p, _ := m.store.GetGitProject(ctx, in.ProjectID); p != nil && m.projectCoversRepo(ctx, p, repo) {
			_ = m.placeIssue(ctx, p, issue, "", 0)
		}
	}
	_ = m.store.SetGitIssueSubscription(ctx, issue.ID, actor.User.ID, true)
	m.recordReferences(ctx, repo, issue, actor, issue.Body)
	m.notifyIssue(ctx, repo, issue, actor, "opened", issue.Title, mentions(issue.Body))
	m.dispatchIssue(ctx, repo, issue, actor, "opened", nil)
	m.Trigger(ctx, repo, "issues", "opened", "", "", actor.User, m.issuePayloadMap(ctx, repo, issue))
	return issue, nil
}

// Edits to apply to an issue
type IssueUpdate struct {
	Title       *string
	Body        *string
	State       *string
	MilestoneID *string
	DueDate     *time.Time
	ClearDue    bool
	SetLabels   bool
	LabelIDs    []string
	SetAssign   bool
	AssigneeIDs []string
}

// Reports whether the actor may edit an issue's own fields
func (m *Manager) canEdit(actor Actor, issue *db.GitIssue) bool {
	return actor.CanWrite || (actor.User != nil && actor.User.ID == issue.AuthorID)
}

// Applies an update writing one timeline event per change
func (m *Manager) UpdateIssue(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, up IssueUpdate) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	cols := map[string]any{}
	if up.Title != nil && strings.TrimSpace(*up.Title) != issue.Title {
		if !validTitle(*up.Title) {
			return fmt.Errorf("%w: a title is required", ErrInvalid)
		}
		old := issue.Title
		issue.Title = strings.TrimSpace(*up.Title)
		cols["title"] = issue.Title
		_, _ = m.AddEvent(ctx, issue, actor.User, EventRenamed, map[string]string{"from": old, "to": issue.Title})
	}
	if up.Body != nil && *up.Body != issue.Body {
		if len(*up.Body) > MaxBodyBytes {
			return fmt.Errorf("%w: body is too large", ErrInvalid)
		}
		issue.Body = *up.Body
		cols["body"] = issue.Body
		m.recordReferences(ctx, repo, issue, actor, issue.Body)
	}
	if up.State != nil && *up.State != issue.State {
		if err := m.setState(ctx, repo, actor, issue, nil, *up.State, ""); err != nil {
			return err
		}
	}
	if up.MilestoneID != nil && actor.CanWrite {
		if err := m.setMilestone(ctx, repo, actor, issue, *up.MilestoneID, cols); err != nil {
			return err
		}
	}
	if (up.DueDate != nil || up.ClearDue) && actor.CanWrite {
		issue.DueDate = up.DueDate
		cols["due_date"] = up.DueDate
		payload := map[string]any{"due": nil}
		if up.DueDate != nil {
			payload["due"] = up.DueDate.UTC().Format(time.RFC3339)
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, EventDueDate, payload)
	}
	if up.SetLabels && actor.CanWrite {
		if err := m.SetLabels(ctx, repo, actor, issue, up.LabelIDs); err != nil {
			return err
		}
	}
	if up.SetAssign && actor.CanWrite {
		if err := m.SetAssignees(ctx, repo, actor, issue, up.AssigneeIDs); err != nil {
			return err
		}
	}
	if len(cols) > 0 {
		cols["updated_at"] = time.Now().UTC()
		if err := m.store.UpdateGitIssueColumns(ctx, issue.ID, cols); err != nil {
			return err
		}
		if up.Title != nil || up.Body != nil {
			m.dispatchIssue(ctx, repo, issue, actor, "edited", nil)
			m.Trigger(ctx, repo, m.issueEvent(issue), "edited", "", "", actor.User, m.issuePayloadMap(ctx, repo, issue))
		}
	}
	return nil
}

func (m *Manager) issueEvent(issue *db.GitIssue) string {
	if issue.IsPull {
		return "pull_request"
	}
	return "issues"
}

// Closes or reopens, honoring dependencies and recording who closed
func (m *Manager) setState(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pr *db.GitPullRequest, state, via string) error {
	if state != "open" && state != "closed" {
		return fmt.Errorf("%w: state must be open or closed", ErrInvalid)
	}
	if state == issue.State {
		return nil
	}
	var headRepo *db.GitRepository
	if issue.IsPull {
		if pr == nil {
			var err error
			if pr, err = m.Pull(ctx, issue); err != nil {
				return err
			}
		}
		if pr.MergedAt != nil {
			return fmt.Errorf("%w: merged pull requests cannot change state", ErrInvalid)
		}
		if state == "open" {
			headRepo, _ = m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID)
			if headRepo == nil || m.branchTip(headRepo, pr.HeadBranch).IsZero() {
				return fmt.Errorf("%w: the head branch no longer exists, restore it first", ErrInvalid)
			}
		}
	}
	cols := map[string]any{"state": state, "updated_at": time.Now().UTC()}
	if state == "closed" {
		if !issue.IsPull && gitrepo.UnitsOf(repo).BlockOnDependencies {
			if blockers, _ := m.store.CountOpenGitBlockers(ctx, []string{issue.ID}); blockers[issue.ID] > 0 {
				return ErrBlocked
			}
		}
		now := time.Now().UTC()
		issue.ClosedAt = &now
		issue.ClosedBy = actor.id()
		cols["closed_at"] = now
		cols["closed_by"] = actor.id()
		payload := map[string]any{}
		if via != "" {
			payload["via"] = via
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, EventClosed, payload)
	} else {
		issue.ClosedAt = nil
		issue.ClosedBy = ""
		cols["closed_at"] = nil
		cols["closed_by"] = ""
		_, _ = m.AddEvent(ctx, issue, actor.User, EventReopened, nil)
	}
	issue.State = state
	if err := m.store.UpdateGitIssueColumns(ctx, issue.ID, cols); err != nil {
		return err
	}
	action := "closed"
	if state == "open" {
		action = "reopened"
	}
	if pr != nil {
		if state == "closed" {
			if pr.AutoMergeMethod != "" {
				pr.AutoMergeMethod, pr.AutoMergeBy = "", ""
				_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"auto_merge_method": "", "auto_merge_by": ""})
			}
		} else {
			if tip, _ := m.refreshPullRef(ctx, repo, pr, issue.Number, headRepo); !tip.IsZero() {
				pr.HeadSHA, pr.HeadDeleted = tip.String(), false
				_ = m.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"head_sha": pr.HeadSHA, "head_deleted": false})
			}
			m.refreshPullStats(ctx, repo, pr)
		}
	}
	m.recordBlockerState(ctx, repo, actor, issue, state)
	m.notifyIssue(ctx, repo, issue, actor, action, issue.Title, nil)
	m.dispatchIssue(ctx, repo, issue, actor, action, nil)
	m.Trigger(ctx, repo, m.issueEvent(issue), action, "", "", actor.User, m.issuePayloadMap(ctx, repo, issue))
	return nil
}

// Writes a timeline event on every issue this one blocks
func (m *Manager) recordBlockerState(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, state string) {
	blocked, _ := m.store.ListGitIssueBlocking(ctx, issue.ID)
	if len(blocked) == 0 {
		return
	}
	kind := EventBlockerClosed
	if state == db.IssueStateOpen {
		kind = EventBlockerReopened
	}
	payload := dependencyEvent(issue, repo.Namespace+"/"+repo.Name)
	for _, b := range blocked {
		_, _ = m.AddEvent(ctx, b, actor.User, kind, payload)
	}
}

// Closes or reopens through the public api
func (m *Manager) SetIssueState(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, state string) error {
	if actor.User == nil || actor.User.IsAnonymous() {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if !m.canEdit(actor, issue) {
		return ErrForbidden
	}
	return m.setState(ctx, repo, actor, issue, nil, state, "")
}

func (m *Manager) setMilestone(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, id string, cols map[string]any) error {
	current := ""
	if issue.MilestoneID != nil {
		current = *issue.MilestoneID
	}
	if id == current {
		return nil
	}
	if current != "" {
		old, _ := m.store.GetGitMilestone(ctx, repo.ID, current)
		title := ""
		if old != nil {
			title = old.Title
		}
		_, _ = m.AddEvent(ctx, issue, actor.User, EventDemilestoned, map[string]string{"id": current, "title": title})
	}
	if id == "" {
		issue.MilestoneID = nil
		cols["milestone_id"] = nil
		return nil
	}
	ms, err := m.store.GetGitMilestone(ctx, repo.ID, id)
	if err != nil {
		return err
	}
	if ms == nil {
		return fmt.Errorf("%w: milestone not found", ErrInvalid)
	}
	issue.MilestoneID = &ms.ID
	cols["milestone_id"] = ms.ID
	_, _ = m.AddEvent(ctx, issue, actor.User, EventMilestoned, map[string]string{"id": ms.ID, "title": ms.Title})
	return nil
}

// Labels of the repository or its organization by id
func (m *Manager) validLabels(ctx context.Context, repo *db.GitRepository, ids []string) ([]*db.GitLabel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := m.store.GetGitLabelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	orgID := m.orgIDOf(ctx, repo)
	out := rows[:0]
	for _, l := range rows {
		if (l.RepoID != nil && *l.RepoID == repo.ID) || (l.OrgID != nil && orgID != "" && *l.OrgID == orgID) {
			out = append(out, l)
		}
	}
	return out, nil
}

func labelIDs(labels []*db.GitLabel) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.ID)
	}
	return out
}

// Owning organization id, empty for user namespaces
func (m *Manager) orgIDOf(ctx context.Context, repo *db.GitRepository) string {
	if repo == nil || !repo.IsOrgNamespace {
		return ""
	}
	org, _ := m.store.GetOrganization(ctx, repo.Namespace)
	if org == nil {
		return ""
	}
	return org.ID
}

// Users who may be assigned, anyone who can read the repository
func (m *Manager) validAssignees(ctx context.Context, repo *db.GitRepository, ids []string) []string {
	users, _ := m.store.GetUsersByIDs(ctx, ids)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		u := users[id]
		if u == nil {
			continue
		}
		principal := &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: []string{"user"}, Provider: u.AuthProvider}
		if m.git.Access().CanSee(ctx, principal, repo) {
			out = append(out, id)
		}
	}
	return out
}

// Replaces labels honoring exclusive scopes, events per change
func (m *Manager) SetLabels(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, ids []string) error {
	wanted, err := m.validLabels(ctx, repo, ids)
	if err != nil {
		return err
	}
	wanted = resolveExclusive(wanted)
	current, _ := m.store.GetGitIssueLabels(ctx, []string{issue.ID})
	before := map[string]*db.GitLabel{}
	for _, l := range current[issue.ID] {
		before[l.ID] = l
	}
	after := map[string]*db.GitLabel{}
	for _, l := range wanted {
		after[l.ID] = l
	}
	if err := m.store.SetGitIssueLabels(ctx, issue.ID, labelIDs(wanted)); err != nil {
		return err
	}
	for id, l := range before {
		if after[id] == nil {
			_, _ = m.AddEvent(ctx, issue, actor.User, EventUnlabeled, labelEvent(l))
		}
	}
	for id, l := range after {
		if before[id] == nil {
			_, _ = m.AddEvent(ctx, issue, actor.User, EventLabeled, labelEvent(l))
			m.dispatchIssue(ctx, repo, issue, actor, "labeled", nil)
			m.Trigger(ctx, repo, m.issueEvent(issue), "labeled", "", "", actor.User, m.issuePayloadMap(ctx, repo, issue))
		}
	}
	return nil
}

func labelEvent(l *db.GitLabel) map[string]string {
	return map[string]string{"id": l.ID, "name": l.Name, "color": l.Color}
}

// Keeps the last label of each exclusive scope
func resolveExclusive(labels []*db.GitLabel) []*db.GitLabel {
	lastByScope := map[string]int{}
	for i, l := range labels {
		if l.ExclusiveScope != "" {
			lastByScope[l.ExclusiveScope] = i
		}
	}
	out := labels[:0]
	for i, l := range labels {
		if l.ExclusiveScope != "" && lastByScope[l.ExclusiveScope] != i {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Replaces assignees with events and notifications
func (m *Manager) SetAssignees(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, ids []string) error {
	wanted := m.validAssignees(ctx, repo, ids)
	current, _ := m.store.GetGitIssueAssignees(ctx, []string{issue.ID})
	before := map[string]bool{}
	for _, u := range current[issue.ID] {
		before[u.ID] = true
	}
	after := map[string]bool{}
	for _, id := range wanted {
		after[id] = true
	}
	if err := m.store.SetGitIssueAssignees(ctx, issue.ID, wanted); err != nil {
		return err
	}
	var added []string
	for id := range before {
		if !after[id] {
			_, _ = m.AddEvent(ctx, issue, actor.User, EventUnassigned, map[string]string{"user_id": id})
		}
	}
	for id := range after {
		if !before[id] {
			added = append(added, id)
			_, _ = m.AddEvent(ctx, issue, actor.User, EventAssigned, map[string]string{"user_id": id})
		}
	}
	if len(added) > 0 {
		m.Notify(ctx, Notification{
			Repo: repo, Issue: issue, Kind: m.notifyKind(issue), Actor: actor.User,
			Subject: fmt.Sprintf("[%s/%s] %s (#%d)", repo.Namespace, repo.Name, issue.Title, issue.Number),
			Text:    fmt.Sprintf("%s assigned you to #%d %s.", actor.username(), issue.Number, issue.Title),
			URL:     m.IssueURL(ctx, repo, issue), Recipients: added, Assigned: added, Reason: "assigned",
		})
		m.dispatchIssue(ctx, repo, issue, actor, "assigned", nil)
		m.Trigger(ctx, repo, m.issueEvent(issue), "assigned", "", "", actor.User, m.issuePayloadMap(ctx, repo, issue))
	}
	return nil
}

func (m *Manager) notifyKind(issue *db.GitIssue) string {
	if issue.IsPull {
		return NotifyPull
	}
	return NotifyIssue
}

// Locks or unlocks the conversation
func (m *Manager) SetLocked(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, locked bool, reason string) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if issue.Locked == locked {
		return nil
	}
	issue.Locked, issue.LockReason = locked, reason
	if !locked {
		issue.LockReason = ""
	}
	if err := m.store.UpdateGitIssueColumns(ctx, issue.ID, map[string]any{"locked": locked, "lock_reason": issue.LockReason}); err != nil {
		return err
	}
	if locked {
		_, _ = m.AddEvent(ctx, issue, actor.User, EventLocked, map[string]string{"reason": reason})
	} else {
		_, _ = m.AddEvent(ctx, issue, actor.User, EventUnlocked, nil)
	}
	return nil
}

// Pins or unpins, at most three pins per kind
func (m *Manager) SetPinned(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue, pinned bool) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if pinned == (issue.Pinned > 0) {
		return nil
	}
	order := 0
	if pinned {
		n, err := m.store.CountPinnedGitIssues(ctx, repo.ID, issue.IsPull)
		if err != nil {
			return err
		}
		if n >= MaxPinnedIssues {
			return fmt.Errorf("%w: at most %d issues can be pinned", ErrInvalid, MaxPinnedIssues)
		}
		if order, err = m.store.NextGitPinOrder(ctx, repo.ID); err != nil {
			return err
		}
	}
	issue.Pinned = order
	if err := m.store.UpdateGitIssueColumns(ctx, issue.ID, map[string]any{"pinned": order}); err != nil {
		return err
	}
	kind := EventPinned
	if !pinned {
		kind = EventUnpinned
	}
	_, _ = m.AddEvent(ctx, issue, actor.User, kind, nil)
	return nil
}

// Deletes an issue and its notifications
func (m *Manager) DeleteIssue(ctx context.Context, repo *db.GitRepository, actor Actor, issue *db.GitIssue) error {
	if !actor.CanAdmin {
		return ErrForbidden
	}
	_ = m.store.DeleteGitNotificationsByIssue(ctx, issue.ID)
	if err := m.store.DeleteGitIssue(ctx, issue.ID); err != nil {
		return err
	}
	m.dispatchIssue(ctx, repo, issue, actor, "deleted", nil)
	return nil
}

// One change applied to many issues
type BulkUpdate struct {
	State             *string
	MilestoneID       *string
	AddLabelIDs       []string
	RemoveLabelIDs    []string
	AddAssigneeIDs    []string
	RemoveAssigneeIDs []string
}

// Applies a bulk change, returning how many issues changed
func (m *Manager) BulkUpdate(ctx context.Context, repo *db.GitRepository, actor Actor, numbers []int64, up BulkUpdate) (int, error) {
	if !actor.CanWrite {
		return 0, ErrForbidden
	}
	if repo.IsArchived {
		return 0, gitrepo.ErrArchived
	}
	changed := 0
	for _, n := range numbers {
		issue, err := m.Issue(ctx, repo, n)
		if err != nil || issue.IsPull {
			continue
		}
		touched := false
		if up.State != nil && *up.State != issue.State {
			if err := m.setState(ctx, repo, actor, issue, nil, *up.State, ""); err == nil {
				touched = true
			}
		}
		if up.MilestoneID != nil {
			cols := map[string]any{}
			if err := m.setMilestone(ctx, repo, actor, issue, *up.MilestoneID, cols); err == nil && len(cols) > 0 {
				_ = m.store.UpdateGitIssueColumns(ctx, issue.ID, cols)
				touched = true
			}
		}
		if len(up.AddLabelIDs) > 0 || len(up.RemoveLabelIDs) > 0 {
			current, _ := m.store.GetGitIssueLabels(ctx, []string{issue.ID})
			ids := map[string]bool{}
			for _, l := range current[issue.ID] {
				ids[l.ID] = true
			}
			for _, id := range up.AddLabelIDs {
				ids[id] = true
			}
			for _, id := range up.RemoveLabelIDs {
				delete(ids, id)
			}
			if err := m.SetLabels(ctx, repo, actor, issue, keys(ids)); err == nil {
				touched = true
			}
		}
		if len(up.AddAssigneeIDs) > 0 || len(up.RemoveAssigneeIDs) > 0 {
			current, _ := m.store.GetGitIssueAssignees(ctx, []string{issue.ID})
			ids := map[string]bool{}
			for _, u := range current[issue.ID] {
				ids[u.ID] = true
			}
			for _, id := range up.AddAssigneeIDs {
				ids[id] = true
			}
			for _, id := range up.RemoveAssigneeIDs {
				delete(ids, id)
			}
			if err := m.SetAssignees(ctx, repo, actor, issue, keys(ids)); err == nil {
				touched = true
			}
		}
		if touched {
			changed++
		}
	}
	return changed, nil
}

// Lists issues with the repository's query allowlist applied
func (m *Manager) ListIssues(ctx context.Context, opts stores.IssueListOptions) ([]*db.GitIssue, int64, error) {
	if err := stores.IssuesQuery.Validate(opts.Query); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return m.store.ListGitIssues(ctx, opts)
}

// Repository ids the viewer may read, for cross repository searches
func (m *Manager) VisibleRepoIDs(ctx context.Context, user *auth.AuthenticatedUser, namespace string) (issueIDs, pullIDs []string, repos map[string]*db.GitRepository, err error) {
	opts := m.git.Access().ListOptions(user, namespace)
	opts.IncludeArchived = true
	rows, _, err := m.store.ListGitRepositories(ctx, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	repos = make(map[string]*db.GitRepository, len(rows))
	for _, r := range rows {
		issues := m.git.Access().UnitReadable(ctx, user, r, db.GitUnitIssues)
		pulls := m.git.Access().UnitReadable(ctx, user, r, db.GitUnitPulls)
		if issues {
			issueIDs = append(issueIDs, r.ID)
		}
		if pulls {
			pullIDs = append(pullIDs, r.ID)
		}
		if issues || pulls {
			repos[r.ID] = r
		}
	}
	return issueIDs, pullIDs, repos, nil
}

// Page url of an issue or pull request
func (m *Manager) IssueURL(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue) string {
	kind := "issues"
	if issue.IsPull {
		kind = "pulls"
	}
	return fmt.Sprintf("%s/%s/%d", m.git.HTMLURL(ctx, repo), kind, issue.Number)
}

// ── Webhooks, notifications, and action payloads ─────────────────────────

func (m *Manager) issuePayload(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue) *webhook.IssuePayload {
	p := &webhook.IssuePayload{
		Number: issue.Number, Title: issue.Title, Body: issue.Body, State: issue.State, IsPull: issue.IsPull,
		URL: m.IssueURL(ctx, repo, issue), Labels: []string{}, Assignees: []string{},
	}
	if labels, _ := m.store.GetGitIssueLabels(ctx, []string{issue.ID}); labels != nil {
		for _, l := range labels[issue.ID] {
			p.Labels = append(p.Labels, l.Name)
		}
	}
	if assignees, _ := m.store.GetGitIssueAssignees(ctx, []string{issue.ID}); assignees != nil {
		for _, u := range assignees[issue.ID] {
			p.Assignees = append(p.Assignees, u.Username)
		}
	}
	if issue.MilestoneID != nil {
		if ms, _ := m.store.GetGitMilestone(ctx, repo.ID, *issue.MilestoneID); ms != nil {
			p.Milestone = ms.Title
		}
	}
	if u, _ := m.store.GetUserByID(ctx, issue.AuthorID); u != nil {
		p.Author = u.Username
	}
	return p
}

// Issue payload as a generic map for workflow event contexts
func (m *Manager) issuePayloadMap(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue) map[string]any {
	p := m.issuePayload(ctx, repo, issue)
	b, _ := json.Marshal(p)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	key := "issue"
	if issue.IsPull {
		key = "pull_request"
	}
	return map[string]any{key: out}
}

// Sends the issue webhook, with a comment section when given
func (m *Manager) dispatchIssue(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, actor Actor, action string, comment *db.GitComment) {
	event := "git_issue"
	if issue.IsPull {
		event = "git_pull_request"
	}
	if comment != nil {
		if issue.IsPull {
			event = "git_pull_request_comment"
		} else {
			event = "git_issue_comment"
		}
	}
	payload := webhook.GitPayload{Action: action, Sender: webhook.Sender{Username: actor.username()}, Issue: m.issuePayload(ctx, repo, issue)}
	if comment != nil {
		payload.Comment = &webhook.CommentPayload{ID: comment.ID, Body: comment.Body, Author: actor.username()}
	}
	if issue.IsPull {
		if pr, _ := m.store.GetGitPullRequest(ctx, issue.ID); pr != nil {
			payload.PullRequest = m.pullPayload(ctx, repo, issue, pr)
		}
	}
	m.git.DispatchGit(ctx, event, repo, payload)
}

// Notifies participants and watchers about an issue action
func (m *Manager) notifyIssue(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue, actor Actor, action, title string, mentioned []string) {
	ids := m.resolveMentions(ctx, repo, mentioned)
	m.Notify(ctx, Notification{
		Repo: repo, Issue: issue, Kind: m.notifyKind(issue), Actor: actor.User, Reason: action,
		Subject:   fmt.Sprintf("[%s/%s] %s (#%d)", repo.Namespace, repo.Name, title, issue.Number),
		Text:      fmt.Sprintf("%s %s #%d %s.", actor.username(), action, issue.Number, issue.Title),
		URL:       m.IssueURL(ctx, repo, issue),
		Mentioned: ids,
	})
}

// User ids behind mentioned usernames and org teams
func (m *Manager) resolveMentions(ctx context.Context, repo *db.GitRepository, names []string) []string {
	var out []string
	for _, name := range names {
		if strings.Contains(name, "/") {
			out = append(out, m.teamMembers(ctx, repo, name)...)
			continue
		}
		if u, _ := m.store.GetUserByUsername(ctx, name); u != nil {
			out = append(out, u.ID)
		}
	}
	return out
}

// Members of an org team named as org/team
func (m *Manager) teamMembers(ctx context.Context, repo *db.GitRepository, ref string) []string {
	orgName, teamName, _ := strings.Cut(ref, "/")
	if orgName != repo.Namespace || !repo.IsOrgNamespace {
		return nil
	}
	org, _ := m.store.GetOrganization(ctx, orgName)
	if org == nil {
		return nil
	}
	team, _ := m.store.GetOrgTeamByName(ctx, org.ID, teamName)
	if team == nil {
		return nil
	}
	ids, _ := m.store.TeamUserIDs(ctx, team)
	return ids
}

// Builds a query from caller filters with defaults applied
func IssueListOptionsFrom(repoID string, state string, page *pages.Query, orderBy string, limit, offset int) stores.IssueListOptions {
	opts := stores.IssueListOptions{RepoID: repoID, State: state, OrderBy: orderBy, Limit: limit, Offset: offset}
	if page != nil {
		opts.Query = *page
	}
	return opts
}
