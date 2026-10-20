package ghapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/pages"
)

const labelPageCap = 1000

// Unit an issue row belongs to
func issueUnit(issue *db.GitIssue) string {
	if issue.IsPull {
		return db.GitUnitPulls
	}
	return db.GitUnitIssues
}

// Loads an issue by number and checks read on its unit
func (q *request) issue(repo *db.GitRepository) (*db.GitIssue, bool) {
	number, ok := q.pathInt("number")
	if !ok {
		return nil, false
	}
	issue, err := q.h.forge.Issue(q.ctx, repo, number)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	if !q.readable(repo, issueUnit(issue)) {
		return nil, false
	}
	return issue, true
}

func (q *request) issueView(repo *db.GitRepository, issue *db.GitIssue) (obj, bool) {
	v, err := q.h.forge.IssueView(q.ctx, q.user, repo, issue)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	return q.issueJSON(repo, v), true
}

func (q *request) orgID(repo *db.GitRepository) string {
	if !repo.IsOrgNamespace {
		return ""
	}
	org, _ := q.h.store.GetOrganization(q.ctx, repo.Namespace)
	if org == nil {
		return ""
	}
	return org.ID
}

// Repository and organization labels keyed by lower cased name
func (q *request) labelsByName(repo *db.GitRepository) (map[string]*db.GitLabel, []*db.GitLabel, error) {
	labels, _, err := q.h.forge.ListLabels(q.ctx, repo, q.orgID(repo), pages.Query{}, labelPageCap, 0)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[string]*db.GitLabel, len(labels))
	for _, l := range labels {
		byName[strings.ToLower(l.Name)] = l
	}
	return byName, labels, nil
}

// Label ids for names, creating missing ones like GitHub does
func (q *request) labelIDs(repo *db.GitRepository, names []string, create bool) ([]string, bool) {
	byName, _, err := q.labelsByName(repo)
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	var ids []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if l := byName[strings.ToLower(name)]; l != nil {
			ids = append(ids, l.ID)
			continue
		}
		if !create {
			q.validation("Label", "name", "invalid", "label "+name+" does not exist")
			return nil, false
		}
		l, err := q.h.forge.CreateLabel(q.ctx, repo, "", forge.LabelInput{Name: name, Color: "ededed"})
		if err != nil {
			q.failErr(err)
			return nil, false
		}
		byName[strings.ToLower(name)] = l
		ids = append(ids, l.ID)
	}
	return ids, true
}

// Strings from a json field that may be a list or one value
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*s = list
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = []string{one}
	return nil
}

// -- Issues --

func (q *request) listIssues() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	lv := q.levels(repo)
	issues := lv.Unit(db.GitUnitIssues) >= gitrepo.LevelRead && (gitrepo.UnitEnabled(repo, db.GitUnitIssues) || lv.IsOwner || lv.CanAdmin)
	pulls := lv.Unit(db.GitUnitPulls) >= gitrepo.LevelRead && (gitrepo.UnitEnabled(repo, db.GitUnitPulls) || lv.IsOwner || lv.CanAdmin)
	if !issues && !pulls {
		q.notFound()
		return
	}
	p := parsePage(q.r)
	opts := stores.IssueListOptions{RepoID: repo.ID, State: q.query("state"), Limit: p.perPage, Offset: p.offset()}
	switch opts.State {
	case "", "open":
		opts.State = "open"
	case "closed":
	case "all":
		opts.State = ""
	default:
		q.validation("Issue", "state", "invalid", "state must be open, closed, or all")
		return
	}
	if labels := q.query("labels"); labels != "" {
		for _, l := range strings.Split(labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				opts.Labels = append(opts.Labels, l)
			}
		}
	}
	opts.Author = q.query("creator")
	opts.Assignee = q.query("assignee")
	opts.Milestone = q.query("milestone")
	if issues != pulls {
		isPull := pulls
		opts.IsPull = &isPull
	}
	if v := q.query("pulls"); v == "false" {
		f := false
		opts.IsPull = &f
	}
	opts.OrderBy = issueOrder(q.query("sort"), q.query("direction"))
	rows, total, err := q.h.forge.ListIssues(q.ctx, opts)
	if err != nil {
		q.failErr(err)
		return
	}
	views, err := q.h.forge.IssueViews(q.ctx, q.user, map[string]*db.GitRepository{repo.ID: repo}, rows)
	if err != nil {
		q.failErr(err)
		return
	}
	out := make([]obj, 0, len(views))
	for _, v := range views {
		out = append(out, q.issueJSON(repo, v))
	}
	q.paginate(p, total)
	q.json(http.StatusOK, out)
}

// GitHub sort and direction parameters as an order clause
func issueOrder(sort, direction string) string {
	col := "created_at"
	switch sort {
	case "updated":
		col = "updated_at"
	case "comments":
		col = "comments"
	}
	dir := "DESC"
	if strings.EqualFold(direction, "asc") {
		dir = "ASC"
	}
	return col + " " + dir
}

