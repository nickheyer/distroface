package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/merkletrie"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// Returned when a merge has conflicts the caller must inspect
var ErrMergeConflict = errors.New("merge conflict")

// Returned when a fast forward is impossible
var ErrNotFastForward = errors.New("not a fast forward")

// One conflicted path with the three sides and a marked merge for text
type Conflict struct {
	Path   string
	Base   plumbing.Hash
	Ours   plumbing.Hash
	Theirs plumbing.Hash
	Kind   string // content, modify_delete, delete_modify, mode, binary, submodule, directory
	Marked []byte
}

// Outcome of a three way tree merge
type MergeResult struct {
	Tree      plumbing.Hash
	Conflicts []Conflict
}

// Reports whether any conflict remains
func (m *MergeResult) Clean() bool { return len(m.Conflicts) == 0 }

type sideChange struct {
	entry  object.TreeEntry
	action merkletrie.Action
	from   object.TreeEntry
}

func changeMap(changes object.Changes) (map[string]sideChange, error) {
	out := map[string]sideChange{}
	for _, ch := range changes {
		action, err := ch.Action()
		if err != nil {
			return nil, err
		}
		p := ch.To.Name
		if action == merkletrie.Delete {
			p = ch.From.Name
		}
		// Renames show as a delete plus an insert so both paths merge independently
		if action == merkletrie.Modify && ch.From.Name != ch.To.Name {
			out[ch.From.Name] = sideChange{action: merkletrie.Delete, from: ch.From.TreeEntry}
			out[ch.To.Name] = sideChange{action: merkletrie.Insert, entry: ch.To.TreeEntry}
			continue
		}
		out[p] = sideChange{entry: ch.To.TreeEntry, action: action, from: ch.From.TreeEntry}
	}
	return out, nil
}

// Three way merge of two commits against their merge base, dry runs write no objects
func (r *Repo) Merge3(ctx context.Context, base, ours, theirs plumbing.Hash, write bool) (*MergeResult, error) {
	baseTree := &object.Tree{}
	if !base.IsZero() {
		bc, err := r.Commit(base)
		if err != nil {
			return nil, err
		}
		baseTree, err = bc.Tree()
		if err != nil {
			return nil, err
		}
	}
	oc, err := r.Commit(ours)
	if err != nil {
		return nil, err
	}
	oursTree, err := oc.Tree()
	if err != nil {
		return nil, err
	}
	tc, err := r.Commit(theirs)
	if err != nil {
		return nil, err
	}
	theirsTree, err := tc.Tree()
	if err != nil {
		return nil, err
	}
	return r.MergeTrees(ctx, baseTree, oursTree, theirsTree, write)
}

