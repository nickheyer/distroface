package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	pathpkg "path"
	"strconv"
	"strings"

	"gitea.com/gitea/runner/act/model"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/actions/jobparser"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"go.yaml.in/yaml/v4"
)

// Directories searched for workflow files, first hit wins
var WorkflowDirs = []string{".distroface/workflows", ".gitea/workflows", ".github/workflows"}

// IsWorkflowFile reports whether a path names a workflow file
func IsWorkflowFile(p string) bool {
	ext := strings.ToLower(pathpkg.Ext(p))
	return ext == ".yml" || ext == ".yaml"
}

// WorkflowFile is one file under a workflows directory
type WorkflowFile struct {
	Path   string
	Source string
	// Name as written, the file name without extension otherwise
	Name   string
	Events []*jobparser.Event
	Err    error
}

// ParseWorkflowFile reads a workflow the way the runner will
func ParseWorkflowFile(path string, src []byte) (f *WorkflowFile) {
	f = &WorkflowFile{Path: path, Source: string(src), Name: workflowBaseName(path)}
	defer recoverPlanning(&f.Err)
	wf, err := model.ReadWorkflow(bytes.NewReader(src))
	if err != nil {
		f.Err = err
		return f
	}
	if name := strings.TrimSpace(wf.Name); name != "" {
		f.Name = name
	}
	events, err := jobparser.ParseRawOn(&wf.RawOn)
	if err != nil {
		f.Err = err
		return f
	}
	f.Events = events
	f.Err = validateJobs(wf)
	return f
}

// Act decode failures and evaluator panics become a planning error
func recoverPlanning(err *error) {
	r := recover()
	if r == nil {
		return
	}
	switch v := r.(type) {
	case *jobparser.DecodeError:
		*err = v
	case error:
		*err = fmt.Errorf("planning failed: %w", v)
	default:
		*err = fmt.Errorf("planning failed: %v", v)
	}
}

// Jobs need runs-on and steps, or a workflow to call
func validateJobs(wf *model.Workflow) (err error) {
	defer recoverPlanning(&err)
	if len(wf.Jobs) == 0 {
		return errors.New("workflow has no jobs")
	}
	for id, job := range wf.Jobs {
		if job == nil {
			return fmt.Errorf("job %s is empty", id)
		}
		if job.Uses != "" {
			continue
		}
		if len(job.RunsOn()) == 0 {
			return fmt.Errorf("job %s has no runs-on", id)
		}
		if len(job.Steps) == 0 {
			return fmt.Errorf("job %s has no steps", id)
		}
	}
	return nil
}

func workflowBaseName(path string) string {
	return strings.TrimSuffix(pathpkg.Base(path), pathpkg.Ext(path))
}

// EventNames lists the events the file listens to
func (f *WorkflowFile) EventNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range f.Events {
		if !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e.Name)
		}
	}
	return out
}

