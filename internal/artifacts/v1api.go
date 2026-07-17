package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Drop in v1 rest facade for old dfcli and ci
type V1API struct {
	store    *stores.Store
	manager  *Manager
	authMgr  *auth.Manager
	enforcer *rbac.Enforcer
	access   *Access
	limiter  *admin.Limiter // Failed login lockout, nil disables
	log      *logger.Logger
	routes   []v1Route
}

var v1RepoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func NewV1API(store *stores.Store, manager *Manager, authMgr *auth.Manager, enforcer *rbac.Enforcer, limiter *admin.Limiter, log *logger.Logger) *V1API {
	a := &V1API{
		store:    store,
		manager:  manager,
		authMgr:  authMgr,
		enforcer: enforcer,
		access:   NewAccess(store, enforcer),
		limiter:  limiter,
		log:      log,
	}
	a.buildRoutes()
	return a
}

// Mounts login and refresh, never namespace rewritten
func (a *V1API) RegisterAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", a.handleRefresh)
}

// Mounts the artifact data plane
func (a *V1API) RegisterArtifacts(mux *http.ServeMux) {
	mux.Handle("/api/v1/artifacts", a)
	mux.Handle("/api/v1/artifacts/", a)
}

// ── Ordered router ───────────────────────────────────────────────────────

type v1Route struct {
	method  string
	pattern *regexp.Regexp
	vars    []string
	handler func(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string)
}

// V1 registration order is load bearing, keep it
func (a *V1API) buildRoutes() {
	add := func(method, pattern string, vars []string, h func(http.ResponseWriter, *http.Request, *auth.AuthenticatedUser, map[string]string)) {
		a.routes = append(a.routes, v1Route{method: method, pattern: regexp.MustCompile(pattern), vars: vars, handler: h})
	}

	add(http.MethodPost, `^/api/v1/artifacts/repos$`, nil, a.handleCreateRepo)
	add(http.MethodGet, `^/api/v1/artifacts/repos$`, nil, a.handleListRepos)
	add(http.MethodDelete, `^/api/v1/artifacts/repos/([^/]+)$`, []string{"repo"}, a.handleDeleteRepo)
	add(http.MethodPost, `^/api/v1/artifacts/([^/]+)/upload$`, []string{"repo"}, a.handleInitiateUpload)
	add(http.MethodPatch, `^/api/v1/artifacts/([^/]+)/upload/([^/]+)$`, []string{"repo", "uuid"}, a.handleUploadChunk)
	add(http.MethodPut, `^/api/v1/artifacts/([^/]+)/upload/([^/]+)$`, []string{"repo", "uuid"}, a.handleCompleteUpload)
	add(http.MethodGet, `^/api/v1/artifacts/([^/]+)/([^/]+)/(.*)$`, []string{"repo", "version", "path"}, a.handleDownload)
	add(http.MethodGet, `^/api/v1/artifacts/([^/]+)/query$`, []string{"repo"}, a.handleQuery)
	add(http.MethodDelete, `^/api/v1/artifacts/([^/]+)/([^/]+)/(.*)$`, []string{"repo", "version", "path"}, a.handleDeleteArtifact)
	add(http.MethodGet, `^/api/v1/artifacts/([^/]+)/versions$`, []string{"repo"}, a.handleListVersions)
	add(http.MethodPut, `^/api/v1/artifacts/([^/]+)/([^/]+)/metadata$`, []string{"repo", "id"}, a.handleUpdateMetadata)
	add(http.MethodPut, `^/api/v1/artifacts/([^/]+)/([^/]+)/properties$`, []string{"repo", "id"}, a.handleUpdateProperties)
	add(http.MethodGet, `^/api/v1/artifacts/search$`, nil, a.handleSearch)
	add(http.MethodPut, `^/api/v1/artifacts/([^/]+)/([^/]+)/rename$`, []string{"repo", "id"}, a.handleRename)
}

