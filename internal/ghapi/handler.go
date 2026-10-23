package ghapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Package includes a GitHub REST compatible api for actions and tooling

// Path the handler is mounted under, GitHub Enterprise convention
const Prefix = "/api/v3"

const (
	defaultPerPage = 30
	maxPerPage     = 100
	maxBodyBytes   = 1 << 20
)

// Handler answers GitHub shaped requests over the forge
type Handler struct {
	store     *stores.Store
	auth      *auth.Manager
	git       *gitrepo.Manager
	forge     *forge.Manager
	artifacts *artifacts.Manager
	enforcer  *rbac.Enforcer
	log       *logger.Logger
	mux       *http.ServeMux
}

func New(store *stores.Store, authMgr *auth.Manager, git *gitrepo.Manager, fm *forge.Manager, am *artifacts.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *Handler {
	h := &Handler{store: store, auth: authMgr, git: git, forge: fm, artifacts: am, enforcer: enforcer, log: log, mux: http.NewServeMux()}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
	h.handle("GET /user", (*request).getUser)
	h.handle("GET /rate_limit", (*request).rateLimit)
	h.handle("GET /repos/{owner}/{repo}", (*request).getRepo)
	h.handle("GET /repos/{owner}/{repo}/branches/{branch...}", (*request).getBranch)
	h.handle("GET /repos/{owner}/{repo}/commits/{ref}", (*request).getCommit)
	h.handle("GET /repos/{owner}/{repo}/commits/{ref}/statuses", (*request).listStatuses)
	h.handle("GET /repos/{owner}/{repo}/commits/{ref}/status", (*request).combinedStatus)
	h.handle("POST /repos/{owner}/{repo}/statuses/{sha}", (*request).createStatus)
	h.handle("GET /repos/{owner}/{repo}/git/ref/{ref...}", (*request).getRef)
	h.handle("GET /repos/{owner}/{repo}/git/refs/{ref...}", (*request).getRef)
	h.handle("GET /repos/{owner}/{repo}/contents/{path...}", (*request).getContents)
	h.handle("GET /repos/{owner}/{repo}/contents", (*request).getContents)

	h.handle("GET /repos/{owner}/{repo}/releases", (*request).listReleases)
	h.handle("POST /repos/{owner}/{repo}/releases", (*request).createRelease)
	h.handle("GET /repos/{owner}/{repo}/releases/latest", (*request).latestRelease)
	h.handle("GET /repos/{owner}/{repo}/releases/{id}", (*request).getRelease)
	h.handle("PATCH /repos/{owner}/{repo}/releases/{id}", (*request).updateRelease)
	h.handle("DELETE /repos/{owner}/{repo}/releases/{id}", (*request).deleteRelease)
	// tags/{tag}, {id}/assets, and assets/{id} overlap for the mux
	h.handle("/repos/{owner}/{repo}/releases/{a}/{b}", (*request).releaseSub)

	h.handle("GET /repos/{owner}/{repo}/issues", (*request).listIssues)
	h.handle("POST /repos/{owner}/{repo}/issues", (*request).createIssue)
	h.handle("GET /repos/{owner}/{repo}/issues/{number}", (*request).getIssue)
	h.handle("PATCH /repos/{owner}/{repo}/issues/{number}", (*request).updateIssue)
	// comments/{id}, {number}/comments, and {number}/labels overlap the same way
	h.handle("/repos/{owner}/{repo}/issues/{a}/{b}", (*request).issueSub)
	h.handle("DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}", (*request).removeIssueLabel)
	h.handle("GET /repos/{owner}/{repo}/labels", (*request).listLabels)

	h.handle("GET /repos/{owner}/{repo}/pulls", (*request).listPulls)
	h.handle("POST /repos/{owner}/{repo}/pulls", (*request).createPull)
	h.handle("GET /repos/{owner}/{repo}/pulls/{number}", (*request).getPull)
	h.handle("GET /repos/{owner}/{repo}/pulls/{number}/comments", (*request).listComments)
	h.handle("POST /repos/{owner}/{repo}/pulls/{number}/comments", (*request).createComment)

	h.handle("GET /repos/{owner}/{repo}/actions/runs/{run_id}", (*request).getRun)
	h.handle("GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs", (*request).listRunJobs)

	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "Not Found")
	})
}

// One request with the resolved caller and a per request user cache
type request struct {
	h     *Handler
	w     http.ResponseWriter
	r     *http.Request
	ctx   context.Context
	user  *auth.AuthenticatedUser
	users map[string]*db.User
	// Path values a dispatcher assigns on top of the mux wildcards
	vals map[string]string
}

