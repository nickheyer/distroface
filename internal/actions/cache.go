package actions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
)

// Archive and repository caps, GitHub's numbers
const (
	cacheMaxArchiveBytes int64 = 10 << 30
	cacheRepoCapBytes    int64 = 10 << 30
	cacheUnusedTTL             = 7 * 24 * time.Hour
	cacheReservedTTL           = time.Hour
	cacheURLTTL                = time.Hour
	cacheMaxKeyLen             = 512
	cacheCandidates            = 500
)

// Job, run, and repository behind a cache request
type cacheScope struct {
	job  *db.ActionsJob
	run  *db.ActionsRun
	repo *db.GitRepository
}

// CacheHandler serves the legacy artifactcache protocol under the cache url
func (m *Manager) CacheHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_apis/artifactcache/cache", m.cacheLookup)
	mux.HandleFunc("POST /_apis/artifactcache/caches", m.cacheReserve)
	mux.HandleFunc("PATCH /_apis/artifactcache/caches/{id}", m.cacheUpload)
	mux.HandleFunc("POST /_apis/artifactcache/caches/{id}", m.cacheCommit)
	mux.HandleFunc("GET /_apis/artifactcache/artifacts/{id}", m.cacheDownload)
	return mux
}

func cacheJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func cacheError(w http.ResponseWriter, status int, msg string) {
	cacheJSON(w, status, map[string]any{"message": msg})
}

// Resolves the running job behind the bearer token
func (m *Manager) cacheAuth(w http.ResponseWriter, r *http.Request) (*cacheScope, bool) {
	ctx := r.Context()
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" || token == r.Header.Get("Authorization") {
		cacheError(w, http.StatusUnauthorized, "job token required")
		return nil, false
	}
	user, err := m.auth.ValidateAPIToken(ctx, token)
	if err != nil || user == nil || user.TokenID == "" {
		cacheError(w, http.StatusUnauthorized, "invalid job token")
		return nil, false
	}
	job, err := m.store.GetActionsJobByTokenID(ctx, user.TokenID)
	if err != nil {
		cacheError(w, http.StatusInternalServerError, "lookup failed")
		return nil, false
	}
	if job == nil {
		cacheError(w, http.StatusUnauthorized, "token is not bound to a job")
		return nil, false
	}
	if job.Status != StatusRunning {
		cacheError(w, http.StatusForbidden, "job is not running")
		return nil, false
	}
	run, err := m.store.GetActionsRunByID(ctx, job.RunID)
	if err != nil || run == nil {
		cacheError(w, http.StatusForbidden, "run not found")
		return nil, false
	}
	repo, err := m.store.GetGitRepositoryByID(ctx, run.RepoID)
	if err != nil || repo == nil {
		cacheError(w, http.StatusForbidden, "repository not found")
		return nil, false
	}
	return &cacheScope{job: job, run: run, repo: repo}, true
}

// Refs whose entries restore first, the run's own then the default branch
func (sc *cacheScope) preferredRefs() []string {
	return []string{sc.run.Ref, "refs/heads/" + sc.repo.DefaultBranch}
}

