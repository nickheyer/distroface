package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Compiled, request-ready portal
type portalEntry struct {
	portal  *storage.RegistryPortal
	orgName string
	rules   *PathMapper // Compiled custom rules, may be nil
}

// Resolves a requested repo name to its canonical path, custom rules run
// first (results must land in the org namespace), then unqualified names
// get the org prefix when map_unqualified is set
func (e *portalEntry) mapName(name string) string {
	if e.rules != nil {
		if mapped := e.rules.MapName(name); mapped != name {
			if strings.HasPrefix(mapped, e.orgName+"/") {
				return mapped
			}
			return name
		}
	}
	if e.portal.MapUnqualified && !strings.Contains(name, "/") {
		return e.orgName + "/" + name
	}
	return name
}

// Resolves registry requests to per-org portals by listener port and Host
// header, non-portal traffic gets default behavior
type PortalResolver struct {
	store *storage.Store
	log   *logger.Logger

	mu      sync.RWMutex
	entries map[string]*portalEntry // Keyed "port|host", catch-alls use empty host
	loaded  bool
}

func NewPortalResolver(store *storage.Store, log *logger.Logger) *PortalResolver {
	return &PortalResolver{store: store, log: log}
}

// Drops the lookup table, next request rebuilds it
func (pr *PortalResolver) Invalidate() {
	pr.mu.Lock()
	pr.entries = nil
	pr.loaded = false
	pr.mu.Unlock()
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

// Matches dedicated port + host, then the port catch-all, then host-only portals
func (pr *PortalResolver) resolve(r *http.Request) *portalEntry {
	if pr == nil {
		return nil
	}
	host := strings.ToLower(r.Host)
	if bare, _, err := net.SplitHostPort(host); err == nil {
		host = bare
	}
	port := listenerPort(r)

	pr.mu.RLock()
	if !pr.loaded {
		pr.mu.RUnlock()
		pr.mu.Lock()
		if !pr.loaded {
			pr.reloadLocked()
		}
		pr.mu.Unlock()
		pr.mu.RLock()
	}
	defer pr.mu.RUnlock()

	if port > 0 {
		if e, ok := pr.entries[portalKey(port, host)]; ok {
			return e
		}
		if e, ok := pr.entries[portalKey(port, "")]; ok {
			return e
		}
	}
	if e, ok := pr.entries[portalKey(0, host)]; ok {
		return e
	}
	return nil
}

func (pr *PortalResolver) reloadLocked() {
	portals, err := pr.store.ListRegistryPortals(context.Background())
	if err != nil {
		// Leave loaded=false so the next request retries
		pr.log.Error("portal resolver: failed to load portals: %v", err)
		return
	}

	entries := make(map[string]*portalEntry, len(portals))
	for _, p := range portals {
		if !p.Enabled || p.Org == nil {
			continue
		}
		entry := &portalEntry{portal: p, orgName: p.Org.Name}
		if rules, err := ParsePortalRules(p.Rules); err != nil {
			pr.log.Error("portal %s (%s): stored rules invalid, custom rules disabled: %v", p.Name, p.Hostname, err)
		} else if len(rules) > 0 {
			mapper, err := NewPathMapper(rules, pr.log)
			if err != nil {
				pr.log.Error("portal %s (%s): rules failed to compile, custom rules disabled: %v", p.Name, p.Hostname, err)
			} else {
				entry.rules = mapper
			}
		}
		entries[portalKey(p.Port, strings.ToLower(p.Hostname))] = entry
	}
	pr.entries = entries
	pr.loaded = true
}

// Ports needed by enabled portals, used by the proxy manager
func (pr *PortalResolver) DesiredPorts() ([]int, error) {
	portals, err := pr.store.ListRegistryPortals(context.Background())
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
func ParsePortalRules(rulesJSON string) ([]MappingRule, error) {
	if rulesJSON == "" || rulesJSON == "[]" {
		return nil, nil
	}
	var rules []MappingRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// Rewrites repo name when the request belongs to a portal
func (pr *PortalResolver) MapName(r *http.Request, name string) string {
	if e := pr.resolve(r); e != nil {
		return e.mapName(name)
	}
	return name
}

// Check if anon access permitted for the request
func (pr *PortalResolver) AllowAnonymous(r *http.Request) bool {
	if e := pr.resolve(r); e != nil {
		return !e.portal.RequireAuth
	}
	return true
}

// Check if push permitted for the request
func (pr *PortalResolver) AllowPush(r *http.Request) bool {
	if e := pr.resolve(r); e != nil {
		return e.portal.AllowPush
	}
	return true
}

// Rewrites repo names on portal traffic, enforces read-only portals, points
// 401 challenge realms at the requesting host
func (pr *PortalResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = &realmRewriter{ResponseWriter: w, req: r}

		entry := pr.resolve(r)
		if match := apiRoutePattern.FindStringSubmatch(r.URL.Path); match != nil && entry != nil {
			if !entry.portal.AllowPush && r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeRegistryDenied(w, "portal is read-only")
				return
			}

			name, suffix := match[1], match[2]
			mapped := entry.mapName(name)
			if mapped != name {
				pr.log.Debug("path mapping: %s -> %s (host %s, %s %s)", name, mapped, r.Host, r.Method, r.URL.Path)
				r.URL.Path = "/v2/" + mapped + "/" + suffix
				r.URL.RawPath = "" // Repo names have no escapable chars
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeRegistryDenied(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": "DENIED", "message": message}},
	})
}

var realmPattern = regexp.MustCompile(`realm="[^"]*"`)

// Rewrites bearer challenge to the scheme+host of the incoming request, so every hostname completes token flow
type realmRewriter struct {
	http.ResponseWriter
	req         *http.Request
	wroteHeader bool
}

func (rw *realmRewriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
		if code == http.StatusUnauthorized {
			if v := rw.Header().Get("Www-Authenticate"); strings.HasPrefix(v, "Bearer ") {
				realm := fmt.Sprintf(`realm="%s://%s/auth/token"`, requestScheme(rw.req), rw.req.Host)
				rw.Header().Set("Www-Authenticate", realmPattern.ReplaceAllString(v, realm))
			}
		}
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *realmRewriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func requestScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.TrimSpace(strings.SplitN(proto, ",", 2)[0])
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
