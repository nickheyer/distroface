package ghapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/nickheyer/distroface/pkg/utils"
)

func (q *request) getUser() {
	if q.user == nil || q.user.IsAnonymous() {
		q.fail(http.StatusUnauthorized, "Requires authentication")
		return
	}
	u := q.dbUser(q.user.ID)
	if u == nil {
		u = &db.User{ID: q.user.ID, Username: q.user.Username, DisplayName: q.user.Username}
	}
	out := q.userJSON(u)
	out["email"] = nilIfEmpty(q.user.Email)
	out["created_at"] = ts(u.CreatedAt)
	out["updated_at"] = ts(u.UpdatedAt)
	q.json(http.StatusOK, out)
}

func (q *request) rateLimit() {
	reset := time.Now().Add(time.Hour).Unix()
	core := obj{"limit": 5000, "remaining": 5000, "reset": reset, "used": 0, "resource": "core"}
	q.json(http.StatusOK, obj{"resources": obj{"core": core, "search": core, "graphql": core}, "rate": core})
}

func (q *request) getRepo() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	q.json(http.StatusOK, q.repoJSON(repo))
}

// Opens the git storage of a repository, 404 when it cannot be read
func (q *request) open(repo *db.GitRepository) (*gitrepo.Repo, bool) {
	r, err := q.h.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	return r, true
}

// Resolves a ref falling back to the default branch
func resolveRef(r *gitrepo.Repo, repo *db.GitRepository, ref string) (plumbing.Hash, bool) {
	if ref == "" {
		ref = repo.DefaultBranch
	}
	if r.IsEmpty() {
		return plumbing.ZeroHash, false
	}
	h, err := r.Resolve(ref)
	if err != nil {
		return plumbing.ZeroHash, false
	}
	return h, true
}

func (q *request) getBranch() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	defer r.Close()
	name := q.r.PathValue("branch")
	sha := r.BranchHash(name)
	if sha.IsZero() {
		q.fail(http.StatusNotFound, "Branch not found")
		return
	}
	protections, _ := q.h.store.ListGitBranchProtections(q.ctx, repo.ID)
	protected := gitrepo.MatchProtection(protections, name) != nil
	out := obj{
		"name": name, "protected": protected, "protection": obj{"enabled": protected},
		"commit": obj{"sha": sha.String(), "url": q.repoAPI(repo) + "/commits/" + sha.String()},
		"_links": obj{"self": q.repoAPI(repo) + "/branches/" + name, "html": q.repoHTML(repo) + "/tree/" + name},
	}
	if c, err := r.Commit(sha); err == nil {
		out["commit"] = q.commitJSON(repo, c, true)
	}
	q.json(http.StatusOK, out)
}

func (q *request) getCommit() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	defer r.Close()
	h, ok := resolveRef(r, repo, q.r.PathValue("ref"))
	if !ok {
		q.fail(http.StatusUnprocessableEntity, "No commit found for SHA: %s", q.r.PathValue("ref"))
		return
	}
	c, err := r.Commit(h)
	if err != nil {
		q.fail(http.StatusUnprocessableEntity, "No commit found for SHA: %s", q.r.PathValue("ref"))
		return
	}
	out := q.commitJSON(repo, c, true)
	out["stats"] = obj{"additions": 0, "deletions": 0, "total": 0}
	out["files"] = []obj{}
	q.json(http.StatusOK, out)
}

// One ref by its full name below refs/
func (q *request) getRef() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	defer r.Close()
	ref := strings.Trim(q.r.PathValue("ref"), "/")
	kind, name, found := strings.Cut(ref, "/")
	if !found || name == "" {
		q.notFound()
		return
	}
	switch kind {
	case "heads":
		if sha := r.BranchHash(name); !sha.IsZero() {
			q.json(http.StatusOK, q.refJSON(repo, "refs/heads/"+name, "commit", sha.String()))
			return
		}
	case "tags":
		if sha := r.TagHash(name); !sha.IsZero() {
			objType := "commit"
			if r.TagObject(sha) != nil {
				objType = "tag"
			}
			q.json(http.StatusOK, q.refJSON(repo, "refs/tags/"+name, objType, sha.String()))
			return
		}
	case "pull":
		if h, err := r.Resolve("refs/pull/" + name); err == nil {
			q.json(http.StatusOK, q.refJSON(repo, "refs/pull/"+name, "commit", h.String()))
			return
		}
	}
	q.notFound()
}

