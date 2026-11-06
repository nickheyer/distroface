package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/x/fdpool"
	"github.com/nickheyer/distroface/internal/db"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Tiny pool so dropped packs lose their descriptors at once
	e.pool = fdpool.New(1)
	return e
}

// Commits one file on main and returns the commit hash
func commitFile(t *testing.T, e *Engine, namespace, name, content string, parents ...plumbing.Hash) plumbing.Hash {
	t.Helper()
	r, err := e.OpenRepo(namespace, name, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	blob := r.Storer.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	w, err := blob.Writer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	blobHash, err := r.Storer.SetEncodedObject(blob)
	if err != nil {
		t.Fatal(err)
	}
	tree := &object.Tree{Entries: []object.TreeEntry{{Name: "file.txt", Mode: filemode.Regular, Hash: blobHash}}}
	treeObj := r.Storer.NewEncodedObject()
	if err := tree.Encode(treeObj); err != nil {
		t.Fatal(err)
	}
	treeHash, err := r.Storer.SetEncodedObject(treeObj)
	if err != nil {
		t.Fatal(err)
	}
	sig := object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(int64(len(content)), 0)}
	c := &object.Commit{Author: sig, Committer: sig, Message: content, TreeHash: treeHash, ParentHashes: parents}
	commitObj := r.Storer.NewEncodedObject()
	if err := c.Encode(commitObj); err != nil {
		t.Fatal(err)
	}
	h, err := r.Storer.SetEncodedObject(commitObj)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), h)); err != nil {
		t.Fatal(err)
	}
	return h
}

// Reads the committed file through a fresh open
func readFile(e *Engine, namespace, name string, commit plumbing.Hash) (string, error) {
	r, err := e.OpenRepo(namespace, name, false)
	if err != nil {
		return "", err
	}
	defer r.Close()
	c, err := r.Repo.CommitObject(commit)
	if err != nil {
		return "", err
	}
	f, err := c.File("file.txt")
	if err != nil {
		return "", err
	}
	return f.Contents()
}

// Ages every pack past the swap grace so a repack may drop it
func agePacks(t *testing.T, e *Engine, namespace, name string) {
	t.Helper()
	packs, err := filepath.Glob(filepath.Join(e.AbsPath(namespace, name, false), "objects", "pack", "pack-*.pack"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * repackPackGrace)
	for _, p := range packs {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

// Caches lazily backed pack objects the way request reads do
func warmCache(t *testing.T, e *Engine, namespace, name string, commit plumbing.Hash) {
	t.Helper()
	e.cache.Clear()
	if got, err := readFile(e, namespace, name, commit); err != nil || got != "one" {
		t.Fatalf("warm read: %q %v", got, err)
	}
}

func TestRepackDropsCachedObjectsOfDeletedPacks(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Init("ns", "repo", false, "main", ""); err != nil {
		t.Fatal(err)
	}
	repo := &db.GitRepository{ID: "repo", Namespace: "ns", Name: "repo"}
	m := &Manager{engine: e}
	ctx := context.Background()

	first := commitFile(t, e, "ns", "repo", "one")
	if err := m.repack(ctx, repo, false); err != nil {
		t.Fatal(err)
	}
	warmCache(t, e, "ns", "repo", first)

	commitFile(t, e, "ns", "repo", "two", first)
	agePacks(t, e, "ns", "repo")
	if err := m.repack(ctx, repo, false); err != nil {
		t.Fatalf("second repack: %v", err)
	}
	if got, err := readFile(e, "ns", "repo", first); err != nil || got != "one" {
		t.Fatalf("after second repack: %q %v", got, err)
	}
}

func TestRenameDropsCachedObjects(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Init("ns", "repo", false, "main", ""); err != nil {
		t.Fatal(err)
	}
	m := &Manager{engine: e}
	first := commitFile(t, e, "ns", "repo", "one")
	if err := m.repack(context.Background(), &db.GitRepository{ID: "repo", Namespace: "ns", Name: "repo"}, false); err != nil {
		t.Fatal(err)
	}
	warmCache(t, e, "ns", "repo", first)

	if err := e.Rename("ns", "repo", "ns", "moved"); err != nil {
		t.Fatal(err)
	}
	if got, err := readFile(e, "ns", "moved", first); err != nil || got != "one" {
		t.Fatalf("after rename: %q %v", got, err)
	}
}

func TestDeleteDropsCachedObjectsSharedWithForks(t *testing.T) {
	e := newTestEngine(t)
	m := &Manager{engine: e}
	ctx := context.Background()
	var first plumbing.Hash
	for _, name := range []string{"repo", "fork"} {
		if err := e.Init("ns", name, false, "main", ""); err != nil {
			t.Fatal(err)
		}
		h := commitFile(t, e, "ns", name, "one")
		if first.IsZero() {
			first = h
		} else if h != first {
			t.Fatalf("fork commit %s differs from %s", h, first)
		}
		if err := m.repack(ctx, &db.GitRepository{ID: name, Namespace: "ns", Name: name}, false); err != nil {
			t.Fatal(err)
		}
	}
	warmCache(t, e, "ns", "repo", first)

	if err := e.Delete("ns", "repo"); err != nil {
		t.Fatal(err)
	}
	if got, err := readFile(e, "ns", "fork", first); err != nil || got != "one" {
		t.Fatalf("fork after delete: %q %v", got, err)
	}
}
