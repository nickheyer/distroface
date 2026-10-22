package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeBytes(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSizeCacheTracksPacksAndLooseIncrementally(t *testing.T) {
	root := t.TempDir()
	objects := filepath.Join(root, "objects")
	writeBytes(t, filepath.Join(objects, "ab", "0001"), 100)
	writeBytes(t, filepath.Join(objects, "cd", "0002"), 50)
	writeBytes(t, filepath.Join(objects, "pack", "pack-1.pack"), 1000)
	writeBytes(t, filepath.Join(objects, "pack", "pack-1.idx"), 10)
	writeBytes(t, filepath.Join(objects, "info", "packs"), 5)

	c := newSizeCache()
	got, err := c.measure(objects, true)
	if err != nil || got != 1165 {
		t.Fatalf("full: got %d, %v", got, err)
	}
	// Age the loose dirs so an unchanged mtime is trusted
	old := time.Now().Add(-time.Minute)
	for _, d := range []string{"ab", "cd"} {
		if err := os.Chtimes(filepath.Join(objects, d), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if got, err = c.measure(objects, false); err != nil || got != 1165 {
		t.Fatalf("refresh: got %d, %v", got, err)
	}
	// A push adds a pack only, loose dirs keep their cached bytes
	writeBytes(t, filepath.Join(objects, "pack", "pack-2.pack"), 2000)
	if got, err = c.measure(objects, false); err != nil || got != 3165 {
		t.Fatalf("after pack: got %d, %v", got, err)
	}
	// A web commit touches one loose dir, only that one is rescanned
	writeBytes(t, filepath.Join(objects, "ab", "0003"), 7)
	if got, err = c.measure(objects, false); err != nil || got != 3172 {
		t.Fatalf("after loose: got %d, %v", got, err)
	}
	// Pruned objects and dropped packs disappear too
	if err := os.Remove(filepath.Join(objects, "pack", "pack-1.pack")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(objects, "cd")); err != nil {
		t.Fatal(err)
	}
	if got, err = c.measure(objects, false); err != nil || got != 2122 {
		t.Fatalf("after prune: got %d, %v", got, err)
	}
	c.forget(root)
	if len(c.repos) != 0 {
		t.Fatalf("forget kept %d entries", len(c.repos))
	}
	if got, err = c.measure(filepath.Join(root, "missing"), false); err != nil || got != 0 {
		t.Fatalf("missing: got %d, %v", got, err)
	}
}
