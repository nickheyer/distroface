package gitrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
)

const lfsMediaType = "application/vnd.git-lfs+json"

var oidPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// LFS serves the batch api, basic transfer, verify, and locks
type LFS struct {
	manager *Manager
	tokens  *authTokenSigner
	log     *logger.Logger
}

// Token signer used by git-lfs-authenticate
type authTokenSigner = auth.TokenService

func NewLFS(manager *Manager, tokens *auth.TokenService, log *logger.Logger) *LFS {
	return &LFS{manager: manager, tokens: tokens, log: log}
}

type lfsBatchRequest struct {
	Operation string   `json:"operation"`
	Transfers []string `json:"transfers"`
	Ref       struct {
		Name string `json:"name"`
	} `json:"ref"`
	Objects  []lfsObjectRef `json:"objects"`
	HashAlgo string         `json:"hash_algo"`
}

type lfsObjectRef struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
}

type lfsAction struct {
	Href      string            `json:"href"`
	Header    map[string]string `json:"header,omitempty"`
	ExpiresIn int               `json:"expires_in,omitempty"`
}

type lfsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lfsObjectResponse struct {
	OID           string                `json:"oid"`
	Size          int64                 `json:"size"`
	Authenticated bool                  `json:"authenticated,omitempty"`
	Actions       map[string]*lfsAction `json:"actions,omitempty"`
	Error         *lfsError             `json:"error,omitempty"`
}

type lfsBatchResponse struct {
	Transfer string               `json:"transfer"`
	Objects  []*lfsObjectResponse `json:"objects"`
	HashAlgo string               `json:"hash_algo"`
}

type lfsLock struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	LockedAt time.Time `json:"locked_at"`
	Owner    struct {
		Name string `json:"name"`
	} `json:"owner"`
}

// Strict parse shared by the write classifier and the handler, body restored
func parseLFSBatch(r *http.Request) (*lfsBatchRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req lfsBatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// Reports whether a request under info/lfs writes
func (l *LFS) IsWrite(r *http.Request, rest string) bool {
	switch {
	case rest == "objects/batch" && r.Method == http.MethodPost:
		// Malformed bodies count as writes, the handler rejects them anyway
		req, err := parseLFSBatch(r)
		return err != nil || req.Operation == "upload"
	case strings.HasPrefix(rest, "objects/") && r.Method == http.MethodPut:
		return true
	case strings.HasPrefix(rest, "locks"):
		return r.Method == http.MethodPost
	}
	return false
}

func writeLFSJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", lfsMediaType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func lfsFail(w http.ResponseWriter, status int, msg string) {
	writeLFSJSON(w, status, map[string]string{"message": msg})
}

// Routes one lfs request
func (l *LFS) ServeHTTP(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, user *auth.AuthenticatedUser, rest string) {
	if !repo.LFSEnabled || !l.manager.gitSettings(r.Context(), repo).GetLfsEnabled() {
		lfsFail(w, http.StatusNotFound, "lfs is disabled for this repository")
		return
	}
	switch {
	case rest == "objects/batch" && r.Method == http.MethodPost:
		l.batch(w, r, repo, user)
	case strings.HasPrefix(rest, "objects/") && r.Method == http.MethodGet:
		l.ServeObject(w, r, repo, strings.TrimPrefix(rest, "objects/"))
	case strings.HasPrefix(rest, "objects/") && r.Method == http.MethodPut:
		l.upload(w, r, repo, strings.TrimPrefix(rest, "objects/"))
	case rest == "verify" && r.Method == http.MethodPost:
		l.verify(w, r, repo)
	case rest == "locks" && r.Method == http.MethodPost:
		l.createLock(w, r, repo, user)
	case rest == "locks" && r.Method == http.MethodGet:
		l.listLocks(w, r, repo)
	case rest == "locks/verify" && r.Method == http.MethodPost:
		l.verifyLocks(w, r, repo, user)
	case strings.HasPrefix(rest, "locks/") && strings.HasSuffix(rest, "/unlock") && r.Method == http.MethodPost:
		l.unlock(w, r, repo, user, strings.TrimSuffix(strings.TrimPrefix(rest, "locks/"), "/unlock"))
	default:
		lfsFail(w, http.StatusNotFound, "unknown lfs endpoint")
	}
}

