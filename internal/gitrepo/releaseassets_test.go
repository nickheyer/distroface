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
	m := NewManager(store, engine, blobs, res, NewAccess(store, enforcer), webhook.NewDispatcher(store, log, res), audit.NewRecorder(store, res, log), log)
	repo := &db.GitRepository{ID: uuid.New().String(), Namespace: "acme", Name: "app", DefaultBranch: "main"}
	if err := store.DB().Create(repo).Error; err != nil {
		t.Fatal(err)
	}
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: "v1.0.0", Name: "v1.0.0", AuthorID: "u1"}
	if err := store.CreateGitRelease(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	return &assetHarness{m: m, store: store, blobs: blobs, repo: repo, rel: rel}
}

func (h *assetHarness) put(t *testing.T, name, body string, replace bool) *db.GitReleaseAsset {
	t.Helper()
	a, err := h.m.StoreReleaseAsset(context.Background(), h.repo, h.rel, ReleaseAssetInput{Name: name, Body: strings.NewReader(body), UploaderID: "u1", Replace: replace})
	if err != nil {
		t.Fatalf("store %s: %v", name, err)
	}
	return a
}

func TestReleaseAssetRoundTrip(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	a := h.put(t, "app.tar.gz", "hello", false)
	if a.Size != 5 || !strings.HasPrefix(a.Digest, "sha256:") || a.ContentType != "application/gzip" {
		t.Fatalf("asset = %+v", a)
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
	fresh, _ := h.store.GetGitReleaseAsset(ctx, a.ID)
	if fresh.DownloadCount != 1 {
		t.Fatalf("download count = %d", fresh.DownloadCount)
	}
	p := h.m.ReleaseAssetProto(ctx, h.repo, h.rel, fresh)
	if !strings.HasSuffix(p.DownloadUrl, "/acme/app/releases/download/v1.0.0/app.tar.gz") || p.DownloadCount != 1 {
		t.Fatalf("proto = %s", proto.MessageName(p))
	}
}

func TestReleaseAssetReplaceAndReap(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	first := h.put(t, "notes.txt", "one", false)
	second := h.put(t, "notes.txt", "two", true)
	if second.ID != first.ID || second.Digest == first.Digest || second.Size != 3 {
		t.Fatalf("replace must keep the row and swap the blob: %+v -> %+v", first, second)
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
	if err := h.m.DeleteReleaseAssets(ctx, h.rel); err != nil {
		t.Fatal(err)
	}
	if f, _, err := h.blobs.OpenBlob(second.Digest); err != nil {
		t.Fatalf("blob still referenced by v2.0.0 must survive: %v", err)
	} else {
		f.Close()
	}
	if err := h.m.DeleteReleaseAssets(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.blobs.OpenBlob(second.Digest); err == nil {
		t.Fatal("last reference gone, blob must be reaped")
	}
}

func TestReleaseAssetSizeCap(t *testing.T) {
	h := newAssetHarness(t)
	ctx := context.Background()
	raw, err := protojson.Marshal(&v1.Settings{Git: &v1.GitSettings{MaxReleaseAssetMb: proto.Int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSettingsValue(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", string(raw)); err != nil {
		t.Fatal(err)
	}
	big := io.LimitReader(zeroReader{}, 2<<20)
	_, err = h.m.StoreReleaseAsset(ctx, h.repo, h.rel, ReleaseAssetInput{Name: "big.bin", Body: big})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected the size cap, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(h.blobs.Root(), "_uploads"))
	if len(entries) != 0 {
		t.Fatalf("a refused upload must leave no partial file, found %d", len(entries))
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
