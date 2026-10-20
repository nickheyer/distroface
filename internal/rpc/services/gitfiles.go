package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/nickheyer/distroface/pkg/utils"
	"gopkg.in/yaml.v3"
)

var _ distrofacev1connect.GitFileServiceHandler = (*GitFileService)(nil)

const (
	gitCommitsDefaultLimit = 30
	gitCommitsMaxLimit     = 100
	gitGrepLimit           = 500
	gitLastCommitBudget    = 2000
	// Largest file the web editor accepts, bigger files arrive by push
	gitMaxEditBytes = 25 << 20
)

type GitFileService struct {
	store   *stores.Store
	manager *gitrepo.Manager
	log     *logger.Logger
}

func NewGitFileService(store *stores.Store, manager *gitrepo.Manager, log *logger.Logger) *GitFileService {
	return &GitFileService{store: store, manager: manager, log: log}
}

// Loads a visible repository and opens it
func (s *GitFileService) open(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, *gitrepo.Repo, error) {
	repo, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, namespace, name)
	if err != nil {
		return nil, nil, err
	}
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, nil, mapGitErr(err)
	}
	return repo, r, nil
}

// Loads a repository for a code write and opens it
func (s *GitFileService) openForWrite(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string) (*storage.GitRepository, *gitrepo.Repo, error) {
	if err := portalAllowsGitWrite(ctx); err != nil {
		return nil, nil, err
	}
	repo, err := gitRepoWithLevel(ctx, s.store, s.manager.Access(), user, namespace, name, storage.GitUnitCode, gitrepo.LevelWrite)
	if err != nil {
		return nil, nil, err
	}
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, nil, mapGitErr(err)
	}
	return repo, r, nil
}

// Resolves a ref falling back to the default branch, 404 on misses
func gitResolveRef(r *gitrepo.Repo, repo *storage.GitRepository, ref string) (plumbing.Hash, error) {
	if ref == "" {
		ref = repo.DefaultBranch
	}
	if r.IsEmpty() {
		return plumbing.ZeroHash, connect.Errorf(connect.CodeNotFound, "repository is empty")
	}
	h, err := r.Resolve(ref)
	if err != nil {
		return plumbing.ZeroHash, connect.Errorf(connect.CodeNotFound, "ref %q not found", ref)
	}
	return h, nil
}

func gitCleanPath(p string) string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "." {
		return ""
	}
	return p
}

func (s *GitFileService) rawURL(ctx context.Context, repo *storage.GitRepository, ref, p string) string {
	return s.manager.HTMLURL(ctx, repo) + "/-/raw/" + ref + "/" + p
}

// ── Tree and blobs ───────────────────────────────────────────────────────

func (s *GitFileService) GetTree(ctx context.Context, req *v1.GetTreeRequest) (*v1.GetTreeResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	commit, err := gitResolveRef(r, repo, req.Ref)
	if err != nil {
		return nil, err
	}
	dir := gitCleanPath(req.Path)
	entries, truncated, err := r.Tree(ctx, commit, dir, req.Recursive)
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "path %q not found", dir)
	}
	out := &v1.GetTreeResponse{Sha: commit.String(), Path: dir, Truncated: truncated}
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	var last map[string]*object.Commit
	if req.WithLastCommit && !req.Recursive {
		paths := make([]string, 0, len(entries)+1)
		for _, e := range entries {
			paths = append(paths, e.Path)
		}
		if dir != "" {
			paths = append(paths, dir)
		}
		last, _ = r.LastCommits(ctx, commit, paths, gitLastCommitBudget)
		if dir == "" {
			if c, err := r.Commit(commit); err == nil {
				out.LastCommit = enrich.commit(c, false, false)
			}
		} else if c := last[dir]; c != nil {
			out.LastCommit = enrich.commit(c, false, false)
		}
	}
	for _, e := range entries {
		p := gitrepo.EntryToProto(e)
		if c := last[e.Path]; c != nil {
			p.LastCommit = enrich.commit(c, false, false)
		}
		out.Entries = append(out.Entries, p)
	}
	return out, nil
}

func (s *GitFileService) blobProto(ctx context.Context, repo *storage.GitRepository, ref string, b *gitrepo.Blob) *v1.GitBlob {
	raw := s.rawURL(ctx, repo, ref, b.Path)
	present, lfsURL := false, ""
	if b.LFS != nil {
		if obj, _ := s.store.GetLFSObject(ctx, repo.ID, b.LFS.OID); obj != nil {
			present = true
			lfsURL = raw + "?lfs=1"
		}
	}
	return gitBlobToProto(b, raw, present, lfsURL)
}

