package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/webhook"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Fields of a release
type ReleaseInput struct {
	Tag        string
	Target     string
	Name       *string
	Body       *string
	Draft      *bool
	Prerelease *bool
}

// Loads a release by tag
func (m *Manager) Release(ctx context.Context, repo *db.GitRepository, tag string) (*db.GitRelease, error) {
	rel, err := m.store.GetGitRelease(ctx, repo.ID, tag)
	if err != nil {
		return nil, err
	}
	if rel == nil {
		return nil, fmt.Errorf("%w: release %s", ErrNotFound, tag)
	}
	return rel, nil
}

// Assets attached to a release, by name
func (m *Manager) ReleaseAssets(ctx context.Context, rel *db.GitRelease) ([]*db.GitReleaseAsset, error) {
	return m.store.ListGitReleaseAssets(ctx, rel.ID)
}

// ReleaseAsset loads one asset of a release by row id
func (m *Manager) ReleaseAsset(ctx context.Context, rel *db.GitRelease, id string) (*db.GitReleaseAsset, error) {
	a, err := m.store.GetGitReleaseAsset(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil || a.ReleaseID != rel.ID {
		return nil, fmt.Errorf("%w: asset", ErrNotFound)
	}
	return a, nil
}

// ReleaseAssetByName loads one asset of a release by file name
func (m *Manager) ReleaseAssetByName(ctx context.Context, rel *db.GitRelease, name string) (*db.GitReleaseAsset, error) {
	a, err := m.store.GetGitReleaseAssetByName(ctx, rel.ID, name)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, fmt.Errorf("%w: asset %s", ErrNotFound, name)
	}
	return a, nil
}

// AssetDownloadURL is the browser link of an asset, GitHub shaped
func (m *Manager) AssetDownloadURL(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, a *db.GitReleaseAsset) string {
	return m.git.HTMLURL(ctx, repo) + "/releases/download/" + url.PathEscape(rel.Tag) + "/" + url.PathEscape(a.Name)
}

// AssetUploadURL is where a client streams a new asset of a release
func (m *Manager) AssetUploadURL(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease) string {
	return m.git.HTMLURL(ctx, repo) + "/-/releases/" + url.PathEscape(rel.Tag) + "/assets"
}

// UploadReleaseAsset streams one file onto a release
func (m *Manager) UploadReleaseAsset(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, name, label, contentType string, body io.Reader) (*db.GitReleaseAsset, error) {
	if !actor.CanWrite {
		return nil, ErrForbidden
	}
	if repo.IsArchived {
		return nil, gitrepo.ErrArchived
	}
	a, err := m.git.StoreReleaseAsset(ctx, repo, rel, gitrepo.ReleaseAssetInput{Name: name, Label: label, ContentType: contentType, UploaderID: actor.id(), Body: body})
	if err != nil {
		return nil, mapAssetErr(err)
	}
	m.dispatchRelease(ctx, repo, actor, rel, "edited")
	return a, nil
}

// UpdateReleaseAsset renames or relabels an asset
func (m *Manager) UpdateReleaseAsset(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, a *db.GitReleaseAsset, name, label *string) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if name != nil && strings.TrimSpace(*name) != a.Name {
		newName := strings.TrimSpace(*name)
		if err := gitrepo.ValidateAssetName(newName); err != nil {
			return mapAssetErr(err)
		}
		if other, _ := m.store.GetGitReleaseAssetByName(ctx, rel.ID, newName); other != nil {
			return fmt.Errorf("%w: an asset named %s already exists", ErrConflict, newName)
		}
		a.Name = newName
	}
	if label != nil {
		a.Label = strings.TrimSpace(*label)
	}
	if err := m.store.UpdateGitReleaseAsset(ctx, a); err != nil {
		return err
	}
	m.dispatchRelease(ctx, repo, actor, rel, "edited")
	return nil
}

// DeleteReleaseAsset removes an asset and its file
func (m *Manager) DeleteReleaseAsset(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, a *db.GitReleaseAsset) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	if err := m.git.DeleteReleaseAsset(ctx, a); err != nil {
		return err
	}
	m.dispatchRelease(ctx, repo, actor, rel, "edited")
	return nil
}

// Storage errors mapped onto the forge vocabulary
func mapAssetErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gitrepo.ErrInvalid):
		return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), gitrepo.ErrInvalid.Error()+": "))
	case errors.Is(err, gitrepo.ErrAssetTooLarge):
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return err
}

