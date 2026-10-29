package migrations

import (
	"path/filepath"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Flagged package files become release assets and the empty helper package goes
func TestMoveReleaseAssetsOntoReleases(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "m.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE git_repositories (id TEXT PRIMARY KEY, namespace TEXT, name TEXT)`,
		`INSERT INTO git_repositories (id, namespace, name) VALUES ('g1', 'acme', 'app')`,
		`CREATE TABLE git_releases (id TEXT PRIMARY KEY, repo_id TEXT NOT NULL, tag TEXT NOT NULL)`,
		`INSERT INTO git_releases (id, repo_id, tag) VALUES ('r1', 'g1', 'v1.0.0')`,
		`CREATE TABLE artifact_repositories (id INTEGER PRIMARY KEY, namespace TEXT, name TEXT, description TEXT, type INTEGER, git_repo_id TEXT)`,
		`INSERT INTO artifact_repositories (id, namespace, name, description, type, git_repo_id) VALUES (7, 'acme', 'app', 'Release assets of acme/app', 1, 'g1')`,
		`INSERT INTO artifact_repositories (id, namespace, name, description, type, git_repo_id) VALUES (8, 'acme', 'lib', 'Hand made', 1, 'g1')`,
		`CREATE TABLE artifacts (id TEXT PRIMARY KEY, repo_id INTEGER, name TEXT, path TEXT, upload_id TEXT, version TEXT, kind TEXT NOT NULL DEFAULT '', props_hash TEXT NOT NULL DEFAULT '', digest TEXT, size INTEGER, mime_type TEXT, metadata TEXT, created_at DATETIME, updated_at DATETIME)`,
		`INSERT INTO artifacts (id, repo_id, name, path, upload_id, version, digest, size, mime_type, metadata, created_at) VALUES ('a1', 7, 'app.tgz', 'app.tgz', 'u', 'v1.0.0', 'sha256:aa', 10, 'application/gzip', '{}', '2026-01-01 00:00:00')`,
		`INSERT INTO artifacts (id, repo_id, name, path, upload_id, version, digest, size, mime_type, metadata, created_at) VALUES ('a2', 8, 'notes.txt', 'notes.txt', 'u', 'v1.0.0', 'sha256:bb', 3, 'text/plain', '{}', '2026-01-01 00:00:00')`,
		`INSERT INTO artifacts (id, repo_id, name, path, upload_id, version, digest, size, mime_type, metadata, created_at) VALUES ('a3', 8, 'lib.tgz', 'lib.tgz', 'u', 'v1.0.0', 'sha256:cc', 5, 'application/gzip', '{}', '2026-01-01 00:00:00')`,
		`CREATE TABLE artifact_properties (id INTEGER PRIMARY KEY AUTOINCREMENT, artifact_id TEXT, key TEXT, value TEXT)`,
		`INSERT INTO artifact_properties (artifact_id, key, value) VALUES ('a1', 'release', 'true'), ('a1', 'label', 'Tarball'), ('a3', 'release', 'true')`,
	}
	for _, q := range stmts {
		if err := gdb.Exec(q).Error; err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := moveReleaseAssetsOntoReleases(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}
	var assets []db.GitReleaseAsset
	if err := gdb.Order("name").Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[0].Name != "app.tgz" || assets[0].Label != "Tarball" || assets[0].Digest != "sha256:aa" || assets[1].Name != "lib.tgz" {
		t.Fatalf("assets = %+v", assets)
	}
	var left int64
	gdb.Raw(`SELECT COUNT(*) FROM artifacts`).Scan(&left)
	if left != 1 {
		t.Fatalf("expected the unflagged file to stay, %d rows left", left)
	}
	var pkgs []int64
	gdb.Raw(`SELECT id FROM artifact_repositories ORDER BY id`).Scan(&pkgs)
	if len(pkgs) != 1 || pkgs[0] != 8 {
		t.Fatalf("packages left = %v, the empty helper package must go and the hand made one must stay", pkgs)
	}
	var props int64
	gdb.Raw(`SELECT COUNT(*) FROM artifact_properties`).Scan(&props)
	if props != 0 {
		t.Fatalf("moved properties must be deleted, %d left", props)
	}
	if gdb.Migrator().HasColumn(&db.Artifact{}, "kind") {
		t.Fatal("kind column must be dropped")
	}
}