func (s *GitFileService) GetBlob(ctx context.Context, req *v1.GetBlobRequest) (*v1.GetBlobResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	commit, err := gitResolveRef(r, repo, req.Ref)
	if err != nil {
		return nil, err
	}
	p := gitCleanPath(req.Path)
	if p == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "path is required")
	}
	b, err := r.Blob(commit, p, gitrepo.MaxInlineBlob)
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "file %q not found", p)
	}
	ref := req.Ref
	if ref == "" {
		ref = repo.DefaultBranch
	}
	out := &v1.GetBlobResponse{Sha: commit.String(), Blob: s.blobProto(ctx, repo, ref, b)}
	if last, err := r.LastCommits(ctx, commit, []string{p}, gitLastCommitBudget); err == nil {
		if c := last[p]; c != nil {
			out.LastCommit = newGitCommitEnricher(ctx, s.store, s.manager, repo, r).commit(c, false, false)
		}
	}
	return out, nil
}

func (s *GitFileService) GetReadme(ctx context.Context, req *v1.GetReadmeRequest) (*v1.GetReadmeResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if r.IsEmpty() {
		return &v1.GetReadmeResponse{}, nil
	}
	commit, err := gitResolveRef(r, repo, req.Ref)
	if err != nil {
		return nil, err
	}
	b, err := r.Readme(commit, gitCleanPath(req.Path))
	if err != nil {
		return &v1.GetReadmeResponse{}, nil
	}
	ref := req.Ref
	if ref == "" {
		ref = repo.DefaultBranch
	}
	return &v1.GetReadmeResponse{Blob: s.blobProto(ctx, repo, ref, b)}, nil
}

// ── Commits ──────────────────────────────────────────────────────────────

func (s *GitFileService) ListCommits(ctx context.Context, req *v1.ListCommitsRequest) (*v1.ListCommitsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if r.IsEmpty() {
		return &v1.ListCommitsResponse{Page: &v1.PageInfo{}}, nil
	}
	from, err := gitResolveRef(r, repo, req.Ref)
	if err != nil {
		return nil, err
	}
	limit, _ := pages.Parse(req.Page)
	if limit <= 0 || limit > gitCommitsMaxLimit {
		limit = gitCommitsDefaultLimit
	}
	q := gitrepo.LogQuery{From: from, Path: gitCleanPath(req.Path), Author: req.Author, Limit: limit}
	if req.Since != nil {
		t := req.Since.AsTime()
		q.Since = &t
	}
	if req.Until != nil {
		t := req.Until.AsTime()
		q.Until = &t
	}
	if token := req.GetPage().GetPageToken(); token != "" {
		cursor, err := r.Resolve(token)
		if err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid page token")
		}
		q.Cursor = cursor
	}
	commits, next, err := r.Log(ctx, q)
	if err != nil {
		return nil, mapGitErr(err)
	}
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	enrich.loadStatuses(commits)
	out := make([]*v1.GitCommit, 0, len(commits))
	for _, c := range commits {
		out = append(out, enrich.commit(c, false, false))
	}
	return &v1.ListCommitsResponse{
		Commits: out,
		Page:    &v1.PageInfo{NextPageToken: next},
	}, nil
}

func (s *GitFileService) GetCommit(ctx context.Context, req *v1.GetCommitRequest) (*v1.GetCommitResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	h, err := gitResolveRef(r, repo, req.Sha)
	if err != nil {
		return nil, err
	}
	c, err := r.Commit(h)
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "commit %q not found", req.Sha)
	}
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	enrich.loadStatuses([]*object.Commit{c})
	statuses, _ := s.store.LatestGitCommitStatuses(ctx, repo.ID, c.Hash.String())
	users := newGitUserCache(s.store)
	out := &v1.GetCommitResponse{Commit: enrich.commit(c, true, true)}
	for _, st := range statuses {
		out.Statuses = append(out.Statuses, gitCommitStatusToProto(ctx, users, st))
	}
	out.Branches, out.Tags = r.RefsAt(c.Hash)
	return out, nil
}

