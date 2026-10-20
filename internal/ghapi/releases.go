package ghapi

import (
	"net/http"
	"path"
	"strings"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/pages"
)

// Release assets as json rows
func (q *request) assetList(repo *db.GitRepository, rel *db.GitRelease) ([]obj, []*db.Artifact) {
	assets, pkg, err := q.h.forge.ReleaseAssets(q.ctx, repo, rel)
	if err != nil || pkg == nil {
		return []obj{}, nil
	}
	out := make([]obj, 0, len(assets))
	for _, a := range assets {
		out = append(out, q.assetJSON(repo, pkg, a))
	}
	return out, assets
}

func (q *request) releaseWithAssets(repo *db.GitRepository, rel *db.GitRelease) obj {
	assets, _ := q.assetList(repo, rel)
	return q.releaseJSON(repo, rel, assets)
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
	if !ok || !q.writable(repo, db.GitUnitReleases, rbac.ActionPush) {
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
	if !ok || !q.writable(repo, db.GitUnitReleases, rbac.ActionPush) {
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
	if !ok || !q.writable(repo, db.GitUnitReleases, rbac.ActionPush) {
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
	assets, _ := q.assetList(repo, rel)
	p := parsePage(q.r)
	q.paginate(p, int64(len(assets)))
	q.json(http.StatusOK, pageSlice(assets, p))
}

// Finds an asset by integer or row id across the release package
func (q *request) assetByPath(repo *db.GitRepository) (*db.ArtifactRepository, *db.Artifact, *db.GitRelease, bool) {
	pkg, err := q.h.forge.ReleasePackage(q.ctx, repo, false)
	if err != nil || pkg == nil {
		q.notFound()
		return nil, nil, nil, false
	}
	id := q.param("id")
	rows, _, err := q.h.store.ListArtifacts(q.ctx, pkg.ID, "", 0, 0)
	if err != nil {
		q.failErr(err)
		return nil, nil, nil, false
	}
	canWrite := q.actor(repo, db.GitUnitReleases).CanWrite
	for _, a := range rows {
		if a.Properties[forge.ReleaseAssetProperty] != "true" || !idMatches(id, a.ID) {
			continue
		}
		rel, _ := q.h.store.GetGitRelease(q.ctx, repo.ID, a.Version)
		if rel == nil || (rel.Draft && !canWrite) {
			break
		}
		return pkg, a, rel, true
	}
	q.notFound()
	return nil, nil, nil, false
}

func (q *request) getAsset() {
	repo, ok := q.repo()
	if !ok || !q.readable(repo, db.GitUnitReleases) {
		return
	}
	pkg, a, _, ok := q.assetByPath(repo)
	if !ok {
		return
	}
	q.json(http.StatusOK, q.assetJSON(repo, pkg, a))
}

// Streams the request body into the release package as a flagged asset
func (q *request) uploadAsset() {
	repo, ok := q.repo()
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
	if err := artifacts.ValidatePath(name); err != nil {
		q.validation("ReleaseAsset", "name", "invalid", err.Error())
		return
	}
	_, existing := q.assetList(repo, rel)
	for _, a := range existing {
		if a.Path == name {
			q.validation("ReleaseAsset", "name", "already_exists", "an asset with that name already exists")
			return
		}
	}
	pkg, err := q.h.forge.ReleasePackage(q.ctx, repo, true)
	if err != nil {
		q.failErr(err)
		return
	}
	blobs := q.h.artifacts.Blobs()
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		q.failErr(err)
		return
	}
	if _, err := blobs.AppendChunk(uploadID, q.r.Body); err != nil {
		_ = blobs.CancelUpload(uploadID)
		q.failErr(err)
		return
	}
	props := map[string]string{forge.ReleaseAssetProperty: "true"}
	if label := strings.TrimSpace(q.query("label")); label != "" {
		props["label"] = label
	}
	art, err := q.h.artifacts.CompleteUpload(q.ctx, pkg, uploadID, rel.Tag, name, "{}", props)
	if err != nil {
		q.failErr(err)
		return
	}
	if ct := strings.TrimSpace(q.r.Header.Get("Content-Type")); ct != "" && ct != "application/octet-stream" && art.MimeType != ct {
		art.MimeType = ct
		_ = q.h.store.UpdateArtifact(q.ctx, art)
	}
	q.json(http.StatusCreated, q.assetJSON(repo, pkg, art))
}

func (q *request) updateAsset() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rbac.ActionPush) {
		return
	}
	pkg, a, rel, ok := q.assetByPath(repo)
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
	if in.Label != nil {
		props := map[string]string{}
		for k, v := range a.Properties {
			props[k] = v
		}
		props["label"] = strings.TrimSpace(*in.Label)
		if props["label"] == "" {
			delete(props, "label")
		}
		if err := q.h.store.SetArtifactProperties(q.ctx, a.ID, props); err != nil {
			q.failErr(err)
			return
		}
		a.Properties = props
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" && *in.Name != a.Path {
		newName := path.Base(strings.TrimSpace(*in.Name))
		if err := artifacts.ValidatePath(newName); err != nil {
			q.validation("ReleaseAsset", "name", "invalid", err.Error())
			return
		}
		_, existing := q.assetList(repo, rel)
		for _, other := range existing {
			if other.Path == newName {
				q.validation("ReleaseAsset", "name", "already_exists", "an asset with that name already exists")
				return
			}
		}
		a.Path, a.Name = newName, newName
		if err := q.h.store.UpdateArtifact(q.ctx, a); err != nil {
			q.failErr(err)
			return
		}
	}
	q.json(http.StatusOK, q.assetJSON(repo, pkg, a))
}

func (q *request) deleteAsset() {
	repo, ok := q.repo()
	if !ok || !q.writable(repo, db.GitUnitReleases, rbac.ActionPush) {
		return
	}
	_, a, _, ok := q.assetByPath(repo)
	if !ok {
		return
	}
	if err := q.h.artifacts.DeleteArtifact(q.ctx, a); err != nil {
		q.failErr(err)
		return
	}
	q.json(http.StatusNoContent, nil)
}
