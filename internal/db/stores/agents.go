package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── Providers ────────────────────────────────────────────────────────────

func (s *Store) CreateAgentProvider(ctx context.Context, p *db.AgentProvider) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if p.IsDefault {
			if err := tx.Model(&db.AgentProvider{}).Where("scope_kind = ? AND scope_id = ?", p.ScopeKind, p.ScopeID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(p).Error
	})
}

func (s *Store) GetAgentProvider(ctx context.Context, id string) (*db.AgentProvider, error) {
	var p db.AgentProvider
	err := s.db.WithContext(ctx).First(&p, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}

func (s *Store) ListAgentProviders(ctx context.Context, scopeKind, scopeID string, limit, offset int) ([]*db.AgentProvider, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.AgentProvider{}).Where("scope_kind = ? AND scope_id = ?", scopeKind, scopeID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	var out []*db.AgentProvider
	err := q.Order("is_default DESC, created_at ASC").Find(&out).Error
	return out, total, err
}

// Connectors a repository may use, repo then org then instance
func (s *Store) ListAgentProvidersForRepo(ctx context.Context, repoID, orgID string) ([]*db.AgentProvider, error) {
	q := s.db.WithContext(ctx).Model(&db.AgentProvider{})
	if orgID != "" {
		q = q.Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)",
			db.ActionsScopeRepo, repoID, db.ActionsScopeOrg, orgID, db.ActionsScopeSystem, "")
	} else {
		q = q.Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)", db.ActionsScopeRepo, repoID, db.ActionsScopeSystem, "")
	}
	var out []*db.AgentProvider
	err := q.Order("CASE scope_kind WHEN 'repo' THEN 0 WHEN 'org' THEN 1 ELSE 2 END, is_default DESC, created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) UpdateAgentProvider(ctx context.Context, p *db.AgentProvider) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if p.IsDefault {
			if err := tx.Model(&db.AgentProvider{}).Where("scope_kind = ? AND scope_id = ? AND id <> ?", p.ScopeKind, p.ScopeID, p.ID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Save(p).Error
	})
}

func (s *Store) UpdateAgentProviderColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.AgentProvider{}).Where("id = ?", id).Updates(cols).Error
}

func (s *Store) DeleteAgentProvider(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.Agent{}).Where("provider_id = ?", id).Update("provider_id", "").Error; err != nil {
			return err
		}
		return tx.Delete(&db.AgentProvider{}, "id = ?", id).Error
	})
}

// Tokens sessions spent through a connector since a moment
func (s *Store) AgentProviderTokensSince(ctx context.Context, providerID string, since time.Time) (int64, error) {
	var total struct{ Total int64 }
	err := s.db.WithContext(ctx).Model(&db.AgentSession{}).
		Select("COALESCE(SUM(prompt_tokens + output_tokens), 0) AS total").
		Where("provider_id = ? AND created_at >= ?", providerID, since).
		Scan(&total).Error
	return total.Total, err
}

// ── Agents ───────────────────────────────────────────────────────────────

func (s *Store) CreateAgent(ctx context.Context, a *db.Agent) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *Store) GetAgent(ctx context.Context, id string) (*db.Agent, error) {
	var a db.Agent
	err := s.db.WithContext(ctx).First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

func (s *Store) GetAgentBySlug(ctx context.Context, scopeKind, scopeID, slug string) (*db.Agent, error) {
	var a db.Agent
	err := s.db.WithContext(ctx).First(&a, "scope_kind = ? AND scope_id = ? AND slug = ?", scopeKind, scopeID, slug).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

func (s *Store) ListAgents(ctx context.Context, scopeKind, scopeID string, limit, offset int) ([]*db.Agent, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.Agent{}).Where("scope_kind = ? AND scope_id = ?", scopeKind, scopeID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	var out []*db.Agent
	err := q.Order("created_at ASC").Find(&out).Error
	return out, total, err
}

// Agents a repository may use, repo then org then instance
func (s *Store) ListAgentsForRepo(ctx context.Context, repoID, orgID string) ([]*db.Agent, error) {
	q := s.db.WithContext(ctx).Model(&db.Agent{})
	if orgID != "" {
		q = q.Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)",
			db.ActionsScopeRepo, repoID, db.ActionsScopeOrg, orgID, db.ActionsScopeSystem, "")
	} else {
		q = q.Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)", db.ActionsScopeRepo, repoID, db.ActionsScopeSystem, "")
	}
	var out []*db.Agent
	err := q.Order("CASE scope_kind WHEN 'repo' THEN 0 WHEN 'org' THEN 1 ELSE 2 END, created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) UpdateAgent(ctx context.Context, a *db.Agent) error {
	return s.db.WithContext(ctx).Save(a).Error
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.Agent{}, "id = ?", id).Error
}

