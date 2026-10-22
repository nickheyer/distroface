package runner

import (
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/uuid"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Zips the matched files and uploads them as one artifact
func (j *job) uploadArtifact(ctx context.Context, with map[string]string, out io.Writer) error {
	name := strings.TrimSpace(with["name"])
	if name == "" {
		name = "artifact"
	}
	files, err := j.collectFiles(with["path"], boolInput(with["include-hidden-files"]))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		msg := "No files were found with the provided path: " + strings.TrimSpace(with["path"])
		switch strings.ToLower(strings.TrimSpace(with["if-no-files-found"])) {
		case "ignore":
		case "error":
			return errors.New(msg)
		default:
			fmt.Fprintf(out, "##[warning]%s\n", msg)
		}
		return nil
	}
	zipPath := filepath.Join(j.tempDir, "upload-"+uuid.NewString()[:8]+".zip")
	zf, err := j.openFile(zipPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = j.remove(zipPath) }()
	defer zf.Close()
	total, err := j.writeArtifactZip(zf, commonDir(files), files, intInput(with["compression-level"], 6))
	if err != nil {
		return err
	}
	packed, err := zf.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := zf.Seek(0, io.SeekStart); err != nil {
		return err
	}
	retention := intInput(with["retention-days"], 0)
	if _, err := j.r.client.UploadArtifact(ctx, j.task.JobId, j.task.Token, name, zf, int32(len(files)), int32(retention), boolInput(with["overwrite"])); err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	fmt.Fprintf(out, "Uploaded %d file(s), %d bytes (%d zipped), to artifact %q\n", len(files), total, packed, name)
	return nil
}

// Entries are named relative to root, level 0 stores without deflate
func (j *job) writeArtifactZip(w io.Writer, root string, files []string, level int) (int64, error) {
	zw := zip.NewWriter(w)
	method := uint16(zip.Deflate)
	if level <= 0 {
		method = zip.Store
	} else if level != flate.DefaultCompression {
		zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
			return flate.NewWriter(out, min(level, flate.BestCompression))
		})
	}
	var total int64
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		fh, err := j.openUpload(f)
		if err != nil {
			return 0, err
		}
		hdr := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: method}
		if info, err := fh.Stat(); err == nil {
			hdr.Modified = info.ModTime()
			hdr.SetMode(info.Mode().Perm())
		}
		entry, err := zw.CreateHeader(hdr)
		if err == nil {
			var n int64
			n, err = io.Copy(entry, fh)
			total += n
		}
		fh.Close()
		if err != nil {
			return 0, fmt.Errorf("pack %s: %w", rel, err)
		}
	}
	return total, zw.Close()
}

// Job dir files open through the root, others as is
func (j *job) openUpload(hostPath string) (*os.File, error) {
	if _, err := j.rel(hostPath); err == nil && j.root != nil {
		return j.open(hostPath)
	}
	if j.confined {
		return nil, fmt.Errorf("path %q is outside the job directory", hostPath)
	}
	return os.Open(hostPath)
}

// Expands upload patterns against the host side mount
func (j *job) collectFiles(spec string, hidden bool) ([]string, error) {
	var include, exclude []string
	for _, line := range strings.Split(spec, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pat, err := j.hostPattern(strings.TrimPrefix(line, "!"))
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "!") {
			exclude = append(exclude, pat)
		} else {
			include = append(include, pat)
		}
	}
	seen := map[string]bool{}
	var files []string
	for _, pat := range include {
		if info, err := os.Stat(pat); err == nil {
			if info.IsDir() {
				pat = filepath.Join(pat, "**")
			} else {
				if !seen[pat] {
					seen[pat] = true
					files = append(files, pat)
				}
				continue
			}
		}
		matches, err := doublestar.FilepathGlob(pat, doublestar.WithFilesOnly())
		if err != nil {
			return nil, fmt.Errorf("bad pattern %q: %w", pat, err)
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	var kept []string
	for _, f := range files {
		skip := false
		for _, ex := range exclude {
			if ok, _ := doublestar.PathMatch(filepath.ToSlash(ex), filepath.ToSlash(f)); ok {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, f)
		}
	}
	if !hidden {
		kept = dropHidden(kept)
	}
	for _, f := range kept {
		if !j.realInside(f) {
			return nil, fmt.Errorf("path %q resolves outside the job directory", j.pathOf(f))
		}
	}
	sort.Strings(kept)
	return kept, nil
}

// Drops files with a dot segment below the common root
func dropHidden(files []string) []string {
	root := commonDir(files)
	var kept []string
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		hidden := false
		for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.HasPrefix(seg, ".") && seg != "." && seg != ".." {
				hidden = true
				break
			}
		}
		if !hidden {
			kept = append(kept, f)
		}
	}
	return kept
}

