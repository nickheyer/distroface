package artifacts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/pkg/logger"
	v1proto "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Golden tests lock exact v1 shapes and quirks

type testEnv struct {
	t        *testing.T
	store    *stores.Store
	authMgr  *auth.Manager
	enforcer *rbac.Enforcer
	res      *settings.Resolver
	manager  *Manager
	blobs    *BlobStore
	v1       *V1API
	mux      *http.ServeMux
	blobRoot string
}

func newTestEnv(t *testing.T, retention *v1proto.ArtifactRetentionSettings) *testEnv {
	t.Helper()
	dir := t.TempDir()

	store, err := stores.NewSQLiteStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := enforcer.SeedDefaultPolicies(false); err != nil {
		t.Fatalf("SeedDefaultPolicies: %v", err)
	}

	res := settings.NewResolver(store, nil)
	seed := &v1proto.Settings{Artifacts: &v1proto.ArtifactSettings{
		MaxFileSizeMb: proto.Int64(10),
		Retention:     retention,
	}}
	if _, err := res.SeedSystem(context.Background(), seed); err != nil {
		t.Fatalf("SeedSystem: %v", err)
	}

	authMgr, err := auth.NewManager(store, enforcer, "", res)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	blobRoot := filepath.Join(dir, "artifacts")
	blobs, err := NewBlobStore(blobRoot)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	log := logger.New()
	manager := NewManager(store, blobs, res, log)

	mux := http.NewServeMux()
	v1 := NewV1API(store, manager, authMgr, enforcer, nil, nil, log)
	v1.RegisterAuth(mux)
	v1.RegisterArtifacts(mux)

	return &testEnv{t: t, store: store, authMgr: authMgr, enforcer: enforcer, res: res, manager: manager, blobs: blobs, v1: v1, mux: mux, blobRoot: blobRoot}
}

// Local user with roles, returns session token
func (e *testEnv) newUser(username string, roles ...string) string {
	e.t.Helper()
	ctx := context.Background()
	user, err := e.authMgr.CreateLocalUser(ctx, username, username+"@test.local", "hunter22")
	if err != nil {
		e.t.Fatalf("CreateLocalUser(%s): %v", username, err)
	}
	for _, role := range roles {
		if err := e.store.AssignRole(ctx, user.ID, role, "local"); err != nil {
			e.t.Fatalf("AssignRole(%s,%s): %v", username, role, err)
		}
	}
	_, _, token, _, err := e.authMgr.Login(ctx, username, "hunter22")
	if err != nil {
		e.t.Fatalf("Login(%s): %v", username, err)
	}
	return token
}

func (e *testEnv) do(method, target, token string, body io.Reader) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, target, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) doJSON(method, target, token string, payload any) *httptest.ResponseRecorder {
	e.t.Helper()
	raw, _ := json.Marshal(payload)
	return e.do(method, target, token, bytes.NewReader(raw))
}

