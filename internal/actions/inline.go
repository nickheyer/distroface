package actions

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

// StartInlineRun creates a run from workflow source that lives outside the repository,
// secrets stay out of the job and its token is read only
func (m *Manager) StartInlineRun(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser, path, source, branch, sha string, payload map[string]any) (*db.ActionsRun, error) {
	if !m.Enabled(ctx, repo) {
		return nil, ErrDisabled
	}
	wf, err := ParseWorkflow([]byte(source))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	r, err := m.openRepo(repo)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if branch == "" {
		branch = repo.DefaultBranch
	}
	var commit plumbing.Hash
	if sha != "" {
		commit, err = r.Resolve(sha)
	} else {
		commit, err = r.Resolve(branch)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s not found", ErrNotFound, branch)
	}
	fullRef := branch
	if !strings.HasPrefix(fullRef, "refs/") {
		fullRef = "refs/heads/" + branch
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["ref"] = fullRef
	payload["repository"] = m.repoPayload(ctx, repo)
	if actor != nil {
		payload["sender"] = map[string]any{"login": actor.Username}
	}
	in := &triggerInput{
		Event: "workflow_dispatch", Ref: fullRef, SHA: commit.String(), FileCommit: commit, Actor: actor,
		Payload: payload, Inputs: map[string]any{}, OnlyPath: path,
		// Inline runs execute model chosen commands, they never see secrets
		FromFork: true,
	}
	return m.createRun(ctx, repo, &WorkflowFile{Path: path, Source: source, Workflow: wf}, in)
}

// GetRun loads a run by id
func (m *Manager) GetRun(ctx context.Context, id string) (*db.ActionsRun, error) {
	return m.store.GetActionsRunByID(ctx, id)
}

// RunJobs lists the jobs of a run
func (m *Manager) RunJobs(ctx context.Context, runID string) ([]*db.ActionsJob, error) {
	return m.store.ListActionsJobs(ctx, runID)
}

// RunsForCommit lists runs that executed on one commit of a repository
func (m *Manager) RunsForCommit(ctx context.Context, repo *db.GitRepository, sha string, limit int) ([]*db.ActionsRun, error) {
	runs, _, err := m.store.ListActionsRuns(ctx, stores.ActionsRunListOptions{RepoID: repo.ID, SHA: sha, Limit: limit})
	return runs, err
}

// JobLog returns the whole log of a job from live chunks or the compacted blob
func (m *Manager) JobLog(ctx context.Context, job *db.ActionsJob) ([]byte, error) {
	if job.LogDigest != "" {
		return m.ReadCompactedLog(job)
	}
	chunks, err := m.store.ListActionsJobLogs(ctx, job.ID, 0, 0)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(c.Data)
	}
	return buf.Bytes(), nil
}

// RunURL is the page of a run
func (m *Manager) RunURL(ctx context.Context, repo *db.GitRepository, runID string) string {
	return m.runURL(ctx, repo, runID)
}

// Whether a registered runner carries every label
func (m *Manager) RunnerAvailable(ctx context.Context, repo *db.GitRepository, labels []string) bool {
	return m.runnerRegisteredFor(ctx, repo, labels)
}