// Path value from a dispatcher or the mux pattern
func (q *request) param(name string) string {
	if v, ok := q.vals[name]; ok {
		return v
	}
	return q.r.PathValue(name)
}

func (q *request) with(name, value string) *request {
	if q.vals == nil {
		q.vals = map[string]string{}
	}
	q.vals[name] = value
	return q
}

// Splits the two segment release paths the mux cannot tell apart
func (q *request) releaseSub() {
	a, b := q.r.PathValue("a"), q.r.PathValue("b")
	method := q.r.Method
	switch {
	case a == "tags" && method == http.MethodGet:
		q.with("tag", b).releaseByTag()
	case a == "assets" && method == http.MethodGet:
		q.with("id", b).getAsset()
	case a == "assets" && method == http.MethodPatch:
		q.with("id", b).updateAsset()
	case a == "assets" && method == http.MethodDelete:
		q.with("id", b).deleteAsset()
	case b == "assets" && method == http.MethodGet:
		q.with("id", a).listAssets()
	case b == "assets" && method == http.MethodPost:
		q.with("id", a).uploadAsset()
	default:
		q.notFound()
	}
}

// Splits the two segment issue paths the mux cannot tell apart
func (q *request) issueSub() {
	a, b := q.r.PathValue("a"), q.r.PathValue("b")
	method := q.r.Method
	switch {
	case a == "comments" && method == http.MethodGet:
		q.with("id", b).getComment()
	case a == "comments" && method == http.MethodPatch:
		q.with("id", b).updateComment()
	case a == "comments" && method == http.MethodDelete:
		q.with("id", b).deleteComment()
	case b == "comments" && method == http.MethodGet:
		q.with("number", a).listComments()
	case b == "comments" && method == http.MethodPost:
		q.with("number", a).createComment()
	case b == "labels" && method == http.MethodGet:
		q.with("number", a).listIssueLabels()
	case b == "labels" && method == http.MethodPost:
		q.with("number", a).addIssueLabels()
	case b == "labels" && method == http.MethodPut:
		q.with("number", a).setIssueLabels()
	default:
		q.notFound()
	}
}

type handlerFunc func(q *request)

func (h *Handler) handle(pattern string, fn handlerFunc) {
	h.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		user, status, msg := h.resolveUser(r)
		if status != 0 {
			writeError(w, status, msg)
			return
		}
		ctx := auth.WithUser(r.Context(), user)
		fn(&request{h: h, w: w, r: r.WithContext(ctx), ctx: ctx, user: user, users: map[string]*db.User{}})
	})
}

// Caller from bearer, token, basic, or cookie credentials
func (h *Handler) resolveUser(r *http.Request) (*auth.AuthenticatedUser, int, string) {
	if !h.auth.IsAnyAuthEnabled() {
		return auth.NoAuthAdmin(), 0, ""
	}
	token := credential(r)
	if token != "" {
		user, err := h.auth.ValidateToken(r.Context(), token)
		if err != nil {
			return nil, http.StatusUnauthorized, "Bad credentials"
		}
		if user.MustChangePassword {
			return nil, http.StatusForbidden, "password change required before continuing"
		}
		return user, 0, ""
	}
	if portal.AnonymousAllowed(r.Context(), h.auth.IsAnonymousAccessEnabled()) {
		return h.auth.AnonymousUser(), 0, ""
	}
	return nil, http.StatusUnauthorized, "Requires authentication"
}

// Token from the Authorization header in its GitHub spellings
func credential(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header != "" {
		scheme, value, _ := strings.Cut(header, " ")
		value = strings.TrimSpace(value)
		switch strings.ToLower(scheme) {
		case "bearer", "token":
			return value
		case "basic":
			raw, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return ""
			}
			_, password, _ := strings.Cut(string(raw), ":")
			return strings.TrimSpace(password)
		}
	}
	return auth.ExtractToken(r.Header)
}

// -- Responses --

type errorBody struct {
	Message          string        `json:"message"`
	DocumentationURL string        `json:"documentation_url"`
	Errors           []errorDetail `json:"errors,omitempty"`
}

