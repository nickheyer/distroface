package gitrepo

import (
	"context"
	"sync"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/format/packfile"
	"github.com/go-git/go-git/v6/plumbing/revlist"
	"github.com/go-git/go-git/v6/plumbing/storer"
	"github.com/nickheyer/distroface/internal/db"
)

// Packs younger than this survive a swap, a push may still be writing them
const repackPackGrace = time.Minute

// Loose objects younger than this are never pruned
const pruneObjectGrace = time.Hour

// Repacks and prunes one repository, returning the size afterwards
func (m *Manager) Maintain(ctx context.Context, repo *db.GitRepository) (int64, error) {
	unlock := m.maintenanceLock(repo.ID)
	defer unlock()
	for _, wiki := range []bool{false, true} {
		if !m.engine.Exists(repo.Namespace, repo.Name, wiki) {
			continue
		}
		if err := m.repack(ctx, repo, wiki); err != nil && ctx.Err() == nil {
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

// One maintenance run per repository at a time
func (m *Manager) maintenanceLock(id string) func() {
	mu, _ := m.maintenance.LoadOrStore(id, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// Builds the new pack without the writer lock, swaps packs under it only while refs held still
func (m *Manager) repack(ctx context.Context, repo *db.GitRepository, wiki bool) error {
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, wiki)
	if err != nil {
		return err
	}
	before := allRefs(r)
	var newPack plumbing.Hash
	var packed []plumbing.Hash
	if tips := refTargets(before); len(tips) > 0 {
		if packed, err = revlist.Objects(r.Storer, tips, nil); err == nil {
			newPack, err = writePack(r, packed)
		}
	}
	r.Close()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(packed) > 0 {
		lock := m.engine.LockRepo(repo.ID)
		r, err = m.engine.OpenRepo(repo.Namespace, repo.Name, wiki)
		if err == nil {
			if refsEqual(before, allRefs(r)) {
				err = swapPacks(r, newPack, packed)
			}
			r.Close()
		}
		lock.Unlock()
		if err != nil {
			return err
		}
	}
	r, err = m.engine.OpenRepo(repo.Namespace, repo.Name, wiki)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Repo.Prune(git.PruneOptions{OnlyObjectsOlderThan: time.Now().Add(-pruneObjectGrace), Handler: r.Repo.DeleteObject})
}

// Every hash reference, server owned refs included
func allRefs(r *Repo) map[plumbing.ReferenceName]plumbing.Hash {
	out := map[plumbing.ReferenceName]plumbing.Hash{}
	iter, err := r.Repo.References()
	if err != nil {
		return out
	}
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference {
			out[ref.Name()] = ref.Hash()
		}
		return nil
	})
	return out
}

func refTargets(refs map[plumbing.ReferenceName]plumbing.Hash) []plumbing.Hash {
	seen := map[plumbing.Hash]bool{}
	out := make([]plumbing.Hash, 0, len(refs))
	for _, h := range refs {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func refsEqual(a, b map[plumbing.ReferenceName]plumbing.Hash) bool {
	if len(a) != len(b) {
		return false
	}
	for name, h := range a {
		if b[name] != h {
			return false
		}
	}
	return true
}

// Writes one pack holding the given objects and returns its hash
func writePack(r *Repo, objects []plumbing.Hash) (plumbing.Hash, error) {
	pw, ok := r.Storer.(storer.PackfileWriter)
	if !ok {
		return plumbing.ZeroHash, git.ErrPackedObjectsNotSupported
	}
	cfg, err := r.Repo.Config()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	w, err := pw.PackfileWriter()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	h, err := packfile.NewEncoder(w, r.Storer, false).Encode(objects, cfg.Pack.Window)
	if err != nil {
		_ = w.Close()
		return plumbing.ZeroHash, err
	}
	return h, w.Close()
}

// Drops the packs the new one replaced and the loose copies it now holds
func swapPacks(r *Repo, newPack plumbing.Hash, packed []plumbing.Hash) error {
	pos, ok := r.Storer.(storer.PackedObjectStorer)
	if !ok {
		return git.ErrPackedObjectsNotSupported
	}
	packs, err := pos.ObjectPacks()
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-repackPackGrace)
	for _, h := range packs {
		if h == newPack {
			continue
		}
		if err := pos.DeleteOldObjectPackAndIndex(h, cutoff); err != nil {
			return err
		}
	}
	los, ok := r.Storer.(storer.LooseObjectStorer)
	if !ok {
		return nil
	}
	inPack := make(map[plumbing.Hash]bool, len(packed))
	for _, h := range packed {
		inPack[h] = true
	}
	return los.ForEachObjectHash(func(h plumbing.Hash) error {
		if inPack[h] {
			return los.DeleteLooseObject(h)
		}
		return nil
	})
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
