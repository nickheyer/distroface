package gitrepo

import (
	"net/url"
	"strings"

	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/go-git/go-git/v6/storage"
)

// Loader resolves in process file transport urls against the engine root
type Loader struct {
	engine *Engine
}

var _ transport.Loader = (*Loader)(nil)

// Loader used by fork and pull request fetches between local repositories
func (e *Engine) Loader() *Loader {
	return &Loader{engine: e}
}

// Load accepts /namespace/name.git or /namespace/name.wiki.git paths only
func (l *Loader) Load(u *url.URL) (storage.Storer, error) {
	path := strings.Trim(u.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return nil, transport.ErrRepositoryNotFound
	}
	namespace := parts[0]
	name := parts[1]
	wiki := false
	if strings.HasSuffix(name, ".wiki.git") {
		name = strings.TrimSuffix(name, ".wiki.git")
		wiki = true
	} else {
		name = strings.TrimSuffix(name, ".git")
	}
	st, _, err := l.engine.Storer(namespace, name, wiki)
	if err != nil {
		return nil, transport.ErrRepositoryNotFound
	}
	return st, nil
}

// File transport url for a local repository
func (e *Engine) LocalURL(namespace, name string, wiki bool) string {
	return "file:///" + RepoDir(namespace, name, wiki)
}
