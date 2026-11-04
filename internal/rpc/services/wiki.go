package services

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

var _ distrofacev1connect.WikiServiceHandler = (*WikiService)(nil)

// Landing page and the chrome files rendered around every page
const (
	wikiHomeTitle   = "Home"
	wikiSidebarFile = "_Sidebar.md"
	wikiFooterFile  = "_Footer.md"
	wikiBranch      = "main"
)

type WikiService struct {
	store   *stores.Store
	manager *gitrepo.Manager
	log     *logger.Logger
}

func NewWikiService(store *stores.Store, manager *gitrepo.Manager, log *logger.Logger) *WikiService {
	return &WikiService{store: store, manager: manager, log: log}
}

// Loads a repository with the wiki unit at the needed level
func (s *WikiService) repo(ctx context.Context, namespace, name string, need gitrepo.Level) (*auth.AuthenticatedUser, *storage.GitRepository, error) {
	user := auth.UserFromContext(ctx)
	if need > gitrepo.LevelRead {
		if err := portalAllowsGitWrite(ctx); err != nil {
			return nil, nil, err
		}
	}
	repo, err := gitRepoWithLevel(ctx, s.store, s.manager.Access(), user, namespace, name, storage.GitUnitWiki, need)
	if err != nil {
		return nil, nil, err
	}
	return user, repo, nil
}

// File name of a page title
func wikiFilename(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 || strings.Contains(title, "..") || strings.ContainsAny(title, "\\\x00") || strings.HasPrefix(title, "/") {
		return "", connect.Errorf(connect.CodeInvalidArgument, "invalid page title")
	}
	name := strings.ReplaceAll(title, " ", "-")
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	return name, nil
}

// Page title of a file name
func wikiTitle(filename string) string {
	name := strings.TrimSuffix(filename, ".md")
	return strings.ReplaceAll(name, "-", " ")
}

// Opens the wiki repository, nil when it was never created
func (s *WikiService) open(repo *storage.GitRepository) (*gitrepo.Repo, error) {
	if !s.manager.Engine().Exists(repo.Namespace, repo.Name, true) {
		return nil, nil
	}
	r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, true)
	if err != nil {
		return nil, mapGitErr(err)
	}
	return r, nil
}

// Branch the wiki pages live on
func wikiHead(r *gitrepo.Repo) string {
	if r == nil {
		return wikiBranch
	}
	if b := r.HeadBranch(); b != "" {
		return b
	}
	return wikiBranch
}

func (s *WikiService) pageProto(ctx context.Context, repo *storage.GitRepository, r *gitrepo.Repo, commit plumbing.Hash, filename string, withChrome bool) (*v1.WikiPage, error) {
	b, err := r.Blob(commit, filename, gitrepo.MaxInlineBlob)
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", wikiTitle(filename))
	}
	page := &v1.WikiPage{Title: wikiTitle(filename), Filename: filename, Content: string(b.Content)}
	if last, err := r.LastCommits(ctx, commit, []string{filename}, gitLastCommitBudget); err == nil {
		if c := last[filename]; c != nil {
			page.LastCommit = newGitCommitEnricher(ctx, s.store, s.manager, repo, r).commit(c, false, false)
		}
	}
	if withChrome {
		if sb, err := r.Blob(commit, wikiSidebarFile, gitrepo.MaxInlineBlob); err == nil {
			page.Sidebar = string(sb.Content)
		}
		if ft, err := r.Blob(commit, wikiFooterFile, gitrepo.MaxInlineBlob); err == nil {
			page.Footer = string(ft.Content)
		}
	}
	return page, nil
}

