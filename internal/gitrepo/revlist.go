package gitrepo

import (
	"container/heap"
	"context"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
)

// Uninteresting commits walked past the last interesting one
const revListSlop = 5

type revFlag uint8

const (
	revInteresting revFlag = 1 << iota
	revUninteresting
)

type revNode struct {
	c    *object.Commit
	when int64
	seq  int
}

// Newest commit first, insertion order breaks ties
type revHeap []*revNode

func (h *revHeap) Len() int { return len(*h) }
func (h *revHeap) Less(i, j int) bool {
	a, b := (*h)[i], (*h)[j]
	if a.when != b.when {
		return a.when > b.when
	}
	return a.seq < b.seq
}
func (h *revHeap) Swap(i, j int)   { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }
func (h *revHeap) Push(x any)      { *h = append(*h, x.(*revNode)) }
func (h *revHeap) Pop() any        { old := *h; n := old[len(old)-1]; *h = old[:len(old)-1]; return n }
func (h *revHeap) peekWhen() int64 { return (*h)[0].when }

// Commit behind a hash, annotated tags peeled
func peelCommit(st storer.EncodedObjectStorer, h plumbing.Hash) (*object.Commit, error) {
	c, err := object.GetCommit(st, h)
	if err == nil {
		return c, nil
	}
	tag, terr := object.GetTag(st, h)
	if terr != nil {
		return nil, err
	}
	return tag.Commit()
}

// Rev-list want --not have, newest first, bounded by limit
func RevList(ctx context.Context, st storer.EncodedObjectStorer, want, have []plumbing.Hash, limit int) ([]*object.Commit, bool, error) {
	w := &revWalk{st: st, flags: map[plumbing.Hash]revFlag{}, parents: map[plumbing.Hash][]plumbing.Hash{}, inHeap: map[plumbing.Hash]bool{}}
	for _, h := range have {
		// Excludes that do not resolve simply exclude nothing
		_ = w.push(h, revUninteresting)
	}
	for _, h := range want {
		if h.IsZero() {
			continue
		}
		if err := w.push(h, revInteresting); err != nil {
			return nil, false, err
		}
	}
	var out []*object.Commit
	truncated := false
	slop := revListSlop
	for w.heap.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		n := heap.Pop(&w.heap).(*revNode)
		delete(w.inHeap, n.c.Hash)
		if w.flags[n.c.Hash]&revUninteresting != 0 {
			for _, p := range n.c.ParentHashes {
				if err := w.push(p, revUninteresting); err != nil {
					return nil, false, err
				}
			}
		} else {
			w.interesting--
			out = append(out, n.c)
			if limit > 0 && len(out) >= limit {
				truncated = true
				break
			}
			for _, p := range n.c.ParentHashes {
				if err := w.push(p, revInteresting); err != nil {
					return nil, false, err
				}
			}
		}
		slop = w.stillInteresting(n.when, slop)
		if slop == 0 {
			break
		}
	}
	// Commits an uninteresting path reached later drop out
	kept := out[:0]
	for _, c := range out {
		if w.flags[c.Hash]&revUninteresting == 0 {
			kept = append(kept, c)
		}
	}
	return kept, truncated, nil
}

type revWalk struct {
	st          storer.EncodedObjectStorer
	heap        revHeap
	flags       map[plumbing.Hash]revFlag
	parents     map[plumbing.Hash][]plumbing.Hash
	inHeap      map[plumbing.Hash]bool
	interesting int
	seq         int
}

// Flags a commit and every loaded ancestor uninteresting
func (w *revWalk) markUninteresting(h plumbing.Hash) {
	stack := []plumbing.Hash{h}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		f := w.flags[x]
		if f&revUninteresting != 0 {
			continue
		}
		w.flags[x] = f | revUninteresting
		if w.inHeap[x] && f&revInteresting != 0 {
			w.interesting--
		}
		stack = append(stack, w.parents[x]...)
	}
}

// Queues a commit once, uninteresting always wins over interesting
func (w *revWalk) push(h plumbing.Hash, flag revFlag) error {
	if h.IsZero() {
		return nil
	}
	if flag == revUninteresting {
		w.markUninteresting(h)
	}
	if _, loaded := w.parents[h]; loaded {
		return nil
	}
	c, err := peelCommit(w.st, h)
	if err != nil {
		return err
	}
	if c.Hash != h {
		// Peeled tags flag their commit instead
		return w.push(c.Hash, flag)
	}
	w.parents[h] = c.ParentHashes
	if w.flags[h]&revUninteresting == 0 {
		w.flags[h] |= revInteresting
		w.interesting++
	}
	w.seq++
	heap.Push(&w.heap, &revNode{c: c, when: c.Committer.When.Unix(), seq: w.seq})
	w.inHeap[h] = true
	return nil
}

// Git's slop rule, walks on through skewed dates
func (w *revWalk) stillInteresting(when int64, slop int) int {
	if w.heap.Len() == 0 {
		return 0
	}
	if when <= w.heap.peekWhen() {
		return revListSlop
	}
	if w.interesting > 0 {
		return revListSlop
	}
	return slop - 1
}

// Children before parents, newest first among the ready ones
func TopoOrder(commits []*object.Commit) []*object.Commit {
	if len(commits) < 2 {
		return commits
	}
	inSet := make(map[plumbing.Hash]*object.Commit, len(commits))
	for _, c := range commits {
		inSet[c.Hash] = c
	}
	children := make(map[plumbing.Hash]int, len(commits))
	for _, c := range commits {
		for _, p := range c.ParentHashes {
			if _, ok := inSet[p]; ok {
				children[p]++
			}
		}
	}
	ready := revHeap{}
	seq := 0
	for _, c := range commits {
		if children[c.Hash] == 0 {
			seq++
			heap.Push(&ready, &revNode{c: c, when: c.Committer.When.Unix(), seq: seq})
		}
	}
	out := make([]*object.Commit, 0, len(commits))
	for ready.Len() > 0 {
		n := heap.Pop(&ready).(*revNode)
		out = append(out, n.c)
		for _, p := range n.c.ParentHashes {
			pc, ok := inSet[p]
			if !ok {
				continue
			}
			children[p]--
			if children[p] == 0 {
				seq++
				heap.Push(&ready, &revNode{c: pc, when: pc.Committer.When.Unix(), seq: seq})
			}
		}
	}
	return out
}