type issueBody struct {
	Title     *string    `json:"title"`
	Body      *string    `json:"body"`
	State     *string    `json:"state"`
	Labels    stringList `json:"labels"`
	Assignees stringList `json:"assignees"`
	Assignee  *string    `json:"assignee"`
	Milestone any        `json:"milestone"`
}

func (q *request) createIssue() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitIssues, rbac.ActionRead) {
		return
	}
	var in issueBody
	if !q.decode(&in) {
		return
	}
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		q.validation("Issue", "title", "missing_field", "title is required")
		return
	}
	actor := q.actor(repo, db.GitUnitIssues)
	var labelIDs []string
	if len(in.Labels) > 0 {
		if labelIDs, ok = q.labelIDs(repo, in.Labels, actor.CanWrite); !ok {
			return
		}
	}
	body := ""
	if in.Body != nil {
		body = *in.Body
	}
	issue, err := q.h.forge.CreateIssue(q.ctx, repo, actor, forge.NewIssue{Title: *in.Title, Body: body, LabelIDs: labelIDs, AssigneeIDs: q.userIDs(in.Assignees, in.Assignee)})
	if err != nil {
		q.failErr(err)
		return
	}
	out, ok := q.issueView(repo, issue)
	if !ok {
		return
	}
	q.json(http.StatusCreated, out)
}

// User ids behind login names, unknown names are dropped
func (q *request) userIDs(names stringList, single *string) []string {
	all := append([]string{}, names...)
	if single != nil && *single != "" {
		all = append(all, *single)
	}
	var ids []string
	for _, n := range all {
		if u := q.dbUserByName(strings.TrimSpace(n)); u != nil {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

func (q *request) getIssue() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	out, ok := q.issueView(repo, issue)
	if !ok {
		return
	}
	q.json(http.StatusOK, out)
}

func (q *request) updateIssue() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	unit := issueUnit(issue)
	if q.user == nil || q.user.IsAnonymous() {
		q.fail(http.StatusUnauthorized, "Requires authentication")
		return
	}
	if !q.writableLoose(repo, unit) {
		return
	}
	var in issueBody
	if !q.decode(&in) {
		return
	}
	actor := q.actor(repo, unit)
	up := forge.IssueUpdate{Title: in.Title, Body: in.Body}
	if in.State != nil {
		switch *in.State {
		case "open", "closed":
		default:
			q.validation("Issue", "state", "invalid", "state must be open or closed")
			return
		}
		up.State = in.State
	}
	if in.Labels != nil {
		ids, ok := q.labelIDs(repo, in.Labels, actor.CanWrite)
		if !ok {
			return
		}
		up.SetLabels, up.LabelIDs = true, ids
	}
	if in.Assignees != nil || in.Assignee != nil {
		up.SetAssign, up.AssigneeIDs = true, q.userIDs(in.Assignees, in.Assignee)
	}
	if err := q.h.forge.UpdateIssue(q.ctx, repo, actor, issue, up); err != nil {
		q.failErr(err)
		return
	}
	out, ok := q.issueView(repo, issue)
	if !ok {
		return
	}
	q.json(http.StatusOK, out)
}

// Comments and edits need a signed in reader, the forge decides authorship rules
func (q *request) writableLoose(repo *db.GitRepository, unit string) bool {
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
	if !q.capability(rbac.ActionRead, "", true) {
		q.fail(http.StatusForbidden, "Resource not accessible by integration")
		return false
	}
	return true
}

// -- Comments --

func (q *request) listComments() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	p := parsePage(q.r)
	rows, total, err := q.h.store.ListGitComments(q.ctx, issue.ID, true, p.perPage, p.offset())
	if err != nil {
		q.failErr(err)
		return
	}
	out := make([]obj, 0, len(rows))
	for _, c := range rows {
		out = append(out, q.commentJSON(repo, issue, c))
	}
	q.paginate(p, total)
	q.json(http.StatusOK, out)
}

func (q *request) createComment() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	if !q.writableLoose(repo, issueUnit(issue)) {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !q.decode(&in) {
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		q.validation("IssueComment", "body", "missing_field", "body is required")
		return
	}
	c, err := q.h.forge.CreateComment(q.ctx, repo, q.actor(repo, issueUnit(issue)), issue, in.Body, false)
	if err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusCreated, q.commentJSON(repo, issue, c))
}

// Comment by integer or row id within the repository
func (q *request) commentByPath(repo *db.GitRepository) (*db.GitIssue, *db.GitComment, bool) {
	id := q.param("id")
	if issue, c, err := q.h.forge.Comment(q.ctx, repo, id); err == nil {
		return issue, c, true
	}
	ids, err := q.h.store.ListGitCommentIDsByRepo(q.ctx, repo.ID)
	if err != nil {
		q.failErr(err)
		return nil, nil, false
	}
	for _, rowID := range ids {
		if !idMatches(id, rowID) {
			continue
		}
		issue, c, err := q.h.forge.Comment(q.ctx, repo, rowID)
		if err != nil {
			q.failErr(err)
			return nil, nil, false
		}
		return issue, c, true
	}
	q.notFound()
	return nil, nil, false
}

