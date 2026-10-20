package stores_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
)

func artifactStore(t *testing.T) (*stores.Store, *db.GitRepository, *db.ActionsRun) {
	t.Helper()
	store := storetest.New(t)
	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "app", DefaultBranch: "main"}
	if err := store.DB().Create(repo).Error; err != nil {
		t.Fatal(err)
	}
	run := &db.ActionsRun{ID: uuid.New().String(), RepoID: repo.ID, Number: 1, WorkflowPath: "ci.yml", Status: "running"}
	if err := store.CreateActionsRun(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	return store, repo, run
}

func TestCreateActionsArtifactOrReuse(t *testing.T) {
	store, repo, run := artifactStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make([]string, 8)
	created := make([]bool, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			row, fresh, err := store.CreateActionsArtifactOrReuse(ctx, repo.ID, &db.ActionsArtifact{RunID: run.ID, Name: "dist", Status: "pending"})
			if err != nil {
				t.Errorf("create %d: %v", i, err)
				return
			}
			ids[i], created[i] = row.ID, fresh
		}(i)
	}
	wg.Wait()
	fresh := 0
	for i := range ids {
		if created[i] {
			fresh++
		}
		if ids[i] != ids[0] {
			t.Fatalf("rows differ: %v", ids)
		}
	}
	if fresh != 1 {
		t.Fatalf("%d creators won", fresh)
	}
	rows, err := store.ListActionsArtifacts(ctx, run.ID)
	if err != nil || len(rows) != 1 || rows[0].Number != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	other, fresh2, err := store.CreateActionsArtifactOrReuse(ctx, repo.ID, &db.ActionsArtifact{RunID: run.ID, Name: "other", Status: "pending"})
	if err != nil || !fresh2 || other.Number != 2 {
		t.Fatalf("second name = %+v, %v, %v", other, fresh2, err)
	}
	var counter int64
	store.DB().Model(&db.GitRepository{}).Where("id = ?", repo.ID).Pluck("artifact_counter", &counter)
	if counter != 2 {
		t.Fatalf("artifact_counter = %d", counter)
	}
	if err := store.DB().Create(&db.ActionsArtifact{ID: uuid.New().String(), RunID: run.ID, Name: "dist"}).Error; err == nil {
		t.Fatal("duplicate name in a run was accepted")
	}
}

func TestListLiveActionsArtifactsForRepo(t *testing.T) {
	store, repo, run := artifactStore(t)
	ctx := context.Background()
	past, future := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	for _, a := range []*db.ActionsArtifact{
		{RunID: run.ID, Name: "expired", Status: "confirmed", ExpiresAt: &past},
		{RunID: run.ID, Name: "live", Status: "confirmed", ExpiresAt: &future},
		{RunID: run.ID, Name: "forever", Status: "confirmed"},
		{RunID: run.ID, Name: "pending", Status: "pending"},
	} {
		if err := store.CreateActionsArtifact(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := store.ListLiveActionsArtifactsForRepo(ctx, repo.ID, "", time.Now(), 10, 0)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("live = %+v, %d, %v", rows, total, err)
	}
	for _, r := range rows {
		if r.Name == "expired" || r.Name == "pending" {
			t.Fatalf("%s listed as live", r.Name)
		}
	}
	rows, total, err = store.ListLiveActionsArtifactsForRepo(ctx, repo.ID, "expired", time.Now(), 10, 0)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("expired by name = %+v, %d, %v", rows, total, err)
	}
}

func TestListActionsCachesForRepo(t *testing.T) {
	store, repo, _ := artifactStore(t)
	ctx := context.Background()
	elsewhere := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "elsewhere", DefaultBranch: "main"}
	if err := store.DB().Create(elsewhere).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, c := range []*db.ActionsCache{
		{RepoID: repo.ID, Key: "a", Version: "1", State: stores.ActionsCacheReserved, LastUsedAt: now},
		{RepoID: repo.ID, Key: "b", Version: "1", State: stores.ActionsCacheComplete, Digest: "sha256:b", LastUsedAt: now},
		{RepoID: elsewhere.ID, Key: "c", Version: "1", State: stores.ActionsCacheComplete, Digest: "sha256:c", LastUsedAt: now},
	} {
		if err := store.CreateActionsCache(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListActionsCachesForRepo(ctx, repo.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("caches = %+v, %v", rows, err)
	}
}
