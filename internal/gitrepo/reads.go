package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-enry/go-enry/v2"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
	"github.com/go-git/go-git/v6/storage"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Inline blob cap for rpc answers
const MaxInlineBlob = 1 << 20

// Returned when a ref or path does not resolve
var ErrNotFound = errors.New("not found")

// Returned when a repository has no commits yet
var ErrEmptyRepo = errors.New("repository is empty")

// Open repository with its storer and closer
type Repo struct {
	Repo   *git.Repository
	Storer storage.Storer
	close  func()
}

// Releases pack descriptors
func (r *Repo) Close() {
	if r != nil && r.close != nil {
		r.close()
	}
}

// Opens a repository for reads and writes
func (e *Engine) OpenRepo(namespace, name string, wiki bool) (*Repo, error) {
	st, closer, err := e.Storer(namespace, name, wiki)
	if err != nil {
		return nil, err
	}
	repo, err := git.Open(st, nil)
	if err != nil {
		closer()
		return nil, err
	}
	return &Repo{Repo: repo, Storer: st, close: closer}, nil
}

// Reports whether HEAD resolves to a commit
func (r *Repo) IsEmpty() bool {
	_, err := r.Repo.Head()
	return err != nil
}

// Resolves a branch, tag, or sha to a commit hash
func (r *Repo) Resolve(rev string) (plumbing.Hash, error) {
	if rev == "" {
		head, err := r.Repo.Head()
		if err != nil {
			return plumbing.ZeroHash, ErrEmptyRepo
		}
		return head.Hash(), nil
	}
	// Branches win over tags, both win over abbreviated hashes
	for _, name := range []plumbing.ReferenceName{plumbing.NewBranchReferenceName(rev), plumbing.NewTagReferenceName(rev)} {
		if ref, err := r.Repo.Reference(name, true); err == nil {
			return r.peelToCommit(ref.Hash())
		}
	}
	if plumbing.IsHash(rev) {
		return r.peelToCommit(plumbing.NewHash(rev))
	}
	h, err := r.Repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil || h == nil {
		return plumbing.ZeroHash, ErrNotFound
	}
	return r.peelToCommit(*h)
}

// Follows annotated tags down to the commit
func (r *Repo) peelToCommit(h plumbing.Hash) (plumbing.Hash, error) {
	for range 8 {
		obj, err := r.Storer.EncodedObject(plumbing.AnyObject, h)
		if err != nil {
			return plumbing.ZeroHash, ErrNotFound
		}
		switch obj.Type() {
		case plumbing.CommitObject:
			return h, nil
		case plumbing.TagObject:
			tag, err := object.DecodeTag(r.Storer, obj)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			h = tag.Target
		default:
			return plumbing.ZeroHash, fmt.Errorf("%w: %s is not a commit", ErrNotFound, h)
		}
	}
	return plumbing.ZeroHash, ErrNotFound
}

// Commit for a hash
func (r *Repo) Commit(h plumbing.Hash) (*object.Commit, error) {
	c, err := r.Repo.CommitObject(h)
	if err != nil {
		return nil, ErrNotFound
	}
	return c, nil
}

// Default branch name of the repository from HEAD, empty when unborn
func (r *Repo) HeadBranch() string {
	ref, err := r.Storer.Reference(plumbing.HEAD)
	if err != nil || ref.Type() != plumbing.SymbolicReference {
		return ""
	}
	return ref.Target().Short()
}

// Points HEAD at a branch
func (r *Repo) SetHead(branch string) error {
	return r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branch)))
}

// Branch tips keyed by short name
func (r *Repo) Branches() (map[string]plumbing.Hash, error) {
	out := map[string]plumbing.Hash{}
	iter, err := r.Repo.Branches()
	if err != nil {
		return out, nil
	}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		out[ref.Name().Short()] = ref.Hash()
		return nil
	})
	return out, err
}

// Tag refs keyed by short name, hash is the tag or commit object
func (r *Repo) Tags() (map[string]plumbing.Hash, error) {
	out := map[string]plumbing.Hash{}
	iter, err := r.Repo.Tags()
	if err != nil {
		return out, nil
	}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		out[ref.Name().Short()] = ref.Hash()
		return nil
	})
	return out, err
}

// Counts of branches and tags
func (r *Repo) RefCounts() (int32, int32) {
	b, _ := r.Branches()
	t, _ := r.Tags()
	return int32(len(b)), int32(len(t))
}

