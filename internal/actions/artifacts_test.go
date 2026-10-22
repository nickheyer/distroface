package actions

import (
	"testing"

	"github.com/nickheyer/distroface/internal/db"
)

// Legacy per file rows fold into one entry, current zips stand alone
func TestGroupRunArtifacts(t *testing.T) {
	rows := []*db.ActionsArtifact{
		{ID: "r1", RunID: "run", Name: "frontend-build", ArtifactID: "a1"},
		{ID: "r2", RunID: "run", Name: "frontend-build", ArtifactID: "a2"},
		{ID: "r3", RunID: "run", Name: "proto-gen", ArtifactID: "a3", FileCount: 4},
		{ID: "r4", RunID: "run", Name: "gone", ArtifactID: "missing"},
	}
	arts := map[string]*db.Artifact{
		"a1": {ID: "a1", RepoID: 7, Version: "actions-1", Path: "frontend-build/index.html", Size: 10},
		"a2": {ID: "a2", RepoID: 7, Version: "actions-1", Path: "frontend-build/app.js", Size: 5},
		"a3": {ID: "a3", RepoID: 7, Version: "actions-1", Path: "proto-gen.zip", Size: 99},
	}
	groups := groupRunArtifacts(rows, arts)
	if len(groups) != 3 {
		t.Fatalf("groups = %d", len(groups))
	}
	pkg := &db.ArtifactRepository{Namespace: "acme", Name: "app"}
	for _, g := range groups {
		g.Pkg = pkg
	}

	legacy := groups[0]
	if legacy.Row.ID != "r2" || len(legacy.Files) != 2 || legacy.Size != 15 || legacy.FileCount != 2 {
		t.Fatalf("legacy group = %+v", legacy)
	}
	if got := legacy.DownloadPath(); got != "/api/v1/artifacts/_ns/acme/app/query?actions.artifact=frontend-build&actions.run=run&format=zip&kind=workflow&num=100000" {
		t.Fatalf("legacy download = %s", got)
	}

	zipped := groups[1]
	if zipped.Size != 99 || zipped.FileCount != 4 {
		t.Fatalf("zip group = %+v", zipped)
	}
	if got := zipped.DownloadPath(); got != "/api/v1/artifacts/_ns/acme/app/actions-1/proto-gen.zip" {
		t.Fatalf("zip download = %s", got)
	}

	if gone := groups[2]; len(gone.Files) != 0 || gone.DownloadPath() != "" || gone.FileCount != 0 {
		t.Fatalf("missing artifact group = %+v", gone)
	}
}
