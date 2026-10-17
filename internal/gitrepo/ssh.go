package gitrepo

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/ssh"
)

const (
	sshIdleTimeout    = 5 * time.Minute
	sshPerKeySessions = 8
)

// Portal lookup by ssh listener port
type SSHPortalResolver interface {
	ResolveSSH(port int) *portal.Portal
}

// SSHServer accepts git over ssh on one listener
type SSHServer struct {
	manager  *Manager
	lfs      *LFS
	portals  SSHPortalResolver
	log      *logger.Logger
	config   *ssh.ServerConfig
	hostKeys []ssh.Signer

	mu       sync.Mutex
	sessions map[string]int
}

func newSSHServer(manager *Manager, lfs *LFS, hostKeys []ssh.Signer, portals SSHPortalResolver, log *logger.Logger) *SSHServer {
	s := &SSHServer{manager: manager, lfs: lfs, portals: portals, log: log, sessions: map[string]int{}, hostKeys: hostKeys}
	s.config = &ssh.ServerConfig{
		PublicKeyCallback: s.publicKey,
		ServerVersion:     "SSH-2.0-distroface",
		MaxAuthTries:      6,
	}
	for _, k := range hostKeys {
		s.config.AddHostKey(k)
	}
	return s
}

// Loads or generates the ed25519 and rsa host keys
func LoadHostKeys(keysDir string) ([]ssh.Signer, error) {
	var signers []ssh.Signer
	ed := filepath.Join(keysDir, "ssh_host_ed25519_key")
	if _, err := os.Stat(ed); err != nil {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := writeKey(ed, priv); err != nil {
			return nil, err
		}
	}
	rsaPath := filepath.Join(keysDir, "ssh_host_rsa_key")
	if _, err := os.Stat(rsaPath); err != nil {
		priv, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return nil, err
		}
		if err := writeKey(rsaPath, priv); err != nil {
			return nil, err
		}
	}
	for _, p := range []string{ed, rsaPath} {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		signers = append(signers, signer)
	}
	return signers, nil
}

func writeKey(path string, key any) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return err
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".pub", ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644)
}

// Fingerprint lookup against user and deploy keys
func (s *SSHServer) publicKey(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	ctx := context.Background()
	fp := ssh.FingerprintSHA256(key)
	ip := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	// An agent offering unregistered keys is not an attack, MaxAuthTries bounds the probe
	row, err := s.manager.Store().GetSSHKeyByFingerprint(ctx, fp)
	if err != nil || row == nil || (row.ExpiresAt != nil && row.ExpiresAt.Before(time.Now())) || row.Usage == "signing" {
		s.manager.Audit(ctx, audit.Event{Action: "Git/auth", Resource: "auth", Outcome: audit.OutcomeDenied, Detail: "ssh " + fp, SourceIP: ip})
		return nil, errors.New("unknown key")
	}
	_ = s.manager.Store().TouchSSHKey(ctx, row.ID)
	return &ssh.Permissions{Extensions: map[string]string{"key-id": row.ID, "fingerprint": fp}}, nil
}

// Principal for an authenticated key
func (s *SSHServer) principal(ctx context.Context, perms *ssh.Permissions) (*auth.AuthenticatedUser, error) {
	row, err := s.manager.Store().GetSSHKey(ctx, perms.Extensions["key-id"])
	if err != nil || row == nil {
		return nil, errors.New("key vanished")
	}
	if row.OwnerType == db.SSHKeyOwnerGitRepo {
		repo, err := s.manager.Store().GetGitRepositoryByID(ctx, row.OwnerID)
		if err != nil || repo == nil {
			return nil, errors.New("deploy key repository vanished")
		}
		return &auth.AuthenticatedUser{
			ID: "deploykey:" + row.ID, Username: "deploy-key", Roles: []string{"user"}, Provider: "deploykey",
			Scope: &auth.CredentialScope{Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO, ID: repo.Namespace + "/" + repo.Name, ReadOnly: row.ReadOnly},
		}, nil
	}
	u, err := s.manager.Store().GetUserByID(ctx, row.OwnerID)
	if err != nil || u == nil || !u.IsActive {
		return nil, errors.New("account unavailable")
	}
	roles, _ := s.manager.Store().GetUserRoleNames(ctx, u.ID)
	user := &auth.AuthenticatedUser{ID: u.ID, Username: u.Username, Roles: roles, Provider: u.AuthProvider}
	if u.Email != nil {
		user.Email = *u.Email
	}
	return user, nil
}

// Accepts connections until the listener closes
func (s *SSHServer) Serve(ln net.Listener, port int) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn, port)
	}
}

func (s *SSHServer) handleConn(nc net.Conn, port int) {
	defer nc.Close()
	_ = nc.SetDeadline(time.Now().Add(30 * time.Second))
	sconn, chans, reqs, err := ssh.NewServerConn(nc, s.config)
	if err != nil {
		return
	}
	defer sconn.Close()
	_ = nc.SetDeadline(time.Time{})
	fp := sconn.Permissions.Extensions["fingerprint"]
	if !s.trackSession(fp, 1) {
		return
	}
	defer s.trackSession(fp, -1)
	go ssh.DiscardRequests(reqs)
	var p *portal.Portal
	if s.portals != nil {
		p = s.portals.ResolveSSH(port)
	}
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(sconn, ch, requests, nc.RemoteAddr().String(), p)
	}
}

