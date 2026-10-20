package runner

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/uuid"
)

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
	root := commonDir(files)
	var total int64
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		fh, err := j.openUpload(f)
		if err != nil {
			return err
		}
		info, _ := fh.Stat()
		_, err = j.r.client.UploadArtifact(ctx, j.task.JobId, j.task.Token, name, filepath.ToSlash(rel), fh)
		fh.Close()
		if err != nil {
			return fmt.Errorf("upload %s: %w", rel, err)
		}
		if info != nil {
			total += info.Size()
		}
	}
	fmt.Fprintf(out, "Uploaded %d file(s), %d bytes, to artifact %q\n", len(files), total, name)
	return nil
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

// Decides where one artifact file lands for the download inputs
type artifactSelector struct {
	name    string
	pattern string
	merge   bool
}

// Target path of a zip entry, false when unwanted
func (s artifactSelector) target(entry string) (string, bool) {
	artifact, rest, found := strings.Cut(entry, "/")
	if !found || rest == "" {
		return "", false
	}
	switch {
	case s.name != "":
		if artifact != sanitizeArtifactName(s.name) {
			return "", false
		}
		return rest, true
	case s.pattern != "":
		if ok, _ := doublestar.Match(s.pattern, artifact); !ok {
			return "", false
		}
	}
	if s.merge {
		return rest, true
	}
	return artifact + "/" + rest, true
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
	ns, repo, _ := strings.Cut(j.task.RepoFullName, "/")
	version := "actions-" + stringify(j.github["run_number"])
	prefix := ""
	if sel.name != "" {
		prefix = sanitizeArtifactName(sel.name) + "/"
	}
	body, err := j.r.client.DownloadArtifacts(ctx, j.task.Token, ns, repo, version, prefix)
	if errors.Is(err, errNoArtifacts) {
		if sel.name != "" {
			return fmt.Errorf("no artifact named %q was found for this run", sel.name)
		}
		fmt.Fprintln(out, "No artifacts were found for this run")
		return nil
	}
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(j.tempDir, "artifact-"+uuid.NewString()[:8]+".zip")
	tmp, err := j.openFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		body.Close()
		return err
	}
	defer func() { _ = j.remove(tmpPath) }()
	defer tmp.Close()
	size, err := io.Copy(tmp, body)
	_ = body.Close()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return fmt.Errorf("artifact archive: %w", err)
	}
	count := 0
	names := map[string]bool{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasPrefix(f.Name, version+"/") {
			continue
		}
		rel, ok := sel.target(strings.TrimPrefix(f.Name, version+"/"))
		if !ok {
			continue
		}
		names[strings.SplitN(strings.TrimPrefix(f.Name, version+"/"), "/", 2)[0]] = true
		rel = path.Clean(rel)
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := j.mkdirInside(filepath.Dir(target)); err != nil {
			return err
		}
		w, err := j.createInside(target)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			w.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		_ = rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		count++
	}
	if count == 0 && sel.name != "" {
		return fmt.Errorf("no artifact named %q was found for this run", sel.name)
	}
	if count == 0 && sel.pattern != "" {
		fmt.Fprintf(out, "##[warning]No artifacts matched pattern %q\n", sel.pattern)
		return nil
	}
	fmt.Fprintf(out, "Downloaded %d file(s) of %d artifact(s), %d bytes, into %s\n", count, len(names), size, j.pathOf(dest))
	return nil
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
