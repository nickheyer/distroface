package actions

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

func TestValidateArtifactName(t *testing.T) {
	for _, ok := range []string{"build", "frontend-dist", "report 1"} {
		if err := ValidateArtifactName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "a/b", "a\\b", "a:b", "a\nb", "a*b"} {
		if err := ValidateArtifactName(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestBlockFileNameRoundTrips(t *testing.T) {
	name := blockFileName("block-0001")
	if !strings.HasPrefix(name, "block-") || strings.ContainsAny(name, "/+=") {
		t.Fatalf("name = %q", name)
	}
}

// Stores a blob and answers its digest
func putBlob(t *testing.T, h *resultsHarness, content string) string {
	t.Helper()
	blobs := h.m.artifacts.Blobs()
	id, err := blobs.InitiateUpload()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.AppendChunk(id, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	digest, _, _, err := blobs.CompleteUpload(id)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestResultsAPIBlockWithoutIDAppends(t *testing.T) {
	h := newResultsHarness(t)
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "stream", "version": 4})
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, body)
	}
	upload := h.local(body["signedUploadUrl"].(string))
	for _, part := range []string{"one ", "two ", "three"} {
		req, _ := http.NewRequest(http.MethodPut, upload+"&comp=block", strings.NewReader(part))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("block without id answered %d", resp.StatusCode)
		}
	}
	status, body = h.twirp(t, token, "FinalizeArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "stream", "size": "13"})
	if status != http.StatusOK {
		t.Fatalf("finalize: %d %v", status, body)
	}
	status, body = h.twirp(t, token, "ListArtifacts", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": "caller-job"})
	arts, _ := body["artifacts"].([]any)
	if status != http.StatusOK || len(arts) != 1 {
		t.Fatalf("list: %d %v", status, body)
	}
	if a := arts[0].(map[string]any); a["workflowJobRunBackendId"] != "caller-job" || a["size"] != "13" {
		t.Fatalf("listed = %v", a)
	}
	rows, _ := h.m.RunArtifacts(context.Background(), h.run)
	got, _ := http.Get(h.local(h.m.ArtifactDownloadURL(context.Background(), rows[0])))
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(data) != "one two three" {
		t.Fatalf("download = %q", data)
	}
}

func TestResultsAPIConcurrentCreateSharesOneRow(t *testing.T) {
	h := newResultsHarness(t)
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	var wg sync.WaitGroup
	statuses := make([]int, 8)
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _ = h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "race", "version": 4})
		}(i)
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusOK {
			t.Fatalf("create %d answered %d", i, s)
		}
	}
	rows, err := h.m.store.ListActionsArtifacts(context.Background(), h.run.ID)
	if err != nil || len(rows) != 1 || rows[0].Number != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
}

func TestResultsAPIRetryRefreshesAbandonClock(t *testing.T) {
	h := newResultsHarness(t)
	ctx := context.Background()
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	if status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "retry", "version": 4}); status != http.StatusOK {
		t.Fatalf("create: %d %v", status, body)
	}
	row, _ := h.m.store.GetActionsArtifactByName(ctx, h.run.ID, "retry")
	old := time.Now().UTC().Add(-2 * pendingArtifactGrace)
	if err := h.m.store.UpdateActionsArtifactColumns(ctx, row.ID, map[string]any{"created_at": old}); err != nil {
		t.Fatal(err)
	}
	if stale, _ := h.m.store.ListStalePendingActionsArtifacts(ctx, time.Now().UTC().Add(-pendingArtifactGrace), 10); len(stale) != 1 {
		t.Fatalf("aged row not stale: %+v", stale)
	}
	if status, body := h.twirp(t, token, "CreateArtifact", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "retry", "version": 4}); status != http.StatusOK {
		t.Fatalf("retry: %d %v", status, body)
	}
	if stale, _ := h.m.store.ListStalePendingActionsArtifacts(ctx, time.Now().UTC().Add(-pendingArtifactGrace), 10); len(stale) != 0 {
		t.Fatalf("retried row still stale: %+v", stale)
	}
	again, _ := h.m.store.GetActionsArtifactByName(ctx, h.run.ID, "retry")
	if again.ID != row.ID || !again.CreatedAt.After(old) {
		t.Fatalf("row = %+v", again)
	}
}

