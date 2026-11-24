package gitrepo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type assetHarness struct {
	m     *Manager
	arts  *artifacts.Manager
	store *stores.Store
	blobs *artifacts.BlobStore
	repo  *db.GitRepository
	rel   *db.GitRelease
}

func newAssetHarness(t *testing.T) *assetHarness {
	t.Helper()
	root := t.TempDir()
	log := logger.New()
	store := storetest.New(t)
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	res := settings.NewResolver(store, nil)
	blobs, err := artifacts.NewBlobStore(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(filepath.Join(root, "git"), root)
	if err != nil {
		t.Fatal(err)
	}
	arts := artifacts.NewManager(store, blobs, res, log)
	m := NewManager(store, engine, arts, res, NewAccess(store, enforcer), webhook.NewDispatcher(store, log, res), audit.NewRecorder(store, res, log), log)
	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "app", DefaultBranch: "main"}
	if err := store.DB().Create(repo).Error; err != nil {
		t.Fatal(err)
	}
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: "v1.0.0", Name: "v1.0.0", AuthorID: "u1"}
	if err := store.CreateGitRelease(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	return &assetHarness{m: m, arts: arts, store: store, blobs: blobs, repo: repo, rel: rel}
}

func (h *assetHarness) put(t *testing.T, name, body string, replace bool) *db.Artifact {
	t.Helper()
	a, err := h.m.StoreReleaseAsset(context.Background(), h.repo, h.rel, ReleaseAssetInput{Name: name, Body: strings.NewReader(body), Replace: replace})
	if err != nil {
		t.Fatalf("store %s: %v", name, err)
	}
	return a
}

func TestReleaseAssetRoundTrip(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	a := h.put(t, "app.tar.gz", "hello", false)
	if a.Size != 5 || !strings.HasPrefix(a.Digest, "sha256:") || a.MimeType != "application/gzip" {
		t.Fatalf("asset = %+v", a)
	}
	pkg, err := h.store.GetArtifactRepository(ctx, "acme", "app")
	if err != nil || pkg == nil {
		t.Fatalf("release package: %v %v", pkg, err)
	}
	if pkg.GitRepoID == nil || *pkg.GitRepoID != h.repo.ID || a.RepoID != pkg.ID {
		t.Fatalf("assets must live in the linked package: %+v", pkg)
	}
	if a.Version != "v1.0.0" || a.Path != "app.tar.gz" || !IsReleaseAsset(a) {
		t.Fatalf("artifact identity = %s %s %v", a.Version, a.Path, a.Properties)
	}
	if _, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "app.tar.gz", Body: strings.NewReader("again")}); err == nil {
		t.Fatal("a duplicate name must be refused without replace")
	}
	for _, bad := range []string{"", "../x", "a/b", "a\\b", "con?"} {
		if _, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: bad, Body: strings.NewReader("x")}); err == nil {
			t.Errorf("name %q must be refused", bad)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/acme/app/releases/download/v1.0.0/app.tar.gz", nil)
	h.m.ServeReleaseAsset(rec, req, a)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("serve: %d %q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="app.tar.gz"`) {
		t.Fatalf("content disposition %q", cd)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("downloads must be hardened")
	}
	fresh, err := h.m.ReleaseAssetByName(ctx, h.repo, h.rel, "app.tar.gz")
	if err != nil || fresh == nil || fresh.DownloadCount != 1 {
		t.Fatalf("download count = %+v err %v", fresh, err)
	}
	if p := artifacts.ToProto(fresh); p.DownloadCount != 1 || p.Name != "app.tar.gz" || p.Properties[ReleaseAssetProperty] != "true" {
		t.Fatalf("proto = %v", p)
	}
}

func TestReleaseAssetsStayApartFromPlainArtifacts(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	asset := h.put(t, "app.bin", "asset", false)
	pkg, _ := h.store.GetArtifactRepository(ctx, "acme", "app")
	plain, err := h.arts.StoreStream(ctx, pkg, strings.NewReader("plain"), artifacts.Upload{Version: "v1.0.0", Path: "ci.log"})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := h.m.ReleaseAssets(ctx, h.repo, h.rel)
	if err != nil || len(assets) != 1 || assets[0].ID != asset.ID {
		t.Fatalf("only flagged artifacts are assets: %v err %v", assets, err)
	}
	if got, _ := h.m.ReleaseAsset(ctx, h.repo, h.rel, plain.ID); got != nil {
		t.Fatal("an unflagged artifact is not an asset")
	}
	if err := h.m.DeleteReleaseAssets(ctx, h.repo, h.rel); err != nil {
		t.Fatal(err)
	}
	if kept, _ := h.store.GetArtifact(ctx, plain.ID); kept == nil {
		t.Fatal("deleting a release must leave plain artifacts alone")
	}
}

