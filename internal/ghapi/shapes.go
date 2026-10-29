package ghapi

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

type obj = map[string]any

// -- Users --

func (q *request) userJSON(u *db.User) obj {
	if u == nil {
		return q.ghost()
	}
	return obj{
		"login": u.Username, "id": numID(u.ID), "node_id": "", "avatar_url": "", "gravatar_id": "",
		"url": q.apiBase() + "/users/" + u.Username, "html_url": q.base() + "/" + u.Username,
		"type": "User", "site_admin": false, "name": u.DisplayName,
	}
}

func (q *request) userByID(id string) obj {
	return q.userJSON(q.dbUser(id))
}

func (q *request) ghost() obj {
	return obj{"login": "ghost", "id": 0, "node_id": "", "avatar_url": "", "url": "", "html_url": "", "type": "User", "site_admin": false}
}

// Namespace account of a repository, an organization or a user
func (q *request) ownerJSON(repo *db.GitRepository) obj {
	if repo.IsOrgNamespace {
		id := numID("org:" + repo.Namespace)
		if org, _ := q.h.store.GetOrganization(q.ctx, repo.Namespace); org != nil {
			id = numID(org.ID)
		}
		return obj{
			"login": repo.Namespace, "id": id, "node_id": "", "avatar_url": "", "url": q.apiBase() + "/orgs/" + repo.Namespace,
			"html_url": q.base() + "/orgs/" + repo.Namespace, "type": "Organization", "site_admin": false,
		}
	}
	if u := q.dbUserByName(repo.Namespace); u != nil {
		return q.userJSON(u)
	}
	return obj{"login": repo.Namespace, "id": numID("user:" + repo.Namespace), "node_id": "", "avatar_url": "", "url": "", "html_url": q.base() + "/" + repo.Namespace, "type": "User", "site_admin": false}
}

// -- Repositories --

func (q *request) repoJSON(repo *db.GitRepository) obj {
	api := q.repoAPI(repo)
	html := q.repoHTML(repo)
	lv := q.levels(repo)
	full := repo.Namespace + "/" + repo.Name
	visibility := "public"
	if repo.IsPrivate {
		visibility = "private"
	}
	out := obj{
		"id": numID(repo.ID), "node_id": "", "name": repo.Name, "full_name": full, "owner": q.ownerJSON(repo),
		"private": repo.IsPrivate, "visibility": visibility, "html_url": html, "url": api, "description": repo.Description,
		"fork": repo.ForkOfID != nil, "archived": repo.IsArchived, "disabled": false, "homepage": repo.Website,
		"default_branch": repo.DefaultBranch, "clone_url": q.h.git.HTTPCloneURL(q.ctx, repo, false),
		"git_url": q.h.git.HTTPCloneURL(q.ctx, repo, false), "ssh_url": q.h.git.SSHCloneURL(q.ctx, repo, false),
		"size": repo.SizeBytes / 1024, "stargazers_count": 0, "watchers_count": 0, "forks_count": 0, "open_issues_count": 0,
		"has_issues": gitrepo.UnitEnabled(repo, db.GitUnitIssues), "has_projects": gitrepo.UnitEnabled(repo, db.GitUnitProjects),
		"has_wiki": gitrepo.UnitEnabled(repo, db.GitUnitWiki), "has_pages": false, "has_downloads": true,
		"topics": []string{}, "language": nil, "license": nil, "mirror_url": nil,
		"created_at": ts(repo.CreatedAt), "updated_at": ts(repo.UpdatedAt), "pushed_at": tsPtr(repo.LastPush),
		"permissions": obj{
			"admin":    lv.IsOwner || lv.CanAdmin,
			"maintain": lv.IsOwner || lv.CanAdmin,
			"push":     lv.Unit(db.GitUnitCode) >= gitrepo.LevelWrite,
			"triage":   lv.Unit(db.GitUnitIssues) >= gitrepo.LevelWrite,
			"pull":     lv.Unit(db.GitUnitCode) >= gitrepo.LevelRead,
		},
		"releases_url": api + "/releases{/id}", "issues_url": api + "/issues{/number}", "pulls_url": api + "/pulls{/number}",
		"labels_url": api + "/labels{/name}", "comments_url": api + "/comments{/number}", "commits_url": api + "/commits{/sha}",
		"contents_url": api + "/contents/{+path}", "statuses_url": api + "/statuses/{sha}", "branches_url": api + "/branches{/branch}",
		"git_refs_url": api + "/git/refs{/sha}", "tags_url": api + "/tags", "issue_comment_url": api + "/issues/comments{/number}",
	}
	if topics, err := q.h.store.GetGitTopics(q.ctx, []string{repo.ID}); err == nil && topics[repo.ID] != nil {
		out["topics"] = topics[repo.ID]
	}
	if repo.ForkOfID != nil {
		if parent, _ := q.h.store.GetGitRepositoryByID(q.ctx, *repo.ForkOfID); parent != nil {
			out["parent"] = obj{"full_name": parent.Namespace + "/" + parent.Name, "name": parent.Name, "html_url": q.repoHTML(parent)}
		}
	}
	return out
}

