package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// Everything a tool execution can reach
type runContext struct {
	m        *Manager
	ctx      context.Context
	sess     *db.AgentSession
	repo     *db.GitRepository
	def      *Definition
	bot      *auth.AuthenticatedUser
	botUser  *db.User
	actor    forge.Actor
	issue    *db.GitIssue
	pr       *db.GitPullRequest
	ws       *workspace
	provider *ResolvedProvider
	settings *v1.GitAgentsSettings
	// Side effects the finisher may complete
	posted   bool
	reviewed bool
	prOpened bool
	comments []string
}

// One callable the model may use
type toolSpec struct {
	Name        string
	Description string
	Schema      string
	// Counts as a write under the all_writes policy
	Write bool
	// Counts as a commit under the commits policy
	Commit bool
	Run    func(rc *runContext, args map[string]any) (string, error)
}

// Whether a call pauses for a human under the agent's policy
func needsApproval(def *Definition, spec *toolSpec) bool {
	switch def.Approval {
	case ApprovalAllWrites:
		return spec.Write || spec.Commit
	case ApprovalCommits:
		return spec.Commit
	}
	return false
}

// Definitions handed to the model for an agent
func toolsFor(def *Definition, cfg *v1.GitAgentsSettings, hasTarget, isPull bool) []llm.Tool {
	var out []llm.Tool
	for _, name := range def.ToolNames() {
		spec := toolRegistry[name]
		if spec == nil {
			continue
		}
		if name == "run_command" && !cfg.GetAllowCommands() {
			continue
		}
		if (name == "post_comment" || name == "update_issue") && !hasTarget {
			continue
		}
		if name == "submit_review" && !isPull {
			continue
		}
		out = append(out, llm.Tool{Name: spec.Name, Description: spec.Description, Parameters: json.RawMessage(spec.Schema)})
	}
	return out
}

func argString(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func argInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func argBool(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	}
	return false
}

func argStrings(args map[string]any, key string) []string {
	var out []string
	switch v := args[key].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func (rc *runContext) openRepo() (*gitrepo.Repo, error) {
	return rc.m.git.Engine().OpenRepo(rc.repo.Namespace, rc.repo.Name, false)
}

// Every file with every hunk, nothing withheld from the model
func diffOpts(path string) gitrepo.DiffOptions {
	return gitrepo.DiffOptions{OnlyPath: path, MaxLines: math.MaxInt32, AllFiles: true}
}

// Tools the model may call, keyed by name
var toolRegistry = map[string]*toolSpec{
	"get_repository_info": {
		Name:        "get_repository_info",
		Description: "Describes the repository, its default branch, languages, the working branch of this session, and the instruction files found.",
		Schema:      `{"type":"object","properties":{}}`,
		Run:         toolRepositoryInfo,
	},
	"list_files": {
		Name:        "list_files",
		Description: "Lists files in a directory of the working tree. Directories end with a slash. Use recursive for a full subtree listing.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"Directory path, empty for the root"},"recursive":{"type":"boolean"}}}`,
		Run:         toolListFiles,
	},
	"read_file": {
		Name:        "read_file",
		Description: "Reads a text file from the working tree with line numbers. Use start_line and end_line to read a range of a large file.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["path"]}`,
		Run:         toolReadFile,
	},
	"search_code": {
		Name:        "search_code",
		Description: "Searches committed files with a regular expression and returns matching lines as path:line: text. Uncommitted edits are not searched.",
		Schema:      `{"type":"object","properties":{"pattern":{"type":"string","description":"Go regular expression"},"path_glob":{"type":"string","description":"Glob such as src/**/*.go"},"case_insensitive":{"type":"boolean"},"limit":{"type":"integer","description":"Zero or absent returns every match"}},"required":["pattern"]}`,
		Run:         toolSearchCode,
	},
	"get_diff": {
		Name:        "get_diff",
		Description: "Shows what changed. On a pull request this is the diff against its base, otherwise the uncommitted edits of this session. Without a path it lists changed files with counts, with a path it returns the hunks with old and new line numbers.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"}}}`,
		Run:         toolGetDiff,
	},
	"get_pull_request": {
		Name:        "get_pull_request",
		Description: "Returns the pull request this session targets: description, commits, review threads, checks, mergeability, and recent comments.",
		Schema:      `{"type":"object","properties":{"number":{"type":"integer","description":"Another pull request of this repository, defaults to the target"}}}`,
		Run:         toolGetPull,
	},
	"get_issue": {
		Name:        "get_issue",
		Description: "Returns an issue or pull request with its labels, assignees, and comments.",
		Schema:      `{"type":"object","properties":{"number":{"type":"integer","description":"Defaults to the target of this session"}}}`,
		Run:         toolGetIssue,
	},
	"list_commits": {
		Name:        "list_commits",
		Description: "Lists recent commits, the pull request's commits when one is targeted, otherwise the working branch history.",
		Schema:      `{"type":"object","properties":{"limit":{"type":"integer","description":"Zero or absent lists every commit"}}}`,
		Run:         toolListCommits,
	},
	"get_checks": {
		Name:        "get_checks",
		Description: "Returns commit statuses and workflow runs for the head commit, with run ids for get_workflow_run_log.",
		Schema:      `{"type":"object","properties":{}}`,
		Run:         toolGetChecks,
	},
	"get_workflow_run_log": {
		Name:        "get_workflow_run_log",
		Description: "Returns a workflow job log, whole unless tail_lines is given. Picks the first failed job unless job_name is given.",
		Schema:      `{"type":"object","properties":{"run_id":{"type":"string"},"job_name":{"type":"string"},"tail_lines":{"type":"integer","description":"Lines from the end, zero or absent returns the whole log"}},"required":["run_id"]}`,
		Run:         toolWorkflowLog,
	},
	"search_issues": {
		Name:        "search_issues",
		Description: "Searches issues and pull requests of this repository by text, state, and labels.",
		Schema:      `{"type":"object","properties":{"query":{"type":"string"},"state":{"type":"string","enum":["open","closed","all"]},"labels":{"type":"array","items":{"type":"string"}},"is_pull":{"type":"boolean"},"limit":{"type":"integer","description":"Zero or absent returns every match"}}}`,
		Run:         toolSearchIssues,
	},
	"post_comment": {
		Name:        "post_comment",
		Description: "Posts a markdown comment on the targeted issue or pull request.",
		Schema:      `{"type":"object","properties":{"body":{"type":"string"}},"required":["body"]}`,
		Write:       true,
		Run:         toolPostComment,
	},
	"submit_review": {
		Name:        "submit_review",
		Description: "Submits one review on the targeted pull request with optional inline comments anchored to lines of the new side of the diff. Call it once when the review is complete.",
		Schema:      `{"type":"object","properties":{"event":{"type":"string","enum":["approve","request_changes","comment"]},"body":{"type":"string","description":"Review summary in markdown"},"comments":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"line":{"type":"integer","description":"Line number on the new side, or old side when side is old"},"side":{"type":"string","enum":["new","old"]},"start_line":{"type":"integer","description":"First line of a multi line comment"},"body":{"type":"string","description":"Markdown, a suggestion fenced block proposes a replacement"}},"required":["path","line","body"]}}},"required":["event"]}`,
		Write:       true,
		Run:         toolSubmitReview,
	},
	"update_issue": {
		Name:        "update_issue",
		Description: "Changes labels, assignees, title, or state of the targeted issue or pull request. Labels are matched by name and created when missing only if create_labels is true.",
		Schema:      `{"type":"object","properties":{"add_labels":{"type":"array","items":{"type":"string"}},"remove_labels":{"type":"array","items":{"type":"string"}},"create_labels":{"type":"boolean"},"assignees":{"type":"array","items":{"type":"string"},"description":"Usernames, replaces the current assignees"},"title":{"type":"string"},"state":{"type":"string","enum":["open","closed"]}}}`,
		Write:       true,
		Run:         toolUpdateIssue,
	},
	"write_file": {
		Name:        "write_file",
		Description: "Creates or replaces a whole file in the working tree. Changes stay uncommitted until commit is called.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
		Run:         toolWriteFile,
	},
	"edit_file": {
		Name:        "edit_file",
		Description: "Replaces one exact occurrence of old_text with new_text in a file. old_text must match exactly once, include enough surrounding lines to be unique.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"]}`,
		Run:         toolEditFile,
	},
	"delete_file": {
		Name:        "delete_file",
		Description: "Deletes a file from the working tree.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
		Run:         toolDeleteFile,
	},
	"rename_file": {
		Name:        "rename_file",
		Description: "Moves a file to a new path in the working tree.",
		Schema:      `{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"]}`,
		Run:         toolRenameFile,
	},
	"commit": {
		Name:        "commit",
		Description: "Commits every uncommitted edit to the working branch with a message. The first line is the subject.",
		Schema:      `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`,
		Commit:      true,
		Run:         toolCommit,
	},
	"open_pull_request": {
		Name:        "open_pull_request",
		Description: "Opens a pull request from the working branch into its base branch. Commit first.",
		Schema:      `{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"},"draft":{"type":"boolean"}},"required":["title"]}`,
		Commit:      true,
		Run:         toolOpenPull,
	},
	"run_command": {
		Name:        "run_command",
		Description: "Runs a shell script on a workflow runner against the working branch with uncommitted edits applied, and returns its output. Use it to build, test, or lint. Slow, so batch commands into one script.",
		Schema:      `{"type":"object","properties":{"command":{"type":"string","description":"bash script"},"timeout_minutes":{"type":"integer"}},"required":["command"]}`,
		Commit:      true,
		Run:         toolRunCommand,
	},
	"request_review": {
		Name:        "request_review",
		Description: "Requests reviews from users on the pull request this session opened or targets.",
		Schema:      `{"type":"object","properties":{"usernames":{"type":"array","items":{"type":"string"}}},"required":["usernames"]}`,
		Write:       true,
		Run:         toolRequestReview,
	},
}

