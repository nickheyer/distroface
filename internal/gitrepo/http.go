package gitrepo

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/utils"
	"golang.org/x/crypto/bcrypt"
)

// Project path plus a smart http, lfs, or browse suffix
var gitPath = regexp.MustCompile(`^/([^/]+)/([^/]+?)(\.wiki)?(\.git)?/(info/refs|git-upload-pack|git-receive-pack|git-upload-archive|HEAD|objects/.+|info/lfs(?:/.*)?|-/.*)$`)

// Audience of lfs bearer tokens
const LFSAudience = "distroface-git"

// Chunk size for streamed pack responses
const flushChunk = 4096

// HTTP serves smart http, lfs, raw, and archive routes ahead of the spa
type HTTP struct {
	manager     *Manager
	authMgr     *auth.Manager
	tokens      *auth.TokenService
	authLimiter *admin.Limiter
	pullLimiter *admin.Limiter
	anonLimiter *admin.Limiter
	lfs         *LFS
	log         *logger.Logger
}

func NewHTTP(manager *Manager, authMgr *auth.Manager, tokens *auth.TokenService, authLimiter, pullLimiter, anonLimiter *admin.Limiter, log *logger.Logger) *HTTP {
	h := &HTTP{manager: manager, authMgr: authMgr, tokens: tokens, authLimiter: authLimiter, pullLimiter: pullLimiter, anonLimiter: anonLimiter, log: log}
	h.lfs = NewLFS(manager, tokens, log)
	return h
}

// Wraps the spa handler so git suffixes are served first
func (h *HTTP) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := gitPath.FindStringSubmatch(r.URL.Path)
		if m == nil || !h.manager.Enabled(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		h.serve(w, r, m[1], m[2], m[3] != "", m[5])
	})
}

// Resolved request identity and access
type gitRequest struct {
	repo   *db.GitRepository
	user   *auth.AuthenticatedUser
	wiki   bool
	suffix string
}

