package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Compiled portal proxy instance
type portalEntry struct {
	portal  *storage.RegistryPortal
	orgName string
	rules   *PathMapper // Compiled custom rules, may be nil
}

// Resolve repo name to canon path, then runs rules to map to org's base namespace
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

// Resolves requests to org portals and applies mapping+access rules
type PortalResolver struct {
	store *storage.Store
	log   *logger.Logger

	mu     sync.RWMutex
	byHost map[string]*portalEntry
	loaded bool
}

func NewPortalResolver(store *storage.Store, log *logger.Logger) *PortalResolver {
	return &PortalResolver{store: store, log: log}
}

// Drops the host lookup table, next request rebuilds it
func (pr *PortalResolver) Invalidate() {
	pr.mu.Lock()
	pr.byHost = nil
	pr.loaded = false
	pr.mu.Unlock()
}

func (pr *PortalResolver) resolve(host string) *portalEntry {
	if pr == nil {
		return nil
	}
	host = strings.ToLower(host)

	pr.mu.RLock()
	if pr.loaded {
		e := pr.lookupLocked(host)
		pr.mu.RUnlock()
		return e
	}
	pr.mu.RUnlock()

	pr.mu.Lock()
	defer pr.mu.Unlock()
	if !pr.loaded {
		pr.reloadLocked()
	}
	return pr.lookupLocked(host)
}

// Matches the exact host first, then the host without its port
func (pr *PortalResolver) lookupLocked(host string) *portalEntry {
	if e, ok := pr.byHost[host]; ok {
		return e
	}
	if bare, _, err := net.SplitHostPort(host); err == nil {
		if e, ok := pr.byHost[bare]; ok {
			return e
		}
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

	byHost := make(map[string]*portalEntry, len(portals))
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
		byHost[strings.ToLower(p.Hostname)] = entry
	}
	pr.byHost = byHost
	pr.loaded = true
}

// Decodes a portals stored rules JSON
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

// Rewrites repo name for portal hosts, other hosts pass through
func (pr *PortalResolver) MapName(host, name string) string {
	if e := pr.resolve(host); e != nil {
		return e.mapName(name)
	}
	return name
}

// Check if anon access permitted on the host
func (pr *PortalResolver) AllowAnonymous(host string) bool {
	if e := pr.resolve(host); e != nil {
		return !e.portal.RequireAuth
	}
	return true
}

// Check if push permitted on the host
func (pr *PortalResolver) AllowPush(host string) bool {
	if e := pr.resolve(host); e != nil {
		return e.portal.AllowPush
	}
	return true
}

// Rewrites repo names on portal hosts, enforces read-only portals, points 401 challenge at requesting host
func (pr *PortalResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = &realmRewriter{ResponseWriter: w, req: r}

		entry := pr.resolve(r.Host)
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
