package gitrepo

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/osfs"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/cache"
	formatcfg "github.com/go-git/go-git/v6/plumbing/format/config"
	"github.com/go-git/go-git/v6/storage"
	"github.com/go-git/go-git/v6/storage/filesystem"
	"github.com/go-git/go-git/v6/x/fdpool"
)

// Repo names are one path segment and never claim the git or wiki suffixes
var NamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// Returned for names outside the pattern or ending in a reserved suffix
var ErrInvalidName = errors.New("invalid repository name")

// Returned when a repository directory is missing
var ErrRepoNotFound = errors.New("git repository not found on disk")

// Reports whether a repository name is acceptable
func ValidName(name string) bool {
	if !NamePattern.MatchString(name) {
		return false
	}
	return !strings.HasSuffix(name, ".git") && !strings.HasSuffix(name, ".wiki")
}

// Engine owns the bare repository tree and the shared go-git resources
type Engine struct {
	root     string
	fs       billy.Filesystem
	cache    cache.Object
	pool     *fdpool.Pool
	locks    *lockTable
	transfer *limiter
	keysDir  string
	repos    string
	// Repositories whose directory is moving right now
	renaming sync.Map
}

// Flags a repository as moving until the returned func runs
func (e *Engine) MarkRenaming(id string) func() {
	e.renaming.Store(id, struct{}{})
	return func() { e.renaming.Delete(id) }
}

// Reports whether a repository directory is moving
func (e *Engine) Renaming(id string) bool {
	_, ok := e.renaming.Load(id)
	return ok
}

// Opens the storage root, creating it, and wires the shared caches
func NewEngine(storagePath, dataDir string) (*Engine, error) {
	repos := filepath.Join(storagePath, "repos")
	if err := os.MkdirAll(repos, 0o755); err != nil {
		return nil, fmt.Errorf("creating git storage: %w", err)
	}
	keys := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		return nil, fmt.Errorf("creating keys directory: %w", err)
	}
	return &Engine{
		root:     storagePath,
		repos:    repos,
		fs:       osfs.New(repos, osfs.WithBoundOS()),
		cache:    cache.NewObjectLRU(256 * cache.MiByte),
		pool:     fdpool.New(1024),
		locks:    newLockTable(),
		transfer: newLimiter(func() int { return 32 }),
		keysDir:  keys,
	}, nil
}

// Directory holding every bare repository
func (e *Engine) ReposDir() string { return e.repos }

// Directory holding host and signing keys
func (e *Engine) KeysDir() string { return e.keysDir }

// Installs the live transfer cap supplier
func (e *Engine) SetTransferLimit(limit func() int) {
	e.transfer.limit = limit
}

// Transfer cap gate shared by every pack operation
func (e *Engine) Transfers() *limiter { return e.transfer }

// Relative directory of a repository or its wiki inside the root
func RepoDir(namespace, name string, wiki bool) string {
	if wiki {
		return namespace + "/" + name + ".wiki.git"
	}
	return namespace + "/" + name + ".git"
}

// Absolute path of a repository directory, names validated by the caller
func (e *Engine) AbsPath(namespace, name string, wiki bool) string {
	return filepath.Join(e.repos, filepath.FromSlash(RepoDir(namespace, name, wiki)))
}

// Reports whether a repository directory exists
func (e *Engine) Exists(namespace, name string, wiki bool) bool {
	info, err := os.Stat(e.AbsPath(namespace, name, wiki))
	return err == nil && info.IsDir()
}

// Opens a storer over one repository, the closer releases pack descriptors
func (e *Engine) Storer(namespace, name string, wiki bool) (storage.Storer, func(), error) {
	if !ValidName(name) || !NamePattern.MatchString(namespace) {
		return nil, nil, ErrInvalidName
	}
	if !e.Exists(namespace, name, wiki) {
		return nil, nil, ErrRepoNotFound
	}
	fs, err := e.fs.Chroot(RepoDir(namespace, name, wiki))
	if err != nil {
		return nil, nil, err
	}
	st := filesystem.NewStorageWithOptions(fs, e.cache, filesystem.Options{
		LargeObjectThreshold: 32 << 20,
		Pool:                 e.pool,
	})
	closer := func() {
		if c, ok := any(st.ObjectStorage).(io.Closer); ok {
			_ = c.Close()
		}
	}
	return st, closer, nil
}