func (s *WikiService) ListWikiPages(ctx context.Context, req *v1.ListWikiPagesRequest) (*v1.ListWikiPagesResponse, error) {
	_, repo, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	out := &v1.ListWikiPagesResponse{HttpCloneUrl: s.manager.HTTPCloneURL(ctx, repo, true), SshCloneUrl: s.manager.SSHCloneURL(ctx, repo, true), Page: &v1.PageInfo{}}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	if r == nil || r.IsEmpty() {
		if r != nil {
			r.Close()
		}
		return out, nil
	}
	defer r.Close()
	head, err := r.Resolve(wikiHead(r))
	if err != nil {
		return out, nil
	}
	entries, truncated, err := r.Tree(ctx, head, "", true)
	if err == nil && truncated {
		err = fmt.Errorf("%w: wiki has more than %d pages", gitrepo.ErrTooLarge, gitrepo.MaxTreeEntries)
	}
	if err != nil {
		return nil, mapGitErr(err)
	}
	var files []string
	for _, e := range entries {
		if e.Mode.IsFile() && strings.HasSuffix(strings.ToLower(e.Name), ".md") && !strings.HasPrefix(e.Name, "_") {
			files = append(files, e.Path)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if wikiTitle(files[i]) == wikiHomeTitle {
			return true
		}
		if wikiTitle(files[j]) == wikiHomeTitle {
			return false
		}
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	if q := strings.ToLower(pages.ParseQuery(req.Page).Text); q != "" {
		filtered := files[:0]
		for _, f := range files {
			if strings.Contains(strings.ToLower(wikiTitle(f)), q) {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}
	limit, offset := pages.Parse(req.Page)
	total := int64(len(files))
	if offset > len(files) {
		offset = len(files)
	}
	end := min(offset+limit, len(files))
	window := files[offset:end]
	last, _ := r.LastCommits(ctx, head, window, gitLastCommitBudget)
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	for _, f := range window {
		p := &v1.WikiPage{Title: wikiTitle(f), Filename: f}
		if c := last[f]; c != nil {
			p.LastCommit = enrich.commit(c, false, false)
		}
		out.Pages = append(out.Pages, p)
	}
	out.Exists = len(files) > 0
	out.Page = pages.Info(offset, limit, total)
	return out, nil
}

func (s *WikiService) GetWikiPage(ctx context.Context, req *v1.GetWikiPageRequest) (*v1.GetWikiPageResponse, error) {
	_, repo, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	filename, err := wikiFilename(req.Title)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "the wiki has no pages yet")
	}
	defer r.Close()
	rev := req.Sha
	if rev == "" {
		rev = wikiHead(r)
	}
	commit, err := r.Resolve(rev)
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", req.Title)
	}
	page, err := s.pageProto(ctx, repo, r, commit, filename, true)
	if err != nil {
		return nil, err
	}
	return &v1.GetWikiPageResponse{Page: page}, nil
}

// Commits edits onto the wiki, creating the repository on first write
func (s *WikiService) commit(ctx context.Context, user *auth.AuthenticatedUser, repo *storage.GitRepository, message string, expected plumbing.Hash, edits []gitrepo.TreeEdit) (plumbing.Hash, error) {
	if !s.manager.Engine().Exists(repo.Namespace, repo.Name, true) {
		if err := s.manager.Engine().Init(repo.Namespace, repo.Name, true, wikiBranch, repo.ObjectFormat); err != nil {
			return plumbing.ZeroHash, mapGitErr(err)
		}
	}
	r, err := s.open(repo)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	branch := wikiHead(r)
	r.Close()
	sha, err := s.manager.CommitToBranch(ctx, repo, user, gitrepo.CommitRequest{
		Branch: branch, ExpectedHead: expected, Message: message, Edits: edits, Wiki: true,
		Options: gitrepo.HookOptions{Wiki: true, BypassPullRequest: true, BypassSignedCommits: true},
	})
	if err != nil {
		return plumbing.ZeroHash, mapGitErr(err)
	}
	return sha, nil
}

func (s *WikiService) pageAfterWrite(ctx context.Context, repo *storage.GitRepository, sha plumbing.Hash, filename string) (*v1.WikiPage, error) {
	r, err := s.open(repo)
	if err != nil || r == nil {
		return &v1.WikiPage{Title: wikiTitle(filename), Filename: filename}, nil
	}
	defer r.Close()
	return s.pageProto(ctx, repo, r, sha, filename, true)
}

func (s *WikiService) CreateWikiPage(ctx context.Context, req *v1.CreateWikiPageRequest) (*v1.CreateWikiPageResponse, error) {
	user, repo, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	filename, err := wikiFilename(req.Title)
	if err != nil {
		return nil, err
	}
	if r, err := s.open(repo); err != nil {
		return nil, err
	} else if r != nil {
		head, _ := r.Resolve(wikiHead(r))
		_, exists := r.Blob(head, filename, 1)
		r.Close()
		if exists == nil {
			return nil, connect.Errorf(connect.CodeAlreadyExists, "page %q already exists", req.Title)
		}
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "Create " + wikiTitle(filename)
	}
	sha, err := s.commit(ctx, user, repo, message, plumbing.ZeroHash, []gitrepo.TreeEdit{{Path: filename, Mode: filemode.Regular, Content: strings.NewReader(req.Content)}})
	if err != nil {
		return nil, err
	}
	page, err := s.pageAfterWrite(ctx, repo, sha, filename)
	if err != nil {
		return nil, err
	}
	return &v1.CreateWikiPageResponse{Page: page}, nil
}

func (s *WikiService) UpdateWikiPage(ctx context.Context, req *v1.UpdateWikiPageRequest) (*v1.UpdateWikiPageResponse, error) {
	user, repo, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	filename, err := wikiFilename(req.Title)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", req.Title)
	}
	head, err := r.Resolve(wikiHead(r))
	if err != nil {
		r.Close()
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", req.Title)
	}
	current, err := r.Blob(head, filename, gitrepo.MaxInlineBlob)
	r.Close()
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", req.Title)
	}
	target := filename
	if req.NewTitle != nil && strings.TrimSpace(*req.NewTitle) != "" {
		if target, err = wikiFilename(*req.NewTitle); err != nil {
			return nil, err
		}
	}
	content := string(current.Content)
	if req.Content != nil {
		content = *req.Content
	}
	if target == filename && content == string(current.Content) {
		page, err := s.pageAfterWrite(ctx, repo, head, filename)
		if err != nil {
			return nil, err
		}
		return &v1.UpdateWikiPageResponse{Page: page}, nil
	}
	var expected plumbing.Hash
	if req.ExpectedSha != "" {
		expected = plumbing.NewHash(req.ExpectedSha)
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "Update " + wikiTitle(filename)
	}
	edit := gitrepo.TreeEdit{Path: target, Mode: filemode.Regular, Content: strings.NewReader(content)}
	if target != filename {
		edit.Rename = filename
	}
	sha, err := s.commit(ctx, user, repo, message, expected, []gitrepo.TreeEdit{edit})
	if err != nil {
		return nil, err
	}
	page, err := s.pageAfterWrite(ctx, repo, sha, target)
	if err != nil {
		return nil, err
	}
	return &v1.UpdateWikiPageResponse{Page: page}, nil
}