func (s *GitFileService) GetCommitDiff(ctx context.Context, req *v1.GetCommitDiffRequest) (*v1.GetCommitDiffResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	h, err := gitResolveRef(r, repo, req.Sha)
	if err != nil {
		return nil, err
	}
	c, err := r.Commit(h)
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "commit %q not found", req.Sha)
	}
	parent := int(max(req.Parent, 0))
	limit, offset := pages.Parse(req.Page)
	diff, err := r.CommitDiff(ctx, h, parent, gitrepo.DiffOptions{Limit: limit, Offset: offset, PathQuery: pages.ParseQuery(req.Page).Text})
	if err != nil {
		return nil, mapGitErr(err)
	}
	out := &v1.GetCommitDiffResponse{Diff: diff, Page: pages.Info(offset, limit, int64(diff.TotalFiles))}
	if c.NumParents() > parent {
		out.BaseSha = c.ParentHashes[parent].String()
	}
	return out, nil
}

// Resolves a head of the form ns/name:ref against another visible repository
func (s *GitFileService) resolveHead(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, r *gitrepo.Repo, head string) (plumbing.Hash, error) {
	if i := strings.Index(head, ":"); i > 0 && strings.Contains(head[:i], "/") {
		ns, name, _ := strings.Cut(head[:i], "/")
		other, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, ns, name)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		or, err := s.manager.Engine().OpenRepo(other.Namespace, other.Name, false)
		if err != nil {
			return plumbing.ZeroHash, mapGitErr(err)
		}
		defer or.Close()
		h, err := gitResolveRef(or, other, head[i+1:])
		if err != nil {
			return plumbing.ZeroHash, err
		}
		// Objects must live in the base repository for a diff to be computed
		if _, err := r.Commit(h); err != nil {
			if maxMB := s.manager.GitSettings(ctx, repo).GetMaxRepoSizeMb(); maxMB > 0 {
				if size, err := s.manager.Engine().Size(repo.Namespace, repo.Name, false); err == nil && size > maxMB*1024*1024 {
					return plumbing.ZeroHash, connect.Errorf(connect.CodeResourceExhausted, "repository is over its size quota, compare across repositories is unavailable")
				}
			}
			staging := plumbing.ReferenceName("refs/distroface/compare/" + h.String())
			if err := s.manager.FetchLocal(ctx, repo, other, false, "+"+plumbing.NewBranchReferenceName(head[i+1:]).String()+":"+staging.String()); err != nil {
				return plumbing.ZeroHash, mapGitErr(err)
			}
			// The ref only had to exist for the fetch, the objects stay until pruned
			s.manager.DropStagingRef(repo, staging)
		}
		return h, nil
	}
	h, err := gitResolveRef(r, repo, head)
	return h, err
}

// Resolved ends of a comparison
type gitCompareTarget struct {
	repo      *storage.GitRepository
	r         *gitrepo.Repo
	base      plumbing.Hash
	head      plumbing.Hash
	mergeBase plumbing.Hash
}

// Tree the comparison diffs from, the merge base when one exists
func (t gitCompareTarget) from() plumbing.Hash {
	if t.mergeBase.IsZero() {
		return t.base
	}
	return t.mergeBase
}

func (t gitCompareTarget) mergeBaseString() string {
	if t.mergeBase.IsZero() {
		return ""
	}
	return t.mergeBase.String()
}

// Opens the repository and resolves base, head, and their merge base, caller closes r
func (s *GitFileService) compareTarget(ctx context.Context, namespace, name, base, head string) (*gitCompareTarget, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, namespace, name)
	if err != nil {
		return nil, err
	}
	baseHash, err := gitResolveRef(r, repo, base)
	if err != nil {
		r.Close()
		return nil, err
	}
	headHash, err := s.resolveHead(ctx, user, repo, r, head)
	if err != nil {
		r.Close()
		return nil, err
	}
	mergeBase, err := r.MergeBase(baseHash, headHash)
	if err != nil {
		mergeBase = plumbing.ZeroHash
	}
	return &gitCompareTarget{repo: repo, r: r, base: baseHash, head: headHash, mergeBase: mergeBase}, nil
}

// Loads one page of commits from an oldest first hash list
func gitPageCommits(r *gitrepo.Repo, hashes []plumbing.Hash, page *v1.PageRequest) ([]*object.Commit, *v1.PageInfo, error) {
	limit, offset := pages.Parse(page)
	total := len(hashes)
	offset = min(offset, total)
	end := min(offset+limit, total)
	commits := make([]*object.Commit, 0, end-offset)
	for _, h := range hashes[offset:end] {
		c, err := r.Commit(h)
		if err != nil {
			return nil, nil, err
		}
		commits = append(commits, c)
	}
	return commits, pages.Info(offset, limit, int64(total)), nil
}

