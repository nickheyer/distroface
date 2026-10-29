package actions

import (
	"archive/zip"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
)

// PipelineAPIPrefix is where upload-artifact v1 to v3 and the setup actions look
const PipelineAPIPrefix = "/api/actions_pipeline"

const (
	pipelineRouteBase        = "/_apis/pipelines/workflows/{run_id}/artifacts"
	pipelineFileLengthHeader = "x-tfs-filelength"
	pipelineMD5Header        = "x-actions-results-md5"
	pipelineStorageType      = "actions_storage"
	// Staged files of one upload wait under the block dir here
	pipelineStageDir = "v3"
)

// PipelineHandler serves the actions_pipeline protocol the pre v4 toolkit speaks
func (m *Manager) PipelineHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pipelineRouteBase, m.pipelineList)
	mux.HandleFunc("POST "+pipelineRouteBase, m.pipelineCreate)
	mux.HandleFunc("PATCH "+pipelineRouteBase, m.pipelineFinalize)
	mux.HandleFunc("PUT "+pipelineRouteBase+"/{artifact_hash}/upload", m.pipelineUpload)
	mux.HandleFunc("GET "+pipelineRouteBase+"/{artifact_hash}/path", m.pipelineItems)
	mux.HandleFunc("GET "+pipelineRouteBase+"/{artifact_hash}/download_url", m.pipelineItems)
	mux.HandleFunc("GET "+pipelineRouteBase+"/{artifact_hash}/download", m.pipelineDownload)
	mux.HandleFunc("GET "+pipelineRouteBase+"/{artifact_id}/zip", m.pipelineZip)
	return mux
}

func pipelineJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func pipelineError(w http.ResponseWriter, status int, msg string) {
	pipelineJSON(w, status, map[string]string{"message": msg})
}

// Job behind the bearer, run in the path must match
func (m *Manager) pipelineAuth(w http.ResponseWriter, r *http.Request) (*resultsScope, bool) {
	jobID, err := m.runtimeBearer(r)
	if err != nil {
		pipelineError(w, http.StatusUnauthorized, err.Error())
		return nil, false
	}
	scope, err := m.resultsScopeOf(r, jobID)
	if err != nil {
		pipelineError(w, http.StatusUnauthorized, err.Error())
		return nil, false
	}
	if runID := r.PathValue("run_id"); runID != scope.run.ID && runID != strconv.FormatInt(scope.run.Number, 10) {
		pipelineError(w, http.StatusBadRequest, "run-id does not match")
		return nil, false
	}
	return scope, true
}

// Callback url for the toolkit, hash stands for name
func (m *Manager) pipelineURL(r *http.Request, scope *resultsScope, suffix string) string {
	return strings.TrimRight(m.git.BaseURL(r.Context()), "/") + PipelineAPIPrefix +
		strings.ReplaceAll(pipelineRouteBase, "{run_id}", scope.run.ID) + suffix
}

func artifactNameHash(name string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(name)))
}

// Cleans an item path, slashes normalized, root confined
func cleanItemPath(raw string) string {
	p := path.Clean("/" + strings.ReplaceAll(raw, "\\", "/"))
	if p == "/" {
		return ""
	}
	return p[1:]
}

// Artifact name and inner path, checked against hash
func (m *Manager) pipelineItem(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	name, rel, _ := strings.Cut(cleanItemPath(r.URL.Query().Get("itemPath")), "/")
	if r.PathValue("artifact_hash") != artifactNameHash(name) {
		pipelineError(w, http.StatusBadRequest, "invalid artifact hash")
		return "", "", false
	}
	if err := ValidateArtifactName(name); err != nil {
		pipelineError(w, http.StatusBadRequest, err.Error())
		return "", "", false
	}
	return name, rel, true
}

type pipelineCreateRequest struct {
	Type          string
	Name          string
	RetentionDays int64
}

func (m *Manager) pipelineCreate(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	var req pipelineCreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		pipelineError(w, http.StatusBadRequest, "decode request body: "+err.Error())
		return
	}
	if err := ValidateArtifactName(req.Name); err != nil {
		pipelineError(w, http.StatusBadRequest, err.Error())
		return
	}
	retention := ""
	if req.RetentionDays > 0 {
		retention = "?retentionDays=" + strconv.FormatInt(req.RetentionDays, 10)
	}
	hash := artifactNameHash(req.Name)
	pipelineJSON(w, http.StatusOK, map[string]any{
		"name": req.Name, "type": pipelineStorageType,
		"fileContainerResourceUrl": m.pipelineURL(r, scope, "/"+hash+"/upload"+retention),
		"url":                      m.pipelineURL(r, scope, ""),
	})
}

