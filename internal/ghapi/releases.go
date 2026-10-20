package ghapi

import (
	"net/http"
	"path"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/pages"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

// Release assets as json rows
func (q *request) assetList(repo *db.GitRepository, rel *db.GitRelease) []obj {
	assets, err := q.h.forge.ReleaseAssets(q.ctx, rel)
	if err != nil {
		return []obj{}
	}
	out := make([]obj, 0, len(assets))
	for _, a := range assets {
		out = append(out, q.assetJSON(repo, rel, a))
	}
	return out
}

func (q *request) releaseWithAssets(repo *db.GitRepository, rel *db.GitRelease) obj {
	return q.releaseJSON(repo, rel, q.assetList(repo, rel))
}

// Release named by an integer id, a row id, or a tag in the path
func (q *request) releaseByPath(repo *db.GitRepository, canWrite bool) (*db.GitRelease, bool) {
	id := q.param("id")
	var rel *db.GitRelease
	if row, _ := q.h.store.GetGitReleaseByID(q.ctx, id); row != nil && row.RepoID == repo.ID {
		rel = row
	}
	if rel == nil {
		rows, _, err := q.h.store.ListGitReleases(q.ctx, repo.ID, true, true, pages.Query{}, 0, 0)
		if err != nil {
			q.failErr(err)
			return nil, false
		}
		for _, row := range rows {
			if idMatches(id, row.ID) || row.Tag == id {
				rel = row
				break
			}
		}
	}
	if rel == nil || (rel.Draft && !canWrite) {
		q.notFound()
		return nil, false
	}
	return rel, true
}

func (q *request) listReleases() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	p := parsePage(q.r)
	canWrite := q.actor(repo, db.GitUnitReleases).CanWrite
	rows, total, err := q.h.store.ListGitReleases(q.ctx, repo.ID, canWrite, true, pages.Query{}, p.perPage, p.offset())
	if err != nil {
		q.failErr(err)
		return
	}
	out := make([]obj, 0, len(rows))
	for _, rel := range rows {
		out = append(out, q.releaseWithAssets(repo, rel))
	}
	q.paginate(p, total)
	q.json(http.StatusOK, out)
}

func (q *request) latestRelease() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	rel, err := q.h.store.LatestGitRelease(q.ctx, repo.ID)
	if err != nil {
		q.failErr(err)
		return
	}
	if rel == nil {
		q.notFound()
		return
	}
	q.json(http.StatusOK, q.releaseWithAssets(repo, rel))
}

func (q *request) releaseByTag() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	rel, err := q.h.forge.Release(q.ctx, repo, q.param("tag"))
	if err != nil {
		q.failErr(err)
		return
	}
	if rel.Draft && !q.actor(repo, db.GitUnitReleases).CanWrite {
		q.notFound()
		return
	}
	q.json(http.StatusOK, q.releaseWithAssets(repo, rel))
}

func (q *request) getRelease() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	rel, ok := q.releaseByPath(repo, q.actor(repo, db.GitUnitReleases).CanWrite)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.releaseWithAssets(repo, rel))
}

type releaseBody struct {
	TagName         string  `json:"tag_name"`
	TargetCommitish string  `json:"target_commitish"`
	Name            *string `json:"name"`
	Body            *string `json:"body"`
	Draft           *bool   `json:"draft"`
	Prerelease      *bool   `json:"prerelease"`
}

func (q *request) createRelease() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rpcAction(distrofacev1connect.ReleaseServiceCreateReleaseProcedure)) {
		return
	}
	var in releaseBody
	if !q.decode(&in) {
		return
	}
	if strings.TrimSpace(in.TagName) == "" {
		q.validation("Release", "tag_name", "missing_field", "tag_name is required")
		return
	}
	rel, err := q.h.forge.CreateRelease(q.ctx, repo, q.actor(repo, db.GitUnitReleases), forge.ReleaseInput{
		Tag: in.TagName, Target: in.TargetCommitish, Name: in.Name, Body: in.Body, Draft: in.Draft, Prerelease: in.Prerelease,
	})
	if err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusCreated, q.releaseWithAssets(repo, rel))
}

func (q *request) updateRelease() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rpcAction(distrofacev1connect.ReleaseServiceUpdateReleaseProcedure)) {
		return
	}
	rel, ok := q.releaseByPath(repo, true)
	if !ok {
		return
	}
	var in releaseBody
	if !q.decode(&in) {
		return
	}
	if err := q.h.forge.UpdateRelease(q.ctx, repo, q.actor(repo, db.GitUnitReleases), rel, forge.ReleaseInput{Name: in.Name, Body: in.Body, Draft: in.Draft, Prerelease: in.Prerelease}); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusOK, q.releaseWithAssets(repo, rel))
}