// ── Read tools ───────────────────────────────────────────────────────────

func toolRepositoryInfo(rc *runContext, _ map[string]any) (string, error) {
	var b strings.Builder
	repo := rc.repo
	fmt.Fprintf(&b, "Repository: %s/%s\n", repo.Namespace, repo.Name)
	if repo.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", repo.Description)
	}
	fmt.Fprintf(&b, "Default branch: %s\nVisibility: %s\n", repo.DefaultBranch, map[bool]string{true: "private", false: "public"}[repo.IsPrivate])
	if rc.ws != nil {
		fmt.Fprintf(&b, "Working branch: %s (base %s)\n", rc.ws.branch, rc.ws.baseBranch)
		if !rc.ws.head.IsZero() {
			fmt.Fprintf(&b, "Head commit: %s\n", rc.ws.head.String())
		}
		if n := len(rc.ws.pendingPaths()); n > 0 {
			fmt.Fprintf(&b, "Uncommitted edits: %d files\n", n)
		}
		if len(rc.ws.commits) > 0 {
			fmt.Fprintf(&b, "Commits this session: %s\n", strings.Join(rc.ws.commits, ", "))
		}
	}
	r, err := rc.openRepo()
	if err == nil {
		defer r.Close()
		if rc.ws != nil && !rc.ws.head.IsZero() {
			if langs, err := r.Languages(rc.ctx, rc.ws.head); err == nil && len(langs) > 0 {
				type kv struct {
					k string
					v int64
				}
				var list []kv
				for k, v := range langs {
					list = append(list, kv{k, v})
				}
				sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
				var names []string
				for i, e := range list {
					if i >= 6 {
						break
					}
					names = append(names, e.k)
				}
				fmt.Fprintf(&b, "Languages: %s\n", strings.Join(names, ", "))
			}
			if readme, err := r.Readme(rc.ws.head, ""); err == nil && readme != nil && !readme.IsBinary {
				content := readme.Content
				// The inline reader stops at its display cap, fetch the rest
				if readme.Truncated {
					if whole, err := r.Blob(rc.ws.head, readme.Path, 0); err == nil {
						content = whole.Content
					}
				}
				fmt.Fprintf(&b, "\n%s:\n%s\n", readme.Path, string(content))
			}
		}
	}
	if files := rc.m.InstructionFiles(rc.ctx, repo); len(files) > 0 {
		fmt.Fprintf(&b, "Instruction files: %s\n", strings.Join(files, ", "))
	}
	return b.String(), nil
}

