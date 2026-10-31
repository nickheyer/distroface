package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Manager over a scratch database with one repository and one running job
type resultsHarness struct {
	m    *Manager
	repo *db.GitRepository
	run  *db.ActionsRun
	job  *db.ActionsJob
	srv  *httptest.Server
}

func newResultsHarness(t *testing.T) *resultsHarness {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	log := logger.New()
	store := storetest.New(t)
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := enforcer.SeedDefaultPolicies(); err != nil {
		t.Fatal(err)
	}
	res := settings.NewResolver(store, nil)
	authMgr, err := auth.NewManager(store, enforcer, "test-secret-test-secret-test-secret", res)
	if err != nil {
		t.Fatal(err)
	}
	if err := authMgr.LoadSealingKey("", root); err != nil {
		t.Fatal(err)
	}
	blobs, err := artifacts.NewBlobStore(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	artifactMgr := artifacts.NewManager(store, blobs, res, log)
	engine, err := gitrepo.NewEngine(filepath.Join(root, "git"), root)
	if err != nil {
		t.Fatal(err)
	}
	gitMgr := gitrepo.NewManager(store, engine, blobs, res, gitrepo.NewAccess(store, enforcer), webhook.NewDispatcher(store, log, res), audit.NewRecorder(store, res, log), log)
	forgeMgr := forge.NewManager(store, gitMgr, mailer.NewService(store, res, log), res, log)
	t.Cleanup(func() { forgeMgr.Wait(context.Background()) })
	m := NewManager(store, gitMgr, forgeMgr, artifactMgr, authMgr, res, log)

	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "app", DefaultBranch: "main"}
	if err := store.DB().Create(repo).Error; err != nil {
		t.Fatal(err)
	}
	run := &db.ActionsRun{ID: uuid.New().String(), RepoID: repo.ID, Number: 1, WorkflowPath: "ci.yml", WorkflowName: "ci", Status: StatusRunning}
	job := &db.ActionsJob{ID: uuid.New().String(), Name: "build", JobKey: "build", WorkflowJobID: "build", RunsOn: "[]", Needs: "[]", Matrix: "{}",
		Steps: "[]", Outputs: "{}", Annotations: "[]", CallInputs: "{}", Status: StatusRunning, RunnerID: "rn", Attempt: 1}
	if err := store.CreateActionsRun(ctx, run, []*db.ActionsJob{job}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(ResultsAPIPrefix+"/", m.ResultsHandler())
	mux.Handle(ArtifactDownloadPrefix+"/", m.ArtifactDownloadHandler())
	mux.Handle(PipelineAPIPrefix+"/", http.StripPrefix(PipelineAPIPrefix, m.PipelineHandler()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &resultsHarness{m: m, repo: repo, run: run, job: job, srv: srv}
}

// Signed links name the instance base url, the test server answers instead
func (h *resultsHarness) local(u string) string {
	for _, prefix := range []string{"/twirp/", ArtifactDownloadPrefix, PipelineAPIPrefix} {
		if i := strings.Index(u, prefix); i >= 0 {
			return h.srv.URL + u[i:]
		}
	}
	return u
}

// Runs the v4 upload flow end to end and answers the finalized row
func (h *resultsHarness) uploadV4(t *testing.T, token, name string, content []byte) *db.ActionsArtifact {
	t.Helper()
	status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": name, "version": 4})
	if status != http.StatusOK {
		t.Fatalf("create %s: %d %v", name, status, body)
	}
	req, _ := http.NewRequest(http.MethodPut, h.local(body["signedUploadUrl"].(string))+"&comp=block&blockid=only", bytes.NewReader(content))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("block of %s: %d", name, resp.StatusCode)
	}
	status, body = h.twirp(t, token, "FinalizeArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": name, "size": fmt.Sprint(len(content))})
	if status != http.StatusOK {
		t.Fatalf("finalize %s: %d %v", name, status, body)
	}
	a, err := h.m.store.GetActionsArtifactByName(context.Background(), h.run.ID, name)
	if err != nil || a == nil {
		t.Fatalf("row of %s: %v %v", name, a, err)
	}
	return a
}

func (h *resultsHarness) twirp(t *testing.T, token, method string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+ResultsAPIPrefix+"/"+method, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestResultsAPIUploadListDownloadDelete(t *testing.T) {
	h := newResultsHarness(t)
	token, err := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := h.twirp(t, "", "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist", "version": 4}); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d %v", status, body)
	}
	status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist", "version": 4})
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("create: %d %v", status, body)
	}
	upload := h.local(body["signedUploadUrl"].(string))

	first, second := []byte("hello "), []byte("artifact")
	for i, part := range [][]byte{first, second} {
		req, _ := http.NewRequest(http.MethodPut, upload+"&comp=block&blockid=blk"+fmt.Sprint(i), bytes.NewReader(part))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("block %d: %d", i, resp.StatusCode)
		}
	}
	list := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><BlockList><Latest>blk0</Latest><Latest>blk1</Latest></BlockList>`
	req, _ := http.NewRequest(http.MethodPut, upload+"&comp=blocklist", strings.NewReader(list))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("blocklist: %d", resp.StatusCode)
	}

	content := append(append([]byte{}, first...), second...)
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if status, body := h.twirp(t, token, "FinalizeArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist", "size": "3", "hash": hash}); status == http.StatusOK {
		t.Fatalf("size mismatch must fail: %v", body)
	}
	status, body = h.twirp(t, token, "FinalizeArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist", "size": fmt.Sprint(len(content)), "hash": hash})
	if status != http.StatusOK || body["ok"] != true || body["artifactId"] != "1" {
		t.Fatalf("finalize: %d %v", status, body)
	}
	if status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist", "version": 4}); status != http.StatusConflict {
		t.Fatalf("duplicate name must conflict: %d %v", status, body)
	}

	status, body = h.twirp(t, token, "ListArtifacts", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name_filter": "dist"})
	arts, _ := body["artifacts"].([]any)
	if status != http.StatusOK || len(arts) != 1 {
		t.Fatalf("list: %d %v", status, body)
	}
	if a := arts[0].(map[string]any); a["name"] != "dist" || a["size"] != fmt.Sprint(len(content)) || a["databaseId"] != "1" {
		t.Fatalf("listed = %v", a)
	}

	status, body = h.twirp(t, token, "GetSignedArtifactURL", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist"})
	if status != http.StatusOK {
		t.Fatalf("signed url: %d %v", status, body)
	}
	got, err := http.Get(h.local(body["signedUrl"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if got.StatusCode != http.StatusOK || !bytes.Equal(data, content) || got.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %q %s", got.StatusCode, data, got.Header.Get("Content-Type"))
	}

	rows, err := h.m.RunArtifacts(context.Background(), h.run)
	if err != nil || len(rows) != 1 || rows[0].Number != 1 || rows[0].Size != int64(len(content)) {
		t.Fatalf("run artifacts = %+v, %v", rows, err)
	}
	ui, err := http.Get(h.local(h.m.ArtifactDownloadURL(context.Background(), rows[0])))
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(ui.Body)
	ui.Body.Close()
	if ui.StatusCode != http.StatusOK || !bytes.Equal(data, content) {
		t.Fatalf("ui download: %d %q", ui.StatusCode, data)
	}
	if bad, _ := http.Get(h.local(h.m.ArtifactDownloadURL(context.Background(), rows[0])) + "x"); bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered signature answered %d", bad.StatusCode)
	}

	status, body = h.twirp(t, token, "DeleteArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "dist"})
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("delete: %d %v", status, body)
	}
	if rows, _ := h.m.RunArtifacts(context.Background(), h.run); len(rows) != 0 {
		t.Fatalf("artifact survived delete: %+v", rows)
	}
	if _, _, err := h.m.artifacts.Blobs().OpenBlob(hash); err == nil {
		t.Fatal("blob survived delete")
	}
}

func TestResultsAPIRefusesOtherRuns(t *testing.T) {
	h := newResultsHarness(t)
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	if status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": "other", "workflow_job_run_backend_id": h.job.ID, "name": "dist", "version": 4}); status != http.StatusForbidden {
		t.Fatalf("foreign run: %d %v", status, body)
	}
	if status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "a/b", "version": 4}); status != http.StatusBadRequest {
		t.Fatalf("bad name: %d %v", status, body)
	}
}