func TestReleaseAssetTagWithSlash(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: h.repo.ID, Tag: "release/1.0%", Name: "r", AuthorID: "u1"}
	if err := h.store.CreateGitRelease(ctx, rel); err != nil {
		t.Fatal(err)
	}
	a, err := h.m.StoreReleaseAsset(ctx, h.repo, rel, ReleaseAssetInput{Name: "a.bin", Body: strings.NewReader("x")})
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != "release%2F1.0%25" || AssetTag(a.Version) != rel.Tag {
		t.Fatalf("version %q does not round trip to %q", a.Version, rel.Tag)
	}
	if got, _ := h.m.ReleaseAssetByName(ctx, h.repo, rel, "a.bin"); got == nil {
		t.Fatal("asset of a slashed tag must resolve")
	}
}

func TestReleaseAssetReplaceAndReap(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	first, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "notes.txt", Label: "Notes", Body: strings.NewReader("one")})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.m.ServeReleaseAsset(rec, httptest.NewRequest(http.MethodGet, "/x", nil), first)
	second := h.put(t, "notes.txt", "two", true)
	if second.ID != first.ID || second.Digest == first.Digest || second.Size != 3 {
		t.Fatalf("replace must keep the id and swap the blob: %+v -> %+v", first, second)
	}
	if second.Label != "Notes" || second.DownloadCount != 1 {
		t.Fatalf("replace must keep label and downloads: %q %d", second.Label, second.DownloadCount)
	}
	if _, _, err := h.blobs.OpenBlob(first.Digest); !os.IsNotExist(unwrapPathErr(err)) {
		t.Fatalf("old blob must be reaped, open returned %v", err)
	}
	// A shared digest survives one owner going away
	other := &db.GitRelease{ID: uuid.New().String(), RepoID: h.repo.ID, Tag: "v2.0.0", Name: "v2", AuthorID: "u1"}
	if err := h.store.CreateGitRelease(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.StoreReleaseAsset(ctx, h.repo, other, ReleaseAssetInput{Name: "notes.txt", Body: strings.NewReader("two")}); err != nil {
		t.Fatal(err)
	}
	if err := h.m.DeleteReleaseAssets(ctx, h.repo, h.rel); err != nil {
		t.Fatal(err)
	}
	if f, _, err := h.blobs.OpenBlob(second.Digest); err != nil {
		t.Fatalf("blob still referenced by v2.0.0 must survive: %v", err)
	} else {
		f.Close()
	}
	if err := h.m.DeleteReleaseAssets(ctx, h.repo, other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.blobs.OpenBlob(second.Digest); err == nil {
		t.Fatal("last reference gone, blob must be reaped")
	}
}

func TestReleaseAssetRenameAndLabel(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	a := h.put(t, "old.bin", "x", false)
	name, label := "new.bin", "  Linux build  "
	if err := h.m.UpdateReleaseAsset(ctx, a, &name, &label); err != nil {
		t.Fatal(err)
	}
	got, _ := h.m.ReleaseAssetByName(ctx, h.repo, h.rel, "new.bin")
	if got == nil || got.ID != a.ID || got.Name != "new.bin" || got.Label != "Linux build" {
		t.Fatalf("renamed asset = %+v", got)
	}
}

func TestReleaseAssetSizeCap(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	raw, err := protojson.Marshal(&v1.Settings{Artifacts: &v1.ArtifactSettings{MaxFileSizeMb: proto.Int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSettingsValue(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", string(raw)); err != nil {
		t.Fatal(err)
	}
	big := io.LimitReader(zeroReader{}, 2<<20)
	_, err = h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "big.bin", Body: big})
	if !errors.Is(err, ErrAssetTooLarge) {
		t.Fatalf("expected the size cap, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(h.blobs.Root(), "_uploads"))
	if len(entries) != 0 {
		t.Fatalf("a refused upload must leave no partial file, found %d", len(entries))
	}
}

func TestSealReleasePackage(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	h.put(t, "a.bin", "x", false)
	h.repo.IsPrivate = true
	if err := h.m.SealReleasePackage(ctx, h.repo); err != nil {
		t.Fatal(err)
	}
	if pkg, _ := h.store.GetArtifactRepository(ctx, "acme", "app"); pkg == nil || !pkg.IsPrivate {
		t.Fatal("a private repository must not keep a public release package")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func unwrapPathErr(err error) error {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return pe.Err
	}
	return err
}

func (h *assetHarness) pkg(t *testing.T) *db.ArtifactRepository {
	t.Helper()
	pkg, err := h.store.GetArtifactRepository(context.Background(), "acme", "app")
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestReleaseAssetReadsNeverLink(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	pkg := &db.ArtifactRepository{Namespace: "acme", Name: "app"}
	if err := h.store.CreateArtifactRepository(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	_, err := h.arts.StoreStream(ctx, pkg, strings.NewReader("x"), artifacts.Upload{
		Version: "v1.0.0", Path: "a.bin", Properties: map[string]string{ReleaseAssetProperty: "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := h.m.ReleaseAssets(ctx, h.repo, h.rel)
	if err != nil || len(assets) != 1 {
		t.Fatalf("name linked package must serve its assets: %d %v", len(assets), err)
	}
	if got := h.pkg(t); got.GitRepoID != nil {
		t.Fatal("a read must not link the package")
	}
}

func TestReleaseAssetForeignPackage(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	other := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "other", DefaultBranch: "main"}
	if err := h.store.DB().Create(other).Error; err != nil {
		t.Fatal(err)
	}
	pkg := &db.ArtifactRepository{Namespace: "acme", Name: "app", GitRepoID: &other.ID}
	if err := h.store.CreateArtifactRepository(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	_, err := h.arts.StoreStream(ctx, pkg, strings.NewReader("x"), artifacts.Upload{
		Version: "v1.0.0", Path: "a.bin", Properties: map[string]string{ReleaseAssetProperty: "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assets, err := h.m.ReleaseAssets(ctx, h.repo, h.rel); err != nil || len(assets) != 0 {
		t.Fatalf("another repository's package holds no assets of this one: %d %v", len(assets), err)
	}
	if a, err := h.m.ReleaseAssetByName(ctx, h.repo, h.rel, "a.bin"); err != nil || a != nil {
		t.Fatalf("lookup must miss cleanly: %v %v", a, err)
	}
	if err := h.m.DeleteReleaseAssets(ctx, h.repo, h.rel); err != nil {
		t.Fatalf("release deletion must not trip on a foreign package: %v", err)
	}
	if kept, _ := h.store.ListArtifactsByProperty(ctx, pkg.ID, "", ReleaseAssetProperty, "true"); len(kept) != 1 {
		t.Fatal("the foreign package must be left alone")
	}
	_, err = h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "b.bin", Body: strings.NewReader("x")})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("an upload must be refused, got %v", err)
	}
}

// Package manager stub granting manage to one user
type stubPackages struct {
	PackageManager
	manager string
}

func (s stubPackages) CanManageArtifactRepo(_ context.Context, user *auth.AuthenticatedUser, _ *db.ArtifactRepository) bool {
	return user != nil && user.ID == s.manager
}

func TestUploadLinksOnlyManagedPackage(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	h.m.SetPackageManager(stubPackages{manager: "owner"})
	if err := h.store.CreateArtifactRepository(ctx, &db.ArtifactRepository{Namespace: "acme", Name: "app", IsPrivate: true}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []*auth.AuthenticatedUser{nil, {ID: "writer"}} {
		_, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "a.bin", Body: strings.NewReader("x"), User: user})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("an upload by %+v must be refused, got %v", user, err)
		}
		if got := h.pkg(t); got.GitRepoID != nil {
			t.Fatal("a refused upload must not link the package")
		}
	}
	if kept, _ := h.store.ListArtifactsByProperty(ctx, h.pkg(t).ID, "", ReleaseAssetProperty, "true"); len(kept) != 0 {
		t.Fatal("a refused upload must not store a file")
	}
	_, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "a.bin", Body: strings.NewReader("x"), User: &auth.AuthenticatedUser{ID: "owner"}})
	if err != nil {
		t.Fatalf("a package manager must be able to link: %v", err)
	}
	if got := h.pkg(t); got.GitRepoID == nil || *got.GitRepoID != h.repo.ID {
		t.Fatalf("package must be linked: %+v", got)
	}
	// Once linked any uploader reaches the package
	if _, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "b.bin", Body: strings.NewReader("x"), User: &auth.AuthenticatedUser{ID: "writer"}}); err != nil {
		t.Fatalf("a linked package must take uploads: %v", err)
	}
}

func TestPrivateRepoUploadSealsPublicPackage(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	h.m.SetPackageManager(stubPackages{manager: "owner"})
	if err := h.store.CreateArtifactRepository(ctx, &db.ArtifactRepository{Namespace: "acme", Name: "app"}); err != nil {
		t.Fatal(err)
	}
	h.repo.IsPrivate = true
	_, err := h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "a.bin", Body: strings.NewReader("x"), User: &auth.AuthenticatedUser{ID: "owner"}})
	if err != nil {
		t.Fatal(err)
	}
	pkg := h.pkg(t)
	if !pkg.IsPrivate || pkg.GitRepoID == nil || *pkg.GitRepoID != h.repo.ID {
		t.Fatalf("package must be linked and private: %+v", pkg)
	}
}

func TestSealCoversNameLinkedPackage(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	if err := h.store.CreateArtifactRepository(ctx, &db.ArtifactRepository{Namespace: "acme", Name: "app"}); err != nil {
		t.Fatal(err)
	}
	h.repo.IsPrivate = true
	if err := h.m.SealReleasePackage(ctx, h.repo); err != nil {
		t.Fatal(err)
	}
	if !h.pkg(t).IsPrivate {
		t.Fatal("the name linked package must follow the repository private")
	}
}

func TestReleaseAssetTagTooLong(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: h.repo.ID, Tag: strings.Repeat("a/", 50), Name: "long", AuthorID: "u1"}
	_, err := h.m.StoreReleaseAsset(ctx, h.repo, rel, ReleaseAssetInput{Name: "a.bin", Body: strings.NewReader("x")})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "too long to carry assets") {
		t.Fatalf("expected a clear tag length error, got %v", err)
	}
	if h.pkg(t) != nil {
		t.Fatal("a refused upload must not create the package")
	}
}

