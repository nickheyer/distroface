package gitrepo

import (
	"context"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/nickheyer/distroface/internal/db"
)

// Repacks and prunes one repository, returning the size afterwards
func (m *Manager) Maintain(ctx context.Context, repo *db.GitRepository) (int64, error) {
	unlock := m.engine.LockRepo(repo.ID)
	defer unlock()
	for _, wiki := range []bool{false, true} {
		if !m.engine.Exists(repo.Namespace, repo.Name, wiki) {
			continue
		}
		r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, wiki)
		if err != nil {
			return 0, err
		}
		err = r.Repo.RepackObjects(&git.RepackConfig{UseRefDeltas: false, OnlyDeletePacksOlderThan: time.Now().Add(-time.Minute)})
		if err == nil {
			err = r.Repo.Prune(git.PruneOptions{OnlyObjectsOlderThan: time.Now().Add(-time.Hour), Handler: r.Repo.DeleteObject})
		}
		r.Close()
		if err != nil && ctx.Err() == nil {
			m.log.Error("maintenance %s/%s wiki=%v: %v", repo.Namespace, repo.Name, wiki, err)
		}
	}
	size, err := m.engine.Size(repo.Namespace, repo.Name, false)
	if err != nil {
		return 0, err
	}
	_ = m.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"size_bytes": size})
	return size, nil
}

// Runs maintenance over every repository on the configured interval
func (m *Manager) ScheduleMaintenance(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		var last time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				gc := m.res.System(ctx).GetGit().GetGc()
				if !gc.GetEnabled() {
					continue
				}
				interval := time.Duration(gc.GetIntervalHours()) * time.Hour
				if interval <= 0 {
					interval = 168 * time.Hour
				}
				if time.Since(last) < interval {
					continue
				}
				last = time.Now()
				repos, err := m.store.ListAllGitRepositories(ctx)
				if err != nil {
					m.log.Error("maintenance listing: %v", err)
					continue
				}
				for _, repo := range repos {
					if ctx.Err() != nil {
						return
					}
					if _, err := m.Maintain(ctx, repo); err != nil {
						m.log.Error("maintenance %s/%s: %v", repo.Namespace, repo.Name, err)
					}
				}
			}
		}
	}()
}