// File content or directory listing at a ref
func (q *request) getContents() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	defer r.Close()
	ref := q.query("ref")
	if ref == "" {
		ref = repo.DefaultBranch
	}
	commit, ok := resolveRef(r, repo, ref)
	if !ok {
		q.fail(http.StatusNotFound, "No commit found for the ref %s", ref)
		return
	}
	p := strings.Trim(path.Clean("/"+q.r.PathValue("path")), "/")
	if p == "." {
		p = ""
	}
	api := q.repoAPI(repo) + "/contents/"
	html := q.repoHTML(repo)
	entryJSON := func(e gitrepo.Entry) obj {
		kind := "file"
		switch {
		case e.IsSubmodule:
			kind = "submodule"
		case e.Mode == filemode.Symlink:
			kind = "symlink"
		case !e.Mode.IsFile():
			kind = "dir"
		}
		out := obj{
			"type": kind, "name": e.Name, "path": e.Path, "sha": e.Hash.String(), "size": e.Size,
			"url": api + e.Path + "?ref=" + ref, "html_url": html + "/tree/" + ref + "/" + e.Path,
			"git_url": q.repoAPI(repo) + "/git/blobs/" + e.Hash.String(), "download_url": nil,
		}
		if kind == "file" || kind == "symlink" {
			out["html_url"] = html + "/blob/" + ref + "/" + e.Path
			out["download_url"] = html + "/-/raw/" + ref + "/" + e.Path
		}
		out["_links"] = obj{"self": out["url"], "git": out["git_url"], "html": out["html_url"]}
		return out
	}
	if p != "" {
		if b, err := r.Blob(commit, p, gitrepo.MaxInlineBlob); err == nil {
			out := entryJSON(b.Entry)
			out["type"] = "file"
			if b.Truncated {
				out["content"] = ""
				out["encoding"] = "none"
			} else {
				out["content"] = base64.StdEncoding.EncodeToString(b.Content)
				out["encoding"] = "base64"
			}
			q.json(http.StatusOK, out)
			return
		}
	}
	entries, _, err := r.Tree(q.ctx, commit, p, false)
	if err != nil {
		q.notFound()
		return
	}
	out := make([]obj, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryJSON(e))
	}
	q.json(http.StatusOK, out)
}

// -- Commit statuses --

func (q *request) createStatus() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	if !q.writable(repo, db.GitUnitCode, rpcAction(distrofacev1connect.GitFileServiceCreateCommitStatusProcedure)) {
		return
	}
	var in struct {
		State       string `json:"state"`
		Context     string `json:"context"`
		Description string `json:"description"`
		TargetURL   string `json:"target_url"`
	}
	if !q.decode(&in) {
		return
	}
	switch in.State {
	case "error", "failure", "pending", "success":
	default:
		q.validation("Status", "state", "invalid", "state must be one of error, failure, pending, success")
		return
	}
	if in.TargetURL != "" && !utils.IsWebURL(in.TargetURL) {
		q.validation("Status", "target_url", "invalid", "target_url must be an http or https url")
		return
	}
	if strings.TrimSpace(in.Context) == "" {
		in.Context = "default"
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	h, found := resolveRef(r, repo, q.r.PathValue("sha"))
	if !found {
		r.Close()
		q.fail(http.StatusUnprocessableEntity, "No commit found for SHA: %s", q.r.PathValue("sha"))
		return
	}
	branches := r.BranchesAt(h)
	r.Close()
	st := &db.GitCommitStatus{
		ID: uuid.New().String(), RepoID: repo.ID, SHA: h.String(), Context: strings.TrimSpace(in.Context), State: in.State,
		TargetURL: in.TargetURL, Description: in.Description, CreatorID: q.user.ID, CreatedAt: time.Now(),
	}
	if err := q.h.store.CreateGitCommitStatus(q.ctx, st); err != nil {
		q.failErr(err)
		return
	}
	// Open pull requests at this head recompute their merge state
	for _, b := range branches {
		prs, _ := q.h.store.ListOpenPullsByHead(q.ctx, repo.ID, b)
		for _, pr := range prs {
			if pr.MergeableState == "blocked" {
				_ = q.h.store.UpdateGitPullRequestColumns(q.ctx, pr.IssueID, map[string]any{"mergeable_state": "unknown"})
			}
		}
	}
	if in.State != "pending" {
		go q.h.forge.RetryAutoMerge(context.WithoutCancel(q.ctx), repo, h.String())
	}
	q.json(http.StatusCreated, q.statusJSON(repo, st))
}

func (q *request) listStatuses() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	h, found := resolveRef(r, repo, q.r.PathValue("ref"))
	r.Close()
	if !found {
		q.notFound()
		return
	}
	p := parsePage(q.r)
	rows, total, err := q.h.store.ListGitCommitStatuses(q.ctx, repo.ID, h.String(), p.perPage, p.offset())
	if err != nil {
		q.failErr(err)
		return
	}
	out := make([]obj, 0, len(rows))
	for _, st := range rows {
		out = append(out, q.statusJSON(repo, st))
	}
	q.paginate(p, total)
	q.json(http.StatusOK, out)
}

func (q *request) combinedStatus() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	r, ok := q.open(repo)
	if !ok {
		return
	}
	h, found := resolveRef(r, repo, q.r.PathValue("ref"))
	r.Close()
	if !found {
		q.notFound()
		return
	}
	rows, err := q.h.store.LatestGitCommitStatuses(q.ctx, repo.ID, h.String())
	if err != nil {
		q.failErr(err)
		return
	}
	statuses := make([]obj, 0, len(rows))
	for _, st := range rows {
		statuses = append(statuses, q.statusJSON(repo, st))
	}
	q.json(http.StatusOK, obj{
		"state": combinedState(rows), "sha": h.String(), "total_count": len(rows), "statuses": statuses,
		"repository": obj{"id": numID(repo.ID), "name": repo.Name, "full_name": repo.Namespace + "/" + repo.Name, "html_url": q.repoHTML(repo)},
		"commit_url": q.repoAPI(repo) + "/commits/" + h.String(), "url": q.repoAPI(repo) + "/commits/" + h.String() + "/status",
	})
}