// Comma separated, trimmed, empty entries dropped
func parseCacheKeys(raw string) []string {
	var out []string
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Best candidate by ref preference, candidates arrive newest first
func pickCache(entries []*db.ActionsCache, key string, exact bool, prefer []string) *db.ActionsCache {
	var best *db.ActionsCache
	bestRank := len(prefer) + 1
	for _, e := range entries {
		if exact && e.Key != key {
			continue
		}
		if !exact && !strings.HasPrefix(e.Key, key) {
			continue
		}
		rank := len(prefer)
		for i, ref := range prefer {
			if ref != "" && e.Ref == ref {
				rank = i
				break
			}
		}
		if rank < bestRank {
			best, bestRank = e, rank
			if rank == 0 {
				break
			}
		}
	}
	return best
}

func (m *Manager) cacheLookup(w http.ResponseWriter, r *http.Request) {
	sc, ok := m.cacheAuth(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	keys := parseCacheKeys(r.URL.Query().Get("keys"))
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	if len(keys) == 0 || version == "" {
		cacheError(w, http.StatusBadRequest, "keys and version are required")
		return
	}
	prefer := sc.preferredRefs()
	candidates := map[string][]*db.ActionsCache{}
	load := func(key string) []*db.ActionsCache {
		if list, seen := candidates[key]; seen {
			return list
		}
		list, err := m.store.ListActionsCachesByPrefix(ctx, sc.repo.ID, key, version, cacheCandidates)
		if err != nil {
			m.log.Warn("actions: cache lookup %s: %v", key, err)
		}
		candidates[key] = list
		return list
	}
	var hit *db.ActionsCache
	for _, key := range keys {
		if hit = pickCache(load(key), key, true, prefer); hit != nil {
			break
		}
	}
	if hit == nil {
		for _, key := range keys {
			if hit = pickCache(load(key), key, false, prefer); hit != nil {
				break
			}
		}
	}
	if hit == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = m.store.UpdateActionsCacheColumns(ctx, hit.ID, map[string]any{"last_used_at": time.Now().UTC()})
	exp := time.Now().Add(cacheURLTTL).Unix()
	location := fmt.Sprintf("%s/api/actions/cache/_apis/artifactcache/artifacts/%d?exp=%d&sig=%s",
		strings.TrimRight(m.git.BaseURL(ctx), "/"), hit.ID, exp, cacheSignature(m.cacheKey(), hit.ID, exp))
	cacheJSON(w, http.StatusOK, map[string]any{"result": "hit", "archiveLocation": location, "cacheKey": hit.Key})
}

func (m *Manager) cacheKey() []byte {
	return m.auth.DerivedKey("actions-cache")
}

// Hmac over the entry id and expiry, hex encoded
func cacheSignature(key []byte, id, exp int64) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%d:%d", id, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyCacheSignature(key []byte, id, exp int64, sig string, now time.Time) bool {
	if exp <= now.Unix() {
		return false
	}
	want, err := hex.DecodeString(cacheSignature(key, id, exp))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

func (m *Manager) cacheReserve(w http.ResponseWriter, r *http.Request) {
	sc, ok := m.cacheAuth(w, r)
	if !ok {
		return
	}
	if sc.run.FromFork {
		cacheError(w, http.StatusForbidden, "fork runs cannot save caches")
		return
	}
	var body struct {
		Key       string `json:"key"`
		Version   string `json:"version"`
		CacheSize int64  `json:"cacheSize"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		cacheError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body.Key, body.Version = strings.TrimSpace(body.Key), strings.TrimSpace(body.Version)
	if body.Key == "" || body.Version == "" || len(body.Key) > cacheMaxKeyLen || strings.ContainsAny(body.Key, ",\n") {
		cacheError(w, http.StatusBadRequest, "key and version are required, keys hold no commas")
		return
	}
	if body.CacheSize > cacheMaxArchiveBytes {
		cacheError(w, http.StatusBadRequest, fmt.Sprintf("cache size of %d bytes is over the %d GB limit", body.CacheSize, cacheMaxArchiveBytes>>30))
		return
	}
	ctx := r.Context()
	exists, err := m.store.ActionsCacheExists(ctx, sc.repo.ID, body.Key, body.Version, time.Now().Add(-cacheReservedTTL))
	if err != nil {
		cacheError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if exists {
		cacheError(w, http.StatusConflict, fmt.Sprintf("cache entry already exists for key %s", body.Key))
		return
	}
	row := &db.ActionsCache{RepoID: sc.repo.ID, Key: body.Key, Version: body.Version, Ref: sc.run.Ref, Size: body.CacheSize, State: stores.ActionsCacheReserved, LastUsedAt: time.Now().UTC()}
	if err := m.store.CreateActionsCache(ctx, row); err != nil {
		cacheError(w, http.StatusInternalServerError, "reserve failed")
		return
	}
	cacheJSON(w, http.StatusCreated, map[string]any{"cacheId": row.ID})
}

// Reserved entry of the scope named by the path id
func (m *Manager) reservedCache(w http.ResponseWriter, r *http.Request, sc *cacheScope) (*db.ActionsCache, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		cacheError(w, http.StatusBadRequest, "invalid cache id")
		return nil, false
	}
	row, err := m.store.GetActionsCacheInRepo(r.Context(), sc.repo.ID, id)
	if err != nil {
		cacheError(w, http.StatusInternalServerError, "lookup failed")
		return nil, false
	}
	if row == nil {
		cacheError(w, http.StatusNotFound, "cache entry not found")
		return nil, false
	}
	if row.State != stores.ActionsCacheReserved {
		cacheError(w, http.StatusConflict, "cache entry is already committed")
		return nil, false
	}
	return row, true
}

// Start and end of a bytes start-end/* header
func parseContentRange(h string) (int64, int64, error) {
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(h, "bytes ") {
		return 0, 0, errors.New("content-range must use bytes")
	}
	span, _, _ := strings.Cut(strings.TrimPrefix(h, "bytes "), "/")
	startText, endText, ok := strings.Cut(span, "-")
	if !ok {
		return 0, 0, errors.New("content-range needs start-end")
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startText), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, errors.New("bad content-range start")
	}
	end, err := strconv.ParseInt(strings.TrimSpace(endText), 10, 64)
	if err != nil || end < start {
		return 0, 0, errors.New("bad content-range end")
	}
	return start, end, nil
}

// Staging area for chunks before they enter the blob store
func (m *Manager) cacheStagingDir() string {
	return filepath.Join(m.artifacts.Blobs().Root(), "cache-uploads")
}

func (m *Manager) cacheStagingPath(id int64) string {
	return filepath.Join(m.cacheStagingDir(), strconv.FormatInt(id, 10))
}

func (m *Manager) cacheUpload(w http.ResponseWriter, r *http.Request) {
	sc, ok := m.cacheAuth(w, r)
	if !ok {
		return
	}
	if sc.run.FromFork {
		cacheError(w, http.StatusForbidden, "fork runs cannot save caches")
		return
	}
	row, ok := m.reservedCache(w, r, sc)
	if !ok {
		return
	}
	start, end, err := parseContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		cacheError(w, http.StatusBadRequest, err.Error())
		return
	}
	if end >= cacheMaxArchiveBytes {
		cacheError(w, http.StatusBadRequest, "chunk exceeds the archive size limit")
		return
	}
	if err := os.MkdirAll(m.cacheStagingDir(), 0o755); err != nil {
		cacheError(w, http.StatusInternalServerError, "staging unavailable")
		return
	}
	f, err := os.OpenFile(m.cacheStagingPath(row.ID), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		cacheError(w, http.StatusInternalServerError, "staging unavailable")
		return
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		cacheError(w, http.StatusInternalServerError, "seek failed")
		return
	}
	if _, err := io.Copy(f, io.LimitReader(r.Body, end-start+1)); err != nil {
		cacheError(w, http.StatusInternalServerError, "write failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) cacheCommit(w http.ResponseWriter, r *http.Request) {
	sc, ok := m.cacheAuth(w, r)
	if !ok {
		return
	}
	if sc.run.FromFork {
		cacheError(w, http.StatusForbidden, "fork runs cannot save caches")
		return
	}
	row, ok := m.reservedCache(w, r, sc)
	if !ok {
		return
	}
	var body struct {
		Size int64 `json:"size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		cacheError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ctx := r.Context()
	staging := m.cacheStagingPath(row.ID)
	info, err := os.Stat(staging)
	if err != nil || info.Size() != body.Size || body.Size <= 0 {
		m.dropCache(ctx, row)
		cacheError(w, http.StatusBadRequest, "uploaded bytes do not match the declared size")
		return
	}
	digest, size, err := m.stageCacheBlob(staging)
	if err != nil {
		m.log.Warn("actions: cache commit %d: %v", row.ID, err)
		m.dropCache(ctx, row)
		cacheError(w, http.StatusInternalServerError, "storing the archive failed")
		return
	}
	now := time.Now().UTC()
	cols := map[string]any{"digest": digest, "size": size, "state": stores.ActionsCacheComplete, "last_used_at": now}
	if err := m.store.UpdateActionsCacheColumns(ctx, row.ID, cols); err != nil {
		cacheError(w, http.StatusInternalServerError, "commit failed")
		return
	}
	_ = os.Remove(staging)
	m.enforceCacheCap(ctx, sc.repo.ID, row.ID)
	w.WriteHeader(http.StatusNoContent)
}

// Streams a staged file into the blob store, answering its digest
func (m *Manager) stageCacheBlob(path string) (string, int64, error) {
	blobs := m.artifacts.Blobs()
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		return "", 0, err
	}
	if _, err := blobs.AppendChunk(uploadID, f); err != nil {
		_ = blobs.CancelUpload(uploadID)
		return "", 0, err
	}
	digest, size, _, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		_ = blobs.CancelUpload(uploadID)
		return "", 0, err
	}
	return digest, size, nil
}

func (m *Manager) cacheDownload(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		cacheError(w, http.StatusBadRequest, "invalid cache id")
		return
	}
	exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil || !verifyCacheSignature(m.cacheKey(), id, exp, r.URL.Query().Get("sig"), time.Now()) {
		cacheError(w, http.StatusForbidden, "download link is invalid or expired")
		return
	}
	row, err := m.store.GetActionsCache(r.Context(), id)
	if err != nil || row == nil || row.State != stores.ActionsCacheComplete || row.Digest == "" {
		cacheError(w, http.StatusNotFound, "cache entry not found")
		return
	}
	f, info, err := m.artifacts.Blobs().OpenBlob(row.Digest)
	if err != nil {
		cacheError(w, http.StatusNotFound, "cache archive not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// Evicts least recently used entries until the repository fits the cap
func (m *Manager) enforceCacheCap(ctx context.Context, repoID string, keep int64) {
	total, err := m.store.SumActionsCacheBytes(ctx, repoID)
	if err != nil || total <= cacheRepoCapBytes {
		return
	}
	lru, err := m.store.ListActionsCachesLRU(ctx, repoID, 200)
	if err != nil {
		return
	}
	for _, c := range lru {
		if total <= cacheRepoCapBytes {
			return
		}
		if c.ID == keep {
			continue
		}
		m.dropCache(ctx, c)
		total -= c.Size
	}
}

// Removes the row, its staging file, and an orphaned blob
func (m *Manager) dropCache(ctx context.Context, c *db.ActionsCache) {
	_ = os.Remove(m.cacheStagingPath(c.ID))
	if err := m.store.DeleteActionsCache(ctx, c.ID); err != nil {
		m.log.Warn("actions: delete cache %d: %v", c.ID, err)
		return
	}
	if c.Digest == "" {
		return
	}
	if n, _ := m.store.CountActionsCachesByDigest(ctx, c.Digest); n > 0 {
		return
	}
	if n, _ := m.store.CountArtifactsByDigest(ctx, c.Digest); n > 0 {
		return
	}
	if n, _ := m.store.CountActionsJobsByLogDigest(ctx, c.Digest); n > 0 {
		return
	}
	if err := m.artifacts.Blobs().DeleteBlob(c.Digest); err != nil {
		m.log.Warn("actions: delete cache blob %s: %v", c.Digest, err)
	}
}

// Drops stale reservations, idle entries, and orphaned staging files
func (m *Manager) pruneCaches(ctx context.Context) {
	now := time.Now()
	if stale, err := m.store.ListReservedActionsCachesBefore(ctx, now.Add(-cacheReservedTTL), 500); err == nil {
		for _, c := range stale {
			m.dropCache(ctx, c)
		}
	}
	if idle, err := m.store.ListActionsCachesUnusedBefore(ctx, now.Add(-cacheUnusedTTL), 500); err == nil {
		for _, c := range idle {
			m.dropCache(ctx, c)
		}
	}
	entries, err := os.ReadDir(m.cacheStagingDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(now.Add(-cacheReservedTTL)) {
			continue
		}
		row, _ := m.store.GetActionsCache(ctx, id)
		if row == nil || row.State != stores.ActionsCacheReserved {
			_ = os.Remove(m.cacheStagingPath(id))
		}
	}
}