func (q *request) getComment() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, c, ok := q.commentByPath(repo)
	if !ok || !q.readable(repo, issueUnit(issue)) {
		return
	}
	q.json(http.StatusOK, q.commentJSON(repo, issue, c))
}

func (q *request) updateComment() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, c, ok := q.commentByPath(repo)
	if !ok || !q.writableLoose(repo, issueUnit(issue)) {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !q.decode(&in) {
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		q.validation("IssueComment", "body", "missing_field", "body is required")
		return
	}
	if err := q.h.forge.UpdateComment(q.ctx, repo, q.actor(repo, issueUnit(issue)), issue, c, in.Body); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusOK, q.commentJSON(repo, issue, c))
}

func (q *request) deleteComment() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, c, ok := q.commentByPath(repo)
	if !ok || !q.writableLoose(repo, issueUnit(issue)) {
		return
	}
	if err := q.h.forge.DeleteComment(q.ctx, repo, q.actor(repo, issueUnit(issue)), issue, c); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusNoContent, nil)
}

// -- Labels --

func (q *request) listLabels() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitIssues) {
		return
	}
	_, labels, err := q.labelsByName(repo)
	if err != nil {
		q.failErr(err)
		return
	}
	out := make([]obj, 0, len(labels))
	for _, l := range labels {
		out = append(out, q.labelJSON(repo, l))
	}
	p := parsePage(q.r)
	q.paginate(p, int64(len(out)))
	q.json(http.StatusOK, pageSlice(out, p))
}

func (q *request) currentLabels(issue *db.GitIssue) ([]*db.GitLabel, bool) {
	labels, err := q.h.store.GetGitIssueLabels(q.ctx, []string{issue.ID})
	if err != nil {
		q.failErr(err)
		return nil, false
	}
	return labels[issue.ID], true
}

func (q *request) labelRows(repo *db.GitRepository, labels []*db.GitLabel) []obj {
	out := make([]obj, 0, len(labels))
	for _, l := range labels {
		out = append(out, q.labelJSON(repo, l))
	}
	return out
}

func (q *request) listIssueLabels() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	labels, ok := q.currentLabels(issue)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.labelRows(repo, labels))
}

// Label names from a body that is a list, an object with labels, or one string
func (q *request) labelNames() ([]string, bool) {
	var raw any
	if !q.decode(&raw) {
		return nil, false
	}
	switch v := raw.(type) {
	case nil:
		return nil, true
	case string:
		return []string{v}, true
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	case map[string]any:
		var out []string
		if list, ok := v["labels"].([]any); ok {
			for _, item := range list {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
		}
		return out, true
	}
	q.fail(http.StatusBadRequest, "Problems parsing JSON")
	return nil, false
}

func (q *request) applyLabels(replace bool) {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	unit := issueUnit(issue)
	if !q.writableLoose(repo, unit) {
		return
	}
	names, ok := q.labelNames()
	if !ok {
		return
	}
	actor := q.actor(repo, unit)
	ids, ok := q.labelIDs(repo, names, actor.CanWrite)
	if !ok {
		return
	}
	if !replace {
		current, ok := q.currentLabels(issue)
		if !ok {
			return
		}
		seen := map[string]bool{}
		for _, id := range ids {
			seen[id] = true
		}
		for _, l := range current {
			if !seen[l.ID] {
				ids = append(ids, l.ID)
			}
		}
	}
	if err := q.h.forge.SetLabels(q.ctx, repo, actor, issue, ids); err != nil {
		q.failErr(err)
		return
	}
	labels, ok := q.currentLabels(issue)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.labelRows(repo, labels))
}

func (q *request) addIssueLabels() { q.applyLabels(false) }
func (q *request) setIssueLabels() { q.applyLabels(true) }

func (q *request) removeIssueLabel() {
	repo, ok := q.repo()
	if !ok {
		return
	}
	issue, ok := q.issue(repo)
	if !ok {
		return
	}
	unit := issueUnit(issue)
	if !q.writableLoose(repo, unit) {
		return
	}
	current, ok := q.currentLabels(issue)
	if !ok {
		return
	}
	name := q.r.PathValue("name")
	var ids []string
	found := false
	for _, l := range current {
		if strings.EqualFold(l.Name, name) {
			found = true
			continue
		}
		ids = append(ids, l.ID)
	}
	if !found {
		q.fail(http.StatusNotFound, "Label does not exist")
		return
	}
	if err := q.h.forge.SetLabels(q.ctx, repo, q.actor(repo, unit), issue, ids); err != nil {
		q.failErr(err)
		return
	}
	labels, ok := q.currentLabels(issue)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.labelRows(repo, labels))
}