func (s *GitFileService) CompareRefs(ctx context.Context, req *v1.CompareRefsRequest) (*v1.CompareRefsResponse, error) {
	t, err := s.compareTarget(ctx, req.Namespace, req.Name, req.Base, req.Head)
	if err != nil {
		return nil, err
	}
	defer t.r.Close()
	hashes, err := t.r.CommitRange(ctx, t.from(), t.head)
	if err != nil {
		return nil, mapGitErr(err)
	}
	_, behind, _ := t.r.AheadBehind(ctx, t.head, t.base, gitAheadBehindLimit)
	diff, err := t.r.Diff(ctx, t.from(), t.head, gitrepo.DiffOptions{})
	if err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.CompareRefsResponse{
		BaseSha: t.base.String(), HeadSha: t.head.String(), MergeBaseSha: t.mergeBaseString(),
		Ahead: int32(len(hashes)), Behind: behind,
		ChangedFiles: diff.TotalFiles, Additions: diff.Additions, Deletions: diff.Deletions,
	}, nil
}

func (s *GitFileService) ListCompareCommits(ctx context.Context, req *v1.ListCompareCommitsRequest) (*v1.ListCompareCommitsResponse, error) {
	t, err := s.compareTarget(ctx, req.Namespace, req.Name, req.Base, req.Head)
	if err != nil {
		return nil, err
	}
	defer t.r.Close()
	hashes, err := t.r.CommitRange(ctx, t.from(), t.head)
	if err != nil {
		return nil, mapGitErr(err)
	}
	commits, info, err := gitPageCommits(t.r, hashes, req.Page)
	if err != nil {
		return nil, mapGitErr(err)
	}
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, t.repo, t.r)
	enrich.loadStatuses(commits)
	out := &v1.ListCompareCommitsResponse{Page: info}
	for _, c := range commits {
		out.Commits = append(out.Commits, enrich.commit(c, false, false))
	}
	return out, nil
}

func (s *GitFileService) GetCompareDiff(ctx context.Context, req *v1.GetCompareDiffRequest) (*v1.GetCompareDiffResponse, error) {
	t, err := s.compareTarget(ctx, req.Namespace, req.Name, req.Base, req.Head)
	if err != nil {
		return nil, err
	}
	defer t.r.Close()
	limit, offset := pages.Parse(req.Page)
	diff, err := t.r.Diff(ctx, t.from(), t.head, gitrepo.DiffOptions{Limit: limit, Offset: offset, PathQuery: pages.ParseQuery(req.Page).Text})
	if err != nil {
		return nil, mapGitErr(err)
	}
	return &v1.GetCompareDiffResponse{Diff: diff, Page: pages.Info(offset, limit, int64(diff.TotalFiles)), BaseSha: t.from().String(), HeadSha: t.head.String()}, nil
}

func (s *GitFileService) GetDiffFile(ctx context.Context, req *v1.GetDiffFileRequest) (*v1.GetDiffFileResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	head, err := s.resolveHead(ctx, user, repo, r, req.Head)
	if err != nil {
		return nil, err
	}
	var base plumbing.Hash
	if req.Base != "" {
		if base, err = gitResolveRef(r, repo, req.Base); err != nil {
			return nil, err
		}
	} else if c, err := r.Commit(head); err == nil && c.NumParents() > 0 {
		base = c.ParentHashes[0]
	}
	p := gitCleanPath(req.Path)
	diff, err := r.Diff(ctx, base, head, gitrepo.DiffOptions{OnlyPath: p, Limit: 1})
	if err != nil {
		return nil, mapGitErr(err)
	}
	if len(diff.Files) == 0 {
		return nil, connect.Errorf(connect.CodeNotFound, "path %q did not change", p)
	}
	return &v1.GetDiffFileResponse{File: diff.Files[0]}, nil
}

func (s *GitFileService) Blame(ctx context.Context, req *v1.BlameRequest) (*v1.BlameResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	commit, err := gitResolveRef(r, repo, req.Ref)
	if err != nil {
		return nil, err
	}
	p := gitCleanPath(req.Path)
	runs, err := r.Blame(ctx, commit, p)
	if err != nil {
		if errors.Is(err, gitrepo.ErrNotFound) {
			return nil, connect.Errorf(connect.CodeNotFound, "file %q not found", p)
		}
		return nil, mapGitErr(err)
	}
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	out := &v1.BlameResponse{Sha: commit.String()}
	for _, run := range runs {
		out.Ranges = append(out.Ranges, &v1.GitBlameRange{
			StartLine: int32(run.Start), EndLine: int32(run.End), Commit: enrich.commit(run.Commit, false, false), Lines: run.Lines,
		})
	}
	return out, nil
}

