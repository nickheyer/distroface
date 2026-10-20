package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// ── Runners ──────────────────────────────────────────────────────────────

func (s *Store) CreateActionsRunner(ctx context.Context, r *db.ActionsRunner) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *Store) GetActionsRunner(ctx context.Context, id string) (*db.ActionsRunner, error) {
	var r db.ActionsRunner
	err := s.db.WithContext(ctx).First(&r, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) GetActionsRunnerByTokenHash(ctx context.Context, hash string) (*db.ActionsRunner, error) {
	var r db.ActionsRunner
	err := s.db.WithContext(ctx).First(&r, "token_hash = ?", hash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) ListActionsRunners(ctx context.Context, scopeKind, scopeID string, limit, offset int) ([]*db.ActionsRunner, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ActionsRunner{}).Where("scope_kind = ? AND scope_id = ?", scopeKind, scopeID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.ActionsRunner
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at ASC").Find(&out).Error
	return out, total, err
}

// Runners that may serve a repository, repo then org then system
func (s *Store) ListActionsRunnersForRepo(ctx context.Context, repoID, orgID string) ([]*db.ActionsRunner, error) {
	var out []*db.ActionsRunner
	err := s.runnersServingRepo(ctx, repoID, orgID).Find(&out).Error
	return out, err
}

// Same set paged, own runners first, then org, then instance
func (s *Store) ListActionsRunnersServingRepo(ctx context.Context, repoID, orgID string, limit, offset int) ([]*db.ActionsRunner, int64, error) {
	q := s.runnersServingRepo(ctx, repoID, orgID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	var out []*db.ActionsRunner
	err := q.Order("CASE scope_kind WHEN 'repo' THEN 0 WHEN 'org' THEN 1 ELSE 2 END, created_at ASC").Find(&out).Error
	return out, total, err
}

func (s *Store) runnersServingRepo(ctx context.Context, repoID, orgID string) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&db.ActionsRunner{})
	if orgID != "" {
		return q.Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)",
			db.ActionsScopeRepo, repoID, db.ActionsScopeOrg, orgID, db.ActionsScopeSystem, "")
	}
	return q.Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)", db.ActionsScopeRepo, repoID, db.ActionsScopeSystem, "")
}

func (s *Store) UpdateActionsRunner(ctx context.Context, r *db.ActionsRunner) error {
	return s.db.WithContext(ctx).Save(r).Error
}

func (s *Store) TouchActionsRunner(ctx context.Context, id, version string, busy int) error {
	return s.db.WithContext(ctx).Model(&db.ActionsRunner{}).Where("id = ?", id).Updates(map[string]any{"last_seen": time.Now().UTC(), "version": version, "busy": busy}).Error
}

func (s *Store) DeleteActionsRunner(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.ActionsRunner{}, "id = ?", id).Error
}

func (s *Store) CreateActionsRunnerToken(ctx context.Context, t *db.ActionsRunnerToken) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *Store) GetActionsRunnerToken(ctx context.Context, hash string) (*db.ActionsRunnerToken, error) {
	var t db.ActionsRunnerToken
	err := s.db.WithContext(ctx).First(&t, "token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, time.Now().UTC()).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

func (s *Store) UseActionsRunnerToken(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.ActionsRunnerToken{}).Where("id = ?", id).Update("used_at", time.Now().UTC()).Error
}

// ── Workflow state ───────────────────────────────────────────────────────

func (s *Store) GetDisabledWorkflows(ctx context.Context, repoID string) (map[string]bool, error) {
	var rows []db.ActionsWorkflowState
	if err := s.db.WithContext(ctx).Where("repo_id = ? AND disabled = ?", repoID, true).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Path] = true
	}
	return out, nil
}

func (s *Store) SetWorkflowDisabled(ctx context.Context, repoID, path string, disabled bool) error {
	row := db.ActionsWorkflowState{RepoID: repoID, Path: path, Disabled: disabled}
	return s.db.WithContext(ctx).Save(&row).Error
}

// ── Runs and jobs ────────────────────────────────────────────────────────