func (l *LFS) objectURL(ctx context.Context, repo *db.GitRepository, oid string) string {
	return l.manager.HTTPCloneURL(ctx, repo, false) + "/info/lfs/objects/" + oid
}

func (l *LFS) batch(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, user *auth.AuthenticatedUser) {
	req, err := parseLFSBatch(r)
	if err != nil {
		lfsFail(w, http.StatusBadRequest, "invalid batch request")
		return
	}
	if req.HashAlgo != "" && req.HashAlgo != "sha256" {
		lfsFail(w, http.StatusConflict, "only sha256 objects are supported")
		return
	}
	ctx := r.Context()
	upload := req.Operation == "upload"
	if !upload && req.Operation != "download" {
		lfsFail(w, http.StatusBadRequest, "operation must be download or upload")
		return
	}
	header := map[string]string{}
	if a := r.Header.Get("Authorization"); a != "" {
		header["Authorization"] = a
	}
	maxBytes := l.manager.gitSettings(ctx, repo).GetMaxLfsObjectMb() * 1024 * 1024
	resp := lfsBatchResponse{Transfer: "basic", HashAlgo: "sha256"}
	// One lookup covers the whole batch
	oids := make([]string, 0, len(req.Objects))
	for _, o := range req.Objects {
		if oidPattern.MatchString(o.OID) {
			oids = append(oids, o.OID)
		}
	}
	known, lookupErr := l.manager.Store().GetLFSObjects(ctx, repo.ID, oids)
	for _, o := range req.Objects {
		obj := &lfsObjectResponse{OID: o.OID, Size: o.Size, Authenticated: true}
		resp.Objects = append(resp.Objects, obj)
		if !oidPattern.MatchString(o.OID) {
			obj.Error = &lfsError{Code: 422, Message: "invalid oid"}
			continue
		}
		if lookupErr != nil {
			obj.Error = &lfsError{Code: 500, Message: "lookup failed"}
			continue
		}
		existing := known[o.OID]
		if upload {
			if maxBytes > 0 && o.Size > maxBytes {
				obj.Error = &lfsError{Code: 422, Message: fmt.Sprintf("object exceeds the %d MB limit", maxBytes/(1024*1024))}
				continue
			}
			if existing != nil {
				continue
			}
			// Only bytes the caller could read anyway link without a transfer
			if l.readableElsewhere(ctx, user, o.OID) {
				if err := l.manager.Store().UpsertLFSObject(ctx, repo.ID, o.OID, o.Size); err != nil {
					obj.Error = &lfsError{Code: 500, Message: "could not record object"}
				}
				continue
			}
			obj.Actions = map[string]*lfsAction{
				"upload": {Href: l.objectURL(ctx, repo, o.OID), Header: header, ExpiresIn: 3600},
				"verify": {Href: l.manager.HTTPCloneURL(ctx, repo, false) + "/info/lfs/verify", Header: header, ExpiresIn: 3600},
			}
			continue
		}
		if existing == nil {
			obj.Error = &lfsError{Code: 404, Message: "object not found"}
			continue
		}
		obj.Size = existing.Size
		obj.Actions = map[string]*lfsAction{"download": {Href: l.objectURL(ctx, repo, o.OID), Header: header, ExpiresIn: 3600}}
	}
	writeLFSJSON(w, http.StatusOK, resp)
}

// Whether a repository the caller may read already holds the object
func (l *LFS) readableElsewhere(ctx context.Context, user *auth.AuthenticatedUser, oid string) bool {
	ids, err := l.manager.Store().ListLFSLinkRepoIDs(ctx, oid, 25)
	if err != nil {
		return false
	}
	for _, id := range ids {
		other, err := l.manager.Store().GetGitRepositoryByID(ctx, id)
		if err != nil || other == nil {
			continue
		}
		if l.manager.Access().CanSee(ctx, user, other) {
			return true
		}
	}
	return false
}

