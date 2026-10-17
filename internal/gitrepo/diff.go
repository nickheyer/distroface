package gitrepo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	fdiff "github.com/go-git/go-git/v6/plumbing/format/diff"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/merkletrie"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Caps that keep single file diff answers bounded
const (
	MaxDiffFileLines = 20000
	MaxDiffFileBytes = 2 << 20
	DiffContextLines = 3
	// Changed files past this count are listed without stats or hunks
	MaxDiffFiles = 1000
)

// Diff options
type DiffOptions struct {
	// Files to emit after Offset, zero emits every matching file
	Limit  int
	Offset int
	// Case insensitive substring every counted path must contain
	PathQuery string
	// Exactly one path, hunks included
	OnlyPath string
	// Changed line cap above which hunks are withheld, zero uses MaxDiffFileLines
	MaxLines  int
	WithHunks bool
}

// Line counts and content flags of one blob pair, immutable per pair
type blobPairStats struct {
	additions int32
	deletions int32
	binary    bool
	lfs       bool
}

var blobStats = NewMemo[blobPairStats](12*time.Hour, 20000)

func changePath(ch *object.Change) string {
	if ch.To.Name != "" {
		return ch.To.Name
	}
	return ch.From.Name
}

// Structured diff between two trees, totals over every matching file and hunks only on the page
func (r *Repo) DiffTrees(ctx context.Context, from, to *object.Tree, opts DiffOptions) (*v1.GitDiff, error) {
	changes, err := object.DiffTreeWithOptions(ctx, from, to, &object.DiffTreeOptions{DetectRenames: true, RenameScore: 50, RenameLimit: 1000})
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(opts.PathQuery)
	matched := make([]*object.Change, 0, len(changes))
	for _, ch := range changes {
		if opts.OnlyPath != "" && ch.From.Name != opts.OnlyPath && ch.To.Name != opts.OnlyPath {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(ch.From.Name), query) && !strings.Contains(strings.ToLower(ch.To.Name), query) {
			continue
		}
		matched = append(matched, ch)
	}
	sort.SliceStable(matched, func(i, j int) bool { return changePath(matched[i]) < changePath(matched[j]) })
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = MaxDiffFileLines
	}
	withHunks := opts.WithHunks || opts.OnlyPath != ""
	out := &v1.GitDiff{TotalFiles: int32(len(matched))}
	for i, ch := range matched {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		inPage := i >= opts.Offset && (opts.Limit <= 0 || len(out.Files) < opts.Limit)
		withStats := i < MaxDiffFiles
		file, err := r.changeToFile(ctx, ch, maxLines, withHunks && inPage && withStats, withStats)
		if err != nil {
			return nil, err
		}
		if !withStats {
			out.Truncated = true
		}
		out.Additions += file.Additions
		out.Deletions += file.Deletions
		if inPage {
			out.Files = append(out.Files, file)
		}
	}
	return out, nil
}

// Diff of a commit against one parent, or against the empty tree for roots
func (r *Repo) CommitDiff(ctx context.Context, commit plumbing.Hash, parentIndex int, opts DiffOptions) (*v1.GitDiff, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return nil, err
	}
	toTree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	fromTree := &object.Tree{}
	if c.NumParents() > parentIndex {
		parent, err := c.Parent(parentIndex)
		if err != nil {
			return nil, err
		}
		fromTree, err = parent.Tree()
		if err != nil {
			return nil, err
		}
	}
	return r.DiffTrees(ctx, fromTree, toTree, opts)
}

// Diff between two commits
func (r *Repo) Diff(ctx context.Context, from, to plumbing.Hash, opts DiffOptions) (*v1.GitDiff, error) {
	fromTree := &object.Tree{}
	if !from.IsZero() {
		c, err := r.Commit(from)
		if err != nil {
			return nil, err
		}
		fromTree, err = c.Tree()
		if err != nil {
			return nil, err
		}
	}
	c, err := r.Commit(to)
	if err != nil {
		return nil, err
	}
	toTree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	return r.DiffTrees(ctx, fromTree, toTree, opts)
}

