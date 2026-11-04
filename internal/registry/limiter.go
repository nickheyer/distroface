package registry

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Checks bearer token and gives back subject
type SubjectVerifier interface {
	VerifyTokenSubject(raw string) (string, error)
}

// GET or HEAD of /v2/<name>/manifests/<reference>, a scan instead of a regexp
func isManifestPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/v2/")
	if !ok {
		return false
	}
	i := strings.LastIndex(rest, "/manifests/")
	if i <= 0 {
		return false
	}
	ref := rest[i+len("/manifests/"):]
	return ref != "" && !strings.Contains(ref, "/")
}

// Throttle manifest pulls per user or per ip
func PullRateLimit(next http.Handler, verifier SubjectVerifier, userLimiter, anonLimiter *admin.Limiter, log *logger.Logger) http.Handler {
	if userLimiter == nil && anonLimiter == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !isManifestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		raw, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !isBearer {
			next.ServeHTTP(w, r)
			return
		}

		limiter := anonLimiter
		key := "ip:" + admin.ClientIP(r.RemoteAddr, r.Header)
		if verifier != nil {
			if sub, err := verifier.VerifyTokenSubject(strings.TrimSpace(raw)); err == nil && sub != "" {
				limiter = userLimiter
				key = "user:" + sub
			}
		}
		// One limits lookup per request, the closure runs dynamic
		// redis work so calling it three times added needless latency
		if limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		limit := limiter.Limit()
		if limit <= 0 {
			next.ServeHTTP(w, r)
			return
		}

		allowed, remaining, resetAt := limiter.Take(key)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

		if !allowed {
			retryAfter := int(time.Until(resetAt).Seconds()) + 1
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"pull rate limit exceeded"}]}`))
			log.Warn("registry: pull rate limit exceeded for %s", key)
			return
		}

		next.ServeHTTP(w, r)
	})
}
