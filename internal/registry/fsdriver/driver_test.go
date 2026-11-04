package fsdriver

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	"github.com/distribution/distribution/v3/registry/storage/driver/testsuites"
)

// The upstream suite writes five gigabytes unless short, so it runs short here
func TestUpstreamDriverSuite(t *testing.T) {
	short := flag.Lookup("test.short")
	prev := short.Value.String()
	if err := flag.Set("test.short", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = flag.Set("test.short", prev) })
	root := t.TempDir()
	testsuites.Driver(t, func() (storagedriver.StorageDriver, error) {
		return New(Params{RootDirectory: root}), nil
	}, false)
}

func TestParseSync(t *testing.T) {
	for in, want := range map[string]Sync{"": SyncCommit, "commit": SyncCommit, " Always ": SyncAlways, "NEVER": SyncNever} {
		got, err := ParseSync(in)
		if err != nil || got != want {
			t.Errorf("ParseSync(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseSync("sometimes"); err == nil {
		t.Error("ParseSync accepted sometimes")
	}
}

func TestFromParametersAndFactory(t *testing.T) {
	root := t.TempDir()
	d, err := factory.Create(context.Background(), Name, map[string]any{"rootdirectory": root, "sync": "never", "maxthreads": 50})
	if err != nil {
		t.Fatal(err)
	}
	if d.Name() != Name {
		t.Errorf("Name() = %q", d.Name())
	}
	if err := d.PutContent(context.Background(), "/probe", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "probe")); err != nil {
		t.Errorf("content did not land under rootdirectory: %v", err)
	}
	for name, params := range map[string]map[string]any{
		"missing root": {"sync": "commit"},
		"bad sync":     {"rootdirectory": root, "sync": "sometimes"},
		"bad threads":  {"rootdirectory": root, "maxthreads": "lots"},
	} {
		if _, err := FromParameters(params); err == nil {
			t.Errorf("%s: accepted %v", name, params)
		}
	}
}

type syncCounter struct {
	mu    sync.Mutex
	files int
	dirs  int
}

func (c *syncCounter) hook(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if fi.IsDir() {
		c.dirs++
	} else {
		c.files++
	}
	return nil
}

func (c *syncCounter) reset() (files, dirs int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	files, dirs = c.files, c.dirs
	c.files, c.dirs = 0, 0
	return files, dirs
}

func countingDriver(t *testing.T, mode Sync) (*driver, *syncCounter) {
	t.Helper()
	c := &syncCounter{}
	d := newDriver(t.TempDir(), mode)
	d.fsync = c.hook
	return d, c
}

const (
	uploadData = "/docker/registry/v2/repositories/ns/app/_uploads/u1/data"
	uploadMeta = "/docker/registry/v2/repositories/ns/app/_uploads/u1/startedat"
	blobData   = "/docker/registry/v2/blobs/sha256/ab/abcdef/data"
	tagLink    = "/docker/registry/v2/repositories/ns/app/_manifests/tags/latest/current/link"
)

func TestSyncPolicyPerMode(t *testing.T) {
	type want struct{ files, dirs int }
	cases := []struct {
		mode                                             Sync
		uncommittedClose, commit, uploadMeta, blob, link want
	}{
		{SyncAlways, want{1, 0}, want{1, 0}, want{1, 1}, want{1, 1}, want{1, 1}},
		{SyncCommit, want{0, 0}, want{1, 0}, want{0, 0}, want{1, 1}, want{1, 1}},
		{SyncNever, want{0, 0}, want{0, 0}, want{0, 0}, want{0, 0}, want{0, 0}},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			d, c := countingDriver(t, tc.mode)
			check := func(step string, w want) {
				t.Helper()
				files, dirs := c.reset()
				if files != w.files || dirs != w.dirs {
					t.Errorf("%s synced files=%d dirs=%d, want %d/%d", step, files, dirs, w.files, w.dirs)
				}
			}
			// A chunk boundary, the upload writer closes without committing
			w, err := d.Writer(ctx, uploadData, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("chunk one ")); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			check("uncommitted close", tc.uncommittedClose)

			// The final chunk commits the blob
			w, err = d.Writer(ctx, uploadData, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("chunk two")); err != nil {
				t.Fatal(err)
			}
			if err := w.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			check("commit", tc.commit)
			if got, _ := d.GetContent(ctx, uploadData); string(got) != "chunk one chunk two" {
				t.Errorf("appended content = %q", got)
			}

			for _, step := range []struct {
				name, path string
				want       want
			}{
				{"upload bookkeeping", uploadMeta, tc.uploadMeta},
				{"blob", blobData, tc.blob},
				{"link", tagLink, tc.link},
			} {
				if err := d.PutContent(ctx, step.path, []byte(step.name)); err != nil {
					t.Fatal(err)
				}
				check(step.name, step.want)
				if got, _ := d.GetContent(ctx, step.path); string(got) != step.name {
					t.Errorf("%s content = %q", step.name, got)
				}
				entries, _ := d.List(ctx, filepath.Dir(step.path))
				for _, e := range entries {
					if strings.HasSuffix(e, ".tmp") {
						t.Errorf("%s left a temp file behind: %v", step.name, entries)
					}
				}
			}
		})
	}
}

