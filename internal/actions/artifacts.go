package actions

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Expired rows deleted per sweep pass
const expireBatch = 500

// ArtifactVersion names the package version holding a run's uploads
func ArtifactVersion(run *db.ActionsRun) string {
	return fmt.Sprintf("actions-%d", run.Number)
}

// Zip holding every file of one named artifact
func artifactZipPath(name string) string {
	return artifacts.SanitizePath(name) + ".zip"
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

// Existing rows of the name, a conflict unless the caller overwrites
func (m *Manager) existingArtifact(ctx context.Context, run *db.ActionsRun, name string, overwrite bool) ([]*db.ActionsArtifact, error) {
	rows, err := m.store.ListActionsArtifactsByName(ctx, run.ID, name)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && !overwrite {
		return nil, fmt.Errorf("%w: artifact %q already exists in this run, set overwrite to replace it", ErrConflict, name)
	}
	return rows, nil
}

// BeginArtifactUpload opens an upload session in the linked package
func (m *Manager) BeginArtifactUpload(ctx context.Context, runner *db.ActionsRunner, jobID, name string, overwrite bool) (string, *db.ArtifactRepository, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("%w: artifact name is required", ErrInvalid)
	}
	job, run, repo, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return "", nil, err
	}
	if job.Status != StatusRunning {
		return "", nil, fmt.Errorf("%w: job is not running", ErrConflict)
	}
	if _, err := m.existingArtifact(ctx, run, name, overwrite); err != nil {
		return "", nil, err
	}
	if job.TokenID == "" {
		return "", nil, fmt.Errorf("%w: job has no token to upload with", ErrDenied)
	}
	pkg, err := m.ensurePackage(ctx, repo)
	if err != nil {
		return "", nil, err
	}
	// Chunks arrive on the data plane under the job token, bind the session to it
	uploadID, err := m.artifacts.Blobs().InitiateOwnedUpload(auth.TokenUploadOwner(job.TokenID))
	if err != nil {
		return "", nil, err
	}
	return uploadID, pkg, nil
}

// FinishArtifactUpload stores the zip and records it on the run
func (m *Manager) FinishArtifactUpload(ctx context.Context, runner *db.ActionsRunner, jobID, name, uploadID string, fileCount, retentionDays int32, overwrite bool) (*RunArtifact, error) {
	job, run, repo, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != StatusRunning {
		return nil, fmt.Errorf("%w: job is not running", ErrConflict)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: artifact name is required", ErrInvalid)
	}
	if uploadID == "" {
		return nil, fmt.Errorf("%w: upload id is required", ErrInvalid)
	}
	if err := m.artifacts.Blobs().VerifyUploadOwner(uploadID, auth.TokenUploadOwner(job.TokenID)); err != nil {
		return nil, err
	}
	previous, err := m.existingArtifact(ctx, run, name, overwrite)
	if err != nil {
		return nil, err
	}
	pkg, err := m.ensurePackage(ctx, repo)
	if err != nil {
		return nil, err
	}
	props := map[string]string{"actions.run": run.ID, "actions.artifact": name, "actions.job": job.ID}
	art, err := m.artifacts.CompleteWorkflowUpload(ctx, pkg, uploadID, ArtifactVersion(run), artifactZipPath(name), props)
	if err != nil {
		return nil, err
	}
	for _, old := range previous {
		m.dropRunArtifact(ctx, old, art.ID)
	}
	row := &db.ActionsArtifact{RunID: run.ID, JobID: job.ID, ArtifactID: art.ID, Name: name, FileCount: fileCount}
	days := m.Settings(ctx, repo).GetArtifactRetentionDays()
	if retentionDays > 0 && (days <= 0 || retentionDays < days) {
		days = retentionDays
	}
	if days > 0 {
		t := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		row.ExpiresAt = &t
	}
	if err := m.store.CreateActionsArtifact(ctx, row); err != nil {
		return nil, err
	}
	return &RunArtifact{Row: row, Files: []*db.Artifact{art}, Size: art.Size, FileCount: fileCount, Pkg: pkg}, nil
}