// Picks run artifacts for the download inputs
type artifactSelector struct {
	name    string
	pattern string
	merge   bool
}

// Whether an artifact of this name is wanted
func (s artifactSelector) matches(name string) bool {
	switch {
	case s.name != "":
		return name == s.name
	case s.pattern != "":
		ok, _ := doublestar.Match(s.pattern, name)
		return ok
	}
	return true
}

// Subdirectory an artifact unpacks into, empty for the destination itself
func (s artifactSelector) dir(name string) string {
	if s.name != "" || s.merge {
		return ""
	}
	return name
}

func (j *job) downloadArtifact(ctx context.Context, with map[string]string, out io.Writer) error {
	sel := artifactSelector{name: strings.TrimSpace(with["name"]), pattern: strings.TrimSpace(with["pattern"]), merge: boolInput(with["merge-multiple"])}
	dest := j.workspace
	if p := strings.TrimSpace(with["path"]); p != "" {
		var err error
		if dest, err = j.hostPattern(p); err != nil {
			return err
		}
	}
	all, err := j.r.client.ListTaskArtifacts(ctx, j.task.JobId)
	if err != nil {
		return err
	}
	var picked []*v1.RunArtifact
	for _, a := range all {
		if sel.matches(a.Name) && a.DownloadUrl != "" {
			picked = append(picked, a)
		}
	}
	if len(picked) == 0 {
		switch {
		case sel.name != "":
			return fmt.Errorf("no artifact named %q was found for this run", sel.name)
		case sel.pattern != "":
			fmt.Fprintf(out, "##[warning]No artifacts matched pattern %q\n", sel.pattern)
		default:
			fmt.Fprintln(out, "No artifacts were found for this run")
		}
		return nil
	}
	var count int
	var size int64
	for _, a := range picked {
		target := dest
		if dir := sel.dir(a.Name); dir != "" {
			target = filepath.Join(dest, dir)
		}
		n, bytes, err := j.fetchArtifact(ctx, a, target)
		if err != nil {
			return fmt.Errorf("download %s: %w", a.Name, err)
		}
		count += n
		size += bytes
	}
	fmt.Fprintf(out, "Downloaded %d file(s) of %d artifact(s), %d bytes, into %s\n", count, len(picked), size, j.pathOf(dest))
	return nil
}

// Streams one artifact zip to a temp file and unpacks it
func (j *job) fetchArtifact(ctx context.Context, a *v1.RunArtifact, dest string) (int, int64, error) {
	body, err := j.r.client.DownloadArtifact(ctx, j.task.Token, a.DownloadUrl)
	if err != nil {
		return 0, 0, err
	}
	tmpPath := filepath.Join(j.tempDir, "artifact-"+uuid.NewString()[:8]+".zip")
	tmp, err := j.openFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		body.Close()
		return 0, 0, err
	}
	defer func() { _ = j.remove(tmpPath) }()
	defer tmp.Close()
	size, err := io.Copy(tmp, body)
	_ = body.Close()
	if err != nil {
		return 0, 0, err
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return 0, 0, fmt.Errorf("artifact archive: %w", err)
	}
	legacy := "actions-" + stringify(j.github["run_number"]) + "/" + sanitizeArtifactName(a.Name) + "/"
	count, err := j.extractArtifactZip(zr, dest, legacy)
	return count, size, err
}

// Unpacks entries under dest, pre zip uploads carried a version and name prefix
func (j *job) extractArtifactZip(zr *zip.Reader, dest, legacyPrefix string) (int, error) {
	count := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(f.Name, legacyPrefix)
		rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := j.mkdirInside(filepath.Dir(target)); err != nil {
			return count, err
		}
		w, err := j.createInside(target)
		if err != nil {
			return count, err
		}
		rc, err := f.Open()
		if err != nil {
			w.Close()
			return count, err
		}
		_, err = io.Copy(w, rc)
		_ = rc.Close()
		if mode := f.Mode().Perm(); err == nil && mode&0o111 != 0 {
			_ = w.Chmod(mode)
		}
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// Mirrors artifacts.SanitizePath on the server
func sanitizeArtifactName(name string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, name)
	s = strings.Trim(s, "-.")
	if s == "" {
		return "artifact"
	}
	return s
}

// Integer step input, falling back when blank or malformed
func intInput(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