func toolListFiles(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	entries, err := rc.ws.list(rc.ctx, r, argString(args, "path"), argBool(args, "recursive"))
	if err != nil {
		if errors.Is(err, gitrepo.ErrNotFound) {
			return "", fmt.Errorf("directory not found")
		}
		return "", err
	}
	if len(entries) == 0 {
		return "(empty)", nil
	}
	return strings.Join(entries, "\n"), nil
}

func toolReadFile(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	p := argString(args, "path")
	content, _, err := rc.ws.read(r, p)
	if err != nil {
		if errors.Is(err, gitrepo.ErrNotFound) {
			return "", fmt.Errorf("file not found: %s", p)
		}
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start, end := argInt(args, "start_line"), argInt(args, "end_line")
	if start < 1 {
		start = 1
	}
	if end < start {
		end = len(lines)
	}
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d lines)\n", p, len(lines))
	if start > len(lines) {
		fmt.Fprintf(&b, "[start_line %d is past the end]\n", start)
		return b.String(), nil
	}
	width := len(fmt.Sprint(end))
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%*d| %s\n", width, i, lines[i-1])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "[%d more lines, read from start_line %d]\n", len(lines)-end, end+1)
	}
	return b.String(), nil
}

func toolSearchCode(rc *runContext, args map[string]any) (string, error) {
	if rc.ws.head.IsZero() {
		return "(repository is empty)", nil
	}
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	// Zero or absent means every match
	limit := max(argInt(args, "limit"), 0)
	pattern := argString(args, "pattern")
	if strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	fetch := 0
	if limit > 0 {
		fetch = limit + 1
	}
	results, err := r.Grep(rc.ws.head, pattern, argString(args, "path_glob"), argBool(args, "case_insensitive"), fetch)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "no matches", nil
	}
	var b strings.Builder
	for i, res := range results {
		if limit > 0 && i >= limit {
			b.WriteString("[more matches not shown, raise limit or narrow the pattern]\n")
			break
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", res.FileName, res.LineNumber, strings.TrimRight(res.Content, "\r\n"))
	}
	return b.String(), nil
}

func toolGetDiff(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	path := strings.TrimSpace(argString(args, "path"))
	if rc.pr != nil && len(rc.ws.pendingPaths()) == 0 && len(rc.ws.commits) == 0 {
		return pullDiff(rc, r, path)
	}
	// Session changes: commits since the base plus uncommitted edits
	var b strings.Builder
	base := plumbing.ZeroHash
	if rc.ws.baseBranch != "" {
		base = r.BranchHash(rc.ws.baseBranch)
	}
	if !base.IsZero() && !rc.ws.head.IsZero() && base != rc.ws.head {
		if mb, err := r.MergeBase(base, rc.ws.head); err == nil && !mb.IsZero() {
			base = mb
		}
		if path == "" {
			diff, err := r.Diff(rc.ctx, base, rc.ws.head, diffOpts(""))
			if err == nil && len(diff.Files) > 0 {
				b.WriteString("Committed on the working branch:\n")
				writeDiffSummary(&b, diff)
			}
		} else {
			diff, err := r.Diff(rc.ctx, base, rc.ws.head, diffOpts(path))
			if err == nil && len(diff.Files) > 0 {
				b.WriteString("Committed on the working branch:\n")
				writeHunks(&b, diff.Files[0])
			}
		}
	}
	pending := rc.ws.pendingPaths()
	if len(pending) == 0 {
		if b.Len() == 0 {
			if rc.pr != nil {
				return pullDiff(rc, r, path)
			}
			return "no changes", nil
		}
		return b.String(), nil
	}
	b.WriteString("Uncommitted edits:\n")
	for _, p := range pending {
		if path != "" && p != path {
			continue
		}
		e := rc.ws.edits[p]
		var before []byte
		if !rc.ws.head.IsZero() {
			if blob, err := r.Blob(rc.ws.head, p, 0); err == nil && !blob.IsBinary {
				before = blob.Content
			}
		}
		switch {
		case e.delete:
			fmt.Fprintf(&b, "--- %s (deleted)\n", p)
		case before == nil:
			fmt.Fprintf(&b, "+++ %s (new file, %d bytes)\n", p, len(e.content))
			if path != "" {
				for _, l := range strings.Split(string(e.content), "\n") {
					fmt.Fprintf(&b, "+%s\n", l)
				}
			}
		default:
			fmt.Fprintf(&b, "~~~ %s\n", p)
			if path != "" {
				b.WriteString(renderUnified(string(before), string(e.content)))
			}
		}
	}
	if path == "" {
		b.WriteString("Pass a path for the hunks of one file.\n")
	}
	return b.String(), nil
}

// Diff of the targeted pull request against its base
func pullDiff(rc *runContext, r *gitrepo.Repo, path string) (string, error) {
	base := plumbing.ZeroHash
	if rc.pr.MergeBaseSHA != "" {
		base = plumbing.NewHash(rc.pr.MergeBaseSHA)
	}
	head := plumbing.NewHash(rc.pr.HeadSHA)
	if base.IsZero() {
		if tip := r.BranchHash(rc.pr.BaseBranch); !tip.IsZero() {
			if mb, err := r.MergeBase(tip, head); err == nil {
				base = mb
			}
		}
	}
	var b strings.Builder
	if path == "" {
		diff, err := r.Diff(rc.ctx, base, head, diffOpts(""))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Pull request #%d: %d files, +%d -%d\n", rc.issue.Number, diff.TotalFiles, diff.Additions, diff.Deletions)
		writeDiffSummary(&b, diff)
		b.WriteString("Pass a path for the hunks of one file.\n")
		return b.String(), nil
	}
	diff, err := r.Diff(rc.ctx, base, head, diffOpts(path))
	if err != nil {
		return "", err
	}
	if len(diff.Files) == 0 {
		return "", fmt.Errorf("%s is not changed by this pull request", path)
	}
	writeHunks(&b, diff.Files[0])
	return b.String(), nil
}

