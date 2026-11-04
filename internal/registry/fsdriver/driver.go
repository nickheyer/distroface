// Package fsdriver stores registry content on the local filesystem with
// durability as a setting instead of an upstream constant
package fsdriver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/base"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	"github.com/google/uuid"
)

// Registration name used in the distribution storage block
const Name = "localfs"

// Upstream bounds for concurrent driver calls
const (
	defaultMaxThreads = uint64(100)
	minThreads        = uint64(25)
)

// Sync names how much of each write waits for the disk
type Sync string

const (
	// Upstream behavior, every commit and uncommitted close fsyncs and
	// every small write syncs its directory
	SyncAlways Sync = "always"
	// Committed blobs, manifests and links sync like upstream, upload
	// scratch that a client rebuilds after a crash does not
	SyncCommit Sync = "commit"
	// Nothing syncs, the page cache decides when bytes reach the disk
	SyncNever Sync = "never"
)

// Config spelling to a mode, empty means commit
func ParseSync(s string) (Sync, error) {
	switch mode := Sync(strings.ToLower(strings.TrimSpace(s))); mode {
	case "":
		return SyncCommit, nil
	case SyncAlways, SyncCommit, SyncNever:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown sync mode %q, want always, commit or never", s)
	}
}

// Params configures one driver
type Params struct {
	RootDirectory string
	MaxThreads    uint64
	Sync          Sync
}

func init() {
	factory.Register(Name, driverFactory{})
}

type driverFactory struct{}

func (driverFactory) Create(_ context.Context, parameters map[string]any) (storagedriver.StorageDriver, error) {
	return FromParameters(parameters)
}

// FromParameters reads rootdirectory, maxthreads and sync
func FromParameters(parameters map[string]any) (*Driver, error) {
	var p Params
	if root, ok := parameters["rootdirectory"]; ok {
		p.RootDirectory = fmt.Sprint(root)
	}
	if p.RootDirectory == "" {
		return nil, errors.New("localfs storage needs a rootdirectory")
	}
	threads, err := base.GetLimitFromParameter(parameters["maxthreads"], minThreads, defaultMaxThreads)
	if err != nil {
		return nil, fmt.Errorf("maxthreads config error: %w", err)
	}
	p.MaxThreads = threads
	if raw, ok := parameters["sync"]; ok {
		mode, err := ParseSync(fmt.Sprint(raw))
		if err != nil {
			return nil, err
		}
		p.Sync = mode
	}
	return New(p), nil
}

// Driver is the exported driver, path checks and the concurrency limit
// come from the upstream base wrappers
type Driver struct {
	baseEmbed
}

type baseEmbed struct {
	base.Base
}

func New(p Params) *Driver {
	if p.MaxThreads == 0 {
		p.MaxThreads = defaultMaxThreads
	}
	if p.MaxThreads < minThreads {
		p.MaxThreads = minThreads
	}
	if p.Sync == "" {
		p.Sync = SyncCommit
	}
	return &Driver{baseEmbed{base.Base{
		StorageDriver: base.NewRegulator(newDriver(p.RootDirectory, p.Sync), p.MaxThreads),
	}}}
}

type driver struct {
	root string
	mode Sync
	// Flushes one open file or directory, tests count calls through it
	fsync func(*os.File) error
}

func newDriver(root string, mode Sync) *driver {
	return &driver{root: root, mode: mode, fsync: (*os.File).Sync}
}

// Upload scratch is rebuilt by the client after any crash
func isUploadScratch(p string) bool {
	return strings.Contains(p, "/_uploads/")
}

// Whether a small committed write at this path waits for the disk
func (d *driver) syncContent(p string) bool {
	switch d.mode {
	case SyncAlways:
		return true
	case SyncCommit:
		return !isUploadScratch(p)
	default:
		return false
	}
}

func (d *driver) Name() string {
	return Name
}

func (d *driver) GetContent(_ context.Context, subPath string) ([]byte, error) {
	p, err := os.ReadFile(d.fullPath(subPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: subPath}
		}
		return nil, err
	}
	return p, nil
}

// Writes next to the target and renames over it, never a partial file
func (d *driver) PutContent(ctx context.Context, subPath string, contents []byte) error {
	tempPath := subPath + "." + uuid.NewString() + ".tmp"
	sync := d.syncContent(subPath)
	w, err := d.openWriter(tempPath, false, sync, false)
	if err != nil {
		return err
	}
	if _, err := w.Write(contents); err != nil {
		return errors.Join(err, w.Cancel(ctx))
	}
	if err := w.Commit(ctx); err != nil {
		return errors.Join(err, w.Cancel(ctx))
	}
	if err := w.Close(); err != nil {
		return errors.Join(err, d.Delete(ctx, tempPath))
	}
	if err := d.Move(ctx, tempPath, subPath); err != nil {
		return errors.Join(err, d.Delete(ctx, tempPath))
	}
	if sync {
		return d.syncDir(filepath.Dir(d.fullPath(subPath)))
	}
	return nil
}