func TestReleaseAssetInSubdirectory(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	first := h.put(t, "seed.bin", "x", false)
	pkg := h.pkg(t)
	nested, err := h.arts.StoreStream(ctx, pkg, strings.NewReader("one"), artifacts.Upload{
		Version: first.Version, Path: "linux/app.bin", Properties: map[string]string{ReleaseAssetProperty: "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	replaced := h.put(t, "app.bin", "two!", true)
	if replaced.ID != nested.ID || replaced.Path != "linux/app.bin" || replaced.Size != 4 {
		t.Fatalf("replace must hit the nested artifact: %+v", replaced)
	}
	if assets, _ := h.m.ReleaseAssets(ctx, h.repo, h.rel); len(assets) != 2 {
		t.Fatalf("replace must not add a second copy, have %d", len(assets))
	}
	name := "tool.bin"
	if err := h.m.UpdateReleaseAsset(ctx, replaced, &name, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := h.store.GetArtifact(ctx, nested.ID)
	if got == nil || got.Path != "linux/tool.bin" || got.Name != "tool.bin" {
		t.Fatalf("rename must keep the directory: %+v", got)
	}
}

func TestRevalidationIsNotADownload(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	a := h.put(t, "a.bin", "hello", false)
	serve := func(header, value string) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/acme/app/releases/download/v1.0.0/a.bin", nil)
		if header != "" {
			req.Header.Set(header, value)
		}
		h.m.ServeReleaseAsset(rec, req, a)
		return rec.Code, rec.Header().Get("ETag")
	}
	count := func() int64 {
		got, err := h.store.GetArtifact(ctx, a.ID)
		if err != nil || got == nil {
			t.Fatalf("artifact: %v %v", got, err)
		}
		return got.DownloadCount
	}
	code, etag := serve("", "")
	if code != http.StatusOK || count() != 1 {
		t.Fatalf("a full fetch counts once: %d count %d", code, count())
	}
	if code, _ := serve("If-None-Match", etag); code != http.StatusNotModified || count() != 1 {
		t.Fatalf("a matching etag must answer 304 uncounted: %d count %d", code, count())
	}
	if code, _ := serve("If-None-Match", `"stale"`); code != http.StatusOK || count() != 2 {
		t.Fatalf("a stale etag is a download: %d count %d", code, count())
	}
	if code, _ := serve("Range", "bytes=0-1"); code != http.StatusPartialContent || count() != 2 {
		t.Fatalf("a range must stay uncounted: %d count %d", code, count())
	}
}