// Opens a repository handle, the closer releases pack descriptors
func (e *Engine) Open(namespace, name string, wiki bool) (*git.Repository, func(), error) {
	st, closer, err := e.Storer(namespace, name, wiki)
	if err != nil {
		return nil, nil, err
	}
	repo, err := git.Open(st, nil)
	if err != nil {
		closer()
		return nil, nil, err
	}
	return repo, closer, nil
}

// Creates an empty bare repository with the given default branch
func (e *Engine) Init(namespace, name string, wiki bool, defaultBranch, objectFormat string) error {
	if !ValidName(name) || !NamePattern.MatchString(namespace) {
		return ErrInvalidName
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	path := e.AbsPath(namespace, name, wiki)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("repository directory already exists")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	opts := []git.InitOption{git.WithDefaultBranch(plumbing.NewBranchReferenceName(defaultBranch))}
	if objectFormat == string(formatcfg.SHA256) {
		opts = append(opts, git.WithObjectFormat(formatcfg.SHA256))
	}
	if _, err := git.PlainInit(path, true, opts...); err != nil {
		_ = os.RemoveAll(path)
		return err
	}
	return nil
}

// Removes a repository and its wiki from disk
func (e *Engine) Delete(namespace, name string) error {
	if !ValidName(name) || !NamePattern.MatchString(namespace) {
		return ErrInvalidName
	}
	for _, wiki := range []bool{false, true} {
		if err := os.RemoveAll(e.AbsPath(namespace, name, wiki)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	parent := filepath.Join(e.repos, namespace)
	if entries, err := os.ReadDir(parent); err == nil && len(entries) == 0 {
		_ = os.Remove(parent)
	}
	return nil
}

// Moves a repository and its wiki to a new address on disk
func (e *Engine) Rename(oldNamespace, oldName, newNamespace, newName string) error {
	if !ValidName(newName) || !NamePattern.MatchString(newNamespace) {
		return ErrInvalidName
	}
	if err := os.MkdirAll(filepath.Join(e.repos, newNamespace), 0o755); err != nil {
		return err
	}
	for _, wiki := range []bool{false, true} {
		from := e.AbsPath(oldNamespace, oldName, wiki)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if err := os.Rename(from, e.AbsPath(newNamespace, newName, wiki)); err != nil {
			return err
		}
	}
	return nil
}

// Bytes used by the object store of a repository
func (e *Engine) Size(namespace, name string, wiki bool) (int64, error) {
	var total int64
	err := filepath.WalkDir(filepath.Join(e.AbsPath(namespace, name, wiki), "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// Shared lock table keyed by repository id
type lockTable struct {
	mu    sync.Mutex
	locks map[string]*repoLock
}

// Push serializes writes with fan out, store guards disk
type repoLock struct {
	push  sync.Mutex
	store sync.Mutex
	refs  int
}

func newLockTable() *lockTable {
	return &lockTable{locks: map[string]*repoLock{}}
}

func (t *lockTable) acquire(id string) *repoLock {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.locks[id]
	if !ok {
		l = &repoLock{}
		t.locks[id] = l
	}
	l.refs++
	return l
}

func (t *lockTable) release(id string, l *repoLock) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(t.locks, id)
	}
}

// Writer lock on one repository, released in two phases
type RepoLock struct {
	table      *lockTable
	id         string
	l          *repoLock
	holdsStore bool
	holdsPush  bool
}

// Takes both locks for one write and its fan out
func (e *Engine) LockRepo(id string) *RepoLock {
	l := e.locks.acquire(id)
	l.push.Lock()
	l.store.Lock()
	return &RepoLock{table: e.locks, id: id, l: l, holdsStore: true, holdsPush: true}
}

// Store lock only, safe inside a post receive fan out
func (e *Engine) LockStore(id string) func() {
	l := e.locks.acquire(id)
	l.store.Lock()
	return func() {
		l.store.Unlock()
		e.locks.release(id, l)
	}
}

// Frees the store for fan out, keeps the push lock
func (l *RepoLock) UnlockStore() {
	if l.holdsStore {
		l.holdsStore = false
		l.l.store.Unlock()
	}
}

// Releases every lock still held
func (l *RepoLock) Unlock() {
	l.UnlockStore()
	if l.holdsPush {
		l.holdsPush = false
		l.l.push.Unlock()
		l.table.release(l.id, l.l)
	}
}