func (s *GitFileService) SearchCode(ctx context.Context, req *v1.SearchCodeRequest) (*v1.SearchCodeResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "pattern is required")
	}
	commit, err := gitResolveRef(r, repo, req.Ref)
	if err != nil {
		return nil, err
	}
	results, err := r.Grep(commit, req.Pattern, req.PathGlob, req.CaseInsensitive, gitGrepLimit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	limit, offset := pages.Parse(req.Page)
	total := int64(len(results))
	if offset > len(results) {
		offset = len(results)
	}
	end := min(offset+limit, len(results))
	out := &v1.SearchCodeResponse{Sha: commit.String(), Page: pages.Info(offset, limit, total)}
	for _, res := range results[offset:end] {
		out.Matches = append(out.Matches, &v1.GitGrepMatch{Path: res.FileName, LineNumber: int32(res.LineNumber), Line: res.Content})
	}
	return out, nil
}

// ── Web commits ──────────────────────────────────────────────────────────

func gitFileMode(executable bool) filemode.FileMode {
	if executable {
		return filemode.Executable
	}
	return filemode.Regular
}

func (s *GitFileService) UpdateFiles(ctx context.Context, req *v1.UpdateFilesRequest) (*v1.UpdateFilesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.openForWrite(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	r.Close()
	if len(req.Operations) == 0 {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "at least one operation is required")
	}
	if strings.TrimSpace(req.Message) == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a commit message is required")
	}
	branch := req.Branch
	if branch == "" {
		branch = repo.DefaultBranch
	}
	if req.NewBranch != "" && !gitValidRefName(req.NewBranch) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid branch name")
	}
	edits := make([]gitrepo.TreeEdit, 0, len(req.Operations))
	for _, op := range req.Operations {
		p := gitCleanPath(op.Path)
		if p == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "operation path is required")
		}
		if len(op.Content) > gitMaxEditBytes {
			return nil, connect.Errorf(connect.CodeResourceExhausted, "%s exceeds the %d MB web edit limit, push it with git instead", p, gitMaxEditBytes>>20)
		}
		switch op.Kind {
		case v1.FileOperationKind_FILE_OPERATION_KIND_CREATE, v1.FileOperationKind_FILE_OPERATION_KIND_UPDATE:
			edits = append(edits, gitrepo.TreeEdit{Path: p, Mode: gitFileMode(op.Executable), Content: bytes.NewReader(op.Content)})
		case v1.FileOperationKind_FILE_OPERATION_KIND_DELETE:
			edits = append(edits, gitrepo.TreeEdit{Path: p})
		case v1.FileOperationKind_FILE_OPERATION_KIND_RENAME:
			from := gitCleanPath(op.FromPath)
			if from == "" {
				return nil, connect.Errorf(connect.CodeInvalidArgument, "rename needs a source path")
			}
			var content *bytes.Reader
			if op.Content != nil {
				content = bytes.NewReader(op.Content)
			}
			e := gitrepo.TreeEdit{Path: p, Mode: gitFileMode(op.Executable), Rename: from}
			if content != nil {
				e.Content = content
			}
			edits = append(edits, e)
		default:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "operation kind is required")
		}
	}
	var expected plumbing.Hash
	if req.ExpectedHeadSha != "" {
		expected = plumbing.NewHash(req.ExpectedHeadSha)
	}
	sha, err := s.manager.CommitToBranch(ctx, repo, user, gitrepo.CommitRequest{
		Branch: branch, NewBranch: req.NewBranch, ExpectedHead: expected, Message: req.Message, Edits: edits,
	})
	if err != nil {
		return nil, mapGitErr(err)
	}
	target := branch
	if req.NewBranch != "" {
		target = req.NewBranch
	}
	return &v1.UpdateFilesResponse{Commit: s.commitAfterWrite(ctx, repo, sha), Branch: target}, nil
}

// Reads a commit back after a write for the response
func (s *GitFileService) commitAfterWrite(ctx context.Context, repo *storage.GitRepository, sha plumbing.Hash) *v1.GitCommit {
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return &v1.GitCommit{Sha: sha.String()}
	}
	defer r.Close()
	c, err := r.Commit(sha)
	if err != nil {
		return &v1.GitCommit{Sha: sha.String()}
	}
	return newGitCommitEnricher(ctx, s.store, s.manager, repo, r).commit(c, true, true)
}