// Event is the first trigger of that name, nil when absent
func (f *WorkflowFile) Event(name string) *jobparser.Event {
	for _, e := range f.Events {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Every trigger of that name, push and pull_request may repeat
func (f *WorkflowFile) eventsNamed(name string) []*jobparser.Event {
	var out []*jobparser.Event
	for _, e := range f.Events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// DispatchInputs declared under workflow_dispatch in file order
func (f *WorkflowFile) DispatchInputs() []jobparser.WorkflowDispatchInput {
	if e := f.Event("workflow_dispatch"); e != nil {
		return e.Inputs()
	}
	return nil
}

// Cron specs of every schedule trigger
func (f *WorkflowFile) Schedules() []string {
	var out []string
	for _, e := range f.Events {
		if !e.IsSchedule() {
			continue
		}
		for _, s := range e.Schedules() {
			if c := strings.TrimSpace(s["cron"]); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// ValidateDispatchInputs types the given values against the declarations
func ValidateDispatchInputs(decl []jobparser.WorkflowDispatchInput, given map[string]string) (out map[string]any, err error) {
	defer recoverPlanning(&err)
	known := map[string]bool{}
	for _, d := range decl {
		known[d.Name] = true
	}
	for name := range given {
		if !known[name] {
			return nil, fmt.Errorf("unknown input %q", name)
		}
	}
	out = map[string]any{}
	for _, d := range decl {
		raw, ok := given[d.Name]
		if !ok || raw == "" {
			if d.Required && d.Default == "" {
				return nil, fmt.Errorf("input %q is required", d.Name)
			}
			raw = d.Default
		}
		switch d.Type {
		case "boolean":
			switch strings.ToLower(raw) {
			case "true":
				out[d.Name] = true
			case "false", "":
				out[d.Name] = false
			default:
				return nil, fmt.Errorf("input %q must be true or false", d.Name)
			}
		case "number":
			if raw == "" {
				out[d.Name] = float64(0)
				continue
			}
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("input %q must be a number", d.Name)
			}
			out[d.Name] = n
		case "choice":
			if raw != "" && !containsString(d.Options, raw) {
				return nil, fmt.Errorf("input %q must be one of %s", d.Name, strings.Join(d.Options, ", "))
			}
			out[d.Name] = raw
		default:
			out[d.Name] = raw
		}
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// Job of a stored single job payload
func parseJobPayload(payload string) (string, *jobparser.Job, error) {
	if strings.TrimSpace(payload) == "" {
		return "", nil, errors.New("job has no workflow payload")
	}
	var swf jobparser.SingleWorkflow
	if err := yaml.Unmarshal([]byte(payload), &swf); err != nil {
		return "", nil, fmt.Errorf("decode job payload: %w", err)
	}
	id, job := swf.Job()
	if job == nil {
		return "", nil, errors.New("job payload holds no job")
	}
	return id, job, nil
}

func (m *Manager) openRepo(repo *db.GitRepository) (*gitrepo.Repo, error) {
	return m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
}

// LoadWorkflows reads and parses every workflow file at a revision
func (m *Manager) LoadWorkflows(ctx context.Context, repo *db.GitRepository, rev string) ([]*WorkflowFile, plumbing.Hash, error) {
	r, err := m.openRepo(repo)
	if err != nil {
		return nil, plumbing.ZeroHash, err
	}
	defer r.Close()
	if rev == "" {
		rev = repo.DefaultBranch
	}
	commit, err := r.Resolve(rev)
	if err != nil {
		if errors.Is(err, gitrepo.ErrEmptyRepo) || errors.Is(err, gitrepo.ErrNotFound) {
			return nil, plumbing.ZeroHash, nil
		}
		return nil, plumbing.ZeroHash, err
	}
	return loadWorkflowFiles(ctx, r, commit), commit, nil
}

// Reads the first workflows directory that holds yaml files
func loadWorkflowFiles(ctx context.Context, r *gitrepo.Repo, commit plumbing.Hash) []*WorkflowFile {
	for _, dir := range WorkflowDirs {
		entries, _, err := r.Tree(ctx, commit, dir, false)
		if err != nil {
			continue
		}
		var out []*WorkflowFile
		for _, e := range entries {
			if !e.Mode.IsFile() || !IsWorkflowFile(e.Name) {
				continue
			}
			p := pathpkg.Join(dir, e.Name)
			blob, err := r.Blob(commit, p, workflowFileCap)
			switch {
			case err != nil:
				out = append(out, &WorkflowFile{Path: p, Name: workflowBaseName(p), Err: err})
			case blob.Truncated:
				out = append(out, &WorkflowFile{Path: p, Name: workflowBaseName(p), Err: fmt.Errorf("workflow file exceeds %d bytes", workflowFileCap)})
			default:
				out = append(out, ParseWorkflowFile(p, blob.Content))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// Reads one workflow file of a repository at a revision
func (m *Manager) readWorkflowSource(repo *db.GitRepository, rev, path string) ([]byte, plumbing.Hash, error) {
	r, err := m.openRepo(repo)
	if err != nil {
		return nil, plumbing.ZeroHash, err
	}
	defer r.Close()
	h, err := r.Resolve(rev)
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("%s not found in %s/%s", rev, repo.Namespace, repo.Name)
	}
	blob, err := r.Blob(h, path, workflowFileCap)
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("%s not found in %s/%s at %s", path, repo.Namespace, repo.Name, rev)
	}
	if blob.Truncated {
		return nil, plumbing.ZeroHash, fmt.Errorf("%s exceeds %d bytes", path, workflowFileCap)
	}
	return blob.Content, h, nil
}
