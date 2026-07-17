package portal

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nickheyer/distroface/pkg/logger"
)

// Opens and closes portal listeners to match the enabled portals
type Manager struct {
	resolver *Resolver
	bindHost string
	log      *logger.Logger

	mu        sync.Mutex
	handler   http.Handler
	tlsConfig *tls.Config
	timeouts  ServerTimeouts
	servers   map[int]*http.Server
}

// Applied to all
type ServerTimeouts struct {
	ReadHeader time.Duration
	Idle       time.Duration
}

func NewManager(resolver *Resolver, bindHost string, log *logger.Logger) *Manager {
	return &Manager{
		resolver: resolver,
		bindHost: bindHost,
		log:      log,
		servers:  map[int]*http.Server{},
	}
}

// Handler must be set before the first Reconcile
func (m *Manager) SetHandler(handler http.Handler) {
	m.mu.Lock()
	m.handler = handler
	m.mu.Unlock()
}

// Portal listeners terminate tls when set, sni picks per host certs
func (m *Manager) SetTLSConfig(cfg *tls.Config) {
	m.mu.Lock()
	m.tlsConfig = cfg
	m.mu.Unlock()
}

// Timeouts must be set before the first Reconcile
func (m *Manager) SetTimeouts(t ServerTimeouts) {
	m.mu.Lock()
	m.timeouts = t
	m.mu.Unlock()
}

// Syncs running listeners with the enabled portals, called at startup and after every portal write
func (m *Manager) Reconcile(ctx context.Context) error {
	m.resolver.Invalidate()

	ports, err := m.resolver.DesiredPorts()
	if err != nil {
		return fmt.Errorf("loading portal ports: %w", err)
	}
	desired := map[int]bool{}
	for _, p := range ports {
		desired[p] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for port, srv := range m.servers {
		if !desired[port] {
			_ = srv.Close()
			delete(m.servers, port)
			m.log.Info("portal proxy on port %d closed", port)
		}
	}

	var errs []error
	for port := range desired {
		if _, ok := m.servers[port]; ok {
			continue
		}
		if m.handler == nil {
			errs = append(errs, fmt.Errorf("port %d: no handler set", port))
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(m.bindHost, strconv.Itoa(port)))
		if err != nil {
			errs = append(errs, fmt.Errorf("port %d: %w", port, err))
			m.log.Error("portal proxy failed to bind port %d: %v", port, err)
			continue
		}
		if m.tlsConfig != nil {
			ln = tls.NewListener(ln, m.tlsConfig)
		}
		srv := &http.Server{
			Handler:           m.handler,
			ReadHeaderTimeout: m.timeouts.ReadHeader,
			IdleTimeout:       m.timeouts.Idle,
		}
		m.servers[port] = srv
		go func(port int) {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				m.log.Error("portal proxy on port %d stopped: %v", port, err)
			}
		}(port)
		m.log.Info("portal proxy listening on %s:%d", m.bindHost, port)
	}
	return errors.Join(errs...)
}

// Probes that a port can be bound, used to validate before storing a portal
func (m *Manager) ProbePort(port int) error {
	m.mu.Lock()
	if _, ok := m.servers[port]; ok {
		m.mu.Unlock()
		return nil // Already ours, shareable
	}
	m.mu.Unlock()

	ln, err := net.Listen("tcp", net.JoinHostPort(m.bindHost, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return ln.Close()
}

// Closes every portal listener
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port, srv := range m.servers {
		_ = srv.Close()
		delete(m.servers, port)
	}
}