// Session counts and newest session per agent id
func (s *Store) AgentSessionStats(ctx context.Context, agentIDs []string) (map[string]int64, map[string]time.Time, error) {
	counts, last := map[string]int64{}, map[string]time.Time{}
	if len(agentIDs) == 0 {
		return counts, last, nil
	}
	var rows []struct {
		AgentID string
		Count   int64
		Last    time.Time
	}
	err := s.db.WithContext(ctx).Model(&db.AgentSession{}).
		Select("agent_id, COUNT(*) AS count, MAX(created_at) AS last").
		Where("agent_id IN ?", agentIDs).Group("agent_id").Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		counts[r.AgentID] = r.Count
		last[r.AgentID] = r.Last
	}
	return counts, last, nil
}

// ── Identities ───────────────────────────────────────────────────────────

func (s *Store) GetAgentIdentity(ctx context.Context, slug string) (*db.AgentIdentity, error) {
	var id db.AgentIdentity
	err := s.db.WithContext(ctx).First(&id, "slug = ?", slug).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &id, err
}

func (s *Store) GetAgentIdentityByUser(ctx context.Context, userID string) (*db.AgentIdentity, error) {
	var id db.AgentIdentity
	err := s.db.WithContext(ctx).First(&id, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &id, err
}

func (s *Store) CreateAgentIdentity(ctx context.Context, id *db.AgentIdentity) error {
	return s.db.WithContext(ctx).Create(id).Error
}

func (s *Store) ListAgentIdentities(ctx context.Context) ([]*db.AgentIdentity, error) {
	var out []*db.AgentIdentity
	err := s.db.WithContext(ctx).Find(&out).Error
	return out, err
}

// User ids of every agent bot account
func (s *Store) AgentUserIDs(ctx context.Context) (map[string]bool, error) {
	var ids []string
	err := s.db.WithContext(ctx).Model(&db.User{}).Where("auth_provider = ?", db.AuthProviderAgent).Pluck("id", &ids).Error
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// ── Sessions ─────────────────────────────────────────────────────────────

func (s *Store) CreateAgentSession(ctx context.Context, sess *db.AgentSession) error {
	if sess.ID == "" {
		sess.ID = uuid.New().String()
	}
	if sess.Number == 0 {
		number, err := s.NextGitCounter(ctx, sess.RepoID, "agent_counter")
		if err != nil {
			return err
		}
		sess.Number = number
	}
	return s.db.WithContext(ctx).Create(sess).Error
}

func (s *Store) GetAgentSession(ctx context.Context, repoID, id string) (*db.AgentSession, error) {
	var sess db.AgentSession
	err := s.db.WithContext(ctx).First(&sess, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sess, err
}

func (s *Store) GetAgentSessionByID(ctx context.Context, id string) (*db.AgentSession, error) {
	var sess db.AgentSession
	err := s.db.WithContext(ctx).First(&sess, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sess, err
}

type AgentSessionListOptions struct {
	RepoID      string
	AgentID     string
	AgentSlug   string
	Status      string
	Conclusion  string
	IssueNumber int64
	Actor       string
	Trigger     string
	Limit       int
	Offset      int
}

func (s *Store) ListAgentSessions(ctx context.Context, opts AgentSessionListOptions) ([]*db.AgentSession, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.AgentSession{}).Where("repo_id = ?", opts.RepoID)
	if opts.AgentID != "" {
		q = q.Where("agent_id = ?", opts.AgentID)
	}
	if opts.AgentSlug != "" {
		q = q.Where("agent_slug = ?", opts.AgentSlug)
	}
	if opts.Status != "" {
		q = q.Where("status = ?", opts.Status)
	}
	if opts.Conclusion != "" {
		q = q.Where("conclusion = ?", opts.Conclusion)
	}
	if opts.IssueNumber > 0 {
		q = q.Where("issue_number = ? OR result_pull_number = ?", opts.IssueNumber, opts.IssueNumber)
	}
	if opts.Trigger != "" {
		q = q.Where("trigger = ?", opts.Trigger)
	}
	if opts.Actor != "" {
		q = q.Where("trigger_user_id IN (SELECT id FROM users WHERE username = ? OR id = ?)", opts.Actor, opts.Actor)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit).Offset(opts.Offset)
	}
	var out []*db.AgentSession
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

func (s *Store) UpdateAgentSession(ctx context.Context, sess *db.AgentSession) error {
	return s.db.WithContext(ctx).Save(sess).Error
}

func (s *Store) UpdateAgentSessionColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.AgentSession{}).Where("id = ?", id).Updates(cols).Error
}

// Sessions not yet completed in a repository, optionally one agent
func (s *Store) CountActiveAgentSessions(ctx context.Context, repoID, agentID string) (int64, error) {
	q := s.db.WithContext(ctx).Model(&db.AgentSession{}).Where("repo_id = ? AND status IN ?", repoID, []string{"running", "waiting_approval"})
	if agentID != "" {
		q = q.Where("agent_id = ?", agentID)
	}
	var n int64
	err := q.Count(&n).Error
	return n, err
}

func (s *Store) CountAgentSessionsSince(ctx context.Context, repoID string, since time.Time) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.AgentSession{}).Where("repo_id = ? AND created_at >= ?", repoID, since).Count(&n).Error
	return n, err
}