func (s *Store) CreateActionsRun(ctx context.Context, run *db.ActionsRun, jobs []*db.ActionsJob) error {
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		for _, j := range jobs {
			if j.ID == "" {
				j.ID = uuid.New().String()
			}
			j.RunID = run.ID
			if err := tx.Create(j).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Adds late expanded matrix legs to an existing run
func (s *Store) CreateActionsJobs(ctx context.Context, jobs []*db.ActionsJob) error {
	if len(jobs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, j := range jobs {
			if j.ID == "" {
				j.ID = uuid.New().String()
			}
			if err := tx.Create(j).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetActionsRun(ctx context.Context, repoID, id string) (*db.ActionsRun, error) {
	var run db.ActionsRun
	err := s.db.WithContext(ctx).First(&run, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &run, err
}

func (s *Store) GetActionsRunByID(ctx context.Context, id string) (*db.ActionsRun, error) {
	var run db.ActionsRun
	err := s.db.WithContext(ctx).First(&run, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &run, err
}

type ActionsRunListOptions struct {
	RepoID       string
	WorkflowPath string
	Status       string
	Conclusion   string
	Event        string
	Ref          string
	Actor        string
	Limit        int
	Offset       int
}

func (s *Store) ListActionsRuns(ctx context.Context, opts ActionsRunListOptions) ([]*db.ActionsRun, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ActionsRun{}).Where("repo_id = ?", opts.RepoID)
	if opts.WorkflowPath != "" {
		q = q.Where("workflow_path = ?", opts.WorkflowPath)
	}
	if opts.Status != "" {
		q = q.Where("status = ?", opts.Status)
	}
	if opts.Conclusion != "" {
		q = q.Where("conclusion = ?", opts.Conclusion)
	}
	if opts.Event != "" {
		q = q.Where("event = ?", opts.Event)
	}
	if opts.Ref != "" {
		q = q.Where("ref = ?", opts.Ref)
	}
	if opts.Actor != "" {
		q = q.Where("trigger_user_id IN (SELECT id FROM users WHERE username = ? OR id = ?)", opts.Actor, opts.Actor)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.ActionsRun
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit).Offset(opts.Offset)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

// Run counts and newest run per workflow path
func (s *Store) ActionsWorkflowStats(ctx context.Context, repoID string) (map[string]int32, map[string]time.Time, error) {
	var rows []struct {
		WorkflowPath string
		Count        int32
		Last         time.Time
	}
	err := s.db.WithContext(ctx).Model(&db.ActionsRun{}).Select("workflow_path, COUNT(*) as count, MAX(created_at) as last").
		Where("repo_id = ?", repoID).Group("workflow_path").Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	counts, last := map[string]int32{}, map[string]time.Time{}
	for _, r := range rows {
		counts[r.WorkflowPath] = r.Count
		last[r.WorkflowPath] = r.Last
	}
	return counts, last, nil
}

func (s *Store) UpdateActionsRun(ctx context.Context, run *db.ActionsRun) error {
	return s.db.WithContext(ctx).Save(run).Error
}

func (s *Store) UpdateActionsRunColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.ActionsRun{}).Where("id = ?", id).Updates(cols).Error
}

// Runs in the same concurrency group that are still live
func (s *Store) ListLiveRunsInGroup(ctx context.Context, repoID, group, exceptID string) ([]*db.ActionsRun, error) {
	var out []*db.ActionsRun
	err := s.db.WithContext(ctx).Where("repo_id = ? AND concurrency = ? AND id <> ? AND status IN ?", repoID, group, exceptID, []string{"queued", "running", "waiting"}).Find(&out).Error
	return out, err
}

func (s *Store) ListActionsJobs(ctx context.Context, runID string) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// Running job whose api token row carries the id, nil when none
func (s *Store) GetActionsJobByTokenID(ctx context.Context, tokenID string) (*db.ActionsJob, error) {
	if tokenID == "" {
		return nil, nil
	}
	var j db.ActionsJob
	if err := s.db.WithContext(ctx).Where("token_id = ?", tokenID).First(&j).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

func (s *Store) GetActionsJob(ctx context.Context, id string) (*db.ActionsJob, error) {
	var j db.ActionsJob
	err := s.db.WithContext(ctx).First(&j, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &j, err
}

func (s *Store) UpdateActionsJob(ctx context.Context, j *db.ActionsJob) error {
	return s.db.WithContext(ctx).Save(j).Error
}

func (s *Store) UpdateActionsJobColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.ActionsJob{}).Where("id = ?", id).Updates(cols).Error
}

// Claims one queued job matching the labels for a runner, atomically
func (s *Store) ClaimActionsJob(ctx context.Context, runnerID string, candidates []string) (*db.ActionsJob, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	var claimed *db.ActionsJob
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var jobs []db.ActionsJob
		if err := tx.Where("id IN ? AND status = ?", candidates, "queued").Order("created_at ASC").Find(&jobs).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for i := range jobs {
			job := jobs[i]
			// Matrix jobs past max-parallel stay queued until a sibling finishes
			res := tx.Model(&db.ActionsJob{}).
				Where("id = ? AND status = ? AND (max_parallel = 0 OR (SELECT COUNT(*) FROM actions_jobs r WHERE r.run_id = ? AND r.matrix_group = ? AND r.status = ?) < max_parallel)",
					job.ID, "queued", job.RunID, job.MatrixGroup, "running").
				Updates(map[string]any{"status": "running", "runner_id": runnerID, "started_at": now})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}
			job.Status = "running"
			job.RunnerID = runnerID
			job.StartedAt = &now
			claimed = &job
			return nil
		}
		return nil
	})
	return claimed, err
}

// Live jobs of a job level concurrency group in one repository
func (s *Store) ListLiveJobsInGroup(ctx context.Context, repoID, group, exceptID string) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Model(&db.ActionsJob{}).
		Joins("JOIN actions_runs ON actions_runs.id = actions_jobs.run_id").
		Where("actions_runs.repo_id = ? AND actions_jobs.concurrency = ? AND actions_jobs.id <> ? AND actions_jobs.status IN ?", repoID, group, exceptID, []string{"waiting", "queued", "running"}).
		Find(&out).Error
	return out, err
}

// Queued jobs created before the cutoff that no runner took
func (s *Store) ListQueuedActionsJobsBefore(ctx context.Context, cutoff time.Time) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Where("status = ? AND created_at < ?", "queued", cutoff).Find(&out).Error
	return out, err
}