func TestExpiredArtifactsAreGone(t *testing.T) {
	h := newResultsHarness(t)
	ctx := context.Background()
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	a := h.uploadV4(t, token, "old", []byte("stale bytes"))
	past := time.Now().UTC().Add(-time.Minute)
	if err := h.m.store.UpdateActionsArtifactColumns(ctx, a.ID, map[string]any{"expires_at": past}); err != nil {
		t.Fatal(err)
	}
	fresh := h.uploadV4(t, token, "new", []byte("fresh bytes"))

	rows, _ := h.m.RunArtifacts(ctx, h.run)
	if len(rows) != 1 || rows[0].ID != fresh.ID {
		t.Fatalf("run artifacts = %+v", rows)
	}
	status, body := h.twirp(t, token, "ListArtifacts", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID})
	if arts, _ := body["artifacts"].([]any); status != http.StatusOK || len(arts) != 1 {
		t.Fatalf("list: %d %v", status, body)
	}
	if status, body := h.twirp(t, token, "GetSignedArtifactURL", map[string]any{"workflow_run_backend_id": h.run.ID, "workflow_job_run_backend_id": h.job.ID, "name": "old"}); status != http.StatusNotFound {
		t.Fatalf("signed url of expired: %d %v", status, body)
	}
	a.ExpiresAt = &past
	gone, _ := http.Get(h.local(h.m.ArtifactDownloadURL(ctx, a)))
	gone.Body.Close()
	if gone.StatusCode != http.StatusGone {
		t.Fatalf("signed download of expired answered %d", gone.StatusCode)
	}
	live, _, _ := h.m.store.ListLiveActionsArtifactsForRepo(ctx, h.repo.ID, "", time.Now(), 10, 0)
	if len(live) != 1 || live[0].ID != fresh.ID {
		t.Fatalf("live repo artifacts = %+v", live)
	}

	rec := httptest.NewRecorder()
	h.m.ServeRepoArtifact(rec, httptest.NewRequest(http.MethodGet, "/", nil), h.repo, a.ID)
	if rec.Code != http.StatusGone {
		t.Fatalf("repo route for expired answered %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.m.ServeRepoArtifact(rec, httptest.NewRequest(http.MethodGet, "/", nil), h.repo, fresh.ID)
	if rec.Code != http.StatusOK || rec.Body.String() != "fresh bytes" {
		t.Fatalf("repo route answered %d %q", rec.Code, rec.Body.String())
	}
	other := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "other", DefaultBranch: "main"}
	rec = httptest.NewRecorder()
	h.m.ServeRepoArtifact(rec, httptest.NewRequest(http.MethodGet, "/", nil), other, fresh.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign repo answered %d", rec.Code)
	}
	if u := h.m.ArtifactPageURL(ctx, h.repo, fresh); !strings.HasSuffix(u, "/acme/app/-/actions/artifacts/"+fresh.ID+"/new.zip") {
		t.Fatalf("page url = %q", u)
	}

	h.m.expireArtifacts(ctx)
	if left, _ := h.m.store.ListActionsArtifacts(ctx, h.run.ID); len(left) != 1 || left[0].ID != fresh.ID {
		t.Fatalf("sweep left %+v", left)
	}
	if _, _, err := h.m.artifacts.Blobs().OpenBlob(a.Digest); err == nil {
		t.Fatal("expired blob survived the sweep")
	}
}

