package ghapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

const runScanCap = 2000

// Artifact routes answer 503 instead of pretending an empty instance
func (q *request) actionsReady() bool {
	if q.h.actions == nil {
		q.fail(http.StatusServiceUnavailable, "actions are not enabled on this instance")
		return false
	}
	return true
}

// Run by row id or by the integer GitHub consumers echo back
func (q *request) runByPath(repo *db.GitRepository) (*db.ActionsRun, bool) {
	id := q.r.PathValue("run_id")
	if run, _ := q.h.store.GetActionsRun(q.ctx, repo.ID, id); run != nil {
		return run, true
	}
	rows, _, err := q.h.store.ListActionsRuns(q.ctx, stores.ActionsRunListOptions{RepoID: repo.ID, Limit: runScanCap})
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	for _, run := range rows {
		if idMatches(id, run.ID) || strconv.FormatInt(actions.NumericID(run.ID), 10) == id || fmt.Sprint(run.Number) == id {
			return run, true
		}
	}
	q.notFound()
	return nil, false
}

func ghStatus(status string) string {
	switch status {
	case actions.StatusWaiting:
		return "waiting"
	case actions.StatusQueued:
		return "queued"
	case actions.StatusRunning:
		return "in_progress"
	case actions.StatusCompleted:
		return "completed"
	}
	return status
}

func ghConclusion(c string) any {
	if c == "" {
		return nil
	}
	return c
}

func (q *request) runJSON(repo *db.GitRepository, run *db.ActionsRun) obj {
	api := q.repoAPI(repo) + "/actions/runs/" + run.ID
	ref := plumbing.ReferenceName(run.Ref)
	branch := ref.Short()
	actor := q.ghost()
	if run.TriggerUserID != "" {
		actor = q.userByID(run.TriggerUserID)
	}
	updated := run.CreatedAt
	if run.FinishedAt != nil {
		updated = *run.FinishedAt
	} else if run.StartedAt != nil {
		updated = *run.StartedAt
	}
	return obj{
		"id": numID(run.ID), "node_id": "", "name": run.WorkflowName, "display_title": run.WorkflowName, "head_branch": branch,
		"head_sha": run.SHA, "path": run.WorkflowPath, "run_number": run.Number, "run_attempt": 1, "event": run.Event,
		"status": ghStatus(run.Status), "conclusion": ghConclusion(run.Conclusion), "workflow_id": numID(run.WorkflowPath),
		"url": api, "html_url": q.repoHTML(repo) + "/actions/runs/" + run.ID, "jobs_url": api + "/jobs", "logs_url": api + "/logs",
		"artifacts_url": api + "/artifacts", "cancel_url": api + "/cancel", "rerun_url": api + "/rerun", "workflow_url": q.repoAPI(repo) + "/actions/workflows/" + path.Base(run.WorkflowPath),
		"created_at": ts(run.CreatedAt), "updated_at": ts(updated), "run_started_at": tsPtr(run.StartedAt),
		"actor": actor, "triggering_actor": actor, "pull_requests": []obj{},
		"repository":  obj{"id": numID(repo.ID), "name": repo.Name, "full_name": repo.Namespace + "/" + repo.Name, "html_url": q.repoHTML(repo), "url": q.repoAPI(repo)},
		"_distroface": obj{"run_id": run.ID},
	}
}

// Step rows as the job stores them
type storedStep struct {
	Number     int32      `json:"number"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Conclusion string     `json:"conclusion,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (q *request) jobJSON(repo *db.GitRepository, run *db.ActionsRun, job *db.ActionsJob, runnerNames map[string]string) obj {
	var steps []storedStep
	_ = json.Unmarshal([]byte(job.Steps), &steps)
	stepRows := make([]obj, 0, len(steps))
	for _, s := range steps {
		stepRows = append(stepRows, obj{"name": s.Name, "number": s.Number, "status": ghStatus(s.Status), "conclusion": ghConclusion(s.Conclusion), "started_at": tsPtr(s.StartedAt), "completed_at": tsPtr(s.FinishedAt)})
	}
	runnerName := ""
	if job.RunnerID != "" {
		name, ok := runnerNames[job.RunnerID]
		if !ok {
			if r, _ := q.h.store.GetActionsRunner(q.ctx, job.RunnerID); r != nil {
				name = r.Name
			}
			runnerNames[job.RunnerID] = name
		}
		runnerName = name
	}
	var labels []string
	_ = json.Unmarshal([]byte(job.RunsOn), &labels)
	if labels == nil {
		labels = []string{}
	}
	api := q.repoAPI(repo) + "/actions/jobs/" + job.ID
	return obj{
		"id": numID(job.ID), "run_id": numID(run.ID), "run_url": q.repoAPI(repo) + "/actions/runs/" + run.ID, "node_id": "", "name": job.Name,
		"head_sha": run.SHA, "head_branch": plumbing.ReferenceName(run.Ref).Short(), "status": ghStatus(job.Status), "conclusion": ghConclusion(job.Conclusion),
		"started_at": tsPtr(job.StartedAt), "completed_at": tsPtr(job.FinishedAt), "created_at": ts(job.CreatedAt), "steps": stepRows,
		"labels": labels, "runner_name": runnerName, "runner_id": numID(job.RunnerID), "run_attempt": job.Attempt, "workflow_name": run.WorkflowName,
		"url": api, "html_url": q.repoHTML(repo) + "/actions/runs/" + run.ID + "/jobs/" + job.ID, "check_run_url": api,
		"_distroface": obj{"job_id": job.ID},
	}
}