// Streams an object from the shared blob store
func (l *LFS) ServeObject(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, oid string) {
	if !oidPattern.MatchString(oid) {
		lfsFail(w, http.StatusNotFound, "object not found")
		return
	}
	obj, err := l.manager.Store().GetLFSObject(r.Context(), repo.ID, oid)
	if err != nil || obj == nil {
		lfsFail(w, http.StatusNotFound, "object not found")
		return
	}
	f, info, err := l.manager.Blobs().OpenBlob("sha256:" + oid)
	if err != nil {
		lfsFail(w, http.StatusNotFound, "object bytes missing")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", oid))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// Receives an object, verifying the sha256 matches the oid
func (l *LFS) upload(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, oid string) {
	if !oidPattern.MatchString(oid) {
		lfsFail(w, http.StatusBadRequest, "invalid oid")
		return
	}
	ctx := r.Context()
	maxBytes := l.manager.gitSettings(ctx, repo).GetMaxLfsObjectMb() * 1024 * 1024
	blobs := l.manager.Blobs()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		lfsFail(w, http.StatusInternalServerError, "upload failed")
		return
	}
	var body io.Reader = r.Body
	if maxBytes > 0 {
		body = io.LimitReader(r.Body, maxBytes+1)
	}
	n, err := blobs.AppendChunk(uploadID, body)
	if err != nil {
		_ = blobs.CancelUpload(uploadID)
		lfsFail(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if maxBytes > 0 && n > maxBytes {
		_ = blobs.CancelUpload(uploadID)
		lfsFail(w, http.StatusRequestEntityTooLarge, "object exceeds the size limit")
		return
	}
	digest, size, _, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		lfsFail(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if digest != "sha256:"+oid {
		// Content did not match, drop the blob unless something else owns it
		l.manager.ReapBlob(ctx, digest)
		lfsFail(w, http.StatusBadRequest, "object content does not match its oid")
		return
	}
	if err := l.manager.Store().UpsertLFSObject(ctx, repo.ID, oid, size); err != nil {
		lfsFail(w, http.StatusInternalServerError, "could not record object")
		return
	}
	total, _ := l.manager.Store().LFSRepoSize(ctx, repo.ID)
	_ = l.manager.Store().UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"lfs_size_bytes": total})
	w.WriteHeader(http.StatusOK)
}

func (l *LFS) verify(w http.ResponseWriter, r *http.Request, repo *db.GitRepository) {
	var req lfsObjectRef
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		lfsFail(w, http.StatusBadRequest, "invalid verify request")
		return
	}
	obj, err := l.manager.Store().GetLFSObject(r.Context(), repo.ID, req.OID)
	if err != nil || obj == nil {
		lfsFail(w, http.StatusNotFound, "object not found")
		return
	}
	if obj.Size != req.Size {
		lfsFail(w, http.StatusUnprocessableEntity, "size mismatch")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (l *LFS) lockToJSON(ctx context.Context, lock *db.LFSLock) lfsLock {
	out := lfsLock{ID: lock.ID, Path: lock.Path, LockedAt: lock.CreatedAt}
	out.Owner.Name = lock.OwnerID
	if u, err := l.manager.Store().GetUserByID(ctx, lock.OwnerID); err == nil && u != nil {
		out.Owner.Name = u.Username
	}
	return out
}

func (l *LFS) createLock(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, user *auth.AuthenticatedUser) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Path == "" {
		lfsFail(w, http.StatusBadRequest, "path is required")
		return
	}
	ctx := r.Context()
	existing, next, err := l.manager.Store().ListLFSLocks(ctx, repo.ID, req.Path, "", 1, "")
	_ = next
	if err == nil && len(existing) > 0 {
		writeLFSJSON(w, http.StatusConflict, map[string]any{"lock": l.lockToJSON(ctx, existing[0]), "message": "already created lock"})
		return
	}
	lock := &db.LFSLock{RepoID: repo.ID, Path: req.Path, OwnerID: user.ID}
	if err := l.manager.Store().CreateLFSLock(ctx, lock); err != nil {
		lfsFail(w, http.StatusInternalServerError, "could not create lock")
		return
	}
	writeLFSJSON(w, http.StatusCreated, map[string]any{"lock": l.lockToJSON(ctx, lock)})
}

func (l *LFS) listLocks(w http.ResponseWriter, r *http.Request, repo *db.GitRepository) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	locks, next, err := l.manager.Store().ListLFSLocks(r.Context(), repo.ID, q.Get("path"), q.Get("id"), limit, q.Get("cursor"))
	if err != nil {
		lfsFail(w, http.StatusInternalServerError, "could not list locks")
		return
	}
	out := make([]lfsLock, 0, len(locks))
	for _, lk := range locks {
		out = append(out, l.lockToJSON(r.Context(), lk))
	}
	writeLFSJSON(w, http.StatusOK, map[string]any{"locks": out, "next_cursor": next})
}

func (l *LFS) verifyLocks(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, user *auth.AuthenticatedUser) {
	var req struct {
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	locks, next, err := l.manager.Store().ListLFSLocks(r.Context(), repo.ID, "", "", req.Limit, req.Cursor)
	if err != nil {
		lfsFail(w, http.StatusInternalServerError, "could not list locks")
		return
	}
	ours, theirs := []lfsLock{}, []lfsLock{}
	for _, lk := range locks {
		if lk.OwnerID == user.ID {
			ours = append(ours, l.lockToJSON(r.Context(), lk))
		} else {
			theirs = append(theirs, l.lockToJSON(r.Context(), lk))
		}
	}
	writeLFSJSON(w, http.StatusOK, map[string]any{"ours": ours, "theirs": theirs, "next_cursor": next})
}

func (l *LFS) unlock(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, user *auth.AuthenticatedUser, id string) {
	var req struct {
		Force bool `json:"force"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	ctx := r.Context()
	lock, err := l.manager.Store().GetLFSLock(ctx, repo.ID, id)
	if err != nil || lock == nil {
		lfsFail(w, http.StatusNotFound, "lock not found")
		return
	}
	if lock.OwnerID != user.ID {
		if !req.Force || !l.manager.Access().Can(ctx, user, repo, db.GitUnitCode, rbac.ActionUpdate) {
			lfsFail(w, http.StatusForbidden, "lock belongs to another user")
			return
		}
	}
	if err := l.manager.Store().DeleteLFSLock(ctx, repo.ID, id); err != nil {
		lfsFail(w, http.StatusInternalServerError, "could not delete lock")
		return
	}
	writeLFSJSON(w, http.StatusOK, map[string]any{"lock": l.lockToJSON(ctx, lock)})
}

// Mints the token json git-lfs-authenticate prints over ssh
func (l *LFS) AuthenticateJSON(ctx context.Context, repo *db.GitRepository, user *auth.AuthenticatedUser, operation string) ([]byte, error) {
	actions := []string{"pull"}
	if operation == "upload" {
		actions = append(actions, "push")
	}
	// Deploy keys carry their id, a username can never collide with the prefix
	subject := user.Username
	if user.IsDeployKey() {
		subject = user.ID
	}
	tok, err := l.tokens.SignTokenAudience(LFSAudience, time.Hour, subject, []*auth.ResourceActions{{Type: "git", Name: repo.Namespace + "/" + repo.Name, Actions: actions}})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"href":       l.manager.HTTPCloneURL(ctx, repo, false) + "/info/lfs",
		"header":     map[string]string{"Authorization": "Bearer " + tok},
		"expires_in": 3600,
	})
}

// Direct accessors used by the ssh lfs transfer protocol
func (l *LFS) Manager() *Manager { return l.manager }
