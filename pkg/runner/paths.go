package runner

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Host path for an executor path, confined to the job dir in containers
func (j *job) hostPattern(p string) (string, error) {
	p = filepath.ToSlash(p)
	root := filepath.ToSlash(j.pathOf(j.dir))
	var host string
	switch {
	case root != "" && (p == root || strings.HasPrefix(p, root+"/")):
		host = filepath.Join(j.dir, filepath.FromSlash(strings.TrimPrefix(p, root)))
	case path.IsAbs(p):
		host = filepath.FromSlash(p)
	default:
		host = filepath.Join(j.workspace, filepath.FromSlash(p))
	}
	if j.confined && !within(j.dir, host) {
		return "", fmt.Errorf("path %q is outside the job directory", p)
	}
	return host, nil
}

// Whether target sits under root after cleaning, symlinks not followed
func within(root, target string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Symlink resolved containment, always true outside containers
func (j *job) realInside(p string) bool {
	if !j.confined {
		return true
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	root, err := filepath.EvalSymlinks(j.dir)
	if err != nil {
		return false
	}
	return within(root, resolved)
}

// Creates a directory in the job, refusing symlinked escapes
func (j *job) mkdirInside(dir string) error {
	if _, err := j.rel(dir); err == nil && j.root != nil {
		return j.mkdirAll(dir)
	}
	if j.confined {
		return fmt.Errorf("path %q is outside the job directory", j.pathOf(dir))
	}
	return os.MkdirAll(dir, 0o777)
}

// Opens a job file for writing, replacing a symlink leaf
func (j *job) createInside(target string) (*os.File, error) {
	if _, err := j.rel(target); err == nil && j.root != nil {
		if fi, err := j.lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = j.remove(target)
		}
		return j.openFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	}
	if j.confined {
		return nil, fmt.Errorf("path %q is outside the job directory", j.pathOf(target))
	}
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		os.Remove(target)
	}
	return os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
}

func commonDir(files []string) string {
	if len(files) == 0 {
		return ""
	}
	dir := filepath.Dir(files[0])
	for _, f := range files[1:] {
		for !strings.HasPrefix(f, dir+string(filepath.Separator)) && dir != filepath.Dir(dir) {
			dir = filepath.Dir(dir)
		}
	}
	return dir
}