// Queued jobs, oldest first, across every repo a runner can serve
func (s *Store) ListQueuedActionsJobs(ctx context.Context, limit int) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Where("status = ?", "queued").Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

// Queued jobs inside one runner scope, oldest first, paged
func (s *Store) ListQueuedActionsJobsInScope(ctx context.Context, scopeKind, scopeID string, limit, offset int) ([]*db.ActionsJob, error) {
	q := s.db.WithContext(ctx).Model(&db.ActionsJob{}).
		Joins("JOIN actions_runs ON actions_runs.id = actions_jobs.run_id").
		Where("actions_jobs.status = ?", "queued")
	switch scopeKind {
	case db.ActionsScopeOrg:
		q = q.Joins("JOIN git_repositories ON git_repositories.id = actions_runs.repo_id").
			Where("git_repositories.is_org_namespace = ? AND git_repositories.namespace = (SELECT name FROM organizations WHERE id = ?)", true, scopeID)
	case db.ActionsScopeRepo:
		q = q.Where("actions_runs.repo_id = ?", scopeID)
	}
	var out []*db.ActionsJob
	err := q.Order("actions_jobs.created_at ASC").Limit(limit).Offset(offset).Find(&out).Error
	return out, err
}

// Runs by id in one query
func (s *Store) GetActionsRunsByIDs(ctx context.Context, ids []string) (map[string]*db.ActionsRun, error) {
	out := map[string]*db.ActionsRun{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []*db.ActionsRun
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// Running jobs whose runner stopped reporting
func (s *Store) ListStaleRunningJobs(ctx context.Context, cutoff time.Time) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Where("status = ? AND runner_id NOT IN (SELECT id FROM actions_runners WHERE last_seen > ?)", "running", cutoff).Find(&out).Error
	return out, err
}

// Append refused because the job log would pass its byte cap
var ErrJobLogCapped = errors.New("job log cap reached")

// Append refused because another chunk already took the index
var ErrJobLogConflict = errors.New("job log index taken")

// Appends a chunk, counting its bytes against the cap in the same transaction
func (s *Store) AppendActionsJobLog(ctx context.Context, jobID string, index int, data []byte, maxBytes int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&db.ActionsJob{}).
			Where("id = ? AND log_chunks <= ? AND log_bytes + ? <= ?", jobID, index, len(data), maxBytes).
			Updates(map[string]any{"log_chunks": index + 1, "log_bytes": gorm.Expr("log_bytes + ?", len(data))})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var job db.ActionsJob
			if err := tx.First(&job, "id = ?", jobID).Error; err != nil {
				return err
			}
			if job.LogBytes+int64(len(data)) > maxBytes {
				return ErrJobLogCapped
			}
			return ErrJobLogConflict
		}
		return tx.Create(&db.ActionsJobLog{JobID: jobID, Index: index, Data: data}).Error
	})
}