func writeDiffSummary(b *strings.Builder, diff *v1.GitDiff) {
	for _, f := range diff.Files {
		name := f.NewPath
		if name == "" {
			name = f.OldPath
		}
		status := strings.ToLower(strings.TrimPrefix(f.Status.String(), "DIFF_STATUS_"))
		if f.OldPath != "" && f.NewPath != "" && f.OldPath != f.NewPath {
			name = f.OldPath + " -> " + f.NewPath
		}
		extra := ""
		if f.IsBinary {
			extra = " binary"
		}
		fmt.Fprintf(b, "%-9s %s (+%d -%d)%s\n", status, name, f.Additions, f.Deletions, extra)
	}
	if diff.Truncated {
		b.WriteString("[file list truncated]\n")
	}
}

// Hunks with both line numbers so inline comments anchor correctly
func writeHunks(b *strings.Builder, f *v1.DiffFile) {
	name := f.NewPath
	if name == "" {
		name = f.OldPath
	}
	fmt.Fprintf(b, "%s (+%d -%d)\n", name, f.Additions, f.Deletions)
	if f.IsBinary {
		b.WriteString("[binary file]\n")
		return
	}
	if f.Truncated || len(f.Hunks) == 0 {
		b.WriteString("[diff too large to display, read the file instead]\n")
		return
	}
	b.WriteString("Format: old|new| +/-/space text\n")
	for _, h := range f.Hunks {
		fmt.Fprintf(b, "%s\n", h.Header)
		for _, l := range h.Lines {
			switch l.Kind {
			case v1.DiffLineKind_DIFF_LINE_KIND_ADD:
				fmt.Fprintf(b, "    |%4d| +%s\n", l.NewNumber, l.Text)
			case v1.DiffLineKind_DIFF_LINE_KIND_DELETE:
				fmt.Fprintf(b, "%4d|    | -%s\n", l.OldNumber, l.Text)
			default:
				fmt.Fprintf(b, "%4d|%4d|  %s\n", l.OldNumber, l.NewNumber, l.Text)
			}
		}
	}
}

// Compact unified style rendering of two texts
func renderUnified(before, after string) string {
	dmp := diffmatchpatch.New()
	a, b2, lines := dmp.DiffLinesToChars(before, after)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b2, false), lines)
	var out strings.Builder
	for _, d := range diffs {
		text := strings.TrimSuffix(d.Text, "\n")
		parts := strings.Split(text, "\n")
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			if len(parts) > 6 {
				for _, l := range parts[:3] {
					fmt.Fprintf(&out, " %s\n", l)
				}
				fmt.Fprintf(&out, " ... %d unchanged lines ...\n", len(parts)-6)
				for _, l := range parts[len(parts)-3:] {
					fmt.Fprintf(&out, " %s\n", l)
				}
				continue
			}
			for _, l := range parts {
				fmt.Fprintf(&out, " %s\n", l)
			}
		case diffmatchpatch.DiffDelete:
			for _, l := range parts {
				fmt.Fprintf(&out, "-%s\n", l)
			}
		case diffmatchpatch.DiffInsert:
			for _, l := range parts {
				fmt.Fprintf(&out, "+%s\n", l)
			}
		}
	}
	return out.String()
}

func toolGetPull(rc *runContext, args map[string]any) (string, error) {
	issue, pr := rc.issue, rc.pr
	if n := argInt(args, "number"); n > 0 && (issue == nil || int64(n) != issue.Number) {
		other, err := rc.m.forge.Issue(rc.ctx, rc.repo, int64(n))
		if err != nil {
			return "", fmt.Errorf("pull request #%d not found", n)
		}
		if !other.IsPull {
			return "", fmt.Errorf("#%d is an issue, use get_issue", n)
		}
		otherPR, err := rc.m.forge.Pull(rc.ctx, other)
		if err != nil {
			return "", err
		}
		issue, pr = other, otherPR
	}
	if issue == nil || pr == nil {
		return "", fmt.Errorf("this session has no pull request, pass a number")
	}
	return rc.m.describePull(rc.ctx, rc.repo, rc.actor, issue, pr), nil
}

func toolGetIssue(rc *runContext, args map[string]any) (string, error) {
	issue := rc.issue
	if n := argInt(args, "number"); n > 0 && (issue == nil || int64(n) != issue.Number) {
		other, err := rc.m.forge.Issue(rc.ctx, rc.repo, int64(n))
		if err != nil {
			return "", fmt.Errorf("#%d not found", n)
		}
		issue = other
	}
	if issue == nil {
		return "", fmt.Errorf("this session has no issue, pass a number")
	}
	if issue.IsPull {
		pr, err := rc.m.forge.Pull(rc.ctx, issue)
		if err == nil {
			return rc.m.describePull(rc.ctx, rc.repo, rc.actor, issue, pr), nil
		}
	}
	return rc.m.describeIssue(rc.ctx, rc.repo, issue), nil
}