func (h *HTTP) serve(w http.ResponseWriter, r *http.Request, namespace, name string, wiki bool, suffix string) {
	ctx := r.Context()
	store := h.manager.Store()
	repo, moved, err := store.ResolveGitRepository(ctx, namespace, name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	user, hasCreds, ok := h.identify(w, r, repo, suffix)
	if !ok {
		return
	}
	if repo == nil {
		h.notFound(w, r, hasCreds)
		return
	}
	if portal.ForeignRef(ctx, repo.Namespace) {
		h.notFound(w, r, hasCreds)
		return
	}
	req := &gitRequest{repo: repo, user: user, wiki: wiki, suffix: suffix}
	unit := db.GitUnitCode
	if wiki {
		unit = db.GitUnitWiki
	}
	push := h.isPushIntent(r, suffix)
	action := rbac.ActionPull
	if push {
		action = rbac.ActionPush
	}
	if !h.manager.Access().Can(ctx, user, repo, unit, action) {
		if !h.manager.Access().CanSee(ctx, user, repo) {
			h.notFound(w, r, hasCreds)
			return
		}
		if !hasCreds && (user == nil || user.IsAnonymous()) {
			h.challenge(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Old names redirect only once the caller is allowed in, so moves never leak
	if moved && strings.HasPrefix(suffix, "info/refs") && r.Method == http.MethodGet {
		target := h.manager.HTTPCloneURL(ctx, repo, wiki) + "/" + suffix
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	if push {
		if p := portal.FromContext(ctx); p != nil && !p.AllowPush {
			http.Error(w, "portal is read-only", http.StatusForbidden)
			return
		}
		if repo.IsArchived {
			http.Error(w, "repository is archived", http.StatusForbidden)
			return
		}
	}

	switch {
	case suffix == "info/refs":
		h.infoRefs(w, r, req)
	case suffix == "git-upload-pack", suffix == "git-receive-pack", suffix == "git-upload-archive":
		h.serviceRPC(w, r, req, "git-"+strings.TrimPrefix(suffix, "git-"))
	case suffix == "HEAD", strings.HasPrefix(suffix, "objects/"):
		http.Error(w, "dumb http protocol is not supported", http.StatusForbidden)
	case strings.HasPrefix(suffix, "info/lfs"):
		if wiki {
			http.NotFound(w, r)
			return
		}
		h.lfs.ServeHTTP(w, r, repo, user, strings.TrimPrefix(strings.TrimPrefix(suffix, "info/lfs"), "/"))
	case strings.HasPrefix(suffix, "-/raw/"):
		h.raw(w, r, req, strings.TrimPrefix(suffix, "-/raw/"))
	case strings.HasPrefix(suffix, "-/archive/"):
		h.archive(w, r, req, strings.TrimPrefix(suffix, "-/archive/"))
	case strings.HasPrefix(suffix, "-/lfs/"):
		if wiki {
			http.NotFound(w, r)
			return
		}
		h.lfs.ServeObject(w, r, repo, strings.TrimPrefix(suffix, "-/lfs/"))
	case strings.HasPrefix(suffix, "-/releases/"):
		h.releaseAsset(w, r, req, strings.TrimPrefix(suffix, "-/releases/"))
	case strings.HasPrefix(suffix, "-/actions/artifacts/"):
		h.actionsArtifact(w, r, req, strings.TrimPrefix(suffix, "-/actions/artifacts/"))
	default:
		http.NotFound(w, r)
	}
}

// Requests that write
func (h *HTTP) isPushIntent(r *http.Request, suffix string) bool {
	switch {
	case suffix == "git-receive-pack":
		return true
	case suffix == "info/refs":
		return r.URL.Query().Get("service") == transport.ReceivePackService
	case strings.HasPrefix(suffix, "info/lfs"):
		return h.lfs.IsWrite(r, strings.TrimPrefix(strings.TrimPrefix(suffix, "info/lfs"), "/"))
	}
	return false
}

// Resolves basic, bearer token, or lfs jwt credentials
func (h *HTTP) identify(w http.ResponseWriter, r *http.Request, repo *db.GitRepository, suffix string) (*auth.AuthenticatedUser, bool, bool) {
	ctx := r.Context()
	clientIP := admin.ClientIP(r.RemoteAddr, r.Header)
	if !h.authMgr.IsAnyAuthEnabled() {
		return auth.NoAuthAdmin(), true, true
	}
	header := r.Header.Get("Authorization")
	if header == "" {
		if portal.AnonymousAllowed(ctx, h.authMgr.IsAnonymousAccessEnabled()) {
			return h.authMgr.AnonymousUser(), false, true
		}
		h.challenge(w, r)
		return nil, false, false
	}
	if h.authLimiter != nil && h.authLimiter.Blocked(clientIP) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many failed authentication attempts", http.StatusTooManyRequests)
		return nil, true, false
	}
	deny := func() (*auth.AuthenticatedUser, bool, bool) {
		if h.authLimiter != nil {
			h.authLimiter.Record(clientIP)
		}
		h.manager.Audit(ctx, audit.Event{Action: "Git/auth", Resource: "auth", Outcome: audit.OutcomeDenied, SourceIP: clientIP, Detail: r.URL.Path})
		h.challenge(w, r)
		return nil, true, false
	}
	scheme, value, _ := strings.Cut(header, " ")
	value = strings.TrimSpace(value)
	switch strings.ToLower(scheme) {
	case "basic":
		username, password, ok := r.BasicAuth()
		if !ok {
			return deny()
		}
		if strings.HasPrefix(password, "df_") {
			user, err := h.authMgr.ValidateAPIToken(ctx, password)
			if err != nil {
				return deny()
			}
			h.resetLimiter(clientIP)
			return user, true, true
		}
		u, err := h.manager.Store().GetUserByIdentifier(ctx, username)
		if err != nil || u == nil || u.PasswordHash == "" || !u.IsActive || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
			return deny()
		}
		roles, _ := h.manager.Store().GetUserRoleNames(ctx, u.ID)
		user := &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: roles, Provider: u.AuthProvider}
		if u.Email != nil {
			user.Email = *u.Email
		}
		h.resetLimiter(clientIP)
		return user, true, true
	case "bearer":
		if strings.HasPrefix(value, "df_") {
			user, err := h.authMgr.ValidateAPIToken(ctx, value)
			if err != nil {
				return deny()
			}
			h.resetLimiter(clientIP)
			return user, true, true
		}
		// Session jwt from the spa or an lfs token minted over ssh
		if user, err := h.authMgr.ValidateSession(ctx, value); err == nil {
			return user, true, true
		}
		if h.tokens != nil && strings.HasPrefix(suffix, "info/lfs") && repo != nil {
			subject, access, err := h.tokens.VerifyAccess(value, LFSAudience)
			if err == nil {
				if user := h.lfsPrincipal(ctx, subject, access, repo); user != nil {
					return user, true, true
				}
			}
		}
		return deny()
	}
	return deny()
}

// Principal from an lfs jwt scoped to one repository
func (h *HTTP) lfsPrincipal(ctx context.Context, subject string, access []*auth.ResourceActions, repo *db.GitRepository) *auth.AuthenticatedUser {
	full := repo.Namespace + "/" + repo.Name
	for _, a := range access {
		if a.Type != "git" || a.Name != full {
			continue
		}
		readOnly := true
		for _, act := range a.Actions {
			if act == "push" {
				readOnly = false
			}
		}
		if id, ok := strings.CutPrefix(subject, "deploykey:"); ok {
			row, err := h.manager.Store().GetSSHKey(ctx, id)
			if err != nil || row == nil || row.OwnerType != db.SSHKeyOwnerGitRepo || row.OwnerID != repo.ID || (row.ExpiresAt != nil && row.ExpiresAt.Before(time.Now())) {
				return nil
			}
			return &auth.AuthenticatedUser{ID: "deploykey:" + row.ID, Username: "deploy-key", Roles: []string{"user"}, Provider: "deploykey",
				Scope: &auth.CredentialScope{Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO, ID: full, ReadOnly: readOnly || row.ReadOnly}}
		}
		u, err := h.manager.Store().GetUserByUsername(ctx, subject)
		if err != nil || u == nil || !u.IsActive {
			return nil
		}
		roles, _ := h.manager.Store().GetUserRoleNames(ctx, u.ID)
		return &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: roles, Provider: u.AuthProvider,
			Scope: &auth.CredentialScope{Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO, ID: full, ReadOnly: readOnly}}
	}
	return nil
}

func (h *HTTP) resetLimiter(ip string) {
	if h.authLimiter != nil {
		h.authLimiter.Reset(ip)
	}
}

func (h *HTTP) challenge(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("WWW-Authenticate", `Basic realm="distroface"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// Existence never leaks to the unauthenticated
func (h *HTTP) notFound(w http.ResponseWriter, r *http.Request, hasCreds bool) {
	if !hasCreds {
		h.challenge(w, r)
		return
	}
	http.Error(w, "repository not found", http.StatusNotFound)
}

// Transfers wait out a rename with a short retry instead of hitting a half moved directory
func (h *HTTP) moving(w http.ResponseWriter, req *gitRequest) bool {
	if !h.manager.Engine().Renaming(req.repo.ID) {
		return false
	}
	w.Header().Set("Retry-After", "2")
	http.Error(w, "repository is moving, retry shortly", http.StatusServiceUnavailable)
	return true
}

func (h *HTTP) acquire(w http.ResponseWriter, r *http.Request) (func(), bool) {
	release, err := h.manager.Engine().Transfers().Acquire(r.Context(), 10*time.Second)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return nil, false
	}
	return release, true
}

func noCache(w http.ResponseWriter) {
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
}

// Smart advertisement for upload-pack or receive-pack
func (h *HTTP) infoRefs(w http.ResponseWriter, r *http.Request, req *gitRequest) {
	service := r.URL.Query().Get("service")
	if service != transport.UploadPackService && service != transport.ReceivePackService {
		http.Error(w, "dumb http protocol is not supported", http.StatusForbidden)
		return
	}
	if h.moving(w, req) {
		return
	}
	release, ok := h.acquire(w, r)
	if !ok {
		return
	}
	defer release()
	st, closer, err := h.manager.Engine().Storer(req.repo.Namespace, req.repo.Name, req.wiki)
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusInternalServerError)
		return
	}
	defer closer()
	noCache(w)
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-advertisement", transport.ServiceName(service)))
	frw := &flushWriter{ResponseWriter: w, rc: http.NewResponseController(w), idle: transferIdleTimeout}
	version := r.Header.Get("Git-Protocol")
	var serveErr error
	if service == transport.UploadPackService {
		serveErr = transport.UploadPack(r.Context(), st, nil, frw, &transport.UploadPackRequest{GitProtocol: version, AdvertiseRefs: true, StatelessRPC: true})
	} else {
		serveErr = transport.ReceivePack(r.Context(), st, nil, frw, &transport.ReceivePackRequest{GitProtocol: version, AdvertiseRefs: true, StatelessRPC: true})
	}
	if serveErr != nil && !frw.started.Load() {
		h.log.Error("git advertisement %s/%s: %v", req.repo.Namespace, req.repo.Name, serveErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// Stateless pack exchange
func (h *HTTP) serviceRPC(w http.ResponseWriter, r *http.Request, req *gitRequest, service string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	expected := fmt.Sprintf("application/x-git-%s-request", transport.ServiceName(service))
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Type")), expected) {
		http.Error(w, "unexpected content type", http.StatusForbidden)
		return
	}
	if h.moving(w, req) {
		return
	}
	release, ok := h.acquire(w, r)
	if !ok {
		return
	}
	defer release()
	ctx := r.Context()
	clientIP := admin.ClientIP(r.RemoteAddr, r.Header)
	if service == transport.UploadPackService && !h.rateLimitPull(w, req.user, clientIP) {
		return
	}
	rc := http.NewResponseController(w)
	var body io.ReadCloser = &deadlineReader{ReadCloser: r.Body, rc: rc, idle: transferIdleTimeout}
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad gzip body", http.StatusBadRequest)
			return
		}
		defer gz.Close()
		body = gz
	}
	// Writers lock before opening so a rename in flight cannot leave a ghost directory
	repo := req.repo
	var lock *RepoLock
	if service == transport.ReceivePackService {
		lock = h.manager.Engine().LockRepo(repo.ID)
		defer lock.Unlock()
		current, err := h.manager.Store().GetGitRepositoryByID(ctx, repo.ID)
		if err != nil || current == nil {
			http.Error(w, "repository not found", http.StatusNotFound)
			return
		}
		repo = current
		if budget, over := h.manager.pushBudget(ctx, repo, req.wiki); budget > 0 {
			if r.ContentLength > budget {
				http.Error(w, over.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			body = &budgetReader{ReadCloser: body, left: budget, err: over}
		}
	}
	st, closer, err := h.manager.Engine().Storer(repo.Namespace, repo.Name, req.wiki)
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusInternalServerError)
		return
	}
	defer closer()
	w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-result", transport.ServiceName(service)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	noCache(w)
	frw := &flushWriter{ResponseWriter: w, rc: rc, idle: transferIdleTimeout}
	version := r.Header.Get("Git-Protocol")
	var serveErr error
	switch service {
	case transport.UploadPackService:
		serveErr = transport.UploadPack(ctx, st, body, frw, &transport.UploadPackRequest{GitProtocol: version, StatelessRPC: true})
		if serveErr == nil {
			_ = h.manager.Store().IncrementGitPullCount(ctx, repo.ID)
			h.manager.Audit(ctx, audit.Event{Action: "Git/fetch", Resource: "git", Outcome: audit.OutcomeSuccess, Detail: repo.Namespace + "/" + repo.Name, SourceIP: clientIP, Actor: req.user.Username, ActorID: req.user.ID})
		}
	case transport.ReceivePackService:
		serveErr = transport.ReceivePack(ctx, st, body, frw, &transport.ReceivePackRequest{
			GitProtocol: version, StatelessRPC: true,
			Hooks: h.manager.Hooks(repo, req.user, HookOptions{Wiki: req.wiki}, lock),
		})
		lock.Unlock()
	case transport.UploadArchiveService:
		serveErr = transport.UploadArchive(ctx, st, body, frw, &transport.UploadArchiveRequest{})
	}
	if serveErr != nil {
		if !frw.started.Load() {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		if ctx.Err() == nil && !errors.Is(serveErr, io.EOF) {
			h.log.Warn("git %s %s/%s: %v", service, repo.Namespace, repo.Name, serveErr)
		}
	}
}

// Pull limits per user or per client ip for anonymous
func (h *HTTP) rateLimitPull(w http.ResponseWriter, user *auth.AuthenticatedUser, clientIP string) bool {
	limiter, key := h.anonLimiter, "ip:"+clientIP
	if user != nil && !user.IsAnonymous() {
		limiter, key = h.pullLimiter, "user:"+user.ID
	}
	if limiter == nil || limiter.Limit() <= 0 {
		return true
	}
	allowed, remaining, resetAt := limiter.Take(key)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limiter.Limit()))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
	if !allowed {
		retry := int(time.Until(resetAt).Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(max(retry, 1)))
		http.Error(w, "pull rate limit exceeded", http.StatusTooManyRequests)
		return false
	}
	return true
}

// Streams a blob with a sniffed content type
func (h *HTTP) raw(w http.ResponseWriter, r *http.Request, req *gitRequest, rest string) {
	ref, p, ok := strings.Cut(rest, "/")
	if !ok || p == "" {
		http.NotFound(w, r)
		return
	}
	repo, err := h.manager.Engine().OpenRepo(req.repo.Namespace, req.repo.Name, req.wiki)
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusInternalServerError)
		return
	}
	defer repo.Close()
	commit, err := repo.Resolve(ref)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, rd, err := repo.OpenBlob(commit, p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rd.Close()
	etag := `"` + file.Hash.String() + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	head := make([]byte, 1024)
	n, _ := io.ReadFull(rd, head)
	head = head[:n]
	if r.URL.Query().Get("lfs") == "1" && !req.wiki {
		if ptr := ParseLFSPointer(head); ptr != nil {
			h.lfs.ServeObject(w, r, req.repo, ptr.OID)
			return
		}
	}
	ct := utils.HardenUserContent(w.Header(), sniffContentType(p, head))
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	if strings.HasPrefix(ct, "text/") || strings.HasPrefix(ct, "image/") || ct == "application/json" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Name))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(head); err != nil {
		return
	}
	_, _ = io.Copy(w, rd)
}