// -- Releases --

func (q *request) releaseJSON(repo *db.GitRepository, rel *db.GitRelease, assets []obj) obj {
	api := q.repoAPI(repo) + "/releases/" + fmt.Sprint(numID(rel.ID))
	html := q.repoHTML(repo)
	if assets == nil {
		assets = []obj{}
	}
	return obj{
		"id": numID(rel.ID), "node_id": "", "tag_name": rel.Tag, "target_commitish": rel.TargetSHA, "name": rel.Name, "body": rel.Body,
		"draft": rel.Draft, "prerelease": rel.Prerelease, "created_at": ts(rel.CreatedAt), "published_at": tsPtr(rel.PublishedAt),
		"author": q.userByID(rel.AuthorID), "html_url": html + "/releases/" + rel.Tag, "url": api, "assets_url": api + "/assets",
		"upload_url": api + "/assets{?name,label}", "tarball_url": html + "/-/archive/" + rel.Tag + ".tar.gz",
		"zipball_url": html + "/-/archive/" + rel.Tag + ".zip", "assets": assets, "make_latest": "true",
	}
}

func (q *request) assetJSON(repo *db.GitRepository, rel *db.GitRelease, a *db.GitReleaseAsset) obj {
	api := q.repoAPI(repo) + "/releases/assets/" + fmt.Sprint(numID(a.ID))
	uploader := q.ghost()
	if a.UploaderID != "" {
		uploader = q.userByID(a.UploaderID)
	}
	return obj{
		"id": numID(a.ID), "node_id": "", "name": a.Name, "label": a.Label, "content_type": a.ContentType, "size": a.Size,
		"state": "uploaded", "download_count": a.DownloadCount, "created_at": ts(a.CreatedAt), "updated_at": ts(a.UpdatedAt),
		"browser_download_url": q.h.forge.AssetDownloadURL(q.ctx, repo, rel, a),
		"url":                  api, "uploader": uploader,
		"_distroface": obj{"asset_id": a.ID, "digest": a.Digest},
	}
}

// -- Issues and comments --

func (q *request) labelJSON(repo *db.GitRepository, l *db.GitLabel) obj {
	return obj{
		"id": numID(l.ID), "node_id": "", "name": l.Name, "color": strings.TrimPrefix(l.Color, "#"), "description": l.Description,
		"default": false, "url": q.repoAPI(repo) + "/labels/" + l.Name,
	}
}

