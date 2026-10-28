package stores

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Old image and git star tables fold into one polymorphic table on upgrade
func TestStarTablesFold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL)`,
		`INSERT INTO users (id, username) VALUES ('u1', 'nick'), ('u2', 'pat')`,
		`CREATE TABLE stars (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, repo_id TEXT NOT NULL, created_at DATETIME,
			CONSTRAINT fk_stars_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE)`,
		`CREATE UNIQUE INDEX idx_star_user_repo ON stars (user_id, repo_id)`,
		`INSERT INTO stars (id, user_id, repo_id, created_at) VALUES ('s1', 'u1', 'img1', '2026-01-01 00:00:00'), ('s2', 'u2', 'img1', '2026-01-02 00:00:00')`,
		`CREATE TABLE git_stars (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, repo_id TEXT NOT NULL, created_at DATETIME)`,
		`INSERT INTO git_stars (id, user_id, repo_id, created_at) VALUES ('g1', 'u1', 'git1', '2026-01-03 00:00:00')`,
	}
	for _, s := range stmts {
		if err := gdb.Exec(s).Error; err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	raw, _ := gdb.DB()
	_ = raw.Close()

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if store.db.Migrator().HasTable("git_stars") {
		t.Fatal("git_stars should be gone")
	}
	if store.db.Migrator().HasColumn(&db.Star{}, "repo_id") {
		t.Fatal("stars.repo_id should be gone")
	}
	counts, err := store.StarCounts(ctx, db.StarKindImage, []string{"img1"})
	if err != nil || counts["img1"] != 2 {
		t.Fatalf("image star count = %v, %v", counts, err)
	}
	gitCounts, err := store.StarCounts(ctx, db.StarKindGit, []string{"git1"})
	if err != nil || gitCounts["git1"] != 1 {
		t.Fatalf("git star count = %v, %v", gitCounts, err)
	}
	set, err := store.StarredSet(ctx, "u1", db.StarKindGit, []string{"git1"})
	if err != nil || !set["git1"] {
		t.Fatalf("starred set = %v, %v", set, err)
	}
	// The unique index now spans the kind, a second star of the same target is a no-op
	created, err := store.Star(ctx, "u1", db.StarKindImage, "img1")
	if err != nil || created {
		t.Fatalf("duplicate star created=%v err=%v", created, err)
	}
	created, err = store.Star(ctx, "u1", db.StarKindArtifact, "7")
	if err != nil || !created {
		t.Fatalf("artifact star created=%v err=%v", created, err)
	}
	byUser, err := store.CountStarsByUser(ctx, "u1")
	if err != nil || byUser[db.StarKindImage] != 1 || byUser[db.StarKindGit] != 1 || byUser[db.StarKindArtifact] != 1 {
		t.Fatalf("stars by user = %v, %v", byUser, err)
	}
	if err := store.Unstar(ctx, "u1", db.StarKindArtifact, "7"); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.CountStars(ctx, db.StarKindArtifact, "7"); n != 0 {
		t.Fatalf("artifact star count after unstar = %d", n)
	}
}

// A copy of a real database upgrades when DF_SMOKE_DB points at it
func TestStarMigrationSmoke(t *testing.T) {
	src := os.Getenv("DF_SMOKE_DB")
	if src == "" {
		t.Skip("DF_SMOKE_DB not set")
	}
	store, err := NewSQLiteStore(src)
	if err != nil {
		t.Fatalf("migrate %s: %v", src, err)
	}
	defer store.Close()
	var n int64
	if err := store.db.Model(&db.Star{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("%d stars after upgrade", n)
	if store.db.Migrator().HasTable("git_stars") {
		t.Fatal("git_stars should be gone")
	}
}
