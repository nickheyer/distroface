package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage"
)

// Returned when a compare and set ref update loses a race
var ErrRefChanged = errors.New("the branch moved, refresh and try again")

// Returned when an edit touches a path that is a directory or vice versa
var ErrPathConflict = errors.New("path conflicts with an existing entry")

// One file change in a web commit
type TreeEdit struct {
	Path    string
	Mode    filemode.FileMode
	Content io.Reader // nil deletes the path
	Rename  string    // old path moved here
}

// Signs encoded objects, nil disables signing
type Signer interface {
	Sign(ctx context.Context, message io.Reader) ([]byte, error)
}

// Writes a blob and returns its hash
func (r *Repo) WriteBlob(content io.Reader) (plumbing.Hash, error) {
	obj := r.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := io.Copy(w, content); err != nil {
		w.Close()
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return r.Storer.SetEncodedObject(obj)
}

// Writes a tree object
func (r *Repo) WriteTree(entries []object.TreeEntry) (plumbing.Hash, error) {
	sortTreeEntries(entries)
	tree := &object.Tree{Entries: entries}
	obj := r.Storer.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return r.Storer.SetEncodedObject(obj)
}

// Git orders entries by name with directories compared as if slash terminated
func sortTreeEntries(entries []object.TreeEntry) {
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
}

// In memory tree being edited
type treeNode struct {
	entries map[string]object.TreeEntry
	dirs    map[string]*treeNode
	hash    plumbing.Hash
	loaded  bool
}

func newTreeNode(hash plumbing.Hash) *treeNode {
	return &treeNode{entries: map[string]object.TreeEntry{}, dirs: map[string]*treeNode{}, hash: hash}
}

func (r *Repo) loadNode(n *treeNode) error {
	if n.loaded {
		return nil
	}
	n.loaded = true
	if n.hash.IsZero() {
		return nil
	}
	tree, err := r.Repo.TreeObject(n.hash)
	if err != nil {
		return err
	}
	for _, e := range tree.Entries {
		n.entries[e.Name] = e
	}
	return nil
}

func (r *Repo) childNode(n *treeNode, name string) (*treeNode, error) {
	if err := r.loadNode(n); err != nil {
		return nil, err
	}
	if child, ok := n.dirs[name]; ok {
		return child, nil
	}
	if e, ok := n.entries[name]; ok {
		if e.Mode != filemode.Dir {
			return nil, fmt.Errorf("%w: %s", ErrPathConflict, name)
		}
		child := newTreeNode(e.Hash)
		n.dirs[name] = child
		return child, nil
	}
	child := newTreeNode(plumbing.ZeroHash)
	n.dirs[name] = child
	return child, nil
}

// Sets or removes a leaf at a slash separated path
func (r *Repo) setPath(root *treeNode, p string, entry *object.TreeEntry) error {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	node := root
	for _, part := range parts[:len(parts)-1] {
		child, err := r.childNode(node, part)
		if err != nil {
			return err
		}
		node = child
	}
	if err := r.loadNode(node); err != nil {
		return err
	}
	leaf := parts[len(parts)-1]
	if entry == nil {
		delete(node.entries, leaf)
		delete(node.dirs, leaf)
		return nil
	}
	if existing, ok := node.entries[leaf]; ok && existing.Mode == filemode.Dir {
		return fmt.Errorf("%w: %s is a directory", ErrPathConflict, p)
	}
	delete(node.dirs, leaf)
	e := *entry
	e.Name = leaf
	node.entries[leaf] = e
	return nil
}

// Writes modified subtrees bottom up, empty directories vanish
func (r *Repo) flushNode(n *treeNode) (plumbing.Hash, bool, error) {
	if !n.loaded {
		return n.hash, !n.hash.IsZero(), nil
	}
	for name, child := range n.dirs {
		h, keep, err := r.flushNode(child)
		if err != nil {
			return plumbing.ZeroHash, false, err
		}
		if !keep {
			delete(n.entries, name)
			continue
		}
		n.entries[name] = object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: h}
	}
	if len(n.entries) == 0 {
		return plumbing.ZeroHash, false, nil
	}
	entries := make([]object.TreeEntry, 0, len(n.entries))
	for _, e := range n.entries {
		entries = append(entries, e)
	}
	h, err := r.WriteTree(entries)
	return h, true, err
}

