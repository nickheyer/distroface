package actions

import (
	"context"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Parsed schedules of one repository at a default branch commit
type scheduleEntry struct {
	sha     plumbing.Hash
	branch  string
	checked time.Time
	items   []scheduledWorkflow
}

// Ref events invalidate entries, this bounds staleness without them
const scheduleRecheck = 10 * time.Minute

// Ref movement drops the cached entry so the next tick reopens the repo
func (m *Manager) forgetSchedules(repoID string) {
	m.schedMu.Lock()
	delete(m.schedCache, repoID)
	m.schedMu.Unlock()
}

type scheduledWorkflow struct {
	file  *WorkflowFile
	crons []*Cron
	specs []string
}

// ── Schedules ────────────────────────────────────────────────────────────

func (m *Manager) scheduleLoop(ctx context.Context) {
	next := time.Now().Truncate(time.Minute).Add(time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		m.runSchedules(ctx, next.UTC())
		next = next.Add(time.Minute)
		if time.Now().After(next) {
			next = time.Now().Truncate(time.Minute).Add(time.Minute)
		}
	}
}

// Fires every cron matching the minute across enabled repositories
func (m *Manager) runSchedules(ctx context.Context, now time.Time) {
	repos, err := m.store.ListAllGitRepositories(ctx)
	if err != nil {
		m.log.Error("actions: list repositories: %v", err)
		return
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		if repo.IsArchived {
			continue
		}
		// Most repositories schedule nothing, a fresh entry says so without opening them
		m.schedMu.Lock()
		entry := m.schedCache[repo.ID]
		m.schedMu.Unlock()
		if entry != nil && entry.branch == repo.DefaultBranch && now.Sub(entry.checked) < scheduleRecheck && len(entry.items) == 0 {
			continue
		}
		if !m.Enabled(ctx, repo) {
			continue
		}
		if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR && !m.Settings(ctx, repo).GetRunOnMirror() {
			continue
		}
		m.runRepoSchedules(ctx, repo, entry, now)
	}
}

func (m *Manager) runRepoSchedules(ctx context.Context, repo *db.GitRepository, entry *scheduleEntry, now time.Time) {
	fresh := entry != nil && entry.branch == repo.DefaultBranch && now.Sub(entry.checked) < scheduleRecheck
	if !fresh {
		r, err := m.openRepo(repo)
		if err != nil {
			return
		}
		defer r.Close()
		head := r.BranchHash(repo.DefaultBranch)
		if head.IsZero() {
			return
		}
		entry = m.refreshSchedules(ctx, repo, r, head, now)
	}
	if len(entry.items) == 0 {
		return
	}
	head := entry.sha
	disabled, _ := m.store.GetDisabledWorkflows(ctx, repo.ID)
	for _, sw := range entry.items {
		if disabled[sw.file.Path] {
			continue
		}
		for i, c := range sw.crons {
			if !c.Matches(now) {
				continue
			}
			in := &triggerInput{
				Event: "schedule", Ref: "refs/heads/" + repo.DefaultBranch, SHA: head.String(), OnlyPath: sw.file.Path,
				Payload: map[string]any{"schedule": sw.specs[i], "repository": m.repoPayload(ctx, repo)},
			}
			if _, err := m.createRun(ctx, repo, sw.file, in); err != nil {
				m.log.Error("actions: schedule %s/%s %s: %v", repo.Namespace, repo.Name, sw.file.Path, err)
			}
			break
		}
	}
}

// Reparses workflow files when the default branch moved, otherwise stamps the entry
func (m *Manager) refreshSchedules(ctx context.Context, repo *db.GitRepository, r *gitrepo.Repo, head plumbing.Hash, now time.Time) *scheduleEntry {
	m.schedMu.Lock()
	entry := m.schedCache[repo.ID]
	m.schedMu.Unlock()
	if entry == nil || entry.sha != head || entry.branch != repo.DefaultBranch {
		entry = &scheduleEntry{sha: head, branch: repo.DefaultBranch}
		for _, f := range loadWorkflowFiles(ctx, r, head) {
			if f.Err != nil || f.Workflow == nil {
				continue
			}
			spec, ok := f.Workflow.On["schedule"]
			if !ok || len(spec.Crons) == 0 {
				continue
			}
			sw := scheduledWorkflow{file: f}
			for _, c := range spec.Crons {
				if parsed, err := ParseCron(c); err == nil {
					sw.crons = append(sw.crons, parsed)
					sw.specs = append(sw.specs, c)
				}
			}
			if len(sw.crons) > 0 {
				entry.items = append(entry.items, sw)
			}
		}
	}
	entry.checked = now
	m.schedMu.Lock()
	m.schedCache[repo.ID] = entry
	m.schedMu.Unlock()
	return entry
}

// ── Reaper ───────────────────────────────────────────────────────────────

func (m *Manager) reaperLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapJobs(ctx)
		}
	}
}