func (a *V1API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Portal injects the org namespace as a reserved marker segment
	forcedNS := ""
	if rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/artifacts/_ns/"); ok {
		if i := strings.IndexByte(rest, '/'); i > 0 {
			forcedNS = rest[:i]
			r.URL.Path = "/api/v1/artifacts/" + rest[i+1:]
		}
	}

	pathMatched := false
	for _, route := range a.routes {
		m := route.pattern.FindStringSubmatch(r.URL.Path)
		if m == nil {
			continue
		}
		pathMatched = true
		if r.Method != route.method {
			continue
		}

		user, ok := a.resolveUser(w, r)
		if !ok {
			return
		}

		vars := make(map[string]string, len(route.vars)+1)
		for i, name := range route.vars {
			vars[name] = m[i+1]
		}
		if forcedNS != "" {
			vars["namespace"] = forcedNS
		}
		route.handler(w, r, user, vars)
		return
	}
	if pathMatched {
		http.Error(w, "METHOD NOT ALLOWED", http.StatusMethodNotAllowed)
		return
	}
	http.NotFound(w, r)
}

// ── Auth ─────────────────────────────────────────────────────────────────

// V1 middleware semantics on the v2 auth stack
func (a *V1API) resolveUser(w http.ResponseWriter, r *http.Request) (*auth.AuthenticatedUser, bool) {
	if !a.authMgr.IsAnyAuthEnabled() {
		return &auth.AuthenticatedUser{ID: "admin", Username: "admin", Roles: []string{"admin"}, Provider: "none"}, true
	}

	token := auth.ExtractToken(r.Header)
	if token == "" {
		if a.authMgr.IsAnonymousAccessEnabled() {
			return a.authMgr.AnonymousUser(), true
		}
		http.Error(w, "INVALID TOKEN", http.StatusUnauthorized)
		return nil, false
	}

	user, err := a.authMgr.ValidateToken(r.Context(), token)
	if err != nil {
		http.Error(w, "INVALID TOKEN", http.StatusUnauthorized)
		return nil, false
	}
	return user, true
}

type v1AuthResponse struct {
	Token     string    `json:"token,omitempty"`
	ExpiresIn int       `json:"expires_in,omitempty"`
	IssuedAt  time.Time `json:"issued_at,omitempty"`
	Username  string    `json:"username,omitempty"`
	Groups    []string  `json:"groups,omitempty"`
}

func (a *V1API) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := admin.ClientIP(r.RemoteAddr, r.Header)
	if a.limiter != nil && a.limiter.Blocked(clientIP) {
		http.Error(w, "TOO MANY REQUESTS", http.StatusTooManyRequests)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	user, roles, token, expiresAt, err := a.authMgr.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if a.limiter != nil {
			a.limiter.Record(clientIP)
		}
		http.Error(w, "INVALID CREDENTIALS", http.StatusUnauthorized)
		return
	}
	if a.limiter != nil {
		a.limiter.Reset(clientIP)
	}

	writeJSON(w, http.StatusOK, v1AuthResponse{
		Token:     token,
		ExpiresIn: int(time.Until(expiresAt).Seconds()),
		IssuedAt:  time.Now().UTC(),
		Username:  user.Username,
		Groups:    roles,
	})
}

func (a *V1API) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	user, err := a.authMgr.ValidateToken(r.Context(), req.RefreshToken)
	if err != nil || user == nil {
		http.Error(w, "INVALID REFRESH TOKEN", http.StatusUnauthorized)
		return
	}

	token, expiresAt, err := a.authMgr.IssueSession(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, v1AuthResponse{
		Token:     token,
		ExpiresIn: int(time.Until(expiresAt).Seconds()),
		IssuedAt:  time.Now().UTC(),
		Username:  user.Username,
		Groups:    user.Roles,
	})
}

// ── Repo handlers ────────────────────────────────────────────────────────

