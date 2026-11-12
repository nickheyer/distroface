package stores_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
)

func TestBlobReferencedCoversEveryTable(t *testing.T) {
	store, repo, run := artifactStore(t)
	ctx := context.Background()
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }

	if ok, err := store.BlobReferenced(ctx, digest("0")); err != nil || ok {
		t.Fatalf("unknown digest: referenced %v err %v", ok, err)
	}

	job := &db.ActionsJob{ID: uuid.New().String(), RunID: run.ID, Name: "build", Status: "success", LogDigest: digest("a")}
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: "v1", AuthorID: "u"}
	rows := []any{
		job,
		&db.ActionsArtifact{ID: uuid.New().String(), RunID: run.ID, Name: "dist", Digest: digest("b"), Status: "confirmed"},
		&db.ActionsCache{RepoID: repo.ID, Key: "k", Digest: digest("c"), State: "complete"},
		rel,
		&db.GitReleaseAsset{ID: uuid.New().String(), ReleaseID: rel.ID, Name: "a.bin", Digest: digest("d")},
	}
	for _, row := range rows {
		if err := store.DB().Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}
	if err := store.UpsertLFSObject(ctx, repo.ID, strings.Repeat("e", 64), 7); err != nil {
		t.Fatalf("UpsertLFSObject: %v", err)
	}
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		if ok, err := store.BlobReferenced(ctx, digest(c)); err != nil || !ok {
			t.Errorf("digest %s: referenced %v err %v", c, ok, err)
		}
	}
}

func TestUpsertLFSObjectTracksRepoSize(t *testing.T) {
	store := storetest.New(t)
	ctx := context.Background()
	repos := make([]*db.GitRepository, 2)
	for i, name := range []string{"one", "two"} {
		repos[i] = &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: name, DefaultBranch: "main"}
		if err := store.DB().Create(repos[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	oidA, oidB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	steps := []struct {
		repo int
		oid  string
		size int64
	}{
		{0, oidA, 100},
		{0, oidA, 100},
		{0, oidB, 30},
		// Existing bytes count at their stored size
		{1, oidA, 999},
	}
	for _, s := range steps {
		if err := store.UpsertLFSObject(ctx, repos[s.repo].ID, s.oid, s.size); err != nil {
			t.Fatalf("UpsertLFSObject: %v", err)
		}
	}
	for i, want := range []int64{130, 100} {
		var got db.GitRepository
		if err := store.DB().First(&got, "id = ?", repos[i].ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.LFSSizeBytes != want {
			t.Errorf("repo %d lfs bytes %d, want %d", i, got.LFSSizeBytes, want)
		}
	}
}
