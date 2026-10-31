package stores_test

import (
	"context"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
)

// Server notes take the next index and a runner continues after them
func TestPutActionsJobLog(t *testing.T) {
	store := storetest.New(t)
	ctx := context.Background()
	if err := store.DB().Create(&db.GitRepository{ID: "r1", Namespace: "ns", Name: "app"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Create(&db.ActionsRun{ID: "run1", RepoID: "r1", Number: 1, WorkflowPath: "ci.yml"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Create(&db.ActionsJob{ID: "job1", RunID: "run1", Name: "build", JobKey: "build"}).Error; err != nil {
		t.Fatal(err)
	}

	first, second := []byte("##[error]no runner\n"), []byte("second\n")
	if err := store.PutActionsJobLog(ctx, "job1", first); err != nil {
		t.Fatal(err)
	}
	if err := store.PutActionsJobLog(ctx, "job1", second); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetActionsJob(ctx, "job1")
	if err != nil || job == nil {
		t.Fatalf("get job: %v", err)
	}
	if job.LogChunks != 2 || job.LogBytes != int64(len(first)+len(second)) {
		t.Fatalf("counters = %d chunks, %d bytes", job.LogChunks, job.LogBytes)
	}
	chunks, err := store.ListActionsJobLogs(ctx, "job1", 0, 0)
	if err != nil || len(chunks) != 2 || chunks[0].Index != 0 || chunks[1].Index != 1 || string(chunks[1].Data) != "second\n" {
		t.Fatalf("chunks = %+v, %v", chunks, err)
	}

	if err := store.UpdateActionsJobColumns(ctx, "job1", map[string]any{"status": "running", "runner_id": "rn1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendActionsJobLog(ctx, "job1", "rn1", 2, []byte("third\n"), 1<<20); err != nil {
		t.Fatalf("runner append after note: %v", err)
	}
	chunks, _ = store.ListActionsJobLogs(ctx, "job1", 2, 0)
	if len(chunks) != 1 || string(chunks[0].Data) != "third\n" {
		t.Fatalf("tail = %+v", chunks)
	}
}
