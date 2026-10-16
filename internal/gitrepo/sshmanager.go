package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/crypto/ssh"
)

// SSHManager keeps one listener per primary and portal ssh port
type SSHManager struct {
	server   *SSHServer
	manager  *Manager
	portals  *portal.Resolver
	bindHost string
	log      *logger.Logger

	mu        sync.Mutex
	listeners map[int]net.Listener
	bindErrs  map[int]error
}

func NewSSHManager(manager *Manager, tokens *auth.TokenService, portals *portal.Resolver, bindHost string, authLimiter *admin.Limiter, log *logger.Logger) (*SSHManager, error) {
	keys, err := LoadHostKeys(manager.Engine().KeysDir())
	if err != nil {
		return nil, fmt.Errorf("ssh host keys: %w", err)
	}
	lfs := NewLFS(manager, tokens, log)
	return &SSHManager{
		server:    newSSHServer(manager, lfs, keys, authLimiter, portals, log),
		manager:   manager,
		portals:   portals,
		bindHost:  bindHost,
		log:       log,
		listeners: map[int]net.Listener{},
		bindErrs:  map[int]error{},
	}, nil
}

// Ports that should be listening right now
func (m *SSHManager) desiredPorts(ctx context.Context) map[int]bool {
	desired := map[int]bool{}
	git := m.manager.res.System(ctx).GetGit()
	if !git.GetEnabled() || !git.GetSshEnabled() {
		return desired
	}
	if p := int(git.GetSshPort()); p > 0 {
		desired[p] = true
	}
	if m.portals != nil {
		for _, p := range m.portals.SSHPorts() {
			desired[p] = true
		}
	}
	return desired
}

// Opens and closes listeners to match settings and portals
func (m *SSHManager) Reconcile() {
	ctx := context.Background()
	desired := m.desiredPorts(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	for port, ln := range m.listeners {
		if !desired[port] {
			_ = ln.Close()
			delete(m.listeners, port)
			m.log.Info("git ssh listener on port %d closed", port)
		}
	}
	for port := range m.bindErrs {
		if !desired[port] {
			delete(m.bindErrs, port)
		}
	}
	var errs []error
	for port := range desired {
		if _, ok := m.listeners[port]; ok {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(m.bindHost, strconv.Itoa(port)))
		if err != nil {
			errs = append(errs, err)
			m.bindErrs[port] = err
			m.log.Error("git ssh failed to bind port %d: %v", port, err)
			continue
		}
		delete(m.bindErrs, port)
		m.listeners[port] = ln
		go m.server.Serve(ln, port)
		m.log.Info("git ssh listening on %s:%d", m.bindHost, port)
	}
	if len(errs) > 0 {
		m.log.Warn("git ssh reconcile: %v", errors.Join(errs...))
	}
}

// Closes every listener
func (m *SSHManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port, ln := range m.listeners {
		_ = ln.Close()
		delete(m.listeners, port)
	}
}

// Reports whether a port is served by us or can still be bound
func (m *SSHManager) Probe(port int) (listening bool, err error) {
	if port < 1 || port > 65535 {
		return false, fmt.Errorf("port must be between 1 and 65535")
	}
	m.mu.Lock()
	_, listening = m.listeners[port]
	bindErr := m.bindErrs[port]
	m.mu.Unlock()
	if listening {
		return true, nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(m.bindHost, strconv.Itoa(port)))
	if err != nil {
		return false, describeBindError(port, err)
	}
	_ = ln.Close()
	if bindErr == nil {
		return false, nil
	}
	// A port that failed earlier is free now, retry the real bind
	m.Reconcile()
	m.mu.Lock()
	_, listening = m.listeners[port]
	bindErr = m.bindErrs[port]
	m.mu.Unlock()
	if bindErr != nil {
		return false, describeBindError(port, bindErr)
	}
	return listening, nil
}

// Plain language for the two bind failures operators hit
func describeBindError(port int, err error) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		msg := strings.ToLower(opErr.Err.Error())
		switch {
		case strings.Contains(msg, "permission denied"):
			return fmt.Errorf("port %d needs elevated privileges on this host, pick a port above 1023 or grant the binary cap_net_bind_service", port)
		case strings.Contains(msg, "address already in use"):
			return fmt.Errorf("port %d is already in use by another process on this host", port)
		}
	}
	return fmt.Errorf("port %d cannot be bound: %v", port, err)
}

// Public host key lines for the ui
func (m *SSHManager) HostKeys() []string {
	var out []string
	for _, k := range m.server.hostKeys {
		out = append(out, string(ssh.MarshalAuthorizedKey(k.PublicKey())))
	}
	return out
}