// Sessions in the given states across every repository, oldest first
func (s *Store) ListAgentSessionsByStatus(ctx context.Context, statuses []string, limit int) ([]*db.AgentSession, error) {
	q := s.db.WithContext(ctx).Where("status IN ?", statuses).Order("created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []*db.AgentSession
	err := q.Find(&out).Error
	return out, err
}

func (s *Store) ListQueuedAgentSessions(ctx context.Context, repoID string) ([]*db.AgentSession, error) {
	var out []*db.AgentSession
	err := s.db.WithContext(ctx).Where("repo_id = ? AND status = ?", repoID, "queued").Order("created_at ASC").Find(&out).Error
	return out, err
}

// Live sessions of one agent on one issue or pull request
func (s *Store) ListLiveAgentSessionsForIssue(ctx context.Context, repoID, agentID string, issueNumber int64) ([]*db.AgentSession, error) {
	var out []*db.AgentSession
	err := s.db.WithContext(ctx).Where("repo_id = ? AND agent_id = ? AND issue_number = ? AND status IN ?", repoID, agentID, issueNumber, []string{"queued", "running", "waiting_approval"}).Find(&out).Error
	return out, err
}

// Newest session that opened a given pull request
func (s *Store) AgentSessionForResultPull(ctx context.Context, repoID string, pullNumber int64) (*db.AgentSession, error) {
	var sess db.AgentSession
	err := s.db.WithContext(ctx).Where("repo_id = ? AND result_pull_number = ?", repoID, pullNumber).Order("created_at DESC").First(&sess).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sess, err
}

func (s *Store) DeleteAgentSession(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ?", id).Delete(&db.AgentSessionEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", id).Delete(&db.AgentApproval{}).Error; err != nil {
			return err
		}
		return tx.Delete(&db.AgentSession{}, "id = ?", id).Error
	})
}

