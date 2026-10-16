package actions

import (
	"context"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Parsed schedules of one repository at a default branch commit
type scheduleEntry struct {
	sha   plumbing.Hash
	items []scheduledWorkflow
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
		if repo.IsArchived || !m.Enabled(ctx, repo) {
			continue
		}
		if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR && !m.Settings(ctx, repo).GetRunOnMirror() {
			continue
		}
		m.runRepoSchedules(ctx, repo, now)
	}
}

func (m *Manager) runRepoSchedules(ctx context.Context, repo *db.GitRepository, now time.Time) {
	r, err := m.openRepo(repo)
	if err != nil {
		return
	}
	defer r.Close()
	head := r.BranchHash(repo.DefaultBranch)
	if head.IsZero() {
		return
	}
	m.schedMu.Lock()
	entry := m.schedCache[repo.ID]
	m.schedMu.Unlock()
	if entry == nil || entry.sha != head {
		entry = &scheduleEntry{sha: head}
		files, err := loadWorkflowFiles(ctx, r, head)
		if err == nil {
			for _, f := range files {
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
		m.schedMu.Lock()
		m.schedCache[repo.ID] = entry
		m.schedMu.Unlock()
	}
	if len(entry.items) == 0 {
		return
	}
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

// Fails jobs past their timeout or whose runner went silent
func (m *Manager) reapJobs(ctx context.Context) {
	now := time.Now().UTC()
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
	touched := map[string]*db.ActionsRun{}
	repos := map[string]*db.GitRepository{}
	for _, j := range running {
		reason, conclusion := "", ""
		switch {
		case stale[j.ID]:
			reason, conclusion = "runner stopped reporting", ConclusionFailure
		case j.StartedAt != nil:
			timeout := defaultJobTimeout
			if def, err := decodeDefinition(j); err == nil && def.Job.TimeoutMinutes > 0 {
				timeout = def.Job.TimeoutMinutes
			}
			if now.Sub(*j.StartedAt) > time.Duration(timeout)*time.Minute {
				reason, conclusion = "job exceeded its timeout", ConclusionTimedOut
			}
		}
		if reason == "" {
			continue
		}
		run := touched[j.RunID]
		if run == nil {
			run, _ = m.store.GetActionsRunByID(ctx, j.RunID)
			if run == nil {
				continue
			}
			touched[j.RunID] = run
		}
		repo := repos[run.RepoID]
		if repo == nil {
			repo, _ = m.store.GetGitRepositoryByID(ctx, run.RepoID)
			if repo == nil {
				continue
			}
			repos[run.RepoID] = repo
		}
		m.failJob(ctx, repo, run, j, conclusion, reason)
		m.compactJobLogs(ctx, j)
	}
	for _, run := range touched {
		if repo := repos[run.RepoID]; repo != nil {
			if fresh, _ := m.store.GetActionsRunByID(ctx, run.ID); fresh != nil {
				run = fresh
			}
			m.advanceRun(ctx, repo, run)
		}
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
	if expired, err := m.store.ListExpiredActionsArtifacts(ctx, 500); err == nil {
		for _, a := range expired {
			if art, _ := m.store.GetArtifact(ctx, a.ArtifactID); art != nil {
				if err := m.artifacts.DeleteArtifact(ctx, art); err != nil {
					m.log.Warn("actions: expire artifact %s: %v", a.ArtifactID, err)
					continue
				}
			}
			_ = m.store.DeleteActionsArtifact(ctx, a.ID)
		}
	}
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