// Tag tip and tag object sha when the tag exists
func (m *Manager) tagTarget(repo *db.GitRepository, tag string) (plumbing.Hash, bool) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return plumbing.ZeroHash, false
	}
	defer r.Close()
	h := r.TagHash(tag)
	if h.IsZero() {
		return plumbing.ZeroHash, false
	}
	if t := r.TagObject(h); t != nil {
		if c, err := t.Commit(); err == nil {
			return c.Hash, true
		}
	}
	return h, true
}

// Creates a release, tagging the target when the tag does not exist yet
func (m *Manager) CreateRelease(ctx context.Context, repo *db.GitRepository, actor Actor, in ReleaseInput) (*db.GitRelease, error) {
	if !actor.CanWrite {
		return nil, ErrForbidden
	}
	if repo.IsArchived {
		return nil, gitrepo.ErrArchived
	}
	if !gitrepo.UnitEnabled(repo, db.GitUnitReleases) {
		return nil, fmt.Errorf("%w: releases are disabled on this repository", ErrForbidden)
	}
	tag := strings.TrimSpace(in.Tag)
	if tag == "" || plumbing.NewTagReferenceName(tag).Validate() != nil {
		return nil, fmt.Errorf("%w: a valid tag name is required", ErrInvalid)
	}
	if existing, _ := m.store.GetGitRelease(ctx, repo.ID, tag); existing != nil {
		return nil, fmt.Errorf("%w: release %s already exists", ErrConflict, tag)
	}
	target, exists := m.tagTarget(repo, tag)
	if !exists {
		if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
			return nil, gitrepo.ErrMirror
		}
		r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
		if err != nil {
			return nil, err
		}
		rev := in.Target
		if rev == "" {
			rev = repo.DefaultBranch
		}
		target, err = r.Resolve(rev)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: target %q not found", ErrInvalid, rev)
		}
		if err := m.git.UpdateRef(ctx, repo, actor.User, plumbing.NewTagReferenceName(tag), plumbing.ZeroHash, target, gitrepo.HookOptions{}); err != nil {
			return nil, err
		}
	}
	rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: tag, AuthorID: actor.id(), TargetSHA: target.String()}
	if in.Name != nil {
		rel.Name = strings.TrimSpace(*in.Name)
	}
	if rel.Name == "" {
		rel.Name = tag
	}
	if in.Body != nil {
		rel.Body = *in.Body
	}
	if in.Draft != nil {
		rel.Draft = *in.Draft
	}
	if in.Prerelease != nil {
		rel.Prerelease = *in.Prerelease
	}
	if !rel.Draft {
		now := time.Now()
		rel.PublishedAt = &now
	}
	if err := m.store.CreateGitRelease(ctx, rel); err != nil {
		return nil, err
	}
	if !rel.Draft {
		m.announceRelease(ctx, repo, actor, rel, "published")
	} else {
		m.dispatchRelease(ctx, repo, actor, rel, "created")
	}
	if m.releaseCreated != nil {
		m.releaseCreated(ctx, repo, rel)
	}
	return rel, nil
}

// Edits a release
func (m *Manager) UpdateRelease(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, in ReleaseInput) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if repo.IsArchived {
		return gitrepo.ErrArchived
	}
	wasDraft := rel.Draft
	if in.Name != nil {
		rel.Name = strings.TrimSpace(*in.Name)
		if rel.Name == "" {
			rel.Name = rel.Tag
		}
	}
	if in.Body != nil {
		rel.Body = *in.Body
	}
	if in.Prerelease != nil {
		rel.Prerelease = *in.Prerelease
	}
	if in.Draft != nil {
		rel.Draft = *in.Draft
		if !rel.Draft && rel.PublishedAt == nil {
			now := time.Now()
			rel.PublishedAt = &now
		}
	}
	if err := m.store.UpdateGitRelease(ctx, rel); err != nil {
		return err
	}
	if wasDraft && !rel.Draft {
		m.announceRelease(ctx, repo, actor, rel, "published")
	} else {
		m.dispatchRelease(ctx, repo, actor, rel, "edited")
	}
	return nil
}

// Publishes a draft
func (m *Manager) PublishRelease(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease) error {
	if !rel.Draft {
		return nil
	}
	f := false
	return m.UpdateRelease(ctx, repo, actor, rel, ReleaseInput{Draft: &f})
}

