package forge

import (
	"errors"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

type spanFixture struct {
	t    *testing.T
	st   *memory.Storage
	repo *gitrepo.Repo
	tree plumbing.Hash
	tick int64
}

func newSpanFixture(t *testing.T) *spanFixture {
	t.Helper()
	st := memory.NewStorage()
	r, err := git.Init(st)
	if err != nil {
		t.Fatal(err)
	}
	tree := &object.Tree{}
	obj := st.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		t.Fatal(err)
	}
	h, err := st.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	return &spanFixture{t: t, st: st, repo: &gitrepo.Repo{Repo: r, Storer: st}, tree: h}
}

func (f *spanFixture) commit(parents ...plumbing.Hash) plumbing.Hash {
	f.t.Helper()
	f.tick++
	sig := object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(1700000000+f.tick, 0)}
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

func (f *spanFixture) branch(name string, h plumbing.Hash) {
	f.t.Helper()
	if err := f.st.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), h)); err != nil {
		f.t.Fatal(err)
	}
}

func merged(head, mergeCommit plumbing.Hash) *db.GitPullRequest {
	at := time.Now()
	return &db.GitPullRequest{HeadSHA: head.String(), BaseBranch: "main", MergedAt: &at, MergeCommitSHA: mergeCommit.String()}
}

// Fork point B0, base moved to B2, branch H1 then H2
func TestPullSpanShapes(t *testing.T) {
	f := newSpanFixture(t)
	b0 := f.commit()
	b1 := f.commit(b0)
	b2 := f.commit(b1)
	h1 := f.commit(b0)
	h2 := f.commit(h1)
	f.branch("main", b2)
	gone := plumbing.NewHash("1111111111111111111111111111111111111111")

	t.Run("open pull measures from the fork point", func(t *testing.T) {
		pr := &db.GitPullRequest{HeadSHA: h2.String(), BaseBranch: "main"}
		span, err := pullSpanOf(f.repo, pr, false)
		if err != nil || span.Base != b0 || span.Head != h2 || span.HeadReplaced {
			t.Fatalf("got %+v %v", span, err)
		}
	})

	t.Run("open pull with missing head", func(t *testing.T) {
		pr := &db.GitPullRequest{HeadSHA: gone.String(), BaseBranch: "main"}
		if _, err := pullSpanOf(f.repo, pr, false); !errors.Is(err, ErrPullHeadMissing) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("open pull with deleted base", func(t *testing.T) {
		pr := &db.GitPullRequest{HeadSHA: h2.String(), BaseBranch: "nope"}
		if _, err := pullSpanOf(f.repo, pr, false); !errors.Is(err, ErrPullBaseMissing) {
			t.Fatalf("got %v", err)
		}
	})

	m := f.commit(b2, h2)
	f.branch("main", m)

	t.Run("merge commit with head present", func(t *testing.T) {
		span, err := pullSpanOf(f.repo, merged(h2, m), false)
		if err != nil || span.Base != b0 || span.Head != h2 || span.HeadReplaced {
			t.Fatalf("got %+v %v", span, err)
		}
	})

	t.Run("merge commit with head gone uses the second parent", func(t *testing.T) {
		span, err := pullSpanOf(f.repo, merged(gone, m), false)
		if err != nil || span.Base != b0 || span.Head != h2 || !span.HeadReplaced {
			t.Fatalf("got %+v %v", span, err)
		}
	})

	t.Run("merged with head gone and merge commit gone", func(t *testing.T) {
		if _, err := pullSpanOf(f.repo, merged(gone, gone), false); !errors.Is(err, ErrPullHeadMissing) {
			t.Fatalf("got %v", err)
		}
	})

	s := f.commit(b2)
	t.Run("squash with head present", func(t *testing.T) {
		span, err := pullSpanOf(f.repo, merged(h2, s), false)
		if err != nil || span.Base != b0 || span.Head != h2 {
			t.Fatalf("got %+v %v", span, err)
		}
	})

	t.Run("squash with head gone diffs the squash commit", func(t *testing.T) {
		span, err := pullSpanOf(f.repo, merged(gone, s), false)
		if err != nil || span.Base != b2 || span.Head != s || !span.HeadReplaced {
			t.Fatalf("got %+v %v", span, err)
		}
	})

	t.Run("stored merge base wins when trusted", func(t *testing.T) {
		// A base that has not absorbed the head yet, main already has
		f.branch("dev", b2)
		pr := &db.GitPullRequest{HeadSHA: h2.String(), BaseBranch: "dev", MergeBaseSHA: b1.String()}
		span, err := pullSpanOf(f.repo, pr, true)
		if err != nil || span.Base != b1 {
			t.Fatalf("got %+v %v", span, err)
		}
		span, err = pullSpanOf(f.repo, pr, false)
		if err != nil || span.Base != b0 {
			t.Fatalf("recompute got %+v %v", span, err)
		}
	})

	t.Run("stored zero marker means unrelated histories", func(t *testing.T) {
		pr := &db.GitPullRequest{HeadSHA: h2.String(), BaseBranch: "main", MergeBaseSHA: storedMergeBase(plumbing.ZeroHash)}
		span, err := pullSpanOf(f.repo, pr, true)
		if err != nil || !span.Base.IsZero() || span.Head != h2 {
			t.Fatalf("got %+v %v", span, err)
		}
	})

	t.Run("unrelated histories resolve to the empty tree", func(t *testing.T) {
		orphan := f.commit()
		pr := &db.GitPullRequest{HeadSHA: orphan.String(), BaseBranch: "main"}
		span, err := pullSpanOf(f.repo, pr, false)
		if err != nil || !span.Base.IsZero() || span.Head != orphan {
			t.Fatalf("got %+v %v", span, err)
		}
	})
}