type v1Repo struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Namespace   string    `json:"namespace"`
	FullName    string    `json:"full_name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	Private     bool      `json:"private"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (a *V1API) handleCreateRepo(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, _ map[string]string) {
	if !a.can(user, rbac.ActionCreate, "*") {
		http.Error(w, "FORBIDDEN", http.StatusForbidden)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Namespace   string `json:"namespace"`
		Description string `json:"description"`
		Private     bool   `json:"private"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}
	if !v1RepoNamePattern.MatchString(req.Name) {
		http.Error(w, "INVALID REPOSITORY NAME", http.StatusBadRequest)
		return
	}

	ns := req.Namespace
	if ns == "" {
		ns = user.Username
	}
	if !a.access.CanCreateInNamespace(r.Context(), user, ns) {
		http.Error(w, "FORBIDDEN", http.StatusForbidden)
		return
	}

	existing, err := a.store.GetArtifactRepository(r.Context(), ns, req.Name)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		http.Error(w, "REPOSITORY EXISTS", http.StatusConflict)
		return
	}

	isPrivate := req.Private
	if !isPrivate && ns != user.Username {
		isPrivate = a.manager.EffectivePrivateByDefault(r.Context(), ns)
	}
	repo := &storage.ArtifactRepository{
		Namespace:   ns,
		Name:        req.Name,
		Description: req.Description,
		OwnerID:     user.ID,
		IsPrivate:   isPrivate,
	}
	if err := a.store.CreateArtifactRepository(r.Context(), repo); err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (a *V1API) handleListRepos(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, _ map[string]string) {
	if !a.canAny(user, rbac.ActionRead) {
		http.Error(w, "FORBIDDEN", http.StatusForbidden)
		return
	}

	repos, err := a.listVisibleRepos(r, user, r.URL.Query().Get("namespace"))
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}

	owners := map[string]string{}
	out := make([]v1Repo, 0, len(repos))
	for _, repo := range repos {
		out = append(out, a.repoToV1(r, repo, owners))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *V1API) handleDeleteRepo(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionDelete)
	if !ok {
		return
	}
	if !a.access.HasRepoAccess(r.Context(), user, repo, rbac.ActionDelete) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	if err := a.manager.DeleteRepository(r.Context(), repo); err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Upload handlers ──────────────────────────────────────────────────────

func (a *V1API) handleInitiateUpload(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionPush)
	if !ok {
		return
	}
	if repo.IsPrivate && !a.access.HasRepoAccess(r.Context(), user, repo, rbac.ActionPush) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	uploadID, err := a.manager.Blobs().InitiateUpload()
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}

	location := fmt.Sprintf("/api/v1/artifacts/%s/upload/%s", repo.Name, uploadID)
	if user == nil || repo.Namespace != user.Username {
		// Org repos carry the marker so follow-up requests stay namespaced
		location = fmt.Sprintf("/api/v1/artifacts/_ns/%s/%s/upload/%s", repo.Namespace, repo.Name, uploadID)
	}
	w.Header().Set("Location", location)
	w.Header().Set("Upload-ID", uploadID)
	w.WriteHeader(http.StatusAccepted)
}

// No permission gate per chunk, v1 quirk kept
func (a *V1API) handleUploadChunk(w http.ResponseWriter, r *http.Request, _ *auth.AuthenticatedUser, vars map[string]string) {
	if _, err := a.manager.Blobs().AppendChunk(vars["uuid"], r.Body); err != nil {
		http.Error(w, "UPLOAD FAILED", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a *V1API) handleCompleteUpload(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	query := r.URL.Query()
	version := query.Get("version")
	artifactPath := query.Get("path")
	if vars["repo"] == "" || vars["uuid"] == "" || version == "" {
		http.Error(w, "Required parameters missing", http.StatusBadRequest)
		return
	}

	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionPush)
	if !ok {
		return
	}
	if repo.IsPrivate && !a.access.HasRepoAccess(r.Context(), user, repo, rbac.ActionPush) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Properties come from query param and PUT body
	properties := map[string]string{}
	if raw := query.Get("properties"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &properties); err != nil {
			a.log.Debug("v1 facade: bad properties query param: %v", err)
		}
	}
	if err := json.NewDecoder(r.Body).Decode(&properties); err != nil && err.Error() != "EOF" {
		a.log.Debug("v1 facade: bad properties body: %v", err)
	}

	artifact, err := a.manager.CompleteUpload(r.Context(), repo, vars["uuid"], version, artifactPath, "", properties)
	if err != nil {
		a.writeManagerErr(w, err)
		return
	}

	a.log.Info("v1 facade: artifact %s uploaded to %s@%s", artifact.Path, repo.Name, artifact.Version)
	w.WriteHeader(http.StatusCreated)
}

// ── Download handlers ────────────────────────────────────────────────────

func (a *V1API) handleDownload(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionPull)
	if !ok {
		return
	}
	if !a.access.CanSee(r.Context(), user, repo) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	if err := ValidatePath(vars["path"]); err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	artifact, err := a.store.GetArtifactByPathVersion(r.Context(), repo.ID, vars["version"], vars["path"])
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	if artifact == nil {
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return
	}

	f, info, err := a.manager.Blobs().OpenBlob(artifact.Digest)
	if err != nil {
		a.log.Error("v1 facade: blob missing for artifact %s (%s)", artifact.ID, artifact.Digest)
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, artifact.Name, info.ModTime(), f)
}

func (a *V1API) handleQuery(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionPull)
	if !ok {
		return
	}
	if !a.access.CanSee(r.Context(), user, repo) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	query := r.URL.Query()
	criteria := stores.ArtifactSearchCriteria{
		RepoID:     &repo.ID,
		Name:       query.Get("name"),
		Version:    query.Get("version"),
		Path:       query.Get("path"),
		Properties: map[string]string{},
		Sort:       query.Get("sort"),
		Order:      strings.ToUpper(query.Get("order")),
		Limit:      1, // V1 default latest match only
	}
	if n, err := strconv.Atoi(query.Get("num")); err == nil && n > 0 {
		criteria.Limit = n
	}

	skip := map[string]bool{"num": true, "format": true, "name": true, "version": true, "upload_id": true, "path": true, "sort": true, "order": true, "flat": true}
	for key, values := range query {
		if skip[key] || len(values) == 0 {
			continue
		}
		criteria.Properties[key] = values[0]
	}

	artifacts, _, err := a.store.SearchArtifacts(r.Context(), criteria)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	if len(artifacts) == 0 {
		http.Error(w, "No matching artifacts found", http.StatusNotFound)
		return
	}

	format := NormalizeFormat(query.Get("format"))
	flat := query.Get("flat") == "1"

	contentType := "application/zip"
	if format == FormatTarGz {
		contentType = "application/gzip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", repo.Name+"-artifacts."+format))

	if err := a.manager.WriteArchive(w, artifacts, format, flat); err != nil {
		a.log.Error("v1 facade: archive stream for %s: %v", repo.Name, err)
	}
}

func (a *V1API) handleListVersions(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionRead)
	if !ok {
		return
	}
	if !a.access.CanSee(r.Context(), user, repo) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	artifacts, _, err := a.store.ListArtifacts(r.Context(), repo.ID, "", 0, 0)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}

	grouped := map[string][]v1Artifact{}
	for _, artifact := range artifacts {
		grouped[artifact.Version] = append(grouped[artifact.Version], artifactToV1(artifact))
	}
	writeJSON(w, http.StatusOK, grouped)
}

// ── Search ───────────────────────────────────────────────────────────────

type v1SearchResponse struct {
	Results []v1Artifact `json:"results"`
	Total   int          `json:"total"`
	Limit   int          `json:"limit"`
	Offset  int          `json:"offset"`
	Sort    string       `json:"sort"`
	Order   string       `json:"order"`
}

var v1SortFields = map[string]bool{
	"name": true, "version": true, "path": true,
	"size": true, "created_at": true, "updated_at": true,
}

func (a *V1API) handleSearch(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, _ map[string]string) {
	if !a.canAny(user, rbac.ActionRead) {
		http.Error(w, "FORBIDDEN", http.StatusForbidden)
		return
	}

	query := r.URL.Query()
	criteria := stores.ArtifactSearchCriteria{
		Name:       query.Get("name"),
		Version:    query.Get("version"),
		Path:       query.Get("path"),
		Properties: map[string]string{},
		Limit:      9999, // V1 default
	}

	sortField := query.Get("sort")
	if sortField == "" {
		sortField = "created_at"
	}
	if !v1SortFields[sortField] {
		http.Error(w, "INVALID SORT FIELD", http.StatusBadRequest)
		return
	}
	criteria.Sort = sortField

	order := strings.ToUpper(query.Get("order"))
	if order != "ASC" && order != "DESC" {
		order = "DESC"
	}
	criteria.Order = order

	if n, err := strconv.Atoi(query.Get("num")); err == nil && n > 0 {
		criteria.Limit = n
	}
	if n, err := strconv.Atoi(query.Get("offset")); err == nil && n > 0 {
		criteria.Offset = n
	}

	searchNS := query.Get("namespace")
	if repoName := query.Get("repo"); repoName != "" {
		ns := searchNS
		if ns == "" && user != nil {
			ns = user.Username
		}
		repo, err := a.store.GetArtifactRepository(r.Context(), ns, repoName)
		if err != nil {
			http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
			return
		}
		if repo == nil {
			http.Error(w, "Repository not found", http.StatusNotFound)
			return
		}
		if !a.access.CanSee(r.Context(), user, repo) {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		criteria.RepoID = &repo.ID
	} else {
		repos, err := a.listVisibleRepos(r, user, searchNS)
		if err != nil {
			http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
			return
		}
		if len(repos) == 0 {
			writeJSON(w, http.StatusOK, v1SearchResponse{Results: []v1Artifact{}, Sort: sortField, Order: order})
			return
		}
		for _, repo := range repos {
			criteria.RepoIDs = append(criteria.RepoIDs, repo.ID)
		}
	}

	skip := map[string]bool{"username": true, "repo": true, "namespace": true, "num": true, "offset": true, "archive": true, "format": true, "name": true, "version": true, "path": true, "sort": true, "order": true}
	for key, values := range query {
		if skip[key] || len(values) == 0 {
			continue
		}
		criteria.Properties[key] = values[0]
	}

	artifacts, _, err := a.store.SearchArtifacts(r.Context(), criteria)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}

	results := make([]v1Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		results = append(results, artifactToV1(artifact))
	}

	// V1 quirks, total is row count and offset zero
	writeJSON(w, http.StatusOK, v1SearchResponse{
		Results: results,
		Total:   len(results),
		Limit:   min(criteria.Limit, len(results)),
		Offset:  0,
		Sort:    sortField,
		Order:   order,
	})
}

// ── Artifact mutation handlers ───────────────────────────────────────────

func (a *V1API) handleDeleteArtifact(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionDelete)
	if !ok {
		return
	}
	if !a.access.HasRepoAccess(r.Context(), user, repo, rbac.ActionDelete) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	artifact, err := a.store.GetArtifactByPathVersion(r.Context(), repo.ID, vars["version"], vars["path"])
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	if artifact == nil {
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return
	}

	if err := a.manager.DeleteArtifact(r.Context(), artifact); err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *V1API) handleUpdateMetadata(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionUpdate)
	if !ok {
		return
	}
	// V1 allowed metadata updates on any visible repo
	if !a.access.CanSee(r.Context(), user, repo) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	var metadata map[string]any
	if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	artifact, ok := a.getRepoArtifact(w, r, repo, vars["id"])
	if !ok {
		return
	}

	raw, err := json.Marshal(metadata)
	if err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}
	artifact.Metadata = string(raw)
	if err := a.store.UpdateArtifact(r.Context(), artifact); err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *V1API) handleUpdateProperties(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionUpdate)
	if !ok {
		return
	}
	if !a.access.HasRepoAccess(r.Context(), user, repo, rbac.ActionUpdate) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	var properties map[string]string
	if err := json.NewDecoder(r.Body).Decode(&properties); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	artifact, ok := a.getRepoArtifact(w, r, repo, vars["id"])
	if !ok {
		return
	}

	if err := a.store.SetArtifactProperties(r.Context(), artifact.ID, properties); err != nil {
		if errors.Is(err, stores.ErrDuplicateIdentity) {
			http.Error(w, "Artifact with this version, path, and property set exists", http.StatusConflict)
			return
		}
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *V1API) handleRename(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, vars map[string]string) {
	repo, ok := a.getRepo(w, r, user, a.repoNS(user, vars), vars["repo"], rbac.ActionUpdate)
	if !ok {
		return
	}
	if !a.access.HasRepoAccess(r.Context(), user, repo, rbac.ActionUpdate) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	artifact, ok := a.getRepoArtifact(w, r, repo, vars["id"])
	if !ok {
		return
	}

	newPath := req.Path
	if newPath == "" {
		if dir := path.Dir(artifact.Path); dir != "." {
			newPath = dir + "/" + req.Name
		} else {
			newPath = req.Name
		}
	}
	if err := ValidatePath(newPath); err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	artifact.Name = req.Name
	artifact.Path = newPath
	if req.Version != "" {
		if err := ValidateVersion(req.Version); err != nil {
			http.Error(w, "Invalid version", http.StatusBadRequest)
			return
		}
		artifact.Version = req.Version
	}

	if err := a.store.UpdateArtifact(r.Context(), artifact); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			http.Error(w, "CONFLICT", http.StatusConflict)
			return
		}
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ── Access helpers ───────────────────────────────────────────────────────

// Route level rbac check like v1 requirePermission
func (a *V1API) can(user *auth.AuthenticatedUser, action, objectID string) bool {
	if user == nil {
		return false
	}
	allowed, err := a.enforcer.Enforce(user.Roles, rbac.ResourceArtifacts, action, objectID)
	if err != nil {
		a.log.Error("v1 facade: rbac enforce: %v", err)
		return false
	}
	return allowed
}

// Global grant or any scoped object grant
func (a *V1API) canAny(user *auth.AuthenticatedUser, action string) bool {
	if a.can(user, action, "*") {
		return true
	}
	return user != nil && len(a.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceArtifacts, action)) > 0
}

// Resolves the request namespace, marker takes precedence over caller
func (a *V1API) repoNS(user *auth.AuthenticatedUser, vars map[string]string) string {
	if ns := vars["namespace"]; ns != "" {
		return ns
	}
	if user != nil {
		return user.Username
	}
	return ""
}

// Route permission check plus repo fetch
func (a *V1API) getRepo(w http.ResponseWriter, r *http.Request, user *auth.AuthenticatedUser, namespace, name, action string) (*storage.ArtifactRepository, bool) {
	if namespace == "" && user != nil {
		namespace = user.Username
	}
	if !a.can(user, action, namespace+"/"+name) {
		http.Error(w, "FORBIDDEN", http.StatusForbidden)
		return nil, false
	}
	repo, err := a.store.GetArtifactRepository(r.Context(), namespace, name)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return nil, false
	}
	if repo == nil {
		http.Error(w, "Repository not found", http.StatusNotFound)
		return nil, false
	}
	return repo, true
}

func (a *V1API) getRepoArtifact(w http.ResponseWriter, r *http.Request, repo *storage.ArtifactRepository, id string) (*storage.Artifact, bool) {
	artifact, err := a.store.GetArtifact(r.Context(), id)
	if err != nil {
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
		return nil, false
	}
	if artifact == nil || artifact.RepoID != repo.ID {
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return nil, false
	}
	return artifact, true
}

// Public repos plus own plus org plus scoped grants
func (a *V1API) listVisibleRepos(r *http.Request, user *auth.AuthenticatedUser, namespace string) ([]*storage.ArtifactRepository, error) {
	repos, _, err := a.store.ListArtifactRepositories(r.Context(), a.access.ListOptions(user, namespace))
	if err != nil {
		return nil, err
	}
	return repos, nil
}

// ── JSON mapping ─────────────────────────────────────────────────────────

type v1Artifact struct {
	ID         string            `json:"id"`
	RepoID     int64             `json:"repo_id"`
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	UploadID   string            `json:"upload_id"`
	Version    string            `json:"version"`
	Size       int64             `json:"size"`
	MimeType   string            `json:"mime_type"`
	Metadata   string            `json:"metadata"`
	Properties map[string]string `json:"properties"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

func artifactToV1(a *storage.Artifact) v1Artifact {
	props := a.Properties
	if props == nil {
		props = map[string]string{}
	}
	return v1Artifact{
		ID:         a.ID,
		RepoID:     a.RepoID,
		Name:       a.Name,
		Path:       a.Path,
		UploadID:   a.UploadID,
		Version:    a.Version,
		Size:       a.Size,
		MimeType:   a.MimeType,
		Metadata:   a.Metadata,
		Properties: props,
		CreatedAt:  a.CreatedAt,
		UpdatedAt:  a.UpdatedAt,
	}
}

func (a *V1API) repoToV1(r *http.Request, repo *storage.ArtifactRepository, ownerCache map[string]string) v1Repo {
	owner, ok := ownerCache[repo.OwnerID]
	if !ok && repo.OwnerID != "" {
		if u, err := a.store.GetUserByID(r.Context(), repo.OwnerID); err == nil && u != nil {
			owner = u.Username
		}
		ownerCache[repo.OwnerID] = owner
	}
	return v1Repo{
		ID:          repo.ID,
		Name:        repo.Name,
		Namespace:   repo.Namespace,
		FullName:    repo.Namespace + "/" + repo.Name,
		Description: repo.Description,
		Owner:       owner,
		Private:     repo.IsPrivate,
		CreatedAt:   repo.CreatedAt,
		UpdatedAt:   repo.UpdatedAt,
	}
}

func (a *V1API) writeManagerErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUploadNotFound):
		http.Error(w, "Upload not found", http.StatusNotFound)
	case errors.Is(err, ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "SERVER ERROR", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
