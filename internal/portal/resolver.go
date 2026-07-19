package portal

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Resolves requests to per-org portals by listener port and Host header
type Resolver struct {
	store *stores.Store
	log   *logger.Logger

	// Gates https enforcement on actual cert availability
	certReady func(ctx context.Context, p *Portal, host string) bool

	mu      sync.RWMutex
	entries map[string]*Portal // Keyed "port|host", catch-alls use empty host
	hosts   map[string]bool
	loaded  bool
}

func NewResolver(store *stores.Store, log *logger.Logger) *Resolver {
	return &Resolver{store: store, log: log}
}

// SetCertReady installs the readiness check used before tls redirects
func (res *Resolver) SetCertReady(fn func(ctx context.Context, p *Portal, host string) bool) {
	res.certReady = fn
}

// Enforcement waits until the portal can actually serve tls
func (res *Resolver) tlsEnforceable(r *http.Request, p *Portal) bool {
	if res.certReady == nil {
		return true
	}
	return res.certReady(r.Context(), p, r.Host)
}

// Drops the lookup table, next request rebuilds it
func (res *Resolver) Invalidate() {
	res.mu.Lock()
	res.entries = nil
	res.hosts = nil
	res.loaded = false
	res.mu.Unlock()
}

func portalKey(port int, host string) string {
	return strconv.Itoa(port) + "|" + host
}

// Port of the listener that accepted the request, 0 when unknown
func listenerPort(r *http.Request) int {
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, portStr, err := net.SplitHostPort(addr.String()); err == nil {
			if port, err := strconv.Atoi(portStr); err == nil {
				return port
			}
		}
	}
	return 0
}

func bareHost(host string) string {
	host = strings.ToLower(host)
	if bare, _, err := net.SplitHostPort(host); err == nil {
		return bare
	}
	return host
}

func (res *Resolver) ensureLoaded() {
	res.mu.RLock()
	loaded := res.loaded
	res.mu.RUnlock()
	if loaded {
		return
	}
	res.mu.Lock()
	if !res.loaded {
		res.reloadLocked()
	}
	res.mu.Unlock()
}

// Matches dedicated port + host, then the port catch-all, then host-only portals
func (res *Resolver) resolveHostPort(host string, port int) *Portal {
	res.ensureLoaded()
	res.mu.RLock()
	defer res.mu.RUnlock()

	if port > 0 {
		if p, ok := res.entries[portalKey(port, host)]; ok {
			return p
		}
		if p, ok := res.entries[portalKey(port, "")]; ok {
			return p
		}
	}
	if p, ok := res.entries[portalKey(0, host)]; ok {
		return p
	}
	return nil
}

func (res *Resolver) Resolve(r *http.Request) *Portal {
	if res == nil {
		return nil
	}
	return res.resolveHostPort(bareHost(r.Host), listenerPort(r))
}

// The one hostname precedence implementation, shared with the tls layer
func (res *Resolver) ResolveForTLS(host string, port int) (certs.TLSPortal, bool) {
	p := res.resolveHostPort(bareHost(host), port)
	if p == nil {
		return certs.TLSPortal{}, false
	}
	return certs.TLSPortal{ID: p.ID, OrgID: p.OrgID, Source: p.CertSource, CatchAll: p.CatchAll}, true
}

func (res *Resolver) reloadLocked() {
	portals, err := res.store.ListRegistryPortals(context.Background())
	if err != nil {
		// Leave loaded=false so the next request retries
		res.log.Error("portal resolver: failed to load portals: %v", err)
		return
	}

	entries := make(map[string]*Portal, len(portals))
	hosts := map[string]bool{}
	for _, p := range portals {
		if !p.Enabled || p.Org == nil {
			continue
		}
		entry := &Portal{
			ID:             p.ID,
			Name:           p.Name,
			OrgID:          p.OrgID,
			OrgName:        p.Org.Name,
			OrgDisplayName: p.Org.DisplayName,
			MapUnqualified: p.MapUnqualified,
			AllowPush:      p.AllowPush,
			RequireAuth:    p.RequireAuth,
			TLS:            p.TLS,
			CertSource:     p.CertSource,
			CatchAll:       p.Hostname == "",
		}
		if rules, err := ParseRules(p.Rules); err != nil {
			res.log.Error("portal %s (%s): stored rules invalid, custom rules disabled: %v", p.Name, p.Hostname, err)
		} else if len(rules) > 0 {
			mapper, err := newPathMapper(rules, res.log)
			if err != nil {
				res.log.Error("portal %s (%s): rules failed to compile, custom rules disabled: %v", p.Name, p.Hostname, err)
			} else {
				entry.rules = mapper
			}
		}
		host := strings.ToLower(p.Hostname)
		entries[portalKey(p.Port, host)] = entry
		if host != "" {
			hosts[host] = true
		}
	}
	res.entries = entries
	res.hosts = hosts
	res.loaded = true
}

// Ports needed by enabled portals, used by the proxy manager
func (res *Resolver) DesiredPorts() ([]int, error) {
	portals, err := res.store.ListRegistryPortals(context.Background())
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var ports []int
	for _, p := range portals {
		if p.Enabled && p.Port > 0 && !seen[p.Port] {
			seen[p.Port] = true
			ports = append(ports, p.Port)
		}
	}
	return ports, nil
}

// Decodes a portal's stored rules JSON
func ParseRules(rulesJSON string) ([]*v1.PortalRule, error) {
	if rulesJSON == "" || rulesJSON == "[]" {
		return nil, nil
	}
	var rules []*v1.PortalRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// ── auth.RegistryAccessPolicy ────────────────────────────────────────────

// Rewrites repo name when the request belongs to a portal
func (res *Resolver) MapName(r *http.Request, name string) string {
	if p := res.Resolve(r); p != nil {
		return p.MapName(name)
	}
	return name
}

// Check if anon access permitted for the request
func (res *Resolver) AllowAnonymous(r *http.Request) bool {
	if p := res.Resolve(r); p != nil {
		return !p.RequireAuth
	}
	return true
}

// Check if push permitted for the request
func (res *Resolver) AllowPush(r *http.Request) bool {
	if p := res.Resolve(r); p != nil {
		return p.AllowPush
	}
	return true
}

// Check if the host belongs to an enabled hostname portal
func (res *Resolver) IsPortalHost(host string) bool {
	host = bareHost(host)
	if host == "" {
		return false
	}
	res.ensureLoaded()
	res.mu.RLock()
	defer res.mu.RUnlock()
	return res.hosts[host]
}