func (s *Store) ListFinishedAgentSessionsBefore(ctx context.Context, repoID string, cutoff time.Time, limit int) ([]*db.AgentSession, error) {
	q := s.db.WithContext(ctx).Where("repo_id = ? AND status = ? AND created_at < ?", repoID, "completed", cutoff).Order("created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []*db.AgentSession
	err := q.Find(&out).Error
	return out, err
}

// ── Events ───────────────────────────────────────────────────────────────

// Appends one event assigning the next sequence number
func (s *Store) AppendAgentSessionEvent(ctx context.Context, ev *db.AgentSessionEvent) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var last struct{ Seq int64 }
		if err := tx.Model(&db.AgentSessionEvent{}).Select("COALESCE(MAX(seq), 0) AS seq").Where("session_id = ?", ev.SessionID).Scan(&last).Error; err != nil {
			return err
		}
		ev.Seq = last.Seq + 1
		if ev.CreatedAt.IsZero() {
			ev.CreatedAt = time.Now().UTC()
		}
		if err := tx.Create(ev).Error; err != nil {
			return err
		}
		return tx.Model(&db.AgentSession{}).Where("id = ?", ev.SessionID).Updates(map[string]any{"event_count": ev.Seq, "updated_at": time.Now().UTC()}).Error
	})
}

func (s *Store) ListAgentSessionEvents(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]*db.AgentSessionEvent, error) {
	q := s.db.WithContext(ctx).Where("session_id = ? AND seq > ?", sessionID, afterSeq).Order("seq ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []*db.AgentSessionEvent
	err := q.Find(&out).Error
	return out, err
}

func (s *Store) CountAgentSessionEvents(ctx context.Context, sessionID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.AgentSessionEvent{}).Where("session_id = ?", sessionID).Count(&n).Error
	return n, err
}

func (s *Store) DeleteAgentSessionEvents(ctx context.Context, sessionID string) error {
	return s.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&db.AgentSessionEvent{}).Error
}

// ── Approvals ────────────────────────────────────────────────────────────

func (s *Store) CreateAgentApproval(ctx context.Context, a *db.AgentApproval) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *Store) GetAgentApproval(ctx context.Context, sessionID, id string) (*db.AgentApproval, error) {
	var a db.AgentApproval
	err := s.db.WithContext(ctx).First(&a, "session_id = ? AND id = ?", sessionID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

func (s *Store) GetPendingAgentApproval(ctx context.Context, sessionID string) (*db.AgentApproval, error) {
	var a db.AgentApproval
	err := s.db.WithContext(ctx).Where("session_id = ? AND status = ?", sessionID, "pending").Order("created_at ASC").First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

func (s *Store) UpdateAgentApproval(ctx context.Context, a *db.AgentApproval) error {
	return s.db.WithContext(ctx).Save(a).Error
}

func (s *Store) ListAgentApprovals(ctx context.Context, sessionID string) ([]*db.AgentApproval, error) {
	var out []*db.AgentApproval
	err := s.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// ── Usage ────────────────────────────────────────────────────────────────

// One session reduced to what usage reports aggregate
type AgentUsageRow struct {
	RepoID          string
	AgentID         string
	AgentSlug       string
	AgentName       string
	ProviderID      string
	ProviderName    string
	Model           string
	Conclusion      string
	Status          string
	ToolCalls       int64
	InputTokens     int64
	OutputTokens    int64
	PromptTokens    int64
	CacheReadTokens int64
	CreatedAt       time.Time
}

// Sessions since a moment, every repository when repoIDs is nil
func (s *Store) AgentUsageRows(ctx context.Context, repoIDs []string, since time.Time) ([]AgentUsageRow, error) {
	q := s.db.WithContext(ctx).Model(&db.AgentSession{}).
		Select("repo_id, agent_id, agent_slug, agent_name, provider_id, provider_name, model, conclusion, status, tool_calls, input_tokens, output_tokens, prompt_tokens, cache_read_tokens, created_at").
		Where("created_at >= ?", since)
	if repoIDs != nil {
		if len(repoIDs) == 0 {
			return nil, nil
		}
		q = q.Where("repo_id IN ?", repoIDs)
	}
	var out []AgentUsageRow
	err := q.Order("created_at ASC").Scan(&out).Error
	return out, err
}
