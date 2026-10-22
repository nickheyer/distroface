package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1proto "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func (e *testEnv) workflowUpload(repo *storage.ArtifactRepository, version, path, content string, props map[string]string) *storage.Artifact {
	e.t.Helper()
	id, err := e.blobs.InitiateUpload()
	if err != nil {
		e.t.Fatalf("InitiateUpload: %v", err)
	}
	if _, err := e.blobs.AppendChunk(id, strings.NewReader(content)); err != nil {
		e.t.Fatalf("AppendChunk: %v", err)
	}
	art, err := e.manager.CompleteWorkflowUpload(context.Background(), repo, id, version, path, props)
	if err != nil {
		e.t.Fatalf("CompleteWorkflowUpload: %v", err)
	}
	return art
}

func (e *testEnv) versionKeys(target, token string) []string {
	e.t.Helper()
	rec := e.do(http.MethodGet, target, token, nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET %s: got %d body %q", target, rec.Code, rec.Body.String())
	}
	var grouped map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &grouped); err != nil {
		e.t.Fatalf("decode %s: %v", target, err)
	}
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e *testEnv) searchTotal(target, token string) int {
	e.t.Helper()
	rec := e.do(http.MethodGet, target, token, nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET %s: got %d body %q", target, rec.Code, rec.Body.String())
	}
	var resp struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		e.t.Fatalf("decode %s: %v", target, err)
	}
	return resp.Total
}

// Workflow uploads stay out of package listings, retention, and counts
func TestWorkflowUploadsKeptApart(t *testing.T) {
	e := newTestEnv(t, &v1proto.ArtifactRetentionSettings{
		Enabled:           proto.Bool(true),
		MaxTotalSizeBytes: proto.Int64(10),
		ExcludeLatest:     proto.Bool(true),
	})
	ctx := context.Background()
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "app"})
	e.uploadArtifact(token, "app", "1.0", "app.bin", "dat1", nil)
	repo := e.repoByName("app")

	// Sixty workflow bytes would blow the ten byte cap if retention counted them
	for i := 1; i <= 3; i++ {
		art := e.workflowUpload(repo, fmt.Sprintf("actions-%d", i), "build.zip", strings.Repeat("z", 20), map[string]string{"actions.run": fmt.Sprint(i)})
		if art.Kind != storage.ArtifactKindWorkflow {
			t.Fatalf("kind = %q", art.Kind)
		}
	}
	e.uploadArtifact(token, "app", "1.1", "app.bin", "dat2", nil)

	regular, _, err := e.store.ListArtifacts(ctx, repo.ID, "", stores.KindRegular, 0, 0)
	if err != nil || len(regular) != 2 {
		t.Fatalf("regular rows = %d (%v), retention must ignore workflow uploads", len(regular), err)
	}
	workflow, _, err := e.store.ListArtifacts(ctx, repo.ID, "", stores.KindWorkflow, 0, 0)
	if err != nil || len(workflow) != 3 {
		t.Fatalf("workflow rows = %d (%v)", len(workflow), err)
	}
	all, _, _ := e.store.ListArtifacts(ctx, repo.ID, "", stores.KindAny, 0, 0)
	if len(all) != 5 {
		t.Fatalf("all rows = %d", len(all))
	}

	stats, err := e.store.GetArtifactRepoStats(ctx, []int64{repo.ID})
	if err != nil {
		t.Fatal(err)
	}
	if st := stats[repo.ID]; st.Count != 2 || st.Size != 8 || st.WorkflowCount != 3 || st.WorkflowSize != 60 {
		t.Fatalf("stats = %+v", st)
	}
	latest, _ := e.store.LatestArtifactVersions(ctx, []int64{repo.ID})
	if latest[repo.ID] != "1.1" {
		t.Fatalf("latest = %q, workflow versions must not win", latest[repo.ID])
	}

	if got := e.versionKeys("/api/v1/artifacts/app/versions", token); strings.Join(got, ",") != "1.0,1.1" {
		t.Fatalf("versions = %v", got)
	}
	if got := e.versionKeys("/api/v1/artifacts/app/versions?kind=workflow", token); strings.Join(got, ",") != "actions-1,actions-2,actions-3" {
		t.Fatalf("workflow versions = %v", got)
	}
	if n := e.searchTotal("/api/v1/artifacts/search?repo=app", token); n != 2 {
		t.Fatalf("default search = %d", n)
	}
	if n := e.searchTotal("/api/v1/artifacts/search?repo=app&kind=workflow", token); n != 3 {
		t.Fatalf("workflow search = %d", n)
	}
	if n := e.searchTotal("/api/v1/artifacts/search?repo=app&kind=all", token); n != 5 {
		t.Fatalf("kind=all search = %d", n)
	}

	// Query downloads need the kind too, the runner path asks for it with properties
	rec := e.do(http.MethodGet, "/api/v1/artifacts/app/query?actions.run=2", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("default query must not see workflow files: %d", rec.Code)
	}
	rec = e.do(http.MethodGet, "/api/v1/artifacts/app/query?kind=workflow&actions.run=2", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("workflow query: %d %q", rec.Code, rec.Body.String())
	}
}