// Applies edits to a tree and returns the new root tree hash
func (r *Repo) EditTree(base plumbing.Hash, edits []TreeEdit) (plumbing.Hash, error) {
	root := newTreeNode(base)
	for _, e := range edits {
		if e.Path == "" && e.Rename == "" {
			return plumbing.ZeroHash, fmt.Errorf("edit without a path")
		}
		if e.Rename != "" {
			var moved *object.TreeEntry
			if base != plumbing.ZeroHash {
				tree, err := r.Repo.TreeObject(base)
				if err != nil {
					return plumbing.ZeroHash, err
				}
				te, err := tree.FindEntry(strings.Trim(e.Rename, "/"))
				if err != nil {
					return plumbing.ZeroHash, fmt.Errorf("%w: %s", ErrNotFound, e.Rename)
				}
				moved = te
			}
			if err := r.setPath(root, e.Rename, nil); err != nil {
				return plumbing.ZeroHash, err
			}
			if e.Content == nil && moved != nil {
				if err := r.setPath(root, e.Path, &object.TreeEntry{Mode: moved.Mode, Hash: moved.Hash}); err != nil {
					return plumbing.ZeroHash, err
				}
				continue
			}
		}
		if e.Content == nil {
			if err := r.setPath(root, e.Path, nil); err != nil {
				return plumbing.ZeroHash, err
			}
			continue
		}
		h, err := r.WriteBlob(e.Content)
		if err != nil {
			return plumbing.ZeroHash, err
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

// Fields of a commit to create
type CommitSpec struct {
	Tree      plumbing.Hash
	Parents   []plumbing.Hash
	Message   string
	Author    object.Signature
	Committer object.Signature
	Signer    Signer
}

// Writes a commit object, signing it when a signer is set
func (r *Repo) WriteCommit(ctx context.Context, spec CommitSpec) (plumbing.Hash, error) {
	msg := spec.Message
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	c := &object.Commit{
		Author:       spec.Author,
		Committer:    spec.Committer,
		Message:      msg,
		TreeHash:     spec.Tree,
		ParentHashes: spec.Parents,
	}
	if c.Author.When.IsZero() {
		c.Author.When = time.Now()
	}
	if c.Committer.When.IsZero() {
		c.Committer.When = time.Now()
	}
	if spec.Signer != nil {
		unsigned := r.Storer.NewEncodedObject()
		if err := c.EncodeWithoutSignature(unsigned); err != nil {
			return plumbing.ZeroHash, err
		}
		rd, err := unsigned.Reader()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		sig, err := spec.Signer.Sign(ctx, rd)
		rd.Close()
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("signing commit: %w", err)
		}
		c.Signature = string(sig)
	}
	obj := r.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return r.Storer.SetEncodedObject(obj)
}

// Writes an annotated tag object
func (r *Repo) WriteTag(ctx context.Context, name string, target plumbing.Hash, targetType plumbing.ObjectType, tagger object.Signature, message string, signer Signer) (plumbing.Hash, error) {
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	if tagger.When.IsZero() {
		tagger.When = time.Now()
	}
	t := &object.Tag{Name: name, Tagger: tagger, Message: message, TargetType: targetType, Target: target}
	if signer != nil {
		unsigned := r.Storer.NewEncodedObject()
		if err := t.EncodeWithoutSignature(unsigned); err != nil {
			return plumbing.ZeroHash, err
		}
		rd, err := unsigned.Reader()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		sig, err := signer.Sign(ctx, rd)
		rd.Close()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		t.Signature = string(sig)
	}
	obj := r.Storer.NewEncodedObject()
	if err := t.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return r.Storer.SetEncodedObject(obj)
}

// Compare and set update of a reference, zero old creates, zero new deletes
func (r *Repo) UpdateRef(name plumbing.ReferenceName, old, new plumbing.Hash) error {
	current, err := r.Storer.Reference(name)
	exists := err == nil && current != nil
	if old.IsZero() {
		if exists {
			return ErrRefChanged
		}
	} else {
		if !exists || current.Hash() != old {
			return ErrRefChanged
		}
	}
	if new.IsZero() {
		if !exists {
			return nil
		}
		return r.Storer.RemoveReference(name)
	}
	newRef := plumbing.NewHashReference(name, new)
	if exists {
		if err := r.Storer.CheckAndSetReference(newRef, current); err != nil {
			if errors.Is(err, storage.ErrReferenceHasChanged) {
				return ErrRefChanged
			}
			return err
		}
		return nil
	}
	return r.Storer.SetReference(newRef)
}

// Current hash of a branch, zero when missing
func (r *Repo) BranchHash(branch string) plumbing.Hash {
	ref, err := r.Storer.Reference(plumbing.NewBranchReferenceName(branch))
	if err != nil {
		return plumbing.ZeroHash
	}
	return ref.Hash()
}

// Current hash of a tag ref, zero when missing
func (r *Repo) TagHash(tag string) plumbing.Hash {
	ref, err := r.Storer.Reference(plumbing.NewTagReferenceName(tag))
	if err != nil {
		return plumbing.ZeroHash
	}
	return ref.Hash()
}

// Reads a whole blob into memory
func (r *Repo) BlobContent(h plumbing.Hash) ([]byte, error) {
	blob, err := r.Repo.BlobObject(h)
	if err != nil {
		return nil, err
	}
	rd, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rd); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Tree hash of a commit
func (r *Repo) TreeOf(commit plumbing.Hash) (plumbing.Hash, error) {
	c, err := r.Commit(commit)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return c.TreeHash, nil
}