// Chunks from an index onward, zero limit returns them all
func (s *Store) ListActionsJobLogs(ctx context.Context, jobID string, fromIndex, limit int) ([]*db.ActionsJobLog, error) {
	var out []*db.ActionsJobLog
	q := s.db.WithContext(ctx).Where("job_id = ? AND \"index\" >= ?", jobID, fromIndex).Order("\"index\" ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&out).Error
	return out, err
}

func (s *Store) DeleteActionsJobLogs(ctx context.Context, jobID string) error {
	return s.db.WithContext(ctx).Where("job_id = ?", jobID).Delete(&db.ActionsJobLog{}).Error
}

// Runs older than the cutoff whose logs can be dropped
func (s *Store) ListFinishedRunsBefore(ctx context.Context, cutoff time.Time, limit int) ([]*db.ActionsRun, error) {
	var out []*db.ActionsRun
	err := s.db.WithContext(ctx).Where("status = ? AND finished_at < ?", "completed", cutoff).Order("finished_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ── Secrets and variables ────────────────────────────────────────────────

func (s *Store) UpsertActionsSecret(ctx context.Context, sec *db.ActionsSecret) error {
	var existing db.ActionsSecret
	err := s.db.WithContext(ctx).First(&existing, "scope_kind = ? AND scope_id = ? AND name = ?", sec.ScopeKind, sec.ScopeID, sec.Name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if sec.ID == "" {
			sec.ID = uuid.New().String()
		}
		return s.db.WithContext(ctx).Create(sec).Error
	}
	if err != nil {
		return err
	}
	sec.ID = existing.ID
	return s.db.WithContext(ctx).Model(&existing).Updates(map[string]any{"sealed": sec.Sealed, "updated_at": time.Now().UTC()}).Error
}

// Every secret on the instance, for resealing
func (s *Store) ListAllActionsSecrets(ctx context.Context) ([]*db.ActionsSecret, error) {
	var out []*db.ActionsSecret
	err := s.db.WithContext(ctx).Find(&out).Error
	return out, err
}

func (s *Store) SetActionsSecretSealed(ctx context.Context, id string, sealed []byte) error {
	return s.db.WithContext(ctx).Model(&db.ActionsSecret{}).Where("id = ?", id).Updates(map[string]any{"sealed": sealed, "updated_at": time.Now().UTC()}).Error
}

func (s *Store) ListActionsSecrets(ctx context.Context, scopeKind, scopeID string, limit, offset int) ([]*db.ActionsSecret, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ActionsSecret{}).Where("scope_kind = ? AND scope_id = ?", scopeKind, scopeID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.ActionsSecret
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("name ASC").Find(&out).Error
	return out, total, err
}

// Every secret visible to a repo, repo over org over system
func (s *Store) ListActionsSecretsForRepo(ctx context.Context, repoID, orgID string) ([]*db.ActionsSecret, error) {
	var out []*db.ActionsSecret
	err := s.db.WithContext(ctx).Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)",
		db.ActionsScopeSystem, "", db.ActionsScopeOrg, orgID, db.ActionsScopeRepo, repoID).Find(&out).Error
	return out, err
}

func (s *Store) DeleteActionsSecret(ctx context.Context, scopeKind, scopeID, name string) error {
	return s.db.WithContext(ctx).Where("scope_kind = ? AND scope_id = ? AND name = ?", scopeKind, scopeID, name).Delete(&db.ActionsSecret{}).Error
}

func (s *Store) UpsertActionsVariable(ctx context.Context, v *db.ActionsVariable) error {
	var existing db.ActionsVariable
	err := s.db.WithContext(ctx).First(&existing, "scope_kind = ? AND scope_id = ? AND name = ?", v.ScopeKind, v.ScopeID, v.Name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if v.ID == "" {
			v.ID = uuid.New().String()
		}
		return s.db.WithContext(ctx).Create(v).Error
	}
	if err != nil {
		return err
	}
	v.ID = existing.ID
	return s.db.WithContext(ctx).Model(&existing).Updates(map[string]any{"value": v.Value, "updated_at": time.Now().UTC()}).Error
}

func (s *Store) ListActionsVariables(ctx context.Context, scopeKind, scopeID string, limit, offset int) ([]*db.ActionsVariable, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ActionsVariable{}).Where("scope_kind = ? AND scope_id = ?", scopeKind, scopeID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.ActionsVariable
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("name ASC").Find(&out).Error
	return out, total, err
}

func (s *Store) ListActionsVariablesForRepo(ctx context.Context, repoID, orgID string) ([]*db.ActionsVariable, error) {
	var out []*db.ActionsVariable
	err := s.db.WithContext(ctx).Where("(scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?) OR (scope_kind = ? AND scope_id = ?)",
		db.ActionsScopeSystem, "", db.ActionsScopeOrg, orgID, db.ActionsScopeRepo, repoID).Find(&out).Error
	return out, err
}

func (s *Store) DeleteActionsVariable(ctx context.Context, scopeKind, scopeID, name string) error {
	return s.db.WithContext(ctx).Where("scope_kind = ? AND scope_id = ? AND name = ?", scopeKind, scopeID, name).Delete(&db.ActionsVariable{}).Error
}

// ── Run artifacts ────────────────────────────────────────────────────────

func (s *Store) CreateActionsArtifact(ctx context.Context, a *db.ActionsArtifact) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *Store) ListActionsArtifacts(ctx context.Context, runID string) ([]*db.ActionsArtifact, error) {
	var out []*db.ActionsArtifact
	err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) ListExpiredActionsArtifacts(ctx context.Context, limit int) ([]*db.ActionsArtifact, error) {
	var out []*db.ActionsArtifact
	err := s.db.WithContext(ctx).Where("expires_at IS NOT NULL AND expires_at < ?", time.Now().UTC()).Limit(limit).Find(&out).Error
	return out, err
}