// Commits ahead and behind between two commits through the merge base
func (r *Repo) AheadBehind(ctx context.Context, a, b plumbing.Hash, limit int) (int32, int32, error) {
	if a == b {
		return 0, 0, nil
	}
	ca, err := r.Commit(a)
	if err != nil {
		return 0, 0, err
	}
	cb, err := r.Commit(b)
	if err != nil {
		return 0, 0, err
	}
	bases, err := ca.MergeBase(cb)
	if err != nil {
		return 0, 0, err
	}
	var base []plumbing.Hash
	for _, c := range bases {
		base = append(base, c.Hash)
	}
	count := func(from plumbing.Hash) (int32, error) {
		n := int32(0)
		iter := object.NewCommitPreorderIter(mustCommit(r, from), nil, base)
		err := iter.ForEach(func(c *object.Commit) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n++
			if limit > 0 && int(n) >= limit {
				return storer.ErrStop
			}
			return nil
		})
		return n, err
	}
	ahead, err := count(a)
	if err != nil {
		return 0, 0, err
	}
	behind, err := count(b)
	return ahead, behind, err
}

func mustCommit(r *Repo, h plumbing.Hash) *object.Commit {
	c, err := r.Commit(h)
	if err != nil {
		return &object.Commit{Hash: h}
	}
	return c
}

// Every merge base of two commits, empty when histories are unrelated
func (r *Repo) MergeBases(a, b plumbing.Hash) ([]plumbing.Hash, error) {
	ca, err := r.Commit(a)
	if err != nil {
		return nil, err
	}
	cb, err := r.Commit(b)
	if err != nil {
		return nil, err
	}
	bases, err := ca.MergeBase(cb)
	if err != nil {
		return nil, err
	}
	out := make([]plumbing.Hash, 0, len(bases))
	for _, c := range bases {
		out = append(out, c.Hash)
	}
	return out, nil
}

// Merge base of two commits, zero when histories are unrelated
func (r *Repo) MergeBase(a, b plumbing.Hash) (plumbing.Hash, error) {
	ca, err := r.Commit(a)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	cb, err := r.Commit(b)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	bases, err := ca.MergeBase(cb)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if len(bases) == 0 {
		return plumbing.ZeroHash, nil
	}
	return bases[0].Hash, nil
}

// Reports whether ancestor reaches descendant
func (r *Repo) IsAncestor(ancestor, descendant plumbing.Hash) (bool, error) {
	if ancestor == descendant {
		return true, nil
	}
	ca, err := r.Commit(ancestor)
	if err != nil {
		return false, err
	}
	cd, err := r.Commit(descendant)
	if err != nil {
		return false, err
	}
	return ca.IsAncestor(cd)
}

// Commits reachable from new and not from old, newest first, capped
func (r *Repo) CommitsBetween(ctx context.Context, old, new plumbing.Hash, limit int) ([]*object.Commit, bool, error) {
	if new.IsZero() {
		return nil, false, nil
	}
	head, err := r.Commit(new)
	if err != nil {
		return nil, false, err
	}
	var ignore []plumbing.Hash
	if !old.IsZero() {
		if _, err := r.Commit(old); err == nil {
			ignore = []plumbing.Hash{old}
		}
	}
	var out []*object.Commit
	truncated := false
	iter := object.NewCommitPreorderIter(head, nil, ignore)
	err = iter.ForEach(func(c *object.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if limit > 0 && len(out) >= limit {
			truncated = true
			return storer.ErrStop
		}
		out = append(out, c)
		return nil
	})
	return out, truncated, err
}

var commitRanges = NewMemo[[]plumbing.Hash](time.Hour, 1024)

// Commits reachable from new and not from old, oldest first, cached per pair
func (r *Repo) CommitRange(ctx context.Context, old, new plumbing.Hash) ([]plumbing.Hash, error) {
	key := ":" + new.String()
	if _, err := r.Commit(old); err == nil {
		key = old.String() + key
	}
	return commitRanges.Get(ctx, key, func() ([]plumbing.Hash, error) {
		commits, _, err := r.CommitsBetween(ctx, old, new, 0)
		if err != nil {
			return nil, err
		}
		out := make([]plumbing.Hash, len(commits))
		for i, c := range commits {
			out[len(commits)-1-i] = c.Hash
		}
		return out, nil
	})
}

// Log options for one listing
type LogQuery struct {
	From   plumbing.Hash
	Path   string
	Author string
	Since  *time.Time
	Until  *time.Time
	Cursor plumbing.Hash
	Limit  int
}