func (s *SSHServer) trackSession(fp string, delta int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if delta > 0 && s.sessions[fp] >= sshPerKeySessions {
		return false
	}
	s.sessions[fp] += delta
	if s.sessions[fp] <= 0 {
		delete(s.sessions, fp)
	}
	return true
}

func (s *SSHServer) handleSession(sconn *ssh.ServerConn, ch ssh.Channel, requests <-chan *ssh.Request, remote string, p *portal.Portal) {
	defer ch.Close()
	env := map[string]string{}
	idle := time.NewTimer(sshIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-idle.C:
			fmt.Fprintln(ch.Stderr(), "session idle timeout")
			return
		case req, ok := <-requests:
			if !ok {
				return
			}
			switch req.Type {
			case "env":
				var kv struct{ Key, Value string }
				if ssh.Unmarshal(req.Payload, &kv) == nil && kv.Key == "GIT_PROTOCOL" {
					env[kv.Key] = kv.Value
				}
				_ = req.Reply(true, nil)
			case "exec":
				var payload struct{ Command string }
				if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
					_ = req.Reply(false, nil)
					return
				}
				_ = req.Reply(true, nil)
				idle.Stop()
				code := s.exec(sconn, ch, payload.Command, env, remote, p)
				_ = sendExit(ch, code)
				return
			case "shell", "pty-req":
				_ = req.Reply(req.Type == "shell", nil)
				if req.Type == "shell" {
					user, err := s.principal(context.Background(), sconn.Permissions)
					name := "there"
					if err == nil {
						name = user.Username
					}
					fmt.Fprintf(ch, "Hi %s! You've successfully authenticated, but Distroface does not provide shell access.\n", name)
					_ = sendExit(ch, 0)
					return
				}
			default:
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}
	}
}

func sendExit(ch ssh.Channel, code int) error {
	var payload struct{ Status uint32 }
	payload.Status = uint32(code)
	_, err := ch.SendRequest("exit-status", false, ssh.Marshal(&payload))
	return err
}

// Parsed exec command
type sshCommand struct {
	service string
	path    string
	args    []string
}

// Accepts git-upload-pack 'path' and git upload-pack path forms
func parseSSHCommand(raw string) (*sshCommand, error) {
	fields := shellSplit(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return nil, errors.New("empty command")
	}
	if fields[0] == "git" && len(fields) > 1 {
		fields = append([]string{"git-" + fields[1]}, fields[2:]...)
	}
	switch fields[0] {
	case transport.UploadPackService, transport.ReceivePackService, transport.UploadArchiveService, "git-lfs-authenticate", "git-lfs-transfer":
	default:
		return nil, fmt.Errorf("unsupported command %q", fields[0])
	}
	var path string
	var args []string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "--") {
			continue
		}
		if path == "" {
			path = f
			continue
		}
		args = append(args, f)
	}
	if path == "" {
		return nil, errors.New("missing repository path")
	}
	return &sshCommand{service: fields[0], path: path, args: args}, nil
}

