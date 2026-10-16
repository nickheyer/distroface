package forge

import (
	"context"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// Commits scanned per push for issue references
const pushScanLimit = 100

// Closes issues referenced by commit messages on the default branch
func (m *Manager) onPushIssues(ctx context.Context, ev gitrepo.RefEvent) {
	repo := ev.Repo
	var updates []gitrepo.RefUpdate
	for _, u := range ev.Updates {
		if u.Name.IsBranch() && !u.New.IsZero() {
			updates = append(updates, u)
		}
	}
	if len(updates) == 0 {
		return
	}
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	defer r.Close()
	actor := Actor{User: ev.Actor, CanWrite: true}
	if ev.Actor != nil {
		actor.CanWrite = m.git.Access().Level(ctx, ev.Actor, repo, db.GitUnitIssues) >= gitrepo.LevelWrite
	}
	for _, u := range updates {
		old := u.Old
		if u.Forced {
			old = plumbing.ZeroHash
		}
		commits, _, err := r.CommitsBetween(ctx, old, u.New, pushScanLimit)
		if err != nil {
			continue
		}
		onDefault := u.Name.Short() == repo.DefaultBranch
		for _, c := range commits {
			for _, ref := range ParseReferences(c.Message) {
				target, issue := m.resolveReference(ctx, repo, actor, ref)
				if issue == nil || issue.IsPull {
					continue
				}
				payload := map[string]any{"sha": c.Hash.String(), "repo": repo.Namespace + "/" + repo.Name, "branch": u.Name.Short()}
				if ref.Closes && onDefault && issue.State == "open" && target.ID == repo.ID && actor.CanWrite {
					if err := m.setState(ctx, target, actor, issue, "closed", c.Hash.String()); err == nil {
						continue
					}
				}
				_, _ = m.AddEvent(ctx, issue, ev.Actor, EventReferenced, payload)
			}
		}
	}
}