func TestDeleteRepoArtifactsReclaimsStorage(t *testing.T) {
	h := newResultsHarness(t)
	ctx := context.Background()
	token, _ := h.m.runtimeToken(h.job.ID, h.run.ID, 10)
	zipped := h.uploadV4(t, token, "dist", []byte("artifact bytes"))
	pending := &db.ActionsArtifact{RunID: h.run.ID, JobID: h.job.ID, Name: "half", Status: ArtifactPending}
	if err := h.m.store.CreateActionsArtifact(ctx, pending); err != nil {
		t.Fatal(err)
	}
	blockDir := h.m.artifactBlockDir(pending.ID)
	if err := os.MkdirAll(blockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logDigest := putBlob(t, h, "job log")
	if err := h.m.store.UpdateActionsJobColumns(ctx, h.job.ID, map[string]any{"log_digest": logDigest, "log_chunks": 1}); err != nil {
		t.Fatal(err)
	}
	cacheDigest := putBlob(t, h, "cache archive")
	cache := &db.ActionsCache{RepoID: h.repo.ID, Key: "npm-abc", Version: "v1", Digest: cacheDigest, Size: 13, State: stores.ActionsCacheComplete, LastUsedAt: time.Now().UTC()}
	if err := h.m.store.CreateActionsCache(ctx, cache); err != nil {
		t.Fatal(err)
	}
	shared := putBlob(t, h, "shared by a package")
	sharedRow := &db.ActionsArtifact{RunID: h.run.ID, Name: "shared", Digest: shared, Size: 19, Status: ArtifactConfirmed}
	if err := h.m.store.CreateActionsArtifact(ctx, sharedRow); err != nil {
		t.Fatal(err)
	}
	otherRepo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "other", DefaultBranch: "main"}
	if err := h.m.store.DB().Create(otherRepo).Error; err != nil {
		t.Fatal(err)
	}
	other := &db.ActionsCache{RepoID: otherRepo.ID, Key: "other", Version: "v1", Digest: shared, Size: 19, State: stores.ActionsCacheComplete, LastUsedAt: time.Now().UTC()}
	if err := h.m.store.CreateActionsCache(ctx, other); err != nil {
		t.Fatal(err)
	}

	if err := h.m.DeleteRepoArtifacts(ctx, h.repo); err != nil {
		t.Fatal(err)
	}
	if rows, _ := h.m.store.ListActionsArtifacts(ctx, h.run.ID); len(rows) != 0 {
		t.Fatalf("artifact rows survived: %+v", rows)
	}
	if _, err := os.Stat(blockDir); !os.IsNotExist(err) {
		t.Fatal("pending block dir survived")
	}
	for name, digest := range map[string]string{"artifact": zipped.Digest, "log": logDigest, "cache": cacheDigest} {
		if _, _, err := h.m.artifacts.Blobs().OpenBlob(digest); err == nil {
			t.Fatalf("%s blob survived", name)
		}
	}
	if _, _, err := h.m.artifacts.Blobs().OpenBlob(shared); err != nil {
		t.Fatal("blob another repository's cache still references was deleted")
	}
	if caches, _ := h.m.store.ListActionsCachesForRepo(ctx, h.repo.ID); len(caches) != 0 {
		t.Fatalf("caches survived: %+v", caches)
	}
	job, _ := h.m.store.GetActionsJob(ctx, h.job.ID)
	if job.LogDigest != "" {
		t.Fatalf("job still names its log blob %q", job.LogDigest)
	}
}

func TestStreamArtifactNamesLegacyFiles(t *testing.T) {
	h := newResultsHarness(t)
	digest := putBlob(t, h, "plain text")
	rec := httptest.NewRecorder()
	h.m.streamArtifact(rec, httptest.NewRequest(http.MethodGet, "/", nil), &db.ActionsArtifact{Name: "report", Digest: digest, ContentType: "text/plain"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), `"report"`) || !bytes.Equal(rec.Body.Bytes(), []byte("plain text")) {
		t.Fatalf("legacy stream: %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	rec = httptest.NewRecorder()
	h.m.streamArtifact(rec, httptest.NewRequest(http.MethodGet, "/", nil), &db.ActionsArtifact{Name: "dist", Digest: digest})
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `"dist.zip"`) {
		t.Fatalf("zip stream disposition = %q", cd)
	}
	if u := h.m.ArtifactDownloadURL(context.Background(), &db.ActionsArtifact{ID: "x", Name: "a b", ContentType: "application/zip"}); !strings.Contains(u, "/x/a%20b.zip?") {
		t.Fatalf("download url = %s", fmt.Sprint(u))
	}
}