// Applies or reverses one commit on a branch through the merge engine
func (s *GitFileService) replay(ctx context.Context, namespace, name, sha, branch, newBranch string, revert bool) (*v1.GitCommit, string, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.openForWrite(ctx, user, namespace, name)
	if err != nil {
		return nil, "", err
	}
	h, err := gitResolveRef(r, repo, sha)
	if err != nil {
		r.Close()
		return nil, "", err
	}
	c, err := r.Commit(h)
	r.Close()
	if err != nil {
		return nil, "", connect.Errorf(connect.CodeNotFound, "commit %q not found", sha)
	}
	if c.NumParents() > 1 {
		return nil, "", connect.Errorf(connect.CodeInvalidArgument, "merge commits cannot be replayed, pick one side")
	}
	if branch == "" {
		branch = repo.DefaultBranch
	}
	if newBranch != "" && !gitValidRefName(newBranch) {
		return nil, "", connect.Errorf(connect.CodeInvalidArgument, "invalid branch name")
	}
	parent := plumbing.ZeroHash
	if c.NumParents() == 1 {
		parent = c.ParentHashes[0]
	}
	verb, ancestor, theirs := "Cherry pick", parent, c.Hash
	if revert {
		verb, ancestor, theirs = "Revert", c.Hash, parent
	}
	summary := strings.SplitN(strings.TrimSpace(c.Message), "\n", 2)[0]
	message := fmt.Sprintf("%s %q\n\nThis %s commit %s.\n", verb, summary, strings.ToLower(verb)+"s", c.Hash.String())
	if !revert {
		message = c.Message
		if !strings.HasSuffix(message, "\n") {
			message += "\n"
		}
		message += "\n(cherry picked from commit " + c.Hash.String() + ")\n"
	}
	var conflicts []string
	build := func(r *gitrepo.Repo, head plumbing.Hash) (plumbing.Hash, error) {
		if head.IsZero() {
			return plumbing.ZeroHash, fmt.Errorf("%w: target branch does not exist", gitrepo.ErrInvalid)
		}
		res, err := r.Merge3(ctx, ancestor, head, theirs, true)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if !res.Clean() {
			for _, cf := range res.Conflicts {
				conflicts = append(conflicts, cf.Path)
			}
			return plumbing.ZeroHash, gitrepo.ErrMergeConflict
		}
		headTree, err := r.TreeOf(head)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if res.Tree == headTree {
			return plumbing.ZeroHash, fmt.Errorf("%w: nothing to %s", gitrepo.ErrInvalid, strings.ToLower(verb))
		}
		author := s.manager.Signature(ctx, user)
		if !revert {
			author = c.Author
		}
		return r.WriteCommit(ctx, gitrepo.CommitSpec{Tree: res.Tree, Parents: []plumbing.Hash{head}, Message: message, Author: author, Committer: s.manager.Signature(ctx, user), Signer: s.manager.Signer(ctx)})
	}
	newSHA, err := s.manager.CommitToBranch(ctx, repo, user, gitrepo.CommitRequest{Branch: branch, NewBranch: newBranch, Message: message, Build: build})
	if err != nil {
		if len(conflicts) > 0 {
			return nil, "", connect.Errorf(connect.CodeFailedPrecondition, "conflicts in %s", strings.Join(conflicts, ", "))
		}
		return nil, "", mapGitErr(err)
	}
	target := branch
	if newBranch != "" {
		target = newBranch
	}
	return s.commitAfterWrite(ctx, repo, newSHA), target, nil
}

func (s *GitFileService) RevertCommit(ctx context.Context, req *v1.RevertCommitRequest) (*v1.RevertCommitResponse, error) {
	commit, branch, err := s.replay(ctx, req.Namespace, req.Name, req.Sha, req.Branch, req.NewBranch, true)
	if err != nil {
		return nil, err
	}
	return &v1.RevertCommitResponse{Commit: commit, Branch: branch}, nil
}

func (s *GitFileService) CherryPickCommit(ctx context.Context, req *v1.CherryPickCommitRequest) (*v1.CherryPickCommitResponse, error) {
	commit, branch, err := s.replay(ctx, req.Namespace, req.Name, req.Sha, req.Branch, req.NewBranch, false)
	if err != nil {
		return nil, err
	}
	return &v1.CherryPickCommitResponse{Commit: commit, Branch: branch}, nil
}