func (d *driver) Reader(_ context.Context, subPath string, offset int64) (io.ReadCloser, error) {
	file, err := os.OpenFile(d.fullPath(subPath), os.O_RDONLY, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: subPath}
		}
		return nil, err
	}
	seekPos, err := file.Seek(offset, io.SeekStart)
	if err != nil {
		file.Close()
		return nil, err
	} else if seekPos < offset {
		file.Close()
		return nil, storagedriver.InvalidOffsetError{Path: subPath, Offset: offset}
	}
	return file, nil
}

// Commits reach the disk outside never mode, the file is the blob itself
func (d *driver) Writer(_ context.Context, subPath string, appendMode bool) (storagedriver.FileWriter, error) {
	return d.openWriter(subPath, appendMode, d.mode != SyncNever, d.mode == SyncAlways)
}

func (d *driver) openWriter(subPath string, appendMode, commitSync, closeSync bool) (*fileWriter, error) {
	fullPath := d.fullPath(subPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o777); err != nil {
		return nil, err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if !appendMode {
		// Truncating on open saves the separate ftruncate upstream issues
		flags |= os.O_TRUNC
	}
	fp, err := os.OpenFile(fullPath, flags, 0o666)
	if err != nil {
		return nil, err
	}
	var offset int64
	if appendMode {
		n, err := fp.Seek(0, io.SeekEnd)
		if err != nil {
			fp.Close()
			return nil, err
		}
		offset = n
	}
	return &fileWriter{
		file:       fp,
		size:       offset,
		bw:         bufio.NewWriter(fp),
		fsync:      d.fsync,
		commitSync: commitSync,
		closeSync:  closeSync,
	}, nil
}

func (d *driver) Stat(_ context.Context, subPath string) (storagedriver.FileInfo, error) {
	fi, err := os.Stat(d.fullPath(subPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: subPath}
		}
		return nil, err
	}
	return fileInfo{FileInfo: fi, path: subPath}, nil
}

func (d *driver) List(_ context.Context, subPath string) ([]string, error) {
	dir, err := os.Open(d.fullPath(subPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storagedriver.PathNotFoundError{Path: subPath}
		}
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(names))
	for _, name := range names {
		keys = append(keys, filepath.Join(subPath, name))
	}
	return keys, nil
}

func (d *driver) Move(_ context.Context, sourcePath, destPath string) error {
	source, dest := d.fullPath(sourcePath), d.fullPath(destPath)
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return storagedriver.PathNotFoundError{Path: sourcePath}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
		return err
	}
	return os.Rename(source, dest)
}

func (d *driver) Delete(_ context.Context, subPath string) error {
	fullPath := d.fullPath(subPath)
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return storagedriver.PathNotFoundError{Path: subPath}
		}
		return err
	}
	return os.RemoveAll(fullPath)
}

func (d *driver) RedirectURL(*http.Request, string) (string, error) {
	return "", nil
}

// Native walk, directory types come from readdir and sizes load on
// demand, so a sweep over a hundred thousand blobs skips a stat each
func (d *driver) Walk(ctx context.Context, from string, f storagedriver.WalkFn, options ...func(*storagedriver.WalkOptions)) error {
	opts := &storagedriver.WalkOptions{}
	for _, o := range options {
		o(opts)
	}
	hint := opts.StartAfterHint
	rel, err := filepath.Rel(from, hint)
	if err != nil || strings.HasPrefix(rel, "..") {
		// The hint lies outside from, walk everything when it sorts before
		if strings.ReplaceAll(hint, "/", "\x00") < strings.ReplaceAll(from, "/", "\x00") {
			_, err := d.walk(ctx, from, "", f)
			return err
		}
		return nil
	}
	// The hint lies inside from, resume there and climb back up to from.
	// A hint naming a file or a removed path has nothing below it
	base := hint
	for strings.HasPrefix(base, from) {
		more, err := d.walk(ctx, base, hint, f)
		if err != nil {
			var notFound storagedriver.PathNotFoundError
			if !errors.As(err, &notFound) && !errors.Is(err, syscall.ENOTDIR) {
				return err
			}
			more = true
		}
		if !more || base == from {
			break
		}
		hint = base
		base, _ = filepath.Split(hint)
		if len(base) > 1 {
			base = strings.TrimSuffix(base, "/")
		}
	}
	return nil
}

