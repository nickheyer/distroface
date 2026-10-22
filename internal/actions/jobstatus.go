package actions

import (
	"context"
	"time"
)

// Registry calls ask per blob, a short memo spares the database
const (
	jobStatusTTL     = 5 * time.Second
	jobStatusMaxSize = 4096
)

type jobStatusEntry struct {
	running bool
	until   time.Time
}

// JobRunning reports whether a job still executes, briefly memoized
func (m *Manager) JobRunning(ctx context.Context, jobID string) bool {
	now := time.Now()
	m.statusMu.Lock()
	if e, ok := m.jobStatus[jobID]; ok && now.Before(e.until) {
		m.statusMu.Unlock()
		return e.running
	}
	m.statusMu.Unlock()
	job, err := m.store.GetActionsJob(ctx, jobID)
	if err != nil {
		return false
	}
	running := job != nil && job.Status == StatusRunning
	m.statusMu.Lock()
	if len(m.jobStatus) >= jobStatusMaxSize {
		for id, e := range m.jobStatus {
			if !now.Before(e.until) {
				delete(m.jobStatus, id)
			}
		}
	}
	if len(m.jobStatus) < jobStatusMaxSize {
		m.jobStatus[jobID] = jobStatusEntry{running: running, until: now.Add(jobStatusTTL)}
	}
	m.statusMu.Unlock()
	return running
}

// Completion drops the memo so the registry refuses at once
func (m *Manager) forgetJobStatus(jobID string) {
	m.statusMu.Lock()
	delete(m.jobStatus, jobID)
	m.statusMu.Unlock()
}