// Sidecar every chunk of one staged file refreshes
type pipelineFileMeta struct {
	Path string `json:"path"`
	// Real byte count the toolkit announced, negative when it sent none
	Size int64 `json:"size"`
	// Byte count on the wire, gzip when the toolkit compressed it
	Total int64 `json:"total"`
	Gzip  bool  `json:"gzip"`
}

func (m *Manager) pipelineFileDir(artifactID, rel string) string {
	sum := sha256.Sum256([]byte(rel))
	return filepath.Join(m.artifactBlockDir(artifactID), pipelineStageDir, hex.EncodeToString(sum[:20]))
}

// Parses a Content-Range header, end -1 means empty
func parsePipelineRange(h string) (start, end, total int64, err error) {
	if _, err = fmt.Sscanf(strings.TrimSpace(h), "bytes %d-%d/%d", &start, &end, &total); err != nil {
		return 0, 0, 0, errors.New("content-range must be bytes start-end/total")
	}
	if start < 0 || total < 0 || end < start-1 || (total > 0 && end >= total) {
		return 0, 0, 0, errors.New("content-range is out of bounds")
	}
	return start, end, total, nil
}

func (m *Manager) pipelineUpload(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	name, rel, ok := m.pipelineItem(w, r)
	if !ok {
		return
	}
	if rel == "" {
		pipelineError(w, http.StatusBadRequest, "itemPath needs a file path inside the artifact")
		return
	}
	start, end, total, err := parsePipelineRange(r.Header.Get("Content-Range"))
	if err != nil {
		pipelineError(w, http.StatusBadRequest, err.Error())
		return
	}
	size := int64(-1)
	if raw := r.Header.Get(pipelineFileLengthHeader); raw != "" {
		if size, err = strconv.ParseInt(raw, 10, 64); err != nil || size < 0 {
			pipelineError(w, http.StatusBadRequest, pipelineFileLengthHeader+" must be a byte count")
			return
		}
	}
	var requested *time.Time
	if raw := r.URL.Query().Get("retentionDays"); raw != "" {
		days, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || days <= 0 {
			pipelineError(w, http.StatusBadRequest, "retentionDays must be a positive number")
			return
		}
		t := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		requested = &t
	}
	ctx := r.Context()
	a, err := m.claimArtifact(ctx, scope, name, artifactZipType, m.artifactExpiry(ctx, scope.repo, requested), false)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := m.pipelineFileDir(a.ID, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.savePipelineChunk(r, dir, start, end); err != nil {
		// A refused first chunk leaves no half staged file behind
		_ = os.Remove(dir)
		pipelineError(w, http.StatusBadRequest, err.Error())
		return
	}
	meta := pipelineFileMeta{Path: rel, Size: size, Total: total, Gzip: strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip")}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), func(f *os.File) error { return json.NewEncoder(f).Encode(meta) }); err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pipelineJSON(w, http.StatusOK, map[string]string{"message": "success"})
}

// Temp file write so retries never leave torn chunks
func writeFileAtomic(dest string, fill func(*os.File) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".tmp-*")
	if err != nil {
		return err
	}
	if err := fill(tmp); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// Stores one range, length and md5 checked
func (m *Manager) savePipelineChunk(r *http.Request, dir string, start, end int64) error {
	want := end - start + 1
	sum := md5.New()
	return writeFileAtomic(filepath.Join(dir, fmt.Sprintf("%d-%d.chunk", start, end)), func(f *os.File) error {
		n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(r.Body, want))
		if err != nil {
			return err
		}
		if n != want {
			return fmt.Errorf("chunk carried %d bytes, the range announced %d", n, want)
		}
		if got := r.Header.Get(pipelineMD5Header); got != "" && got != base64.StdEncoding.EncodeToString(sum.Sum(nil)) {
			return errors.New("chunk md5 does not match " + pipelineMD5Header)
		}
		return nil
	})
}

type pipelineChunk struct {
	start, end int64
	path       string
}

// One staged file with its ranges in order, repeats dropped, gaps refused
type pipelineFile struct {
	meta   pipelineFileMeta
	chunks []pipelineChunk
}

func (m *Manager) stagedPipelineFiles(artifactID string) ([]pipelineFile, error) {
	root := filepath.Join(m.artifactBlockDir(artifactID), pipelineStageDir)
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []pipelineFile
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		f, err := m.stagedPipelineFile(filepath.Join(root, d.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].meta.Path < files[j].meta.Path })
	return files, nil
}