func (q *request) deleteRelease() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rpcAction(distrofacev1connect.ReleaseServiceDeleteReleaseProcedure)) {
		return
	}
	rel, ok := q.releaseByPath(repo, true)
	if !ok {
		return
	}
	if err := q.h.forge.DeleteRelease(q.ctx, repo, q.actor(repo, db.GitUnitReleases), rel, false); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusNoContent, nil)
}

// -- Assets --

func (q *request) listAssets() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	rel, ok := q.releaseByPath(repo, q.actor(repo, db.GitUnitReleases).CanWrite)
	if !ok {
		return
	}
	assets := q.assetList(repo, rel)
	p := parsePage(q.r)
	q.paginate(p, int64(len(assets)))
	q.json(http.StatusOK, pageSlice(assets, p))
}

// Finds an asset by integer or row id across the repository's releases
func (q *request) assetByPath(repo *db.GitRepository) (*db.GitReleaseAsset, *db.GitRelease, bool) {
	id := q.param("id")
	rows, err := q.h.store.ListGitReleaseAssetsByRepo(q.ctx, repo.ID)
	if err != nil {
		q.failErr(err)
		return nil, nil, false
	}
	canWrite := q.actor(repo, db.GitUnitReleases).CanWrite
	for _, a := range rows {
		if !idMatches(id, a.ID) {
			continue
		}
		rel, _ := q.h.store.GetGitReleaseByID(q.ctx, a.ReleaseID)
		if rel == nil || rel.RepoID != repo.ID || (rel.Draft && !canWrite) {
			break
		}
		return a, rel, true
	}
	q.notFound()
	return nil, nil, false
}

func (q *request) getAsset() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	a, rel, ok := q.assetByPath(repo)
	if !ok {
		return
	}
	// Octet stream callers get the file, the redirect GitHub answers with
	if strings.Contains(q.r.Header.Get("Accept"), "application/octet-stream") {
		http.Redirect(q.w, q.r, q.h.forge.AssetDownloadURL(q.ctx, repo, rel, a), http.StatusFound)
		return
	}
	q.json(http.StatusOK, q.assetJSON(repo, rel, a))
}

// Streams the body onto the release the way uploads.github.com does
func (q *request) uploadAsset() {
	repo, ok := q.repo()
	// Asset upload has no rpc twin, it is a push
	if !ok || !q.writable(repo, db.GitUnitReleases, rbac.ActionPush) {
		return
	}
	rel, ok := q.releaseByPath(repo, true)
	if !ok {
		return
	}
	name := path.Base(strings.TrimSpace(q.query("name")))
	if name == "" || name == "." || name == "/" {
		q.validation("ReleaseAsset", "name", "missing_field", "name is required")
		return
	}
	if err := gitrepo.ValidateAssetName(name); err != nil {
		q.validation("ReleaseAsset", "name", "invalid", err.Error())
		return
	}
	if existing, _ := q.h.store.GetGitReleaseAssetByName(q.ctx, rel.ID, name); existing != nil {
		q.validation("ReleaseAsset", "name", "already_exists", "an asset with that name already exists")
		return
	}
	a, err := q.h.forge.UploadReleaseAsset(q.ctx, repo, q.actor(repo, db.GitUnitReleases), rel, name, strings.TrimSpace(q.query("label")), q.r.Header.Get("Content-Type"), q.r.Body)
	if err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusCreated, q.assetJSON(repo, rel, a))
}

func (q *request) updateAsset() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rpcAction(distrofacev1connect.ReleaseServiceUpdateReleaseAssetProcedure)) {
		return
	}
	a, rel, ok := q.assetByPath(repo)
	if !ok {
		return
	}
	var in struct {
		Name  *string `json:"name"`
		Label *string `json:"label"`
	}
	if !q.decode(&in) {
		return
	}
	if in.Name != nil {
		trimmed := path.Base(strings.TrimSpace(*in.Name))
		in.Name = &trimmed
	}
	if err := q.h.forge.UpdateReleaseAsset(q.ctx, repo, q.actor(repo, db.GitUnitReleases), rel, a, in.Name, in.Label); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusOK, q.assetJSON(repo, rel, a))
}

func (q *request) deleteAsset() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rpcAction(distrofacev1connect.ReleaseServiceDeleteReleaseAssetProcedure)) {
		return
	}
	a, rel, ok := q.assetByPath(repo)
	if !ok {
		return
	}
	if err := q.h.forge.DeleteReleaseAsset(q.ctx, repo, q.actor(repo, db.GitUnitReleases), rel, a); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusNoContent, nil)
}
