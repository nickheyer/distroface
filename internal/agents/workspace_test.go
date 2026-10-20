package agents

import (
	"strings"
	"testing"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
)

func TestCleanPath(t *testing.T) {
	good := map[string]string{"./a/b.go": "a/b.go", "/a/b.go": "a/b.go", "a//b.go": "a/b.go"}
	for in, want := range good {
		got, err := cleanPath(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q (%v), want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "..", "../x", ".git/config", ".git"} {
		if _, err := cleanPath(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestWorkspaceOverlayWithoutRepo(t *testing.T) {
	ws := newWorkspace(nil, "agents/x/1", "main", plumbing.ZeroHash)
	if err := ws.write(nil, "docs/new.md", []byte("hello\nworld\n")); err != nil {
		t.Fatal(err)
	}
	if err := ws.edit(nil, "docs/new.md", "world", "there"); err != nil {
		t.Fatal(err)
	}
	content, mode, err := ws.read(nil, "docs/new.md")
	if err != nil || string(content) != "hello\nthere\n" || mode != filemode.Regular {
		t.Fatalf("read %q %v %v", content, mode, err)
	}
	if err := ws.edit(nil, "docs/new.md", "missing", "x"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := ws.rename(nil, "docs/new.md", "docs/moved.md"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.read(nil, "docs/new.md"); err == nil {
		t.Fatal("renamed source must be gone")
	}
	edits := ws.treeEdits()
	if len(edits) != 2 || edits[0].Path != "docs/moved.md" || edits[1].Path != "docs/new.md" || edits[1].Content != nil {
		t.Fatalf("edits %+v", edits)
	}
	if err := ws.remove(nil, "docs/moved.md"); err != nil {
		t.Fatal(err)
	}
	if len(ws.pendingPaths()) != 2 {
		t.Fatalf("pending %v", ws.pendingPaths())
	}
}

func TestRenderUnified(t *testing.T) {
	out := renderUnified("a\nb\nc\n", "a\nB\nc\n")
	if !strings.Contains(out, "-b") || !strings.Contains(out, "+B") || !strings.Contains(out, " a") {
		t.Fatalf("diff %q", out)
	}
}
