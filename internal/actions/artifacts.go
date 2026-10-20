package actions

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// ArtifactVersion names the package version holding a run's uploads
func ArtifactVersion(run *db.ActionsRun) string {
	return fmt.Sprintf("actions-%d", run.Number)
}

// Package linked to the repository, created on first use
func (m *Manager) ensurePackage(ctx context.Context, repo *db.GitRepository) (*db.ArtifactRepository, error) {
	pkg, err := m.store.GetArtifactRepository(ctx, repo.Namespace, repo.Name)
	if err != nil {
		return nil, err
	}
	if pkg != nil {
		if pkg.GitRepoID == nil || *pkg.GitRepoID != repo.ID {
			id := repo.ID
			if err := m.store.SetArtifactRepositoryGitRepo(ctx, pkg.ID, &id); err == nil {
				pkg.GitRepoID = &id
			}
		}
		return pkg, nil
	}
	id := repo.ID
	pkg = &db.ArtifactRepository{
		Namespace: repo.Namespace, Name: repo.Name, Description: "Packages of " + repo.Namespace + "/" + repo.Name,
		OwnerID: repo.OwnerID, IsPrivate: repo.IsPrivate, Type: v1.ArtifactRepoType_ARTIFACT_REPO_TYPE_FILE, GitRepoID: &id,
	}
	if err := m.store.CreateArtifactRepository(ctx, pkg); err != nil {
		return nil, err
	}
	return pkg, nil
}

// BeginArtifactUpload opens an upload session in the linked package
func (m *Manager) BeginArtifactUpload(ctx context.Context, runner *db.ActionsRunner, jobID, name string) (string, *db.ArtifactRepository, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil, fmt.Errorf("%w: artifact name is required", ErrInvalid)
	}
	job, _, repo, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return "", nil, err
	}
	if job.Status != StatusRunning {
		return "", nil, fmt.Errorf("%w: job is not running", ErrConflict)
	}
	pkg, err := m.ensurePackage(ctx, repo)
	if err != nil {
		return "", nil, err
	}
	uploadID, err := m.artifacts.Blobs().InitiateUpload()
	if err != nil {
		return "", nil, err
	}
	return uploadID, pkg, nil
}

// FinishArtifactUpload completes the upload and records the run artifact
func (m *Manager) FinishArtifactUpload(ctx context.Context, runner *db.ActionsRunner, jobID, name, relPath, uploadID string) (*db.ActionsArtifact, *db.Artifact, *db.ArtifactRepository, error) {
	job, run, repo, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return nil, nil, nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, nil, fmt.Errorf("%w: artifact name is required", ErrInvalid)
	}
	if uploadID == "" {
		return nil, nil, nil, fmt.Errorf("%w: upload id is required", ErrInvalid)
	}
	pkg, err := m.ensurePackage(ctx, repo)
	if err != nil {
		return nil, nil, nil, err
	}
	relPath = strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(relPath)), "/")
	if relPath == "" || relPath == "." {
		relPath = artifacts.SanitizePath(name)
	}
	fullPath := artifacts.SanitizePath(name) + "/" + relPath
	props := map[string]string{"actions.run": run.ID, "actions.artifact": name, "actions.job": job.ID}
	art, err := m.artifacts.CompleteUpload(ctx, pkg, uploadID, ArtifactVersion(run), fullPath, "{}", props)
	if err != nil {
		return nil, nil, nil, err
	}
	row := &db.ActionsArtifact{RunID: run.ID, ArtifactID: art.ID, Name: name}
	if days := m.Settings(ctx, repo).GetArtifactRetentionDays(); days > 0 {
		t := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		row.ExpiresAt = &t
	}
	if err := m.store.CreateActionsArtifact(ctx, row); err != nil {
		return nil, nil, nil, err
	}
	return row, art, pkg, nil
}

// ArtifactDownloadPath is the data plane url of one artifact file
func ArtifactDownloadPath(pkg *db.ArtifactRepository, art *db.Artifact) string {
	return fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/%s/%s", pkg.Namespace, pkg.Name, art.Version, art.Path)
}