// Depth first in name order, false means the callback asked to stop
func (d *driver) walk(ctx context.Context, from, hint string, f storagedriver.WalkFn) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	entries, err := os.ReadDir(d.fullPath(from))
	if err != nil {
		if os.IsNotExist(err) {
			return false, storagedriver.PathNotFoundError{Path: from}
		}
		return false, err
	}
	for _, entry := range entries {
		child := filepath.Join(from, entry.Name())
		if child <= hint {
			continue
		}
		info, ok := d.entryInfo(child, entry)
		if !ok {
			continue
		}
		err := f(info)
		switch {
		case err == nil && info.IsDir():
			more, err := d.walk(ctx, child, hint, f)
			if err != nil {
				var notFound storagedriver.PathNotFoundError
				if errors.As(err, &notFound) {
					// Removed between readdir and descent, like upstream ignores it
					continue
				}
				return false, err
			}
			if !more {
				return false, nil
			}
		case errors.Is(err, storagedriver.ErrSkipDir):
		case errors.Is(err, storagedriver.ErrFilledBuffer):
			return false, nil
		case err != nil:
			return false, err
		}
	}
	return true, nil
}

// Symlinks resolve like upstream stat, everything else is lazy
func (d *driver) entryInfo(child string, entry os.DirEntry) (storagedriver.FileInfo, bool) {
	if entry.Type()&os.ModeSymlink != 0 {
		fi, err := os.Stat(d.fullPath(child))
		if err != nil {
			return nil, false
		}
		return fileInfo{FileInfo: fi, path: child}, true
	}
	return &walkInfo{path: child, dir: entry.IsDir(), entry: entry}, true
}

func (d *driver) fullPath(subPath string) string {
	return filepath.Join(d.root, subPath)
}

func (d *driver) syncDir(dir string) (retErr error) {
	f, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sync dir: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to close dir: %w", err))
		}
	}()
	if err := d.fsync(f); err != nil {
		return fmt.Errorf("sync dir: %w", err)
	}
	return nil
}

type fileInfo struct {
	os.FileInfo
	path string
}

var _ storagedriver.FileInfo = fileInfo{}

func (fi fileInfo) Path() string {
	return fi.path
}

func (fi fileInfo) Size() int64 {
	if fi.IsDir() {
		return 0
	}
	return fi.FileInfo.Size()
}

func (fi fileInfo) ModTime() time.Time {
	return fi.FileInfo.ModTime()
}

func (fi fileInfo) IsDir() bool {
	return fi.FileInfo.IsDir()
}

// Walk entry whose size and time stat only when asked
type walkInfo struct {
	path  string
	dir   bool
	entry os.DirEntry
	once  sync.Once
	info  os.FileInfo
}

var _ storagedriver.FileInfo = (*walkInfo)(nil)

func (w *walkInfo) Path() string {
	return w.path
}

func (w *walkInfo) IsDir() bool {
	return w.dir
}

func (w *walkInfo) load() os.FileInfo {
	w.once.Do(func() {
		w.info, _ = w.entry.Info()
	})
	return w.info
}

func (w *walkInfo) Size() int64 {
	if w.dir {
		return 0
	}
	if fi := w.load(); fi != nil {
		return fi.Size()
	}
	return 0
}

func (w *walkInfo) ModTime() time.Time {
	if fi := w.load(); fi != nil {
		return fi.ModTime()
	}
	return time.Time{}
}

type fileWriter struct {
	file       *os.File
	size       int64
	bw         *bufio.Writer
	fsync      func(*os.File) error
	commitSync bool
	closeSync  bool
	closed     bool
	committed  bool
	cancelled  bool
}

func (fw *fileWriter) Write(p []byte) (int, error) {
	if fw.closed {
		return 0, errors.New("already closed")
	} else if fw.committed {
		return 0, errors.New("already committed")
	} else if fw.cancelled {
		return 0, errors.New("already cancelled")
	}
	n, err := fw.bw.Write(p)
	fw.size += int64(n)
	return n, err
}

func (fw *fileWriter) Size() int64 {
	return fw.size
}

func (fw *fileWriter) Close() (retErr error) {
	if fw.closed {
		return errors.New("already closed")
	}
	fw.closed = true
	defer func() {
		if err := fw.file.Close(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	if fw.committed {
		return nil
	}
	if err := fw.bw.Flush(); err != nil {
		return err
	}
	if fw.closeSync {
		return fw.fsync(fw.file)
	}
	return nil
}

func (fw *fileWriter) Cancel(context.Context) error {
	if fw.closed {
		return errors.New("already closed")
	}
	fw.cancelled = true
	fw.closed = true
	_ = fw.file.Close()
	return os.Remove(fw.file.Name())
}

func (fw *fileWriter) Commit(context.Context) error {
	if fw.closed {
		return errors.New("already closed")
	} else if fw.committed {
		return errors.New("already committed")
	} else if fw.cancelled {
		return errors.New("already cancelled")
	}
	if err := fw.bw.Flush(); err != nil {
		return err
	}
	if fw.commitSync {
		if err := fw.fsync(fw.file); err != nil {
			return err
		}
	}
	fw.committed = true
	return nil
}
