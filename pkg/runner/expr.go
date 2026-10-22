package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/nickheyer/distroface/pkg/expr"
)

// Every context the runner can resolve
type exprContext struct {
	github    map[string]any
	env       map[string]string
	vars      map[string]string
	secrets   map[string]string
	needs     map[string]any
	matrix    map[string]any
	steps     map[string]any
	inputs    map[string]any
	job       map[string]any
	runner    map[string]any
	strategy  map[string]any
	status    string
	workspace string
	// Workspace view that cannot leave the job dir
	workspaceFS fs.FS
}

func (c *exprContext) Lookup(name string) (any, error) {
	switch name {
	case "github":
		return orMap(c.github), nil
	case "env":
		return expr.StringMap(c.env), nil
	case "vars":
		return expr.StringMap(c.vars), nil
	case "secrets":
		return expr.StringMap(c.secrets), nil
	case "needs":
		return orMap(c.needs), nil
	case "matrix":
		return orMap(c.matrix), nil
	case "steps":
		return orMap(c.steps), nil
	case "inputs":
		return orMap(c.inputs), nil
	case "job":
		return orMap(c.job), nil
	case "runner":
		return orMap(c.runner), nil
	case "strategy":
		return orMap(c.strategy), nil
	}
	return nil, fmt.Errorf("unknown context %q", name)
}

func (c *exprContext) JobStatus() string { return c.status }

func (c *exprContext) HashFiles(patterns []string) (string, error) {
	return hashFiles(c.workspaceFS, c.workspace, patterns), nil
}

// Copy with extra env layered on top, step env for with and run
func (c *exprContext) withEnv(extra map[string]string) *exprContext {
	out := *c
	out.env = make(map[string]string, len(c.env)+len(extra))
	for k, v := range c.env {
		out.env[k] = v
	}
	for k, v := range extra {
		out.env[k] = v
	}
	return &out
}

func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Empty condition means success(), only named status functions override it
func evalCondition(src string, c *exprContext) (bool, error) {
	return expr.Condition(src, c)
}

// Replaces every expression, the first failure is the error
func interpolate(s string, c *exprContext) (string, error) {
	return expr.Interpolate(s, c)
}

func interpolateMap(m map[string]string, c *exprContext) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, err := interpolate(v, c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

func stringify(v any) string { return expr.Stringify(v) }

func truthy(v any) bool { return expr.Truthy(v) }

func toNumber(v any) float64 { return expr.ToNumber(v) }

// Evaluates one expression, wrapper optional
func evalExpr(src string, c *exprContext) (any, error) {
	return expr.Eval(src, c)
}

// Sha256 over sorted per file digests, empty without matches
func hashFiles(fsys fs.FS, root string, patterns []string) string {
	if fsys == nil {
		if root == "" {
			return ""
		}
		fsys = os.DirFS(root)
	}
	var files []string
	seen := map[string]bool{}
	for _, pat := range patterns {
		pat = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pat)), "./")
		if pat == "" {
			continue
		}
		matches, err := doublestar.Glob(fsys, pat, doublestar.WithFilesOnly())
		if err != nil {
			continue
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	total := sha256.New()
	for _, f := range files {
		h := sha256.New()
		fh, err := fsys.Open(f)
		if err != nil {
			continue
		}
		_, _ = io.Copy(h, fh)
		fh.Close()
		total.Write(h.Sum(nil))
	}
	return hex.EncodeToString(total.Sum(nil))
}