func (m *Manager) stagedPipelineFile(dir string) (pipelineFile, error) {
	var f pipelineFile
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return f, fmt.Errorf("staged file in %s has no metadata", filepath.Base(dir))
	}
	if err := json.Unmarshal(raw, &f.meta); err != nil {
		return f, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return f, err
	}
	var chunks []pipelineChunk
	for _, e := range entries {
		var c pipelineChunk
		if _, err := fmt.Sscanf(e.Name(), "%d-%d.chunk", &c.start, &c.end); err != nil || !strings.HasSuffix(e.Name(), ".chunk") {
			continue
		}
		c.path = filepath.Join(dir, e.Name())
		chunks = append(chunks, c)
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].start < chunks[j].start })
	var next int64
	for _, c := range chunks {
		switch {
		case c.start == next:
			f.chunks = append(f.chunks, c)
			next = c.end + 1
		case c.start > next:
			return f, fmt.Errorf("%s is missing bytes %d-%d", f.meta.Path, next, c.start-1)
		}
	}
	if next != f.meta.Total {
		return f, fmt.Errorf("%s has %d of %d bytes", f.meta.Path, next, f.meta.Total)
	}
	return f, nil
}

// Reader over the file's bytes as the toolkit read them from disk
func (f pipelineFile) open() (io.ReadCloser, error) {
	readers := make([]io.Reader, 0, len(f.chunks))
	closers := make([]io.Closer, 0, len(f.chunks))
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	for _, c := range f.chunks {
		fh, err := os.Open(c.path)
		if err != nil {
			closeAll()
			return nil, err
		}
		readers = append(readers, fh)
		closers = append(closers, fh)
	}
	r := io.MultiReader(readers...)
	if f.meta.Gzip {
		gz, err := gzip.NewReader(r)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("%s: %w", f.meta.Path, err)
		}
		r = gz
	}
	return struct {
		io.Reader
		io.Closer
	}{r, closerFunc(closeAll)}, nil
}

type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