// Splits respecting single and double quotes
func shellSplit(s string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// Resolves a repository path, applying portal mapping first
func (s *SSHServer) resolvePath(ctx context.Context, raw string, p *portal.Portal) (*db.GitRepository, bool, error) {
	path := strings.TrimPrefix(strings.TrimSpace(raw), "/")
	path = strings.TrimSuffix(path, "/")
	wiki := false
	switch {
	case strings.HasSuffix(path, ".wiki.git"):
		path = strings.TrimSuffix(path, ".wiki.git")
		wiki = true
	case strings.HasSuffix(path, ".wiki"):
		path = strings.TrimSuffix(path, ".wiki")
		wiki = true
	default:
		path = strings.TrimSuffix(path, ".git")
	}
	if p != nil {
		path = p.MapName(path)
		if !p.InScope(path) {
			return nil, false, errors.New("repository not found")
		}
	}
	ns, name, ok := strings.Cut(path, "/")
	if !ok || ns == "" || name == "" || strings.Contains(name, "/") {
		return nil, false, errors.New("repository path must be namespace/name")
	}
	repo, _, err := s.manager.Store().ResolveGitRepository(ctx, ns, name)
	if err != nil {
		return nil, false, err
	}
	if repo == nil {
		return nil, false, errors.New("repository not found")
	}
	return repo, wiki, nil
}

// Runs one git command over the channel, returning the exit status
func (s *SSHServer) exec(sconn *ssh.ServerConn, ch ssh.Channel, command string, env map[string]string, remote string, p *portal.Portal) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ip := remote
	if host, _, err := net.SplitHostPort(remote); err == nil {
		ip = host
	}
	fail := func(msg string) int {
		fmt.Fprintf(ch.Stderr(), "fatal: %s\n", msg)
		return 1
	}
	if !s.manager.Enabled(ctx) || !s.manager.res.System(ctx).GetGit().GetSshEnabled() {
		return fail("git over ssh is disabled")
	}
	cmd, err := parseSSHCommand(command)
	if err != nil {
		return fail(err.Error())
	}
	user, err := s.principal(ctx, sconn.Permissions)
	if err != nil {
		return fail(err.Error())
	}
	if p != nil {
		ctx = portal.WithPortal(ctx, p)
	}
	ctx = auth.WithUser(ctx, user)
	repo, wiki, err := s.resolvePath(ctx, cmd.path, p)
	if err != nil {
		return fail(err.Error())
	}
	unit := db.GitUnitCode
	if wiki {
		unit = db.GitUnitWiki
	}
	write := cmd.service == transport.ReceivePackService || (strings.HasPrefix(cmd.service, "git-lfs") && len(cmd.args) > 0 && cmd.args[0] == "upload")
	action := rbac.ActionPull
	if write {
		action = rbac.ActionPush
	}
	if !s.manager.Access().Can(ctx, user, repo, unit, action) {
		if !s.manager.Access().CanSee(ctx, user, repo) {
			return fail("repository not found")
		}
		return fail("permission denied")
	}
	if write {
		if p != nil && !p.AllowPush {
			return fail("portal is read-only")
		}
		if repo.IsArchived {
			return fail("repository is archived")
		}
	}
	if s.manager.Engine().Renaming(repo.ID) {
		return fail("repository is moving, retry shortly")
	}
	release, err := s.manager.Engine().Transfers().Acquire(ctx, 10*time.Second)
	if err != nil {
		return fail("server busy")
	}
	defer release()
	// Writers lock before opening so a rename in flight cannot leave a ghost directory
	var lock *RepoLock
	if cmd.service == transport.ReceivePackService {
		lock = s.manager.Engine().LockRepo(repo.ID)
		defer lock.Unlock()
		current, err := s.manager.Store().GetGitRepositoryByID(ctx, repo.ID)
		if err != nil || current == nil {
			return fail("repository not found")
		}
		repo = current
	}
	st, closer, err := s.manager.Engine().Storer(repo.Namespace, repo.Name, wiki)
	if err != nil {
		return fail("repository unavailable")
	}
	defer closer()
	// A client that stops moving bytes loses the channel and its locks
	watch := &idleWatch{}
	go watch.run(ctx, transferIdleTimeout, func() {
		fmt.Fprintln(ch.Stderr(), "fatal: session idle timeout")
		cancel()
		_ = ch.Close()
	})
	reader := io.NopCloser(&touchReader{r: ch, w: watch})
	if cmd.service == transport.ReceivePackService {
		if budget, over := s.manager.pushBudget(ctx, repo, wiki); budget > 0 {
			reader = &budgetReader{ReadCloser: reader, left: budget, err: over}
		}
	}
	writer := &channelWriter{ch: ch, watch: watch}
	switch cmd.service {
	case transport.UploadPackService:
		err = transport.UploadPack(ctx, st, reader, writer, &transport.UploadPackRequest{GitProtocol: env["GIT_PROTOCOL"]})
		if err == nil {
			_ = s.manager.Store().IncrementGitPullCount(ctx, repo.ID)
			s.manager.Audit(ctx, audit.Event{Action: "Git/fetch", Resource: "git", Outcome: audit.OutcomeSuccess, Detail: repo.Namespace + "/" + repo.Name, SourceIP: ip, Actor: user.Username, ActorID: user.ID})
		}
	case transport.ReceivePackService:
		err = transport.ReceivePack(ctx, st, reader, writer, &transport.ReceivePackRequest{
			GitProtocol: env["GIT_PROTOCOL"], Hooks: s.manager.Hooks(repo, user, HookOptions{Wiki: wiki}, lock),
		})
		lock.Unlock()
	case transport.UploadArchiveService:
		err = transport.UploadArchive(ctx, st, reader, writer, &transport.UploadArchiveRequest{})
	case "git-lfs-authenticate":
		op := "download"
		if len(cmd.args) > 0 {
			op = cmd.args[0]
		}
		out, jerr := s.lfs.AuthenticateJSON(ctx, repo, user, op)
		if jerr != nil {
			return fail(jerr.Error())
		}
		_, _ = ch.Write(append(out, '\n'))
		return 0
	case "git-lfs-transfer":
		op := "download"
		if len(cmd.args) > 0 {
			op = cmd.args[0]
		}
		return s.lfs.ServeSSHTransfer(ctx, ch, repo, user, op)
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
		s.log.Warn("ssh %s %s/%s: %v", cmd.service, repo.Namespace, repo.Name, err)
		return 1
	}
	return 0
}

// Channel writer whose close half closes the write side
type channelWriter struct {
	ch    ssh.Channel
	watch *idleWatch
}

func (c *channelWriter) Write(p []byte) (int, error) {
	n, err := c.ch.Write(p)
	if n > 0 && c.watch != nil {
		c.watch.touch()
	}
	return n, err
}

func (c *channelWriter) Close() error { return c.ch.CloseWrite() }