// Pages history, returning the next cursor sha when more remains
func (r *Repo) Log(ctx context.Context, q LogQuery) ([]*object.Commit, string, error) {
	opts := &git.LogOptions{From: q.From, Order: git.LogOrderCommitterTime, Since: q.Since, Until: q.Until}
	if !q.Cursor.IsZero() {
		opts.From = q.Cursor
	}
	if q.Path != "" {
		prefix := strings.TrimSuffix(q.Path, "/")
		opts.PathFilter = func(p string) bool {
			return p == prefix || strings.HasPrefix(p, prefix+"/")
		}
	}
	iter, err := r.Repo.Log(opts)
	if err != nil {
		return nil, "", err
	}
	defer iter.Close()
	author := strings.ToLower(q.Author)
	var out []*object.Commit
	next := ""
	skipCursor := !q.Cursor.IsZero()
	err = iter.ForEach(func(c *object.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if skipCursor {
			skipCursor = false
			return nil
		}
		if author != "" && !strings.Contains(strings.ToLower(c.Author.Name), author) && !strings.Contains(strings.ToLower(c.Author.Email), author) {
			return nil
		}
		if q.Limit > 0 && len(out) >= q.Limit {
			next = out[len(out)-1].Hash.String()
			return storer.ErrStop
		}
		out = append(out, c)
		return nil
	})
	return out, next, err
}

// Directory listing entry with resolved size
type Entry struct {
	Name        string
	Path        string
	Mode        filemode.FileMode
	Hash        plumbing.Hash
	Size        int64
	IsSubmodule bool
	IsLFS       bool
}

// Entries a single listing may return before it is cut short
const MaxTreeEntries = 100000

// Listing stopped at the entry cap
var errTreeCapped = errors.New("tree listing capped")

// Lists one directory of a commit's tree, true when the entry cap cut it short
func (r *Repo) Tree(ctx context.Context, commit plumbing.Hash, dir string, recursive bool) ([]Entry, bool, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, false, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, false, err
	}
	dir = strings.Trim(dir, "/")
	if dir != "" {
		tree, err = tree.Tree(dir)
		if err != nil {
			return nil, false, ErrNotFound
		}
	}
	var out []Entry
	var walk func(t *object.Tree, prefix string) error
	walk = func(t *object.Tree, prefix string) error {
		for _, te := range t.Entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if len(out) >= MaxTreeEntries {
				return errTreeCapped
			}
			p := te.Name
			if prefix != "" {
				p = prefix + "/" + te.Name
			}
			e := Entry{Name: te.Name, Path: p, Mode: te.Mode, Hash: te.Hash, IsSubmodule: te.Mode == filemode.Submodule}
			if te.Mode.IsFile() {
				if size, err := r.Storer.EncodedObjectSize(te.Hash); err == nil {
					e.Size = size
					if size < 512 {
						if blob, err := r.Repo.BlobObject(te.Hash); err == nil {
							e.IsLFS = isLFSPointer(blobBytes(blob))
						}
					}
				}
			}
			out = append(out, e)
			if recursive && te.Mode == filemode.Dir {
				sub, err := r.Repo.TreeObject(te.Hash)
				if err == nil {
					if err := walk(sub, p); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	truncated := false
	if err := walk(tree, dir); err != nil {
		if !errors.Is(err, errTreeCapped) {
			return nil, false, err
		}
		truncated = true
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Mode == filemode.Dir, out[j].Mode == filemode.Dir
		if di != dj {
			return di
		}
		return out[i].Path < out[j].Path
	})
	return out, truncated, nil
}

// File content and metadata at a path
type Blob struct {
	Entry
	Content     []byte
	IsBinary    bool
	Truncated   bool
	ContentType string
	LFS         *LFSPointer
}

// Reads one file of a commit, capped for inline answers
func (r *Repo) Blob(commit plumbing.Hash, p string, maxBytes int64) (*Blob, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	p = strings.Trim(p, "/")
	entry, err := tree.FindEntry(p)
	if err != nil {
		return nil, ErrNotFound
	}
	if !entry.Mode.IsFile() && entry.Mode != filemode.Symlink {
		return nil, fmt.Errorf("%w: %s is not a file", ErrNotFound, p)
	}
	blob, err := r.Repo.BlobObject(entry.Hash)
	if err != nil {
		return nil, ErrNotFound
	}
	out := &Blob{Entry: Entry{Name: path.Base(p), Path: p, Mode: entry.Mode, Hash: entry.Hash, Size: blob.Size}}
	rd, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	limit := blob.Size
	if maxBytes > 0 && limit > maxBytes {
		limit = maxBytes
		out.Truncated = true
	}
	buf, err := io.ReadAll(io.LimitReader(rd, limit))
	if err != nil {
		return nil, err
	}
	out.Content = buf
	out.IsBinary = enry.IsBinary(buf)
	out.ContentType = sniffContentType(p, buf)
	if ptr := ParseLFSPointer(buf); ptr != nil {
		out.LFS = ptr
		out.IsLFS = true
	}
	return out, nil
}

// Opens a blob reader for streaming raw downloads
func (r *Repo) OpenBlob(commit plumbing.Hash, p string) (*object.File, io.ReadCloser, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, nil, err
	}
	f, err := tree.File(strings.Trim(p, "/"))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	rd, err := f.Reader()
	if err != nil {
		return nil, nil, err
	}
	return f, rd, nil
}

