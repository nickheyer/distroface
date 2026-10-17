package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	gittransport "github.com/go-git/go-git/v6/plumbing/transport"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v6/plumbing/transport/ssh"
	"github.com/nickheyer/distroface/internal/db"
	"golang.org/x/crypto/ssh"
)

// Transport option for fetches and pushes
type fetchOption = client.Option

// Options building a transport client for one remote
type RemoteAuth struct {
	HTTPClient *http.Client
	Username   string
	Password   string
	SSHKeyPEM  []byte
	HostKey    string
	OnHostKey  func(fingerprint string)
	// Vetted dialer for ssh and git transports, nil dials directly
	Dialer gittransport.DialContextFunc
}

// Client options for a remote url
func (a RemoteAuth) Options(e *Engine) []client.Option {
	opts := []client.Option{client.WithLoader(e.Loader())}
	if a.HTTPClient != nil {
		opts = append(opts, client.WithHTTPClient(a.HTTPClient))
	}
	if a.Dialer != nil {
		opts = append(opts, client.WithDialer(a.Dialer))
	}
	if a.Password != "" {
		user := a.Username
		if user == "" {
			user = "token"
		}
		opts = append(opts, client.WithHTTPAuth(&githttp.BasicAuth{Username: user, Password: a.Password}))
	}
	if len(a.SSHKeyPEM) > 0 {
		keys, err := gitssh.NewPublicKeys("git", a.SSHKeyPEM, "")
		if err == nil {
			keys.HostKeyCallback = a.hostKeyCallback()
			opts = append(opts, client.WithSSHAuth(keys))
		}
	}
	return opts
}

// Pins the host key on first contact and refuses changes afterwards
func (a RemoteAuth) hostKeyCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if a.HostKey == "" {
			if a.OnHostKey != nil {
				a.OnHostKey(fp)
			}
			return nil
		}
		if fp != a.HostKey {
			return fmt.Errorf("host key for %s changed, expected %s got %s", hostname, a.HostKey, fp)
		}
		return nil
	}
}

// Fetches refspecs from a remote into an open repository
func fetchRefs(ctx context.Context, e *Engine, r *Repo, remoteURL string, refspecs []string, opts []client.Option) error {
	if len(opts) == 0 {
		opts = []client.Option{client.WithLoader(e.Loader())}
	}
	specs := make([]config.RefSpec, 0, len(refspecs))
	for _, s := range refspecs {
		specs = append(specs, config.RefSpec(s))
	}
	remote, err := r.Repo.CreateRemoteAnonymous(&config.RemoteConfig{Name: "anonymous", URLs: []string{remoteURL}})
	if err != nil {
		return err
	}
	err = remote.FetchContext(ctx, &git.FetchOptions{
		RemoteURL: remoteURL, RefSpecs: specs, Force: true, Prune: true, Tags: git.NoTags, ClientOptions: opts,
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

// Pushes refspecs from an open repository to a remote
func pushRefs(ctx context.Context, e *Engine, r *Repo, remoteURL string, refspecs []string, opts []client.Option) error {
	if len(opts) == 0 {
		opts = []client.Option{client.WithLoader(e.Loader())}
	}
	specs := make([]config.RefSpec, 0, len(refspecs))
	for _, s := range refspecs {
		specs = append(specs, config.RefSpec(s))
	}
	remote, err := r.Repo.CreateRemoteAnonymous(&config.RemoteConfig{Name: "anonymous", URLs: []string{remoteURL}})
	if err != nil {
		return err
	}
	err = remote.PushContext(ctx, &git.PushOptions{RemoteURL: remoteURL, RefSpecs: specs, Force: true, Prune: true, ClientOptions: opts})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

// Lists refs of a remote within a timeout, used by mirror validation
func listRemote(ctx context.Context, remoteURL string, opts []client.Option, timeout time.Duration) ([]*plumbing.Reference, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	remote := git.NewRemote(nil, &config.RemoteConfig{Name: "probe", URLs: []string{remoteURL}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{ClientOptions: opts})
	if err != nil {
		if errors.Is(err, gittransport.ErrAuthenticationRequired) {
			return nil, fmt.Errorf("%w: upstream requires credentials", ErrInvalid)
		}
		if errors.Is(err, gittransport.ErrAuthorizationFailed) {
			return nil, fmt.Errorf("%w: upstream rejected the credentials", ErrInvalid)
		}
		return nil, err
	}
	return refs, nil
}

// Exported wrappers used by the mirror package
func (m *Manager) FetchRemote(ctx context.Context, repo *db.GitRepository, wiki bool, remoteURL string, refspecs []string, auth RemoteAuth) error {
	return m.fetchInto(ctx, repo, wiki, remoteURL, refspecs, auth.Options(m.engine))
}

func (m *Manager) PushRemote(ctx context.Context, repo *db.GitRepository, remoteURL string, refspecs []string, auth RemoteAuth) error {
	r, err := m.engine.OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return err
	}
	defer r.Close()
	return pushRefs(ctx, m.engine, r, remoteURL, refspecs, auth.Options(m.engine))
}

func (m *Manager) ListRemote(ctx context.Context, remoteURL string, auth RemoteAuth, timeout time.Duration) ([]*plumbing.Reference, error) {
	return listRemote(ctx, remoteURL, auth.Options(m.engine), timeout)
}