// Removes a run row and its stored file unless the file was reused
func (m *Manager) dropRunArtifact(ctx context.Context, row *db.ActionsArtifact, keep string) {
	if row.ArtifactID != keep {
		if art, _ := m.store.GetArtifact(ctx, row.ArtifactID); art != nil {
			if err := m.artifacts.DeleteArtifact(ctx, art); err != nil {
				m.log.Warn("actions: drop artifact %s: %v", row.ArtifactID, err)
				return
			}
		}
	}
	_ = m.store.DeleteActionsArtifact(ctx, row.ID)
}

// Deletes every expired upload, draining in batches
func (m *Manager) expireArtifacts(ctx context.Context) {
	for ctx.Err() == nil {
		expired, err := m.store.ListExpiredActionsArtifacts(ctx, expireBatch)
		if err != nil || len(expired) == 0 {
			return
		}
		for _, a := range expired {
			if art, _ := m.store.GetArtifact(ctx, a.ArtifactID); art != nil {
				if err := m.artifacts.DeleteArtifact(ctx, art); err != nil {
					m.log.Warn("actions: expire artifact %s: %v", a.ArtifactID, err)
					continue
				}
			}
			_ = m.store.DeleteActionsArtifact(ctx, a.ID)
		}
		if len(expired) < expireBatch {
			return
		}
	}
}

// RunArtifact is one named upload of a run, legacy uploads span many files
type RunArtifact struct {
	Row       *db.ActionsArtifact // Newest row of the name
	Files     []*db.Artifact      // Stored rows still present, one zip for current uploads
	Size      int64
	FileCount int32
	Pkg       *db.ArtifactRepository
}

// DownloadPath streams the zip, legacy file sets are archived on the fly
func (a *RunArtifact) DownloadPath() string {
	if a.Pkg == nil || len(a.Files) == 0 {
		return ""
	}
	if len(a.Files) == 1 && a.Files[0].Path == artifactZipPath(a.Row.Name) {
		return ArtifactDownloadPath(a.Pkg, a.Files[0])
	}
	q := url.Values{"kind": {"workflow"}, "actions.run": {a.Row.RunID}, "actions.artifact": {a.Row.Name}, "format": {"zip"}, "num": {"100000"}}
	return fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/query?%s", a.Pkg.Namespace, a.Pkg.Name, q.Encode())
}

// RunArtifacts groups a run's uploads by name in upload order
func (m *Manager) RunArtifacts(ctx context.Context, run *db.ActionsRun) ([]*RunArtifact, error) {
	rows, err := m.store.ListActionsArtifacts(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ArtifactID)
	}
	arts, err := m.store.GetArtifactsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*db.Artifact, len(arts))
	for _, a := range arts {
		byID[a.ID] = a
	}
	groups := groupRunArtifacts(rows, byID)
	pkgs := map[int64]*db.ArtifactRepository{}
	for _, g := range groups {
		if len(g.Files) == 0 {
			continue
		}
		id := g.Files[0].RepoID
		if _, ok := pkgs[id]; !ok {
			pkgs[id], _ = m.store.GetArtifactRepositoryByID(ctx, id)
		}
		g.Pkg = pkgs[id]
	}
	return groups, nil
}

// TaskArtifacts lists the run of a job the runner holds
func (m *Manager) TaskArtifacts(ctx context.Context, runner *db.ActionsRunner, jobID string) ([]*RunArtifact, error) {
	_, run, _, err := m.runnerJob(ctx, runner, jobID)
	if err != nil {
		return nil, err
	}
	return m.RunArtifacts(ctx, run)
}

// Rows arrive oldest first, the newest row of a name speaks for the group
func groupRunArtifacts(rows []*db.ActionsArtifact, arts map[string]*db.Artifact) []*RunArtifact {
	var order []*RunArtifact
	groups := map[string]*RunArtifact{}
	for _, row := range rows {
		g := groups[row.Name]
		if g == nil {
			g = &RunArtifact{}
			groups[row.Name] = g
			order = append(order, g)
		}
		g.Row = row
		art := arts[row.ArtifactID]
		if art == nil {
			continue
		}
		g.Files = append(g.Files, art)
		g.Size += art.Size
		if row.FileCount > 0 {
			g.FileCount += row.FileCount
		} else {
			g.FileCount++
		}
	}
	return order
}

// ArtifactDownloadPath is the data plane url of one artifact file
func ArtifactDownloadPath(pkg *db.ArtifactRepository, art *db.Artifact) string {
	return fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/%s/%s", pkg.Namespace, pkg.Name, art.Version, art.Path)
}