func (q *request) getRun() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitActions) {
		return
	}
	run, ok := q.runByPath(repo)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.runJSON(repo, run))
}

func (q *request) listRunJobs() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitActions) {
		return
	}
	run, ok := q.runByPath(repo)
	if !ok {
		return
	}
	jobs, err := q.h.store.ListActionsJobs(q.ctx, run.ID)
	if err != nil {
		q.failErr(err)
		return
	}
	names := map[string]string{}
	rows := make([]obj, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, q.jobJSON(repo, run, j, names))
	}
	p := parsePage(q.r)
	q.paginate(p, int64(len(rows)))
	q.json(http.StatusOK, obj{"total_count": len(rows), "jobs": pageSlice(rows, p)})
}

func (q *request) artifactJSON(repo *db.GitRepository, run *db.ActionsRun, a *db.ActionsArtifact) obj {
	api := q.repoAPI(repo) + "/actions/artifacts/" + strconv.FormatInt(a.Number, 10)
	expired := a.ExpiresAt != nil && a.ExpiresAt.Before(time.Now())
	return obj{
		"id": a.Number, "node_id": "", "name": a.Name, "size_in_bytes": a.Size, "url": api, "archive_download_url": api + "/zip",
		"expired": expired, "created_at": ts(a.CreatedAt), "updated_at": ts(a.CreatedAt), "expires_at": tsPtr(a.ExpiresAt),
		"workflow_run": obj{"id": numID(run.ID), "repository_id": numID(repo.ID), "head_repository_id": numID(repo.ID), "head_branch": plumbing.ReferenceName(run.Ref).Short(), "head_sha": run.SHA},
		"_distroface":  obj{"artifact_id": a.ID, "run_id": run.ID},
	}
}

// Artifact by the number in the path, scoped to the repository
func (q *request) artifactByPath(repo *db.GitRepository) (*db.ActionsArtifact, *db.ActionsRun, bool) {
	number, ok := q.pathInt("artifact_id")
	if !ok {
		return nil, nil, false
	}
	a, err := q.h.store.GetActionsArtifactByNumber(q.ctx, repo.ID, number)
	if err != nil {
		q.failErr(err)
		return nil, nil, false
	}
	if a == nil || a.Status != actions.ArtifactConfirmed {
		q.notFound()
		return nil, nil, false
	}
	run, err := q.h.store.GetActionsRunByID(q.ctx, a.RunID)
	if err != nil {
		q.failErr(err)
		return nil, nil, false
	}
	if run == nil {
		q.notFound()
		return nil, nil, false
	}
	return a, run, true
}

func (q *request) listRunArtifacts() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitActions) || !q.actionsReady() {
		return
	}
	run, ok := q.runByPath(repo)
	if !ok {
		return
	}
	rows, err := q.h.actions.RunArtifacts(q.ctx, run)
	if err != nil {
		q.failErr(err)
		return
	}
	name := q.query("name")
	list := make([]obj, 0, len(rows))
	for _, a := range rows {
		if name != "" && a.Name != name {
			continue
		}
		list = append(list, q.artifactJSON(repo, run, a))
	}
	p := parsePage(q.r)
	q.paginate(p, int64(len(list)))
	q.json(http.StatusOK, obj{"total_count": len(list), "artifacts": pageSlice(list, p)})
}

func (q *request) listRepoArtifacts() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitActions) || !q.actionsReady() {
		return
	}
	p := parsePage(q.r)
	rows, total, err := q.h.store.ListLiveActionsArtifactsForRepo(q.ctx, repo.ID, q.query("name"), time.Now(), p.perPage, p.offset())
	if err != nil {
		q.failErr(err)
		return
	}
	runs := map[string]*db.ActionsRun{}
	list := make([]obj, 0, len(rows))
	for _, a := range rows {
		run := runs[a.RunID]
		if run == nil {
			if run, _ = q.h.store.GetActionsRunByID(q.ctx, a.RunID); run == nil {
				continue
			}
			runs[a.RunID] = run
		}
		list = append(list, q.artifactJSON(repo, run, a))
	}
	q.paginate(p, total)
	q.json(http.StatusOK, obj{"total_count": total, "artifacts": list})
}

func (q *request) getArtifact() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitActions) || !q.actionsReady() {
		return
	}
	a, run, ok := q.artifactByPath(repo)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.artifactJSON(repo, run, a))
}

// Octokit follows the redirect to the signed zip
func (q *request) downloadArtifact() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitActions) || !q.actionsReady() {
		return
	}
	a, _, ok := q.artifactByPath(repo)
	if !ok {
		return
	}
	if actions.ArtifactExpired(a) {
		q.fail(http.StatusGone, "Artifact has expired")
		return
	}
	http.Redirect(q.w, q.r, q.h.actions.ArtifactDownloadURL(q.ctx, a), http.StatusFound)
}
