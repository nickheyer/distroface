package stores_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/pkg/pages"
)

func TestArtifactLabelAndDownloadCount(t *testing.T) {
	store := storetest.New(t)
	ctx := context.Background()
	repo := &db.ArtifactRepository{Namespace: "acme", Name: "tools"}
	if err := store.CreateArtifactRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	first := &db.Artifact{RepoID: repo.ID, Name: "cli", Path: "cli", Version: "1.0", Digest: digest("a"), Label: "Command line"}
	if _, err := store.CreateArtifact(ctx, first, nil); err != nil {
		t.Fatal(err)
	}
	other := &db.Artifact{RepoID: repo.ID, Name: "lib", Path: "lib", Version: "1.0", Digest: digest("b")}
	if _, err := store.CreateArtifact(ctx, other, nil); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := store.IncrementArtifactDownloads(ctx, first.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Saving a stale copy must not rewind the counter
	first.Label = "CLI"
	if err := store.UpdateArtifact(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetArtifact(ctx, first.ID)
	if err != nil || got.DownloadCount != 3 || got.Label != "CLI" {
		t.Fatalf("after update = %+v err %v", got, err)
	}

	replaced := &db.Artifact{RepoID: repo.ID, Name: "cli", Path: "cli", Version: "1.0", Digest: digest("c")}
	old, err := store.CreateArtifact(ctx, replaced, nil)
	if err != nil || old != digest("a") {
		t.Fatalf("replace returned %q err %v", old, err)
	}
	if replaced.ID != first.ID || replaced.DownloadCount != 3 || replaced.Label != "CLI" {
		t.Fatalf("a replaced file keeps id, downloads and label: %+v", replaced)
	}

	byLabel, total, err := store.SearchArtifacts(ctx, stores.ArtifactSearchCriteria{
		RepoID: &repo.ID, Query: pages.Query{Filters: []pages.Filter{{Field: "label", Value: "CLI"}}},
	})
	if err != nil || total != 1 || byLabel[0].ID != first.ID {
		t.Fatalf("label filter: %d %v err %v", total, byLabel, err)
	}
	if !stores.ArtifactSortColumns["download_count"] {
		t.Fatal("download_count must be sortable")
	}
	ranked, _, err := store.SearchArtifacts(ctx, stores.ArtifactSearchCriteria{RepoID: &repo.ID, OrderBy: "download_count DESC"})
	if err != nil || len(ranked) != 2 || ranked[0].ID != first.ID {
		t.Fatalf("download order: %v err %v", ranked, err)
	}
}