// Streams a zip or tar.gz archive of a ref
func (h *HTTP) archive(w http.ResponseWriter, r *http.Request, req *gitRequest, file string) {
	var ref, format string
	switch {
	case strings.HasSuffix(file, ".zip"):
		ref, format = strings.TrimSuffix(file, ".zip"), "zip"
	case strings.HasSuffix(file, ".tar.gz"):
		ref, format = strings.TrimSuffix(file, ".tar.gz"), "tar.gz"
	case strings.HasSuffix(file, ".tar"):
		ref, format = strings.TrimSuffix(file, ".tar"), "tar"
	default:
		http.NotFound(w, r)
		return
	}
	release, ok := h.acquire(w, r)
	if !ok {
		return
	}
	defer release()
	repo, err := h.manager.Engine().OpenRepo(req.repo.Namespace, req.repo.Name, req.wiki)
	if err != nil {
		http.Error(w, "repository unavailable", http.StatusInternalServerError)
		return
	}
	defer repo.Close()
	commit, err := repo.Resolve(ref)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	prefix := fmt.Sprintf("%s-%s/", req.repo.Name, strings.ReplaceAll(ref, "/", "-"))
	rc, err := repo.Repo.ArchiveContext(r.Context(), &git.ArchiveOptions{Format: format, Prefix: prefix, Treeish: commit.String()})
	if err != nil {
		h.log.Error("archive %s/%s %s: %v", req.repo.Namespace, req.repo.Name, ref, err)
		http.Error(w, "archive failed", http.StatusInternalServerError)
		return
	}
	defer rc.Close()
	ct := "application/zip"
	if format != "zip" {
		ct = "application/gzip"
		if format == "tar" {
			ct = "application/x-tar"
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("%s-%s.%s", req.repo.Name, strings.ReplaceAll(ref, "/", "-"), format)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// Redirects a release asset link to the artifact download
func (h *HTTP) releaseAsset(w http.ResponseWriter, r *http.Request, req *gitRequest, rest string) {
	tag, asset, ok := strings.Cut(rest, "/")
	if !ok || asset == "" {
		http.NotFound(w, r)
		return
	}
	art, err := h.manager.Store().GetArtifactRepository(r.Context(), req.repo.Namespace, req.repo.Name)
	if err != nil || art == nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/%s/%s", req.repo.Namespace, req.repo.Name, tag, asset), http.StatusFound)
}

// Redirects a workflow artifact link to the artifact download
func (h *HTTP) actionsArtifact(w http.ResponseWriter, r *http.Request, req *gitRequest, id string) {
	art, err := h.manager.Store().GetArtifact(r.Context(), id)
	if err != nil || art == nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/%s/%s", req.repo.Namespace, req.repo.Name, art.Version, art.Path), http.StatusFound)
}

// Flushes after every write so pack data streams, records when the status committed
type flushWriter struct {
	http.ResponseWriter
	started atomic.Bool
	// Write deadline pushed out before every write, zero idle leaves it alone
	rc   *http.ResponseController
	idle time.Duration
}

func (f *flushWriter) WriteHeader(code int) {
	f.started.Store(true)
	f.ResponseWriter.WriteHeader(code)
}

func (f *flushWriter) Write(p []byte) (int, error) {
	f.started.Store(true)
	if f.rc != nil && f.idle > 0 {
		_ = f.rc.SetWriteDeadline(time.Now().Add(f.idle))
	}
	n, err := f.ResponseWriter.Write(p)
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

// ReadFrom copies in chunks so each one reaches the client promptly
func (f *flushWriter) ReadFrom(r io.Reader) (int64, error) {
	buf := make([]byte, flushChunk)
	var n int64
	for {
		nr, rerr := r.Read(buf)
		if nr > 0 {
			nw, werr := f.Write(buf[:nr])
			n += int64(nw)
			if werr != nil {
				return n, werr
			}
			if nw != nr {
				return n, io.ErrShortWrite
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return n, nil
			}
			return n, rerr
		}
	}
}

func (f *flushWriter) Close() error { return nil }