func (s *WikiService) DeleteWikiPage(ctx context.Context, req *v1.DeleteWikiPageRequest) (*v1.DeleteWikiPageResponse, error) {
	user, repo, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	filename, err := wikiFilename(req.Title)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", req.Title)
	}
	head, err := r.Resolve(wikiHead(r))
	if err == nil {
		_, err = r.Blob(head, filename, 1)
	}
	r.Close()
	if err != nil {
		return nil, connect.Errorf(connect.CodeNotFound, "page %q not found", req.Title)
	}
	if _, err := s.commit(ctx, user, repo, "Delete "+wikiTitle(filename), plumbing.ZeroHash, []gitrepo.TreeEdit{{Path: filename}}); err != nil {
		return nil, err
	}
	return &v1.DeleteWikiPageResponse{}, nil
}

func (s *WikiService) ListWikiRevisions(ctx context.Context, req *v1.ListWikiRevisionsRequest) (*v1.ListWikiRevisionsResponse, error) {
	_, repo, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	filename, err := wikiFilename(req.Title)
	if err != nil {
		return nil, err
	}
	r, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &v1.ListWikiRevisionsResponse{Page: &v1.PageInfo{}}, nil
	}
	defer r.Close()
	head, err := r.Resolve(wikiHead(r))
	if err != nil {
		return &v1.ListWikiRevisionsResponse{Page: &v1.PageInfo{}}, nil
	}
	limit, _ := pages.Parse(req.Page)
	if limit <= 0 || limit > gitCommitsMaxLimit {
		limit = gitCommitsDefaultLimit
	}
	q := gitrepo.LogQuery{From: head, Path: filename, Limit: limit}
	if token := req.GetPage().GetPageToken(); token != "" {
		q.Cursor = plumbing.NewHash(token)
	}
	commits, next, err := r.Log(ctx, q)
	if err != nil {
		return nil, mapGitErr(err)
	}
	enrich := newGitCommitEnricher(ctx, s.store, s.manager, repo, r)
	out := &v1.ListWikiRevisionsResponse{Page: &v1.PageInfo{NextPageToken: next}}
	for _, c := range commits {
		out.Commits = append(out.Commits, enrich.commit(c, false, false))
	}
	return out, nil
}