type errorDetail struct {
	Resource string `json:"resource,omitempty"`
	Field    string `json:"field,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-GitHub-Media-Type", "github.v3; format=json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Message: msg})
}

func (q *request) json(status int, v any) { writeJSON(q.w, status, v) }

func (q *request) fail(status int, format string, args ...any) {
	writeError(q.w, status, fmt.Sprintf(format, args...))
}

func (q *request) notFound() { q.fail(http.StatusNotFound, "Not Found") }

func (q *request) validation(resource, field, code, msg string) {
	writeJSON(q.w, http.StatusUnprocessableEntity, errorBody{Message: "Validation Failed", Errors: []errorDetail{{Resource: resource, Field: field, Code: code, Message: msg}}})
}

// Writes the GitHub status a forge or git error maps onto
func (q *request) failErr(err error) {
	status, msg := statusOf(err)
	if status == http.StatusInternalServerError {
		q.h.log.Error("ghapi: %s %s: %v", q.r.Method, q.r.URL.Path, err)
	}
	q.fail(status, "%s", msg)
}

func statusOf(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, forge.ErrNotFound), errors.Is(err, gitrepo.ErrRepoNotFound):
		return http.StatusNotFound, "Not Found"
	case errors.Is(err, forge.ErrForbidden), errors.Is(err, gitrepo.ErrArchived), errors.Is(err, gitrepo.ErrMirror),
		errors.Is(err, forge.ErrLocked), errors.Is(err, forge.ErrBlocked), errors.Is(err, forge.ErrDisabled):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, forge.ErrInvalid), errors.Is(err, gitrepo.ErrInvalid), errors.Is(err, gitrepo.ErrInvalidName),
		errors.Is(err, gitrepo.ErrPathConflict), errors.Is(err, gitrepo.ErrNotFastForward), errors.Is(err, artifacts.ErrInvalid):
		return http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, forge.ErrConflict), errors.Is(err, gitrepo.ErrRefChanged), errors.Is(err, gitrepo.ErrMergeConflict):
		return http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, gitrepo.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err.Error()
	}
	return http.StatusInternalServerError, "Internal error"
}

// Decodes a json body, answering 400 when it is malformed
func (q *request) decode(v any) bool {
	body, err := io.ReadAll(io.LimitReader(q.r.Body, maxBodyBytes))
	if err != nil {
		q.fail(http.StatusBadRequest, "Problems parsing JSON")
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		q.fail(http.StatusBadRequest, "Problems parsing JSON")
		return false
	}
	return true
}

// -- Access --

// Loads a repository the caller may see, 404 hides the rest
func (q *request) repo() (*db.GitRepository, bool) {
	owner, name := portal.ScopeRepoRef(q.ctx, q.r.PathValue("owner"), q.r.PathValue("repo"))
	if owner == "" || name == "" || portal.ForeignRef(q.ctx, owner) {
		q.notFound()
		return nil, false
	}
	repo, _, err := q.h.store.ResolveGitRepository(q.ctx, owner, name)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	if repo == nil || !q.h.git.Access().CanSee(q.ctx, q.user, repo) || !q.capability(rbac.ActionRead, "", true) {
		q.notFound()
		return nil, false
	}
	return repo, true
}

func (q *request) levels(repo *db.GitRepository) gitrepo.Levels {
	return q.h.git.Access().Levels(q.ctx, q.user, repo)
}

// Read on a unit, 410 for a disabled feature like GitHub, 404 otherwise
func (q *request) readable(repo *db.GitRepository, unit string) bool {
	lv := q.levels(repo)
	if !gitrepo.UnitEnabled(repo, unit) && !lv.IsOwner && !lv.CanAdmin {
		q.fail(http.StatusGone, "%s are disabled for this repo", unitLabel(unit))
		return false
	}
	if lv.Unit(unit) < gitrepo.LevelRead {
		q.notFound()
		return false
	}
	return true
}

// Write on a unit with the rbac capability the rpc layer demands
func (q *request) writable(repo *db.GitRepository, unit, action string) bool {
	if !q.readable(repo, unit) {
		return false
	}
	if q.user == nil || q.user.IsAnonymous() {
		q.fail(http.StatusUnauthorized, "Requires authentication")
		return false
	}
	if p := portal.FromContext(q.ctx); p != nil && !p.AllowPush {
		q.fail(http.StatusForbidden, "portal is read-only")
		return false
	}
	if q.levels(repo).Unit(unit) < gitrepo.LevelWrite {
		q.fail(http.StatusForbidden, "Resource not accessible by integration")
		return false
	}
	if repo.IsArchived {
		q.fail(http.StatusForbidden, "Repository was archived so is read-only.")
		return false
	}
	objectID := repo.Namespace + "/" + repo.Name
	if action == rbac.ActionRead {
		objectID = ""
	}
	if !q.capability(action, objectID, action == rbac.ActionRead) {
		q.fail(http.StatusForbidden, "Resource not accessible by integration")
		return false
	}
	return true
}

// Mirrors the rpc interceptor's rbac check for the resource
func (q *request) capability(action, objectID string, anyGrant bool) bool {
	if q.h.enforcer == nil || q.user == nil {
		return true
	}
	if anyGrant {
		return q.h.enforcer.HasPermission(q.user.Roles, rbac.ResourceGit, action)
	}
	ok, err := q.h.enforcer.Enforce(q.user.Roles, rbac.ResourceGit, action, objectID)
	return err == nil && ok
}

func (q *request) actor(repo *db.GitRepository, unit string) forge.Actor {
	lv := q.levels(repo)
	return forge.Actor{User: q.user, CanWrite: lv.Unit(unit) >= gitrepo.LevelWrite, CanAdmin: lv.CanAdmin || lv.IsOwner}
}

func unitLabel(unit string) string {
	switch unit {
	case db.GitUnitIssues:
		return "Issues"
	case db.GitUnitPulls:
		return "Pull requests"
	case db.GitUnitReleases:
		return "Releases"
	case db.GitUnitActions:
		return "Actions"
	case db.GitUnitAgents:
		return "Agents"
	}
	return "Features"
}

// -- Pagination --

type pageParams struct {
	page    int
	perPage int
}

func (p pageParams) offset() int { return (p.page - 1) * p.perPage }

func parsePage(r *http.Request) pageParams {
	p := pageParams{page: 1, perPage: defaultPerPage}
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		p.page = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		p.perPage = min(n, maxPerPage)
	}
	return p
}

// Link header with next, prev, first, and last relations
func linkHeader(base *url.URL, p pageParams, total int64) string {
	last := int((total + int64(p.perPage) - 1) / int64(p.perPage))
	if last < 1 {
		last = 1
	}
	link := func(page int, rel string) string {
		u := *base
		qv := u.Query()
		qv.Set("page", strconv.Itoa(page))
		qv.Set("per_page", strconv.Itoa(p.perPage))
		u.RawQuery = qv.Encode()
		return fmt.Sprintf("<%s>; rel=\"%s\"", u.String(), rel)
	}
	var parts []string
	if p.page < last {
		parts = append(parts, link(p.page+1, "next"), link(last, "last"))
	}
	if p.page > 1 {
		parts = append(parts, link(1, "first"), link(min(p.page-1, last), "prev"))
	}
	return strings.Join(parts, ", ")
}

func (q *request) paginate(p pageParams, total int64) {
	u := *q.r.URL
	u.Scheme, u.Host = "", ""
	abs, err := url.Parse(q.apiBase() + u.RequestURI())
	if err != nil {
		return
	}
	if l := linkHeader(abs, p, total); l != "" {
		q.w.Header().Set("Link", l)
	}
}

// Slices an in memory list the way an sql page would
func pageSlice[T any](items []T, p pageParams) []T {
	start := min(p.offset(), len(items))
	end := min(start+p.perPage, len(items))
	return items[start:end]
}

// -- Identity helpers --

// Stable integer id derived from a row id, within javascript's safe range
func numID(s string) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
		return n
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64() & (1<<53 - 1))
}

// Matches a path id against a row id or its derived integer
func idMatches(pathID, rowID string) bool {
	if pathID == rowID {
		return true
	}
	n, err := strconv.ParseInt(pathID, 10, 64)
	return err == nil && n == numID(rowID)
}

func (q *request) base() string    { return q.h.git.BaseURL(q.ctx) }
func (q *request) apiBase() string { return q.base() + Prefix }

func (q *request) repoAPI(repo *db.GitRepository) string {
	return q.apiBase() + "/repos/" + repo.Namespace + "/" + repo.Name
}

func (q *request) repoHTML(repo *db.GitRepository) string {
	return q.h.git.HTMLURL(q.ctx, repo)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func (q *request) dbUser(id string) *db.User {
	if id == "" {
		return nil
	}
	if u, ok := q.users[id]; ok {
		return u
	}
	u, _ := q.h.store.GetUserByID(q.ctx, id)
	q.users[id] = u
	return u
}

func (q *request) dbUserByName(name string) *db.User {
	for _, u := range q.users {
		if u != nil && u.Username == name {
			return u
		}
	}
	u, _ := q.h.store.GetUserByUsername(q.ctx, name)
	if u != nil {
		q.users[u.ID] = u
	}
	return u
}

func (q *request) query(name string) string {
	return strings.TrimSpace(q.r.URL.Query().Get(name))
}

func (q *request) pathInt(name string) (int64, bool) {
	n, err := strconv.ParseInt(q.param(name), 10, 64)
	if err != nil || n <= 0 {
		q.notFound()
		return 0, false
	}
	return n, true
}