func (s *GitFileService) GetArchiveURL(ctx context.Context, req *v1.GetArchiveURLRequest) (*v1.GetArchiveURLResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	ref := req.Ref
	if ref == "" {
		ref = repo.DefaultBranch
	}
	if _, err := gitResolveRef(r, repo, ref); err != nil {
		return nil, err
	}
	format := req.Format
	switch format {
	case "", "zip":
		format = "zip"
	case "tar.gz", "tgz":
		format = "tar.gz"
	default:
		return nil, connect.Errorf(connect.CodeInvalidArgument, "format must be zip or tar.gz")
	}
	return &v1.GetArchiveURLResponse{Url: s.manager.HTMLURL(ctx, repo) + "/-/archive/" + ref + "." + format}, nil
}

// ── Templates ────────────────────────────────────────────────────────────

// Folders searched for templates and codeowners, in priority order
var gitConfigDirs = []string{".distroface", ".gitea", ".github"}

// Issue template parsed from markdown front matter or a yaml form
type gitIssueTemplate struct {
	Filename  string
	Name      string
	About     string
	Title     string
	Body      string
	Labels    []string
	Assignees []string
	IsForm    bool
}

// Parses an issue template file, markdown with optional front matter or a yaml form
func gitParseIssueTemplate(filename string, content []byte) *gitIssueTemplate {
	t := &gitIssueTemplate{Filename: filename, Name: strings.TrimSuffix(path.Base(filename), path.Ext(filename))}
	ext := strings.ToLower(path.Ext(filename))
	if ext == ".yaml" || ext == ".yml" {
		var form struct {
			Name        string   `yaml:"name"`
			Description string   `yaml:"description"`
			Title       string   `yaml:"title"`
			Labels      []string `yaml:"labels"`
			Assignees   []string `yaml:"assignees"`
		}
		if err := yaml.Unmarshal(content, &form); err != nil {
			return nil
		}
		if form.Name != "" {
			t.Name = form.Name
		}
		t.About, t.Title, t.Labels, t.Assignees = form.Description, form.Title, form.Labels, form.Assignees
		t.Body = string(content)
		t.IsForm = true
		return t
	}
	body := string(content)
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---"); end >= 0 {
			var meta struct {
				Name      string   `yaml:"name"`
				About     string   `yaml:"about"`
				Title     string   `yaml:"title"`
				Labels    []string `yaml:"labels"`
				Assignees []string `yaml:"assignees"`
			}
			if yaml.Unmarshal([]byte(body[4:4+end]), &meta) == nil {
				if meta.Name != "" {
					t.Name = meta.Name
				}
				t.About, t.Title, t.Labels, t.Assignees = meta.About, meta.Title, meta.Labels, meta.Assignees
				body = strings.TrimLeft(body[4+end+4:], "\n")
			}
		}
	}
	t.Body = body
	return t
}

