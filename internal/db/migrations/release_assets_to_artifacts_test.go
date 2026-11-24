package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/migrations"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func TestReleaseAssetsBecomeArtifacts(t *testing.T) {
	store := storetest.New(t)
	gdb := store.DB()
	ctx := context.Background()
	err := gdb.Exec(`CREATE TABLE git_release_assets (
		id text PRIMARY KEY, release_id text NOT NULL, name text NOT NULL, label text NOT NULL DEFAULT '',
		size bigint NOT NULL DEFAULT 0, digest text NOT NULL, content_type text NOT NULL DEFAULT '',
		uploader_id text NOT NULL DEFAULT '', download_count bigint NOT NULL DEFAULT 0,
		created_at timestamp, updated_at timestamp)`).Error
	if err != nil {
		t.Fatal(err)
	}
	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "app", DefaultBranch: "main", IsPrivate: true, OwnerID: "u1"}
	if err := gdb.Create(repo).Error; err != nil {
		t.Fatal(err)
	}
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: "release/1.0", Name: "one", AuthorID: "u1"}
	if err := store.CreateGitRelease(ctx, rel); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	uploaded := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	assetID := uuid.New().String()
	err = gdb.Exec(`INSERT INTO git_release_assets (id, release_id, name, label, size, digest, content_type, download_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, assetID, rel.ID, "app.tar.gz", "Linux build", 42, digest, "application/gzip", 7, uploaded, uploaded).Error
	if err != nil {
		t.Fatal(err)
	}

	if err := migrations.ReleaseAssetsToArtifacts(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}

	if gdb.Migrator().HasTable("git_release_assets") {
		t.Fatal("the legacy table must be dropped")
	}
	pkg, err := store.GetArtifactRepository(ctx, "acme", "app")
	if err != nil || pkg == nil {
		t.Fatalf("release package: %v err %v", pkg, err)
	}
	if !pkg.IsPrivate || pkg.GitRepoID == nil || *pkg.GitRepoID != repo.ID {
		t.Fatalf("package must be private and linked: %+v", pkg)
	}
	a, err := store.GetArtifact(ctx, assetID)
	if err != nil || a == nil {
		t.Fatalf("artifact keeps the asset id: %v err %v", a, err)
	}
	if a.RepoID != pkg.ID || a.Version != "release%2F1.0" || a.Path != "app.tar.gz" || a.Name != "app.tar.gz" {
		t.Fatalf("identity = %+v", a)
	}
	if a.Label != "Linux build" || a.DownloadCount != 7 || a.Size != 42 || a.Digest != digest || a.MimeType != "application/gzip" {
		t.Fatalf("carried fields = %+v", a)
	}
	if a.Properties["release"] != "true" || a.PropsHash != db.PropsFingerprint(a.Properties) {
		t.Fatalf("release flag = %v hash %s", a.Properties, a.PropsHash)
	}
	if !a.CreatedAt.Equal(uploaded) {
		t.Fatalf("created at = %s", a.CreatedAt)
	}
	if ok, err := store.BlobReferenced(ctx, digest); err != nil || !ok {
		t.Fatalf("blob must stay referenced: %v err %v", ok, err)
	}
	// A second run finds nothing to do
	if err := migrations.ReleaseAssetsToArtifacts(gdb, logger.New()); err != nil {
		t.Fatal(err)
	}
}

type legacyFixture struct {
	store *stores.Store
	gdb   *gorm.DB
	repo  *db.GitRepository
	rel   *db.GitRelease
}

func newLegacyFixture(t *testing.T) *legacyFixture {
	t.Helper()
	store := storetest.New(t)
	gdb := store.DB()
	err := gdb.Exec(`CREATE TABLE git_release_assets (
		id text PRIMARY KEY, release_id text NOT NULL, name text NOT NULL, label text NOT NULL DEFAULT '',
		size bigint NOT NULL DEFAULT 0, digest text NOT NULL, content_type text NOT NULL DEFAULT '',
		uploader_id text NOT NULL DEFAULT '', download_count bigint NOT NULL DEFAULT 0,
		created_at timestamp, updated_at timestamp)`).Error
	if err != nil {
		t.Fatal(err)
	}
	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "app", DefaultBranch: "main", OwnerID: "u1"}
	if err := gdb.Create(repo).Error; err != nil {
		t.Fatal(err)
	}
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: "v1", Name: "one", AuthorID: "u1"}
	if err := store.CreateGitRelease(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	return &legacyFixture{store: store, gdb: gdb, repo: repo, rel: rel}
}

func (f *legacyFixture) legacy(t *testing.T, name, label, digest string, downloads int64) string {
	t.Helper()
	id := uuid.New().String()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	err := f.gdb.Exec(`INSERT INTO git_release_assets (id, release_id, name, label, size, digest, content_type, download_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, f.rel.ID, name, label, 1, digest, "application/octet-stream", downloads, now, now).Error
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Package with one artifact already flagged at the tag
func (f *legacyFixture) flagged(t *testing.T, gitRepoID *string, name, digest string, downloads int64) (*db.ArtifactRepository, *db.Artifact) {
	t.Helper()
	ctx := context.Background()
	pkg := &db.ArtifactRepository{Namespace: "acme", Name: "app", GitRepoID: gitRepoID}
	if err := f.store.CreateArtifactRepository(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	a := &db.Artifact{RepoID: pkg.ID, Name: name, Path: name, UploadID: uuid.New().String(), Version: "v1", Digest: digest, Size: 1, Metadata: "{}", DownloadCount: downloads}
	if _, err := f.store.CreateArtifact(ctx, a, map[string]string{"release": "true"}); err != nil {
		t.Fatal(err)
	}
	return pkg, a
}

func (f *legacyFixture) migrate(t *testing.T) {
	t.Helper()
	if err := migrations.ReleaseAssetsToArtifacts(f.gdb, logger.New()); err != nil {
		t.Fatal(err)
	}
}

func sum(c string) string { return "sha256:" + strings.Repeat(c, 64) }

func TestReleaseAssetMigrationMergesSameFile(t *testing.T) {
	f := newLegacyFixture(t)
	ctx := context.Background()
	_, kept := f.flagged(t, nil, "app.bin", sum("a"), 3)
	legacyID := f.legacy(t, "app.bin", "Linux build", sum("a"), 7)
	f.migrate(t)

	got, err := f.store.GetArtifact(ctx, kept.ID)
	if err != nil || got == nil {
		t.Fatalf("kept artifact: %v %v", got, err)
	}
	if got.DownloadCount != 10 || got.Label != "Linux build" || got.Properties["release"] != "true" {
		t.Fatalf("merged artifact = %+v", got)
	}
	if dup, _ := f.store.GetArtifact(ctx, legacyID); dup != nil {
		t.Fatal("the same file must not be stored twice")
	}
	if f.gdb.Migrator().HasTable("git_release_assets") {
		t.Fatal("nothing was left behind, the legacy table must go")
	}
}

func TestReleaseAssetMigrationNeverLinksExistingPackage(t *testing.T) {
	f := newLegacyFixture(t)
	ctx := context.Background()
	pkg := &db.ArtifactRepository{Namespace: "acme", Name: "app"}
	if err := f.store.CreateArtifactRepository(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	legacyID := f.legacy(t, "app.bin", "", sum("a"), 1)
	f.migrate(t)

	if moved, _ := f.store.GetArtifact(ctx, legacyID); moved == nil || moved.RepoID != pkg.ID {
		t.Fatalf("the asset must move into the same named package: %+v", moved)
	}
	got, err := f.store.GetArtifactRepository(ctx, "acme", "app")
	if err != nil || got == nil || got.GitRepoID != nil {
		t.Fatalf("an existing package must stay unlinked: %+v %v", got, err)
	}
}

func TestReleaseAssetMigrationKeepsBothFiles(t *testing.T) {
	f := newLegacyFixture(t)
	ctx := context.Background()
	pkg, old := f.flagged(t, nil, "app.bin", sum("a"), 3)
	legacyID := f.legacy(t, "app.bin", "", sum("b"), 7)
	f.migrate(t)

	asset, err := f.store.GetArtifact(ctx, legacyID)
	if err != nil || asset == nil || asset.Digest != sum("b") || asset.DownloadCount != 7 || asset.Properties["release"] != "true" {
		t.Fatalf("the release must keep serving its own file: %+v %v", asset, err)
	}
	plain, err := f.store.GetArtifact(ctx, old.ID)
	if err != nil || plain == nil || plain.Digest != sum("a") || plain.DownloadCount != 3 {
		t.Fatalf("the earlier artifact must survive: %+v %v", plain, err)
	}
	if plain.Properties["release"] != "false" || plain.PropsHash != db.PropsFingerprint(plain.Properties) {
		t.Fatalf("the earlier artifact must lose the flag: %v", plain.Properties)
	}
	assets, err := f.store.ListArtifactsByProperty(ctx, pkg.ID, "v1", "release", "true")
	if err != nil || len(assets) != 1 || assets[0].ID != legacyID {
		t.Fatalf("exactly the migrated asset is flagged: %d %v", len(assets), err)
	}
	if f.gdb.Migrator().HasTable("git_release_assets") {
		t.Fatal("nothing was left behind, the legacy table must go")
	}
}

func TestReleaseAssetMigrationLeavesForeignPackageRows(t *testing.T) {
	f := newLegacyFixture(t)
	ctx := context.Background()
	other := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "other", DefaultBranch: "main", OwnerID: "u1"}
	if err := f.gdb.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	pkg, _ := f.flagged(t, &other.ID, "theirs.bin", sum("a"), 0)
	legacyID := f.legacy(t, "app.bin", "", sum("b"), 7)
	f.migrate(t)

	var left int64
	if err := f.gdb.Table("git_release_assets").Where("id = ?", legacyID).Count(&left).Error; err != nil || left != 1 {
		t.Fatalf("the asset row must stay recoverable: %d %v", left, err)
	}
	if ok, err := f.store.BlobReferenced(ctx, sum("b")); err != nil || !ok {
		t.Fatalf("its file must stay referenced: %v %v", ok, err)
	}
	if moved, _ := f.store.GetArtifact(ctx, legacyID); moved != nil {
		t.Fatal("nothing may land in another repository's package")
	}
	got, err := f.store.GetArtifactRepository(ctx, "acme", "app")
	if err != nil || got == nil || got.ID != pkg.ID || got.GitRepoID == nil || *got.GitRepoID != other.ID {
		t.Fatalf("the foreign package must be untouched: %+v %v", got, err)
	}
	// A rerun is harmless while rows remain
	f.migrate(t)
}