// Deletes a release, its asset files, and its tag when asked
func (m *Manager) DeleteRelease(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, deleteTag bool) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	if err := m.git.DeleteReleaseAssets(ctx, rel); err != nil {
		return err
	}
	if err := m.store.DeleteGitRelease(ctx, rel.ID); err != nil {
		return err
	}
	if deleteTag && repo.Type != v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
		r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
		if err == nil {
			h := r.TagHash(rel.Tag)
			r.Close()
			if !h.IsZero() {
				if err := m.git.UpdateRef(ctx, repo, actor.User, plumbing.NewTagReferenceName(rel.Tag), h, plumbing.ZeroHash, gitrepo.HookOptions{}); err != nil {
					return err
				}
			}
		}
	}
	m.dispatchRelease(ctx, repo, actor, rel, "deleted")
	return nil
}

// Image repository of the namespace the release may pin, linked here or managed by the actor
func (m *Manager) releaseImageRepo(ctx context.Context, repo *db.GitRepository, actor Actor, imageName string) (*db.Repository, error) {
	img, err := m.store.GetRepository(ctx, repo.Namespace, imageName)
	if err != nil {
		return nil, err
	}
	linked := img != nil && img.GitRepoID != nil && *img.GitRepoID == repo.ID
	if img == nil || (!linked && !m.git.CanManageImage(ctx, actor.User, img)) {
		return nil, fmt.Errorf("%w: image repository %s/%s", ErrNotFound, repo.Namespace, imageName)
	}
	return img, nil
}

// Pins an image reference to a release
func (m *Manager) AttachReleaseImage(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, imageName, tag, digest string) (*db.GitReleaseImage, error) {
	if !actor.CanWrite {
		return nil, ErrForbidden
	}
	img, err := m.releaseImageRepo(ctx, repo, actor, imageName)
	if err != nil {
		return nil, err
	}
	if tag == "" {
		return nil, fmt.Errorf("%w: an image tag is required", ErrInvalid)
	}
	row := &db.GitReleaseImage{ID: uuid.New().String(), ReleaseID: rel.ID, ImageRepoID: img.ID, Tag: tag, Digest: digest}
	if err := m.store.AttachGitReleaseImage(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

func (m *Manager) DetachReleaseImage(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, imageName, tag string) error {
	if !actor.CanWrite {
		return ErrForbidden
	}
	img, err := m.releaseImageRepo(ctx, repo, actor, imageName)
	if err != nil {
		return err
	}
	return m.store.DetachGitReleaseImage(ctx, rel.ID, img.ID, tag)
}

// Drafts release notes from commits and merged pull requests since the previous tag
func (m *Manager) GenerateReleaseNotes(ctx context.Context, repo *db.GitRepository, tag, target, previous string) (string, string, error) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return "", "", err
	}
	defer r.Close()
	head, ok := m.tagTarget(repo, tag)
	if !ok {
		rev := target
		if rev == "" {
			rev = repo.DefaultBranch
		}
		if head, err = r.Resolve(rev); err != nil {
			return "", "", fmt.Errorf("%w: target %q not found", ErrInvalid, rev)
		}
	}
	tags, _ := r.Tags()
	if previous == "" {
		names := make([]string, 0, len(tags))
		for n := range tags {
			if n != tag {
				names = append(names, n)
			}
		}
		sort.Slice(names, func(i, j int) bool { return naturalLess(names[j], names[i]) })
		for _, n := range names {
			if naturalLess(n, tag) || tag == "" {
				previous = n
				break
			}
		}
	}
	var since plumbing.Hash
	if previous != "" {
		since, _ = m.tagTarget(repo, previous)
	}
	commits, _, err := r.CommitsBetween(ctx, since, head, 500)
	if err != nil {
		return "", "", err
	}
	var b strings.Builder
	title := tag
	if title == "" {
		title = "Release notes"
	}
	fmt.Fprintf(&b, "## What's changed\n\n")
	var firstTime, lastTime time.Time
	for _, c := range commits {
		if firstTime.IsZero() || c.Committer.When.Before(firstTime) {
			firstTime = c.Committer.When
		}
		if c.Committer.When.After(lastTime) {
			lastTime = c.Committer.When
		}
	}
	if !firstTime.IsZero() {
		pulls, _ := m.store.ListMergedPullsBetween(ctx, repo.ID, firstTime.Add(-time.Minute), lastTime.Add(time.Minute))
		users := map[string]string{}
		for _, p := range pulls {
			name := users[p.AuthorID]
			if name == "" {
				name = m.usernameOf(ctx, p.AuthorID)
				users[p.AuthorID] = name
			}
			fmt.Fprintf(&b, "* %s by @%s in #%d\n", p.Title, name, p.Number)
		}
		if len(pulls) > 0 {
			b.WriteString("\n## Commits\n\n")
		}
	}
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		if c.NumParents() > 1 {
			continue
		}
		first := strings.SplitN(strings.TrimSpace(c.Message), "\n", 2)[0]
		fmt.Fprintf(&b, "* %s (%s)\n", first, c.Hash.String()[:7])
	}
	if previous != "" {
		fmt.Fprintf(&b, "\n**Full changelog**: %s/compare/%s...%s\n", m.git.HTMLURL(ctx, repo), previous, tag)
	}
	return title, b.String(), nil
}

