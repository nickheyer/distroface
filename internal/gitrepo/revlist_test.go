package gitrepo

import (
	"context"
	"testing"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"
)

type revFixture struct {
	t    *testing.T
	st   *memory.Storage
	tree plumbing.Hash
}

func newRevFixture(t *testing.T) *revFixture {
	t.Helper()
	st := memory.NewStorage()
	tree := &object.Tree{}
	obj := st.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		t.Fatal(err)
	}
	h, err := st.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	return &revFixture{t: t, st: st, tree: h}
}

// Commit at a unix second with the given parents
func (f *revFixture) commit(when int64, parents ...plumbing.Hash) plumbing.Hash {
	f.t.Helper()
	sig := object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(when, 0)}
	c := &object.Commit{Author: sig, Committer: sig, Message: "c", TreeHash: f.tree, ParentHashes: parents}
	obj := f.st.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		f.t.Fatal(err)
	}
	h, err := f.st.SetEncodedObject(obj)
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func hashes(commits []*object.Commit) []plumbing.Hash {
	out := make([]plumbing.Hash, 0, len(commits))
	for _, c := range commits {
		out = append(out, c.Hash)
	}
	return out
}

func sameHashes(t *testing.T, got []plumbing.Hash, want ...plumbing.Hash) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d commits %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %s want %s (all %v)", i, got[i], want[i], got)
		}
	}
}

func TestRevListLinear(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(10)
	b := f.commit(20, a)
	c := f.commit(30, b)
	d := f.commit(40, c)
	out, truncated, err := RevList(context.Background(), f.st, []plumbing.Hash{d}, []plumbing.Hash{b}, 0)
	if err != nil || truncated {
		t.Fatalf("err %v truncated %v", err, truncated)
	}
	sameHashes(t, hashes(out), d, c)
}

func TestRevListExcludesAncestorsReachedAroundTheTip(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(10)
	b := f.commit(20, a)
	c := f.commit(30, b)
	f1 := f.commit(25, b)
	f2 := f.commit(35, f1)
	m := f.commit(40, c, f2)
	out, _, err := RevList(context.Background(), f.st, []plumbing.Hash{m}, []plumbing.Hash{c}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A plain ignore list would also return b and a through the feature side
	sameHashes(t, hashes(out), m, f2, f1)
}

func TestRevListNewBranchAgainstEveryTip(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(10)
	b := f.commit(20, a)
	c := f.commit(30, b)
	tag := f.commit(15, a)
	n1 := f.commit(50, b)
	n2 := f.commit(60, n1)
	out, _, err := RevList(context.Background(), f.st, []plumbing.Hash{n2}, []plumbing.Hash{c, tag}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameHashes(t, hashes(out), n2, n1)
}

func TestRevListLimitTruncates(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(10)
	b := f.commit(20, a)
	c := f.commit(30, b)
	out, truncated, err := RevList(context.Background(), f.st, []plumbing.Hash{c}, nil, 2)
	if err != nil || !truncated {
		t.Fatalf("err %v truncated %v", err, truncated)
	}
	sameHashes(t, hashes(out), c, b)
	out, truncated, err = RevList(context.Background(), f.st, []plumbing.Hash{c}, nil, 0)
	if err != nil || truncated {
		t.Fatalf("err %v truncated %v", err, truncated)
	}
	sameHashes(t, hashes(out), c, b, a)
}

func TestRevListSurvivesClockSkew(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(100)
	b := f.commit(300, a)
	c := f.commit(200, b)
	out, _, err := RevList(context.Background(), f.st, []plumbing.Hash{c}, []plumbing.Hash{b}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameHashes(t, hashes(out), c)

	// An emitted commit that an uninteresting path reaches later is dropped
	p := f.commit(150)
	x := f.commit(200, p)
	h := f.commit(100, p)
	out, _, err = RevList(context.Background(), f.st, []plumbing.Hash{x}, []plumbing.Hash{h}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameHashes(t, hashes(out), x)
}

func TestRevListPeelsAnnotatedTags(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(10)
	b := f.commit(20, a)
	c := f.commit(30, b)
	tag := &object.Tag{Name: "v1", Tagger: object.Signature{Name: "t", When: time.Unix(20, 0)}, Message: "v1\n", TargetType: plumbing.CommitObject, Target: b}
	obj := f.st.NewEncodedObject()
	if err := tag.Encode(obj); err != nil {
		t.Fatal(err)
	}
	th, err := f.st.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := RevList(context.Background(), f.st, []plumbing.Hash{c}, []plumbing.Hash{th}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sameHashes(t, hashes(out), c)
}

func TestTopoOrderIgnoresDates(t *testing.T) {
	f := newRevFixture(t)
	a := f.commit(300)
	b := f.commit(100, a)
	c := f.commit(200, b)
	commits := make([]*object.Commit, 0, 3)
	for _, h := range []plumbing.Hash{a, b, c} {
		cm, err := object.GetCommit(f.st, h)
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, cm)
	}
	sameHashes(t, hashes(TopoOrder(commits)), c, b, a)
}
