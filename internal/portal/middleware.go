package portal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Extracts repo name from OCI path, filters OCI keywords
var apiRoutePattern = regexp.MustCompile(`^/v2/(.+)/((?:manifests|tags|referrers|blobs)/.*)$`)

// Extracts the artifact repo name from the v1 data plane path
var artifactRoutePattern = regexp.MustCompile(`^/api/v1/artifacts/([^/]+)/(.+)$`)

// First segment control-plane keywords never namespace rewritten
var artifactReservedRepo = map[string]bool{"repos": true, "search": true, "_ns": true}

const oidcLoginPath = "/api/v1/auth/oidc/login"

// Serves the whole app per portal, resolves the portal into the request
// context, enforces read-only and require-auth on the data planes, rewrites
// repo names, and points 401 challenge realms at the requesting host
func (res *Resolver) Middleware(primaryHost func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Docker bearer challenges must point at the host being asked
		if strings.HasPrefix(r.URL.Path, "/v2/") {
			w = &realmRewriter{ResponseWriter: w, req: r}
		}

		p := res.Resolve(r)
		if p == nil {
			next.ServeHTTP(w, r)
			return
		}
		r = r.WithContext(WithPortal(r.Context(), p))

		// Https enforced portals bounce cleartext to the same address
		if p.TLS && requestScheme(r) != "https" {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusFound)
			return
		}

		// SSO state cookies only work on the primary origin, bounce through it
		if primaryHostname := primaryHost(); r.URL.Path == oidcLoginPath && primaryHostname != "" {
			origin := requestScheme(r) + "://" + r.Host
			target := requestScheme(r) + "://" + primaryHostname + oidcLoginPath + "?return_to=" + url.QueryEscape(origin)
			http.Redirect(w, r, target, http.StatusFound)
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			if !res.allowMethod(w, r, p) {
				return
			}
			if match := apiRoutePattern.FindStringSubmatch(r.URL.Path); match != nil {
				name, suffix := match[1], match[2]
				if mapped := p.MapName(name); mapped != name {
					res.log.Debug("path mapping: %s -> %s (host %s, %s %s)", name, mapped, r.Host, r.Method, r.URL.Path)
					r.URL.Path = "/v2/" + mapped + "/" + suffix
					r.URL.RawPath = "" // Repo names have no escapable chars
				}
			}

		case strings.HasPrefix(r.URL.Path, "/api/v1/artifacts"):
			if !res.allowMethod(w, r, p) {
				return
			}
			if p.RequireAuth && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s://%s/api/v1/auth/login"`, requestScheme(r), r.Host))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// Control plane listings scope to the org
			if r.URL.Path == "/api/v1/artifacts/repos" || r.URL.Path == "/api/v1/artifacts/search" {
				q := r.URL.Query()
				q.Set("namespace", p.OrgName)
				r.URL.RawQuery = q.Encode()
			}
			if match := artifactRoutePattern.FindStringSubmatch(r.URL.Path); match != nil {
				repo, suffix := match[1], match[2]
				if !artifactReservedRepo[repo] {
					if repo == p.OrgName {
						// Org qualified path, inject the marker so the facade splits it
						r.URL.Path = "/api/v1/artifacts/_ns/" + p.OrgName + "/" + suffix
						r.URL.RawPath = ""
					} else if mapped := p.MapName(repo); mapped != repo {
						res.log.Debug("artifact path mapping: %s -> %s (host %s, %s %s)", repo, mapped, r.Host, r.Method, r.URL.Path)
						r.URL.Path = "/api/v1/artifacts/_ns/" + mapped + "/" + suffix
						r.URL.RawPath = ""
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Blocks writes through read-only portals
func (res *Resolver) allowMethod(w http.ResponseWriter, r *http.Request, p *Portal) bool {
	if p.AllowPush || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": "DENIED", "message": "portal is read-only"}},
	})
	return false
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
