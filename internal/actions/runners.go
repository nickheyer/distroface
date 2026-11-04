package actions

import (
	"context"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
)

// Online, busy, or offline from the last heartbeat
func RunnerStatus(r *db.ActionsRunner) string {
	if r.LastSeen == nil || time.Since(*r.LastSeen) > runnerOnlineWindow {
		return "offline"
	}
	if r.Busy > 0 {
		return "busy"
	}
	return "online"
}

// Decodes the stored label list
func RunnerLabels(r *db.ActionsRunner) []string {
	return orEmpty(decodeStrings(r.Labels))
}

// Whether the runner announced a protocol feature
func RunnerHasCapability(r *db.ActionsRunner, capability string) bool {
	for _, c := range decodeStrings(r.Capabilities) {
		if strings.EqualFold(c, capability) {
			return true
		}
	}
	return false
}

// An online runner in scope with the labels and capability
func (m *Manager) RunnerOnlineFor(ctx context.Context, repo *db.GitRepository, labels []string, capability string) (online bool, registered bool) {
	runners, err := m.store.ListActionsRunnersForRepo(ctx, repo.ID, m.repoOrgID(ctx, repo))
	if err != nil {
		return true, true
	}
	for _, r := range runners {
		if !labelsSubset(labels, decodeStrings(r.Labels)) {
			continue
		}
		registered = true
		if RunnerStatus(r) == "offline" {
			continue
		}
		if capability != "" && !RunnerHasCapability(r, capability) {
			continue
		}
		return true, true
	}
	return false, registered
}

// Fails every running job on a runner that is gone
func (m *Manager) FailJobsOnRunner(ctx context.Context, runnerID, reason string) {
	m.failRunnerJobs(ctx, runnerID, nil, time.Time{}, reason)
}

// Unreported running jobs fail now instead of at timeout
func (m *Manager) FailDroppedJobs(ctx context.Context, runnerID string, reported []string) {
	m.failRunnerJobs(ctx, runnerID, reported, time.Now().UTC().Add(-droppedJobGrace), "runner stopped executing the job")
}

// Zero cutoff fails every unreported job, else only older ones
func (m *Manager) failRunnerJobs(ctx context.Context, runnerID string, reported []string, cutoff time.Time, reason string) {
	jobs, err := m.store.ListActionsJobsByRunner(ctx, runnerID, StatusRunning)
	if err != nil || len(jobs) == 0 {
		return
	}
	keep := make(map[string]bool, len(reported))
	for _, id := range reported {
		keep[id] = true
	}
	for _, j := range jobs {
		if keep[j.ID] {
			continue
		}
		if !cutoff.IsZero() && (j.StartedAt == nil || j.StartedAt.After(cutoff)) {
			continue
		}
		run, _ := m.store.GetActionsRunByID(ctx, j.RunID)
		if run == nil {
			continue
		}
		repo, _ := m.store.GetGitRepositoryByID(ctx, run.RepoID)
		if repo == nil {
			continue
		}
		m.failJob(ctx, repo, run, j, ConclusionFailure, reason)
		m.advanceRun(ctx, repo, run)
	}
}