// Natural ordering so v1.10 sorts after v1.9
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ca, cb := a[ai], b[bi]
		if ca >= '0' && ca <= '9' && cb >= '0' && cb <= '9' {
			aj, bj := ai, bi
			for aj < len(a) && a[aj] >= '0' && a[aj] <= '9' {
				aj++
			}
			for bj < len(b) && b[bj] >= '0' && b[bj] <= '9' {
				bj++
			}
			na, nb := strings.TrimLeft(a[ai:aj], "0"), strings.TrimLeft(b[bi:bj], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			ai, bi = aj, bj
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

// Page url of a release
func (m *Manager) ReleaseURL(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease) string {
	return m.git.HTMLURL(ctx, repo) + "/releases/" + rel.Tag
}

func (m *Manager) releasePayload(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease) *webhook.ReleasePayload {
	p := &webhook.ReleasePayload{Tag: rel.Tag, Name: rel.Name, Body: rel.Body, Draft: rel.Draft, Prerelease: rel.Prerelease, URL: m.ReleaseURL(ctx, repo, rel), Assets: []string{}, Images: []string{}}
	if assets, err := m.ReleaseAssets(ctx, rel); err == nil {
		for _, a := range assets {
			p.Assets = append(p.Assets, a.Name)
		}
	}
	if images, err := m.store.ListGitReleaseImages(ctx, rel.ID); err == nil {
		for _, img := range images {
			if ir, _ := m.store.GetRepositoryByID(ctx, img.ImageRepoID); ir != nil {
				p.Images = append(p.Images, ir.Namespace+"/"+ir.Name+":"+img.Tag)
			}
		}
	}
	return p
}

func (m *Manager) dispatchRelease(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, action string) {
	m.git.DispatchGit(ctx, "git_release", repo, webhook.GitPayload{Action: action, Sender: webhook.Sender{Username: actor.username()}, Release: m.releasePayload(ctx, repo, rel)})
	m.Trigger(ctx, repo, "release", action, "refs/tags/"+rel.Tag, rel.TargetSHA, actor.User, map[string]any{"release": map[string]any{
		"tag_name": rel.Tag, "name": rel.Name, "body": rel.Body, "draft": rel.Draft, "prerelease": rel.Prerelease, "html_url": m.ReleaseURL(ctx, repo, rel),
	}})
}

// Notifies watchers and fires the webhook for a published release
func (m *Manager) announceRelease(ctx context.Context, repo *db.GitRepository, actor Actor, rel *db.GitRelease, action string) {
	m.Notify(ctx, Notification{
		Repo: repo, Kind: NotifyRelease, Actor: actor.User, Reason: "released", SHA: rel.TargetSHA,
		Subject: fmt.Sprintf("[%s/%s] Release %s", repo.Namespace, repo.Name, rel.Name),
		Text:    fmt.Sprintf("%s published release %s of %s/%s.\n\n%s", actor.username(), rel.Name, repo.Namespace, repo.Name, rel.Body),
		URL:     m.ReleaseURL(ctx, repo, rel),
	})
	m.dispatchRelease(ctx, repo, actor, rel, action)
	m.recordActivity(ctx, ActivityEvent{Repo: repo, Actor: actor.User, Action: action, Release: rel})
}