// Additions and deletions of a commit against its first parent
func (r *Repo) CommitStats(ctx context.Context, c *object.Commit) (int32, int32, int32) {
	stats, err := c.StatsContext(ctx)
	if err != nil {
		return 0, 0, 0
	}
	var add, del int32
	for _, s := range stats {
		add += int32(s.Addition)
		del += int32(s.Deletion)
	}
	return add, del, int32(len(stats))
}

// One changed file, stats from the blob pair cache and hunks only when asked
func (r *Repo) changeToFile(ctx context.Context, ch *object.Change, maxLines int, withHunks, withStats bool) (*v1.DiffFile, error) {
	file := &v1.DiffFile{OldPath: ch.From.Name, NewPath: ch.To.Name}
	action, err := ch.Action()
	if err != nil {
		return nil, err
	}
	switch action {
	case merkletrie.Insert:
		file.Status = v1.DiffStatus_DIFF_STATUS_ADDED
	case merkletrie.Delete:
		file.Status = v1.DiffStatus_DIFF_STATUS_DELETED
	default:
		if ch.From.Name != ch.To.Name {
			file.Status = v1.DiffStatus_DIFF_STATUS_RENAMED
		} else if ch.From.TreeEntry.Mode != ch.To.TreeEntry.Mode && ch.From.TreeEntry.Hash == ch.To.TreeEntry.Hash {
			file.Status = v1.DiffStatus_DIFF_STATUS_TYPE_CHANGED
		} else {
			file.Status = v1.DiffStatus_DIFF_STATUS_MODIFIED
		}
	}
	if ch.From.Name != "" {
		file.OldMode = fmt.Sprintf("%06o", uint32(ch.From.TreeEntry.Mode))
		file.OldSha = ch.From.TreeEntry.Hash.String()
	}
	if ch.To.Name != "" {
		file.NewMode = fmt.Sprintf("%06o", uint32(ch.To.TreeEntry.Mode))
		file.NewSha = ch.To.TreeEntry.Hash.String()
	}
	if ch.From.TreeEntry.Mode == filemode.Submodule || ch.To.TreeEntry.Mode == filemode.Submodule {
		file.IsSubmodule = true
		return file, nil
	}
	if !withStats {
		file.Truncated = true
		return file, nil
	}
	key := ch.From.TreeEntry.Hash.String() + ":" + ch.To.TreeEntry.Hash.String()
	var stats blobPairStats
	var hunks []*v1.DiffHunk
	if withHunks {
		if stats, hunks, err = r.patchChange(ctx, ch, maxLines); err != nil {
			return nil, err
		}
		blobStats.Get(ctx, key, func() (blobPairStats, error) { return stats, nil })
	} else if stats, err = blobStats.Get(ctx, key, func() (blobPairStats, error) {
		st, _, err := r.patchChange(ctx, ch, 0)
		return st, err
	}); err != nil {
		return nil, err
	}
	file.IsBinary, file.IsLfs = stats.binary, stats.lfs
	file.Additions, file.Deletions = stats.additions, stats.deletions
	file.Truncated = !stats.binary && int(stats.additions+stats.deletions) > maxLines
	if !file.Truncated {
		file.Hunks = hunks
	}
	return file, nil
}

// Stats of a change and its hunks when maxLines allows them, oversized blobs count as binary
func (r *Repo) patchChange(ctx context.Context, ch *object.Change, maxLines int) (blobPairStats, []*v1.DiffHunk, error) {
	var stats blobPairStats
	for _, h := range []plumbing.Hash{ch.From.TreeEntry.Hash, ch.To.TreeEntry.Hash} {
		if h.IsZero() {
			continue
		}
		if size, err := r.Storer.EncodedObjectSize(h); err == nil && size > MaxDiffFileBytes {
			stats.binary = true
			return stats, nil, nil
		}
	}
	fromFile, toFile, err := ch.Files()
	if err != nil {
		return stats, nil, err
	}
	for _, f := range []*object.File{fromFile, toFile} {
		if f == nil {
			continue
		}
		if bin, err := f.IsBinary(); err == nil && bin {
			stats.binary = true
		}
		if f.Size < 512 {
			if content, err := f.Contents(); err == nil && isLFSPointer([]byte(content)) {
				stats.lfs = true
			}
		}
	}
	if stats.binary {
		return stats, nil, nil
	}
	patch, err := ch.PatchContext(ctx)
	if err != nil {
		return stats, nil, err
	}
	var hunks []*v1.DiffHunk
	for _, fp := range patch.FilePatches() {
		if fp.IsBinary() {
			return blobPairStats{binary: true, lfs: stats.lfs}, nil, nil
		}
		h, add, del := hunksFromChunks(fp.Chunks(), maxLines)
		stats.additions += add
		stats.deletions += del
		hunks = append(hunks, h...)
	}
	return stats, hunks, nil
}

