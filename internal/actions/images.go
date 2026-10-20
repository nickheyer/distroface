package actions

import (
	"context"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
)

// RecordJobImage stores a tag pushed with a job token on its run
func (m *Manager) RecordJobImage(ctx context.Context, jobID string, img *db.Repository, tag, digest string) {
	if jobID == "" || img == nil || tag == "" {
		return
	}
	job, err := m.store.GetActionsJob(ctx, jobID)
	if err != nil || job == nil {
		return
	}
	run, err := m.store.GetActionsRunByID(ctx, job.RunID)
	if err != nil || run == nil {
		return
	}
	row := &db.ActionsRunImage{RunID: run.ID, JobID: job.ID, ImageRepoID: img.ID, Tag: tag, Digest: digest}
	if err := m.store.UpsertActionsRunImage(ctx, row); err != nil {
		m.log.Warn("actions: record image %s/%s:%s for run %s: %v", img.Namespace, img.Name, tag, run.ID, err)
		return
	}
	m.log.Info("actions: run %s pushed %s/%s:%s", run.ID, img.Namespace, img.Name, tag)
	ref := plumbing.ReferenceName(run.Ref)
	if !ref.IsTag() {
		return
	}
	rel, err := m.store.GetGitRelease(ctx, run.RepoID, ref.Short())
	if err != nil || rel == nil {
		return
	}
	m.pinImage(ctx, rel, img.ID, tag, digest)
}

// PinReleaseImages attaches every image pushed by runs of the release tag
func (m *Manager) PinReleaseImages(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease) {
	if repo == nil || rel == nil || strings.TrimSpace(rel.Tag) == "" {
		return
	}
	images, err := m.store.ListActionsRunImagesForRef(ctx, repo.ID, plumbing.NewTagReferenceName(rel.Tag).String())
	if err != nil {
		return
	}
	for _, img := range images {
		m.pinImage(ctx, rel, img.ImageRepoID, img.Tag, img.Digest)
	}
}

// Pins one digest, the store keeps the first row per tag
func (m *Manager) pinImage(ctx context.Context, rel *db.GitRelease, imageRepoID, tag, digest string) {
	row := &db.GitReleaseImage{ID: uuid.New().String(), ReleaseID: rel.ID, ImageRepoID: imageRepoID, Tag: tag, Digest: digest}
	if err := m.store.AttachGitReleaseImage(ctx, row); err != nil {
		m.log.Warn("actions: pin image %s:%s on release %s: %v", imageRepoID, tag, rel.ID, err)
	}
}

// RunImages lists the tags a run pushed
func (m *Manager) RunImages(ctx context.Context, run *db.ActionsRun) ([]*db.ActionsRunImage, error) {
	return m.store.ListActionsRunImages(ctx, run.ID)
}