// Drives the v1 three step upload flow
func (e *testEnv) uploadArtifact(token, repo, version, path, content string, props map[string]string) {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/v1/artifacts/"+repo+"/upload", token, nil)
	if rec.Code != http.StatusAccepted {
		e.t.Fatalf("initiate upload: got %d body %q", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location == "" || rec.Header().Get("Upload-ID") == "" {
		e.t.Fatalf("initiate upload: missing Location/Upload-ID headers")
	}

	// Cover multi chunk even though dfcli sends one PATCH
	half := len(content) / 2
	for _, chunk := range []string{content[:half], content[half:]} {
		rec = e.do(http.MethodPatch, location, token, strings.NewReader(chunk))
		if rec.Code != http.StatusAccepted {
			e.t.Fatalf("chunk PATCH: got %d body %q", rec.Code, rec.Body.String())
		}
	}

	target := fmt.Sprintf("%s?version=%s&path=%s", location, version, path)
	rec = e.doJSON(http.MethodPut, target, token, props)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("complete PUT: got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestV1LoginAndRefresh(t *testing.T) {
	e := newTestEnv(t, nil)
	e.newUser("alice", "user")

	rec := e.doJSON(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "alice", "password": "hunter22"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: got %d body %q", rec.Code, rec.Body.String())
	}
	var loginResp struct {
		Token     string   `json:"token"`
		ExpiresIn int      `json:"expires_in"`
		Username  string   `json:"username"`
		Groups    []string `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("login response decode: %v", err)
	}
	if loginResp.Token == "" || loginResp.Username != "alice" || loginResp.ExpiresIn <= 0 {
		t.Fatalf("login response fields wrong: %+v", loginResp)
	}
	found := false
	for _, g := range loginResp.Groups {
		if g == "user" {
			found = true
		}
	}
	if !found {
		t.Fatalf("login groups missing role: %v", loginResp.Groups)
	}

	rec = e.doJSON(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "alice", "password": "wrong"})
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != "INVALID CREDENTIALS" {
		t.Fatalf("bad login: got %d body %q", rec.Code, rec.Body.String())
	}

	// Refresh with a session token
	rec = e.doJSON(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": loginResp.Token})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: got %d body %q", rec.Code, rec.Body.String())
	}
	var refreshResp struct {
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &refreshResp)
	if refreshResp.Token == "" {
		t.Fatalf("refresh returned empty token")
	}

	// Refresh with a df_ PAT
	ctx := context.Background()
	user, _ := e.store.GetUserByIdentifier(ctx, "alice")
	pat, _, err := e.authMgr.GenerateAPIToken(ctx, user.ID, "ci", nil)
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}
	rec = e.doJSON(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": pat})
	if rec.Code != http.StatusOK {
		t.Fatalf("PAT refresh: got %d body %q", rec.Code, rec.Body.String())
	}

	rec = e.doJSON(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": "garbage"})
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != "INVALID REFRESH TOKEN" {
		t.Fatalf("bad refresh: got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestV1RepoLifecycle(t *testing.T) {
	e := newTestEnv(t, nil)
	token := e.newUser("alice", "user")

	rec := e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo", "description": "test", "private": false})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create repo: got %d body %q", rec.Code, rec.Body.String())
	}

	rec = e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate repo: got %d", rec.Code)
	}

	rec = e.do(http.MethodGet, "/api/v1/artifacts/repos", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list repos: got %d", rec.Code)
	}
	var repos []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Owner   string `json:"owner"`
		Private bool   `json:"private"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &repos); err != nil {
		t.Fatalf("repo list decode: %v (%s)", err, rec.Body.String())
	}
	if len(repos) != 1 || repos[0].Name != "myrepo" || repos[0].Owner != "alice" || repos[0].ID == 0 {
		t.Fatalf("repo list wrong: %+v", repos)
	}

	// No token, anonymous disabled
	rec = e.do(http.MethodGet, "/api/v1/artifacts/repos", "", nil)
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != "INVALID TOKEN" {
		t.Fatalf("anon list: got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestV1UploadDownloadFlow(t *testing.T) {
	e := newTestEnv(t, nil)
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})

	content := "artifact-content-0123456789"
	e.uploadArtifact(token, "myrepo", "1.0.0", "some/file.txt", content, map[string]string{"build": "42", "branch": "main"})

	// Raw download via the three segment route
	rec := e.do(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/some/file.txt", token, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != content {
		t.Fatalf("raw download: got %d body %q", rec.Code, rec.Body.String())
	}

	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/9.9.9/some/file.txt", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing version download: got %d", rec.Code)
	}

	// Versions must not be shadowed by download route
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/versions", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("versions: got %d body %q", rec.Code, rec.Body.String())
	}
	var versions map[string][]struct {
		ID         string            `json:"id"`
		RepoID     int64             `json:"repo_id"`
		Path       string            `json:"path"`
		UploadID   string            `json:"upload_id"`
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &versions); err != nil {
		t.Fatalf("versions decode: %v", err)
	}
	arts, ok := versions["1.0.0"]
	if !ok || len(arts) != 1 || arts[0].Path != "some/file.txt" || arts[0].UploadID == "" || arts[0].Properties["build"] != "42" {
		t.Fatalf("versions payload wrong: %+v", versions)
	}
	artifactID := arts[0].ID

	// Query must not be shadowed by download route
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/query?build=42", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: got %d body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("query content-type: %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "myrepo-artifacts.zip") {
		t.Fatalf("query content-disposition: %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "1.0.0/some/file.txt" {
		t.Fatalf("zip layout wrong: %v", zr.File[0].Name)
	}
	f, _ := zr.File[0].Open()
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != content {
		t.Fatalf("zip content mismatch")
	}

	// Flat zip uses basenames only
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/query?build=42&flat=1", token, nil)
	zr, err = zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("flat zip parse: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "file.txt" {
		t.Fatalf("flat zip layout wrong: %v", zr.File[0].Name)
	}

	// Tarball format
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/query?format=tar.gz", token, nil)
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("tar.gz content-type: %q", ct)
	}
	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip parse: %v", err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != "1.0.0/some/file.txt" {
		t.Fatalf("tar layout wrong: %v %v", hdr, err)
	}

	// No matches
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/query?build=nope", token, nil)
	if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != "No matching artifacts found" {
		t.Fatalf("no-match query: got %d body %q", rec.Code, rec.Body.String())
	}

	// Wrong method on matching path gives 405
	rec = e.do(http.MethodPost, "/api/v1/artifacts/myrepo/query", token, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed: got %d", rec.Code)
	}

	// Complete without version gives v1 error string
	rec = e.do(http.MethodPost, "/api/v1/artifacts/myrepo/upload", token, nil)
	location := rec.Header().Get("Location")
	rec = e.do(http.MethodPut, location, token, nil)
	if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != "Required parameters missing" {
		t.Fatalf("missing version: got %d body %q", rec.Code, rec.Body.String())
	}

	_ = artifactID
}