func (q *request) issueJSON(repo *db.GitRepository, v *forge.IssueView) obj {
	is := v.Issue
	api := q.repoAPI(repo) + "/issues/" + fmt.Sprint(is.Number)
	labels := make([]obj, 0, len(v.Labels))
	for _, l := range v.Labels {
		labels = append(labels, q.labelJSON(repo, l))
	}
	assignees := make([]obj, 0, len(v.Assignees))
	for _, u := range v.Assignees {
		assignees = append(assignees, q.userJSON(u))
	}
	var assignee any
	if len(assignees) > 0 {
		assignee = assignees[0]
	}
	var closedBy any
	if v.ClosedBy != nil {
		closedBy = q.userJSON(v.ClosedBy)
	}
	out := obj{
		"id": numID(is.ID), "node_id": "", "number": is.Number, "title": is.Title, "body": is.Body, "state": is.State,
		"state_reason": nil, "locked": is.Locked, "active_lock_reason": nilIfEmpty(is.LockReason), "user": q.userJSON(v.Author),
		"labels": labels, "assignee": assignee, "assignees": assignees, "milestone": q.milestoneJSON(repo, v.Milestone),
		"comments": is.CommentCount, "created_at": ts(is.CreatedAt), "updated_at": ts(is.UpdatedAt), "closed_at": tsPtr(is.ClosedAt),
		"closed_by": closedBy, "author_association": "NONE", "html_url": q.h.forge.IssueURL(q.ctx, repo, is), "url": api,
		"comments_url": api + "/comments", "labels_url": api + "/labels{/name}", "events_url": api + "/events",
		"repository_url": q.repoAPI(repo), "draft": false,
	}
	if is.IsPull {
		pullAPI := q.repoAPI(repo) + "/pulls/" + fmt.Sprint(is.Number)
		pr := obj{"url": pullAPI, "html_url": q.h.forge.IssueURL(q.ctx, repo, is), "diff_url": pullAPI + ".diff", "patch_url": pullAPI + ".patch", "merged_at": nil}
		if v.Pull != nil {
			pr["merged_at"] = tsPtr(v.Pull.MergedAt)
			out["draft"] = v.Pull.Draft
		}
		out["pull_request"] = pr
	}
	return out
}

func (q *request) milestoneJSON(repo *db.GitRepository, ms *db.GitMilestone) any {
	if ms == nil {
		return nil
	}
	return obj{
		"id": numID(ms.ID), "node_id": "", "number": numID(ms.ID), "title": ms.Title, "description": ms.Description, "state": ms.State,
		"url": q.repoAPI(repo) + "/milestones/" + fmt.Sprint(numID(ms.ID)), "html_url": q.repoHTML(repo) + "/milestones",
		"created_at": ts(ms.CreatedAt), "due_on": tsPtr(ms.DueDate),
	}
}

func (q *request) commentJSON(repo *db.GitRepository, issue *db.GitIssue, c *db.GitComment) obj {
	api := q.repoAPI(repo) + "/issues/comments/" + fmt.Sprint(numID(c.ID))
	return obj{
		"id": numID(c.ID), "node_id": "", "body": c.Body, "user": q.userByID(c.AuthorID), "created_at": ts(c.CreatedAt),
		"updated_at": ts(c.UpdatedAt), "html_url": q.h.forge.IssueURL(q.ctx, repo, issue) + "#comment-" + c.ID, "url": api,
		"issue_url": q.repoAPI(repo) + "/issues/" + fmt.Sprint(issue.Number), "author_association": "NONE",
		"performed_via_github_app": nil, "reactions": obj{"total_count": 0},
	}
}

// -- Pull requests --

func (q *request) pullJSON(repo *db.GitRepository, v *forge.PullView) obj {
	out := q.issueJSON(repo, v.IssueView)
	pr := v.PR
	api := q.repoAPI(repo) + "/pulls/" + fmt.Sprint(v.Issue.Number)
	headRepo := repo
	if v.HeadRepo != nil {
		headRepo = v.HeadRepo
	}
	var mergeable any
	mergeableState := pr.MergeableState
	if v.Merge != nil {
		mergeableState = v.Merge.State
		switch v.Merge.State {
		case "clean", "has_hooks", "unstable":
			mergeable = true
		case "dirty", "conflicting":
			mergeable = false
		}
	}
	var mergedBy any
	if pr.MergedBy != "" {
		mergedBy = q.userByID(pr.MergedBy)
	}
	reviewers := []obj{}
	for _, rr := range v.Requests {
		if rr.UserID != "" {
			reviewers = append(reviewers, q.userByID(rr.UserID))
		}
	}
	delete(out, "pull_request")
	out["url"] = api
	out["issue_url"] = q.repoAPI(repo) + "/issues/" + fmt.Sprint(v.Issue.Number)
	out["commits_url"] = api + "/commits"
	out["review_comments_url"] = api + "/comments"
	out["review_comment_url"] = q.repoAPI(repo) + "/pulls/comments{/number}"
	out["diff_url"] = api + ".diff"
	out["patch_url"] = api + ".patch"
	out["draft"] = pr.Draft
	out["merged"] = pr.MergedAt != nil
	out["merged_at"] = tsPtr(pr.MergedAt)
	out["merged_by"] = mergedBy
	out["merge_commit_sha"] = nilIfEmpty(pr.MergeCommitSHA)
	out["mergeable"] = mergeable
	out["mergeable_state"] = mergeableState
	out["rebaseable"] = mergeable
	out["commits"] = pr.CommitCount
	out["additions"] = pr.Additions
	out["deletions"] = pr.Deletions
	out["changed_files"] = pr.ChangedFiles
	out["review_comments"] = 0
	out["maintainer_can_modify"] = pr.AllowMaintainerEdits
	out["requested_reviewers"] = reviewers
	out["requested_teams"] = []obj{}
	out["head"] = obj{"ref": pr.HeadBranch, "sha": pr.HeadSHA, "label": headRepo.Namespace + ":" + pr.HeadBranch, "user": q.ownerJSON(headRepo), "repo": q.repoJSON(headRepo)}
	out["base"] = obj{"ref": pr.BaseBranch, "sha": pr.MergeBaseSHA, "label": repo.Namespace + ":" + pr.BaseBranch, "user": q.ownerJSON(repo), "repo": q.repoJSON(repo)}
	if pr.MergedAt != nil {
		out["state"] = "closed"
	}
	return out
}