// Three way merge at the tree level with line level diff3 for text files
func (r *Repo) MergeTrees(ctx context.Context, baseTree, oursTree, theirsTree *object.Tree, write bool) (*MergeResult, error) {
	oursChanges, err := object.DiffTreeWithOptions(ctx, baseTree, oursTree, &object.DiffTreeOptions{})
	if err != nil {
		return nil, err
	}
	theirsChanges, err := object.DiffTreeWithOptions(ctx, baseTree, theirsTree, &object.DiffTreeOptions{})
	if err != nil {
		return nil, err
	}
	ourMap, err := changeMap(oursChanges)
	if err != nil {
		return nil, err
	}
	theirMap, err := changeMap(theirsChanges)
	if err != nil {
		return nil, err
	}

	result := &MergeResult{}
	var edits []TreeEdit
	paths := map[string]bool{}
	for p := range ourMap {
		paths[p] = true
	}
	for p := range theirMap {
		paths[p] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	for _, p := range ordered {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		o, inOurs := ourMap[p]
		t, inTheirs := theirMap[p]
		switch {
		case inOurs && !inTheirs:
			// Only we touched it, keep our side which the base tree already lacks
			edits = append(edits, editFor(p, o))
		case inTheirs && !inOurs:
			edits = append(edits, editFor(p, t))
		default:
			edit, conflict, err := r.mergePath(ctx, p, baseEntry(baseTree, p), o, t)
			if err != nil {
				return nil, err
			}
			if conflict != nil {
				result.Conflicts = append(result.Conflicts, *conflict)
				continue
			}
			if edit != nil {
				edits = append(edits, *edit)
			}
		}
	}
	// Directory and file collisions at the same path conflict
	if conflicts := dirFileCollisions(ordered, ourMap, theirMap); len(conflicts) > 0 {
		result.Conflicts = append(result.Conflicts, conflicts...)
	}
	if !write || len(result.Conflicts) > 0 {
		return result, nil
	}
	tree, err := r.applyEntryEdits(oursTree.Hash, edits)
	if err != nil {
		return nil, err
	}
	result.Tree = tree
	return result, nil
}

func baseEntry(tree *object.Tree, p string) *object.TreeEntry {
	e, err := tree.FindEntry(p)
	if err != nil {
		return nil
	}
	return e
}

// Edit that applies one side's change verbatim
func editFor(p string, c sideChange) TreeEdit {
	if c.action == merkletrie.Delete {
		return TreeEdit{Path: p}
	}
	return TreeEdit{Path: p, Mode: c.entry.Mode, Content: hashReader(c.entry.Hash)}
}

// Marker reader that reuses an existing blob by hash
type hashRef struct{ hash plumbing.Hash }

func (hashRef) Read([]byte) (int, error) { return 0, errors.New("hash reference is not readable") }

func hashReader(h plumbing.Hash) *hashRef { return &hashRef{hash: h} }

// Applies edits where content readers may be existing blob hashes
func (r *Repo) applyEntryEdits(base plumbing.Hash, edits []TreeEdit) (plumbing.Hash, error) {
	root := newTreeNode(base)
	for _, e := range edits {
		if e.Content == nil {
			if err := r.setPath(root, e.Path, nil); err != nil {
				return plumbing.ZeroHash, err
			}
			continue
		}
		var h plumbing.Hash
		if ref, ok := e.Content.(*hashRef); ok {
			h = ref.hash
		} else {
			var err error
			h, err = r.WriteBlob(e.Content)
			if err != nil {
				return plumbing.ZeroHash, err
			}
		}
		mode := e.Mode
		if mode == filemode.Empty {
			mode = filemode.Regular
		}
		if err := r.setPath(root, e.Path, &object.TreeEntry{Mode: mode, Hash: h}); err != nil {
			return plumbing.ZeroHash, err
		}
	}
	h, keep, err := r.flushNode(root)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if !keep {
		return r.WriteTree(nil)
	}
	return h, nil
}

// Merges one path changed on both sides
func (r *Repo) mergePath(ctx context.Context, p string, base *object.TreeEntry, o, t sideChange) (*TreeEdit, *Conflict, error) {
	conflict := func(kind string) *Conflict {
		c := &Conflict{Path: p, Kind: kind}
		if base != nil {
			c.Base = base.Hash
		}
		if o.action != merkletrie.Delete {
			c.Ours = o.entry.Hash
		}
		if t.action != merkletrie.Delete {
			c.Theirs = t.entry.Hash
		}
		return c
	}
	switch {
	case o.action == merkletrie.Delete && t.action == merkletrie.Delete:
		return &TreeEdit{Path: p}, nil, nil
	case o.action == merkletrie.Delete:
		return nil, conflict("delete_modify"), nil
	case t.action == merkletrie.Delete:
		return nil, conflict("modify_delete"), nil
	}
	if o.entry.Hash == t.entry.Hash && o.entry.Mode == t.entry.Mode {
		return &TreeEdit{Path: p, Mode: o.entry.Mode, Content: hashReader(o.entry.Hash)}, nil, nil
	}
	if o.entry.Mode == filemode.Submodule || t.entry.Mode == filemode.Submodule {
		return nil, conflict("submodule"), nil
	}
	if o.entry.Mode != t.entry.Mode {
		if base != nil && base.Mode == o.entry.Mode && o.entry.Hash == baseHash(base) {
			return &TreeEdit{Path: p, Mode: t.entry.Mode, Content: hashReader(t.entry.Hash)}, nil, nil
		}
		if base != nil && base.Mode == t.entry.Mode && t.entry.Hash == baseHash(base) {
			return &TreeEdit{Path: p, Mode: o.entry.Mode, Content: hashReader(o.entry.Hash)}, nil, nil
		}
		return nil, conflict("mode"), nil
	}
	var baseContent []byte
	if base != nil {
		var err error
		baseContent, err = r.BlobContent(base.Hash)
		if err != nil {
			return nil, nil, err
		}
	}
	oursContent, err := r.BlobContent(o.entry.Hash)
	if err != nil {
		return nil, nil, err
	}
	theirsContent, err := r.BlobContent(t.entry.Hash)
	if err != nil {
		return nil, nil, err
	}
	if !isText(baseContent) || !isText(oursContent) || !isText(theirsContent) {
		return nil, conflict("binary"), nil
	}
	merged, clean := Diff3(baseContent, oursContent, theirsContent, "ours", "theirs")
	if !clean {
		c := conflict("content")
		c.Marked = merged
		return nil, c, nil
	}
	return &TreeEdit{Path: p, Mode: o.entry.Mode, Content: bytes.NewReader(merged)}, nil, nil
}

func baseHash(e *object.TreeEntry) plumbing.Hash {
	if e == nil {
		return plumbing.ZeroHash
	}
	return e.Hash
}

// Conflicts where one side made a path a file and the other a directory
func dirFileCollisions(paths []string, ours, theirs map[string]sideChange) []Conflict {
	var out []Conflict
	seen := map[string]bool{}
	for _, p := range paths {
		for _, q := range paths {
			if p == q || !strings.HasPrefix(q, p+"/") {
				continue
			}
			oFile := ours[p].action != merkletrie.Delete && ours[p].entry.Mode != filemode.Empty
			tFile := theirs[p].action != merkletrie.Delete && theirs[p].entry.Mode != filemode.Empty
			_, oDir := ours[q]
			_, tDir := theirs[q]
			if (oFile && tDir) || (tFile && oDir) {
				if !seen[p] {
					seen[p] = true
					out = append(out, Conflict{Path: p, Kind: "directory"})
				}
			}
		}
	}
	return out
}

// Line based three way merge with conflict markers, clean reports no markers
func Diff3(base, ours, theirs []byte, oursLabel, theirsLabel string) ([]byte, bool) {
	if bytes.Equal(ours, theirs) {
		return ours, true
	}
	if bytes.Equal(base, ours) {
		return theirs, true
	}
	if bytes.Equal(base, theirs) {
		return ours, true
	}
	baseLines := toLines(string(base))
	oursOps := lineOps(string(base), string(ours))
	theirsOps := lineOps(string(base), string(theirs))

	var out bytes.Buffer
	clean := true
	i := 0
	for i < len(baseLines) || hasPending(oursOps, i) || hasPending(theirsOps, i) {
		oIns, oDel := opsAt(oursOps, i)
		tIns, tDel := opsAt(theirsOps, i)
		switch {
		case len(oIns) == 0 && !oDel && len(tIns) == 0 && !tDel:
			if i < len(baseLines) {
				out.WriteString(baseLines[i])
			}
			i++
		case len(tIns) == 0 && !tDel:
			for _, l := range oIns {
				out.WriteString(l)
			}
			if !oDel && i < len(baseLines) {
				out.WriteString(baseLines[i])
			}
			i++
		case len(oIns) == 0 && !oDel:
			for _, l := range tIns {
				out.WriteString(l)
			}
			if !tDel && i < len(baseLines) {
				out.WriteString(baseLines[i])
			}
			i++
		default:
			// Both sides changed this spot, identical edits merge, others conflict
			oText := strings.Join(oIns, "")
			tText := strings.Join(tIns, "")
			if oDel == tDel && oText == tText {
				out.WriteString(oText)
				if !oDel && i < len(baseLines) {
					out.WriteString(baseLines[i])
				}
				i++
				continue
			}
			clean = false
			// Extend the conflict region across adjacent changed base lines
			j := i
			var oBlock, tBlock, bBlock strings.Builder
			for {
				oI, oD := opsAt(oursOps, j)
				tI, tD := opsAt(theirsOps, j)
				oBlock.WriteString(strings.Join(oI, ""))
				tBlock.WriteString(strings.Join(tI, ""))
				if j < len(baseLines) {
					if !oD {
						oBlock.WriteString(baseLines[j])
					}
					if !tD {
						tBlock.WriteString(baseLines[j])
					}
					bBlock.WriteString(baseLines[j])
				}
				j++
				nextO, nextOD := opsAt(oursOps, j)
				nextT, nextTD := opsAt(theirsOps, j)
				changed := len(nextO) > 0 || nextOD || len(nextT) > 0 || nextTD
				if !changed || j > len(baseLines) {
					break
				}
			}
			fmt.Fprintf(&out, "<<<<<<< %s\n", oursLabel)
			out.WriteString(ensureNewline(oBlock.String()))
			out.WriteString("||||||| base\n")
			out.WriteString(ensureNewline(bBlock.String()))
			out.WriteString("=======\n")
			out.WriteString(ensureNewline(tBlock.String()))
			fmt.Fprintf(&out, ">>>>>>> %s\n", theirsLabel)
			i = j
		}
	}
	return out.Bytes(), clean
}

func ensureNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// Lines with their newline retained
func toLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// Per base line operations: inserted lines before it and whether it is deleted
type lineOp struct {
	insert []string
	delete bool
}

// Computes line operations from base to target keyed by base line index
func lineOps(base, target string) map[int]*lineOp {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(base, target)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), lines)
	ops := map[int]*lineOp{}
	idx := 0
	get := func(i int) *lineOp {
		op := ops[i]
		if op == nil {
			op = &lineOp{}
			ops[i] = op
		}
		return op
	}
	for _, d := range diffs {
		for _, l := range toLines(d.Text) {
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				idx++
			case diffmatchpatch.DiffDelete:
				get(idx).delete = true
				idx++
			case diffmatchpatch.DiffInsert:
				op := get(idx)
				op.insert = append(op.insert, l)
			}
		}
	}
	return ops
}

func opsAt(ops map[int]*lineOp, i int) ([]string, bool) {
	op := ops[i]
	if op == nil {
		return nil, false
	}
	return op.insert, op.delete
}

func hasPending(ops map[int]*lineOp, i int) bool {
	for k := range ops {
		if k >= i {
			return true
		}
	}
	return false
}