// Queued jobs no runner picked up within this long fail
const queuedJobMaxAge = 24 * time.Hour

// One job the reaper decided to fail and why
type reapTarget struct {
	job        *db.ActionsJob
	conclusion string
	reason     string
}

// Fails jobs past their timeout, whose runner went silent, or that no runner ever took
func (m *Manager) reapJobs(ctx context.Context) {
	now := time.Now().UTC()
	var targets []reapTarget
	running, err := m.store.ListRunningActionsJobs(ctx)
	if err != nil {
		return
	}
	stale := map[string]bool{}
	if list, err := m.store.ListStaleRunningJobs(ctx, now.Add(-runnerStaleWindow)); err == nil {
		for _, j := range list {
			stale[j.ID] = true
		}
	}
	for _, j := range running {
		switch {
		case stale[j.ID]:
			targets = append(targets, reapTarget{j, ConclusionFailure, "runner stopped reporting"})
		case j.StartedAt != nil:
			timeout := defaultJobTimeout
			if def, err := decodeDefinition(j); err == nil && def.Job.TimeoutMinutes > 0 {
				timeout = def.Job.TimeoutMinutes
			}
			if now.Sub(*j.StartedAt) > time.Duration(timeout)*time.Minute {
				targets = append(targets, reapTarget{j, ConclusionTimedOut, "job exceeded its timeout"})
			}
		}
	}
	if old, err := m.store.ListQueuedActionsJobsBefore(ctx, now.Add(-queuedJobMaxAge)); err == nil {
		for _, j := range old {
			targets = append(targets, reapTarget{j, ConclusionFailure, "no runner picked the job up within 24 hours"})
		}
	}
	touched := map[string]*db.ActionsRun{}
	repos := map[string]*db.GitRepository{}
	for _, t := range targets {
		run := touched[t.job.RunID]
		if run == nil {
			run, _ = m.store.GetActionsRunByID(ctx, t.job.RunID)
			if run == nil {
				continue
			}
			touched[t.job.RunID] = run
		}
		repo := repos[run.RepoID]
		if repo == nil {
			repo, _ = m.store.GetGitRepositoryByID(ctx, run.RepoID)
			if repo == nil {
				continue
			}
			repos[run.RepoID] = repo
		}
		m.failJob(ctx, repo, run, t.job, t.conclusion, t.reason)
		m.compactJobLogs(ctx, t.job)
		if t.conclusion != ConclusionCancelled {
			m.failFast(ctx, repo, run, t.job)
		}
	}
	for _, run := range touched {
		if repo := repos[run.RepoID]; repo != nil {
			if fresh, _ := m.store.GetActionsRunByID(ctx, run.ID); fresh != nil {
				run = fresh
			}
			m.advanceRun(ctx, repo, run)
		}
	}
	if len(targets) > 0 {
		m.notifyRunners()
	}
	_ = m.store.PruneActionsRunnerTokens(ctx)
}

// ── Retention ────────────────────────────────────────────────────────────

func (m *Manager) retentionLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	m.applyRetention(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.applyRetention(ctx)
		}
	}
}

// Expires artifacts and prunes logs past the configured days
func (m *Manager) applyRetention(ctx context.Context) {
	m.pruneCaches(ctx)
	m.expireArtifacts(ctx)
	repos, err := m.store.ListAllGitRepositories(ctx)
	if err != nil {
		return
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		days := m.Settings(ctx, repo).GetLogRetentionDays()
		if days <= 0 {
			continue
		}
		cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		jobs, err := m.store.ListActionsJobsWithLogsBefore(ctx, repo.ID, cutoff, 500)
		if err != nil {
			continue
		}
		for _, j := range jobs {
			m.dropJobLogs(ctx, j)
		}
	}
}