// Folds staged files into one zip, keeping unreplaced entries
func (m *Manager) buildPipelineZip(a *db.ActionsArtifact, files []pipelineFile) (string, error) {
	dest := filepath.Join(m.artifactBlockDir(a.ID), "merge.zip")
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(out)
	fail := func(err error) (string, error) {
		_ = zw.Close()
		_ = out.Close()
		_ = os.Remove(dest)
		return "", err
	}
	fresh := map[string]bool{}
	for _, f := range files {
		fresh[f.meta.Path] = true
	}
	if a.Digest != "" {
		if err := m.copyZipEntries(zw, a.Digest, fresh); err != nil {
			return fail(err)
		}
	}
	for _, f := range files {
		src, err := f.open()
		if err != nil {
			return fail(err)
		}
		entry, err := zw.CreateHeader(&zip.FileHeader{Name: f.meta.Path, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			src.Close()
			return fail(err)
		}
		n, err := io.Copy(entry, src)
		src.Close()
		if err != nil {
			return fail(fmt.Errorf("%s: %w", f.meta.Path, err))
		}
		if f.meta.Size >= 0 && n != f.meta.Size {
			return fail(fmt.Errorf("%s holds %d bytes, the toolkit announced %d", f.meta.Path, n, f.meta.Size))
		}
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	return dest, nil
}

// Copies stored entries a staged file did not replace
func (m *Manager) copyZipEntries(zw *zip.Writer, digest string, skip map[string]bool) error {
	f, info, err := m.artifacts.Blobs().OpenBlob(digest)
	if err != nil {
		return fmt.Errorf("earlier upload is missing: %w", err)
	}
	defer f.Close()
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return fmt.Errorf("earlier upload is not a zip: %w", err)
	}
	for _, entry := range zr.File {
		if skip[entry.Name] {
			continue
		}
		if err := zw.Copy(entry); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) pipelineFinalize(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("artifactName")
	if err := ValidateArtifactName(name); err != nil {
		pipelineError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	a, err := m.store.GetActionsArtifactByName(ctx, scope.run.ID, name)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a == nil {
		pipelineError(w, http.StatusNotFound, "artifact not found")
		return
	}
	files, err := m.stagedPipelineFiles(a.ID)
	if err != nil {
		pipelineError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(files) == 0 {
		// A repeated confirm after success has nothing left to merge
		if a.Status == ArtifactConfirmed {
			pipelineJSON(w, http.StatusOK, map[string]string{"message": "success"})
			return
		}
		pipelineError(w, http.StatusBadRequest, "no files were uploaded for "+name)
		return
	}
	merged, err := m.buildPipelineZip(a, files)
	if err != nil {
		pipelineError(w, http.StatusBadRequest, err.Error())
		return
	}
	blobs := m.artifacts.Blobs()
	uploadID, err := blobs.AdoptUpload(merged)
	if err != nil {
		_ = os.Remove(merged)
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	digest, size, _, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		_ = blobs.CancelUpload(uploadID)
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cols := map[string]any{"digest": digest, "size": size, "status": ArtifactConfirmed, "content_type": artifactZipType, "job_id": scope.job.ID}
	if err := m.store.UpdateActionsArtifactColumns(ctx, a.ID, cols); err != nil {
		m.gcArtifactBlob(ctx, digest)
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.RemoveAll(m.artifactBlockDir(a.ID))
	if a.Digest != "" && a.Digest != digest {
		m.gcArtifactBlob(ctx, a.Digest)
	}
	pipelineJSON(w, http.StatusOK, map[string]string{"message": "success"})
}

func (m *Manager) pipelineList(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	rows, err := m.RunArtifacts(r.Context(), scope.run)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		item := map[string]any{
			"name": a.Name, "type": pipelineStorageType, "containerId": a.Number, "size": a.Size,
			"fileContainerResourceUrl": m.pipelineURL(r, scope, "/"+artifactNameHash(a.Name)+"/path"),
		}
		if a.ExpiresAt != nil {
			item["expiresOn"] = a.ExpiresAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	pipelineJSON(w, http.StatusOK, map[string]any{"count": len(items), "value": items})
}

// Confirmed artifact by name, 404 when gone or expired
func (m *Manager) pipelineArtifact(w http.ResponseWriter, r *http.Request, scope *resultsScope, name string) (*db.ActionsArtifact, bool) {
	a, err := m.store.GetActionsArtifactByName(r.Context(), scope.run.ID, name)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if a == nil || a.Status != ArtifactConfirmed {
		pipelineError(w, http.StatusNotFound, "artifact not found")
		return nil, false
	}
	if ArtifactExpired(a) {
		pipelineError(w, http.StatusGone, "artifact expired")
		return nil, false
	}
	return a, true
}

// Opens the stored zip of an artifact for entry listing and streaming
func (m *Manager) openArtifactZip(a *db.ActionsArtifact) (*zip.Reader, io.Closer, error) {
	f, info, err := m.artifacts.Blobs().OpenBlob(a.Digest)
	if err != nil {
		return nil, nil, err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return zr, f, nil
}

func (m *Manager) pipelineItems(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	name, rel, ok := m.pipelineItem(w, r)
	if !ok {
		return
	}
	if rel != "" {
		pipelineError(w, http.StatusBadRequest, "itemPath must name the artifact")
		return
	}
	a, ok := m.pipelineArtifact(w, r, scope, name)
	if !ok {
		return
	}
	zr, closer, err := m.openArtifactZip(a)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, "artifact data is unreadable: "+err.Error())
		return
	}
	defer closer.Close()
	download := m.pipelineURL(r, scope, "/"+artifactNameHash(name)+"/download")
	items := make([]map[string]any, 0, len(zr.File))
	for _, entry := range zr.File {
		if strings.HasSuffix(entry.Name, "/") {
			continue
		}
		itemPath := name + "/" + entry.Name
		items = append(items, map[string]any{
			"path": itemPath, "itemType": "file", "fileLength": entry.UncompressedSize64,
			"contentLocation": download + "?itemPath=" + url.QueryEscape(itemPath),
		})
	}
	pipelineJSON(w, http.StatusOK, map[string]any{"count": len(items), "value": items})
}

func (m *Manager) pipelineDownload(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	name, rel, ok := m.pipelineItem(w, r)
	if !ok {
		return
	}
	if rel == "" {
		pipelineError(w, http.StatusBadRequest, "itemPath needs a file path inside the artifact")
		return
	}
	a, ok := m.pipelineArtifact(w, r, scope, name)
	if !ok {
		return
	}
	zr, closer, err := m.openArtifactZip(a)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, "artifact data is unreadable: "+err.Error())
		return
	}
	defer closer.Close()
	for _, entry := range zr.File {
		if entry.Name != rel {
			continue
		}
		src, err := entry.Open()
		if err != nil {
			pipelineError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer src.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatUint(entry.UncompressedSize64, 10))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(rel)))
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, src)
		}
		return
	}
	pipelineError(w, http.StatusNotFound, "file not found in artifact")
}

// Whole zip by GitHub style number or row id
func (m *Manager) pipelineZip(w http.ResponseWriter, r *http.Request) {
	scope, ok := m.pipelineAuth(w, r)
	if !ok {
		return
	}
	id := r.PathValue("artifact_id")
	rows, err := m.store.ListActionsArtifacts(r.Context(), scope.run.ID)
	if err != nil {
		pipelineError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, a := range rows {
		if a.ID == id || strconv.FormatInt(a.Number, 10) == id {
			m.serveConfirmedArtifact(w, r, a)
			return
		}
	}
	pipelineError(w, http.StatusNotFound, "artifact not found")
}
