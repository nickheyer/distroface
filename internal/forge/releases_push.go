package forge

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Drafts a release when an annotated tag lands and release_on_tag is on
func (m *Manager) onPushReleases(ctx context.Context, ev gitrepo.RefEvent) {
	repo := ev.Repo
	if !m.git.GitSettings(ctx, repo).GetReleaseOnTag() || !gitrepo.UnitEnabled(repo, db.GitUnitReleases) {
		return
	}
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	defer r.Close()
	actor := Actor{User: ev.Actor, CanWrite: true}
	for _, u := range ev.Updates {
		if !u.Name.IsTag() || u.New.IsZero() {
			continue
		}
		tag := u.Name.Short()
		if existing, _ := m.store.GetGitRelease(ctx, repo.ID, tag); existing != nil {
			continue
		}
		obj := r.TagObject(u.New)
		if obj == nil {
			continue
		}
		target := u.New
		if c, err := obj.Commit(); err == nil {
			target = c.Hash
		}
		name, body, _ := strings.Cut(strings.TrimSpace(obj.Message), "\n")
		if name == "" {
			name = tag
		}
		authorID := actor.id()
		if authorID == "" {
			authorID = repo.OwnerID
		}
		rel := &db.GitRelease{ID: uuid.New().String(), RepoID: repo.ID, Tag: tag, Name: name, Body: strings.TrimSpace(body), Draft: true, AuthorID: authorID, TargetSHA: target.String()}
		if err := m.store.CreateGitRelease(ctx, rel); err != nil {
			m.log.Debug("release draft for %s: %v", tag, err)
			continue
		}
		if _, err := m.ReleasePackage(ctx, repo, true); err != nil {
			m.log.Debug("release package for %s/%s: %v", repo.Namespace, repo.Name, err)
		}
		m.dispatchRelease(ctx, repo, actor, rel, "created")
	}
}
