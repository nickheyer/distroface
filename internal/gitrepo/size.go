package gitrepo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Loose directories touched this recently are rescanned regardless of mtime
const sizeRescanWindow = 2 * time.Second

// Object store byte counts, loose dirs rescanned when moved
type sizeCache struct {
	mu    sync.Mutex
	repos map[string]*repoSize
}

type repoSize struct {
	mu    sync.Mutex
	loose map[string]looseDir
	total int64
}

type looseDir struct {
	mtime time.Time
	bytes int64
}

func newSizeCache() *sizeCache {
	return &sizeCache{repos: map[string]*repoSize{}}
}

func (c *sizeCache) entry(dir string) *repoSize {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.repos[dir]
	if e == nil {
		e = &repoSize{loose: map[string]looseDir{}}
		c.repos[dir] = e
	}
	return e
}

// Drops every entry under a repository directory
func (c *sizeCache) forget(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.repos {
		if strings.HasPrefix(k, prefix) {
			delete(c.repos, k)
		}
	}
}

// Bytes under an objects directory, full stats every object
func (c *sizeCache) measure(dir string, full bool) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.forget(dir)
			return 0, nil
		}
		return 0, err
	}
	e := c.entry(dir)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	loose := make(map[string]looseDir, len(e.loose))
	var total int64
	for _, ent := range entries {
		name := ent.Name()
		switch {
		case ent.IsDir() && len(name) == 2 && isHexDir(name):
			info, err := ent.Info()
			if err != nil {
				continue
			}
			mt := info.ModTime()
			if !full {
				if prev, ok := e.loose[name]; ok && prev.mtime.Equal(mt) && now.Sub(mt) > sizeRescanWindow {
					loose[name] = prev
					total += prev.bytes
					continue
				}
			}
			bytes, err := dirBytes(filepath.Join(dir, name))
			if err != nil {
				return 0, err
			}
			loose[name] = looseDir{mtime: mt, bytes: bytes}
			total += bytes
		case ent.IsDir():
			bytes, err := treeBytes(filepath.Join(dir, name))
			if err != nil {
				return 0, err
			}
			total += bytes
		default:
			info, err := ent.Info()
			if err != nil {
				continue
			}
			total += info.Size()
		}
	}
	e.loose, e.total = loose, total
	return total, nil
}

func isHexDir(name string) bool {
	for i := range len(name) {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Bytes of the files directly inside a directory
func dirBytes(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total, nil
}

// Bytes of every file below a directory
func treeBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
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