func TestV1SearchQuirks(t *testing.T) {
	e := newTestEnv(t, nil)
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})
	e.uploadArtifact(token, "myrepo", "1.0.0", "a.txt", "aaa", map[string]string{"build": "1"})
	e.uploadArtifact(token, "myrepo", "2.0.0", "b.txt", "bbb", map[string]string{"build": "2"})

	rec := e.do(http.MethodGet, "/api/v1/artifacts/search?repo=myrepo", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: got %d body %q", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []json.RawMessage `json:"results"`
		Total   int               `json:"total"`
		Limit   int               `json:"limit"`
		Offset  int               `json:"offset"`
		Sort    string            `json:"sort"`
		Order   string            `json:"order"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("search decode: %v", err)
	}
	// V1 quirks, total is len results and offset zero
	if resp.Total != 2 || resp.Total != len(resp.Results) || resp.Offset != 0 || resp.Sort != "created_at" || resp.Order != "DESC" {
		t.Fatalf("search quirks wrong: %+v", resp)
	}

	// Property filter as arbitrary query param
	rec = e.do(http.MethodGet, "/api/v1/artifacts/search?repo=myrepo&build=2", token, nil)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Fatalf("property search: got %d results", resp.Total)
	}

	// Invalid sort field gives 400
	rec = e.do(http.MethodGet, "/api/v1/artifacts/search?sort=evil", token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid sort: got %d", rec.Code)
	}

	// Empty search must serialize results as [] not null
	e2 := newTestEnv(t, nil)
	token2 := e2.newUser("bob", "user")
	rec = e2.do(http.MethodGet, "/api/v1/artifacts/search", token2, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Fatalf("empty search: got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestV1PropertiesMetadataRename(t *testing.T) {
	e := newTestEnv(t, nil)
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})
	e.uploadArtifact(token, "myrepo", "1.0.0", "dir/old.txt", "content", map[string]string{"build": "1", "keep": "no"})

	id := e.artifactID("myrepo", "1.0.0", "dir/old.txt")

	// Properties PUT replaces the whole set
	rec := e.doJSON(http.MethodPut, "/api/v1/artifacts/myrepo/"+id+"/properties", token, map[string]string{"build": "2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("properties: got %d body %q", rec.Code, rec.Body.String())
	}
	artifact, _ := e.store.GetArtifact(context.Background(), id)
	if artifact.Properties["build"] != "2" || len(artifact.Properties) != 1 {
		t.Fatalf("properties not replaced: %v", artifact.Properties)
	}

	// Metadata PUT stores arbitrary JSON
	rec = e.doJSON(http.MethodPut, "/api/v1/artifacts/myrepo/"+id+"/metadata", token, map[string]any{"ci": true, "job": "nightly"})
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata: got %d body %q", rec.Code, rec.Body.String())
	}

	// Rename keeping directory
	rec = e.doJSON(http.MethodPut, "/api/v1/artifacts/myrepo/"+id+"/rename", token, map[string]string{"name": "new.txt"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: got %d body %q", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/dir/new.txt", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download after rename: got %d", rec.Code)
	}
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/dir/old.txt", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("old path after rename: got %d", rec.Code)
	}

	// Rename onto matching version path properties gives conflict
	e.uploadArtifact(token, "myrepo", "1.0.0", "dir/other.txt", "other", map[string]string{"build": "2"})
	otherID := e.artifactID("myrepo", "1.0.0", "dir/other.txt")
	rec = e.doJSON(http.MethodPut, "/api/v1/artifacts/myrepo/"+otherID+"/rename", token, map[string]string{"name": "new.txt"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("rename conflict: got %d", rec.Code)
	}

	// Different property set may share version and path
	e.uploadArtifact(token, "myrepo", "1.0.0", "dir/third.txt", "third", map[string]string{"build": "3"})
	thirdID := e.artifactID("myrepo", "1.0.0", "dir/third.txt")
	rec = e.doJSON(http.MethodPut, "/api/v1/artifacts/myrepo/"+thirdID+"/rename", token, map[string]string{"name": "new.txt"})
	if rec.Code != http.StatusOK {
		t.Fatalf("variant rename: got %d body %q", rec.Code, rec.Body.String())
	}
}

// Regression for the v1 orphaned rows leak
func TestV1DeleteAndCascade(t *testing.T) {
	e := newTestEnv(t, nil)
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})

	// Same content at two paths dedups to one blob
	e.uploadArtifact(token, "myrepo", "1.0.0", "one.bin", "identical-bytes", map[string]string{"a": "1"})
	e.uploadArtifact(token, "myrepo", "1.0.0", "two.bin", "identical-bytes", map[string]string{"b": "2"})

	blobs := e.blobFiles()
	if len(blobs) != 1 {
		t.Fatalf("dedup failed: %d blobs on disk", len(blobs))
	}

	// Blob survives while still referenced
	rec := e.do(http.MethodDelete, "/api/v1/artifacts/myrepo/1.0.0/one.bin", token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete artifact: got %d body %q", rec.Code, rec.Body.String())
	}
	if len(e.blobFiles()) != 1 {
		t.Fatalf("blob GC'd while still referenced")
	}

	// Deleting a missing artifact gives 404
	rec = e.do(http.MethodDelete, "/api/v1/artifacts/myrepo/1.0.0/one.bin", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing artifact: got %d", rec.Code)
	}

	// Repo delete removes rows and blob
	rec = e.do(http.MethodDelete, "/api/v1/artifacts/repos/myrepo", token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete repo: got %d body %q", rec.Code, rec.Body.String())
	}

	var artifactRows, propertyRows int64
	e.store.DB().Model(&storage.Artifact{}).Count(&artifactRows)
	e.store.DB().Model(&storage.ArtifactProperty{}).Count(&propertyRows)
	if artifactRows != 0 || propertyRows != 0 {
		t.Fatalf("v1 leak regression: %d artifact rows, %d property rows after repo delete", artifactRows, propertyRows)
	}
	if len(e.blobFiles()) != 0 {
		t.Fatalf("blobs not GC'd after repo delete")
	}
}

func TestV1ReplaceAndRetention(t *testing.T) {
	e := newTestEnv(t, &v1proto.ArtifactRetentionSettings{Enabled: proto.Bool(true), MaxVersions: proto.Int32(2)})
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})

	// Same version path re-upload replaces
	e.uploadArtifact(token, "myrepo", "1.0.0", "app.zip", "first-content", nil)
	e.uploadArtifact(token, "myrepo", "1.0.0", "app.zip", "second-content", nil)

	var count int64
	e.store.DB().Model(&storage.Artifact{}).Count(&count)
	if count != 1 {
		t.Fatalf("replace semantics: %d rows for same version+path", count)
	}
	if len(e.blobFiles()) != 1 {
		t.Fatalf("replaced blob not GC'd: %d blobs", len(e.blobFiles()))
	}
	rec := e.do(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/app.zip", token, nil)
	if rec.Body.String() != "second-content" {
		t.Fatalf("replace kept old content: %q", rec.Body.String())
	}

	// MaxVersions keeps newest two versions per path
	e.uploadArtifact(token, "myrepo", "2.0.0", "app.zip", "v2", nil)
	e.uploadArtifact(token, "myrepo", "3.0.0", "app.zip", "v3", nil)

	e.store.DB().Model(&storage.Artifact{}).Count(&count)
	if count != 2 {
		t.Fatalf("retention: %d rows, want 2", count)
	}
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/app.zip", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("retention kept pruned version: got %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/myrepo/3.0.0/app.zip", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("retention pruned newest version: got %d", rec.Code)
	}
}

func TestV1PropertyVariantIdentity(t *testing.T) {
	e := newTestEnv(t, nil)
	token := e.newUser("alice", "user")
	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", token, map[string]any{"name": "myrepo"})

	// Same version path with different properties both survive
	e.uploadArtifact(token, "myrepo", "1.0.0", "app.zip", "x86-content", map[string]string{"arch": "x86"})
	e.uploadArtifact(token, "myrepo", "1.0.0", "app.zip", "arm-content", map[string]string{"arch": "arm"})

	var count int64
	e.store.DB().Model(&storage.Artifact{}).Count(&count)
	if count != 2 {
		t.Fatalf("property variants collapsed: %d rows, want 2", count)
	}

	// Matching properties replace only their own variant
	e.uploadArtifact(token, "myrepo", "1.0.0", "app.zip", "arm-content-v2", map[string]string{"arch": "arm"})
	e.store.DB().Model(&storage.Artifact{}).Count(&count)
	if count != 2 {
		t.Fatalf("same-props re-upload: %d rows, want 2", count)
	}

	// Property query selects the right variant
	for arch, want := range map[string]string{"x86": "x86-content", "arm": "arm-content-v2"} {
		rec := e.do(http.MethodGet, "/api/v1/artifacts/myrepo/query?arch="+arch, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("query arch=%s: got %d body %q", arch, rec.Code, rec.Body.String())
		}
		zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
		if err != nil || len(zr.File) != 1 {
			t.Fatalf("query arch=%s zip: %v", arch, err)
		}
		f, _ := zr.File[0].Open()
		got, _ := io.ReadAll(f)
		f.Close()
		if string(got) != want {
			t.Fatalf("query arch=%s content: %q want %q", arch, got, want)
		}
	}

	// Property edit onto an occupied identity conflicts
	ctx := context.Background()
	r := e.repoByName("myrepo")
	x86, err := e.store.GetArtifactByIdentity(ctx, r.ID, "1.0.0", "app.zip", map[string]string{"arch": "x86"})
	if err != nil || x86 == nil {
		t.Fatalf("GetArtifactByIdentity: %v %v", x86, err)
	}
	rec := e.doJSON(http.MethodPut, "/api/v1/artifacts/myrepo/"+x86.ID+"/properties", token, map[string]string{"arch": "arm"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("props collision: got %d body %q", rec.Code, rec.Body.String())
	}

	// Bare download resolves the newest variant
	rec = e.do(http.MethodGet, "/api/v1/artifacts/myrepo/1.0.0/app.zip", token, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "arm-content-v2" {
		t.Fatalf("bare download: got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestV1AccessControl(t *testing.T) {
	e := newTestEnv(t, nil)
	owner := e.newUser("alice", "user")
	other := e.newUser("bob", "user")

	e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", owner, map[string]any{"name": "secret", "private": true})
	e.uploadArtifact(owner, "secret", "1.0.0", "s.txt", "sssh", nil)

	// Other users can't touch alice's private repo, even addressed directly
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/_ns/alice/secret/1.0.0/s.txt", other, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("private download by non-owner: got %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/api/v1/artifacts/_ns/alice/secret/upload", other, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("private upload by non-owner: got %d", rec.Code)
	}
	if rec := e.do(http.MethodDelete, "/api/v1/artifacts/_ns/alice/repos/secret", other, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("repo delete by non-owner: got %d", rec.Code)
	}

	// Bare names resolve to the caller's own namespace, hiding others' repos
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/secret/1.0.0/s.txt", other, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bare cross-namespace download: got %d", rec.Code)
	}

	// Private repos hidden from other listings
	rec := e.do(http.MethodGet, "/api/v1/artifacts/repos", other, nil)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("private repo leaked in listing: %s", rec.Body.String())
	}

	// V1 quirk, chunk PATCH has no permission gate
	rec = e.do(http.MethodPost, "/api/v1/artifacts/secret/upload", owner, nil)
	location := rec.Header().Get("Location")
	if rec := e.do(http.MethodPatch, location, other, strings.NewReader("x")); rec.Code != http.StatusAccepted {
		t.Fatalf("chunk PATCH permission quirk: got %d", rec.Code)
	}

	// Owner retains full control
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/secret/1.0.0/s.txt", owner, nil); rec.Code != http.StatusOK {
		t.Fatalf("owner download: got %d", rec.Code)
	}
	if rec := e.do(http.MethodDelete, "/api/v1/artifacts/repos/secret", owner, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("owner repo delete: got %d", rec.Code)
	}
}

// ── Test helpers ─────────────────────────────────────────────────────────

// Finds a repo by bare name across namespaces for tests
func (e *testEnv) repoByName(name string) *storage.ArtifactRepository {
	e.t.Helper()
	repos, _, err := e.store.ListArtifactRepositories(context.Background(), stores.ArtifactRepoListOptions{IncludePrivate: true})
	if err != nil {
		e.t.Fatalf("list repos: %v", err)
	}
	for _, r := range repos {
		if r.Name == name {
			return r
		}
	}
	e.t.Fatalf("repo %s not found", name)
	return nil
}

func (e *testEnv) artifactID(repo, version, path string) string {
	e.t.Helper()
	r := e.repoByName(repo)
	a, err := e.store.GetArtifactByPathVersion(context.Background(), r.ID, version, path)
	if err != nil || a == nil {
		e.t.Fatalf("artifact %s@%s not found", path, version)
	}
	return a.ID
}

func (e *testEnv) blobFiles() []string {
	e.t.Helper()
	var files []string
	filepath.WalkDir(filepath.Join(e.blobRoot, "blobs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	return files
}

// Org namespace repos follow membership for access
func TestV1OrgNamespaceAccess(t *testing.T) {
	e := newTestEnv(t, nil)
	ctx := context.Background()

	owner := e.newUser("orgowner", "user")
	member := e.newUser("orgmember", "user")
	outsider := e.newUser("outsider", "user")

	org := &storage.Organization{Name: "acme", DisplayName: "Acme", CreatedBy: "test"}
	if err := e.store.CreateOrganization(ctx, org); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	addMember := func(username, role string) {
		u, err := e.store.GetUserByUsername(ctx, username)
		if err != nil || u == nil {
			t.Fatalf("GetUserByUsername(%s): %v", username, err)
		}
		if err := e.store.AddOrgMember(ctx, &storage.OrgMember{OrgID: org.ID, UserID: u.ID, Role: role}); err != nil {
			t.Fatalf("AddOrgMember(%s): %v", username, err)
		}
	}
	addMember("orgowner", storage.OrgRoleOwner)
	addMember("orgmember", storage.OrgRoleMember)

	// Plain members cannot create org repos
	if rec := e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", member, map[string]any{"name": "kits", "namespace": "acme", "private": true}); rec.Code != http.StatusForbidden {
		t.Fatalf("member create in org: got %d", rec.Code)
	}
	// Unknown namespaces reject creation outright
	if rec := e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", owner, map[string]any{"name": "kits", "namespace": "ghost"}); rec.Code != http.StatusForbidden {
		t.Fatalf("create in unknown namespace: got %d", rec.Code)
	}
	// Org owners create org repos
	if rec := e.doJSON(http.MethodPost, "/api/v1/artifacts/repos", owner, map[string]any{"name": "kits", "namespace": "acme", "private": true}); rec.Code != http.StatusCreated {
		t.Fatalf("owner create in org: got %d body %q", rec.Code, rec.Body.String())
	}

	// Upload follows the namespaced Location header end to end
	e.uploadArtifact(owner, "_ns/acme/kits", "1.0.0", "kit.bin", "data", nil)

	// Members read private org repos, plain push stays denied
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/_ns/acme/kits/1.0.0/kit.bin", member, nil); rec.Code != http.StatusOK {
		t.Fatalf("member download: got %d body %q", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodPost, "/api/v1/artifacts/_ns/acme/kits/upload", member, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member upload: got %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v1/artifacts/_ns/acme/kits/1.0.0/kit.bin", outsider, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider download: got %d", rec.Code)
	}

	// Destructive operations stay owner/admin only
	if rec := e.do(http.MethodDelete, "/api/v1/artifacts/_ns/acme/repos/kits", member, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member repo delete: got %d", rec.Code)
	}
	if rec := e.do(http.MethodDelete, "/api/v1/artifacts/_ns/acme/repos/kits", owner, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("owner repo delete: got %d body %q", rec.Code, rec.Body.String())
	}
}