func blobBytes(b *object.Blob) []byte {
	rd, err := b.Reader()
	if err != nil {
		return nil
	}
	defer rd.Close()
	buf, _ := io.ReadAll(io.LimitReader(rd, 1024))
	return buf
}

// Content type from the extension first, then the bytes
func sniffContentType(p string, buf []byte) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".txt", ".go", ".rs", ".py", ".js", ".ts", ".yaml", ".yml", ".toml", ".sh", ".c", ".h", ".cpp", ".java", ".rb", ".css", ".html", ".xml", ".proto", ".sql", ".svelte":
		return "text/plain; charset=utf-8"
	}
	return http.DetectContentType(buf)
}

// Readme candidates in priority order
var readmeNames = []string{"README.md", "README.markdown", "README", "README.txt", "readme.md", "readme.markdown", "readme", "readme.txt", "Readme.md"}

// Finds a readme in a directory, then in docs or the config folder
func (r *Repo) Readme(commit plumbing.Hash, dir string) (*Blob, error) {
	for _, candidate := range []string{dir, path.Join(dir, ".distroface"), path.Join(dir, "docs"), path.Join(dir, ".github")} {
		entries, _, err := r.Tree(context.Background(), commit, candidate, false)
		if err != nil {
			continue
		}
		for _, name := range readmeNames {
			for _, e := range entries {
				if e.Name == name && e.Mode.IsFile() {
					return r.Blob(commit, e.Path, MaxInlineBlob)
				}
			}
		}
	}
	return nil, ErrNotFound
}

// Last commit touching each of the given paths at a commit
func (r *Repo) LastCommits(ctx context.Context, commit plumbing.Hash, paths []string, budget int) (map[string]*object.Commit, error) {
	out := map[string]*object.Commit{}
	if len(paths) == 0 {
		return out, nil
	}
	pending := map[string]bool{}
	for _, p := range paths {
		pending[p] = true
	}
	head, err := r.Commit(commit)
	if err != nil {
		return nil, err
	}
	// Walk history once, assigning each path to the first commit that changed it
	iter := object.NewCommitIterCTime(head, nil, nil)
	defer iter.Close()
	steps := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		steps++
		if len(pending) == 0 || (budget > 0 && steps > budget) {
			return storer.ErrStop
		}
		tree, err := c.Tree()
		if err != nil {
			return nil
		}
		var parentTree *object.Tree
		if c.NumParents() > 0 {
			if parent, err := c.Parent(0); err == nil {
				parentTree, _ = parent.Tree()
			}
		}
		for p := range pending {
			cur := entryHash(tree, p)
			var prev plumbing.Hash
			if parentTree != nil {
				prev = entryHash(parentTree, p)
			}
			if cur != prev {
				out[p] = c
				delete(pending, p)
			}
		}
		return nil
	})
	return out, err
}

func entryHash(t *object.Tree, p string) plumbing.Hash {
	if p == "" {
		return t.Hash
	}
	e, err := t.FindEntry(p)
	if err != nil {
		return plumbing.ZeroHash
	}
	return e.Hash
}

// Blame runs grouped by consecutive commit
type BlameRun struct {
	Start  int
	End    int
	Commit *object.Commit
	Lines  []string
}

// Attributes a file's lines, grouped into runs
// Largest text file blame will walk history for
const MaxBlameBytes = 1 << 20

