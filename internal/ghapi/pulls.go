package ghapi

import (
	"net/http"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
)

func (q *request) pullView(repo *db.GitRepository, issue *db.GitIssue, withMerge bool) (obj, bool) {
	pr, err := q.h.forge.Pull(q.ctx, issue)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	view, err := q.h.forge.PullView(q.ctx, q.actor(repo, db.GitUnitPulls), repo, issue, pr, withMerge)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	return q.pullJSON(repo, view), true
}

func (q *request) listPulls() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitPulls) {
		return
	}
	p := parsePage(q.r)
	isPull := true
	opts := stores.IssueListOptions{RepoID: repo.ID, State: q.query("state"), IsPull: &isPull, Base: q.query("base"), Limit: p.perPage, Offset: p.offset()}
	switch opts.State {
	case "", "open":
		opts.State = "open"
	case "closed":
	case "all":
		opts.State = ""
	default:
		q.validation("PullRequest", "state", "invalid", "state must be open, closed, or all")
		return
	}
	if head := q.query("head"); head != "" {
		// GitHub spells the head as owner:branch, the store wants ns/name:branch or a branch
		if owner, branch, found := strings.Cut(head, ":"); found && !strings.Contains(owner, "/") {
			if owner == repo.Namespace {
				head = branch
			} else {
				head = owner + "/" + repo.Name + ":" + branch
			}
		}
		opts.Head = head
	}
	opts.OrderBy = issueOrder(q.query("sort"), q.query("direction"))
	rows, total, err := q.h.forge.ListIssues(q.ctx, opts)
	if err != nil {
		q.failErr(err)
		return
	}
	out := make([]obj, 0, len(rows))
	for _, issue := range rows {
		view, ok := q.pullView(repo, issue, false)
		if !ok {
			return
		}
		out = append(out, view)
	}
	q.paginate(p, total)
	q.json(http.StatusOK, out)
}

func (q *request) getPull() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitPulls) {
		return
	}
	number, ok := q.pathInt("number")
	if !ok {
		return
	}
	issue, err := q.h.forge.Issue(q.ctx, repo, number)
	if err != nil {
		q.failErr(err)
		return
	}
	if !issue.IsPull {
		q.notFound()
		return
	}
	out, ok := q.pullView(repo, issue, true)
	if !ok {
		return
	}
	q.json(http.StatusOK, out)
}

func (q *request) createPull() {
	repo, ok := q.repo()
	if !ok || !q.writableLoose(repo, db.GitUnitPulls) {
		return
	}
	var in struct {
		Title               string `json:"title"`
		Body                string `json:"body"`
		Head                string `json:"head"`
		Base                string `json:"base"`
		Draft               bool   `json:"draft"`
		MaintainerCanModify *bool  `json:"maintainer_can_modify"`
	}
	if !q.decode(&in) {
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		q.validation("PullRequest", "title", "missing_field", "title is required")
		return
	}
	if strings.TrimSpace(in.Head) == "" || strings.TrimSpace(in.Base) == "" {
		q.validation("PullRequest", "head", "missing_field", "head and base are required")
		return
	}
	head := in.Head
	if owner, branch, found := strings.Cut(head, ":"); found && !strings.Contains(owner, "/") {
		if owner == repo.Namespace {
			head = branch
		} else {
			head = owner + "/" + repo.Name + ":" + branch
		}
	}
	allow := true
	if in.MaintainerCanModify != nil {
		allow = *in.MaintainerCanModify
	}
	issue, pr, err := q.h.forge.CreatePull(q.ctx, repo, q.actor(repo, db.GitUnitPulls), forge.NewPull{
		Base: in.Base, Head: head, Title: in.Title, Body: in.Body, Draft: in.Draft, AllowMaintainerEdits: allow,
	})
	if err != nil {
		q.failErr(err)
		return
	}
	view, err := q.h.forge.PullView(q.ctx, q.actor(repo, db.GitUnitPulls), repo, issue, pr, true)
	if err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusCreated, q.pullJSON(repo, view))
}
