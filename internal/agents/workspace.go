package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// One uncommitted change
type pendingEdit struct {
	content []byte
	delete  bool
	mode    filemode.FileMode
}

// Overlay of uncommitted edits on top of the working branch tip
type workspace struct {
	repo *db.GitRepository
	// Branch the session commits to and the base it was cut from
	branch     string
	baseBranch string
	// Tip the edits apply on, zero before the first resolve
	head  plumbing.Hash
	edits map[string]*pendingEdit
	// Every commit the session created
	commits []string
}

func newWorkspace(repo *db.GitRepository, branch, baseBranch string, head plumbing.Hash) *workspace {
	return &workspace{repo: repo, branch: branch, baseBranch: baseBranch, head: head, edits: map[string]*pendingEdit{}}
}

func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	for _, seg := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: invalid path", ErrInvalid)
		}
	}
	p = strings.TrimPrefix(p, "./")
	p = pathpkg.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." || strings.HasPrefix(p, "../") || strings.Contains(p, "\x00") {
		return "", fmt.Errorf("%w: invalid path", ErrInvalid)
	}
	if strings.HasPrefix(p, ".git/") || p == ".git" {
		return "", fmt.Errorf("%w: the .git directory is off limits", ErrInvalid)
	}
	return p, nil
}

// Reads a file through the overlay
func (w *workspace) read(r *gitrepo.Repo, p string) ([]byte, filemode.FileMode, error) {
	p, err := cleanPath(p)
	if err != nil {
		return nil, 0, err
	}
	if e, ok := w.edits[p]; ok {
		if e.delete {
			return nil, 0, fmt.Errorf("%w: %s was deleted in this session", gitrepo.ErrNotFound, p)
		}
		return e.content, e.mode, nil
	}
	if w.head.IsZero() {
		return nil, 0, fmt.Errorf("%w: %s", gitrepo.ErrNotFound, p)
	}
	blob, err := r.Blob(w.head, p, 0)
	if err != nil {
		return nil, 0, err
	}
	if blob.IsBinary {
		return nil, 0, fmt.Errorf("%w: %s is binary", ErrInvalid, p)
	}
	return blob.Content, blob.Mode, nil
}

// Whether a path exists through the overlay
func (w *workspace) exists(r *gitrepo.Repo, p string) bool {
	if e, ok := w.edits[p]; ok {
		return !e.delete
	}
	if w.head.IsZero() {
		return false
	}
	_, err := r.Blob(w.head, p, 1)
	return err == nil
}

// Writes a whole file into the overlay
func (w *workspace) write(r *gitrepo.Repo, p string, content []byte) error {
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	mode := filemode.Regular
	if e, ok := w.edits[p]; ok && !e.delete && e.mode != 0 {
		mode = e.mode
	} else if !w.head.IsZero() {
		if blob, err := r.Blob(w.head, p, 1); err == nil && blob.Mode == filemode.Executable {
			mode = filemode.Executable
		}
	}
	w.edits[p] = &pendingEdit{content: content, mode: mode}
	return nil
}

// Replaces one occurrence of old text, the text must be unique
func (w *workspace) edit(r *gitrepo.Repo, p, oldText, newText string) error {
	current, mode, err := w.read(r, p)
	if err != nil {
		return err
	}
	if oldText == "" {
		return fmt.Errorf("%w: old_text is required, use write_file to create or replace a file", ErrInvalid)
	}
	text := string(current)
	count := strings.Count(text, oldText)
	if count == 0 {
		// Tolerate trailing whitespace drift on each line
		normalized := normalizeWS(text)
		needle := normalizeWS(oldText)
		if strings.Count(normalized, needle) == 1 {
			idx := strings.Index(normalized, needle)
			text = normalized[:idx] + newText + normalized[idx+len(needle):]
			w.edits[p] = &pendingEdit{content: []byte(text), mode: mode}
			return nil
		}
		return fmt.Errorf("%w: old_text was not found in %s, read the file and copy the exact text", ErrInvalid, p)
	}
	if count > 1 {
		return fmt.Errorf("%w: old_text matches %d places in %s, include more surrounding lines", ErrInvalid, count, p)
	}
	text = strings.Replace(text, oldText, newText, 1)
	w.edits[p] = &pendingEdit{content: []byte(text), mode: mode}
	return nil
}

func normalizeWS(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}

func (w *workspace) remove(r *gitrepo.Repo, p string) error {
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	if !w.exists(r, p) {
		return fmt.Errorf("%w: %s", gitrepo.ErrNotFound, p)
	}
	w.edits[p] = &pendingEdit{delete: true}
	return nil
}

func (w *workspace) rename(r *gitrepo.Repo, from, to string) error {
	content, mode, err := w.read(r, from)
	if err != nil {
		return err
	}
	to, err = cleanPath(to)
	if err != nil {
		return err
	}
	if w.exists(r, to) {
		return fmt.Errorf("%w: %s already exists", ErrConflict, to)
	}
	from, _ = cleanPath(from)
	w.edits[from] = &pendingEdit{delete: true}
	w.edits[to] = &pendingEdit{content: content, mode: mode}
	return nil
}

// Paths with uncommitted changes, sorted
func (w *workspace) pendingPaths() []string {
	out := make([]string, 0, len(w.edits))
	for p := range w.edits {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Tree edits for a commit, nil content deletes
func (w *workspace) treeEdits() []gitrepo.TreeEdit {
	var out []gitrepo.TreeEdit
	for _, p := range w.pendingPaths() {
		e := w.edits[p]
		if e.delete {
			out = append(out, gitrepo.TreeEdit{Path: p})
			continue
		}
		mode := e.mode
		if mode == 0 {
			mode = filemode.Regular
		}
		out = append(out, gitrepo.TreeEdit{Path: p, Mode: mode, Content: bytes.NewReader(e.content)})
	}
	return out
}

// Lists a directory through the overlay
func (w *workspace) list(ctx context.Context, r *gitrepo.Repo, dir string, recursive bool) ([]string, error) {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "." {
		dir = ""
	}
	seen := map[string]bool{}
	var out []string
	if !w.head.IsZero() {
		entries, _, err := r.Tree(ctx, w.head, dir, recursive)
		if err != nil && !errors.Is(err, gitrepo.ErrNotFound) {
			return nil, err
		}
		for _, e := range entries {
			name := e.Path
			if !e.Mode.IsFile() && e.Mode != filemode.Symlink && e.Mode != filemode.Submodule {
				name += "/"
			}
			if ed, ok := w.edits[e.Path]; ok && ed.delete {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	for p, e := range w.edits {
		if e.delete {
			continue
		}
		if dir != "" && !strings.HasPrefix(p, dir+"/") {
			continue
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if dir == "" {
			rel = p
		}
		if !recursive && strings.Contains(rel, "/") {
			d := rel[:strings.Index(rel, "/")] + "/"
			full := d
			if dir != "" {
				full = dir + "/" + d
			}
			if !seen[full] {
				seen[full] = true
				out = append(out, full)
			}
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}