func (s *Store) DeleteActionsArtifact(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.ActionsArtifact{}, "id = ?", id).Error
}

// ── Scheduler and retention support ──────────────────────────────────────

func (s *Store) ListRunningActionsJobs(ctx context.Context) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).Where("status = ?", "running").Find(&out).Error
	return out, err
}

func (s *Store) ListActionsJobsByRunner(ctx context.Context, runnerID, status string) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	q := s.db.WithContext(ctx).Where("runner_id = ?", runnerID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Find(&out).Error
	return out, err
}

func (s *Store) CountActionsJobsByLogDigest(ctx context.Context, digest string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.ActionsJob{}).Where("log_digest = ?", digest).Count(&n).Error
	return n, err
}

// Jobs of finished runs older than the cutoff that still hold logs
func (s *Store) ListActionsJobsWithLogsBefore(ctx context.Context, repoID string, cutoff time.Time, limit int) ([]*db.ActionsJob, error) {
	var out []*db.ActionsJob
	err := s.db.WithContext(ctx).
		Where("run_id IN (SELECT id FROM actions_runs WHERE repo_id = ? AND status = ? AND finished_at < ?)", repoID, "completed", cutoff).
		Where("log_chunks > 0 OR log_digest <> ''").
		Order("created_at ASC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *Store) PruneActionsRunnerTokens(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("expires_at < ? OR used_at IS NOT NULL", time.Now().UTC().Add(-24*time.Hour)).Delete(&db.ActionsRunnerToken{}).Error
}

func (s *Store) GetActionsArtifact(ctx context.Context, id string) (*db.ActionsArtifact, error) {
	var a db.ActionsArtifact
	err := s.db.WithContext(ctx).First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}