// Longest a single blame may run
const blameTimeout = 30 * time.Second

// Attributes every line of a text file, refusing binaries, oversized files and slow walks
func (r *Repo) Blame(ctx context.Context, commit plumbing.Hash, p string) ([]BlameRun, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, err
	}
	probe, err := r.Blob(commit, p, 8192)
	if err != nil {
		return nil, err
	}
	if probe.IsBinary {
		return nil, fmt.Errorf("%w: binary files cannot be blamed", ErrInvalid)
	}
	if probe.Size > MaxBlameBytes {
		return nil, fmt.Errorf("%w: files over %d KB cannot be blamed", ErrTooLarge, MaxBlameBytes/1024)
	}
	ctx, cancel := context.WithTimeout(ctx, blameTimeout)
	defer cancel()
	type outcome struct {
		res *git.BlameResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := git.Blame(c, strings.Trim(p, "/"))
		done <- outcome{res, err}
	}()
	var res *git.BlameResult
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: blame took longer than %s", ErrTooLarge, blameTimeout)
	case o := <-done:
		if o.err != nil {
			return nil, ErrNotFound
		}
		res = o.res
	}
	var runs []BlameRun
	for i, line := range res.Lines {
		if len(runs) > 0 && runs[len(runs)-1].Commit.Hash == line.Hash {
			last := &runs[len(runs)-1]
			last.End = i + 1
			last.Lines = append(last.Lines, line.Text)
			continue
		}
		lc, err := r.Commit(line.Hash)
		if err != nil {
			lc = &object.Commit{Hash: line.Hash, Author: object.Signature{Name: line.AuthorName, Email: line.Author, When: line.Date}}
		}
		runs = append(runs, BlameRun{Start: i + 1, End: i + 1, Commit: lc, Lines: []string{line.Text}})
	}
	return runs, nil
}

// Greps one commit's tree
func (r *Repo) Grep(commit plumbing.Hash, pattern string, pathGlob string, caseInsensitive bool, limit int) ([]git.GrepResult, error) {
	flags := ""
	if caseInsensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	opts := &git.GrepOptions{Patterns: []*regexp.Regexp{re}, CommitHash: commit}
	if pathGlob != "" {
		pre, err := regexp.Compile(globToRegexp(pathGlob))
		if err != nil {
			return nil, fmt.Errorf("invalid path glob: %w", err)
		}
		opts.PathSpecs = []*regexp.Regexp{pre}
	}
	results, err := r.Repo.Grep(opts)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// Turns a shell style glob into an anchored regexp
func globToRegexp(glob string) string {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				sb.WriteString(".*")
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					sb.WriteString("(?:/)?")
				}
			} else {
				sb.WriteString("[^/]*")
			}
		case '?':
			sb.WriteString("[^/]")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return sb.String()
}

// Bytes per language over a tree, vendored and generated paths skipped
func (r *Repo) Languages(ctx context.Context, commit plumbing.Hash) (map[string]int64, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	iter := tree.Files()
	defer iter.Close()
	err = iter.ForEach(func(f *object.File) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if enry.IsVendor(f.Name) || enry.IsDotFile(f.Name) || enry.IsDocumentation(f.Name) || enry.IsConfiguration(f.Name) || enry.IsImage(f.Name) {
			return nil
		}
		rd, err := f.Reader()
		if err != nil {
			return nil
		}
		head, _ := io.ReadAll(io.LimitReader(rd, 16*1024))
		rd.Close()
		if enry.IsBinary(head) || enry.IsGenerated(f.Name, head) {
			return nil
		}
		lang := enry.GetLanguage(f.Name, head)
		if lang == "" || enry.GetLanguageType(lang) != enry.Programming {
			return nil
		}
		out[lang] += f.Size
		return nil
	})
	return out, err
}

// Commit stats aggregated per author and per week
type Contributor struct {
	Name, Email          string
	Commits              int32
	Additions, Deletions int32
}

type WeekActivity struct {
	Week                 time.Time
	Commits              int32
	Additions, Deletions int32
}

