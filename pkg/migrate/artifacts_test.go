package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Seeds a minimal v1 db with property variant duplicates
func newV1Fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v1.db")
	fixture, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer fixture.Close()

	for _, stmt := range []string{
		`CREATE TABLE artifact_repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT,
			owner TEXT NOT NULL,
			private BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE artifacts (
			id TEXT PRIMARY KEY,
			repo_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			upload_id TEXT NOT NULL,
			version TEXT NOT NULL,
			size INTEGER NOT NULL,
			mime_type TEXT,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE artifact_properties (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			artifact_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL
		)`,
		`INSERT INTO artifact_repositories (id, name, owner) VALUES (1, 'ci-cache', 'jenkins')`,
	} {
		if _, err := fixture.Exec(stmt); err != nil {
			t.Fatalf("fixture exec: %v", err)
		}
	}

	art := func(id, version, artPath, created string) {
		t.Helper()
		_, err := fixture.Exec(
			`INSERT INTO artifacts (id, repo_id, name, path, upload_id, version, size, created_at, updated_at)
			 VALUES (?, 1, 'build', ?, 'up-'||?, ?, 100, ?, ?)`,
			id, artPath, id, version, created, created)
		if err != nil {
			t.Fatalf("insert artifact %s: %v", id, err)
		}
	}
	prop := func(artifactID, key, value string) {
		t.Helper()
		if _, err := fixture.Exec(
			`INSERT INTO artifact_properties (artifact_id, key, value) VALUES (?, ?, ?)`,
			artifactID, key, value); err != nil {
			t.Fatalf("insert property for %s: %v", artifactID, err)
		}
	}

	// Two property variants share version and path
	art("a-x86", "1.0", "out/build.tgz", "2024-01-01 10:00:00")
	prop("a-x86", "arch", "x86")
	art("a-arm", "1.0", "out/build.tgz", "2024-01-01 10:05:00")
	prop("a-arm", "arch", "arm")
	// True re-upload of the arm variant
	art("a-arm2", "1.0", "out/build.tgz", "2024-01-01 10:10:00")
	prop("a-arm2", "arch", "arm")
	// Propertyless re-upload pair, older one drops
	art("b-old", "2.0", "plain.txt", "2024-01-01 10:00:00")
	art("b-new", "2.0", "plain.txt", "2024-01-01 10:15:00")
	return path
}

func TestPlanArtifactsPropertyIdentity(t *testing.T) {
	v1db, err := OpenV1DB(newV1Fixture(t))
	if err != nil {
		t.Fatalf("OpenV1DB: %v", err)
	}
	defer v1db.Close()

	plan, err := PlanArtifacts(v1db)
	if err != nil {
		t.Fatalf("PlanArtifacts: %v", err)
	}

	kept := map[string]bool{}
	for _, a := range plan.Artifacts {
		kept[a.ID] = true
	}
	for _, id := range []string{"a-x86", "a-arm2", "b-new"} {
		if !kept[id] {
			t.Fatalf("plan dropped %s, kept %v", id, kept)
		}
	}
	if len(plan.Artifacts) != 3 {
		t.Fatalf("plan kept %d artifacts, want 3: %v", len(plan.Artifacts), kept)
	}
	if got := plan.TotalDupSkipped(); got != 2 {
		t.Fatalf("dup skipped %d, want 2", got)
	}
	if len(plan.Invalid) != 0 {
		t.Fatalf("unexpected invalid rows: %v", plan.Invalid)
	}
}