// -- Commits, refs, statuses --

func (q *request) statusJSON(repo *db.GitRepository, st *db.GitCommitStatus) obj {
	return obj{
		"id": numID(st.ID), "node_id": "", "state": st.State, "context": st.Context, "description": st.Description,
		"target_url": nilIfEmpty(st.TargetURL), "creator": q.userByID(st.CreatorID), "created_at": ts(st.CreatedAt), "updated_at": ts(st.CreatedAt),
		"url": q.repoAPI(repo) + "/statuses/" + st.SHA, "avatar_url": nil,
	}
}

func combinedState(statuses []*db.GitCommitStatus) string {
	if len(statuses) == 0 {
		return "pending"
	}
	state := "success"
	for _, st := range statuses {
		switch st.State {
		case "failure", "error":
			return "failure"
		case "pending":
			state = "pending"
		}
	}
	return state
}

func (q *request) commitJSON(repo *db.GitRepository, c *object.Commit, parents bool) obj {
	api := q.repoAPI(repo) + "/commits/" + c.Hash.String()
	html := q.repoHTML(repo) + "/commit/" + c.Hash.String()
	sig := func(s object.Signature) obj {
		return obj{"name": s.Name, "email": s.Email, "date": ts(s.When)}
	}
	var author, committer any
	if u := q.userByEmail(c.Author.Email); u != nil {
		author = q.userJSON(u)
	}
	if u := q.userByEmail(c.Committer.Email); u != nil {
		committer = q.userJSON(u)
	}
	out := obj{
		"sha": c.Hash.String(), "node_id": "", "url": api, "html_url": html, "comments_url": api + "/comments",
		"commit": obj{
			"author": sig(c.Author), "committer": sig(c.Committer), "message": c.Message, "comment_count": 0,
			"tree":         obj{"sha": c.TreeHash.String(), "url": q.repoAPI(repo) + "/git/trees/" + c.TreeHash.String()},
			"url":          q.repoAPI(repo) + "/git/commits/" + c.Hash.String(),
			"verification": obj{"verified": false, "reason": "unsigned", "signature": nil, "payload": nil},
		},
		"author": author, "committer": committer, "parents": []obj{},
	}
	if parents {
		list := make([]obj, 0, len(c.ParentHashes))
		for _, p := range c.ParentHashes {
			list = append(list, obj{"sha": p.String(), "url": q.repoAPI(repo) + "/commits/" + p.String(), "html_url": q.repoHTML(repo) + "/commit/" + p.String()})
		}
		out["parents"] = list
	}
	return out
}

func (q *request) userByEmail(email string) *db.User {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	for _, u := range q.users {
		if u != nil && u.Email != nil && strings.EqualFold(*u.Email, email) {
			return u
		}
	}
	u, _ := q.h.store.GetUserByIdentifier(q.ctx, email)
	if u != nil {
		q.users[u.ID] = u
	}
	return u
}

func (q *request) refJSON(repo *db.GitRepository, ref, objType, sha string) obj {
	api := q.repoAPI(repo)
	return obj{
		"ref": ref, "node_id": "", "url": api + "/git/" + ref,
		"object": obj{"type": objType, "sha": sha, "url": api + "/git/" + objType + "s/" + sha},
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