// Turns chunk operations into unified hunks with line numbers, counts only when maxLines is zero or exceeded
func hunksFromChunks(chunks []fdiff.Chunk, maxLines int) ([]*v1.DiffHunk, int32, int32) {
	type line struct {
		kind v1.DiffLineKind
		text string
	}
	var lines []line
	var add, del int32
	for _, c := range chunks {
		content := c.Content()
		parts := splitLines(content)
		for _, p := range parts {
			switch c.Type() {
			case fdiff.Add:
				lines = append(lines, line{v1.DiffLineKind_DIFF_LINE_KIND_ADD, p})
				add++
			case fdiff.Delete:
				lines = append(lines, line{v1.DiffLineKind_DIFF_LINE_KIND_DELETE, p})
				del++
			default:
				lines = append(lines, line{v1.DiffLineKind_DIFF_LINE_KIND_CONTEXT, p})
			}
		}
	}
	if maxLines <= 0 || int(add+del) > maxLines {
		return nil, add, del
	}
	var hunks []*v1.DiffHunk
	oldNo, newNo := 1, 1
	i := 0
	for i < len(lines) {
		// Skip context until the next change
		for i < len(lines) && lines[i].kind == v1.DiffLineKind_DIFF_LINE_KIND_CONTEXT {
			oldNo++
			newNo++
			i++
		}
		if i >= len(lines) {
			break
		}
		start := i - DiffContextLines
		if start < 0 {
			start = 0
		}
		// Rewind the numbers for the leading context, it is emitted again below
		lead := i - start
		oldNo, newNo = oldNo-lead, newNo-lead
		h := &v1.DiffHunk{OldStart: int32(oldNo), NewStart: int32(newNo)}
		j := start
		for j < len(lines) {
			l := lines[j]
			switch l.kind {
			case v1.DiffLineKind_DIFF_LINE_KIND_CONTEXT:
				// Close the hunk when a long context run follows the last change
				run := 0
				for k := j; k < len(lines) && lines[k].kind == v1.DiffLineKind_DIFF_LINE_KIND_CONTEXT; k++ {
					run++
				}
				if run > 2*DiffContextLines || j+run >= len(lines) {
					tail := min(run, DiffContextLines)
					for t := 0; t < tail; t++ {
						h.Lines = append(h.Lines, &v1.DiffLine{Kind: l.kind, OldNumber: int32(oldNo), NewNumber: int32(newNo), Text: lines[j+t].text})
						oldNo++
						newNo++
						h.OldLines++
						h.NewLines++
					}
					j += tail
					goto closeHunk
				}
				for t := 0; t < run; t++ {
					h.Lines = append(h.Lines, &v1.DiffLine{Kind: l.kind, OldNumber: int32(oldNo), NewNumber: int32(newNo), Text: lines[j+t].text})
					oldNo++
					newNo++
					h.OldLines++
					h.NewLines++
				}
				j += run
			case v1.DiffLineKind_DIFF_LINE_KIND_ADD:
				h.Lines = append(h.Lines, &v1.DiffLine{Kind: l.kind, NewNumber: int32(newNo), Text: l.text})
				newNo++
				h.NewLines++
				j++
			case v1.DiffLineKind_DIFF_LINE_KIND_DELETE:
				h.Lines = append(h.Lines, &v1.DiffLine{Kind: l.kind, OldNumber: int32(oldNo), Text: l.text})
				oldNo++
				h.OldLines++
				j++
			}
		}
	closeHunk:
		h.Header = fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		hunks = append(hunks, h)
		i = j
	}
	return hunks, add, del
}

// Splits chunk content into lines without a trailing empty entry
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