func toolListCommits(rc *runContext, args map[string]any) (string, error) {
	// Zero or absent lists every commit
	limit := max(argInt(args, "limit"), 0)
	var b strings.Builder
	if rc.pr != nil && len(rc.ws.commits) == 0 {
		commits, err := rc.m.forge.PullCommits(rc.ctx, rc.repo, rc.pr, limit)
		if err != nil {
			return "", err
		}
		for _, c := range commits {
			fmt.Fprintf(&b, "%s %s <%s> %s\n", c.Hash.String()[:10], firstLine(c.Message), c.Author.Email, c.Author.When.UTC().Format(time.RFC3339))
		}
		if b.Len() == 0 {
			return "no commits", nil
		}
		return b.String(), nil
	}
	if rc.ws.head.IsZero() {
		return "no commits", nil
	}
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	commits, _, err := r.Log(rc.ctx, gitrepo.LogQuery{From: rc.ws.head, Limit: limit})
	if err != nil {
		return "", err
	}
	for _, c := range commits {
		fmt.Fprintf(&b, "%s %s <%s> %s\n", c.Hash.String()[:10], firstLine(c.Message), c.Author.Email, c.Author.When.UTC().Format(time.RFC3339))
	}
	return b.String(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func toolGetChecks(rc *runContext, _ map[string]any) (string, error) {
	sha := ""
	if rc.pr != nil {
		sha = rc.pr.HeadSHA
	} else if !rc.ws.head.IsZero() {
		sha = rc.ws.head.String()
	}
	if sha == "" {
		return "no head commit", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Head commit: %s\n", sha)
	statuses, _ := rc.m.store.LatestGitCommitStatuses(rc.ctx, rc.repo.ID, sha)
	if len(statuses) > 0 {
		b.WriteString("Statuses:\n")
		for _, s := range statuses {
			fmt.Fprintf(&b, "  %s: %s %s\n", s.Context, s.State, s.Description)
		}
	}
	if rc.m.actions != nil {
		runs, _ := rc.m.actions.RunsForCommit(rc.ctx, rc.repo, sha, 20)
		if len(runs) > 0 {
			b.WriteString("Workflow runs:\n")
			for _, run := range runs {
				fmt.Fprintf(&b, "  run_id=%s %s #%d status=%s conclusion=%s\n", run.ID, run.WorkflowName, run.Number, run.Status, run.Conclusion)
				jobs, _ := rc.m.actions.RunJobs(rc.ctx, run.ID)
				for _, j := range jobs {
					fmt.Fprintf(&b, "    job %q status=%s conclusion=%s\n", j.Name, j.Status, j.Conclusion)
				}
			}
		}
	}
	if len(statuses) == 0 && !strings.Contains(b.String(), "Workflow runs") {
		b.WriteString("No checks reported.\n")
	}
	return b.String(), nil
}

func toolWorkflowLog(rc *runContext, args map[string]any) (string, error) {
	if rc.m.actions == nil {
		return "", fmt.Errorf("actions are not available")
	}
	run, err := rc.m.actions.GetRun(rc.ctx, argString(args, "run_id"))
	if err != nil || run == nil || run.RepoID != rc.repo.ID {
		return "", fmt.Errorf("run not found")
	}
	jobs, err := rc.m.actions.RunJobs(rc.ctx, run.ID)
	if err != nil {
		return "", err
	}
	var job *db.ActionsJob
	want := argString(args, "job_name")
	for _, j := range jobs {
		if want != "" && j.Name == want {
			job = j
			break
		}
		if want == "" && j.Conclusion == actions.ConclusionFailure && job == nil {
			job = j
		}
	}
	if job == nil && len(jobs) > 0 && want == "" {
		job = jobs[0]
	}
	if job == nil {
		return "", fmt.Errorf("job not found")
	}
	raw, err := rc.m.actions.JobLog(rc.ctx, job)
	if err != nil {
		return "", err
	}
	// Zero or absent returns the whole log
	tail := argInt(args, "tail_lines")
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Job %q of %s #%d: status=%s conclusion=%s\n", job.Name, run.WorkflowName, run.Number, job.Status, job.Conclusion)
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func toolSearchIssues(rc *runContext, args map[string]any) (string, error) {
	// Zero or absent returns every match
	limit := max(argInt(args, "limit"), 0)
	opts := stores.IssueListOptions{RepoID: rc.repo.ID, Limit: limit, OrderBy: "updated_at DESC"}
	if st := argString(args, "state"); st == "open" || st == "closed" {
		opts.State = st
	}
	opts.Labels = argStrings(args, "labels")
	if v, ok := args["is_pull"]; ok {
		isPull := argBool(args, "is_pull")
		_ = v
		opts.IsPull = &isPull
	}
	if q := strings.TrimSpace(argString(args, "query")); q != "" {
		opts.Query = pages.Query{Text: q}
	}
	issues, total, err := rc.m.forge.ListIssues(rc.ctx, opts)
	if err != nil {
		return "", err
	}
	if len(issues) == 0 {
		return "no matches", nil
	}
	labels, _ := rc.m.store.GetGitIssueLabels(rc.ctx, issueIDs(issues))
	var b strings.Builder
	fmt.Fprintf(&b, "%d matches\n", total)
	for _, is := range issues {
		kind := "issue"
		if is.IsPull {
			kind = "pull"
		}
		var names []string
		for _, l := range labels[is.ID] {
			names = append(names, l.Name)
		}
		fmt.Fprintf(&b, "#%d [%s %s] %s", is.Number, kind, is.State, is.Title)
		if len(names) > 0 {
			fmt.Fprintf(&b, " labels: %s", strings.Join(names, ", "))
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func issueIDs(issues []*db.GitIssue) []string {
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.ID)
	}
	return out
}

// ── Write tools ──────────────────────────────────────────────────────────

func toolPostComment(rc *runContext, args map[string]any) (string, error) {
	if rc.issue == nil {
		return "", fmt.Errorf("this session has no issue or pull request to comment on")
	}
	body := strings.TrimSpace(argString(args, "body"))
	if body == "" {
		return "", fmt.Errorf("body is required")
	}
	c, err := rc.m.forge.CreateComment(rc.ctx, rc.repo, rc.actor, rc.issue, body, false)
	if err != nil {
		return "", err
	}
	rc.posted = true
	rc.comments = append(rc.comments, c.ID)
	return fmt.Sprintf("comment posted (id %s)", c.ID), nil
}

func toolSubmitReview(rc *runContext, args map[string]any) (string, error) {
	if rc.issue == nil || rc.pr == nil {
		return "", fmt.Errorf("this session has no pull request to review")
	}
	if rc.reviewed {
		return "", fmt.Errorf("a review was already submitted in this session, use post_comment for follow ups")
	}
	var state string
	switch strings.ToLower(strings.TrimSpace(argString(args, "event"))) {
	case "approve", "approved":
		state = forge.ReviewApproved
	case "request_changes", "changes_requested":
		state = forge.ReviewChangesRequested
	case "comment", "commented", "":
		state = forge.ReviewCommented
	default:
		return "", fmt.Errorf("event must be approve, request_changes, or comment")
	}
	var inputs []forge.ReviewCommentInput
	if raw, ok := args["comments"].([]any); ok {
		for _, item := range raw {
			cm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			in := forge.ReviewCommentInput{Path: strings.TrimSpace(argString(cm, "path")), Line: argInt(cm, "line"), Side: argString(cm, "side"), StartLine: argInt(cm, "start_line"), Body: strings.TrimSpace(argString(cm, "body"))}
			if in.Path == "" || in.Line <= 0 || in.Body == "" {
				continue
			}
			if in.Side != "old" {
				in.Side = "new"
			}
			if in.StartLine >= in.Line {
				in.StartLine = 0
			}
			inputs = append(inputs, in)
		}
	}
	body := strings.TrimSpace(argString(args, "body"))
	if body == "" && len(inputs) == 0 {
		return "", fmt.Errorf("a body or at least one inline comment is required")
	}
	rv, err := rc.m.forge.CreateReview(rc.ctx, rc.repo, rc.actor, rc.issue, rc.pr, state, body, rc.pr.HeadSHA, inputs)
	if err != nil {
		return "", err
	}
	rc.reviewed = true
	rc.sess.ResultReviewID = rv.ID
	_ = rc.m.store.UpdateAgentSessionColumns(rc.ctx, rc.sess.ID, map[string]any{"result_review_id": rv.ID})
	return fmt.Sprintf("review submitted as %s with %d inline comments (id %s)", state, len(inputs), rv.ID), nil
}

func toolUpdateIssue(rc *runContext, args map[string]any) (string, error) {
	if rc.issue == nil {
		return "", fmt.Errorf("this session has no issue or pull request")
	}
	var done []string
	add, remove := argStrings(args, "add_labels"), argStrings(args, "remove_labels")
	if len(add) > 0 || len(remove) > 0 {
		orgID := rc.m.repoOrgID(rc.ctx, rc.repo)
		all, _, err := rc.m.forge.ListLabels(rc.ctx, rc.repo, orgID, pages.Query{}, 500, 0)
		if err != nil {
			return "", err
		}
		byName := map[string]*db.GitLabel{}
		for _, l := range all {
			byName[strings.ToLower(l.Name)] = l
		}
		current, _ := rc.m.store.GetGitIssueLabels(rc.ctx, []string{rc.issue.ID})
		ids := map[string]bool{}
		for _, l := range current[rc.issue.ID] {
			ids[l.ID] = true
		}
		var missing []string
		for _, name := range add {
			l := byName[strings.ToLower(name)]
			if l == nil && argBool(args, "create_labels") {
				created, err := rc.m.forge.CreateLabel(rc.ctx, rc.repo, "", forge.LabelInput{Name: name, Color: "#ededed"})
				if err == nil {
					l = created
					byName[strings.ToLower(name)] = l
				}
			}
			if l == nil {
				missing = append(missing, name)
				continue
			}
			ids[l.ID] = true
		}
		for _, name := range remove {
			if l := byName[strings.ToLower(name)]; l != nil {
				delete(ids, l.ID)
			}
		}
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		if err := rc.m.forge.SetLabels(rc.ctx, rc.repo, rc.actor, rc.issue, list); err != nil {
			return "", err
		}
		done = append(done, "labels updated")
		if len(missing) > 0 {
			done = append(done, fmt.Sprintf("unknown labels skipped: %s", strings.Join(missing, ", ")))
		}
	}
	if v, ok := args["assignees"]; ok && v != nil {
		var ids []string
		var unknown []string
		for _, name := range argStrings(args, "assignees") {
			u, _ := rc.m.store.GetUserByUsername(rc.ctx, strings.TrimPrefix(name, "@"))
			if u == nil {
				unknown = append(unknown, name)
				continue
			}
			ids = append(ids, u.ID)
		}
		if err := rc.m.forge.SetAssignees(rc.ctx, rc.repo, rc.actor, rc.issue, ids); err != nil {
			return "", err
		}
		done = append(done, "assignees updated")
		if len(unknown) > 0 {
			done = append(done, fmt.Sprintf("unknown users skipped: %s", strings.Join(unknown, ", ")))
		}
	}
	up := forge.IssueUpdate{}
	changed := false
	if t := strings.TrimSpace(argString(args, "title")); t != "" && t != rc.issue.Title {
		up.Title = &t
		changed = true
	}
	if st := strings.TrimSpace(argString(args, "state")); st == "open" || st == "closed" {
		if st != rc.issue.State {
			up.State = &st
			changed = true
		}
	}
	if changed {
		if err := rc.m.forge.UpdateIssue(rc.ctx, rc.repo, rc.actor, rc.issue, up); err != nil {
			return "", err
		}
		done = append(done, "issue updated")
	}
	if len(done) == 0 {
		return "nothing to change", nil
	}
	if fresh, _ := rc.m.store.GetGitIssueByID(rc.ctx, rc.issue.ID); fresh != nil {
		rc.issue = fresh
	}
	return strings.Join(done, "; "), nil
}

func toolWriteFile(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	p := argString(args, "path")
	if err := rc.ws.write(r, p, []byte(argString(args, "content"))); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s (%d bytes, uncommitted)", p, len(argString(args, "content"))), nil
}

func toolEditFile(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	p := argString(args, "path")
	if err := rc.ws.edit(r, p, argString(args, "old_text"), argString(args, "new_text")); err != nil {
		if errors.Is(err, gitrepo.ErrNotFound) {
			return "", fmt.Errorf("file not found: %s", p)
		}
		return "", err
	}
	return fmt.Sprintf("edited %s (uncommitted)", p), nil
}

func toolDeleteFile(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	p := argString(args, "path")
	if err := rc.ws.remove(r, p); err != nil {
		if errors.Is(err, gitrepo.ErrNotFound) {
			return "", fmt.Errorf("file not found: %s", p)
		}
		return "", err
	}
	return fmt.Sprintf("deleted %s (uncommitted)", p), nil
}

func toolRenameFile(rc *runContext, args map[string]any) (string, error) {
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	defer r.Close()
	if err := rc.ws.rename(r, argString(args, "from"), argString(args, "to")); err != nil {
		return "", err
	}
	return fmt.Sprintf("renamed %s to %s (uncommitted)", argString(args, "from"), argString(args, "to")), nil
}

func toolCommit(rc *runContext, args map[string]any) (string, error) {
	message := strings.TrimSpace(argString(args, "message"))
	if message == "" {
		return "", fmt.Errorf("a commit message is required")
	}
	edits := rc.ws.treeEdits()
	if len(edits) == 0 {
		return "", fmt.Errorf("nothing to commit, edit files first")
	}
	if rc.ws.branch == "" {
		return "", fmt.Errorf("this session has no working branch")
	}
	message += fmt.Sprintf("\n\nAgent: @%s session #%d", rc.botUser.Username, rc.sess.Number)
	r, err := rc.openRepo()
	if err != nil {
		return "", err
	}
	exists := !r.BranchHash(rc.ws.branch).IsZero()
	r.Close()
	req := gitrepo.CommitRequest{Message: message, Edits: edits}
	if exists {
		req.Branch, req.ExpectedHead = rc.ws.branch, rc.ws.head
	} else {
		req.Branch, req.NewBranch = rc.ws.baseBranch, rc.ws.branch
	}
	sha, err := rc.m.git.CommitToBranch(rc.ctx, rc.repo, rc.bot, req)
	if err != nil {
		if errors.Is(err, gitrepo.ErrRefChanged) {
			if r2, err2 := rc.openRepo(); err2 == nil {
				rc.ws.head = r2.BranchHash(rc.ws.branch)
				r2.Close()
			}
			return "", fmt.Errorf("the branch moved while you were working, your edits now apply on the new head, re-read changed files and commit again")
		}
		return "", err
	}
	rc.ws.head = sha
	rc.ws.edits = map[string]*pendingEdit{}
	rc.ws.commits = append(rc.ws.commits, sha.String())
	raw, _ := json.Marshal(rc.ws.commits)
	rc.sess.ResultCommits, rc.sess.Branch = string(raw), rc.ws.branch
	_ = rc.m.store.UpdateAgentSessionColumns(rc.ctx, rc.sess.ID, map[string]any{"result_commits": string(raw), "branch": rc.ws.branch})
	return fmt.Sprintf("committed %s on %s (%d files)", sha.String()[:10], rc.ws.branch, len(edits)), nil
}

func toolOpenPull(rc *runContext, args map[string]any) (string, error) {
	if rc.ws.branch == "" || rc.ws.branch == rc.ws.baseBranch {
		return "", fmt.Errorf("the working branch is the base branch, nothing to propose")
	}
	if len(rc.ws.commits) == 0 {
		r, err := rc.openRepo()
		if err != nil {
			return "", err
		}
		exists := !r.BranchHash(rc.ws.branch).IsZero()
		r.Close()
		if !exists {
			return "", fmt.Errorf("commit your edits before opening a pull request")
		}
	}
	if len(rc.ws.pendingPaths()) > 0 {
		return "", fmt.Errorf("there are uncommitted edits, commit them first")
	}
	title := strings.TrimSpace(argString(args, "title"))
	if title == "" {
		return "", fmt.Errorf("a title is required")
	}
	body := strings.TrimSpace(argString(args, "body"))
	if rc.issue != nil && !rc.issue.IsPull && !strings.Contains(strings.ToLower(body), fmt.Sprintf("#%d", rc.issue.Number)) {
		body = strings.TrimSpace(body + fmt.Sprintf("\n\nCloses #%d", rc.issue.Number))
	}
	body += fmt.Sprintf("\n\n---\nOpened by agent @%s from session #%d.", rc.botUser.Username, rc.sess.Number)
	issue, _, err := rc.m.forge.CreatePull(rc.ctx, rc.repo, rc.actor, forge.NewPull{Base: rc.ws.baseBranch, Head: rc.ws.branch, Title: title, Body: body, Draft: argBool(args, "draft")})
	if err != nil {
		if errors.Is(err, forge.ErrConflict) {
			return "", err
		}
		return "", err
	}
	rc.prOpened = true
	rc.sess.ResultPullNumber = issue.Number
	_ = rc.m.store.UpdateAgentSessionColumns(rc.ctx, rc.sess.ID, map[string]any{"result_pull_number": issue.Number})
	return fmt.Sprintf("opened pull request #%d: %s", issue.Number, rc.m.forge.IssueURL(rc.ctx, rc.repo, issue)), nil
}

func toolRequestReview(rc *runContext, args map[string]any) (string, error) {
	issue, pr := rc.issue, rc.pr
	if rc.sess.ResultPullNumber > 0 {
		if is, err := rc.m.forge.Issue(rc.ctx, rc.repo, rc.sess.ResultPullNumber); err == nil {
			if p, err := rc.m.forge.Pull(rc.ctx, is); err == nil {
				issue, pr = is, p
			}
		}
	}
	if issue == nil || pr == nil {
		return "", fmt.Errorf("no pull request to request reviews on")
	}
	var ids, unknown []string
	for _, name := range argStrings(args, "usernames") {
		u, _ := rc.m.store.GetUserByUsername(rc.ctx, strings.TrimPrefix(name, "@"))
		if u == nil {
			unknown = append(unknown, name)
			continue
		}
		ids = append(ids, u.ID)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no known users among %s", strings.Join(unknown, ", "))
	}
	if err := rc.m.forge.RequestReviewers(rc.ctx, rc.repo, rc.actor, issue, ids, nil, false); err != nil {
		return "", err
	}
	out := fmt.Sprintf("requested %d reviewers on #%d", len(ids), issue.Number)
	if len(unknown) > 0 {
		out += fmt.Sprintf(", unknown users skipped: %s", strings.Join(unknown, ", "))
	}
	return out, nil
}

// ── Descriptions shared by tools and prompts ─────────────────────────────

func (m *Manager) describeIssue(ctx context.Context, repo *db.GitRepository, issue *db.GitIssue) string {
	var b strings.Builder
	kind := "Issue"
	if issue.IsPull {
		kind = "Pull request"
	}
	fmt.Fprintf(&b, "%s #%d: %s\nState: %s\n", kind, issue.Number, issue.Title, issue.State)
	if u, _ := m.store.GetUserByID(ctx, issue.AuthorID); u != nil {
		fmt.Fprintf(&b, "Author: @%s\n", u.Username)
	}
	if labels, _ := m.store.GetGitIssueLabels(ctx, []string{issue.ID}); len(labels[issue.ID]) > 0 {
		var names []string
		for _, l := range labels[issue.ID] {
			names = append(names, l.Name)
		}
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(names, ", "))
	}
	if assignees, _ := m.store.GetGitIssueAssignees(ctx, []string{issue.ID}); len(assignees[issue.ID]) > 0 {
		var names []string
		for _, u := range assignees[issue.ID] {
			names = append(names, "@"+u.Username)
		}
		fmt.Fprintf(&b, "Assignees: %s\n", strings.Join(names, ", "))
	}
	if issue.MilestoneID != nil {
		if ms, _ := m.store.GetGitMilestone(ctx, repo.ID, *issue.MilestoneID); ms != nil {
			fmt.Fprintf(&b, "Milestone: %s\n", ms.Title)
		}
	}
	body := strings.TrimSpace(issue.Body)
	if body == "" {
		body = "(no description)"
	}
	fmt.Fprintf(&b, "\nDescription:\n%s\n", body)
	m.writeComments(ctx, &b, issue)
	return b.String()
}

func (m *Manager) writeComments(ctx context.Context, b *strings.Builder, issue *db.GitIssue) {
	comments, total, err := m.store.ListGitComments(ctx, issue.ID, true, 0, 0)
	if err != nil || len(comments) == 0 {
		return
	}
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.AuthorID)
	}
	users, _ := m.store.GetUsersByIDs(ctx, ids)
	fmt.Fprintf(b, "\nComments (%d total, newest last):\n", total)
	for _, c := range comments {
		name := "unknown"
		if u := users[c.AuthorID]; u != nil {
			name = u.Username
			if u.AuthProvider == db.AuthProviderAgent {
				name += " (agent)"
			}
		}
		fmt.Fprintf(b, "--- @%s at %s (id %s):\n%s\n", name, c.CreatedAt.UTC().Format(time.RFC3339), c.ID, strings.TrimSpace(c.Body))
	}
}

func (m *Manager) describePull(ctx context.Context, repo *db.GitRepository, actor forge.Actor, issue *db.GitIssue, pr *db.GitPullRequest) string {
	var b strings.Builder
	b.WriteString(m.describeIssue(ctx, repo, issue))
	headRepo := repo.Namespace + "/" + repo.Name
	if hr, _ := m.store.GetGitRepositoryByID(ctx, pr.HeadRepoID); hr != nil {
		headRepo = hr.Namespace + "/" + hr.Name
	}
	fmt.Fprintf(&b, "\nBase branch: %s\nHead branch: %s (%s)\nHead commit: %s\nDraft: %v\n", pr.BaseBranch, pr.HeadBranch, headRepo, pr.HeadSHA, pr.Draft)
	if pr.MergedAt != nil {
		fmt.Fprintf(&b, "Merged: yes (%s)\n", pr.MergeCommitSHA)
	}
	fmt.Fprintf(&b, "Changes: %d files, +%d -%d, %d commits\n", pr.ChangedFiles, pr.Additions, pr.Deletions, pr.CommitCount)
	if commits, err := m.forge.PullCommits(ctx, repo, pr, 30); err == nil && len(commits) > 0 {
		b.WriteString("Commits:\n")
		for _, c := range commits {
			fmt.Fprintf(&b, "  %s %s\n", c.Hash.String()[:10], firstLine(c.Message))
		}
	}
	if reviews, _, err := m.store.ListGitReviews(ctx, issue.ID, false, 0, 0); err == nil && len(reviews) > 0 {
		b.WriteString("Reviews:\n")
		for _, rv := range reviews {
			name := m.usernameOf(ctx, rv.ReviewerID)
			state := rv.State
			if rv.Dismissed {
				state += " (dismissed)"
			}
			fmt.Fprintf(&b, "  @%s: %s", name, state)
			if body := strings.TrimSpace(rv.Body); body != "" {
				fmt.Fprintf(&b, " - %s", body)
			}
			b.WriteByte('\n')
		}
	}
	if threads, err := m.store.ListGitReviewComments(ctx, issue.ID, "", ""); err == nil && len(threads) > 0 {
		b.WriteString("Review comments:\n")
		for _, c := range threads {
			flags := ""
			if c.Resolved {
				flags += " resolved"
			}
			if c.OutdatedSHA != "" {
				flags += " outdated"
			}
			fmt.Fprintf(&b, "  %s:%d (%s side)%s @%s: %s\n", c.Path, c.Line, c.Side, flags, m.usernameOf(ctx, c.AuthorID), strings.TrimSpace(c.Body))
		}
	}
	if statuses, _ := m.store.LatestGitCommitStatuses(ctx, repo.ID, pr.HeadSHA); len(statuses) > 0 {
		b.WriteString("Checks on head:\n")
		for _, s := range statuses {
			fmt.Fprintf(&b, "  %s: %s %s\n", s.Context, s.State, s.Description)
		}
	}
	if mg, err := m.forge.Mergeability(ctx, repo, actor, issue, pr); err == nil {
		fmt.Fprintf(&b, "Mergeable state: %s\n", mg.State)
		for _, bl := range mg.Blockers {
			fmt.Fprintf(&b, "  blocker %s: %s\n", bl.Rule, bl.Message)
		}
		if len(mg.Conflicts) > 0 {
			fmt.Fprintf(&b, "  conflicts: %s\n", strings.Join(mg.Conflicts, ", "))
		}
	}
	return b.String()
}

func (m *Manager) usernameOf(ctx context.Context, id string) string {
	if id == "" {
		return "unknown"
	}
	u, _ := m.store.GetUserByID(ctx, id)
	if u == nil {
		return "unknown"
	}
	return u.Username
}