func TestWriterTruncatesWithoutAppend(t *testing.T) {
	d := New(Params{RootDirectory: t.TempDir()})
	ctx := context.Background()
	if err := d.PutContent(ctx, "/x", []byte("a much longer payload")); err != nil {
		t.Fatal(err)
	}
	w, err := d.Writer(ctx, "/x", false)
	if err != nil {
		t.Fatal(err)
	}
	if w.Size() != 0 {
		t.Errorf("fresh writer size = %d", w.Size())
	}
	if _, err := w.Write([]byte("short")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetContent(ctx, "/x"); string(got) != "short" {
		t.Errorf("content after truncating write = %q", got)
	}
}

func seedTree(t *testing.T, d storagedriver.StorageDriver, files ...string) {
	t.Helper()
	for _, f := range files {
		if err := d.PutContent(context.Background(), f, []byte(f)); err != nil {
			t.Fatal(err)
		}
	}
}

func visited(t *testing.T, walk func(storagedriver.WalkFn) error) []string {
	t.Helper()
	var seen []string
	err := walk(func(fi storagedriver.FileInfo) error {
		tag := fi.Path()
		if fi.IsDir() {
			tag += "/"
		}
		seen = append(seen, tag)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return seen
}

var walkFiles = []string{"/a/1", "/a/2", "/a/b/3", "/a/b/c/4", "/a/d/5", "/b/6", "/c/7"}

// Every resume point yields exactly what the upstream list and stat walk yields
func TestWalkMatchesUpstreamFallback(t *testing.T) {
	d := New(Params{RootDirectory: t.TempDir()})
	seedTree(t, d, walkFiles...)
	ctx := context.Background()
	// Hints naming files are left out, upstream fails on those
	for _, from := range []string{"/", "/a", "/a/b"} {
		for _, hint := range []string{"", "/a", "/a/b", "/a/b/c", "/a/d", "/b", "/zzz", "/0", "/a/b/c/zzz"} {
			opt := storagedriver.WithStartAfterHint(hint)
			mine := visited(t, func(fn storagedriver.WalkFn) error { return d.Walk(ctx, from, fn, opt) })
			theirs := visited(t, func(fn storagedriver.WalkFn) error { return storagedriver.WalkFallback(ctx, d, from, fn, opt) })
			if strings.Join(mine, " ") != strings.Join(theirs, " ") {
				t.Errorf("from %s hint %q:\n  got  %v\n  want %v", from, hint, mine, theirs)
			}
		}
	}
}

// Resuming after a file continues with everything that sorts after it
func TestWalkResumesAfterFileHint(t *testing.T) {
	d := New(Params{RootDirectory: t.TempDir()})
	seedTree(t, d, walkFiles...)
	got := visited(t, func(fn storagedriver.WalkFn) error {
		return d.Walk(context.Background(), "/", fn, storagedriver.WithStartAfterHint("/a/b/3"))
	})
	want := "/a/b/c/ /a/b/c/4 /a/d/ /a/d/5 /b/ /b/6 /c/ /c/7"
	if strings.Join(got, " ") != want {
		t.Errorf("resumed walk = %v, want %s", got, want)
	}
}

func TestWalkSkipAndStop(t *testing.T) {
	d := New(Params{RootDirectory: t.TempDir()})
	seedTree(t, d, walkFiles...)
	ctx := context.Background()

	var seen []string
	err := d.Walk(ctx, "/", func(fi storagedriver.FileInfo) error {
		seen = append(seen, fi.Path())
		if fi.Path() == "/a/b" {
			return storagedriver.ErrSkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range seen {
		if strings.HasPrefix(p, "/a/b/") {
			t.Errorf("entered a skipped directory: %v", seen)
		}
	}

	// A filled buffer stops the walk even when resuming from a hint
	seen = nil
	err = d.Walk(ctx, "/", func(fi storagedriver.FileInfo) error {
		seen = append(seen, fi.Path())
		if len(seen) == 2 {
			return storagedriver.ErrFilledBuffer
		}
		return nil
	}, storagedriver.WithStartAfterHint("/a/b/3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Errorf("walk continued past the filled buffer: %v", seen)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = d.Walk(cancelled, "/", func(storagedriver.FileInfo) error { return nil })
	// The upstream base wrapper boxes driver errors in its own type
	var boxed storagedriver.Error
	if errors.As(err, &boxed) {
		err = boxed.Detail
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled walk returned %v", err)
	}
	var notFound storagedriver.PathNotFoundError
	if err := d.Walk(ctx, "/nope", func(storagedriver.FileInfo) error { return nil }); !errors.As(err, &notFound) {
		t.Errorf("walk of a missing root returned %v", err)
	}
}

func TestWalkStatsLazilyAndSurvivesRemovals(t *testing.T) {
	root := t.TempDir()
	d := New(Params{RootDirectory: root})
	seedTree(t, d, walkFiles...)
	ctx := context.Background()
	var seen []string
	err := d.Walk(ctx, "/", func(fi storagedriver.FileInfo) error {
		seen = append(seen, fi.Path())
		switch fi.Path() {
		case "/a/1":
			if fi.Size() != int64(len("/a/1")) || fi.ModTime().IsZero() {
				t.Errorf("file info size=%d modtime=%v", fi.Size(), fi.ModTime())
			}
		case "/a":
			if fi.Size() != 0 {
				t.Errorf("dir size = %d", fi.Size())
			}
		case "/a/b":
			// The directory vanishes before the walk descends into it
			if err := os.RemoveAll(filepath.Join(root, "a", "b")); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(seen, " ")
	if strings.Contains(joined, "/a/b/") || !strings.Contains(joined, "/a/d/5") || !strings.Contains(joined, "/c/7") {
		t.Errorf("walk after removal visited %v", seen)
	}
}

// Dotted and dashed siblings survive a resume past their neighbor
func TestWalkResumesPastDottedSiblings(t *testing.T) {
	d := New(Params{RootDirectory: t.TempDir()})
	seedTree(t, d,
		"/na/app/_manifests/x",
		"/ns/app/_manifests/x",
		"/ns/app-v2/_manifests/x",
		"/ns/app.old/_manifests/x",
		"/ns-old/app/_manifests/x",
		"/nsx/app/_manifests/x",
	)
	got := visited(t, func(fn storagedriver.WalkFn) error {
		return d.Walk(context.Background(), "/", fn, storagedriver.WithStartAfterHint("/ns/app/_manifests"))
	})
	want := "/ns/app/_manifests/x" +
		" /ns/app-v2/ /ns/app-v2/_manifests/ /ns/app-v2/_manifests/x" +
		" /ns/app.old/ /ns/app.old/_manifests/ /ns/app.old/_manifests/x" +
		" /ns-old/ /ns-old/app/ /ns-old/app/_manifests/ /ns-old/app/_manifests/x" +
		" /nsx/ /nsx/app/ /nsx/app/_manifests/ /nsx/app/_manifests/x"
	if strings.Join(got, " ") != want {
		t.Errorf("resumed walk:\n  got  %v\n  want %s", got, want)
	}
}