// Walks history once for contributors and weekly activity, capped by budget
func (r *Repo) Activity(ctx context.Context, commit plumbing.Hash, since time.Time, budget int) ([]Contributor, []WeekActivity, error) {
	head, err := r.Commit(commit)
	if err != nil {
		return nil, nil, err
	}
	byAuthor := map[string]*Contributor{}
	byWeek := map[time.Time]*WeekActivity{}
	iter := object.NewCommitIterCTime(head, nil, nil)
	defer iter.Close()
	steps := 0
	err = iter.ForEach(func(c *object.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		steps++
		if budget > 0 && steps > budget {
			return storer.ErrStop
		}
		var add, del int32
		if c.NumParents() <= 1 {
			if stats, err := c.StatsContext(ctx); err == nil {
				for _, s := range stats {
					add += int32(s.Addition)
					del += int32(s.Deletion)
				}
			}
		}
		key := strings.ToLower(c.Author.Email)
		ctr := byAuthor[key]
		if ctr == nil {
			ctr = &Contributor{Name: c.Author.Name, Email: c.Author.Email}
			byAuthor[key] = ctr
		}
		ctr.Commits++
		ctr.Additions += add
		ctr.Deletions += del
		when := c.Committer.When.UTC()
		if !since.IsZero() && when.Before(since) {
			return nil
		}
		year, week := when.ISOWeek()
		monday := isoWeekStart(year, week)
		wk := byWeek[monday]
		if wk == nil {
			wk = &WeekActivity{Week: monday}
			byWeek[monday] = wk
		}
		wk.Commits++
		wk.Additions += add
		wk.Deletions += del
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	contributors := make([]Contributor, 0, len(byAuthor))
	for _, c := range byAuthor {
		contributors = append(contributors, *c)
	}
	sort.Slice(contributors, func(i, j int) bool {
		if contributors[i].Commits != contributors[j].Commits {
			return contributors[i].Commits > contributors[j].Commits
		}
		return contributors[i].Email < contributors[j].Email
	})
	weeks := make([]WeekActivity, 0, len(byWeek))
	for _, w := range byWeek {
		weeks = append(weeks, *w)
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Week.Before(weeks[j].Week) })
	return contributors, weeks, nil
}

func isoWeekStart(year, week int) time.Time {
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	offset := int(jan4.Weekday())
	if offset == 0 {
		offset = 7
	}
	start := jan4.AddDate(0, 0, -(offset - 1))
	return start.AddDate(0, 0, (week-1)*7)
}

// Branches and tags pointing at a commit
func (r *Repo) RefsAt(commit plumbing.Hash) ([]string, []string) {
	var branches, tags []string
	bs, _ := r.Branches()
	for name, h := range bs {
		if h == commit {
			branches = append(branches, name)
		}
	}
	ts, _ := r.Tags()
	for name, h := range ts {
		peeled, err := r.peelToCommit(h)
		if err == nil && peeled == commit {
			tags = append(tags, name)
		}
	}
	sort.Strings(branches)
	sort.Strings(tags)
	return branches, tags
}

// Annotated tag object when the ref points at one
func (r *Repo) TagObject(h plumbing.Hash) *object.Tag {
	tag, err := r.Repo.TagObject(h)
	if err != nil {
		return nil
	}
	return tag
}

// Proto conversion of a commit without verification or stats
func CommitToProto(c *object.Commit) *v1.GitCommit {
	if c == nil {
		return nil
	}
	out := &v1.GitCommit{
		Sha:       c.Hash.String(),
		Message:   c.Message,
		TreeSha:   c.TreeHash.String(),
		Author:    SignatureToProto(c.Author),
		Committer: SignatureToProto(c.Committer),
	}
	for _, p := range c.ParentHashes {
		out.Parents = append(out.Parents, p.String())
	}
	return out
}

// Proto conversion of a signature
func SignatureToProto(s object.Signature) *v1.GitSignature {
	return &v1.GitSignature{Name: s.Name, Email: s.Email, At: timestamppb.New(s.When)}
}

// Proto conversion of a listing entry
func EntryToProto(e Entry) *v1.GitTreeEntry {
	typ := "blob"
	switch e.Mode {
	case filemode.Dir:
		typ = "tree"
	case filemode.Submodule:
		typ = "commit"
	}
	return &v1.GitTreeEntry{
		Name: e.Name, Path: e.Path, Mode: fmt.Sprintf("%06o", uint32(e.Mode)), Type: typ,
		Size: e.Size, Sha: e.Hash.String(), IsSubmodule: e.IsSubmodule, IsLfs: e.IsLFS,
	}
}

// Shared sort for template and readme lookups
func sortedKeys(m map[string]plumbing.Hash) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Reports whether bytes look like text
func isText(buf []byte) bool {
	return !enry.IsBinary(buf) && !bytes.Contains(buf, []byte{0})
}