// Loads issue templates from the config folders of a commit
func gitLoadIssueTemplates(ctx context.Context, r *gitrepo.Repo, commit plumbing.Hash) []*gitIssueTemplate {
	var out []*gitIssueTemplate
	for _, dir := range gitConfigDirs {
		if b, err := r.Blob(commit, dir+"/ISSUE_TEMPLATE.md", gitrepo.MaxInlineBlob); err == nil {
			if t := gitParseIssueTemplate(b.Path, b.Content); t != nil {
				out = append(out, t)
			}
		}
		entries, _, err := r.Tree(ctx, commit, dir+"/ISSUE_TEMPLATE", false)
		if err != nil {
			continue
		}
		for _, e := range entries {
			ext := strings.ToLower(path.Ext(e.Name))
			if !e.Mode.IsFile() || (ext != ".md" && ext != ".yaml" && ext != ".yml") || strings.EqualFold(e.Name, "config.yml") || strings.EqualFold(e.Name, "config.yaml") {
				continue
			}
			b, err := r.Blob(commit, e.Path, gitrepo.MaxInlineBlob)
			if err != nil {
				continue
			}
			if t := gitParseIssueTemplate(b.Path, b.Content); t != nil {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

// Pull request template body from the config folders
func gitLoadPullTemplate(r *gitrepo.Repo, commit plumbing.Hash) string {
	for _, dir := range gitConfigDirs {
		for _, name := range []string{"PULL_REQUEST_TEMPLATE.md", "pull_request_template.md"} {
			if b, err := r.Blob(commit, dir+"/"+name, gitrepo.MaxInlineBlob); err == nil {
				return string(b.Content)
			}
		}
	}
	return ""
}

// Codeowners file content from the honored locations, empty when none
func gitLoadCodeowners(r *gitrepo.Repo, commit plumbing.Hash) string {
	for _, p := range []string{"CODEOWNERS", ".distroface/CODEOWNERS", ".gitea/CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		if b, err := r.Blob(commit, p, gitrepo.MaxInlineBlob); err == nil {
			return string(b.Content)
		}
	}
	return ""
}

func (s *GitFileService) GetTemplates(ctx context.Context, req *v1.GetTemplatesRequest) (*v1.GetTemplatesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := &v1.GetTemplatesResponse{}
	if r.IsEmpty() {
		return out, nil
	}
	commit, err := gitResolveRef(r, repo, repo.DefaultBranch)
	if err != nil {
		return out, nil
	}
	for _, t := range gitLoadIssueTemplates(ctx, r, commit) {
		out.IssueTemplates = append(out.IssueTemplates, &v1.GitIssueTemplate{
			Filename: t.Filename, Name: t.Name, About: t.About, Title: t.Title, Body: t.Body, Labels: t.Labels, Assignees: t.Assignees, IsForm: t.IsForm,
		})
	}
	out.PullRequestTemplate = gitLoadPullTemplate(r, commit)
	out.HasCodeowners = gitLoadCodeowners(r, commit) != ""
	return out, nil
}

// ── Commit statuses ──────────────────────────────────────────────────────

func (s *GitFileService) CreateCommitStatus(ctx context.Context, req *v1.CreateCommitStatusRequest) (*v1.CreateCommitStatusResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.openForWrite(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	h, err := gitResolveRef(r, repo, req.Sha)
	r.Close()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Context) == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "context is required")
	}
	state := gitStatusStateString(req.State)
	if state == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "state is required")
	}
	if req.TargetUrl != "" && !utils.IsWebURL(req.TargetUrl) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "target_url must be an http or https url")
	}
	st := &storage.GitCommitStatus{
		ID:          uuid.New().String(),
		RepoID:      repo.ID,
		SHA:         h.String(),
		Context:     strings.TrimSpace(req.Context),
		State:       state,
		TargetURL:   req.TargetUrl,
		Description: req.Description,
		CreatorID:   user.ID,
		CreatedAt:   time.Now(),
	}
	if err := s.store.CreateGitCommitStatus(ctx, st); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	s.resetMergeabilityForSHA(ctx, repo, h.String())
	return &v1.CreateCommitStatusResponse{Status: gitCommitStatusToProto(ctx, newGitUserCache(s.store), st)}, nil
}

// Open pull requests whose head is this commit recompute their merge state
func (s *GitFileService) resetMergeabilityForSHA(ctx context.Context, repo *storage.GitRepository, sha string) {
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return
	}
	branches, _ := r.RefsAt(plumbing.NewHash(sha))
	r.Close()
	for _, b := range branches {
		prs, _ := s.store.ListOpenPullsByHead(ctx, repo.ID, b)
		for _, pr := range prs {
			if pr.MergeableState == "blocked" {
				_ = s.store.UpdateGitPullRequestColumns(ctx, pr.IssueID, map[string]any{"mergeable_state": "unknown"})
			}
		}
	}
}

func (s *GitFileService) ListCommitStatuses(ctx context.Context, req *v1.ListCommitStatusesRequest) (*v1.ListCommitStatusesResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	h, err := gitResolveRef(r, repo, req.Sha)
	r.Close()
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListGitCommitStatuses(ctx, repo.ID, h.String(), limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	users := newGitUserCache(s.store)
	out := make([]*v1.GitCommitStatus, 0, len(rows))
	for _, st := range rows {
		out = append(out, gitCommitStatusToProto(ctx, users, st))
	}
	return &v1.ListCommitStatusesResponse{Statuses: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *GitFileService) GetCombinedStatus(ctx context.Context, req *v1.GetCombinedStatusRequest) (*v1.GetCombinedStatusResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, r, err := s.open(ctx, user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	h, err := gitResolveRef(r, repo, req.Sha)
	r.Close()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.LatestGitCommitStatuses(ctx, repo.ID, h.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	users := newGitUserCache(s.store)
	out := &v1.GetCombinedStatusResponse{State: gitCombinedState(rows)}
	for _, st := range rows {
		out.Statuses = append(out.Statuses, gitCommitStatusToProto(ctx, users, st))
	}
	return out, nil
}
