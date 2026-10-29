package migrations

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func openMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "m.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return gdb
}

func execAll(t *testing.T, gdb *gorm.DB, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		if err := gdb.Exec(s).Error; err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func artifactIndexes(t *testing.T, gdb *gorm.DB) map[string]string {
	t.Helper()
	var rows []struct {
		Name string
		SQL  string
	}
	if err := gdb.Raw("SELECT name, sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'actions_artifacts'").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Name] = r.SQL
	}
	return out
}

func TestMoveActionsArtifactsToRuns(t *testing.T) {
	gdb := openMigrationDB(t)
	// The table as auto migrate leaves it, old package columns beside the new blob columns
	execAll(t, gdb, []string{
		`CREATE TABLE git_repositories (id TEXT PRIMARY KEY, artifact_counter INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO git_repositories (id, artifact_counter) VALUES ('r1', 0)`,
		`CREATE TABLE actions_runs (id TEXT PRIMARY KEY, repo_id TEXT NOT NULL)`,
		`INSERT INTO actions_runs (id, repo_id) VALUES ('run1', 'r1'), ('run2', 'r1')`,
		`CREATE TABLE artifacts (id TEXT PRIMARY KEY, repo_id INTEGER NOT NULL DEFAULT 1, name TEXT NOT NULL, path TEXT NOT NULL, upload_id TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', props_hash TEXT NOT NULL DEFAULT '', digest TEXT NOT NULL, size INTEGER NOT NULL, mime_type TEXT, metadata TEXT NOT NULL DEFAULT '{}', created_at DATETIME, updated_at DATETIME)`,
		`INSERT INTO artifacts (id, name, path, digest, size, mime_type) VALUES
			('p1', 'dist', 'dist.zip', 'sha256:aaa', 10, 'application/zip'),
			('p2', 'logs', 'logs/out.txt', 'sha256:bbb', 5, 'text/plain'),
			('p3', 'logs', 'logs/err.txt', 'sha256:ccc', 7, 'text/plain'),
			('p4', 'orphan', 'orphan.zip', 'sha256:ddd', 3, 'application/zip')`,
		`CREATE TABLE artifact_properties (id INTEGER PRIMARY KEY AUTOINCREMENT, artifact_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, created_at DATETIME)`,
		`INSERT INTO artifact_properties (artifact_id, key, value) VALUES ('p1', 'kind', 'workflow'), ('p2', 'kind', 'workflow'), ('p3', 'kind', 'workflow'), ('p4', 'kind', 'workflow')`,
		"CREATE TABLE `actions_artifacts` (`id` text,`run_id` text NOT NULL,`job_id` text NOT NULL DEFAULT '',`artifact_id` text NOT NULL,`name` text NOT NULL,`file_count` integer NOT NULL DEFAULT 0,`expires_at` datetime,`created_at` datetime, `number` integer NOT NULL DEFAULT 0, `digest` text NOT NULL DEFAULT '', `size` integer NOT NULL DEFAULT 0, `content_type` text NOT NULL DEFAULT 'application/zip', `status` text NOT NULL DEFAULT 'pending',PRIMARY KEY (`id`))",
		`CREATE INDEX idx_actions_artifacts_run_id ON actions_artifacts (run_id)`,
		`CREATE INDEX idx_actions_artifacts_artifact_id ON actions_artifacts (artifact_id)`,
		`INSERT INTO actions_artifacts (id, run_id, artifact_id, name, file_count, number, digest, size, status, created_at) VALUES
			('a0', 'run1', '', 'already', 1, 9, 'sha256:eee', 4, 'confirmed', '2026-01-01 00:00:00'),
			('a1', 'run1', 'p1', 'dist', 1, 0, '', 0, 'pending', '2026-01-01 00:00:01'),
			('a2', 'run1', 'p2', 'logs', 0, 0, '', 0, 'pending', '2026-01-01 00:00:02'),
			('a3', 'run1', 'p3', 'logs', 0, 0, '', 0, 'pending', '2026-01-01 00:00:03'),
			('a4', 'gone', 'p4', 'orphan', 1, 0, '', 0, 'pending', '2026-01-01 00:00:04'),
			('a5', 'run1', 'px', 'lost', 1, 0, '', 0, 'pending', '2026-01-01 00:00:05'),
			('a6', 'run2', 'p1', 'dist', 1, 0, '', 0, 'pending', '2026-01-01 00:00:06')`,
	})
	if err := moveActionsArtifactsToRuns(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}

	var rows []db.ActionsArtifact
	if err := gdb.Order("created_at ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got := map[string]db.ActionsArtifact{}
	for _, r := range rows {
		got[r.ID] = r
	}
	if len(got) != 5 || got["a4"].ID != "" || got["a5"].ID != "" {
		t.Fatalf("rows = %+v", rows)
	}
	if a := got["a0"]; a.Number != 9 || a.Digest != "sha256:eee" {
		t.Fatalf("already moved row changed: %+v", a)
	}
	if a := got["a1"]; a.Digest != "sha256:aaa" || a.Size != 10 || a.Number != 10 || a.Status != "confirmed" || a.ContentType != "application/zip" || a.Name != "dist" {
		t.Fatalf("a1 = %+v", a)
	}
	if a := got["a2"]; a.Digest != "sha256:bbb" || a.Number != 11 || a.ContentType != "text/plain" || a.Name != "logs" {
		t.Fatalf("a2 = %+v", a)
	}
	if a := got["a3"]; a.Digest != "sha256:ccc" || a.Number != 12 || a.ContentType != "text/plain" || a.Name != "logs logs-err.txt" {
		t.Fatalf("a3 = %+v", a)
	}
	if a := got["a6"]; a.Digest != "sha256:aaa" || a.Number != 13 || a.RunID != "run2" || a.Name != "dist" {
		t.Fatalf("a6 = %+v", a)
	}

	var packages, props, counter int64
	gdb.Raw("SELECT COUNT(*) FROM artifacts").Scan(&packages)
	gdb.Raw("SELECT COUNT(*) FROM artifact_properties").Scan(&props)
	gdb.Raw("SELECT artifact_counter FROM git_repositories WHERE id = 'r1'").Scan(&counter)
	if packages != 0 || props != 0 {
		t.Fatalf("package rows left: %d artifacts, %d properties", packages, props)
	}
	if counter != 13 {
		t.Fatalf("artifact_counter = %d", counter)
	}
	cols, err := actualColumns(gdb, &db.ActionsArtifact{})
	if err != nil {
		t.Fatal(err)
	}
	if cols["artifact_id"] || cols["file_count"] {
		t.Fatalf("old columns survived: %v", cols)
	}
	indexes := artifactIndexes(t, gdb)
	for _, name := range []string{"idx_actions_artifacts_run_id", "idx_actions_artifacts_number", "idx_actions_artifacts_digest", "idx_actions_artifacts_status", "idx_actions_artifacts_expires_at"} {
		if _, ok := indexes[name]; !ok {
			t.Fatalf("index %s missing, have %v", name, indexes)
		}
	}
	if sql, ok := indexes[actionsArtifactRunNameIndex]; !ok || !strings.Contains(strings.ToUpper(sql), "UNIQUE") {
		t.Fatalf("unique run name index = %q", sql)
	}
	if _, ok := indexes["idx_actions_artifacts_artifact_id"]; ok {
		t.Fatal("index on the dropped column survived")
	}
	if err := gdb.Exec("INSERT INTO actions_artifacts (id, run_id, name) VALUES ('dup', 'run1', 'dist')").Error; err == nil {
		t.Fatal("duplicate name in a run was accepted")
	}
	// A second pass finds nothing to move and keeps the indexes
	if err := moveActionsArtifactsToRuns(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}
	if len(artifactIndexes(t, gdb)) != len(indexes) {
		t.Fatalf("indexes changed on rerun: %v", artifactIndexes(t, gdb))
	}
}

func TestMoveActionsArtifactsToRunsFreshTable(t *testing.T) {
	gdb := openMigrationDB(t)
	if err := gdb.AutoMigrate(&db.ActionsArtifact{}); err != nil {
		t.Fatal(err)
	}
	if err := moveActionsArtifactsToRuns(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}
	if sql, ok := artifactIndexes(t, gdb)[actionsArtifactRunNameIndex]; !ok || !strings.Contains(strings.ToUpper(sql), "UNIQUE") {
		t.Fatalf("fresh table lacks the unique index: %q", sql)
	}
}
